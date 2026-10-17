/**
 * Run a storage directory walk off the server thread.
 *
 * Several categories scan at once, so a small gate caps how many walks are in
 * flight. Spawn failure degrades to the same async walk in-process: slower for
 * the event loop, but still not the synchronous sqlite stall, and the settings
 * page still gets a number.
 */

import { Worker } from "node:worker_threads";
import { hotSafe } from "../hot-safe";
import { logger } from "../logger";
import { isCompiledRuntime } from "../runtime-target";
import {
	DIR_SCAN_MAX_DEPTH,
	DIR_SCAN_MAX_ENTRIES,
	DirectoryMeasureAbortedError,
	type DirectoryTreeMeasurement,
	type MeasureDirectoryTreesOptions,
	measureDirectoryTrees,
} from "./measure-tree";

export { DirectoryMeasureAbortedError };

const CONCURRENCY = 4;
const READY_TIMEOUT_MS = 5_000;
/** A multi-million-entry tree can take minutes. This only bounds a wedged worker. */
const MEASURE_TIMEOUT_MS = 15 * 60 * 1000;

interface FsScanGate {
	active: number;
	waiters: Array<() => void>;
	workersDisabled: boolean;
	resolvedSpecifier?: string;
}

function gate(): FsScanGate {
	return hotSafe<FsScanGate>("narrafork.fsScan.gate", () => ({
		active: 0,
		waiters: [],
		workersDisabled: false,
	}));
}

export function fsScanWorkerSpecifierCandidates(
	compiled = isCompiledRuntime(),
	moduleUrl = import.meta.url,
): string[] {
	const paths = compiled
		? [
				"./lib/fs-scan-worker/fs-scan-worker-entry.js",
				"./server/lib/fs-scan-worker/fs-scan-worker-entry.js",
				"./fs-scan-worker-entry.js",
			]
		: ["./fs-scan-worker-entry.ts"];
	return paths.map((path) => new URL(path, moduleUrl).href);
}

/** Bun's compiled module keys use forward slashes. Only unwrap the virtual drive. */
export function fsScanWorkerEntryPoint(specifier: string): string | URL {
	const url = new URL(specifier);
	if (url.protocol === "file:" && /^\/[a-z]:\/(?:~BUN|%7eBUN)\/root\//i.test(url.pathname)) {
		return decodeURIComponent(url.pathname.slice(1));
	}
	return url;
}

async function withSlot<T>(run: () => Promise<T>): Promise<T> {
	const current = gate();
	if (current.active >= CONCURRENCY) {
		await new Promise<void>((resolve) => {
			current.waiters.push(resolve);
		});
	}
	current.active++;
	try {
		return await run();
	} finally {
		current.active--;
		current.waiters.shift()?.();
	}
}

interface WorkerResultMessage {
	type: "result";
	requestId: string;
	roots: DirectoryTreeMeasurement[];
}

interface WorkerProgressMessage {
	type: "progress";
	requestId: string;
	entriesScanned: number;
	sizeBytes: number;
}

interface WorkerErrorMessage {
	type: "error";
	requestId: string;
	message: string;
	aborted?: boolean;
}

function measureInWorker(
	specifier: string,
	roots: string[],
	options: MeasureDirectoryTreesOptions,
): Promise<DirectoryTreeMeasurement[]> {
	const requestId = `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`;
	const maxEntries = options.maxEntries ?? DIR_SCAN_MAX_ENTRIES;
	const maxDepth = options.maxDepth ?? DIR_SCAN_MAX_DEPTH;
	return new Promise((resolve, reject) => {
		const worker = new Worker(fsScanWorkerEntryPoint(specifier));
		let settled = false;
		let ready = false;
		const readyTimer = setTimeout(
			() => finish(new Error("filesystem scan worker did not become ready")),
			READY_TIMEOUT_MS,
		);
		const measureTimer = setTimeout(
			() => finish(new Error("filesystem scan worker timed out")),
			MEASURE_TIMEOUT_MS,
		);

		const finish = (error?: unknown, result?: DirectoryTreeMeasurement[]) => {
			if (settled) return;
			settled = true;
			clearTimeout(readyTimer);
			clearTimeout(measureTimer);
			options.signal?.removeEventListener("abort", onAbort);
			void worker.terminate();
			if (error) reject(error);
			else resolve(result ?? []);
		};

		const onAbort = () => {
			try {
				worker.postMessage({ type: "cancel", requestId });
			} catch {
				// The worker is already gone.
			}
			finish(new DirectoryMeasureAbortedError());
		};

		if (options.signal) {
			if (options.signal.aborted) {
				finish(new DirectoryMeasureAbortedError());
				return;
			}
			options.signal.addEventListener("abort", onAbort, { once: true });
		}

		worker.once("error", (error) => finish(error));
		worker.on("message", (message: { type?: string }) => {
			if (!ready) {
				if (message?.type !== "ready") {
					finish(new Error("filesystem scan worker sent no ready handshake"));
					return;
				}
				ready = true;
				clearTimeout(readyTimer);
				try {
					worker.postMessage({
						type: "measure",
						requestId,
						roots,
						maxEntries,
						maxDepth,
					});
				} catch (error) {
					finish(error);
				}
				return;
			}
			if (message?.type === "progress") {
				const progress = message as WorkerProgressMessage;
				if (progress.requestId === requestId) {
					options.onProgress?.({
						entriesScanned: progress.entriesScanned,
						sizeBytes: progress.sizeBytes,
					});
				}
				return;
			}
			if (message?.type === "result") {
				const result = message as WorkerResultMessage;
				if (result.requestId === requestId) finish(undefined, result.roots);
				return;
			}
			if (message?.type === "error") {
				const failure = message as WorkerErrorMessage;
				if (failure.requestId !== requestId) return;
				finish(
					failure.aborted
						? new DirectoryMeasureAbortedError()
						: new Error(failure.message || "filesystem scan worker failed"),
				);
			}
		});
	});
}

async function measureWithWorkers(
	roots: string[],
	options: MeasureDirectoryTreesOptions,
): Promise<DirectoryTreeMeasurement[]> {
	const current = gate();
	if (current.workersDisabled) return measureDirectoryTrees(roots, options);

	const candidates = current.resolvedSpecifier
		? [current.resolvedSpecifier]
		: fsScanWorkerSpecifierCandidates();
	let lastError: unknown;
	for (const specifier of candidates) {
		try {
			const result = await measureInWorker(specifier, roots, options);
			current.resolvedSpecifier = specifier;
			return result;
		} catch (error) {
			if (error instanceof DirectoryMeasureAbortedError || options.signal?.aborted) throw error;
			lastError = error;
		}
	}
	current.workersDisabled = true;
	logger.warn("Filesystem scan worker unavailable; measuring directories in-process", {
		reason: lastError instanceof Error ? lastError.message : String(lastError),
	});
	return measureDirectoryTrees(roots, options);
}

export async function measureDirectoriesOffThread(
	roots: string[],
	options: MeasureDirectoryTreesOptions = {},
): Promise<DirectoryTreeMeasurement[]> {
	if (roots.length === 0) return [];
	return withSlot(() => measureWithWorkers(roots, options));
}
