import { Database } from "bun:sqlite";
import { afterEach, beforeEach, expect, mock, test } from "bun:test";
import { postgresPlaceholders } from "../context-composition-history";

mock.module("../../db", () => ({
	activeDatabaseBackend: "sqlite",
	sqlite: null,
	db: null,
	postgresRuntime: null,
}));
const { createContextCharacterService } = await import("../narrator-context-composition");

let database: Database;
let service: ReturnType<typeof createContextCharacterService>;

beforeEach(() => {
	database = new Database(":memory:");
	database.run(`CREATE TABLE narrators (
		id TEXT PRIMARY KEY, type TEXT DEFAULT 'primary', variant TEXT DEFAULT 'primary',
		parent_narrator_id TEXT, runtime_registration_json TEXT, context_char_revision INTEGER DEFAULT 0, context_system_chars INTEGER DEFAULT 0,
		context_summary_chars INTEGER DEFAULT 0, context_tools_chars INTEGER DEFAULT 0,
		context_usage_snapshot_json TEXT);
	 CREATE TABLE narrator_message_refs (
		narrator_id TEXT, message_id TEXT, seq INTEGER, is_compact INTEGER DEFAULT 0, segment_compact_id TEXT);
	 CREATE INDEX refs_seq ON narrator_message_refs(narrator_id, seq);
	 CREATE TABLE narrator_message_context_stats (
		message_id TEXT PRIMARY KEY, role TEXT, parent_tool_use_id TEXT, segments_json TEXT);
	 CREATE TABLE narrator_tool_calls (
		id TEXT PRIMARY KEY, narrator_id TEXT, message_id TEXT, tool_use_id TEXT,
		execution_attempt INTEGER DEFAULT 0, created_at TEXT DEFAULT '2026-01-01',
		input_chars INTEGER DEFAULT 0, output_chars INTEGER DEFAULT 0);
	 CREATE INDEX tools_message ON narrator_tool_calls(message_id);
	 CREATE TABLE narrator_context_pins (
		generation TEXT PRIMARY KEY, narrator_id TEXT, segments_json TEXT);
	 INSERT INTO narrators(id) VALUES ('n');`);
	service = createContextCharacterService(database);
});
afterEach(() => database.close());

function message(id: string, seq: number, segments: unknown = null, role = "user") {
	database
		.query(
			"INSERT INTO narrator_message_context_stats(message_id,role,segments_json) VALUES (?,?,?)",
		)
		.run(id, role, segments === null ? null : JSON.stringify({ segments }));
	database
		.query("INSERT INTO narrator_message_refs(narrator_id,message_id,seq) VALUES ('n',?,?)")
		.run(id, seq);
}

test("post-compact history is returned in one response", async () => {
	database.run(
		"UPDATE narrators SET context_system_chars=7, context_summary_chars=11, context_tools_chars=13 WHERE id='n'",
	);
	for (let i = 0; i < 300; i++)
		message(`m${i}`, i, [{ category: i % 2 ? "assistant" : "user", chars: 1 }]);
	const result = await service.get("n");
	expect(result.nextCursor).toBeNull();
	expect(result.pending).toBe(false);
	expect(result.segments).toHaveLength(303);
	expect(result.totalChars).toBe(7 + 11 + 13 + 300);
	expect(result.segments.slice(0, 3).map((segment) => segment.category)).toEqual([
		"system",
		"summary",
		"toolDefinition",
	]);
});

test("latest compact boundary drops older messages", async () => {
	message("old", 1, [{ category: "user", chars: 10 }]);
	message("compact", 2, [], "system");
	database.run("UPDATE narrator_message_refs SET is_compact=1 WHERE message_id='compact'");
	message("next", 3, [{ category: "user", chars: 4 }]);
	expect((await service.get("n")).totalChars).toBe(4);
});

const deliversTo = (toolUseId: string) =>
	JSON.stringify({
		version: 1,
		run: { logicalRunId: "r", deliverTo: { narratorId: "caller", toolUseId } },
	});

