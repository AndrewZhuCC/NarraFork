import type { FileHistoryTarget } from "@shared/file-change-protocol";
import { targetPathSemantics } from "../lib/agent/execution/path-semantics";
import { hotSafe } from "../lib/hot-safe";

export const FILE_WRITE_QUEUE_LIMITS = Object.freeze({
	waitTimeoutMs: 30_000,
	maxWaitersPerFile: 64,
	maxWaiters: 1024,
});

export interface FileWriteOptions {
	/** Cancels admission. A running body owns its slot until its promise settles. */
	signal?: AbortSignal;
	waitTimeoutMs?: number;
}

export class FileWriteQueueError extends Error {
	constructor(readonly code: "file_busy" | "queue_full") {
		super(
			code === "file_busy"
				? "This file is busy; retry after its current write finishes"
				: "File write queue is full",
		);
		this.name = "FileWriteQueueError";
	}
}

type Waiter = { grant(): void };
type Queue = { waiting: Set<Waiter> };
type QueueState = { files: Map<string, Queue>; waiting: number };

function fileKey(target: FileHistoryTarget): string {
	if (
		!target.deviceId ||
		target.deviceId.includes("\0") ||
		(target.pathFlavor !== "posix" && target.pathFlavor !== "windows") ||
		!target.canonicalPath ||
		target.canonicalPath.includes("\0")
	)
		throw new TypeError("A file write requires its device and canonical path");
	const paths = targetPathSemantics(target.pathFlavor);
	if (!paths.isAbsolute(target.canonicalPath))
		throw new TypeError("File write path must be absolute");
	return JSON.stringify([
		target.deviceId,
		target.pathFlavor,
		paths.identityKey(target.canonicalPath),
	]);
}

/** In-process resource FIFO. Its entries describe actual running work, never disk state. */
export class FileWriteQueue {
	constructor(private readonly state: QueueState = { files: new Map(), waiting: 0 }) {}

	get activeFileCount(): number {
		return this.state.files.size;
	}

	get waitingCount(): number {
		return this.state.waiting;
	}

	async run<T>(
		target: FileHistoryTarget,
		body: () => T | Promise<T>,
		options: FileWriteOptions = {},
	): Promise<T> {
		const release = await this.acquire(fileKey(target), options);
		try {
			options.signal?.throwIfAborted();
			return await body();
		} finally {
			release();
		}
	}

	private release(key: string, queue: Queue): () => void {
		let released = false;
		return () => {
			if (released) return;
			released = true;
			const next = queue.waiting.values().next().value;
			if (next) next.grant();
			else this.state.files.delete(key);
		};
	}

	private acquire(key: string, options: FileWriteOptions): Promise<() => void> {
		const { signal, waitTimeoutMs = FILE_WRITE_QUEUE_LIMITS.waitTimeoutMs } = options;
		if (!Number.isSafeInteger(waitTimeoutMs) || waitTimeoutMs < 0 || waitTimeoutMs > 60_000) {
			return Promise.reject(
				new TypeError("File write wait timeout must be between 0 and 60000 ms"),
			);
		}
		if (signal?.aborted) return Promise.reject(signal.reason);
		const current = this.state.files.get(key);
		if (!current) {
			const queue: Queue = { waiting: new Set() };
			this.state.files.set(key, queue);
			return Promise.resolve(this.release(key, queue));
		}
		if (waitTimeoutMs === 0) return Promise.reject(new FileWriteQueueError("file_busy"));
		if (
			current.waiting.size >= FILE_WRITE_QUEUE_LIMITS.maxWaitersPerFile ||
			this.state.waiting >= FILE_WRITE_QUEUE_LIMITS.maxWaiters
		) {
			return Promise.reject(new FileWriteQueueError("queue_full"));
		}
		return new Promise((resolve, reject) => {
			let timer: ReturnType<typeof setTimeout>;
			const cleanup = () => {
				if (!current.waiting.delete(waiter)) return false;
				this.state.waiting--;
				clearTimeout(timer);
				signal?.removeEventListener("abort", abort);
				return true;
			};
			const cancel = (reason: unknown) => {
				if (cleanup()) reject(reason);
			};
			const abort = () => cancel(signal?.reason);
			const waiter: Waiter = {
				grant: () => {
					if (cleanup()) resolve(this.release(key, current));
				},
			};
			current.waiting.add(waiter);
			this.state.waiting++;
			timer = setTimeout(() => cancel(new FileWriteQueueError("file_busy")), waitTimeoutMs);
			signal?.addEventListener("abort", abort, { once: true });
			if (signal?.aborted) abort();
		});
	}
}

// Keep state, rather than an old class implementation, across Bun --hot reloads.
const sharedState = hotSafe<QueueState>("narrafork.file-write-queue.v1", () => ({
	files: new Map(),
	waiting: 0,
}));
const fileWrites = new FileWriteQueue(sharedState);

export function withFileWrite<T>(
	target: FileHistoryTarget,
	body: () => T | Promise<T>,
	options?: FileWriteOptions,
): Promise<T> {
	return fileWrites.run(target, body, options);
}
