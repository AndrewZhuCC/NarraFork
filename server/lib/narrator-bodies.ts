/**
 * narrator-bodies.ts — where a stored envelope's body is read from
 * (docs/BLOCK_STORAGE-DESIGN.md §10.1, P2).
 *
 * A block-format envelope (`body_format = 2`) keeps its body as block rows, which
 * readers assemble. `content_json` holds the same content while both are written, and is
 * still the body of legacy envelopes. A reader selects `bodySql(alias)` where it used to
 * select `content_json`, and `readBodies` fills in what that left out:
 *
 *   - `bodySql` is NULL for an envelope whose rows are current, and `content_json`
 *     otherwise: a legacy envelope, one whose sync is still queued (a write outside the
 *     store, rows not rebuilt yet), or one without rows (empty or unreadable content);
 *   - `readBodies` assembles the NULL ones from their rows in one query per batch.
 *
 * With the read switch off (`fromBlocks` false) both reduce to plain `content_json`.
 * Takes an explicit connection, so it runs in db-workers too.
 */

import type { Database } from "bun:sqlite";
import { parseStored } from "@shared/narrator-blocks/plan";
import { assembleContent } from "@shared/narrator-blocks/storage";

/** The column a reader selects in place of `content_json` (see the module comment). */
export function bodySql(alias: string, fromBlocks: boolean): string {
	if (!fromBlocks) return `${alias}.content_json`;
	return `CASE WHEN ${alias}.body_format = 2
		AND NOT EXISTS (SELECT 1 FROM narrator_block_sync_queue q WHERE q.message_id = ${alias}.id)
		AND EXISTS (SELECT 1 FROM narrator_blocks b WHERE b.message_id = ${alias}.id)
		THEN NULL ELSE ${alias}.content_json END`;
}

/**
 * The stored size of an envelope's body, for byte budgets: `body_bytes` for a
 * block-format envelope (the sum of its rows), so `content_json` is not read just to be
 * measured; the length of `content_json` otherwise.
 */
export function bodyBytesSql(alias: string, fromBlocks: boolean): string {
	const json = `length(CAST(${alias}.content_json AS BLOB))`;
	if (!fromBlocks) return json;
	return `CASE WHEN ${alias}.body_format = 2 AND ${alias}.body_bytes > 0
		THEN ${alias}.body_bytes ELSE ${json} END`;
}

const ID_BATCH = 500;

/**
 * Each envelope's block rows, assembled into the array they store, by message id.
 * `charge` sees each stored payload and state before it is parsed, for readers that
 * budget what they decode.
 */
export function blockBodies(
	db: Database,
	messageIds: readonly string[],
	charge?: (stored: string) => void,
): Map<string, unknown[]> {
	const rows = new Map<string, Array<{ payload: unknown; state: unknown }>>();
	for (let start = 0; start < messageIds.length; start += ID_BATCH) {
		const ids = messageIds.slice(start, start + ID_BATCH);
		const batch = db
			.query(
				`SELECT message_id AS messageId, payload, state FROM narrator_blocks
				 WHERE message_id IN (${ids.map(() => "?").join(",")}) ORDER BY message_id, ordinal`,
			)
			.all(...ids) as Array<{ messageId: string; payload: string; state: string | null }>;
		for (const row of batch) {
			if (charge) {
				charge(row.payload);
				if (row.state !== null) charge(row.state);
			}
			const list = rows.get(row.messageId) ?? [];
			list.push({ payload: parseStored(row.payload), state: parseStored(row.state) });
			rows.set(row.messageId, list);
		}
	}
	return new Map([...rows].map(([id, list]) => [id, assembleContent(list)]));
}

/**
 * The parsed body of each row that selected `bodySql`: assembled from block rows where
 * it came back NULL, parsed from `content_json` otherwise.
 */
export function readBodies(
	db: Database,
	rows: ReadonlyArray<{ id: string; body: string | null }>,
): Map<string, unknown> {
	const fromBlocks = rows.filter((row) => row.body === null).map((row) => row.id);
	const assembled = fromBlocks.length ? blockBodies(db, fromBlocks) : new Map();
	return new Map(
		rows.map((row) => [
			row.id,
			row.body === null ? (assembled.get(row.id) ?? []) : parseStored(row.body),
		]),
	);
}
