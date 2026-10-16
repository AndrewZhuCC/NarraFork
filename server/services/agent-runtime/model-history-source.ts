import type { Database } from "bun:sqlite";
import type { SQL } from "bun";
import { hotSafe } from "../../lib/hot-safe";
import { isCompiledRuntime } from "../../lib/runtime-target";
import { modelHistoryManifest } from "./model-history-delta";
import {
	MODEL_HISTORY_MAX_BYTES,
	MODEL_HISTORY_TIMEOUT_MS,
	type ModelHistoryCounters,
	type ModelHistorySource,
	type ModelSourceRow,
	type ModelToolSourceRow,
	queryModelHistory,
	sqliteHistoryQuery,
} from "./model-history-query";
import {
	readPostgresModelHistorySignature,
	readSqliteModelHistorySignature,
} from "./model-history-revision";

const SESSION_CACHE_BYTES = 16 * 1024 * 1024;
const GLOBAL_CACHE_BYTES = 64 * 1024 * 1024;
const CACHE_ENTRIES = 8;
const CACHE_IDLE_MS = 60_000;

type Entry = { source: ModelHistorySource; bytes: number; touched: number };
export class ModelHistorySourceCache {
	private entries = new Map<string, Entry>();
	private bytes = 0;
	get size() {
		return this.entries.size;
	}
	get retainedBytes() {
		return this.bytes;
	}
	constructor(private now = () => Date.now()) {}
	prune(): void {
		for (const [key, entry] of this.entries)
			if (this.now() - entry.touched >= CACHE_IDLE_MS) this.delete(key);
	}
	private delete(key: string): void {
		const entry = this.entries.get(key);
		if (entry) this.bytes -= entry.bytes;
		this.entries.delete(key);
	}
	get(key: string): ModelHistorySource | undefined {
		this.prune();
		const entry = this.entries.get(key);
		if (!entry) return;
		entry.touched = this.now();
		this.entries.delete(key);
		this.entries.set(key, entry);
		return entry.source;
	}
	previous(prefix: string): ModelHistorySource | undefined {
		this.prune();
		for (const [key, entry] of this.entries) if (key.startsWith(prefix)) return entry.source;
	}
	set(key: string, source: ModelHistorySource, bytes: number): void {
		this.prune();
		this.delete(key);
		if (bytes > SESSION_CACHE_BYTES) return;
		// One source revision per narrator: old generations cannot accumulate under new signatures.
		const narratorKey = key.slice(0, key.lastIndexOf("\u0000"));
		for (const old of this.entries.keys())
			if (old.startsWith(`${narratorKey}\u0000`)) this.delete(old);
		while (this.entries.size >= CACHE_ENTRIES || this.bytes + bytes > GLOBAL_CACHE_BYTES) {
			const oldest = this.entries.keys().next().value;
			if (oldest === undefined) break;
			this.delete(oldest);
		}
		this.entries.set(key, { source, bytes, touched: this.now() });
		this.bytes += bytes;
	}
	clear(): void {
		this.entries.clear();
		this.bytes = 0;
	}
}

const state = hotSafe("narrator.modelHistorySource.v1", () => {
	const cache = new ModelHistorySourceCache();
	const reaper = setInterval(() => cache.prune(), 15_000);
	reaper.unref();
	return {
		cache,
		workers: 0,
		loads: 0,
		hits: 0,
		signatureReads: 0,
		lastCounters: null as ModelHistoryCounters | null,
		workerSpecifier: null as string | null,
		sourceSignatures: new WeakMap<ModelHistorySource, SourceSignatureRecord>(),
		rowSignatures: new WeakMap<object, SourceSignatureRecord>(),
	};
});
// Upgrade a legacy process pin without re-signing its unknown cached snapshots. Such entries
// miss these maps and must take the fully validated cold path before they can be reused.
state.sourceSignatures ??= new WeakMap<ModelHistorySource, SourceSignatureRecord>();
state.rowSignatures ??= new WeakMap<object, SourceSignatureRecord>();
state.workerSpecifier ??= null;
export function modelHistorySourceStats() {
	return {
		loads: state.loads,
		hits: state.hits,
		signatureReads: state.signatureReads,
		cacheEntries: state.cache.size,
		cacheBytes: state.cache.retainedBytes,
		workers: state.workers,
		lastCounters: state.lastCounters,
	};
}
export function clearModelHistorySourceCacheForTest(): void {
	state.cache.clear();
}

export type ModelHistoryBackend =
	| { backend: "sqlite"; sqlite: Database }
	| { backend: "postgres"; sql: SQL; url: string };
