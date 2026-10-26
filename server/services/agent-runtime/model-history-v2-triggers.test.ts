/**
 * Model-history contract v2 (docs/BLOCK_STORAGE-DESIGN.md §4.5), as v3 installs it on the
 * migrated schema.
 *
 * A native envelope's (`body_format = 2`) blocks count once they leave `open`; `state` never
 * counts. Its `content_json` still counts too (the compatibility projection), and flipping the
 * format alone counts for nothing. Block rows of a legacy envelope never count. A tool
 * block's phase follows its newest ledger attempt. v3: a state-only write (it moves
 * `state_revision`) neither counts nor queues a sync (§5.4).
 */

import { beforeEach, describe, expect, test } from "bun:test";
import { cleanDb, getTestDb } from "../../../tests/setup";
import { readSqliteModelHistorySignature } from "./model-history-revision";
import {
	MODEL_HISTORY_REVISION_CONTRACT,
	MODEL_HISTORY_SQLITE_TRIGGERS,
} from "./model-history-revision-sql";

const { sqlite } = getTestDb();
const NOW = "2026-10-22T00:00:00.000Z";

beforeEach(() => {
	cleanDb(sqlite);
	sqlite
		.query(
			"INSERT INTO model_history_revision_contracts (id, installed_version) VALUES ('model-history', ?)",
		)
		.run(MODEL_HISTORY_REVISION_CONTRACT);
	sqlite.run("INSERT INTO narrators (id, created_at, updated_at) VALUES ('n1', ?, ?)", [NOW, NOW]);
	sqlite.run("INSERT INTO narrators (id, created_at, updated_at) VALUES ('fork', ?, ?)", [
		NOW,
		NOW,
	]);
});

function envelope(id: string, bodyFormat: 1 | 2, role = "user") {
	sqlite.run(
		"INSERT INTO narrator_messages (id, narrator_id, role, content_json, content_text, body_format, created_at) VALUES (?, 'n1', ?, '[]', '', ?, ?)",
		[id, role, bodyFormat, NOW],
	);
	sqlite.run(
		"INSERT INTO narrator_message_refs (id, narrator_id, message_id, seq) VALUES (?, 'n1', ?, (SELECT coalesce(max(seq), 0) + 1 FROM narrator_message_refs WHERE narrator_id = 'n1'))",
		[`ref-${id}`, id],
	);
}

function block(
	id: string,
	messageId: string,
	fields: { kind?: string; subtype?: string | null; phase?: string; wireId?: string } = {},
) {
	const kind = fields.kind ?? "output";
	sqlite.run(
		"INSERT INTO narrator_blocks (id, message_id, ordinal, kind, subtype, phase, wire_id, payload, created_at) VALUES (?, ?, (SELECT count(*) FROM narrator_blocks WHERE message_id = ?), ?, ?, ?, ?, '{}', ?)",
		[
			id,
			messageId,
			messageId,
			kind,
			fields.subtype ?? (kind === "tool" ? "Read" : kind === "context" ? "info" : null),
			fields.phase ?? "done",
			fields.wireId ?? (kind === "tool" ? `wire-${id}` : null),
			NOW,
		],
	);
}

function revision(): number {
	return (
		sqlite.prepare("SELECT model_history_revision AS r FROM narrators WHERE id = 'n1'").get() as {
			r: number;
		}
	).r;
}

/** Whether running `action` bumped the holder's model-history revision. */
function bumps(action: () => void): boolean {
	const before = revision();
	action();
	return revision() !== before;
}

function phase(id: string): string {
	return (
		sqlite.prepare("SELECT phase FROM narrator_blocks WHERE id = ?").get(id) as { phase: string }
	).phase;
}

