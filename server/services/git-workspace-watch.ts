import { createHash } from "node:crypto";
import { lstat } from "node:fs/promises";
import { resolve } from "node:path";
import type {
	GitWorkspaceCategory,
	GitWorkspaceSnapshot,
	GitWorkspaceView,
} from "@shared/git-workspace-events";
import { AppError } from "../lib/errors";
import { eventBus } from "../lib/event-bus";
import { logger } from "../lib/logger";
import { safeSpawn } from "../lib/spawn";
import { gitDiscoveryCache } from "./git-discovery-cache";
import {
	getGitWorkspaceLineStats,
	getGitWorkspaceStatus,
	invalidateGitWorkspace,
	validateGitWorkspaceCache,
} from "./git-management-service";
import { localGitStatusScopesOverlap } from "./git-status-query";
import type { GitWorkspaceTarget } from "./git-workspace";
import { getRemoteGitStatusQueryKey } from "./remote-git-service";

export const WATCH_INTERVAL_MS = 3000;
export const WATCH_MAX_BYTES = 128 * 1024;
const CATEGORIES: GitWorkspaceCategory[] = ["worktree", "index", "refs", "head", "stash"];
export interface WatchSample {
	fingerprints: Partial<Record<GitWorkspaceCategory, string>>;
	uncertain: GitWorkspaceCategory[];
}

export function changedGitCategories(
	previous: WatchSample | undefined,
	sample: WatchSample,
): GitWorkspaceCategory[] {
	return CATEGORIES.filter(
		(category) =>
			sample.uncertain.includes(category) ||
			(previous === undefined
				? sample.fingerprints[category] !== undefined
				: sample.fingerprints[category] !== previous.fingerprints[category]),
	);
}

function fingerprint(value: string): string {
	return createHash("sha256").update(value).digest("hex");
}

