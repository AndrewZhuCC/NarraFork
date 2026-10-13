import type { GitWorkspaceState } from "@shared/git-workspace";

/** Filesystem facts only. Authorization is deliberately never retained here. */
export interface GitDiscovery {
	state: GitWorkspaceState;
	rootPath?: string;
	repositoryPath?: string;
	reason?: string;
	branch?: string | null;
}
interface Entry {
	deviceId: string;
	cwd: string;
	value?: GitDiscovery;
	expiresAt: number;
}

export class GitDiscoveryCache {
	private entries = new Map<string, Entry>();
	private inflight = new Map<string, Promise<GitDiscovery>>();
	private active = 0;
	private queue: Array<() => void> = [];
	constructor(
		private readonly maxEntries = 128,
		private readonly ttlMs = 30_000,
		private readonly now: () => number = Date.now,
		private readonly staleWhileRevalidate = false,
		private readonly maxConcurrent = 4,
	) {}

	peek(deviceId: string, runtimeGeneration: number, cwd: string): GitDiscovery | undefined {
		const entry = this.entries.get(JSON.stringify([deviceId, runtimeGeneration, cwd]));
		return entry?.value ? { ...entry.value } : undefined;
	}

	private probe(key: string, deviceId: string, cwd: string, load: () => Promise<GitDiscovery>) {
		const existing = this.inflight.get(key);
		if (existing) return existing;
		if (this.queue.length >= 32) return Promise.reject(new Error("Git discovery queue is full"));
		let entry = this.entries.get(key);
		if (!entry) {
			if (this.entries.size >= this.maxEntries) {
				for (const oldKey of this.entries.keys()) {
					if (!this.inflight.has(oldKey)) {
						this.entries.delete(oldKey);
						break;
					}
				}
			}
			entry = { deviceId, cwd, expiresAt: 0 };
			if (this.entries.size < this.maxEntries) this.entries.set(key, entry);
		}
		const current = entry;
		const pending = new Promise<GitDiscovery>((resolve, reject) => {
			const run = () => {
				this.active++;
				Promise.resolve()
					.then(load)
					.then((value) => {
						if (
							(value.state === "ready" || value.state === "not_git") &&
							this.entries.get(key) === current
						) {
							current.value = { ...value };
							current.expiresAt = this.now() + this.ttlMs;
						} else if (value.state !== "ready" && value.state !== "not_git")
							this.entries.delete(key);
						return value;
					})
					.finally(() => {
						this.active--;
						this.inflight.delete(key);
						if (!current.value && this.entries.get(key) === current) this.entries.delete(key);
						this.queue.shift()?.();
					})
					.then(resolve, reject);
			};
			if (this.active < this.maxConcurrent) run();
			else this.queue.push(run);
		});
		this.inflight.set(key, pending);
		return pending;
	}

	async get(
		deviceId: string,
		runtimeGeneration: number,
		cwd: string,
		load: () => Promise<GitDiscovery>,
		signal?: AbortSignal,
	): Promise<GitDiscovery> {
		signal?.throwIfAborted();
		const key = JSON.stringify([deviceId, runtimeGeneration, cwd]);
		const entry = this.entries.get(key);
		if (entry?.value) {
			this.entries.delete(key);
			this.entries.set(key, entry);
			if (entry.expiresAt > this.now()) return { ...entry.value };
			if (this.staleWhileRevalidate) {
				// No HTTP principal is retained; the caller reauthorizes these FS facts.
				void this.probe(key, deviceId, cwd, load).catch(() => {});
				return { ...entry.value };
			}
		}
		const pending = this.probe(key, deviceId, cwd, load);
		// A cancelled waiter must not abort another caller's shared bounded probe.
		const value = await new Promise<GitDiscovery>((resolve, reject) => {
			const abort = () => reject(signal?.reason);
			signal?.addEventListener("abort", abort, { once: true });
			pending.then(resolve, reject).finally(() => signal?.removeEventListener("abort", abort));
			if (signal?.aborted) abort();
		});
		signal?.throwIfAborted();
		return { ...value };
	}

	invalidate(
		deviceId: string,
		path: string,
		repositoryPath?: string,
		equals: (a: string, b: string) => boolean = (a, b) => a === b,
	): void {
		for (const [key, entry] of this.entries) {
			if (
				entry.deviceId === deviceId &&
				(this.inflight.has(key) ||
					equals(entry.cwd, path) ||
					(entry.value?.rootPath && equals(entry.value.rootPath, path)) ||
					(repositoryPath &&
						entry.value?.repositoryPath &&
						equals(entry.value.repositoryPath, repositoryPath)))
			) {
				this.entries.delete(key);
				// Keep the old promise singleflight but its detached entry cannot repopulate.
			}
		}
	}

	get size(): number {
		return this.entries.size;
	}
}

export const gitDiscoveryCache = new GitDiscoveryCache(128, 30_000, Date.now, true);
