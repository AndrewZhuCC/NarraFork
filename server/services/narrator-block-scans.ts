/**
 * narrator-block-scans.ts — finding envelopes by what their bodies hold, through the block
 * tables (docs/BLOCK_STORAGE-DESIGN.md §10.1, P2).
 *
 * The queries these replace matched `content_json` (`LIKE`, `json_extract`) across every
 * message. A block-format envelope whose rows are current is found through its rows
 * instead: its context block's indexed subtype, its asset rows, its block's payload. Two
 * kinds of envelope still answer from `content_json`:
 *
 *   - queued ones (`narrator_block_sync_queue`), whose body a write outside the store
 *     changed and whose rows have not caught up; the queue is small and keyed by id;
 *   - legacy ones (`body_format = 1`): not converted yet, written while block writes were
 *     off, or imported. `idx_messages_legacy_body` holds exactly these, so they are found
 *     without walking other bodies, and the index is empty once all are converted.
 *
 * With the read switch off, and on PG (block storage is SQLite-only), callers keep their
 * `content_json` queries unchanged.
 */

import { parseStored } from "@shared/narrator-blocks/plan";
import * as database from "../db";
import { blocksCurrentSql, readBodies } from "../lib/narrator-bodies";
import { settings } from "../lib/settings";

/** Whether scans go through the block tables: SQLite with `narratorBlocks.read`. */
export function blockReadsEnabled(): boolean {
	return database.activeDatabaseBackend === "sqlite" && settings.narratorBlocks.read;
}

/** The envelope at `alias` is not waiting on the block sync. */
const NOT_QUEUED = (alias: string) =>
	`NOT EXISTS (SELECT 1 FROM narrator_block_sync_queue q WHERE q.message_id = ${alias}.id)`;

/**
 * An expression over a non-assistant envelope's stored elements: `read(json, root)` over
 * its block's payload (root `$.raw`) when its rows are current, over `content_json` (root
 * `$`) otherwise. Such an envelope keeps a single block whose payload holds its elements
 * in order; reader-only fields (a compact's status, a card's `isLatest`) are in the
 * block's state and are not found this way.
 */
export function storedElementsSql(
	alias: string,
	read: (json: string, root: string) => string,
): string {
	return `CASE WHEN ${blocksCurrentSql(alias)}
		THEN (SELECT ${read("b.payload", "$.raw")} FROM narrator_blocks b
			WHERE b.message_id = ${alias}.id ORDER BY b.ordinal LIMIT 1)
		ELSE ${read(`${alias}.content_json`, "$")} END`;
}

export interface FoundEnvelope {
	id: string;
	createdAt: string;
	/** The stored content, assembled from rows where they are current. */
	content: unknown;
}

/**
 * Envelopes with a context block of `subtype` whose content contains every string in
 * `needles`, the substring match the `content_json` scans made. A block-format envelope is
 * found through the subtype index and matched on its block's payload; `marker` is the
 * `content_json` substring that finds the same envelopes where no rows answer. Only the
 * payload is searched, so needles must not be reader-only fields.
 */
export function envelopesWithContext(
	subtype: string,
	marker: string,
	needles: readonly string[],
): FoundEnvelope[] {
	const sqlite = database.sqlite;
	const patterns = needles.map((needle) => `%${needle}%`);
	const matching = (column: string) => needles.map(() => ` AND ${column} LIKE ?`).join("");
	type Row = { id: string; createdAt: string; body: string | null };
	const current = sqlite
		.query(
			`SELECT m.id, m.created_at AS createdAt, NULL AS body
			 FROM narrator_blocks b JOIN narrator_messages m ON m.id = b.message_id
			 WHERE b.kind = 'context' AND b.subtype = ? AND m.body_format = 2
				AND ${NOT_QUEUED("m")}${matching("b.payload")}`,
		)
		.all(subtype, ...patterns) as Row[];
	const queued = sqlite
		.query(
			`SELECT m.id, m.created_at AS createdAt, m.content_json AS body
			 FROM narrator_block_sync_queue q JOIN narrator_messages m ON m.id = q.message_id
			 WHERE m.content_json LIKE ?${matching("m.content_json")}`,
		)
		.all(`%${marker}%`, ...patterns) as Row[];
	const legacy = sqlite
		.query(
			`SELECT id, created_at AS createdAt, content_json AS body
			 FROM narrator_messages INDEXED BY idx_messages_legacy_body
			 WHERE body_format = 1 AND content_json LIKE ?${matching("content_json")}`,
		)
		.all(`%${marker}%`, ...patterns) as Row[];
	const rows = [
		...new Map([...current, ...queued, ...legacy].map((row) => [row.id, row])).values(),
	];
	const bodies = readBodies(sqlite, rows);
	return rows.map((row) => ({ id: row.id, createdAt: row.createdAt, content: bodies.get(row.id) }));
}

/**
 * Who holds an envelope with an image element (`{type: "image", imageId}`, recorded as an
 * `input_image` asset): `owners` are the `narrator_messages.narrator_id` of block-format
 * envelopes found through their asset rows; `unindexed` are the queued and legacy
 * envelopes whose `content_json` mentions an image, for the caller's exact check.
 */
export function imageEnvelopeOwners(): {
	owners: string[];
	unindexed: Array<{ narratorId: string; contentJson: unknown }>;
} {
	const sqlite = database.sqlite;
	const owners = sqlite
		.query(
			`SELECT DISTINCT m.narrator_id AS narratorId
			 FROM narrator_block_assets a
			 JOIN narrator_blocks b ON b.id = a.block_id
			 JOIN narrator_messages m ON m.id = b.message_id
			 WHERE a.role = 'input_image' AND m.body_format = 2 AND ${NOT_QUEUED("m")}`,
		)
		.all() as Array<{ narratorId: string }>;
	type Row = { narratorId: string; body: string | null };
	const marker = '%"type":"image"%';
	const unindexed = [
		...(sqlite
			.query(
				`SELECT m.narrator_id AS narratorId, m.content_json AS body
				 FROM narrator_block_sync_queue q JOIN narrator_messages m ON m.id = q.message_id
				 WHERE m.content_json LIKE ?`,
			)
			.all(marker) as Row[]),
		...(sqlite
			.query(
				`SELECT narrator_id AS narratorId, content_json AS body
				 FROM narrator_messages INDEXED BY idx_messages_legacy_body
				 WHERE body_format = 1 AND content_json LIKE ?`,
			)
			.all(marker) as Row[]),
	];
	return {
		owners: owners.map((row) => row.narratorId),
		unindexed: unindexed.map((row) => ({
			narratorId: row.narratorId,
			contentJson: parseStored(row.body),
		})),
	};
}
