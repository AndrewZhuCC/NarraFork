import { Database } from "bun:sqlite";
import { describe, expect, test } from "bun:test";
import { cpSync, existsSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { sqlite } from "@server/db";
import { testEnvironment } from "./preload";
import { seedSqliteTestDatabase, sqliteTemplateKey, sqliteTemplatePath } from "./sqlite-template";

const REPO_ROOT = resolve(import.meta.dir, "..");

/** A minimal tree with the same layout the key reads, copied from the real sources. */
function fixtureRoot(): string {
	const root = mkdtempSync(join(tmpdir(), "nf-template-key-"));
	cpSync(
		join(REPO_ROOT, "drizzle", "meta", "_journal.json"),
		join(root, "drizzle/meta/_journal.json"),
		{ recursive: true },
	);
	writeFileSync(join(root, "drizzle", "0000_a.sql"), "CREATE TABLE a (id TEXT);");
	for (const dir of ["server/db", "server/db/backend", "tests"])
		mkdirSync(join(root, dir), { recursive: true });
	writeFileSync(join(root, "server/db/schema.ts"), "export const a = 1;");
	writeFileSync(join(root, "server/db/backend/sqlite-lifecycle.ts"), "export const b = 1;");
	writeFileSync(join(root, "tests/sqlite-template.ts"), "// recipe v1");
	writeFileSync(join(root, "bun.lock"), "lock v1");
	return root;
}

/** A private scratch area so these cases never read or write the shared template cache. */
function scratch(): { root: string; home: string; template: string } {
	const root = mkdtempSync(join(tmpdir(), "nf-template-seed-"));
	return { root, home: join(root, "home"), template: join(root, "cache", "narrafork-test.db") };
}

describe("pre-migrated SQLite test template", () => {
	test("the database is fully migrated by the real startup, seeded or not", () => {
		const applied = sqlite
			.query<{ n: number }, []>("SELECT count(*) AS n FROM __drizzle_migrations")
			.get();
		const journal = (
			require(join(REPO_ROOT, "drizzle/meta/_journal.json")) as { entries: unknown[] }
		).entries.length;
		expect(applied?.n).toBe(journal);
	});

	test("the preload reports what it did, and a seeded process came from the template", () => {
		// "seeded" when the template is usable; "disabled"/"unavailable" are legitimate fallbacks
		// (opt-out or a failed build), not failures. What must never happen is a silent
		// "seeded" with no template behind it.
		expect(["seeded", "disabled", "unavailable"]).toContain(testEnvironment.sqliteSeed);
		if (process.env.NF_TEST_SQLITE_TEMPLATE === "0")
			expect(testEnvironment.sqliteSeed).toBe("disabled");
		if (testEnvironment.sqliteSeed === "seeded")
			expect(existsSync(sqliteTemplatePath())).toBe(true);
	});

	test("the template file itself carries no clean-shutdown marker", () => {
		// Read the TEMPLATE, not the live connection: startup consumes the marker and resets it to
		// 0, so the live handle would read 0 even if the template were wrong. A marker in the
		// template would move every test database onto the clean-start FTS branch.
		if (testEnvironment.sqliteSeed !== "seeded") return;
		const template = new Database(sqliteTemplatePath(), { readonly: true });
		try {
			const appId = template.query<{ application_id: number }, []>("PRAGMA application_id").get();
			expect(appId?.application_id).toBe(0);
			// Single-file copy must be complete: no WAL left behind to be lost on copy.
			expect(template.query<{ journal_mode: string }, []>("PRAGMA journal_mode").get()).toEqual({
				journal_mode: "delete",
			});
		} finally {
			template.close();
		}
	});

	test("opting out leaves the home untouched and never builds a template", () => {
		const s = scratch();
		try {
			const outcome = seedSqliteTestDatabase(s.home, {
				templatePath: s.template,
				env: { NF_TEST_SQLITE_TEMPLATE: "0" },
			});
			expect(outcome).toBe("disabled");
			expect(existsSync(join(s.home, "narrafork.db"))).toBe(false);
			expect(existsSync(s.template)).toBe(false);
		} finally {
			rmSync(s.root, { recursive: true, force: true });
		}
	});

	test("a failed build falls back cleanly: no seeded file, no published template", () => {
		const s = scratch();
		try {
			const outcome = seedSqliteTestDatabase(s.home, {
				templatePath: s.template,
				buildScript: "process.exit(3);",
				env: {},
			});
			expect(outcome).toBe("unavailable");
			expect(existsSync(join(s.home, "narrafork.db"))).toBe(false);
			expect(existsSync(s.template)).toBe(false);
		} finally {
			rmSync(s.root, { recursive: true, force: true });
		}
	});

	test("an existing database is never overwritten", () => {
		const s = scratch();
		try {
			mkdirSync(s.home, { recursive: true });
			writeFileSync(join(s.home, "narrafork.db"), "existing");
			mkdirSync(join(s.root, "cache"), { recursive: true });
			writeFileSync(s.template, "template");
			expect(seedSqliteTestDatabase(s.home, { templatePath: s.template, env: {} })).toBe("exists");
			expect(require("node:fs").readFileSync(join(s.home, "narrafork.db"), "utf8")).toBe(
				"existing",
			);
		} finally {
			rmSync(s.root, { recursive: true, force: true });
		}
	});

	test("the key changes with migrations, startup sources, the build recipe or the lockfile", () => {
		const root = fixtureRoot();
		try {
			const base = sqliteTemplateKey(root);
			expect(sqliteTemplateKey(root)).toBe(base);
			const seen = new Set([base]);
			const changes: [string, string][] = [
				["drizzle/0000_a.sql", "CREATE TABLE a (id TEXT, b TEXT);"],
				["drizzle/0001_b.sql", "CREATE TABLE b (id TEXT);"],
				["server/db/backend/sqlite-lifecycle.ts", "export const b = 2;"],
				["tests/sqlite-template.ts", "// recipe v2"],
				["bun.lock", "lock v2"],
			];
			for (const [file, content] of changes) {
				writeFileSync(join(root, file), content);
				const key = sqliteTemplateKey(root);
				expect(seen.has(key), `${file} did not change the key`).toBe(false);
				seen.add(key);
			}
			// Test files never shape the production schema and must not churn the template.
			const settled = sqliteTemplateKey(root);
			writeFileSync(join(root, "server/db/schema.test.ts"), "test-only");
			expect(sqliteTemplateKey(root)).toBe(settled);
		} finally {
			rmSync(root, { recursive: true, force: true });
		}
	});
});
