/**
 * Rewrites start from an envelope's rows, not its `content_json` (docs/BLOCK_STORAGE-DESIGN.md
 * §10.4, P3a). Each test detaches the column from the body first — it stops holding the
 * body, as it will once P3b stops writing it, and no sync is queued for it — and checks
 * that the operation reads the body from the rows and builds the new rows from what it
 * wrote.
 */

import { afterAll, beforeEach, describe, expect, mock, test } from "bun:test";
import { assembleContent } from "@shared/narrator-blocks/storage";
import { cleanDb, getTestDb } from "../../../tests/setup";

const { db, sqlite } = getTestDb();
const realDbModule = { ...(await import("../../db")) };
mock.module("../../db", () => ({ ...realDbModule, db, sqlite }));

// narrator-service must be imported before narrator-persistence (pre-existing cycle).
await import("../narrator-service");
const { narratorStore } = await import("../narrator-store");
const { narratorPersistence } = await import("../narrator-persistence");
const blockSync = await import("../narrator-block-sync");
const bodies = await import("../narrator-body-writes");

const NOW = "2026-10-01T00:00:00.000Z";

beforeEach(() => {
	cleanDb(sqlite);
	for (const id of ["n1", "n2"])
		sqlite.run("INSERT INTO narrators (id, title, created_at, updated_at) VALUES (?, ?, ?, ?)", [
			id,
			`Narrator ${id}`,
			NOW,
			NOW,
		]);
});
afterAll(() => {
	blockSync.stopBlockSyncSweep();
	mock.module("../../db", () => realDbModule);
	sqlite.close();
});

/** What the envelope's rows assemble to. */
function assembled(messageId: string): unknown[] {
	const rows = sqlite
		.prepare("SELECT payload, state FROM narrator_blocks WHERE message_id = ? ORDER BY ordinal")
		.all(messageId) as Array<{ payload: string; state: string | null }>;
	return assembleContent(
		rows.map((row) => ({
			payload: JSON.parse(row.payload),
			state: row.state === null ? null : JSON.parse(row.state),
		})),
	);
}

const column = (messageId: string) =>
	JSON.parse(
		(
			sqlite
				.prepare("SELECT content_json AS c FROM narrator_messages WHERE id = ?")
				.get(messageId) as {
				c: string;
			}
		).c,
	);

const rowIds = (messageId: string) =>
	(
		sqlite
			.prepare("SELECT id FROM narrator_blocks WHERE message_id = ? ORDER BY ordinal")
			.all(messageId) as Array<{ id: string }>
	).map((row) => row.id);

/**
 * Make `content_json` stop holding the body without queueing a sync (a write that moves
 * `state_revision` is one the triggers leave alone).
 */
function detachColumn(messageId: string): void {
	sqlite.run(
		"UPDATE narrator_messages SET content_json = '[]', state_revision = state_revision + 1 WHERE id = ?",
		[messageId],
	);
	expect(sqlite.prepare("SELECT count(*) AS n FROM narrator_block_sync_queue").get()).toEqual({
		n: 0,
	});
}

async function step() {
	const opened = await narratorStore.openStep("n1", { uuid: "u", session_id: "s" });
	const write = (item: Parameters<typeof narratorStore.writeStepItem>[2]) =>
		narratorStore.writeStepItem(opened.id, "n1", item);
	await write({ type: "reasoning", id: "r1", text: "think", completed: true, outputIndex: 0 });
	await write({ type: "text", id: "t1", text: "Hello.", completed: true, outputIndex: 1 });
	return opened.id;
}

function share(messageId: string) {
	sqlite.run(
		"INSERT INTO narrator_message_refs (id, narrator_id, message_id, seq) VALUES ('r2', 'n2', ?, 0)",
		[messageId],
	);
}

describe("the body layer", () => {
	test("an insert and a rewrite build the rows from the body handed over", () => {
		db.transaction((tx) => {
			bodies.insertEnvelope(tx, {
				id: "m1",
				narratorId: "n1",
				role: "user",
				bodyFormat: 2,
				contentJson: [{ type: "text", text: "first" }],
				contentText: "first",
				createdAt: NOW,
			});
		});
		expect(assembled("m1")).toEqual([{ type: "text", text: "first" }]);
		const [blockId] = rowIds("m1");
		db.transaction((tx) =>
			bodies.writeEnvelopeBody(tx, "m1", [{ type: "text", text: "second" }], {
				set: { contentText: "second" },
			}),
		);
		expect(assembled("m1")).toEqual([{ type: "text", text: "second" }]);
		expect(rowIds("m1")).toEqual([blockId]);
		expect(bodies.readEnvelope(db, "m1")).toMatchObject({
			contentText: "second",
			contentJson: [{ type: "text", text: "second" }],
		});
	});

	test("a sync without a body keeps the rows the envelope's body is", async () => {
		const messageId = await step();
		const before = assembled(messageId);
		detachColumn(messageId);
		db.transaction(() => blockSync.syncEnvelopeNow(messageId));
		expect(assembled(messageId)).toEqual(before);
		expect(bodies.readEnvelopeBody(db, messageId)).toEqual(before);
	});
});

describe("rewrites read the rows", () => {
	test("a copy-on-write copies the body from the rows", async () => {
		const messageId = await step();
		const body = assembled(messageId);
		share(messageId);
		detachColumn(messageId);
		const copyId = await narratorPersistence.copyOnWriteMessage("n2", messageId, {
			contentText: "edited",
		});
		expect(copyId).not.toBe(messageId);
		expect(assembled(copyId)).toEqual(body);
		expect(column(copyId)).toEqual(body);
		expect(assembled(messageId)).toEqual(body);
	});

	test("an unshared rewrite writes its body and rebuilds the rows from it", async () => {
		const messageId = await step();
		detachColumn(messageId);
		const edited = [{ type: "text", text: "Rewritten." }];
		const id = await narratorPersistence.copyOnWriteMessage("n1", messageId, {
			contentJson: edited,
			contentText: "Rewritten.",
		});
		expect(id).toBe(messageId);
		expect(assembled(messageId)).toEqual(edited);
		expect(column(messageId)).toEqual(edited);
	});

	test("a state patch changes the rows' state and keeps the rest of the body", async () => {
		const messageId = await step();
		const body = assembled(messageId) as Array<Record<string, unknown>>;
		detachColumn(messageId);
		expect(
			await narratorPersistence.patchReasoningTranslation(messageId, 0, "pensar", {
				id: "r1",
				text: "think",
			}),
		).toBe(true);
		expect(assembled(messageId)).toEqual([{ ...body[0], translatedText: "pensar" }, body[1]]);
	});

	test("a body patch rebuilds the rows from the patched body", async () => {
		const messageId = await step();
		const body = assembled(messageId) as Array<Record<string, unknown>>;
		detachColumn(messageId);
		narratorPersistence.patchState(messageId, (content) => content.slice(0, 1));
		expect(assembled(messageId)).toEqual([body[0]]);
	});
});