test("only the session's own turns count: no placement, or placed under its run's destination", async () => {
	message("top", 1, [{ category: "user", chars: 3 }]);
	message("own-turn", 2, [{ category: "assistant", chars: 5 }], "assistant");
	message("nested", 3, [{ category: "assistant", chars: 13 }], "assistant");
	database.run(`UPDATE narrator_message_context_stats SET parent_tool_use_id='dest-tool' WHERE message_id='own-turn';
	 UPDATE narrator_message_context_stats SET parent_tool_use_id='other-tool' WHERE message_id='nested';`);
	// No registered destination: placed rows are nested, not own turns.
	expect((await service.get("n")).totalChars).toBe(3);
	// Registering a destination (no parent, ordinary type) makes rows under it own turns.
	database
		.query("UPDATE narrators SET runtime_registration_json=? WHERE id='n'")
		.run(deliversTo("dest-tool"));
	expect((await service.get("n")).totalChars).toBe(8);
	// A delegated shape adds no placement: it counts exactly the rows the ordinary
	// session does, before and after a destination is registered.
	database.run(`INSERT INTO narrators(id,type,variant,parent_narrator_id) VALUES ('child','subagent','subagent:general','caller');
	 INSERT INTO narrator_message_refs SELECT 'child',message_id,seq,is_compact,segment_compact_id FROM narrator_message_refs WHERE narrator_id='n';`);
	expect((await service.get("child")).totalChars).toBe(3);
	database
		.query("UPDATE narrators SET runtime_registration_json=? WHERE id='child'")
		.run(deliversTo("dest-tool"));
	expect((await service.get("child")).totalChars).toBe(8);
});

test("a request pin stays put after later messages arrive", async () => {
	message("question", 1, [{ category: "user", chars: 8 }]);
	const pin = await service.freezeForRequest(
		"n",
		{ totalChars: 100, systemChars: 3, toolsChars: 2 },
		"request-1",
		"2026-10-05T00:00:00Z",
	);
	expect(pin?.totalChars).toBe(13);
	message("later", 2, [{ category: "assistant", chars: 50 }]);
	const shown = await service.get("n");
	expect(shown.generation).toBe(pin?.generation ?? null);
	expect(shown.totalChars).toBe(13);
	expect(shown.segments.some((segment) => segment.chars === 50)).toBe(false);
});

test("a shared write during pinning leaves the request unclassified", async () => {
	message("shared", 1, [{ category: "user", chars: 5 }]);
	const pending = service.freezeForRequest(
		"n",
		{ totalChars: 100, systemChars: 1, toolsChars: 1 },
		"request-2",
		"2026-10-05T00:00:00Z",
	);
	const { queueContextCharacterRefresh } = await import("../../lib/context-characters");
	queueContextCharacterRefresh("n", "shared");
	expect(await pending).toBeNull();
	expect((await service.get("n")).usage?.composition).toBeNull();
	expect((await service.get("n")).totalChars).toBe(5);
});

test("postgres placeholders stay positional", () => {
	expect(postgresPlaceholders("SELECT * FROM t WHERE a = ? AND (b, c) > (?, ?) LIMIT ?")).toBe(
		"SELECT * FROM t WHERE a = $1 AND (b, c) > ($2, $3) LIMIT $4",
	);
});

test("provider classification is pinned without reading history", async () => {
	message("ignored", 1, [{ category: "user", chars: 99 }]);
	const pin = await service.freezeForRequest(
		"n",
		{
			totalChars: 30,
			systemChars: 12,
			toolsChars: 4,
			compositionSegments: [
				{ category: "system", chars: 12 },
				{ category: "toolDefinition", chars: 4 },
				{ category: "user", chars: 14 },
			],
		},
		"request-3",
		"2026-10-05T00:00:00Z",
		5,
	);
	expect(pin?.totals.find((segment) => segment.category === "summary")?.chars).toBe(5);
	expect(pin?.totals.find((segment) => segment.category === "system")?.chars).toBe(7);
	expect(pin?.totals.find((segment) => segment.category === "user")?.chars).toBe(14);
	expect((await service.get("n")).totalChars).toBe(30);
});
