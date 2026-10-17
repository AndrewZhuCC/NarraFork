import { Database } from "bun:sqlite";
import { expect, test } from "bun:test";
import { fileURLToPath } from "node:url";

// Exercise the generated incremental migration only, never the user's database.
test("字符统计迁移保持旧数据不变且缓存初值为0", async () => {
	const db = new Database(":memory:");
	try {
		db.exec("PRAGMA foreign_keys=ON");
		db.exec(`CREATE TABLE narrators (id TEXT PRIMARY KEY);
		CREATE TABLE narrator_messages (id TEXT PRIMARY KEY, content_json TEXT);
		CREATE TABLE narrator_tool_calls (id TEXT PRIMARY KEY, input_json TEXT, output_json TEXT);
		CREATE TABLE user_preferences (id TEXT PRIMARY KEY NOT NULL);
		INSERT INTO narrators VALUES ('old');
		INSERT INTO narrator_messages VALUES ('old','not-json-old-body');
		INSERT INTO narrator_tool_calls VALUES ('old','old-input','old-output');
		INSERT INTO user_preferences VALUES ('old-preference');`);
		let migration: string | undefined;
		const folder = fileURLToPath(new URL("../../../drizzle/", import.meta.url));
		// drizzle-kit chooses a random suffix; never bind the test to that filename.
		for await (const path of new Bun.Glob("*.sql").scan({ cwd: folder, absolute: true })) {
			const file = Bun.file(path);
			if (file.size > 64 * 1024) continue;
			const sql = await file.text();
			if (sql.includes("ALTER TABLE `narrator_messages` ADD `context_chars_json`")) {
				migration = sql;
				break;
			}
		}
		if (!migration)
			throw new Error("Generate the incremental character statistics migration first");
		// A merge migration can also contain unrelated upstream additions.
		const statements = migration.split("--> statement-breakpoint").filter((statement) => {
			if (/^\s*CREATE TABLE `narrator_context_char_pages`/.test(statement)) return true;
			if (/^\s*CREATE (?:UNIQUE )?INDEX `[^`]+` ON `narrator_context_char_pages`/.test(statement))
				return true;
			return /^\s*ALTER TABLE `(narrator_messages|narrators|narrator_tool_calls)` ADD `(context_chars_json|context_summary_chars|context_system_chars|context_tools_chars|context_char_revision|context_char_cache_json|input_chars|output_chars)`/.test(
				statement,
			);
		});
		for (const statement of statements) db.exec(statement);
		expect(
			db
				.query("SELECT content_json AS body, context_chars_json AS chars FROM narrator_messages")
				.get(),
		).toEqual({ body: "not-json-old-body", chars: null });
		expect(
			db
				.query(
					"SELECT input_json AS inputBody, output_json AS outputBody, input_chars AS input, output_chars AS output FROM narrator_tool_calls",
				)
				.get(),
		).toEqual({ inputBody: "old-input", outputBody: "old-output", input: 0, output: 0 });
		expect(
			db
				.query(
					"SELECT context_summary_chars AS summary, context_system_chars AS system, context_tools_chars AS tools, context_char_revision AS revision, context_char_cache_json AS cache FROM narrators",
				)
				.get(),
		).toEqual({ summary: 0, system: 0, tools: 0, revision: 0, cache: null });
		// Character metadata does not alter unrelated preferences or invent removed legacy columns.
		expect(db.query("SELECT id FROM user_preferences").get()).toEqual({ id: "old-preference" });
		expect(
			(db.query("PRAGMA table_info(user_preferences)").all() as { name: string }[]).map(
				(column) => column.name,
			),
		).toEqual(["id"]);
		// The same complete migration must create the page table and enforce its index and FK.
		db.exec(
			"INSERT INTO narrator_context_char_pages VALUES ('page-old', 'old', 'generation', 0, '[]')",
		);
		expect(() =>
			db.exec(
				"INSERT INTO narrator_context_char_pages VALUES ('page-duplicate', 'old', 'generation', 0, '[]')",
			),
		).toThrow();
		expect(() =>
			db.exec(
				"INSERT INTO narrator_context_char_pages VALUES ('page-orphan', 'missing', 'generation', 1, '[]')",
			),
		).toThrow();
		db.exec("DELETE FROM narrators WHERE id = 'old'");
		expect(db.query("SELECT id FROM narrator_context_char_pages").all()).toEqual([]);
	} finally {
		db.close();
	}
});

test("数字窄表迁移回填已有统计并在之后的写入上保持同步", async () => {
	const folder = fileURLToPath(new URL("../../../drizzle/", import.meta.url));
	let migration: string | undefined;
	for await (const path of new Bun.Glob("*.sql").scan({ cwd: folder, absolute: true })) {
		const file = Bun.file(path);
		if (file.size > 64 * 1024) continue;
		const sql = await file.text();
		if (sql.includes("CREATE TABLE `narrator_message_context_stats`")) {
			migration = sql;
			break;
		}
	}
	if (!migration) throw new Error("Generate the numeric context stats migration first");
	expect(migration).toContain("WHERE true\nON CONFLICT(`message_id`) DO NOTHING");
	expect(migration).toContain("INSERT INTO `narrator_message_context_stats`");
	expect(migration).toContain("COALESCE(`context_chars_json`, '{\"segments\":[]}')");
	expect(migration).toContain("CREATE TRIGGER `narrator_message_context_stats_insert`");
	expect(migration).toContain("CREATE TRIGGER `narrator_message_context_stats_update`");
	expect(migration).toContain("DROP TABLE `narrator_context_char_pages`");
	const statements = migration.split("--> statement-breakpoint").filter((statement) => {
		return (
			/^\s*(?:CREATE TABLE|INSERT INTO) `narrator_message_context_stats`/.test(statement) ||
			/^\s*CREATE TRIGGER `narrator_message_context_stats_(?:insert|update)`/.test(statement) ||
			/^\s*CREATE TABLE `narrator_context_pins`/.test(statement) ||
			/^\s*CREATE INDEX `idx_context_pins_narrator`/.test(statement) ||
			/^\s*DROP TABLE `narrator_context_char_pages`/.test(statement)
		);
	});
	expect(statements).toHaveLength(7);
	expect(statements.join("\n")).not.toContain("content_json");
	const db = new Database(":memory:");
	try {
		db.exec(`PRAGMA foreign_keys=ON;
			CREATE TABLE narrators (id TEXT PRIMARY KEY);
			CREATE TABLE narrator_messages (
				id TEXT PRIMARY KEY, role TEXT NOT NULL, parent_tool_use_id TEXT,
				context_chars_json TEXT, content_json TEXT NOT NULL
			);
			CREATE TABLE narrator_context_char_pages (id TEXT PRIMARY KEY);
			INSERT INTO narrators VALUES ('old');
			INSERT INTO narrator_context_char_pages VALUES ('retired-page');`);
		const insert = db.query("INSERT INTO narrator_messages VALUES (?, ?, ?, ?, ?)");
		const oldStats = '{"segments":[{"chars":17}]}';
		insert.run("old", "assistant", "parent-old", oldStats, "not-json-old-body");
		insert.run("empty", "user", null, null, "old-null-stats-body");
		for (const statement of statements) db.exec(statement);
		const stats = db.query(
			"SELECT role, parent_tool_use_id AS parent, segments_json AS segments FROM narrator_message_context_stats WHERE message_id = ?",
		);
		expect(stats.get("old")).toEqual({
			role: "assistant",
			parent: "parent-old",
			segments: oldStats,
		});
		expect(stats.get("empty")).toEqual({ role: "user", parent: null, segments: '{"segments":[]}' });
		expect(
			db.query("SELECT content_json AS body FROM narrator_messages WHERE id = 'old'").get(),
		).toEqual({
			body: "not-json-old-body",
		});
		expect(
			db.query("SELECT name FROM sqlite_master WHERE name = 'narrator_context_char_pages'").get(),
		).toBeNull();
		insert.run("new", "assistant", null, oldStats, "new-body");
		expect(stats.get("new")).toEqual({ role: "assistant", parent: null, segments: oldStats });
		db.query(
			"UPDATE narrator_messages SET role = ?, parent_tool_use_id = ?, context_chars_json = ? WHERE id = ?",
		).run("system", "parent-new", null, "old");
		expect(stats.get("old")).toEqual({
			role: "system",
			parent: "parent-new",
			segments: '{"segments":[]}',
		});
		// UPDATE must also repair a missing projection rather than silently updating zero rows.
		db.exec("DELETE FROM narrator_message_context_stats WHERE message_id = 'old'");
		db.exec("UPDATE narrator_messages SET role = 'user' WHERE id = 'old'");
		expect(stats.get("old")).toEqual({
			role: "user",
			parent: "parent-new",
			segments: '{"segments":[]}',
		});
		db.exec("DELETE FROM narrator_messages WHERE id = 'new'");
		expect(stats.get("new")).toBeNull();
		db.exec("INSERT INTO narrator_context_pins VALUES ('generation', 'old', '[]')");
		expect(() =>
			db.exec("INSERT INTO narrator_context_pins VALUES ('orphan', 'missing', '[]')"),
		).toThrow();
		db.exec("DELETE FROM narrators WHERE id = 'old'");
		expect(db.query("SELECT generation FROM narrator_context_pins").all()).toEqual([]);
		expect(db.query("PRAGMA foreign_key_check").all()).toEqual([]);
	} finally {
		db.close();
	}
	const postgres = await Bun.file(
		fileURLToPath(
			new URL("../../../drizzle-postgres/0022_context_numeric_stats.sql", import.meta.url),
		),
	).text();
	expect(postgres).toContain("CREATE FUNCTION narrator_message_context_stats_sync()");
	expect(postgres).toContain("CREATE TRIGGER narrator_message_context_stats_insert");
	expect(postgres).toContain("CREATE TRIGGER narrator_message_context_stats_update");
	expect(postgres).toContain('COALESCE("context_chars_json", \'{"segments":[]}\')');
});

// Resource migrations bundled alongside these additions are covered by their own integration fixtures.
test("请求快照新增字段保持旧正文不变且初值为null", async () => {
	const db = new Database(":memory:");
	try {
		db.exec("PRAGMA foreign_keys=ON");
		db.exec(`CREATE TABLE narrators (id TEXT PRIMARY KEY, context_summary TEXT);
		CREATE TABLE api_requests (id TEXT PRIMARY KEY, raw_dump_json TEXT);
		INSERT INTO narrators VALUES ('old', 'old-summary');
		INSERT INTO api_requests VALUES ('old', 'not-json-old-dump');`);
		const folder = fileURLToPath(new URL("../../../drizzle/", import.meta.url));
		const additions: string[] = [];
		for await (const path of new Bun.Glob("*.sql").scan({ cwd: folder, absolute: true })) {
			const file = Bun.file(path);
			if (file.size > 256 * 1024) continue;
			const sql = await file.text();
			for (const statement of sql.split("--> statement-breakpoint")) {
				if (
					/^\s*ALTER TABLE `(api_requests|narrators)` ADD `context_usage_snapshot_json` text;\s*$/.test(
						statement,
					)
				)
					additions.push(statement);
			}
		}
		expect(additions).toHaveLength(2);
		for (const statement of additions) db.exec(statement);
		expect(
			db
				.query(
					"SELECT context_summary AS body, context_usage_snapshot_json AS snapshot FROM narrators",
				)
				.get(),
		).toEqual({ body: "old-summary", snapshot: null });
		expect(
			db
				.query(
					"SELECT raw_dump_json AS body, context_usage_snapshot_json AS snapshot FROM api_requests",
				)
				.get(),
		).toEqual({ body: "not-json-old-dump", snapshot: null });
	} finally {
		db.close();
	}
});
