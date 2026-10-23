/**
 * narrator-block-sync.ts — keep an envelope's `narrator_blocks` rows equal to its stored
 * content (docs/BLOCK_STORAGE-DESIGN.md §4, P1).
 *
 * In P1 every reader still reads `content_json`; envelopes written with `body_format = 2`
 * additionally carry their blocks (`shared/narrator-blocks/storage.ts` maps one onto the
 * other losslessly). `syncEnvelope` brings the rows in line with the content and ledger
 * inside the caller's transaction:
 *   - rows keep their id across rewrites (`matchBlocks`: by identity, then by payload in
 *     order, then by type in place), so attempt ids, lineage and timestamps survive a
 *     checkpoint or an edit, and a removed block's id never passes to a neighbour;
 *   - a row is written only when what it stores changed, so a streaming checkpoint of one
 *     item writes that one block;
 *   - each tool block becomes the `block_id` of its ledger attempts, and the assets and
 *     the block search index follow the rows.
 *
 * Store operations sync the envelopes they write. Everything else converges through
 * `narrator_block_sync_queue`: generated triggers enqueue every block-format envelope whose
 * content or tool ledger changes, and `flushChangedEnvelopes` — run by the store at the end
 * of its writes and by a periodic sweep — syncs whatever is still queued, after a restart
 * too.
 */

import type { DecodableMessage } from "@shared/narrator-blocks/decode";
import {
	adoptedLedger,
	blockAssets,
	blockIndexText,
	type PlanLedgerRow,
	parseStored as parse,
	planEnvelopeBlocks,
} from "@shared/narrator-blocks/plan";
import {
	assembleContent,
	type BlockDraft,
	inAssembledOrder,
	isStateOnlyChange,
	type MatchableBlock,
	matchBlocks,
	splitEnvelope,
} from "@shared/narrator-blocks/storage";
import type { EnvelopeRole } from "@shared/narrator-blocks/types";
import * as database from "../db";
import { removeBlockIndexText, replaceBlockIndexText } from "../db/fts";
import { generateId } from "../lib/id";
import { logger } from "../lib/logger";
import { bodySql, readBodies } from "../lib/narrator-bodies";
import { settings } from "../lib/settings";
import { blockIdForElement, reassignBlockIdForElement } from "./narrator-block-ids";
import { blockReadsEnabled } from "./narrator-block-scans";
import { activeTurnId } from "./narrator-turns";

/** How incomplete output and reasoning are read: still streaming, sealed, or as they were. */
export type SyncMode = "streaming" | "sealed" | "keep";

/** What a write stamps on the blocks it creates (§3.6). */
export interface BlockStamp {
	/** The provider request that produced the new blocks; a discard drops them by it. */
	attemptId?: string | null;
}

/** The `body_format` a newly written envelope gets. */
export function newEnvelopeFormat(): 1 | 2 {
	if (!settings.narratorBlocks?.write || !hasBlockSchema()) return 1;
	startSweep();
	return 2;
}

/** Columns every newly written envelope gets: its format and its run (§3.6). */
export function newEnvelopeColumns(narratorId: string): {
	bodyFormat: 1 | 2;
	turnId: string | null;
} {
	return { bodyFormat: newEnvelopeFormat(), turnId: activeTurnId(narratorId) };
}

let blockSchema: boolean | undefined;

/** Whether this database has block storage (a schema predating it writes legacy rows). */
function hasBlockSchema(): boolean {
	blockSchema ??= !!database.sqlite
		.prepare(
			"SELECT 1 FROM sqlite_schema WHERE type = 'table' AND name = 'narrator_block_sync_queue'",
		)
		.get();
	return blockSchema;
}

/**
 * Most envelopes a transaction syncs from the queue. A store write queues the few it
 * touched; anything beyond this (a bulk rewrite outside the store) is left to the sweep.
 */
const FLUSH_LIMIT = 64;

/** Sync queued envelopes; the store calls this as the last write of its transactions. */
export function flushChangedEnvelopes(limit = FLUSH_LIMIT): number {
	if (!hasBlockSchema()) return 0;
	const ids = (
		database.sqlite
			.prepare("SELECT message_id AS id FROM narrator_block_sync_queue LIMIT ?")
			.all(limit) as Array<{ id: string }>
	).map((row) => row.id);
	const done = database.sqlite.prepare(
		"DELETE FROM narrator_block_sync_queue WHERE message_id = ?",
	);
	for (const id of ids) {
		syncEnvelope(id);
		done.run(id);
	}
	return ids.length;
}

