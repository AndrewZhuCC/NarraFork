/**
 * The block backfill (docs/BLOCK_STORAGE-DESIGN.md §10.3): legacy envelopes gain block
 * rows that assemble back to their stored content, without touching anything the model
 * reads or any revision that would invalidate its caches.
 */

import { afterAll, afterEach, beforeEach, describe, expect, mock, test } from "bun:test";
import { assembleContent } from "@shared/narrator-blocks/storage";
import { cleanDb, getTestDb } from "../../../tests/setup";

const { db, sqlite } = getTestDb();
const realDbModule = { ...(await import("../../db")) };
mock.module("../../db", () => ({ ...realDbModule, db, sqlite }));

// narrator-service must be imported before narrator-persistence (pre-existing cycle).
await import("../narrator-service");
const { narratorStore } = await import("../narrator-store");
const { settings } = await import("../../lib/settings");
const blockSync = await import("../narrator-block-sync");
const backfill = await import("../narrator-block-backfill");
const { runBlockBackfillSlice, runBlockVerifySlice } = await import(
	"../../lib/db-worker/block-backfill-query"
);

const NOW = "2026-10-01T00:00:00.000Z";

const { ensureFts } = await import("../../db/fts");
ensureFts(sqlite, { wasClean: true });

/** Read slices on the test connection, as the inline fallback does. */
const inline = {
	backfill: async (params: Parameters<typeof runBlockBackfillSlice>[1]) =>
		runBlockBackfillSlice(sqlite, params),
	verify: async (params: Parameters<typeof runBlockVerifySlice>[1]) =>
		runBlockVerifySlice(sqlite, params),
};

async function drain(): Promise<number> {
	let slices = 0;
	while (await backfill.runBlockJobSlice(inline)) slices++;
	return slices;
}

/** Write with block storage off: what a database from before P1 holds. */
async function legacy<T>(write: () => Promise<T>): Promise<T> {
	settings.narratorBlocks.write = false;
	try {
		return await write();
	} finally {
		settings.narratorBlocks.write = true;
	}
}

function envelope(messageId: string) {
	return sqlite
		.prepare(
			`SELECT body_format AS bodyFormat, body_revision AS bodyRevision, body_bytes AS bodyBytes,
				content_json AS contentJson
			 FROM narrator_messages WHERE id = ?`,
		)
		.get(messageId) as {
		bodyFormat: number;
		bodyRevision: number;
		bodyBytes: number;
		contentJson: string;
	};
}

function assembled(messageId: string): unknown[] {
	const rows = sqlite
		.prepare("SELECT payload, state FROM narrator_blocks WHERE message_id = ? ORDER BY ordinal")
		.all(messageId) as Array<{ payload: string; state: string | null }>;
	return assembleContent(
		rows.map((row) => ({
			payload: JSON.parse(row.payload),
			state: row.state ? JSON.parse(row.state) : null,
		})),
	);
}

const historyRevision = () =>
	(
		sqlite.prepare("SELECT model_history_revision AS n FROM narrators WHERE id = 'n1'").get() as {
			n: number;
		}
	).n;

beforeEach(() => {
	cleanDb(sqlite);
	sqlite.run("INSERT INTO narrators (id, title, created_at, updated_at) VALUES ('n1', 'N', ?, ?)", [
		NOW,
		NOW,
	]);
	sqlite.run("DELETE FROM narrator_block_backfill");
	sqlite.run("DELETE FROM narrator_block_diagnostics");
});
afterEach(() => {
	settings.narratorBlocks.write = true;
});
afterAll(() => {
	blockSync.stopBlockSyncSweep();
	mock.module("../../db", () => realDbModule);
	sqlite.close();
});

