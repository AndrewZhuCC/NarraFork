/**
 * narrator-body-writes.ts — how a stored envelope's body is read for a rewrite and written
 * (docs/BLOCK_STORAGE-DESIGN.md §10.4, P3a).
 *
 * A block-format envelope keeps its body as block rows; `content_json` is the projection
 * written alongside them (D8). A writer hands the body it writes to these functions
 * instead of the column, and the rows are built from what it handed over, never read back
 * from the column. So what the column holds can change (P3b) without any writer noticing:
 *
 *   - `insertEnvelope`: a new envelope with its body; its rows are built in the same
 *     transaction;
 *   - `writeEnvelopeBody`: replace an envelope's body, with the other columns that change
 *     with it; `updateEnvelope` for a set of columns that may include it;
 *   - `readEnvelope` / `readEnvelopeBody`: what a read-modify-write starts from, read as
 *     every reader reads it (`narrator-body-reads.ts`).
 *
 * All synchronous, inside the caller's transaction (bun:sqlite cannot await in one).
 */

import { and, eq, type SQL } from "drizzle-orm";
import type { db } from "../db";
import { narratorMessages } from "../db/schema";
import {
	type BlockStamp,
	type SyncMode,
	syncCopiedEnvelope,
	syncEnvelopeNow,
} from "./narrator-block-sync";
import { bodyColumns, contentJsonSql, fillBody } from "./narrator-body-reads";

export type BodyTx = Parameters<Parameters<typeof db.transaction>[0]>[0];
/** A transaction, or the database outside one (a read needs no transaction). */
type BodyReader = Pick<BodyTx, "select" | "query">;
type Envelope = typeof narratorMessages.$inferSelect;
type NewEnvelope = typeof narratorMessages.$inferInsert;

/** How the rows of the written body read incomplete output, and what new rows carry. */
export interface BodySync {
	mode?: SyncMode;
	stamp?: BlockStamp;
}

/**
 * Insert an envelope with its body and build its rows. `rows` writes what belongs with the
 * envelope (its ref, its ledger rows) before the blocks are built from both; `copyOf`
 * names the envelope it copies, whose blocks the new ones then descend from (§6.3).
 * Returns the row as stored, with the body it was given.
 */
export function insertEnvelope(
	tx: BodyTx,
	values: NewEnvelope,
	sync: BodySync & { rows?: () => void; copyOf?: string } = {},
): Envelope {
	const row = tx.insert(narratorMessages).values(values).returning().get();
	sync.rows?.();
	if (sync.copyOf) syncCopiedEnvelope(sync.copyOf, row.id, values.contentJson);
	else syncEnvelopeNow(row.id, sync.mode, sync.stamp, values.contentJson);
	return { ...row, contentJson: values.contentJson };
}

/**
 * Update an envelope's columns (none is fine) and rebuild its rows: through
 * `writeEnvelopeBody` when they include its body; otherwise in place, the rows then
 * rebuilt from its body as read (a role or text the blocks derive from may have moved,
 * or `mode` seals what streamed).
 */
export function updateEnvelope(
	tx: BodyTx,
	messageId: string,
	set: Partial<NewEnvelope>,
	sync: BodySync = {},
): void {
	if (Object.hasOwn(set, "contentJson")) {
		writeEnvelopeBody(tx, messageId, set.contentJson, { ...sync, set });
		return;
	}
	if (Object.keys(set).length)
		tx.update(narratorMessages).set(set).where(eq(narratorMessages.id, messageId)).run();
	syncEnvelopeNow(messageId, sync.mode, sync.stamp);
}

/**
 * Replace an envelope's body (`set` names the columns that change with it) and rebuild
 * its rows from it; `rows` writes what belongs with it (a new call's ledger row) before
 * they are. `where` narrows the write (a compare-and-set); nothing else happens when it
 * does not match. Returns whether the envelope was written.
 */
export function writeEnvelopeBody(
	tx: BodyTx,
	messageId: string,
	content: unknown,
	options: BodySync & { set?: Partial<NewEnvelope>; where?: SQL; rows?: () => void } = {},
): boolean {
	const target = eq(narratorMessages.id, messageId);
	const written = tx
		.update(narratorMessages)
		.set({ ...options.set, contentJson: content })
		.where(options.where ? and(target, options.where) : target)
		.returning({ id: narratorMessages.id })
		.get();
	if (!written) return false;
	options.rows?.();
	syncEnvelopeNow(messageId, options.mode, options.stamp, content);
	return true;
}

/** Every column of an envelope, `contentJson` its body as readers read it. */
export function readEnvelope(tx: BodyReader, messageId: string): Envelope | undefined {
	return fillBody(
		tx.query.narratorMessages
			.findFirst({ ...bodyColumns(), where: eq(narratorMessages.id, messageId) })
			.sync(),
	);
}

/** An envelope's body as readers read it; undefined when there is no such envelope. */
export function readEnvelopeBody(tx: BodyReader, messageId: string): unknown {
	return fillBody(
		tx
			.select({ id: narratorMessages.id, contentJson: contentJsonSql() })
			.from(narratorMessages)
			.where(eq(narratorMessages.id, messageId))
			.get(),
	)?.contentJson;
}
