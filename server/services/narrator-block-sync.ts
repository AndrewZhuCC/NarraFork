/**
 * narrator-block-sync.ts — keep an envelope's `narrator_blocks` rows equal to its stored
 * content (docs/BLOCK_STORAGE-DESIGN.md §4, P1).
 *
 * In P1 every reader still reads `content_json`; envelopes written with `body_format = 2`
 * additionally carry their blocks (`shared/narrator-blocks/storage.ts` maps one onto the
 * other losslessly). `syncEnvelope` brings the rows in line with the content and ledger
 * inside the caller's transaction:
 *   - rows keep their id across rewrites (blocks are matched by key, not by position),
 *     so attempt ids, lineage and timestamps survive a checkpoint or an edit;
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

import {
	isBrokenToolOutput,
	outputToText,
	toolOutputImages,
} from "@shared/agent-protocol/tool-output";
import { INTERNAL_READ_TOOL_USE_PREFIX } from "@shared/narrator-blocks/decode";
import { assembleContent, type BlockDraft, splitEnvelope } from "@shared/narrator-blocks/storage";
import type { CanonicalBlock, EnvelopeRole } from "@shared/narrator-blocks/types";
import { sqlite } from "../db";
import { removeBlockIndexText, replaceBlockIndexText } from "../db/fts";
import { generateId } from "../lib/id";
import { logger } from "../lib/logger";
import { settings } from "../lib/settings";
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
	blockSchema ??= !!sqlite
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
		sqlite
			.prepare("SELECT message_id AS id FROM narrator_block_sync_queue LIMIT ?")
			.all(limit) as Array<{ id: string }>
	).map((row) => row.id);
	const done = sqlite.prepare("DELETE FROM narrator_block_sync_queue WHERE message_id = ?");
	for (const id of ids) {
		syncEnvelope(id);
		done.run(id);
	}
	return ids.length;
}

/** Sync one envelope now and take it off the queue. */
export function syncEnvelopeNow(
	messageId: string,
	mode: SyncMode = "keep",
	stamp: BlockStamp = {},
): void {
	if (!hasBlockSchema()) return;
	syncEnvelope(messageId, mode, stamp);
	sqlite.prepare("DELETE FROM narrator_block_sync_queue WHERE message_id = ?").run(messageId);
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
		const batch = sqlite.transaction(() => flushChangedEnvelopes(SWEEP_BATCH))();
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
	const interrupted = sqlite
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
 * Dual-write check (P1 exit criterion): the newest block-format envelopes must assemble
 * from their rows to exactly what they store. A mismatch is logged and re-synced.
 * Returns the ids that differed.
 */
export function auditRecentEnvelopes(limit = AUDIT_ENVELOPES): string[] {
	if (!hasBlockSchema()) return [];
	const rows = sqlite
		.prepare(
			`SELECT id, content_json AS contentJson FROM narrator_messages
			 WHERE body_format = 2 ORDER BY rowid DESC LIMIT ?`,
		)
		.all(limit) as Array<{ id: string; contentJson: string | null }>;
	const differing = rows.flatMap((row) => {
		const stored = parse(row.contentJson);
		if (!Array.isArray(stored)) return [];
		const blocks = sqlite
			.prepare("SELECT payload, state FROM narrator_blocks WHERE message_id = ? ORDER BY ordinal")
			.all(row.id) as Array<{ payload: string; state: string | null }>;
		const assembled = assembleContent(
			blocks.map((block) => ({ payload: parse(block.payload), state: parse(block.state) })),
		);
		return JSON.stringify(assembled) === JSON.stringify(stored) ? [] : [row.id];
	});
	if (differing.length) {
		logger.warn("Block rows differed from their envelope; re-synced", { messageIds: differing });
		sqlite.transaction(() => {
			for (const id of differing) syncEnvelopeNow(id);
		})();
	}
	return differing;
}

// === Sync ===

interface EnvelopeRow {
	id: string;
	narratorId: string;
	role: EnvelopeRole;
	contentJson: string | null;
	contentText: string | null;
	parentToolUseId: string | null;
	messageUuid: string | null;
	origin: string | null;
	provider: string | null;
	bodyFormat: number;
}

interface LedgerRow {
	toolUseId: string;
	toolName: string;
	inputJson: string | null;
	outputJson: string | null;
	status: string;
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

function parse(value: string | null): unknown {
	if (value === null) return null;
	try {
		return JSON.parse(value);
	} catch {
		return value;
	}
}

function stored(value: unknown): string | null {
	return value === null || value === undefined ? null : JSON.stringify(value);
}

/** Characters this block puts in front of the model. */
function modelChars(block: CanonicalBlock | null): number {
	if (!block) return 0;
	switch (block.kind) {
		case "input":
		case "context":
			return block.modelText?.length ?? 0;
		case "output":
		case "reasoning":
			return block.payload.parts.reduce(
				(sum, part) => sum + (part.type === "text" ? part.text.length : 0),
				0,
			);
		case "tool":
			return JSON.stringify(block.payload.input ?? null).length;
	}
}

const INDEX_RESULT_CHARS = 64 * 1024;

/** The text a block is found by (§4.6). */
export function blockIndexText(block: CanonicalBlock | null, adoptedOutput?: unknown): string {
	if (!block) return "";
	switch (block.kind) {
		case "input":
		case "context":
			if (block.modelText) return block.modelText;
			if (block.kind === "context") {
				const body = block.payload.body;
				return [body?.title, body?.text, body?.message, body?.summary]
					.filter((value): value is string => typeof value === "string")
					.join("\n");
			}
			return block.payload.parts.map((part) => (part.type === "text" ? part.text : "")).join("");
		case "output":
		case "reasoning":
			return block.payload.parts.map((part) => (part.type === "text" ? part.text : "")).join("");
		case "tool": {
			const result =
				adoptedOutput === undefined ? "" : outputToText(adoptedOutput).slice(0, INDEX_RESULT_CHARS);
			return [block.subtype, JSON.stringify(block.payload.input ?? null), result]
				.filter(Boolean)
				.join("\n");
		}
	}
}

/** Uploads and images a block references, by role (§4.1). */
function assetsOf(
	draft: BlockDraft,
	adoptedOutput: unknown,
): Array<[assetId: string, role: string]> {
	const assets: Array<[string, string]> = [];
	for (const element of draft.payload.raw) {
		if (!element || typeof element !== "object") continue;
		const raw = element as Record<string, unknown>;
		if (raw.type === "image" && typeof raw.imageId === "string")
			assets.push([raw.imageId, "input_image"]);
		else if (raw.type === "text_file" && typeof raw.fileId === "string")
			assets.push([raw.fileId, "input_file"]);
		else if (raw.type === "image_generation" && typeof raw.savedPath === "string")
			assets.push([raw.savedPath, "output_image"]);
	}
	if (draft.kind === "tool" && !isBrokenToolOutput(adoptedOutput))
		for (const image of toolOutputImages(adoptedOutput)) assets.push([image.imageId, "tool_image"]);
	return [...new Map(assets.map((asset) => [asset[0], asset])).values()];
}

/** Adopted attempt per tool use: the newest execution attempt. */
function adoptedCalls(messageId: string): LedgerRow[] {
	const rows = sqlite
		.prepare(
			`SELECT tool_use_id AS toolUseId, tool_name AS toolName, input_json AS inputJson,
				output_json AS outputJson, status
			 FROM narrator_tool_calls WHERE message_id = ? AND is_internal = 0
			 ORDER BY tool_use_id, execution_attempt, created_at, id`,
		)
		.all(messageId) as LedgerRow[];
	const latest = new Map<string, LedgerRow>();
	for (const row of rows) {
		if (row.toolUseId.startsWith(INTERNAL_READ_TOOL_USE_PREFIX)) continue;
		latest.set(row.toolUseId, row);
	}
	return [...latest.values()];
}

/**
 * Bring one envelope's block rows in line with its stored content, inside the caller's
 * transaction. A no-op for legacy-format envelopes and for content that is not an array.
 */
export function syncEnvelope(
	messageId: string,
	mode: SyncMode = "keep",
	stamp: BlockStamp = {},
): void {
	const row = sqlite
		.prepare(
			`SELECT id, narrator_id AS narratorId, role, content_json AS contentJson,
				content_text AS contentText, parent_tool_use_id AS parentToolUseId,
				sdk_message_uuid AS messageUuid, origin, provider, body_format AS bodyFormat
			 FROM narrator_messages WHERE id = ?`,
		)
		.get(messageId) as EnvelopeRow | null;
	if (!row || row.bodyFormat !== 2) return;
	const content = parse(row.contentJson);
	if (!Array.isArray(content)) return;

	const ledger = adoptedCalls(messageId);
	const outputs = new Map(ledger.map((call) => [call.toolUseId, parse(call.outputJson)]));
	const message = {
		id: row.id,
		narratorId: row.narratorId,
		role: row.role,
		contentText: row.contentText,
		parentToolUseId: row.parentToolUseId,
		messageUuid: row.messageUuid,
		origin: row.origin,
		provider: row.provider,
		toolCalls: ledger.map((call) => ({
			toolUseId: call.toolUseId,
			toolName: call.toolName,
			inputJson: parse(call.inputJson),
			outputJson: parse(call.outputJson),
			status: call.status,
		})),
	};
	const drafts = splitEnvelope(
		{ ...message, contentJson: content },
		{
			streaming: mode === "streaming",
		},
	).blocks;

	const existing = sqlite
		.prepare(
			`SELECT id, ordinal, kind, subtype, phase, wire_id AS wireId, model_text AS modelText,
				payload, state, revision
			 FROM narrator_blocks WHERE message_id = ? ORDER BY ordinal`,
		)
		.all(messageId) as BlockRow[];
	// The rows' keys are what the codec makes of the content they assemble.
	const existingKeys = splitEnvelope({
		...message,
		contentJson: assembleContent(
			existing.map((block) => ({ payload: parse(block.payload), state: parse(block.state) })),
		),
	}).blocks.map((draft) => draft.key);
	const byKey = new Map<string, BlockRow>();
	if (existingKeys.length === existing.length)
		for (const [index, block] of existing.entries()) byKey.set(existingKeys[index], block);

	const now = new Date().toISOString();
	const kept = new Set<string>();
	const plans = drafts.map((draft, ordinal) => {
		const current = byKey.get(draft.key);
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
		return { draft, ordinal, current, phase, id: current?.id ?? generateId() };
	});

	const removed = existing.filter((block) => !kept.has(block.id));
	const deleteBlock = sqlite.prepare("DELETE FROM narrator_blocks WHERE id = ?");
	for (const block of removed) {
		dropBlockIndex(block.id);
		deleteBlock.run(block.id);
	}
	// Ordinals are unique per envelope: park every moving row before placing any.
	const park = sqlite.prepare("UPDATE narrator_blocks SET ordinal = ? WHERE id = ?");
	for (const plan of plans)
		if (plan.current && plan.current.ordinal !== plan.ordinal) park.run(-1 - plan.ordinal, plan.id);

	const insert = sqlite.prepare(
		`INSERT INTO narrator_blocks (id, message_id, ordinal, kind, subtype, phase, revision, wire_id,
			attempt_id, model_chars, byte_size, created_at, completed_at, model_text, payload, state)
		 VALUES (?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
	);
	const update = sqlite.prepare(
		`UPDATE narrator_blocks SET ordinal = ?, kind = ?, subtype = ?, phase = ?, revision = ?,
			wire_id = ?, model_chars = ?, byte_size = ?,
			completed_at = CASE WHEN ? = 'open' THEN NULL ELSE coalesce(completed_at, ?) END,
			model_text = ?, payload = ?, state = ?
		 WHERE id = ?`,
	);
	const clearAssets = sqlite.prepare("DELETE FROM narrator_block_assets WHERE block_id = ?");
	const addAsset = sqlite.prepare(
		"INSERT OR IGNORE INTO narrator_block_assets (block_id, asset_id, role) VALUES (?, ?, ?)",
	);
	const bindLedger = sqlite.prepare(
		`UPDATE narrator_tool_calls SET block_id = ?
		 WHERE message_id = ? AND tool_use_id = ? AND is_internal = 0 AND block_id IS NOT ?`,
	);
	let bodyBytes = 0;
	for (const plan of plans) {
		const { draft } = plan;
		const payload = JSON.stringify(draft.payload);
		const state = stored(draft.state);
		const byteSize =
			Buffer.byteLength(payload) +
			Buffer.byteLength(state ?? "") +
			Buffer.byteLength(draft.modelText ?? "");
		bodyBytes += byteSize;
		const chars = modelChars(draft.canonical);
		const output = draft.wireId ? outputs.get(draft.wireId) : undefined;
		const current = plan.current;
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
			for (const [assetId, role] of assetsOf(draft, output)) addAsset.run(plan.id, assetId, role);
		}
		if (draft.kind === "tool" && draft.wireId)
			bindLedger.run(plan.id, messageId, draft.wireId, plan.id);
		// Searchable once it stops streaming (§4.6): a step's open items are not indexed yet.
		if (plan.phase === "open") dropBlockIndex(plan.id);
		else if (!current || changed || output !== undefined)
			writeBlockIndex(plan.id, blockIndexText(draft.canonical, output));
	}
	sqlite
		.prepare("UPDATE narrator_messages SET body_bytes = ? WHERE id = ? AND body_bytes <> ?")
		.run(bodyBytes, messageId, bodyBytes);
}

/**
 * An envelope's stored content block by block, each marked by whether `attemptId`
 * created it (§5.3). Null when the envelope keeps no block rows (a legacy-format
 * envelope, or a database without block storage): the caller falls back to positions.
 */
export function contentByAttempt(
	messageId: string,
	attemptId: string,
): Array<{ fromAttempt: boolean; toolUseId: string | null; elements: unknown[] }> | null {
	if (!hasBlockSchema()) return null;
	// Converge first: what the rows say must be what the envelope stores now.
	syncEnvelopeNow(messageId);
	const rows = sqlite
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

/** Build a copied envelope's blocks now and point them at the blocks they copy (§6.3). */
export function syncCopiedEnvelope(fromMessageId: string, toMessageId: string): void {
	if (!hasBlockSchema()) return;
	syncEnvelopeNow(toMessageId);
	inheritBlockLineage(fromMessageId, toMessageId);
}

/**
 * Point a copy's blocks at the blocks they were copied from (§6.3): the copy keeps the
 * lineage root, the attempt and the creation time of each block it shares a key with.
 */
export function inheritBlockLineage(fromMessageId: string, toMessageId: string): void {
	const source = keyedRows(fromMessageId);
	if (!source.length) return;
	const byKey = new Map(source.map((row) => [row.key, row]));
	const inherit = sqlite.prepare(
		`UPDATE narrator_blocks SET origin_block_id = ?, attempt_id = ?, created_at = ?
		 WHERE id = ? AND (origin_block_id IS NOT ? OR attempt_id IS NOT ? OR created_at IS NOT ?)`,
	);
	for (const row of keyedRows(toMessageId)) {
		const from = byKey.get(row.key);
		if (!from) continue;
		const origin = from.originBlockId ?? from.id;
		inherit.run(
			origin,
			from.attemptId,
			from.createdAt,
			row.id,
			origin,
			from.attemptId,
			from.createdAt,
		);
	}
}

/** An envelope's block rows with the codec key of each, in ordinal order. */
function keyedRows(messageId: string): Array<{
	id: string;
	key: string;
	originBlockId: string | null;
	attemptId: string | null;
	createdAt: string;
}> {
	const envelope = sqlite
		.prepare(
			`SELECT id, narrator_id AS narratorId, role, content_text AS contentText,
				parent_tool_use_id AS parentToolUseId, origin FROM narrator_messages WHERE id = ?`,
		)
		.get(messageId) as Omit<EnvelopeRow, "contentJson" | "bodyFormat"> | null;
	if (!envelope) return [];
	const rows = sqlite
		.prepare(
			`SELECT id, payload, state, origin_block_id AS originBlockId, attempt_id AS attemptId,
				created_at AS createdAt
			 FROM narrator_blocks WHERE message_id = ? ORDER BY ordinal`,
		)
		.all(messageId) as Array<{
		id: string;
		payload: string;
		state: string | null;
		originBlockId: string | null;
		attemptId: string | null;
		createdAt: string;
	}>;
	const keys = splitEnvelope({
		...envelope,
		contentJson: assembleContent(
			rows.map((row) => ({ payload: parse(row.payload), state: parse(row.state) })),
		),
	}).blocks.map((draft) => draft.key);
	if (keys.length !== rows.length) return [];
	return rows.map((row, index) => ({ ...row, key: keys[index] }));
}

// === Block search index (§4.6) ===

function writeBlockIndex(blockId: string, text: string): void {
	replaceBlockIndexText(sqlite, blockId, text);
}

function dropBlockIndex(blockId: string): void {
	removeBlockIndexText(sqlite, blockId);
}