/**
 * Sync one envelope now and take it off the queue. A writer that just wrote the body
 * passes it as `content`, so the rows are built from what it wrote, not read back.
 */
export function syncEnvelopeNow(
	messageId: string,
	mode: SyncMode = "keep",
	stamp: BlockStamp = {},
	content?: unknown,
): void {
	if (!hasBlockSchema()) return;
	syncEnvelope(messageId, mode, stamp, content);
	database.sqlite
		.prepare("DELETE FROM narrator_block_sync_queue WHERE message_id = ?")
		.run(messageId);
}

/**
 * Rebuild an envelope's rows from what `content_json` stores, for a check that found them
 * differing from it (the dual-write audit and the backfill's verify): the column is the
 * reference they are checked against.
 */
export function resyncFromColumn(messageId: string, stored?: unknown): void {
	if (!hasBlockSchema()) return;
	const row = database.sqlite
		.prepare(
			"SELECT content_json AS contentJson, body_rows_only AS rowsOnly FROM narrator_messages WHERE id = ?",
		)
		.get(messageId) as { contentJson: string | null; rowsOnly: number } | null;
	// A rows-only envelope's column is no reference: its rows are its only copy.
	if (!row || row.rowsOnly) return;
	syncEnvelopeNow(messageId, "keep", {}, stored ?? parse(row.contentJson));
}

const SWEEP_INTERVAL_MS = 5_000;
const SWEEP_BATCH = 20;
/** Main-thread time one sweep may spend; each batch is its own short transaction. */
const SWEEP_BUDGET_MS = 25;
let sweep: ReturnType<typeof setInterval> | undefined;

/** Drain the queue in small transactions until it is empty or the budget is spent. */
function drainQueue(budgetMs: number): number {
	const started = performance.now();
	let synced = 0;
	for (;;) {
		const batch = database.sqlite.transaction(() => flushChangedEnvelopes(SWEEP_BATCH))();
		synced += batch;
		if (batch < SWEEP_BATCH || performance.now() - started >= budgetMs) return synced;
	}
}

function startSweep(): void {
	if (sweep) return;
	sweep = setInterval(() => {
		try {
			const synced = drainQueue(SWEEP_BUDGET_MS);
			if (synced) logger.debug("Synced queued block rows", { synced });
		} catch (error) {
			logger.warn("Block sync sweep failed", { error: String(error) });
			if (/closed/i.test(String(error))) stopBlockSyncSweep();
		}
	}, SWEEP_INTERVAL_MS);
	sweep.unref?.();
}

export function stopBlockSyncSweep(): void {
	if (sweep) clearInterval(sweep);
	sweep = undefined;
}

/**
 * Startup: nothing streams yet, so items an earlier process left `open` were interrupted
 * (§5.3); then drain what it left queued, and keep draining from here on.
 */
export function recoverBlockStorage(): number {
	if (!hasBlockSchema()) return 0;
	const interrupted = database.sqlite
		.prepare(
			`UPDATE narrator_blocks SET phase = 'aborted', revision = revision + 1,
				completed_at = coalesce(completed_at, ?)
			 WHERE phase = 'open' AND kind IN ('output', 'reasoning')`,
		)
		.run(new Date().toISOString()).changes;
	drainQueue(SWEEP_BUDGET_MS);
	auditRecentEnvelopes();
	startSweep();
	return interrupted;
}

const AUDIT_ENVELOPES = 50;

/**
 * Dual-write check (P1 exit criterion): the newest block-format envelopes that keep a
 * projection (not rows-only) must assemble from their rows to exactly what they store. A
 * mismatch is logged and re-synced.
 * Returns the ids that differed.
 */
