/**
 * narrator-block-targets.ts — operations addressed by block id (docs/BLOCK_STORAGE-DESIGN.md
 * §5.5, P2).
 *
 * Every user operation names its target by block id. The history machinery underneath
 * (bounded deletion, COW, file revert) still edits an envelope's stored elements by
 * position, so a target resolves to the element range its block holds:
 *
 *   - a block row: its envelope's rows in ordinal order each hold `payload.raw` elements and
 *     assemble to the stored array (§10.1), so the range follows from the rows' lengths;
 *   - an envelope not converted yet: the ids the decoder gives its blocks (§8.1), matched
 *     through `splitEnvelope`, which groups the same elements the rows would. Decoded ids
 *     name positions, so they resolve only while the envelope is legacy format: once it
 *     has rows (converted, or materialized by a block-addressed mutation) a decoded id is
 *     stale, and a retried delete fails instead of removing the block that moved up.
 *
 * An envelope waiting in the sync queue is brought current first; the ids of unchanged
 * blocks survive a sync (`matchBlocks`). Only legacy envelopes and queued ones are parsed,
 * within one budget per call. Callers resolve inside the narrator's admission, so the
 * ranges cannot move before the mutation reads them.
 */

import { parseStored } from "@shared/narrator-blocks/plan";
import { assembleContent, splitEnvelope } from "@shared/narrator-blocks/storage";
import type { EnvelopeRole } from "@shared/narrator-blocks/types";
import * as database from "../db";
import { AppError, NotFoundError, ValidationError } from "../lib/errors";
import { settings } from "../lib/settings";
import { MAX_BATCH_DELETE_BLOCKS } from "../lib/validators/narrators";
import { syncEnvelopeNow } from "./narrator-block-sync";

/** Envelopes larger than this are not decoded on the main thread to resolve a target. */
const TARGET_ENVELOPE_BYTES = 16 * 1024 * 1024;
/** Stored content one resolve may parse in all (legacy envelopes, queued syncs). */
const TARGET_PARSE_BYTES = 32 * 1024 * 1024;
/**
 * Most elements one delete by block id removes. The blocks are capped at
 * `MAX_BATCH_DELETE_BLOCKS` and the history they touch by the deletion budgets; this only
 * bounds the position list handed to them.
 */
export const MAX_BLOCK_DELETE_ELEMENTS = 4096;

export interface BlockTarget {
	/** The id the caller named (a row id, or a decoded id of an unconverted envelope). */
	blockId: string;
	/** The block row's id; null while its envelope is not converted. */
	rowId: string | null;
	messageId: string;
	/** The narrator's ref position of the envelope. */
	seq: number;
	role: EnvelopeRole;
	kind: string;
	subtype: string | null;
	/** A tool block's call id. */
	wireId: string | null;
	/** The elements of the stored array the block holds: `[from, to)`. */
	from: number;
	to: number;
}

interface LayoutBlock {
	rowId: string | null;
	decodedId: string | null;
	kind: string;
	subtype: string | null;
	wireId: string | null;
	from: number;
	to: number;
}