type SourceSignatureRecord = {
	backend: ModelHistoryBackend;
	narratorId: string;
	revision: string;
	trusted: boolean;
	catalogIdentity?: string;
};
export function markModelHistoryRows(rows: object, source: ModelHistorySource): void {
	const record = state.sourceSignatures.get(source);
	if (!record) throw new Error("Model history source signature missing; request not sent");
	state.rowSignatures.set(rows, record);
}
export async function verifyModelHistoryRowsCurrent(
	rows: object,
	options: { signal?: AbortSignal; deadline?: number } = {},
): Promise<void> {
	options.signal?.throwIfAborted();
	if (options.deadline != null && Date.now() >= options.deadline)
		throw new Error("Model history preparation exceeded 10s budget");
	const record = state.rowSignatures.get(rows);
	if (!record) throw new Error("Model history rows signature missing; request not sent");
	// Only an explicitly recorded unversioned source can take the compatibility path.
	if (!record.trusted) return;
	const current =
		record.backend.backend === "sqlite"
			? readSqliteModelHistorySignature(record.backend.sqlite, record.narratorId)
			: await readPostgresModelHistorySignature(record.backend.sql, record.narratorId, options);
	options.signal?.throwIfAborted();
	if (options.deadline != null && Date.now() >= options.deadline)
		throw new Error("Model history preparation exceeded 10s budget");
	if (
		!current.trusted ||
		current.revision !== record.revision ||
		current.catalogIdentity !== record.catalogIdentity
	)
		throw new Error("Model history changed after provider preparation; request not sent");
}

export type LoadModelHistoryOptions = {
	limit?: number;
	signal?: AbortSignal;
	/** Shared absolute deadline for loading + projection, never restarted between phases. */
	deadline?: number;
};

export async function freezeModelHistorySource(
	source: object,
	deadline: number,
	signal?: AbortSignal,
): Promise<void> {
	type Frame = { value: object; keys: string[] | null; length: number; index: number };
	const stack: Frame[] = [];
	const push = (value: object) => {
		if (Object.isFrozen(value)) return;
		Object.freeze(value);
		// JSON objects already passed the per-container width preflight. Arrays need no
		// Object.values/Object.keys copy: visit primitive slots with an index and yield too.
		const keys = Array.isArray(value) ? null : Object.keys(value);
		stack.push({ value, keys, length: keys?.length ?? (value as unknown[]).length, index: 0 });
	};
	push(source);
	let visitedSlots = 0;
	while (stack.length) {
		const frame = stack[stack.length - 1];
		if (frame.index >= frame.length) {
			stack.pop();
			continue;
		}
		const key = frame.keys ? frame.keys[frame.index++] : frame.index++;
		const value = (frame.value as Record<string | number, unknown>)[key];
		if (value && typeof value === "object") push(value);
		if (++visitedSlots >= 1000) {
			visitedSlots = 0;
			signal?.throwIfAborted();
			if (Date.now() >= deadline) throw new Error("Model history preparation exceeded 10s budget");
			await Bun.sleep(0);
		}
	}
	signal?.throwIfAborted();
	if (Date.now() >= deadline) throw new Error("Model history preparation exceeded 10s budget");
}

/**
 * The embedded worker key inside a compiled binary depends on the build layout: a build whose
 * entries share the `server/` root folds the prefix away, while multi-entry builds keep the full
 * path. No single shape is correct for both, so candidates are probed (same reason
 * COMPILED_WORKER_PATHS exists for the db worker); the winner is cached in state.
 */
export function modelHistoryWorkerSpecifiers(
	compiled = isCompiledRuntime(),
	moduleUrl = import.meta.url,
): string[] {
	return (
		compiled
			? [
					"./services/agent-runtime/model-history-worker.js",
					"./server/services/agent-runtime/model-history-worker.js",
					"./model-history-worker.js",
				]
			: ["./model-history-worker.ts"]
	).map((path) => new URL(path, moduleUrl).href);
}

/** Sentinel distinguishing a worker that never started from one that failed mid-query. */
const WORKER_STARTUP_ERROR = new Error("Model history read-only worker failed to start");