export function auditRecentEnvelopes(limit = AUDIT_ENVELOPES): string[] {
	if (!hasBlockSchema()) return [];
	const rows = database.sqlite
		.prepare(
			`SELECT id, content_json AS contentJson FROM narrator_messages
			 WHERE body_format = 2 AND body_rows_only = 0 ORDER BY rowid DESC LIMIT ?`,
		)
		.all(limit) as Array<{ id: string; contentJson: string | null }>;
	const differing = rows.flatMap((row) => {
		const stored = parse(row.contentJson);
		if (!Array.isArray(stored)) return [];
		const blocks = database.sqlite
			.prepare("SELECT payload, state FROM narrator_blocks WHERE message_id = ? ORDER BY ordinal")
			.all(row.id) as Array<{ payload: string; state: string | null }>;
		const assembled = assembleContent(
			blocks.map((block) => ({ payload: parse(block.payload), state: parse(block.state) })),
		);
		return JSON.stringify(assembled) === JSON.stringify(inAssembledOrder(stored))
			? []
			: [{ id: row.id, stored }];
	});
	if (differing.length) {
		const messageIds = differing.map((envelope) => envelope.id);
		logger.warn("Block rows differed from their envelope; re-synced", { messageIds });
		database.sqlite.transaction(() => {
			for (const { id, stored } of differing) resyncFromColumn(id, stored);
		})();
		return messageIds;
	}
	return [];
}

// === Sync ===

interface EnvelopeRow {
	id: string;
	narratorId: string;
	role: EnvelopeRole;
	contentText: string | null;
	parentToolUseId: string | null;
	messageUuid: string | null;
	origin: string | null;
	provider: string | null;
	bodyFormat: number;
}

interface BlockRow {
	id: string;
	ordinal: number;
	kind: string;
	subtype: string | null;
	phase: string;
	wireId: string | null;
	modelText: string | null;
	payload: string;
	state: string | null;
	revision: number;
}

/**
 * Rows a writer removed together with the elements they held (a delete by block id,
 * §5.5). While `withRetiredBlocks` runs, syncs and copies treat them as gone already, so
 * no remaining block can continue one even where the content left behind is ambiguous
 * (two identical id-less texts).
 */
let retiredRows: ReadonlySet<string> = new Set();

/** Run a synchronous write (a transaction) with these block rows retired. */
export function withRetiredBlocks<T>(rowIds: Iterable<string>, write: () => T): T {
	const outer = retiredRows;
	retiredRows = new Set([...outer, ...rowIds]);
	try {
		return write();
	} finally {
		retiredRows = outer;
	}
}

/**
 * Rows in ordinal order as `matchBlocks` takes them: each keyed by what the codec makes of
 * the content they assemble. Rows the codec would group differently match nothing.
 */
function keyed(
	message: Omit<DecodableMessage, "contentJson">,
	rows: ReadonlyArray<{ id: string; payload: string; state: string | null }>,
): MatchableBlock[] {
	const keys = splitEnvelope({
		...message,
		contentJson: assembleContent(
			rows.map((row) => ({ payload: parse(row.payload), state: parse(row.state) })),
		),
	}).blocks.map((draft) => draft.key);
	return rows.map((row, index) => ({
		key: keys.length === rows.length ? keys[index] : `unmatched:${row.id}`,
		payload: row.payload,
	}));
}

/** The element identity a codec key names exactly (`type:identity`); none for positional keys. */
function elementIdentity(key: string): string | undefined {
	const colon = key.indexOf(":");
	return colon > 0 && !/~\d+$/.test(key) ? key.slice(colon + 1) : undefined;
}

/**
 * A new row's id. A streaming element's row takes the block id handed out when the item
 * opened (`narrator-block-ids.ts`, §5.2), or hands one out now for its later deltas;
 * every other row gets a fresh id.
 */
function newRowId(narratorId: string, key: string, mode: SyncMode): string {
	const identity = mode === "streaming" ? elementIdentity(key) : undefined;
	if (!identity) return generateId();
	const { blockId } = blockIdForElement(narratorId, identity);
	if (!database.sqlite.prepare("SELECT 1 FROM narrator_blocks WHERE id = ?").get(blockId))
		return blockId;
	// The id names a row already (a copy of the envelope kept the element): use a new one.
	const fresh = generateId();
	reassignBlockIdForElement(narratorId, identity, fresh);
	return fresh;
}

/** Non-internal ledger rows of a message, in the order `adoptedLedger` expects. */
function ledgerRows(messageId: string): PlanLedgerRow[] {
	return database.sqlite
		.prepare(
			`SELECT tool_use_id AS toolUseId, tool_name AS toolName, input_json AS inputJson,
				output_json AS outputJson, status
			 FROM narrator_tool_calls WHERE message_id = ? AND is_internal = 0
			 ORDER BY tool_use_id, execution_attempt, created_at, id`,
		)
		.all(messageId) as PlanLedgerRow[];
}

