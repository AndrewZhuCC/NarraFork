/**
 * Read side of the block jobs (docs/BLOCK_STORAGE-DESIGN.md §10.3), run in a read-only
 * db-worker so the main thread never decodes old message bodies:
 *
 *   - `blockBackfillSlice` plans the block rows of the next legacy (`body_format = 1`)
 *     envelopes; the main thread only inserts them.
 *   - `blockVerifySlice` checks block-format envelopes' rows against their stored content
 *     and names the ones that differ.
 *
 * Each slice stops at whichever limit comes first: envelope count, source bytes, or time.
 * Like every worker module, this must not import `server/db/index.ts`.
 */

import type { Database } from "bun:sqlite";
import {
	adoptedLedger,
	blockAssets,
	blockIndexText,
	type PlanLedgerRow,
	parseStored,
	planEnvelopeBlocks,
} from "@shared/narrator-blocks/plan";
import { assembleContent, inAssembledOrder } from "@shared/narrator-blocks/storage";
import type { EnvelopeRole } from "@shared/narrator-blocks/types";

export interface BlockSliceLimits {
	afterRowid: number;
	maxEnvelopes: number;
	maxSourceBytes: number;
	budgetMs: number;
}

export interface BlockBackfillSliceParams extends BlockSliceLimits {
	kind: "blockBackfillSlice";
}

export interface BlockVerifySliceParams extends BlockSliceLimits {
	kind: "blockVerifySlice";
}

/** One planned row, ready to insert. */
export interface BackfillBlockRow {
	kind: string;
	subtype: string | null;
	phase: string;
	wireId: string | null;
	modelText: string | null;
	payload: string;
	state: string | null;
	modelChars: number;
	byteSize: number;
	/** Text for the block search index; null while the block is open (§4.6). */
	indexText: string | null;
	assets: Array<[assetId: string, role: string]>;
}

export interface BackfillEnvelope {
	id: string;
	rowid: number;
	createdAt: string;
	/** What the plan was read at; the apply skips an envelope that changed since. */
	bodyRevision: number;
	/** A state-only write (§5.4) moves this, not `bodyRevision`. */
	stateRevision: number;
	ledgerFingerprint: string;
	bodyBytes: number;
	/** Null when the stored content could not be read as an array of elements. */
	blocks: BackfillBlockRow[] | null;
}

export interface BlockBackfillSliceResult {
	envelopes: BackfillEnvelope[];
	/** The last rowid the slice looked at; the job resumes after it. */
	lastRowid: number;
	/** No envelopes are left after `lastRowid`. */
	done: boolean;
}

export interface BlockVerifySliceResult {
	/** Block-format envelopes whose rows do not assemble to their stored content. */
	mismatched: string[];
	checked: number;
	lastRowid: number;
	done: boolean;
}

interface EnvelopeSource {
	rowid: number;
	id: string;
	narratorId: string;
	role: EnvelopeRole;
	contentJson: string | null;
	contentText: string | null;
	parentToolUseId: string | null;
	messageUuid: string | null;
	origin: string | null;
	provider: string | null;
	createdAt: string;
	bodyRevision: number;
	stateRevision: number;
}

/** Rowids and sizes of the next candidates; bodies are read one at a time within the budget. */
function candidates(
	db: Database,
	params: BlockSliceLimits,
	bodyFormat: 1 | 2,
): Array<{ rowid: number; bytes: number }> {
	return db
		.query(
			// A literal format, so convert's candidates come from idx_messages_legacy_body.
			`SELECT rowid, coalesce(length(CAST(content_json AS BLOB)), 0) AS bytes
			 FROM narrator_messages WHERE rowid > ? AND body_format = ${bodyFormat === 1 ? 1 : 2}
			 ORDER BY rowid LIMIT ?`,
		)
		.all(params.afterRowid, params.maxEnvelopes) as Array<{
		rowid: number;
		bytes: number;
	}>;
}

/** Walk candidates within the slice's byte and time budget; at least one is always taken. */
function withinBudget<T>(
	db: Database,
	params: BlockSliceLimits,
	bodyFormat: 1 | 2,
	visit: (rowid: number) => T,
): { results: T[]; lastRowid: number; done: boolean } {
	const started = performance.now();
	const rows = candidates(db, params, bodyFormat);
	const results: T[] = [];
	let lastRowid = params.afterRowid;
	let bytes = 0;
	for (const row of rows) {
		if (
			results.length > 0 &&
			(bytes + row.bytes > params.maxSourceBytes || performance.now() - started > params.budgetMs)
		)
			return { results, lastRowid, done: false };
		results.push(visit(row.rowid));
		bytes += row.bytes;
		lastRowid = row.rowid;
	}
	return { results, lastRowid, done: rows.length < params.maxEnvelopes };
}