async function readWorker(
	backend: ModelHistoryBackend,
	narratorId: string,
	options: LoadModelHistoryOptions,
	deadline: number,
	versioned: boolean,
	previous?: ModelHistorySource,
): Promise<ModelHistorySource> {
	while (state.workers >= 2) {
		options.signal?.throwIfAborted();
		if (Date.now() >= deadline) throw new Error("Model history preparation exceeded 10s budget");
		await Bun.sleep(1);
	}
	options.signal?.throwIfAborted();
	if (Date.now() >= deadline) throw new Error("Model history preparation exceeded 10s budget");
	state.workers++;
	try {
		const candidates = state.workerSpecifier
			? [state.workerSpecifier]
			: modelHistoryWorkerSpecifiers();
		let lastError: Error | undefined;
		for (const specifier of candidates) {
			options.signal?.throwIfAborted();
			if (Date.now() >= deadline) throw new Error("Model history preparation exceeded 10s budget");
			const worker = new Worker(specifier);
			// A module-load failure surfaces as onerror with no message at all; a running
			// worker always communicates before dying. That difference gates the probe.
			let received = false;
			try {
				const source = await new Promise<ModelHistorySource>((resolve, reject) => {
					const messages: ModelSourceRow[] = [];
					const byId = new Map<string, ModelSourceRow>();
					const previousById = new Map(previous?.messages.map((row) => [row.id, row]));
					const previousTools = new Map(
						previous?.messages.flatMap((row) =>
							row.toolCalls.map((tool) => [tool.id, tool] as const),
						),
					);
					let finished = false;
					const finish = (
						error?: Error,
						counters?: ModelHistoryCounters,
						compactSeq?: number | null,
					) => {
						if (finished) return;
						finished = true;
						clearTimeout(timer);
						options.signal?.removeEventListener("abort", abort);
						worker.terminate();
						if (error || !counters)
							reject(error ?? new Error("Model history worker did not complete"));
						else {
							for (const row of messages)
								row.toolCalls.sort((a, b) => (a.id < b.id ? -1 : a.id > b.id ? 1 : 0));
							resolve({ messages, counters, compactSeq });
						}
					};
					const abort = () => finish(new Error("Model history preparation cancelled"));
					const timer = setTimeout(
						() => finish(new Error("Model history preparation exceeded 10s budget")),
						Math.max(1, deadline - Date.now()),
					);
					options.signal?.addEventListener("abort", abort, { once: true });
					worker.onerror = () =>
						finish(
							received ? new Error("Model history read-only worker failed") : WORKER_STARTUP_ERROR,
						);
					worker.onmessage = (event) => {
						received = true;
						if (Date.now() >= deadline || options.signal?.aborted)
							return finish(new Error("Model history preparation cancelled or timed out"));
						if (event.data.type === "page") {
							for (const row of event.data.rows as ModelSourceRow[]) {
								messages.push(row);
								byId.set(row.id, row);
							}
							worker.postMessage({ type: "ack" });
						} else if (event.data.type === "tools") {
							for (const { messageId, ...tool } of event.data.rows as Array<
								ModelToolSourceRow & { messageId: string }
							>)
								byId.get(messageId)?.toolCalls.push(tool);
							worker.postMessage({ type: "ack" });
						} else if (event.data.type === "reuseBodies") {
							for (const id of event.data.ids as string[]) {
								const old = previousById.get(id);
								if (!old) return finish(new Error("Model history delta source unavailable"));
								const row = { ...old, toolCalls: [] };
								messages.push(row);
								byId.set(id, row);
							}
							worker.postMessage({ type: "ack" });
						} else if (event.data.type === "reuseTools") {
							for (const { id, messageId } of event.data.rows as Array<{
								id: string;
								messageId: string;
							}>) {
								const old = previousTools.get(id);
								if (!old) return finish(new Error("Model history delta tool source unavailable"));
								byId.get(messageId)?.toolCalls.push(old);
							}
							worker.postMessage({ type: "ack" });
						} else if (event.data.type === "order") {
							if (event.data.reset) messages.length = 0;
							for (const id of event.data.ids as string[])
								messages.push(byId.get(id) as ModelSourceRow);
							worker.postMessage({ type: "ack" });
						} else if (event.data.type === "done")
							finish(undefined, event.data.counters, event.data.compactSeq);
						else if (event.data.type === "error") finish(new Error(event.data.message));
					};
					worker.postMessage({
						narratorId,
						deadline,
						limit: options.limit,
						maxBytes: MODEL_HISTORY_MAX_BYTES,
						versioned,
						previous: previous ? modelHistoryManifest(previous) : undefined,
						...(backend.backend === "sqlite"
							? { backend: "sqlite", path: backend.sqlite.filename }
							: { backend: "postgres", url: backend.url }),
					});
				});
				state.workerSpecifier = specifier;
				return source;
			} catch (error) {
				// Only a candidate that never started is probed against the next path shape;
				// a worker that died mid-query must surface its real error unchanged.
				if (error !== WORKER_STARTUP_ERROR || specifier === candidates.at(-1)) throw error as Error;
				lastError = error as Error;
			}
		}
		throw lastError ?? new Error("Model history read-only worker failed");
	} finally {
		state.workers--;
	}
}