const ENVELOPE_COLUMNS = `id, narrator_id AS narratorId, role, content_text AS contentText,
	parent_tool_use_id AS parentToolUseId, sdk_message_uuid AS messageUuid, origin, provider,
	body_format AS bodyFormat`;

function envelopeRow(messageId: string): EnvelopeRow | null {
	return database.sqlite
		.prepare(`SELECT ${ENVELOPE_COLUMNS} FROM narrator_messages WHERE id = ?`)
		.get(messageId) as EnvelopeRow | null;
}

/**
 * The envelope's body as readers read it (`narrator-bodies.ts`): assembled from its rows
 * when they are current, `content_json` otherwise.
 */
function envelopeBody(messageId: string): unknown {
	const row = database.sqlite
		.prepare(
			`SELECT id, ${bodySql("m", blockReadsEnabled())} AS body FROM narrator_messages m WHERE id = ?`,
		)
		.get(messageId) as { id: string; body: string | null } | null;
	return row ? readBodies(database.sqlite, [row]).get(messageId) : undefined;
}

/**
 * Bring one envelope's block rows in line with its content, inside the caller's
 * transaction: `given` when the writer passes the body it wrote; otherwise the body as
 * readers read it, which is `content_json` while a write outside the store is queued and
 * the rows themselves once they are current (a ledger change re-derives a tool block's
 * phase from them). A no-op for legacy-format envelopes and for content that is not an
 * array.
 */