export async function probeGitWatch(
	target: GitWorkspaceTarget,
	signal: AbortSignal,
): Promise<WatchSample> {
	signal = AbortSignal.any([signal, AbortSignal.timeout(20_000)]);
	const { backend, workspace } = target;
	const root = workspace.rootPath;
	if (!backend || !root) throw new Error("Workspace unavailable");
	const fingerprints: WatchSample["fingerprints"] = {};
	const uncertain = new Set<GitWorkspaceCategory>();
	if (backend.kind === "remote") {
		if (!backend.gitWorkspace) throw new Error("Git workspace RPC unavailable");
		if (backend.supportsGitWorkspaceWatch) {
			const sample = await backend.gitWorkspace(
				{
					cwd: root,
					expectedRoot: root,
					operation: "watch",
					maxBytes: WATCH_MAX_BYTES,
					timeoutMs: 5000,
				},
				signal,
			);
			const outputs = sample.outputs ?? {};
			for (const category of ["worktree", "index", "head", "stash"] as const) {
				if (typeof outputs[category] !== "string")
					throw new Error("Incomplete Git watch fingerprint");
				fingerprints[category] = outputs[category];
			}
			if (sample.truncated) {
				// Metadata budgets never invalidate the independently bounded HEAD identity.
				uncertain.add("worktree");
				uncertain.add("index");
				uncertain.add("stash");
			}
			if (outputs.uncertainWorktree === "true") uncertain.add("worktree");
			return { fingerprints, uncertain: [...uncertain] };
		}
		const status = await backend.gitWorkspace(
			{
				cwd: root,
				expectedRoot: root,
				operation: "status",
				maxBytes: WATCH_MAX_BYTES,
				timeoutMs: 5000,
			},
			signal,
		);
		if (status.state && status.state !== "ready") throw new Error("Git watch unavailable");
		const outputs = status.outputs ?? {};
		if (outputs.head === undefined || outputs.branch === undefined || outputs.status === undefined)
			throw new Error("Incomplete remote Git watch status");
		fingerprints.head = fingerprint(`${outputs.head}\0${outputs.branch}`);
		if (outputs.stagedNumstat === undefined || status.truncated) uncertain.add("index");
		else fingerprints.index = fingerprint(outputs.stagedNumstat);
		// v1 exposes HEAD/branch, not unrelated refs. Never turn this limitation
		// into periodic log invalidations. Bounded patches detect equal-line edits.
		let patch = "";
		if (outputs.status || status.truncated) {
			const diff = await backend.gitWorkspace(
				{
					cwd: root,
					expectedRoot: root,
					operation: "fullDiff",
					maxBytes: WATCH_MAX_BYTES,
					timeoutMs: 5000,
				},
				signal,
			);
			patch = diff.stdout ?? "";
			if (diff.truncated || /Binary files .* differ|Subproject commit/.test(patch))
				uncertain.add("worktree");
		}
		fingerprints.worktree = fingerprint(`${outputs.status}\0${patch}`);
		if (status.truncated) uncertain.add("worktree");
		const stash = await backend.gitWorkspace(
			{
				cwd: root,
				expectedRoot: root,
				operation: "stashList",
				maxBytes: WATCH_MAX_BYTES,
				timeoutMs: 5000,
				limit: 100,
			},
			signal,
		);
		fingerprints.stash = fingerprint(stash.stdout ?? "");
		// Comparing the bounded prefix never makes the log refresh periodically.
		if (stash.truncated) uncertain.add("stash");
	} else {
		const run = async (args: string[], category: GitWorkspaceCategory) => {
			const result = await safeSpawn({
				cmd: ["git", "--no-optional-locks", "-C", root, ...args],
				timeout: 5000,
				maxOutputBytes: WATCH_MAX_BYTES,
				signal,
			});
			if (result.stdoutTruncated || result.stderrTruncated) uncertain.add(category);
			if (result.exitCode !== 0 && !(args[0] === "rev-parse" && result.exitCode === 1))
				throw new Error("Git watch probe failed");
			return result.stdout;
		};
		const status = await run(
			["status", "--porcelain=v1", "-z", "--untracked-files=all", "--branch"],
			"worktree",
		);
		// The panel's log is HEAD-only (no --all or ref decorations). Enumerating
		// all refs is both unnecessary and makes large ref sets perpetually truncated.
		fingerprints.refs = fingerprint(
			await run(["rev-parse", "--verify", "--quiet", "HEAD"], "refs"),
		);
		fingerprints.stash = fingerprint(await run(["stash", "list", "--format=%gd:%H"], "stash"));
		fingerprints.index = fingerprint(
			await run(
				["diff", "--cached", "--raw", "--no-abbrev", "--no-ext-diff", "--no-textconv"],
				"index",
			),
		);
		const records = status.split("\0");
		fingerprints.head = fingerprint(records[0]?.startsWith("##") ? records[0] : "");
		const hash = createHash("sha256");
		let scanned = 0;
		for (let i = 0; i < records.length; i++) {
			const record = records[i];
			if (!record || record.startsWith("##")) continue;
			hash.update(record);
			if (++scanned > 2000) {
				uncertain.add("worktree");
				break;
			}
			signal.throwIfAborted();
			const path = record.slice(3);
			const metadata = await lstat(resolve(root, path), { bigint: true }).catch(() => null);
			if (metadata?.isDirectory()) uncertain.add("worktree");
			hash.update(
				metadata
					? `${path}:${metadata.size}:${metadata.mtimeNs}:${metadata.ctimeNs}:${metadata.ino}`
					: `${path}:missing`,
			);
			if (/[RC]/.test(record.slice(0, 2))) hash.update(records[++i] ?? "");
		}
		fingerprints.worktree = hash.digest("hex");
	}
	return { fingerprints, uncertain: [...uncertain] };
}

export const GIT_SNAPSHOT_MAX_BYTES = 128 * 1024;
const REFRESH_INTERVAL_MS = 1000;
const REVALIDATE_INTERVAL_MS = 60_000;
let nextRevision = 0;

