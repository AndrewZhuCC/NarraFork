/**
 * narrator-block-backfill.ts — the background block jobs (docs/BLOCK_STORAGE-DESIGN.md
 * §10.3). Both resume from a durable cursor in `narrator_block_backfill`, run slice by
 * slice with the event loop free in between, and can be paused from the storage page.
 *
 *   - `convert` gives every legacy (`body_format = 1`) envelope its block rows. A read-only
 *     db-worker decodes a slice; the main thread inserts it in one short transaction and
 *     flips the envelope to `body_format = 2` last. The rows go in while the envelope is
 *     still legacy, so no revision trigger fires: the model's history, its caches and the
 *     envelope search index are untouched. An envelope that changed after the worker read
 *     it is flipped without rows instead, and the store's sync builds them.
 *   - `verify` then walks block-format envelopes and records any whose rows do not
 *     assemble to its stored content in `narrator_block_diagnostics`, re-syncing it.
 *
 * SQLite only, like the rest of block storage in this phase.
 */

import * as database from "../db";
import { getDbPath } from "../db/connection";
import { replaceBlockIndexText } from "../db/fts";
import {
	type BackfillEnvelope,
	type BlockBackfillSliceParams,
	type BlockBackfillSliceResult,
	type BlockVerifySliceParams,
	type BlockVerifySliceResult,
	ledgerFingerprint,
	runBlockBackfillSlice,
	runBlockVerifySlice,
} from "../lib/db-worker/block-backfill-query";
import { runReadTask, WorkersUnavailableError } from "../lib/db-worker/pool";
import { hotSafe } from "../lib/hot-safe";
import { generateId } from "../lib/id";
import { logger } from "../lib/logger";
import { settings } from "../lib/settings";
import { syncEnvelopeNow } from "./narrator-block-sync";

export type BlockJob = "convert" | "verify";

const MAX_SLICE_ENVELOPES = 100;
const MIN_SLICE_ENVELOPES = 5;
const MAX_SLICE_SOURCE_BYTES = 4 * 1024 * 1024;
const SLICE_BUDGET_MS = 1_000;
/** A slice's write transaction should stay under this; past it, the next slice is halved. */
const APPLY_TARGET_MS = 50;
/** Without workers the main thread decodes, so slices shrink to stay short. */
const INLINE_SLICE_ENVELOPES = 10;
const INLINE_SLICE_SOURCE_BYTES = 256 * 1024;
const WORKER_TIMEOUT_MS = 30_000;

/** How a slice is read: by a db-worker (production) or inline on a given connection. */
export interface SliceReader {
	backfill(params: BlockBackfillSliceParams): Promise<BlockBackfillSliceResult>;
	verify(params: BlockVerifySliceParams): Promise<BlockVerifySliceResult>;
}

async function readInWorker<T>(
	params: BlockBackfillSliceParams | BlockVerifySliceParams,
	inline: () => T,
): Promise<T> {
	try {
		return await runReadTask<T>(getDbPath(), params, { timeoutMs: WORKER_TIMEOUT_MS });
	} catch (error) {
		if (!(error instanceof WorkersUnavailableError) || error.reason === "pool_shutdown")
			throw error;
		return inline();
	}
}

const inlineLimits = <T extends { maxEnvelopes: number; maxSourceBytes: number }>(params: T) => ({
	...params,
	maxEnvelopes: Math.min(params.maxEnvelopes, INLINE_SLICE_ENVELOPES),
	maxSourceBytes: Math.min(params.maxSourceBytes, INLINE_SLICE_SOURCE_BYTES),
});

const workerReader: SliceReader = {
	backfill: (params) =>
		readInWorker(params, () => runBlockBackfillSlice(database.sqlite, inlineLimits(params))),
	verify: (params) =>
		readInWorker(params, () => runBlockVerifySlice(database.sqlite, inlineLimits(params))),
};

const state = hotSafe("narrator-block-backfill:state", () => ({
	running: false,
	sliceEnvelopes: MAX_SLICE_ENVELOPES,
}));

