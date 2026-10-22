/**
 * narrator-body-reads.ts — message bodies for main-thread Drizzle readers
 * (docs/BLOCK_STORAGE-DESIGN.md §10.1, P2).
 *
 * The Drizzle form of `server/lib/narrator-bodies.ts`. A reader reads the body through
 * one of these, then passes the rows to `fillBodies`:
 *
 *   - a core select puts `contentJsonSql()` where it put `narratorMessages.contentJson`;
 *   - a relational query spreads `bodyColumns(...)` where it had `columns` (or nothing).
 *
 * A block-format envelope whose rows are current comes back NULL and `fillBodies`
 * assembles it from its rows; every other envelope reads `content_json`. With block reads
 * off, and on PG, both return exactly the plain column / `columns` option, and
 * `fillBodies` has nothing to fill.
 */

import type { AnyColumn, SQL } from "drizzle-orm";
import { sql } from "drizzle-orm";
import * as database from "../db";
import { narratorMessages } from "../db/schema";
import { blockBodies } from "../lib/narrator-bodies";
import { blockReadsEnabled } from "./narrator-block-scans";

/** The envelope columns the expression reads: `narratorMessages`, or a relational alias of it. */
interface EnvelopeColumns {
	id: AnyColumn;
	bodyFormat: AnyColumn;
	contentJson: AnyColumn;
}

/**
 * What a reader selects in place of `content_json`: NULL for an envelope whose block rows
 * are current, the column otherwise, decoded like the column. Without block reads, the
 * column itself.
 */
export function contentJsonSql(table: EnvelopeColumns = narratorMessages): SQL<unknown> {
	if (!blockReadsEnabled()) return table.contentJson as unknown as SQL<unknown>;
	return sql`CASE WHEN ${blocksCurrent(table)} THEN NULL ELSE ${table.contentJson} END`.mapWith(
		table.contentJson,
	);
}

/** Envelope columns read by the predicates below. */
interface SizedEnvelopeColumns extends EnvelopeColumns {
	bodyBytes: AnyColumn;
}

/** Whether the envelope's block rows are current, so they hold its body. */
export function bodyInBlocksSql(table: EnvelopeColumns = narratorMessages): SQL {
	return blocksCurrent(table);
}

function blocksCurrent(table: EnvelopeColumns): SQL {
	return sql`(${table.bodyFormat} = 2
		AND NOT EXISTS (SELECT 1 FROM narrator_block_sync_queue q WHERE q.message_id = ${table.id})
		AND EXISTS (SELECT 1 FROM narrator_blocks b WHERE b.message_id = ${table.id}))`;
}

/**
 * Whether the envelope stores an element of `type` (top-level `type` of an array
 * element), read from its block payloads when they hold its body.
 */
export function hasStoredElementSql(
	type: string,
	table: EnvelopeColumns = narratorMessages,
): SQL<boolean> {
	const inColumn = sql`EXISTS (
		SELECT 1 FROM json_each(${table.contentJson}) AS stored_element
		WHERE json_extract(stored_element.value, '$.type') = ${type}
	)`;
	if (!blockReadsEnabled()) return sql<boolean>`(${inColumn})`;
	return sql<boolean>`(CASE WHEN ${blocksCurrent(table)} THEN EXISTS (
		SELECT 1 FROM narrator_blocks b, json_each(b.payload, '$.raw') AS stored_element
		WHERE b.message_id = ${table.id} AND json_extract(stored_element.value, '$.type') = ${type}
	) ELSE ${inColumn} END)`;
}

/**
 * The stored size of the envelope's body: `body_bytes` (the sum of its rows) for a
 * block-format envelope, so the body is not read just to be measured; the length of
 * `content_json` otherwise.
 */