/** Bound the shared projection once, rather than scanning every file for each socket. */
export function boundGitSnapshot(snapshot: GitWorkspaceSnapshot): GitWorkspaceSnapshot {
	const status = snapshot.status;
	const originalFiles = status?.files;
	if (!status || !originalFiles) return snapshot;
	let files = originalFiles.slice(0, 200);
	const project = (): GitWorkspaceSnapshot => {
		const paths = new Set(files.map((file) => file.path));
		const truncated = files.length < originalFiles.length;
		return {
			...snapshot,
			status: { ...status, files, truncated: status.truncated || truncated },
			...(snapshot.lineStats && {
				lineStats: {
					...snapshot.lineStats,
					files: snapshot.lineStats.files?.filter((file) => paths.has(file.path)),
				},
			}),
			detailsTruncated: snapshot.detailsTruncated || snapshot.status?.truncated || truncated,
		};
	};
	let result = project();
	// Leave room for the bounded subscription identity and JSON envelope.
	while (
		files.length &&
		Buffer.byteLength(JSON.stringify(result)) > GIT_SNAPSHOT_MAX_BYTES - 4096
	) {
		files = files.slice(0, Math.floor(files.length / 2));
		result = project();
	}
	return result;
}

export function projectGitSnapshot(
	snapshot: GitWorkspaceSnapshot,
	view: GitWorkspaceView,
): GitWorkspaceSnapshot {
	if (view === "details") return snapshot;
	const result = { ...snapshot, detailsTruncated: undefined };
	if (snapshot.status) {
		const { files: _files, ...status } = snapshot.status;
		result.status = status;
	}
	if (snapshot.lineStats) {
		const { files: _files, ...lineStats } = snapshot.lineStats;
		result.lineStats = lineStats;
	}
	return result;
}

interface Listener {
	changed(categories: GitWorkspaceCategory[]): void;
	snapshot?(snapshot: GitWorkspaceSnapshot): void;
}
interface Entry {
	target: GitWorkspaceTarget;
	listeners: Set<Listener>;
	controller: AbortController;
	timer?: ReturnType<typeof setTimeout>;
	previous?: WatchSample;
	probed: boolean;
	failures: number;
	running: boolean;
	snapshot?: GitWorkspaceSnapshot;
	dirty: boolean;
	revision: number;
	refreshing: boolean;
	refreshTimer?: ReturnType<typeof setTimeout>;
	lastRefreshAt: number;
	lastValidatedAt: number;
	lineStatsUnsupported?: boolean;
}
const snapshotReads = {
	status: getGitWorkspaceStatus,
	lineStats: getGitWorkspaceLineStats,
	invalidate: invalidateGitWorkspace,
	validate: validateGitWorkspaceCache,
};

/** One probe and refresh per device/worktree, only while a consumer is subscribed. */
export class GitWorkspaceWatchPool {
	private entries = new Map<string, Entry>();
	private active = 0;
	private offInvalidated?: () => void;
	constructor(
		private probe = probeGitWatch,
		private interval = WATCH_INTERVAL_MS,
		private reads = snapshotReads,
	) {}