let schema: boolean | undefined;

function hasSchema(): boolean {
	if (database.activeDatabaseBackend !== "sqlite") return false;
	schema ??= !!database.sqlite
		.prepare(
			"SELECT 1 FROM sqlite_schema WHERE type = 'table' AND name = 'narrator_block_backfill'",
		)
		.get();
	return schema;
}

export interface BlockJobState {
	cursor: number;
	complete: boolean;
	paused: boolean;
	processed: number;
	updatedAt: string;
}

function jobState(job: BlockJob): BlockJobState {
	database.sqlite
		.prepare("INSERT OR IGNORE INTO narrator_block_backfill (job, updated_at) VALUES (?, ?)")
		.run(job, new Date().toISOString());
	const row = database.sqlite
		.prepare(
			`SELECT cursor, complete, paused, processed, updated_at AS updatedAt
			 FROM narrator_block_backfill WHERE job = ?`,
		)
		.get(job) as {
		cursor: number;
		complete: number;
		paused: number;
		processed: number;
		updatedAt: string;
	};
	return { ...row, complete: !!row.complete, paused: !!row.paused };
}

function advance(job: BlockJob, cursor: number, handled: number, complete: boolean): void {
	database.sqlite
		.prepare(
			`UPDATE narrator_block_backfill SET cursor = ?, processed = processed + ?, complete = ?,
				updated_at = ? WHERE job = ?`,
		)
		.run(cursor, handled, complete ? 1 : 0, new Date().toISOString(), job);
}

function recordDiagnostic(messageId: string, kind: "mismatch" | "undecodable", detail: string) {
	database.sqlite
		.prepare(
			`INSERT INTO narrator_block_diagnostics (message_id, kind, detail, created_at)
			 VALUES (?, ?, ?, ?)
			 ON CONFLICT (message_id) DO UPDATE SET kind = excluded.kind, detail = excluded.detail,
				created_at = excluded.created_at`,
		)
		.run(messageId, kind, detail, new Date().toISOString());
}

/** Outcome of one envelope of a convert slice. */
type Applied = "converted" | "changed" | "gone" | "undecodable";

function applyEnvelope(envelope: BackfillEnvelope): Applied {
	const current = database.sqlite
		.prepare(
			"SELECT body_format AS bodyFormat, body_revision AS bodyRevision FROM narrator_messages WHERE id = ?",
		)
		.get(envelope.id) as { bodyFormat: number; bodyRevision: number } | null;
	// Deleted, or converted by a write since the worker read it.
	if (!current || current.bodyFormat !== 1) return "gone";
	if (!envelope.blocks) {
		recordDiagnostic(envelope.id, "undecodable", "content_json is not an array of elements");
		return "undecodable";
	}
	if (
		current.bodyRevision !== envelope.bodyRevision ||
		ledgerFingerprint(database.sqlite, envelope.id) !== envelope.ledgerFingerprint
	) {
		// The plan is stale. Flip without rows: the sync queue trigger fires on the flip and
		// the store builds the rows from what the envelope holds now.
		database.sqlite
			.prepare("UPDATE narrator_messages SET body_format = 2 WHERE id = ? AND body_format = 1")
			.run(envelope.id);
		return "changed";
	}
	const insertBlock = database.sqlite.prepare(
		`INSERT INTO narrator_blocks (id, message_id, ordinal, kind, subtype, phase, revision, wire_id,
			model_chars, byte_size, created_at, completed_at, model_text, payload, state)
		 VALUES (?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?, ?, ?, ?, ?)`,
	);
	const addAsset = database.sqlite.prepare(
		"INSERT OR IGNORE INTO narrator_block_assets (block_id, asset_id, role) VALUES (?, ?, ?)",
	);
	const bindLedger = database.sqlite.prepare(
		`UPDATE narrator_tool_calls SET block_id = ?
		 WHERE message_id = ? AND tool_use_id = ? AND is_internal = 0`,
	);
	// A legacy envelope keeps no rows; clear any a crash left behind before writing its own.
	database.sqlite.prepare("DELETE FROM narrator_blocks WHERE message_id = ?").run(envelope.id);
	for (const [ordinal, block] of envelope.blocks.entries()) {
		const id = generateId();
		insertBlock.run(
			id,
			envelope.id,
			ordinal,
			block.kind,
			block.subtype,
			block.phase,
			block.wireId,
			block.modelChars,
			block.byteSize,
			envelope.createdAt,
			block.phase === "open" ? null : envelope.createdAt,
			block.modelText,
			block.payload,
			block.state,
		);
		for (const [assetId, role] of block.assets) addAsset.run(id, assetId, role);
		if (block.kind === "tool" && block.wireId) bindLedger.run(id, envelope.id, block.wireId);
		if (block.indexText) replaceBlockIndexText(database.sqlite, id, block.indexText);
	}
	// Last: the flip queues a sync, which these rows already are.
	database.sqlite
		.prepare(
			"UPDATE narrator_messages SET body_bytes = ?, body_format = 2 WHERE id = ? AND body_format = 1",
		)
		.run(envelope.bodyBytes, envelope.id);
	database.sqlite
		.prepare("DELETE FROM narrator_block_sync_queue WHERE message_id = ?")
		.run(envelope.id);
	return "converted";
}