export function syncEnvelope(
	messageId: string,
	mode: SyncMode = "keep",
	stamp: BlockStamp = {},
	given?: unknown,
): void {
	const row = envelopeRow(messageId);
	if (!row || row.bodyFormat !== 2) return;
	const content = given === undefined ? envelopeBody(messageId) : given;
	if (!Array.isArray(content)) return;

	const message = {
		id: row.id,
		narratorId: row.narratorId,
		role: row.role,
		contentText: row.contentText,
		parentToolUseId: row.parentToolUseId,
		messageUuid: row.messageUuid,
		origin: row.origin,
		provider: row.provider,
	};
	const planned = planEnvelopeBlocks(
		{ ...message, contentJson: content },
		adoptedLedger(ledgerRows(messageId)),
		{ streaming: mode === "streaming" },
	);

	const existing = database.sqlite
		.prepare(
			`SELECT id, ordinal, kind, subtype, phase, wire_id AS wireId, model_text AS modelText,
				payload, state, revision
			 FROM narrator_blocks WHERE message_id = ? ORDER BY ordinal`,
		)
		.all(messageId) as BlockRow[];
	const live = existing.filter((block) => !retiredRows.has(block.id));
	const continued = matchBlocks(
		keyed(message, live),
		planned.blocks.map((block) => ({ key: block.draft.key, payload: block.payload })),
	);

	const now = new Date().toISOString();
	const kept = new Set<string>();
	const plans = planned.blocks.map((block, ordinal) => {
		const { draft } = block;
		const at = continued[ordinal];
		const current = at === undefined ? undefined : live[at];
		if (current) kept.add(current.id);
		let phase = draft.phase;
		// Only a sealing write turns an item still streaming into an interrupted one.
		if (
			mode === "keep" &&
			current?.phase === "open" &&
			phase === "aborted" &&
			(draft.kind === "output" || draft.kind === "reasoning")
		)
			phase = "open";
		return {
			...block,
			ordinal,
			current,
			phase,
			id: current?.id ?? newRowId(message.narratorId, draft.key, mode),
		};
	});

	const removed = existing.filter((block) => !kept.has(block.id));
	const deleteBlock = database.sqlite.prepare("DELETE FROM narrator_blocks WHERE id = ?");
	for (const block of removed) {
		dropBlockIndex(block.id);
		deleteBlock.run(block.id);
	}
	// Ordinals are unique per envelope: park every moving row before placing any.
	const park = database.sqlite.prepare("UPDATE narrator_blocks SET ordinal = ? WHERE id = ?");
	for (const plan of plans)
		if (plan.current && plan.current.ordinal !== plan.ordinal) park.run(-1 - plan.ordinal, plan.id);

	const insert = database.sqlite.prepare(
		`INSERT INTO narrator_blocks (id, message_id, ordinal, kind, subtype, phase, revision, wire_id,
			attempt_id, model_chars, byte_size, created_at, completed_at, model_text, payload, state)
		 VALUES (?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
	);
	const update = database.sqlite.prepare(
		`UPDATE narrator_blocks SET ordinal = ?, kind = ?, subtype = ?, phase = ?, revision = ?,
			wire_id = ?, model_chars = ?, byte_size = ?,
			completed_at = CASE WHEN ? = 'open' THEN NULL ELSE coalesce(completed_at, ?) END,
			model_text = ?, payload = ?, state = ?
		 WHERE id = ?`,
	);
	const clearAssets = database.sqlite.prepare(
		"DELETE FROM narrator_block_assets WHERE block_id = ?",
	);
	const addAsset = database.sqlite.prepare(
		"INSERT OR IGNORE INTO narrator_block_assets (block_id, asset_id, role) VALUES (?, ?, ?)",
	);
	const bindLedger = database.sqlite.prepare(
		`UPDATE narrator_tool_calls SET block_id = ?
		 WHERE message_id = ? AND tool_use_id = ? AND is_internal = 0 AND block_id IS NOT ?`,
	);
	for (const plan of plans) {
		const { draft, payload, state, byteSize, output, current } = plan;
		const chars = plan.modelChars;
		const bodyChanged =
			!current || current.payload !== payload || current.modelText !== draft.modelText;
		const changed =
			bodyChanged ||
			current.state !== state ||
			current.kind !== draft.kind ||
			current.subtype !== draft.subtype ||
			current.phase !== plan.phase ||
			current.wireId !== draft.wireId ||
			current.ordinal !== plan.ordinal;
		if (!current) {
			insert.run(
				plan.id,
				messageId,
				plan.ordinal,
				draft.kind,
				draft.subtype,
				plan.phase,
				draft.wireId,
				stamp.attemptId ?? null,
				chars,
				byteSize,
				now,
				plan.phase === "open" ? null : now,
				draft.modelText,
				payload,
				state,
			);
		} else if (changed) {
			const revision =
				current.revision +
				(bodyChanged || (current.phase === "open" && plan.phase !== "open") ? 1 : 0);
			update.run(
				plan.ordinal,
				draft.kind,
				draft.subtype,
				plan.phase,
				revision,
				draft.wireId,
				chars,
				byteSize,
				plan.phase,
				now,
				draft.modelText,
				payload,
				state,
				plan.id,
			);
		}
		if (!current || changed || output !== undefined) {
			clearAssets.run(plan.id);
			for (const [assetId, role] of blockAssets(draft, output))
				addAsset.run(plan.id, assetId, role);
		}
		if (draft.kind === "tool" && draft.wireId)
			bindLedger.run(plan.id, messageId, draft.wireId, plan.id);
		// Searchable once it stops streaming (§4.6): a step's open items are not indexed yet.
		if (plan.phase === "open") dropBlockIndex(plan.id);
		else if (!current || changed || output !== undefined)
			writeBlockIndex(plan.id, blockIndexText(draft.canonical, output));
	}
	database.sqlite
		.prepare("UPDATE narrator_messages SET body_bytes = ? WHERE id = ? AND body_bytes <> ?")
		.run(planned.bodyBytes, messageId, planned.bodyBytes);
}

// === State-only writes (§5.4) ===

const utf8 = (text: string | null) => (text ? Buffer.byteLength(text) : 0);

/** The columns the decoder derives from a block's elements, which the model reads. */
function sameDerivedColumns(before: BlockDraft[], after: BlockDraft[]): boolean {
	return (
		before.length === after.length &&
		before.every((draft, index) => {
			const next = after[index];
			return (
				draft.key === next.key &&
				draft.kind === next.kind &&
				draft.subtype === next.subtype &&
				draft.phase === next.phase &&
				draft.wireId === next.wireId &&
				draft.modelText === next.modelText
			);
		})
	);
}

/**
 * Write the new `state` of the rows a state-only write changed, in place. Rows that do not
 * line up with the content (a sync still owed) are rebuilt from it instead.
 */
function writeBlockStates(messageId: string, drafts: BlockDraft[]): void {
	// A sync still owed rebuilds the rows from content_json, state included — unless the
	// envelope is rows-only: its column holds nothing, so its rows take the state now.
	if (
		database.sqlite
			.prepare(
				`SELECT 1 FROM narrator_block_sync_queue q JOIN narrator_messages m ON m.id = q.message_id
				 WHERE q.message_id = ? AND m.body_rows_only = 0`,
			)
			.get(messageId)
	)
		return;
	const rows = database.sqlite
		.prepare("SELECT id, kind, state FROM narrator_blocks WHERE message_id = ? ORDER BY ordinal")
		.all(messageId) as Array<{ id: string; kind: string; state: string | null }>;
	const payloadOf = database.sqlite.prepare("SELECT payload FROM narrator_blocks WHERE id = ?");
	const changes: Array<{ id: string; state: string | null; delta: number }> = [];
	const aligned =
		rows.length === drafts.length &&
		rows.every((row, index) => {
			const draft = drafts[index];
			if (row.kind !== draft.kind) return false;
			const state = draft.state === null ? null : JSON.stringify(draft.state);
			if (row.state === state) return true;
			// Only the rows whose state moves are read in full.
			const { payload } = payloadOf.get(row.id) as { payload: string };
			if (payload !== JSON.stringify(draft.payload)) return false;
			changes.push({ id: row.id, state, delta: utf8(state) - utf8(row.state) });
			return true;
		});
	if (!aligned) {
		syncEnvelopeNow(messageId);
		return;
	}
	const writeState = database.sqlite.prepare(
		"UPDATE narrator_blocks SET state = ?, byte_size = byte_size + ? WHERE id = ?",
	);
	for (const change of changes) writeState.run(change.state, change.delta, change.id);
	const delta = changes.reduce((sum, change) => sum + change.delta, 0);
	if (delta)
		database.sqlite
			.prepare("UPDATE narrator_messages SET body_bytes = body_bytes + ? WHERE id = ?")
			.run(delta, messageId);
}

/**
 * A state-only write (§5.4): reader-only fields of an envelope's elements, the ones
 * `splitEnvelope` keeps in a block's `state` (a translation, a card's status). One
 * transaction: `content_json`, the complete compatibility projection, with
 * `state_revision` moved, which contract v3 neither counts nor queues; and the `state` of
 * the block rows it changes, written in place. No revision bump, no COW (a shared envelope
 * is patched for every holder), no search index.
 *
 * `mutate` gets a copy of the stored content and returns the new content, or null to
 * leave it. A change that is not state-only is written as the body change it is: a field
 * outside the state, or a state field the decoder derives a model-visible column from (a
 * compact's status sets its phase). Returns whether anything was written.
 */
export function patchEnvelopeState(
	messageId: string,
	mutate: (content: unknown[]) => unknown[] | null,
): boolean {
	return database.sqlite.transaction(() => {
		const row = envelopeRow(messageId);
		if (!row) return false;
		const content = envelopeBody(messageId);
		if (!Array.isArray(content)) return false;
		const next = mutate(structuredClone(content));
		if (!next) return false;
		const json = JSON.stringify(next);
		if (json === JSON.stringify(content)) return false;
		const message = {
			id: row.id,
			narratorId: row.narratorId,
			role: row.role,
			contentText: row.contentText,
			parentToolUseId: row.parentToolUseId,
			messageUuid: row.messageUuid,
			origin: row.origin,
			provider: row.provider,
		};
		const after = splitEnvelope({ ...message, contentJson: next }).blocks;
		const stateOnly =
			isStateOnlyChange(content, next) &&
			sameDerivedColumns(splitEnvelope({ ...message, contentJson: content }).blocks, after);
		if (!stateOnly) {
			database.sqlite
				.prepare("UPDATE narrator_messages SET content_json = ? WHERE id = ?")
				.run(json, messageId);
			if (row.bodyFormat === 2) syncEnvelopeNow(messageId, "keep", {}, next);
			return true;
		}
		database.sqlite
			.prepare(
				"UPDATE narrator_messages SET content_json = ?, state_revision = state_revision + 1 WHERE id = ?",
			)
			.run(json, messageId);
		if (row.bodyFormat === 2 && hasBlockSchema()) writeBlockStates(messageId, after);
		return true;
	})();
}

/**
 * An envelope's stored content block by block, each marked by whether `attemptId`
 * created it (§5.3). Null when the envelope keeps no block rows (a legacy-format
 * envelope, or a database without block storage): the caller falls back to positions.
 * The caller syncs the envelope first, in the same transaction.
 */
export function contentByAttempt(
	messageId: string,
	attemptId: string,
): Array<{ fromAttempt: boolean; toolUseId: string | null; elements: unknown[] }> | null {
	if (!hasBlockSchema()) return null;
	const rows = database.sqlite
		.prepare(
			`SELECT b.attempt_id AS attemptId, b.kind, b.wire_id AS wireId, b.payload, b.state
			 FROM narrator_blocks b JOIN narrator_messages m ON m.id = b.message_id
			 WHERE b.message_id = ? AND m.body_format = 2 ORDER BY b.ordinal`,
		)
		.all(messageId) as Array<{
		attemptId: string | null;
		kind: string;
		wireId: string | null;
		payload: string;
		state: string | null;
	}>;
	if (!rows.length) return null;
	return rows.map((row) => ({
		fromAttempt: row.attemptId === attemptId,
		toolUseId: row.kind === "tool" ? row.wireId : null,
		elements: assembleContent([{ payload: parse(row.payload), state: parse(row.state) }]),
	}));
}

/**
 * Build a copied envelope's blocks now, from the body the copy was written with, and
 * point them at the blocks they copy (§6.3).
 */
export function syncCopiedEnvelope(
	fromMessageId: string,
	toMessageId: string,
	content?: unknown,
): void {
	if (!hasBlockSchema()) return;
	syncEnvelopeNow(toMessageId, "keep", {}, content);
	inheritBlockLineage(fromMessageId, toMessageId);
}

/**
 * Point a copy's blocks at the blocks they were copied from (§6.3): each block of the copy
 * that continues one of the original (`matchBlocks`) keeps its lineage root, attempt,
 * creation time and pre-edit body.
 */
export function inheritBlockLineage(fromMessageId: string, toMessageId: string): void {
	const source = lineageRows(fromMessageId).filter((row) => !retiredRows.has(row.id));
	if (!source.length) return;
	const copy = lineageRows(toMessageId);
	const envelope = envelopeOf(toMessageId);
	if (!envelope) return;
	const continued = matchBlocks(keyed(envelope, source), keyed(envelope, copy));
	const inherit = database.sqlite.prepare(
		`UPDATE narrator_blocks SET origin_block_id = ?, attempt_id = ?, created_at = ?,
			original_payload = coalesce(original_payload, ?)
		 WHERE id = ? AND (origin_block_id IS NOT ? OR attempt_id IS NOT ? OR created_at IS NOT ?
			OR (original_payload IS NULL AND ? IS NOT NULL))`,
	);
	for (const [index, row] of copy.entries()) {
		const at = continued[index];
		if (at === undefined) continue;
		const from = source[at];
		const origin = from.originBlockId ?? from.id;
		inherit.run(
			origin,
			from.attemptId,
			from.createdAt,
			from.originalPayload,
			row.id,
			origin,
			from.attemptId,
			from.createdAt,
			from.originalPayload,
		);
	}
}

function envelopeOf(messageId: string): Omit<DecodableMessage, "contentJson"> | null {
	return database.sqlite
		.prepare(
			`SELECT id, narrator_id AS narratorId, role, content_text AS contentText,
				parent_tool_use_id AS parentToolUseId, origin FROM narrator_messages WHERE id = ?`,
		)
		.get(messageId) as Omit<DecodableMessage, "contentJson"> | null;
}

/** An envelope's block rows with what a copy inherits from each, in ordinal order. */
function lineageRows(messageId: string): Array<{
	id: string;
	payload: string;
	state: string | null;
	originBlockId: string | null;
	attemptId: string | null;
	createdAt: string;
	originalPayload: string | null;
}> {
	return database.sqlite
		.prepare(
			`SELECT id, payload, state, origin_block_id AS originBlockId, attempt_id AS attemptId,
				created_at AS createdAt, original_payload AS originalPayload
			 FROM narrator_blocks WHERE message_id = ? ORDER BY ordinal`,
		)
		.all(messageId) as ReturnType<typeof lineageRows>;
}

// === Block search index (§4.6) ===

function writeBlockIndex(blockId: string, text: string): void {
	replaceBlockIndexText(database.sqlite, blockId, text);
}

function dropBlockIndex(blockId: string): void {
	removeBlockIndexText(database.sqlite, blockId);
}