	subscribe(
		target: GitWorkspaceTarget,
		changed: Listener["changed"],
		snapshot?: Listener["snapshot"],
	): () => void {
		const key = `${target.workspace.deviceId}:${target.workspace.workspaceKey}`;
		let entry = this.entries.get(key);
		if (!entry) {
			if (this.entries.size >= 128) throw new Error("Git watch capacity reached");
			entry = {
				target,
				listeners: new Set(),
				controller: new AbortController(),
				failures: 0,
				running: false,
				probed: false,
				dirty: true,
				revision: ++nextRevision,
				refreshing: false,
				lastRefreshAt: 0,
				lastValidatedAt: Date.now(),
			};
			this.entries.set(key, entry);
		} else if (
			(target.backend?.runtimeGeneration ?? 0) > (entry.target.backend?.runtimeGeneration ?? 0)
		) {
			entry.target = target;
			entry.previous = undefined;
			entry.probed = false;
			entry.snapshot = undefined;
			entry.lineStatsUnsupported = false;
			entry.dirty = true;
			entry.revision = ++nextRevision;
			entry.failures = 0;
			entry.controller.abort();
			entry.controller = new AbortController();
			clearTimeout(entry.timer);
			clearTimeout(entry.refreshTimer);
			entry.timer = undefined;
			entry.refreshTimer = undefined;
			// Old operations keep their slots until cancellation settles.
		}
		if (!this.offInvalidated) {
			const onInvalidated = ({ queryKey }: { queryKey: string }) => {
				for (const candidate of this.entries.values()) {
					const { backend, workspace } = candidate.target;
					const root = workspace.rootPath ?? workspace.cwd;
					const matches =
						backend?.kind === "remote"
							? getRemoteGitStatusQueryKey(backend, root) === queryKey
							: localGitStatusScopesOverlap(queryKey, root);
					if (matches) this.markDirty(candidate);
				}
			};
			eventBus.on("git:workspace_invalidated", onInvalidated);
			this.offInvalidated = () => eventBus.off("git:workspace_invalidated", onInvalidated);
		}
		const listener = { changed, snapshot };
		entry.listeners.add(listener);
		if (entry.snapshot) snapshot?.(entry.snapshot);
		if (snapshot && entry.dirty) this.scheduleRefresh(entry, true);
		if (!entry.timer && !entry.running) void this.tick(entry);
		return () => {
			entry.listeners.delete(listener);
			if (entry.listeners.size) return;
			entry.controller.abort();
			clearTimeout(entry.timer);
			clearTimeout(entry.refreshTimer);
			if (this.entries.get(key) === entry) this.entries.delete(key);
			if (!this.entries.size) {
				this.offInvalidated?.();
				this.offInvalidated = undefined;
			}
		};
	}

	/** An explicit refresh is authorized by the socket before reaching this method. */
	refresh(target: GitWorkspaceTarget): void {
		const entry = this.entries.get(`${target.workspace.deviceId}:${target.workspace.workspaceKey}`);
		if (!entry) return;
		this.reads.invalidate(entry.target);
		this.markDirty(entry);
		clearTimeout(entry.refreshTimer);
		entry.refreshTimer = undefined;
		this.scheduleRefresh(entry, true);
		for (const listener of entry.listeners)
			listener.changed(["worktree", "index", "refs", "stash"]);
	}

	private publish(entry: Entry, snapshot: GitWorkspaceSnapshot): void {
		entry.snapshot = boundGitSnapshot(snapshot);
		for (const listener of entry.listeners) listener.snapshot?.(entry.snapshot);
	}

	private markDirty(entry: Entry): void {
		if (!entry.dirty) {
			entry.dirty = true;
			entry.revision = ++nextRevision;
			if (entry.snapshot)
				this.publish(entry, {
					...entry.snapshot,
					revision: entry.revision,
					updating: true,
					statusError: undefined,
					lineStatsError: undefined,
				});
		}
		this.scheduleRefresh(entry);
	}

	private scheduleRefresh(entry: Entry, immediate = false): void {
		if (!entry.probed || entry.refreshing || entry.refreshTimer || entry.controller.signal.aborted)
			return;
		if (![...entry.listeners].some((listener) => listener.snapshot)) return;
		const delay = immediate
			? 0
			: Math.max(0, REFRESH_INTERVAL_MS - (Date.now() - entry.lastRefreshAt));
		entry.refreshTimer = setTimeout(() => {
			entry.refreshTimer = undefined;
			void this.refreshFacts(entry);
		}, delay);
		entry.refreshTimer.unref?.();
	}