export async function loadModelHistorySource(
	backend: ModelHistoryBackend,
	narratorId: string,
	options: LoadModelHistoryOptions = {},
): Promise<ModelHistorySource> {
	const deadline = Math.min(
		options.deadline ?? Number.POSITIVE_INFINITY,
		Date.now() + MODEL_HISTORY_TIMEOUT_MS,
	);
	const check = () => {
		options.signal?.throwIfAborted();
		if (Date.now() >= deadline) throw new Error("Model history preparation exceeded 10s budget");
	};
	check();
	const signature = async () => {
		state.signatureReads++;
		return backend.backend === "sqlite"
			? readSqliteModelHistorySignature(backend.sqlite, narratorId)
			: readPostgresModelHistorySignature(backend.sql, narratorId, { ...options, deadline });
	};
	const before = await signature();
	check();
	const identity =
		backend.backend === "sqlite" ? backend.sqlite.filename : `postgres:${Bun.hash(backend.url)}`;
	// An in-memory connection has no cross-thread read-only identity; never cache its shared name.
	const inMemory = backend.backend === "sqlite" && (identity === ":memory:" || !identity);
	const key = `${identity}\u0000${narratorId}\u0000${before.catalogIdentity ?? "unversioned"}:${before.revision}`;
	const cacheable = before.trusted && !inMemory && options.limit == null;
	if (cacheable) {
		const cached = state.cache.get(key);
		const record = cached && state.sourceSignatures.get(cached);
		if (
			cached &&
			record?.trusted &&
			record.narratorId === narratorId &&
			record.revision === before.revision &&
			record.catalogIdentity === before.catalogIdentity
		) {
			state.hits++;
			return cached;
		}
	}
	state.loads++;
	// Test-only in-memory fixtures cannot be opened by a worker. The query's metadata preflight
	// still bounds rows/bytes; production file/PG reads NEVER fall back to inline JSON parsing.
	const candidate = cacheable
		? state.cache.previous(`${identity}\u0000${narratorId}\u0000`)
		: undefined;
	// A trigger disable/re-enable or reinstall can leave body/tool counters unchanged.
	// Catalog epoch changes MUST discard delta reuse and read complete current source rows.
	const candidateRecord = candidate && state.sourceSignatures.get(candidate);
	const previous =
		candidate &&
		candidateRecord?.trusted &&
		candidateRecord.catalogIdentity === before.catalogIdentity
			? candidate
			: undefined;
	let source: ModelHistorySource;
	if (inMemory && backend.backend === "sqlite")
		source = await queryModelHistory(sqliteHistoryQuery(backend.sqlite), {
			narratorId,
			limit: options.limit,
			deadline,
			maxBytes: 1024 * 1024,
			inlineRowBytes: 64 * 1024,
			versioned: before.trusted,
		});
	else {
		try {
			source = await readWorker(backend, narratorId, options, deadline, before.trusted, previous);
		} catch (error) {
			if (
				!previous ||
				!(error instanceof Error) ||
				error.message !== "Model history delta structure changed"
			)
				throw error;
			// Ref deletion/reorder/COW, compact, segment/lazy range changes: one full rebuild,
			// sharing the SAME deadline. No partial delta or indefinite retries are accepted.
			source = await readWorker(backend, narratorId, options, deadline, before.trusted);
		}
	}
	options.signal?.throwIfAborted();
	const after = await signature();
	if (before.trusted !== after.trusted)
		throw new Error("Model history revision trust changed during preparation; request not sent");
	if (
		before.trusted &&
		(before.revision !== after.revision ||
			before.catalogIdentity !== after.catalogIdentity ||
			!after.trusted)
	)
		throw new Error("Model history changed during preparation; retry with a fresh source snapshot");
	await freezeModelHistorySource(source, deadline, options.signal);
	check();
	state.lastCounters = source.counters;
	state.sourceSignatures.set(source, {
		backend,
		narratorId,
		revision: after.revision,
		catalogIdentity: after.catalogIdentity,
		trusted: after.trusted,
	});
	if (cacheable && after.trusted)
		state.cache.set(
			key,
			source,
			source.counters.preparedBytes ??
				source.counters.sourceBytes * 4 +
					source.counters.refs * 1024 +
					source.counters.toolRows * 1024,
		);
	return source;
}