export function storedBodyBytesSql(table: SizedEnvelopeColumns = narratorMessages): SQL<number> {
	const json = sql`length(CAST(${table.contentJson} AS BLOB))`;
	if (!blockReadsEnabled()) return sql<number>`${json}`;
	return sql<number>`(CASE WHEN ${table.bodyFormat} = 2 AND ${table.bodyBytes} > 0
		THEN ${table.bodyBytes} ELSE ${json} END)`;
}

/**
 * The ordinal of the envelope's tool block for the call `wireId`, when its block rows hold
 * its body (NULL otherwise, or without such a block): the position the call's `tool_use`
 * element has among the stored elements, in the same order.
 */
export function toolBlockOrdinalSql(
	table: EnvelopeColumns,
	wireId: AnyColumn | SQL,
): SQL<number | null> {
	if (!blockReadsEnabled()) return sql<number | null>`NULL`;
	return sql<number | null>`(CASE WHEN ${blocksCurrent(table)} THEN (
		SELECT b.ordinal FROM narrator_blocks b
		WHERE b.message_id = ${table.id} AND b.kind = 'tool' AND b.wire_id = ${wireId}
		ORDER BY b.ordinal LIMIT 1
	) END)`;
}

/**
 * Relational queries return `extras` undecoded, so the body comes back as stored text
 * under its own key and `fillBodies` decodes it (as the column would) into `contentJson`.
 */
const STORED_BODY = "storedBody";
const bodyExtras = (table: EnvelopeColumns) => ({
	[STORED_BODY]: sql`CASE WHEN ${blocksCurrent(table)} THEN NULL ELSE ${table.contentJson} END`.as(
		"stored_body",
	),
});

/**
 * Relational-query options reading the body as `contentJsonSql` does (as stored text, which
 * `fillBodies` decodes). Spread in place of the query's `columns`: with none, every column;
 * with some, those (`id` and `contentJson` among them). Typed as the plain option, so the
 * result type is what `fillBodies` leaves.
 */
export function bodyColumns(): Record<never, never>;
export function bodyColumns<const C extends { id: true; contentJson: true }>(
	columns: C,
): { columns: C };
export function bodyColumns(columns?: Record<string, boolean>): object {
	if (!blockReadsEnabled()) return columns ? { columns } : {};
	const rest = columns && Object.entries(columns).filter(([key]) => key !== "contentJson");
	return {
		columns: rest ? Object.fromEntries(rest) : { contentJson: false },
		extras: bodyExtras,
	};
}

/**
 * Complete the bodies read through `contentJsonSql` or `bodyColumns`, in place: decode
 * the stored text a relational query returned, and assemble the NULL ones from their block
 * rows. Stored `content_json` is never NULL, so a NULL is always an envelope whose rows
 * hold its body.
 */
export function fillBodies<T extends { id: string; contentJson: unknown }>(rows: T[]): T[] {
	for (const row of rows) {
		const record = row as Record<string, unknown>;
		if (!Object.hasOwn(record, STORED_BODY)) continue;
		const stored = record[STORED_BODY];
		delete record[STORED_BODY];
		row.contentJson = typeof stored === "string" ? JSON.parse(stored) : null;
	}
	const missing = rows.filter((row) => row.contentJson === null).map((row) => row.id);
	if (missing.length === 0) return rows;
	const bodies = blockBodies(database.sqlite, missing);
	for (const row of rows) if (row.contentJson === null) row.contentJson = bodies.get(row.id) ?? [];
	return rows;
}

/** One envelope's body assembled from its block rows (for a reader that saw it NULL). */
export function blockBody(messageId: string): unknown[] {
	return blockBodies(database.sqlite, [messageId]).get(messageId) ?? [];
}

/** `fillBodies` for one row, or none. */
export function fillBody<T extends { id: string; contentJson: unknown }>(row: T): T;
export function fillBody<T extends { id: string; contentJson: unknown }>(
	row: T | undefined,
): T | undefined;
export function fillBody<T extends { id: string; contentJson: unknown }>(
	row: T | undefined,
): T | undefined {
	if (row) fillBodies([row]);
	return row;
}