/** Size the next slice by how long this slice's write transaction took. */
function adapt(elapsedMs: number, envelopes: number): void {
	if (elapsedMs > APPLY_TARGET_MS) {
		state.sliceEnvelopes = Math.max(MIN_SLICE_ENVELOPES, Math.floor(state.sliceEnvelopes / 2));
		logger.warn("Slow block backfill transaction", {
			elapsedMs: Math.round(elapsedMs),
			envelopes,
			nextSlice: state.sliceEnvelopes,
		});
	} else if (state.sliceEnvelopes < MAX_SLICE_ENVELOPES) {
		state.sliceEnvelopes = Math.min(MAX_SLICE_ENVELOPES, state.sliceEnvelopes * 2);
	}
}

async function convertSlice(reader: SliceReader): Promise<void> {
	const job = jobState("convert");
	const slice = await reader.backfill({
		kind: "blockBackfillSlice",
		afterRowid: job.cursor,
		maxEnvelopes: state.sliceEnvelopes,
		maxSourceBytes: MAX_SLICE_SOURCE_BYTES,
		budgetMs: SLICE_BUDGET_MS,
	});
	const started = performance.now();
	const outcomes = database.sqlite.transaction(() => {
		const applied = slice.envelopes.map(applyEnvelope);
		advance("convert", slice.lastRowid, slice.envelopes.length, slice.done);
		return applied;
	})();
	adapt(performance.now() - started, slice.envelopes.length);
	// Stale plans were flipped for the store to build; let it do so now, a few at a time.
	if (outcomes.includes("changed"))
		database.sqlite.transaction(() => {
			for (const [index, envelope] of slice.envelopes.entries())
				if (outcomes[index] === "changed") syncEnvelopeNow(envelope.id);
		})();
}

async function verifySlice(reader: SliceReader): Promise<void> {
	const job = jobState("verify");
	const slice = await reader.verify({
		kind: "blockVerifySlice",
		afterRowid: job.cursor,
		maxEnvelopes: state.sliceEnvelopes,
		maxSourceBytes: MAX_SLICE_SOURCE_BYTES,
		budgetMs: SLICE_BUDGET_MS,
	});
	database.sqlite.transaction(() => {
		const pending = database.sqlite.prepare(
			"SELECT 1 FROM narrator_block_sync_queue WHERE message_id = ?",
		);
		for (const id of slice.mismatched) {
			// Queued envelopes are about to be synced; they are not a dual-write failure.
			if (pending.get(id)) continue;
			recordDiagnostic(id, "mismatch", "block rows did not assemble to content_json");
			syncEnvelopeNow(id);
		}
		advance("verify", slice.lastRowid, slice.checked, slice.done);
	})();
	if (slice.mismatched.length)
		logger.warn("Block rows differed from their envelope; recorded and re-synced", {
			messageIds: slice.mismatched,
		});
}