	private async refreshFacts(entry: Entry): Promise<void> {
		if (entry.controller.signal.aborted || !entry.dirty || entry.refreshing) return;
		entry.refreshing = true;
		entry.dirty = false;
		entry.lastRefreshAt = Date.now();
		const { controller, target, revision } = entry;
		// A newly observed directory has no fingerprint tied to the idle HTTP cache.
		// Read fresh once; later subscribers reuse this entry without invalidating it.
		const ttlMs = entry.snapshot?.status ? undefined : 0;
		const current = () => !controller.signal.aborted && entry.revision === revision;
		this.publish(entry, {
			...entry.snapshot,
			revision,
			updating: true,
			statusError: undefined,
			lineStatsError: undefined,
		});
		let statusReady = false;
		try {
			const status = await this.reads.status(target, controller.signal, ttlMs);
			if (!current()) return;
			statusReady = true;
			this.publish(entry, {
				...entry.snapshot,
				revision,
				status,
				updating: true,
				detailsTruncated: status.truncated,
			});
			if (entry.lineStatsUnsupported) {
				this.publish(entry, {
					...entry.snapshot,
					revision,
					updating: false,
					lineStatsError: "GIT_LINE_STATS_UNSUPPORTED",
				});
				return;
			}
			const lineStats = await this.reads.lineStats(target, controller.signal, ttlMs);
			if (!current()) return;
			entry.lastValidatedAt = Date.now();
			this.publish(entry, { ...entry.snapshot, revision, lineStats, updating: false });
		} catch (error) {
			if (!current()) return;
			const code = error instanceof AppError ? error.code : "GIT_STATS_FAILED";
			if (statusReady && code === "GIT_LINE_STATS_UNSUPPORTED") entry.lineStatsUnsupported = true;
			this.publish(entry, {
				...entry.snapshot,
				revision,
				updating: false,
				...(statusReady ? { lineStatsError: code } : { statusError: code }),
			});
		} finally {
			entry.refreshing = false;
			if (entry.dirty) this.scheduleRefresh(entry);
		}
	}

	private async tick(entry: Entry): Promise<void> {
		if (entry.controller.signal.aborted || entry.running) return;
		entry.running = true;
		entry.timer = undefined;
		const { controller, target } = entry;
		if (this.active < 4) {
			this.active++;
			const started = performance.now();
			try {
				const sample = await this.probe(target, controller.signal);
				if (!controller.signal.aborted) {
					// Establish the directory baseline once, before reading facts. Its first
					// invalidation also excludes pre-baseline HTTP work still in flight;
					// additional subscribers never repeat this directory-level validation.
					const categories = entry.previous ? changedGitCategories(entry.previous, sample) : [];
					if (categories.includes("head") || categories.includes("refs"))
						gitDiscoveryCache.invalidate(
							target.workspace.deviceId,
							target.workspace.rootPath ?? target.workspace.cwd,
							target.repositoryPath,
							target.backend?.paths.equals,
						);
					if (
						!entry.previous ||
						categories.some((category) => category !== "stash") ||
						Date.now() - entry.lastValidatedAt >= REVALIDATE_INTERVAL_MS
					) {
						this.reads.invalidate(target);
						this.markDirty(entry);
						entry.lastValidatedAt = Date.now();
					} else if (entry.previous && !entry.dirty) {
						this.reads.validate(target);
					}
					entry.failures = 0;
					entry.previous = sample;
					for (const listener of entry.listeners) listener.changed(categories);
				}
			} catch {
				if (!controller.signal.aborted) {
					entry.failures = Math.min(entry.failures + 1, 4);
					if (Date.now() - entry.lastValidatedAt >= REVALIDATE_INTERVAL_MS) {
						this.reads.invalidate(target);
						this.markDirty(entry);
						entry.lastValidatedAt = Date.now();
					}
					for (const listener of entry.listeners) listener.changed([]);
				}
			} finally {
				this.active--;
				// A failed monitor must not block independently available status forever.
				if (controller === entry.controller) entry.probed = true;
				if (performance.now() - started > 5000)
					logger.warn("Slow Git workspace watch probe", {
						workspaceKey: target.workspace.workspaceKey,
						deviceId: target.workspace.deviceId,
						elapsedMs: Math.round(performance.now() - started),
					});
			}
		}
		entry.running = false;
		if (controller === entry.controller && entry.dirty) this.scheduleRefresh(entry);
		if (!entry.controller.signal.aborted) {
			const legacyRemote =
				entry.target.backend?.kind === "remote" && !entry.target.backend.supportsGitWorkspaceWatch;
			const interval = legacyRemote ? Math.max(30_000, this.interval) : this.interval;
			entry.timer = setTimeout(
				() => void this.tick(entry),
				controller !== entry.controller ? 0 : Math.min(60_000, interval * 2 ** entry.failures),
			);
			entry.timer.unref?.();
		}
	}
}
