import { resolve } from "node:path";
import { AppError } from "../lib/errors";
import { logger } from "../lib/logger";
import { normalizePathForOS } from "../lib/platform-path";
import type { GitStatusSummary } from "./git-service";

type Loader<T> = (signal: AbortSignal) => Promise<T>;
interface Waiter<T> {
	resolve(value: T): void;
	reject(error: unknown): void;
	cleanup(): void;
}
interface Job<T> {
	key: string;
	loader: Loader<T>;
	controller: AbortController;
	waiters: Set<Waiter<T>>;
	state: "queued" | "running" | "finished";
	invalidated: boolean;
	createdAt: number;
	readers: number;
	timer?: ReturnType<typeof setTimeout>;
}
interface Entry<T> {
	current: Job<T>;
	next?: Job<T>;
}

export function getLocalGitStatusQueryKey(path: string): string {
	return JSON.stringify(["local", normalizePathForOS(resolve(path), process.platform)]);
}

/** A root write also invalidates scoped file-tree reads, and vice versa. */
export function localGitStatusScopesOverlap(key: string, rawPath: string): boolean {
	let parts: unknown;
	try {
		parts = JSON.parse(key);
	} catch {
		return false;
	}
	if (!Array.isArray(parts) || parts[0] !== "local" || typeof parts[1] !== "string") return false;
	const scope = parts[1];
	const changed = normalizePathForOS(resolve(rawPath), process.platform);
	const within = (path: string, parent: string) =>
		path === parent || path.startsWith(parent.endsWith("/") ? parent : `${parent}/`);
	return within(scope, changed) || within(changed, scope);
}

/**
 * Single flight per execution target, with one trailing refresh after invalidation.
 * No result cache here: fresh HTTP reads and the legacy short-TTL facade share the
 * same actual subprocess. A caller's abort never owns another caller's query.
 */
export class GitStatusQueryCoordinator<T = GitStatusSummary> {
	private entries = new Map<string, Entry<T>>();
	private queue: Job<T>[] = [];
	private running = 0;
	private clearing = false;

	constructor(private readonly limits = { concurrency: 4, queued: 128, timeoutMs: 30_000 }) {}

	query(key: string, loader: Loader<T>, options: { signal?: AbortSignal } = {}) {
		const signal = options.signal;
		if (signal?.aborted) return Promise.reject(signal.reason);
		const entry = this.entries.get(key);
		let job = entry
			? entry.current.invalidated || entry.current.controller.signal.aborted
				? entry.next
				: entry.current
			: undefined;
		if (!job) {
			if (
				this.queue.length >= this.limits.queued &&
				(entry || this.running >= this.limits.concurrency)
			) {
				return Promise.reject(
					new AppError("Git status queue is full; retry shortly", 503, "GIT_STATUS_BUSY"),
				);
			}
			job = {
				key,
				loader,
				controller: new AbortController(),
				waiters: new Set(),
				state: "queued",
				invalidated: false,
				createdAt: performance.now(),
				readers: 0,
			};
			if (entry) entry.next = job;
			else this.entries.set(key, { current: job });
			this.queue.push(job);
			const pending = job;
			job.timer = setTimeout(() => {
				this.cancel(pending, new Error("Git status query timed out"));
			}, this.limits.timeoutMs);
		}
		const shared = job;
		if (shared.waiters.size >= 256)
			return Promise.reject(
				new AppError("Too many Git status readers; retry shortly", 503, "GIT_STATUS_BUSY"),
			);
		const result = new Promise<T>((resolve, reject) => {
			const abort = () => {
				shared.waiters.delete(waiter);
				waiter.cleanup();
				reject(signal?.reason ?? new DOMException("Aborted", "AbortError"));
				if (shared.waiters.size === 0)
					this.cancel(shared, new DOMException("No remaining status readers", "AbortError"));
			};
			const waiter: Waiter<T> = {
				resolve,
				reject,
				cleanup: () => signal?.removeEventListener("abort", abort),
			};
			shared.waiters.add(waiter);
			shared.readers++;
			signal?.addEventListener("abort", abort, { once: true });
		});
		this.pump();
		return result;
	}

	invalidate(key: string): void {
		const job = this.entries.get(key)?.current;
		// A queued query has not observed the repository yet, so it is already fresh.
		if (job?.state === "running") job.invalidated = true;
	}

	invalidateMatching(matches: (key: string) => boolean): void {
		for (const key of this.entries.keys()) {
			if (matches(key)) this.invalidate(key);
		}
	}

	clear(): void {
		this.clearing = true;
		try {
			for (const entry of [...this.entries.values()]) {
				if (entry.next) this.cancel(entry.next, new DOMException("Status reset", "AbortError"));
				this.cancel(entry.current, new DOMException("Status reset", "AbortError"));
			}
		} finally {
			this.clearing = false;
		}
	}

	private cancel(job: Job<T>, error: unknown): void {
		if (job.state === "finished") return;
		job.controller.abort(error);
		this.settle(job, undefined, error);
		// Running work keeps its slot until its subprocess actually exits.
		if (job.state === "queued") this.finish(job);
	}

	private settle(job: Job<T>, value?: T, error?: unknown): void {
		if (job.timer) clearTimeout(job.timer);
		for (const waiter of job.waiters) {
			waiter.cleanup();
			if (value) waiter.resolve(value);
			else waiter.reject(error);
		}
		job.waiters.clear();
	}

	private finish(job: Job<T>): void {
		if (job.state === "finished") return;
		if (job.state === "running") this.running--;
		else this.queue = this.queue.filter((queued) => queued !== job);
		job.state = "finished";
		const elapsedMs = Math.round(performance.now() - job.createdAt);
		if (elapsedMs > 1000)
			logger.warn("Slow coordinated Git status", {
				elapsedMs,
				shared: job.readers > 1,
				readers: job.readers,
				cancelled: job.controller.signal.aborted,
			});
		const entry = this.entries.get(job.key);
		if (entry?.current === job) {
			if (entry.next) this.entries.set(job.key, { current: entry.next });
			else this.entries.delete(job.key);
		} else if (entry?.next === job) {
			entry.next = undefined;
		}
		this.pump();
	}

	private pump(): void {
		if (this.clearing) return;
		while (this.running < this.limits.concurrency) {
			const index = this.queue.findIndex((job) => {
				const entry = this.entries.get(job.key);
				return entry?.current === job;
			});
			if (index < 0) return;
			const [job] = this.queue.splice(index, 1);
			if (!job) return;
			job.state = "running";
			this.running++;
			Promise.resolve()
				.then(() => {
					job.controller.signal.throwIfAborted();
					return job.loader(job.controller.signal);
				})
				.then(
					(value) => {
						this.finish(job);
						if (!job.controller.signal.aborted) this.settle(job, value);
					},
					(error: unknown) => {
						this.finish(job);
						this.settle(job, undefined, error);
					},
				);
		}
	}
}

const coordinator = new GitStatusQueryCoordinator();
export const coordinateGitStatusQuery = coordinator.query.bind(coordinator);
export const invalidateGitStatusQuery = coordinator.invalidate.bind(coordinator);
export const clearGitStatusQueries = coordinator.clear.bind(coordinator);

export function invalidateLocalGitStatusQueries(path: string): void {
	coordinator.invalidateMatching((key) => localGitStatusScopesOverlap(key, path));
}