describe("model-history v2 triggers", () => {
	test("the migrated contract is trusted", () => {
		expect(readSqliteModelHistorySignature(sqlite, "n1").trusted).toBe(true);
	});

	test("a native block counts once it leaves open, and state never counts", () => {
		envelope("m1", 2, "assistant");
		expect(bumps(() => block("b1", "m1", { phase: "open" }))).toBe(false);
		expect(
			bumps(() => sqlite.run(`UPDATE narrator_blocks SET payload = '{"x":1}' WHERE id = 'b1'`)),
		).toBe(false);
		expect(
			bumps(() => sqlite.run("UPDATE narrator_blocks SET phase = 'done' WHERE id = 'b1'")),
		).toBe(true);
		expect(
			bumps(() => sqlite.run(`UPDATE narrator_blocks SET state = '{"t":1}' WHERE id = 'b1'`)),
		).toBe(false);
		expect(
			bumps(() => sqlite.run(`UPDATE narrator_blocks SET payload = '{"x":2}' WHERE id = 'b1'`)),
		).toBe(true);
		expect(bumps(() => block("b2", "m1"))).toBe(true);
		expect(bumps(() => sqlite.run("DELETE FROM narrator_blocks WHERE id = 'b2'"))).toBe(true);
		block("b3", "m1", { phase: "open" });
		expect(bumps(() => sqlite.run("DELETE FROM narrator_blocks WHERE id = 'b3'"))).toBe(false);
	});

	test("a native envelope's body and identity both count while readers read content_json", () => {
		envelope("m1", 2);
		expect(
			bumps(() =>
				sqlite.run(
					`UPDATE narrator_messages SET content_json = '[{"type":"text","text":"x"}]', content_text = 'x' WHERE id = 'm1'`,
				),
			),
		).toBe(true);
		expect(
			bumps(() => sqlite.run("UPDATE narrator_messages SET origin = 'system' WHERE id = 'm1'")),
		).toBe(true);
		expect(
			bumps(() =>
				sqlite.run("UPDATE narrator_messages SET author_narrator_id = 'fork' WHERE id = 'm1'"),
			),
		).toBe(true);
	});

	test("renaming a message's author bumps the narrators holding it", () => {
		// A copy is owned by its holder and keeps its author, whose title is the sender label.
		envelope("m1", 2, "assistant");
		sqlite.run("UPDATE narrator_messages SET author_narrator_id = 'fork' WHERE id = 'm1'");
		sqlite.run("INSERT INTO narrators (id, created_at, updated_at) VALUES ('other', ?, ?)", [
			NOW,
			NOW,
		]);
		expect(
			bumps(() => sqlite.run("UPDATE narrators SET title = 'Renamed' WHERE id = 'fork'")),
		).toBe(true);
		expect(
			bumps(() => sqlite.run("UPDATE narrators SET title = 'Unrelated' WHERE id = 'other'")),
		).toBe(false);
	});

	test("a legacy envelope counts its body and ignores block rows", () => {
		envelope("m1", 1);
		expect(
			bumps(() => sqlite.run("UPDATE narrator_messages SET content_text = 'y' WHERE id = 'm1'")),
		).toBe(true);
		// Materialization inserts blocks under the legacy format, then flips it: neither counts.
		expect(bumps(() => block("b1", "m1", { kind: "input" }))).toBe(false);
		expect(
			bumps(() => sqlite.run("UPDATE narrator_messages SET body_format = 2 WHERE id = 'm1'")),
		).toBe(false);
	});

	test("a tool block's phase follows its newest ledger attempt", () => {
		envelope("m1", 2, "assistant");
		block("t1", "m1", { kind: "tool", phase: "done", wireId: "call-1" });
		const attempt = (id: string, n: number) =>
			sqlite.run(
				"INSERT INTO narrator_tool_calls (id, narrator_id, message_id, tool_use_id, tool_name, status, execution_attempt, block_id, created_at) VALUES (?, 'n1', 'm1', 'call-1', 'Read', 'running', ?, 't1', ?)",
				[id, n, NOW],
			);
		expect(bumps(() => attempt("a1", 1))).toBe(true);
		expect(phase("t1")).toBe("open");
		sqlite.run(
			`UPDATE narrator_tool_calls SET status = 'success', output_json = '"ok"' WHERE id = 'a1'`,
		);
		expect(phase("t1")).toBe("done");
		sqlite.run(
			`UPDATE narrator_tool_calls SET status = 'fail', output_json = '{"_text":"bad","_broken":true}' WHERE id = 'a1'`,
		);
		expect(phase("t1")).toBe("failed");
		attempt("a2", 2);
		expect(phase("t1")).toBe("open");
		// The older attempt no longer moves the block.
		sqlite.run(
			`UPDATE narrator_tool_calls SET status = 'success', output_json = '"late"' WHERE id = 'a1'`,
		);
		expect(phase("t1")).toBe("open");
		sqlite.run(
			`UPDATE narrator_tool_calls SET status = 'success', output_json = 'not json' WHERE id = 'a2'`,
		);
		expect(phase("t1")).toBe("done");
	});

	test("internal executions and unbound rows never move a block", () => {
		envelope("m1", 2, "assistant");
		block("t1", "m1", { kind: "tool", phase: "done", wireId: "call-1" });
		sqlite.run(
			"INSERT INTO narrator_tool_calls (id, narrator_id, message_id, tool_use_id, tool_name, status, execution_attempt, block_id, is_internal, created_at) VALUES ('i1', 'n1', 'm1', 'internal_read_1', 'Read', 'running', 1, 't1', 1, ?)",
			[NOW],
		);
		expect(phase("t1")).toBe("done");
	});

	test("block-format envelopes whose stored body or ledger changes are queued for sync", () => {
		const queued = () =>
			(
				sqlite
					.prepare("SELECT message_id FROM narrator_block_sync_queue ORDER BY message_id")
					.all() as Array<{ message_id: string }>
			).map((row) => row.message_id);
		envelope("legacy", 1);
		envelope("native", 2, "assistant");
		expect(queued()).toEqual(["native"]);
		sqlite.run("DELETE FROM narrator_block_sync_queue");
		sqlite.run("UPDATE narrator_messages SET content_text = 'x' WHERE id = 'legacy'");
		expect(queued()).toEqual([]);
		sqlite.run("UPDATE narrator_messages SET content_text = 'x' WHERE id = 'native'");
		expect(queued()).toEqual(["native"]);
		sqlite.run("DELETE FROM narrator_block_sync_queue");
		// Revision bookkeeping is not a stored-body change.
		sqlite.run(
			"UPDATE narrator_messages SET body_revision = body_revision + 1 WHERE id = 'native'",
		);
		expect(queued()).toEqual([]);
		sqlite.run(
			"INSERT INTO narrator_tool_calls (id, narrator_id, message_id, tool_use_id, tool_name, status, execution_attempt, created_at) VALUES ('a1', 'n1', 'native', 'call-1', 'Read', 'running', 1, ?)",
			[NOW],
		);
		expect(queued()).toEqual(["native"]);
		sqlite.run("DELETE FROM narrator_block_sync_queue");
		sqlite.run("UPDATE narrator_tool_calls SET status = 'success' WHERE id = 'a1'");
		expect(queued()).toEqual(["native"]);
		sqlite.run("DELETE FROM narrator_block_sync_queue");
		sqlite.run(
			"INSERT INTO narrator_tool_calls (id, narrator_id, message_id, tool_use_id, tool_name, status, execution_attempt, created_at) VALUES ('a2', 'n1', 'legacy', 'call-2', 'Read', 'running', 1, ?)",
			[NOW],
		);
		expect(queued()).toEqual([]);
		// Flipping a legacy envelope to blocks queues it as well.
		sqlite.run("UPDATE narrator_messages SET body_format = 2 WHERE id = 'legacy'");
		expect(queued()).toEqual(["legacy"]);
	});

	test("a state-only write moves state_revision and neither counts nor queues a sync", () => {
		const queued = () =>
			(sqlite.prepare("SELECT count(*) AS n FROM narrator_block_sync_queue").get() as { n: number })
				.n;
		for (const [id, format] of [
			["native", 2],
			["legacy", 1],
		] as const) {
			envelope(id, format);
			sqlite.run("DELETE FROM narrator_block_sync_queue");
			const body = () =>
				sqlite.prepare("SELECT body_revision AS r FROM narrator_messages WHERE id = ?").get(id) as {
					r: number;
				};
			const before = body().r;
			expect(
				bumps(() =>
					sqlite.run(
						`UPDATE narrator_messages SET content_json = '[{"type":"text","text":"","translatedText":"t"}]', state_revision = state_revision + 1 WHERE id = ?`,
						[id],
					),
				),
			).toBe(false);
			expect(body().r).toBe(before);
			expect(queued()).toBe(0);
			// The same body change without the state counter is a body write again.
			expect(
				bumps(() =>
					sqlite.run(`UPDATE narrator_messages SET content_json = '[]' WHERE id = ?`, [id]),
				),
			).toBe(true);
			expect(queued()).toBe(format === 2 ? 1 : 0);
			// An identity change counts even inside a state write.
			expect(
				bumps(() =>
					sqlite.run(
						"UPDATE narrator_messages SET origin = 'x', state_revision = state_revision + 1 WHERE id = ?",
						[id],
					),
				),
			).toBe(true);
		}
	});

	test("the installed contract is exactly the rendered trigger set", () => {
		const names = (
			sqlite
				.prepare(
					"SELECT name FROM sqlite_schema WHERE type = 'trigger' AND name GLOB 'model_history_v*' ORDER BY name",
				)
				.all() as Array<{ name: string }>
		).map((row) => row.name);
		expect(names).toEqual([...MODEL_HISTORY_SQLITE_TRIGGERS].sort());
	});
});