interface EnvelopeHead {
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

/** What one resolve may still parse; each envelope it decodes or syncs draws on it. */
interface ParseBudget {
	bytes: number;
}

const newBudget = (): ParseBudget => ({ bytes: TARGET_PARSE_BYTES });

function readHead(messageId: string): EnvelopeHead {
	const row = database.sqlite
		.prepare(
			`SELECT id, narrator_id AS narratorId, role, content_text AS contentText,
				parent_tool_use_id AS parentToolUseId, sdk_message_uuid AS messageUuid, origin,
				provider, body_format AS bodyFormat
			 FROM narrator_messages WHERE id = ?`,
		)
		.get(messageId) as EnvelopeHead | null;
	if (!row) throw new NotFoundError("Message", messageId);
	return row;
}

/** Charge an envelope's stored size to the budget before anything parses it. */
function chargeParse(messageId: string, budget: ParseBudget): void {
	const { bytes } = database.sqlite
		.prepare(
			`SELECT coalesce(length(CAST(content_json AS BLOB)), 0) AS bytes
			 FROM narrator_messages WHERE id = ?`,
		)
		.get(messageId) as { bytes: number };
	budget.bytes -= bytes;
	if (bytes > TARGET_ENVELOPE_BYTES || budget.bytes < 0)
		throw new AppError(
			"The message is too large to address by block",
			413,
			"BLOCK_TARGET_TOO_LARGE",
		);
}

/** An envelope's stored array, charged to the budget. */
function readContent(messageId: string, budget: ParseBudget): unknown {
	chargeParse(messageId, budget);
	const row = database.sqlite
		.prepare("SELECT content_json AS contentJson FROM narrator_messages WHERE id = ?")
		.get(messageId) as { contentJson: string | null };
	return parseStored(row.contentJson);
}

/** The envelope's blocks as the decoder groups its stored elements. */
function splitLayout(envelope: EnvelopeHead, budget: ParseBudget): LayoutBlock[] {
	const content = readContent(envelope.id, budget);
	if (!Array.isArray(content)) return [];
	const { blocks } = splitEnvelope({
		id: envelope.id,
		narratorId: envelope.narratorId,
		role: envelope.role,
		contentJson: content,
		contentText: envelope.contentText,
		parentToolUseId: envelope.parentToolUseId,
		messageUuid: envelope.messageUuid,
		origin: envelope.origin,
		provider: envelope.provider,
	});
	let from = 0;
	return blocks.map((draft) => {
		const to = from + draft.payload.raw.length;
		const block: LayoutBlock = {
			rowId: null,
			decodedId: draft.canonical?.id ?? null,
			kind: draft.kind,
			subtype: draft.subtype,
			wireId: draft.wireId,
			from,
			to,
		};
		from = to;
		return block;
	});
}

/** The envelope's block rows with the element range each holds. */
function rowLayout(messageId: string): LayoutBlock[] {
	const rows = database.sqlite
		.prepare(
			`SELECT id, kind, subtype, wire_id AS wireId,
				coalesce(json_array_length(payload, '$.raw'), 0) AS length
			 FROM narrator_blocks WHERE message_id = ? ORDER BY ordinal`,
		)
		.all(messageId) as Array<{
		id: string;
		kind: string;
		subtype: string | null;
		wireId: string | null;
		length: number;
	}>;
	let from = 0;
	return rows.map((row) => {
		const to = from + row.length;
		const block: LayoutBlock = { ...row, rowId: row.id, decodedId: null, from, to };
		from = to;
		return block;
	});
}

/** Rebuild a queued envelope's rows from what it stores now (synchronous, in a transaction). */
function syncQueued(messageId: string): void {
	syncEnvelopeNow(messageId);
}

function isQueued(messageId: string): boolean {
	return !!database.sqlite
		.prepare("SELECT 1 FROM narrator_block_sync_queue WHERE message_id = ?")
		.get(messageId);
}

/**
 * The blocks of an envelope as they are addressed now: its rows when it is block format
 * (synced first if a write outside the store left it queued), else the decoder's.
 */
function layoutOf(envelope: EnvelopeHead, budget: ParseBudget): LayoutBlock[] {
	if (envelope.bodyFormat !== 2) return splitLayout(envelope, budget);
	if (isQueued(envelope.id)) {
		chargeParse(envelope.id, budget);
		database.sqlite.transaction(() => syncQueued(envelope.id))();
	}
	return rowLayout(envelope.id);
}

/**
 * The envelope a decoded id names among the narrator's: decoded ids are
 * `${messageId}:${suffix}`, and the suffix holds colons too (`:call:…`).
 */
function decodedMessageId(narratorId: string, blockId: string): string | null {
	const prefixes: string[] = [];
	for (let colon = blockId.indexOf(":"); colon > 0; colon = blockId.indexOf(":", colon + 1))
		prefixes.push(blockId.slice(0, colon));
	if (!prefixes.length) return null;
	const row = database.sqlite
		.prepare(
			`SELECT message_id AS messageId FROM narrator_message_refs
			 WHERE narrator_id = ? AND message_id IN (${prefixes.map(() => "?").join(",")})
			 ORDER BY length(message_id) DESC LIMIT 1`,
		)
		.get(narratorId, ...prefixes) as { messageId: string } | null;
	return row?.messageId ?? null;
}

/**
 * Resolve block ids the narrator can see to the element ranges they hold, in the order
 * given. Throws when an id is unknown or names a block outside the narrator's history.
 */
export function resolveBlockTargets(
	narratorId: string,
	blockIds: readonly string[],
): BlockTarget[] {
	const unique = [...new Set(blockIds)];
	if (unique.length === 0) return [];
	if (unique.length > MAX_BATCH_DELETE_BLOCKS)
		throw new ValidationError(`Select at most ${MAX_BATCH_DELETE_BLOCKS} blocks`);
	const rowMessages = new Map(
		(
			database.sqlite
				.prepare(
					`SELECT id, message_id AS messageId FROM narrator_blocks
					 WHERE id IN (${unique.map(() => "?").join(",")})`,
				)
				.all(...unique) as Array<{ id: string; messageId: string }>
		).map((row) => [row.id, row.messageId]),
	);
	const byMessage = new Map<string, string[]>();
	for (const blockId of unique) {
		const messageId = rowMessages.get(blockId) ?? decodedMessageId(narratorId, blockId);
		if (!messageId) throw new NotFoundError("Block", blockId);
		byMessage.set(messageId, [...(byMessage.get(messageId) ?? []), blockId]);
	}
	const resolved = new Map<string, BlockTarget>();
	const refSeq = database.sqlite.prepare(
		"SELECT seq FROM narrator_message_refs WHERE narrator_id = ? AND message_id = ?",
	);
	const budget = newBudget();
	for (const [messageId, ids] of byMessage) {
		const ref = refSeq.get(narratorId, messageId) as { seq: number } | null;
		if (!ref) throw new NotFoundError("Block", ids[0]);
		const envelope = readHead(messageId);
		const layout = layoutOf(envelope, budget);
		for (const blockId of ids) {
			const block = layout.find(
				(candidate) => candidate.rowId === blockId || candidate.decodedId === blockId,
			);
			// A row id that a sync replaced, or a decoded id of an envelope that has rows now.
			if (!block)
				throw new AppError("Message history changed; reload and retry", 409, "BLOCK_TARGET_STALE");
			resolved.set(blockId, {
				blockId,
				rowId: block.rowId,
				messageId,
				seq: ref.seq,
				role: envelope.role,
				kind: block.kind,
				subtype: block.subtype,
				wireId: block.wireId,
				from: block.from,
				to: block.to,
			});
		}
	}
	return unique.map((blockId) => resolved.get(blockId) as BlockTarget);
}

/** Every element position the targets hold, as the index-addressed history API takes them. */
export function targetElements(
	targets: readonly BlockTarget[],
): Array<{ messageId: string; blockIndex: number }> {
	return targets.flatMap((target) =>
		Array.from({ length: target.to - target.from }, (_, offset) => ({
			messageId: target.messageId,
			blockIndex: target.from + offset,
		})),
	);
}

/**
 * Give an unconverted envelope its block rows now, so an operation that keeps per-block
 * data (an edit's original payload) has rows to keep it on. Block writes off is the
 * rollback switch: nothing becomes block format then.
 */
export function materializeEnvelope(messageId: string): void {
	if (!settings.narratorBlocks.write)
		throw new AppError(
			"Block storage writes are turned off; this message cannot be edited by block",
			409,
			"BLOCK_WRITES_OFF",
		);
	database.sqlite.transaction(() => {
		const flipped = database.sqlite
			.prepare("UPDATE narrator_messages SET body_format = 2 WHERE id = ? AND body_format = 1")
			.run(messageId).changes;
		if (flipped) syncEnvelopeNow(messageId);
	})();
}

/**
 * Give the targets' unconverted envelopes their rows before a mutation, so it removes rows
 * by id and its decoded ids go stale with it. With block writes off nothing converts: the
 * targets stay positions, as the index-addressed API has them.
 */
export function materializeTargets(targets: readonly BlockTarget[]): BlockTarget[] {
	if (!settings.narratorBlocks.write) return [...targets];
	const legacy = new Set(
		targets.filter((target) => !target.rowId).map((target) => target.messageId),
	);
	for (const messageId of legacy) materializeEnvelope(messageId);
	return targets.map((target) =>
		target.rowId ? target : { ...target, rowId: rowAt(target.messageId, target.from)?.id ?? null },
	);
}

/** The row holding the elements starting at `from` (rows keep their grouping across edits). */
export function rowAt(messageId: string, from: number): { id: string } | null {
	const block = rowLayout(messageId).find((candidate) => candidate.from === from);
	return block?.rowId ? { id: block.rowId } : null;
}

/**
 * Where "rollback to here" keeps through: the last element the targets hold, in history
 * order. Every target block stays whole, so a run rolled back to keeps all its blocks.
 */
export function lastTargetElement(targets: readonly BlockTarget[]): {
	messageId: string;
	blockIndex: number;
} {
	if (!targets.length) throw new ValidationError("Select at least one block");
	const last = targets.reduce((latest, target) =>
		target.seq > latest.seq || (target.seq === latest.seq && target.to > latest.to)
			? target
			: latest,
	);
	return { messageId: last.messageId, blockIndex: last.to - 1 };
}

/**
 * The position of a tool block's `tool_use` element, which the index-addressed revert
 * planner checks. Usually the block's first element, but an envelope's first block also
 * holds any elements before it that have no block of their own.
 */
export function toolElementIndex(target: BlockTarget): number {
	if (target.kind !== "tool" || !target.wireId) return target.from;
	if (target.rowId) {
		const row = database.sqlite
			.prepare(
				`SELECT min(element.key) AS offset
				 FROM narrator_blocks b, json_each(b.payload, '$.raw') AS element
				 WHERE b.id = ? AND json_extract(element.value, '$.type') = 'tool_use'
					AND json_extract(element.value, '$.id') = ?`,
			)
			.get(target.rowId, target.wireId) as { offset: number | null } | null;
		return target.from + (row?.offset ?? 0);
	}
	const content = readContent(target.messageId, newBudget());
	const elements = Array.isArray(content) ? content.slice(target.from, target.to) : [];
	const offset = elements.findIndex(
		(element) =>
			(element as { type?: unknown })?.type === "tool_use" &&
			(element as { id?: unknown }).id === target.wireId,
	);
	return target.from + Math.max(offset, 0);
}

/** Ceiling on one block's full body: the page truncates, this serves the rest on demand. */
const BLOCK_DETAIL_BYTES = 8 * 1024 * 1024;

export interface BlockDetail {
	id: string;
	messageId: string;
	kind: string;
	subtype: string | null;
	wireId: string | null;
	/** Null for a block of an unconverted envelope, which has no row yet. */
	phase: string | null;
	revision: number | null;
	/** The stored elements the block holds, reader-only fields included. */
	elements: unknown[];
	/** An edited output block's elements before its first edit. */
	originalElements: unknown[] | null;
}

/** One block in full: what its row assembles to, or its elements of an unconverted envelope. */
export function blockDetail(target: BlockTarget): BlockDetail {
	const base = {
		id: target.rowId ?? target.blockId,
		messageId: target.messageId,
		kind: target.kind,
		subtype: target.subtype,
		wireId: target.wireId,
	};
	if (!target.rowId) {
		const content = readContent(target.messageId, newBudget());
		const elements = Array.isArray(content) ? content.slice(target.from, target.to) : [];
		return { ...base, phase: null, revision: null, elements, originalElements: null };
	}
	const row = database.sqlite
		.prepare(
			`SELECT phase, revision, payload, state, original_payload AS originalPayload,
				length(CAST(payload AS BLOB)) + coalesce(length(CAST(state AS BLOB)), 0)
					+ coalesce(length(CAST(original_payload AS BLOB)), 0) AS bytes
			 FROM narrator_blocks WHERE id = ?`,
		)
		.get(target.rowId) as {
		phase: string;
		revision: number;
		payload: string;
		state: string | null;
		originalPayload: string | null;
		bytes: number;
	} | null;
	if (!row) throw new NotFoundError("Block", target.blockId);
	if (row.bytes > BLOCK_DETAIL_BYTES)
		throw new AppError("The block is too large to return whole", 413, "BLOCK_DETAIL_TOO_LARGE");
	const original = (parseStored(row.originalPayload) as { raw?: unknown } | null)?.raw;
	return {
		...base,
		phase: row.phase,
		revision: row.revision,
		elements: assembleContent([
			{ payload: parseStored(row.payload), state: parseStored(row.state) },
		]),
		originalElements: Array.isArray(original) ? original : null,
	};
}