function envelopeAt(db: Database, rowid: number): EnvelopeSource {
	return db
		.query(
			`SELECT rowid, id, narrator_id AS narratorId, role, content_json AS contentJson,
				content_text AS contentText, parent_tool_use_id AS parentToolUseId,
				sdk_message_uuid AS messageUuid, origin, provider, created_at AS createdAt,
				body_revision AS bodyRevision, state_revision AS stateRevision
			 FROM narrator_messages WHERE rowid = ?`,
		)
		.get(rowid) as EnvelopeSource;
}

function ledgerOf(db: Database, messageId: string): PlanLedgerRow[] {
	return db
		.query(
			`SELECT tool_use_id AS toolUseId, tool_name AS toolName, input_json AS inputJson,
				output_json AS outputJson, status
			 FROM narrator_tool_calls WHERE message_id = ? AND is_internal = 0
			 ORDER BY tool_use_id, execution_attempt, created_at, id`,
		)
		.all(messageId) as PlanLedgerRow[];
}

/** Changes whenever a ledger row of the message is added, removed or rewritten. */
export function ledgerFingerprint(db: Database, messageId: string): string {
	const row = db
		.query(
			`SELECT count(*) AS n, coalesce(sum(model_revision), 0) AS revisions
			 FROM narrator_tool_calls WHERE message_id = ?`,
		)
		.get(messageId) as { n: number; revisions: number };
	return `${row.n}:${row.revisions}`;
}

function planBackfill(db: Database, rowid: number): BackfillEnvelope {
	const source = envelopeAt(db, rowid);
	const base = {
		id: source.id,
		rowid: source.rowid,
		createdAt: source.createdAt,
		bodyRevision: source.bodyRevision,
		stateRevision: source.stateRevision,
		ledgerFingerprint: ledgerFingerprint(db, source.id),
	};
	const content = parseStored(source.contentJson);
	if (!Array.isArray(content)) return { ...base, bodyBytes: 0, blocks: null };
	const planned = planEnvelopeBlocks(
		{
			id: source.id,
			narratorId: source.narratorId,
			role: source.role,
			contentJson: content,
			contentText: source.contentText,
			parentToolUseId: source.parentToolUseId,
			messageUuid: source.messageUuid,
			origin: source.origin,
			provider: source.provider,
		},
		adoptedLedger(ledgerOf(db, source.id)),
	);
	return {
		...base,
		bodyBytes: planned.bodyBytes,
		blocks: planned.blocks.map((block) => ({
			kind: block.draft.kind,
			subtype: block.draft.subtype,
			phase: block.draft.phase,
			wireId: block.draft.wireId,
			modelText: block.draft.modelText,
			payload: block.payload,
			state: block.state,
			modelChars: block.modelChars,
			byteSize: block.byteSize,
			indexText:
				block.draft.phase === "open" ? null : blockIndexText(block.draft.canonical, block.output),
			assets: blockAssets(block.draft, block.output),
		})),
	};
}

export function runBlockBackfillSlice(
	db: Database,
	params: BlockBackfillSliceParams,
): BlockBackfillSliceResult {
	const { results, lastRowid, done } = withinBudget(db, params, 1, (rowid) =>
		planBackfill(db, rowid),
	);
	return { envelopes: results, lastRowid, done };
}

/** Whether a block-format envelope's rows assemble to exactly what it stores. */
function rowsMatch(db: Database, rowid: number): { id: string; matches: boolean } {
	const envelope = db
		.query("SELECT id, content_json AS contentJson FROM narrator_messages WHERE rowid = ?")
		.get(rowid) as { id: string; contentJson: string | null };
	const stored = parseStored(envelope.contentJson);
	// Non-array content keeps no rows by design.
	if (!Array.isArray(stored)) return { id: envelope.id, matches: true };
	const rows = db
		.query("SELECT payload, state FROM narrator_blocks WHERE message_id = ? ORDER BY ordinal")
		.all(envelope.id) as Array<{ payload: string; state: string | null }>;
	const assembled = assembleContent(
		rows.map((row) => ({ payload: parseStored(row.payload), state: parseStored(row.state) })),
	);
	return {
		id: envelope.id,
		matches: JSON.stringify(assembled) === JSON.stringify(inAssembledOrder(stored)),
	};
}

export function runBlockVerifySlice(
	db: Database,
	params: BlockVerifySliceParams,
): BlockVerifySliceResult {
	const { results, lastRowid, done } = withinBudget(db, params, 2, (rowid) => rowsMatch(db, rowid));
	return {
		mismatched: results.filter((result) => !result.matches).map((result) => result.id),
		checked: results.length,
		lastRowid,
		done,
	};
}