describe("block backfill", () => {
	test("legacy envelopes gain rows equal to their content, and nothing the model reads moves", async () => {
		const { input, step, toolCallId } = await legacy(async () => {
			const input = await narratorStore.appendInput("n1", "list the files");
			const step = await narratorStore.openStep("n1", { uuid: "u", session_id: "s" });
			await narratorStore.writeStepItem(step.id, "n1", {
				type: "reasoning",
				id: "r1",
				text: "thinking",
				completed: true,
				outputIndex: 0,
			});
			const toolCallId = await narratorStore.writeStepItem(step.id, "n1", {
				type: "tool_use",
				id: "c1",
				name: "Bash",
				input: { command: "ls" },
				outputIndex: 1,
			});
			await narratorStore.settleTool(
				"c1",
				{ output: "a.txt", status: "success" },
				step.id,
				toolCallId,
			);
			return { input, step, toolCallId };
		});
		expect(envelope(input.id).bodyFormat).toBe(1);
		const revisions = [envelope(input.id).bodyRevision, envelope(step.id).bodyRevision];
		const history = historyRevision();

		await drain();

		for (const id of [input.id, step.id]) {
			expect(envelope(id).bodyFormat).toBe(2);
			expect(assembled(id)).toEqual(JSON.parse(envelope(id).contentJson));
			expect(envelope(id).bodyBytes).toBeGreaterThan(0);
		}
		// Storage changed shape only: no revision moved, so no cache was invalidated.
		expect([envelope(input.id).bodyRevision, envelope(step.id).bodyRevision]).toEqual(revisions);
		expect(historyRevision()).toBe(history);
		const tool = sqlite
			.prepare("SELECT id, phase FROM narrator_blocks WHERE message_id = ? AND kind = 'tool'")
			.get(step.id) as { id: string; phase: string };
		expect(tool.phase).toBe("done");
		expect(
			sqlite
				.prepare("SELECT block_id AS blockId FROM narrator_tool_calls WHERE id = ?")
				.get(toolCallId ?? ""),
		).toEqual({ blockId: tool.id });
		// The block search index covers the converted rows.
		expect(
			sqlite
				.prepare("SELECT count(*) AS n FROM narrator_blocks_fts WHERE narrator_blocks_fts MATCH ?")
				.get('"list the files"'),
		).toEqual({ n: 1 });
		expect(sqlite.prepare("SELECT count(*) AS n FROM narrator_block_sync_queue").get()).toEqual({
			n: 0,
		});
		expect(backfill.blockBackfillStatus().convert).toMatchObject({ complete: true, processed: 2 });
		// Running again finds nothing to do.
		expect(await backfill.runBlockJobSlice(inline)).toBe(false);
	});

	test("an envelope that changed after the worker read it is converted from what it holds now", async () => {
		const input = await legacy(() => narratorStore.appendInput("n1", "before"));
		const stale = await inline.backfill({
			kind: "blockBackfillSlice",
			afterRowid: 0,
			maxEnvelopes: 100,
			maxSourceBytes: 1 << 22,
			budgetMs: 1000,
		});
		sqlite.run("UPDATE narrator_messages SET content_json = ?, content_text = ? WHERE id = ?", [
			JSON.stringify([{ type: "text", text: "after" }]),
			"after",
			input.id,
		]);
		await backfill.runBlockJobSlice({ ...inline, backfill: async () => stale });
		expect(envelope(input.id).bodyFormat).toBe(2);
		expect(assembled(input.id)).toEqual([{ type: "text", text: "after" }]);
	});

	test("unreadable content is recorded and left legacy; the job moves past it", async () => {
		sqlite.run(
			`INSERT INTO narrator_messages (id, narrator_id, role, content_json, created_at)
			 VALUES ('odd', 'n1', 'user', '"not an array"', ?)`,
			[NOW],
		);
		await drain();
		expect(envelope("odd").bodyFormat).toBe(1);
		expect(
			sqlite.prepare("SELECT kind FROM narrator_block_diagnostics WHERE message_id = 'odd'").get(),
		).toEqual({ kind: "undecodable" });
		expect(backfill.blockBackfillStatus().convert?.complete).toBe(true);
	});

	test("verify records and repairs block rows that drifted from their envelope", async () => {
		const input = await narratorStore.appendInput("n1", "dual-written");
		sqlite.run("UPDATE narrator_blocks SET payload = ? WHERE message_id = ?", [
			JSON.stringify({ raw: [{ type: "text", text: "drifted" }] }),
			input.id,
		]);
		await drain();
		expect(
			sqlite
				.prepare("SELECT kind FROM narrator_block_diagnostics WHERE message_id = ?")
				.get(input.id),
		).toEqual({ kind: "mismatch" });
		expect(assembled(input.id)).toEqual(JSON.parse(envelope(input.id).contentJson));
		expect(backfill.blockBackfillStatus().verify?.complete).toBe(true);
	});

	test("a paused job runs no slices until resumed", async () => {
		const input = await legacy(() => narratorStore.appendInput("n1", "waiting"));
		backfill.setBlockBackfillPaused(true);
		expect(await backfill.runBlockJobSlice(inline)).toBe(false);
		expect(envelope(input.id).bodyFormat).toBe(1);
		sqlite.run("UPDATE narrator_block_backfill SET paused = 0");
		await drain();
		expect(envelope(input.id).bodyFormat).toBe(2);
	});

	test("slices stop at the envelope limit and resume from the cursor", async () => {
		const ids: string[] = [];
		await legacy(async () => {
			for (let index = 0; index < 7; index++)
				ids.push((await narratorStore.appendInput("n1", `message ${index}`)).id);
		});
		const first = await inline.backfill({
			kind: "blockBackfillSlice",
			afterRowid: 0,
			maxEnvelopes: 3,
			maxSourceBytes: 1 << 22,
			budgetMs: 1000,
		});
		expect(first.envelopes.map((planned) => planned.id)).toEqual(ids.slice(0, 3));
		expect(first.done).toBe(false);
		const rest = await inline.backfill({
			kind: "blockBackfillSlice",
			afterRowid: first.lastRowid,
			maxEnvelopes: 100,
			maxSourceBytes: 1 << 22,
			budgetMs: 1000,
		});
		expect(rest.envelopes.map((planned) => planned.id)).toEqual(ids.slice(3));
		expect(rest.done).toBe(true);
	});
});