/**
 * Run one slice of whichever job has work: convert first, then verify. Returns whether a
 * slice ran (false when both are complete or paused).
 */
export async function runBlockJobSlice(reader: SliceReader = workerReader): Promise<boolean> {
	if (!hasSchema()) return false;
	const convert = jobState("convert");
	if (!convert.complete) {
		// Block writes off is the rollback switch: no envelope becomes block-format.
		if (convert.paused || !settings.narratorBlocks.write) return false;
		await convertSlice(reader);
		return true;
	}
	const verify = jobState("verify");
	if (verify.complete || verify.paused) return false;
	await verifySlice(reader);
	return true;
}

/**
 * Legacy envelopes can appear after convert finished: written while block writes were off,
 * or imported. Send the job back to the first one it can convert; the ones it could not
 * read stay legacy for good and do not count.
 */
export function rearmBlockConvert(): void {
	if (!jobState("convert").complete) return;
	const first = database.sqlite
		.prepare(
			`SELECT min(m.rowid) AS rowid FROM narrator_messages m INDEXED BY idx_messages_legacy_body
			 WHERE m.body_format = 1 AND NOT EXISTS (
				SELECT 1 FROM narrator_block_diagnostics d
				WHERE d.message_id = m.id AND d.kind = 'undecodable')`,
		)
		.get() as { rowid: number | null };
	if (first.rowid === null) return;
	database.sqlite
		.prepare(
			"UPDATE narrator_block_backfill SET cursor = ?, complete = 0, updated_at = ? WHERE job = 'convert'",
		)
		.run(first.rowid - 1, new Date().toISOString());
}

/** Start the jobs in the background, if they have work; a no-op while they already run. */
export function startBlockBackfill(): void {
	if (state.running || !hasSchema()) return;
	rearmBlockConvert();
	state.running = true;
	const next = () => {
		runBlockJobSlice()
			.then((ran) => {
				if (ran) setTimeout(next, 0);
				else state.running = false;
			})
			.catch((error) => {
				state.running = false;
				logger.warn("Block backfill stopped; it resumes at the next start", {
					error: String(error),
				});
			});
	};
	setTimeout(next, 0);
}

export interface BlockBackfillStatus {
	available: boolean;
	convert: BlockJobState | null;
	verify: BlockJobState | null;
	/** The newest `narrator_messages.rowid`: cursors are read as progress against it. */
	maxRowid: number;
	diagnostics: number;
	running: boolean;
}

export function blockBackfillStatus(): BlockBackfillStatus {
	if (!hasSchema())
		return {
			available: false,
			convert: null,
			verify: null,
			maxRowid: 0,
			diagnostics: 0,
			running: false,
		};
	const maxRowid =
		(
			database.sqlite.prepare("SELECT max(rowid) AS n FROM narrator_messages").get() as {
				n: number | null;
			}
		).n ?? 0;
	const diagnostics = (
		database.sqlite
			.prepare("SELECT count(*) AS n FROM (SELECT 1 FROM narrator_block_diagnostics LIMIT 10000)")
			.get() as { n: number }
	).n;
	return {
		available: true,
		convert: jobState("convert"),
		verify: jobState("verify"),
		maxRowid,
		diagnostics,
		running: state.running,
	};
}

/** Pause or resume both jobs; resuming restarts them. */
export function setBlockBackfillPaused(paused: boolean): BlockBackfillStatus {
	if (hasSchema()) {
		jobState("convert");
		jobState("verify");
		database.sqlite
			.prepare("UPDATE narrator_block_backfill SET paused = ?, updated_at = ?")
			.run(paused ? 1 : 0, new Date().toISOString());
		if (!paused) startBlockBackfill();
	}
	return blockBackfillStatus();
}
