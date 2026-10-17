/**
 * Worktree file watcher service.
 *
 * Monitors worktree directories for file changes and broadcasts git status
 * updates to subscribed narrators via WebSocket.
 *
 * Architecture (modelled after VS Code):
 *   optional isolated @parcel/watcher worker process
 *     → event coalescing (75ms aggregate + merge)
 *     → throttled emission (500/batch, 200ms rest)
 *     → debounce (1.5s) + rate limit
 *     → git status query + WS broadcast
 *
 * If the native worker is disabled or unhealthy, the service keeps the same
 * registry active and falls back to low-frequency git-status polling. The main
 * server never loads @parcel/watcher's native addon directly.
 *
 * The native worker is opt-in (`NARRAFORK_ENABLE_NATIVE_WATCHER`), so polling is not a
 * rare degraded mode — it is the default path for every attached narrator. That makes
 * the per-tick cost a main-thread budget question rather than a footnote: a polled tick
 * that finds unchanged git status and file/index metadata skips snapshot capture and
 * DAG advance, where the expensive work is — but only for a bounded run of ticks,
 * because unchanged metadata does not prove an unchanged workspace. See
 * `_processChange` and `MAX_SKIPPED_POLLS`.
 */

import { relative } from "node:path";
import { LOCAL_DEVICE_ID } from "../lib/agent/execution/backend";
import { eventBus } from "../lib/event-bus";
import { hotSafe } from "../lib/hot-safe";
import { logger } from "../lib/logger";
import { toForwardSlash } from "../lib/platform-path";
import type { Locale } from "../lib/prompt-i18n";
import { settings } from "../lib/settings";
import { isNativeWatcherEnabled, ParcelRecursiveWatcher } from "../lib/watcher/parcel-watcher";
import { FileChangeType } from "../lib/watcher/types";
import { advanceChapterSnapshot } from "./chapter-snapshot-ref";
import { commitSyncService } from "./commit-sync-service";
import { recordAttributions, wasRecentlyAttributed } from "./file-attribution-service";
import { gitService } from "./git-service";
import { getStatusSummaryCached, invalidateStatus } from "./git-status-cache";
import { worktreeTreeSnapshot } from "./worktree-tree-snapshot";
import { worktreeWatcherMetadata } from "./worktree-watcher-metadata";

/** Debounce interval for file change events (ms). */
const DEBOUNCE_MS = 1500;

/** How a watched path changed, in the vocabulary a file tree can act on. */
export type WatchedChangeKind = "added" | "updated" | "deleted";

/** Map `@parcel/watcher`'s numeric change type onto the wire vocabulary. */
function toChangeKind(type: FileChangeType): WatchedChangeKind {
	if (type === FileChangeType.ADDED) return "added";
	if (type === FileChangeType.DELETED) return "deleted";
	return "updated";
}

// ── Rate limiter ────────────────────────────────────────────────────────────

const RATE_WINDOW_MS = 2000;
const RATE_LIMIT = 200;
const FALLBACK_POLL_INTERVAL_MS = Math.max(
	1000,
	Number(process.env.NARRAFORK_WATCHER_POLL_INTERVAL_MS) || 5000,
);

interface RateLimitState {
	windowStart: number;
	count: number;
	warned: boolean;
}

// ── Watcher entry ───────────────────────────────────────────────────────────

interface WatcherEntry {
	chapterId: string;
	narratorIds: Set<string>;
	locale: Locale;
	debounceTimer?: ReturnType<typeof setTimeout>;
	pollTimer?: ReturnType<typeof setInterval>;
	lastHeadSha?: string;
	lastStatusSignature?: string;
	lastMetadataFingerprint?: string;
	processing: boolean;
	pendingProcess: boolean;
	/** A queued follow-up stays speculative only if every coalesced trigger is a poll. */
	pendingProcessPolled?: boolean;
	rateLimit: RateLimitState;
	/**
	 * Paths reported by the native watcher during the current debounce window,
	 * mapped to the kind of change last seen for each.
	 *
	 * A map rather than a set because a file tree cannot patch itself from paths
	 * alone: "this path changed" does not say whether a row should appear, be
	 * refreshed, or be removed. Attribution only ever needed the keys, which is why
	 * this used to be a `Set`.
	 */
	pendingPaths?: Map<string, WatchedChangeKind>;
	/** True when more paths changed than {@link MAX_PENDING_PATHS} allows. */
	pendingPathsTruncated?: boolean;
	/**
	 * Consecutive polled ticks skipped on an unchanged status signature.
	 *
	 * Reset by any tick that actually captures. Bounded by {@link MAX_SKIPPED_POLLS};
	 * see `_processChange` for why an unchanged signature is not proof of an unchanged
	 * workspace.
	 */
	skippedPolls?: number;
}

/**
 * Maximum idle polls before a forced capture and refresh. Structural status plus bounded
 * file/index metadata detects ordinary dirty-to-dirty edits without reading blobs or
 * computing line counts. Neither is a content hash, so the periodic sweep remains the
 * fallback for edits invisible to that evidence (12 ticks at the default 5 s interval).
 */
export const MAX_SKIPPED_POLLS = 12;

/**
 * Upper bound on paths tracked per debounce window.
 *
 * A build or dependency install can touch tens of thousands of files; retaining
 * them all would defeat the point of watching cheaply. Past the cap the batch is
 * marked truncated and attributed in aggregate instead of per path.
 */
const MAX_PENDING_PATHS = 500;

interface StatusSignatureFileInput {
	status: string;
	path: string;
	oldPath?: string;
}

interface StatusSignatureInput {
	hasChanges: boolean;
	staged: number;
	unstaged: number;
	untracked: number;
	totalFiles: number;
	totalFilesExact?: boolean;
	truncated?: boolean;
	headSha?: string;
	branch?: string;
	files?: StatusSignatureFileInput[];
}

function getStatusSignature(status: StatusSignatureInput): string {
	return JSON.stringify({
		hasChanges: status.hasChanges,
		staged: status.staged,
		unstaged: status.unstaged,
		untracked: status.untracked,
		totalFiles: status.totalFiles,
		totalFilesExact: status.totalFilesExact,
		truncated: status.truncated,
		headSha: status.headSha,
		branch: status.branch,
		files: (status.files ?? [])
			.slice(0, 200)
			.sort((a, b) => a.path.localeCompare(b.path) || a.status.localeCompare(b.status))
			.map((file) => ({
				status: file.status,
				path: file.path,
				oldPath: file.oldPath,
			})),
	});
}

// ── Singleton ParcelRecursiveWatcher ────────────────────────────────────────

/**
 * Single shared ParcelRecursiveWatcher instance, pinned to globalThis
 * so it survives Bun hot reloads.
 */
const parcelWatcher = hotSafe<ParcelRecursiveWatcher>(
	"narrafork.worktreeWatcher.parcel",
	() =>
		new ParcelRecursiveWatcher(
			(rootPath, events) => {
				// Events from parcel are already coalesced and throttled.
				// We just need to trigger the debounced git-status flow.
				//
				// The change kind is carried through rather than flattened to a path list:
				// the file tree needs to know whether a row appeared, changed or went away,
				// and that information exists only here.
				worktreeWatcher._onFileChange(
					rootPath,
					events.length,
					events.map((event) => ({ path: event.path, kind: toChangeKind(event.type) })),
				);
			},
			(reason) => {
				worktreeWatcher._onWatcherBackendUnavailable(reason);
			},
		),
);

// ── Public API ──────────────────────────────────────────────────────────────

export const worktreeWatcher = {
	_entries: hotSafe<Map<string, WatcherEntry>>(
		"narrafork.worktreeWatcher.entries",
		() => new Map(),
	),

	getActiveCount(): number {
		return this._entries.size;
	},

	/**
	 * Narrators attached to a worktree, or empty when nothing watches it.
	 *
	 * Exposed so the tool-boundary path can fan a workspace change out to every
	 * session viewing that directory without querying the database on the tool
	 * execution hot path. Returns a copy: the caller must not be able to mutate the
	 * watcher's live membership set.
	 */
	getAttachedNarratorIds(worktreePath: string): string[] {
		const entry = this._entries.get(worktreePath);
		return entry ? [...entry.narratorIds] : [];
	},

	getActivePaths(): Array<{ path: string; chapterId: string; narratorCount: number }> {
		const result: Array<{ path: string; chapterId: string; narratorCount: number }> = [];
		for (const [path, entry] of this._entries) {
			result.push({
				path,
				chapterId: entry.chapterId,
				narratorCount: entry.narratorIds.size,
			});
		}
		return result;
	},

	/**
	 * Start watching a worktree directory for file changes.
	 * Multiple narrators can share the same watcher (same chapter).
	 *
	 * Uses @parcel/watcher for a single native recursive subscription per
	 * worktree root, replacing the previous N × fs.watch() approach.
	 */
	watch(worktreePath: string, chapterId: string, narratorId: string, locale: Locale): void {
		const existing = this._entries.get(worktreePath);
		if (existing) {
			existing.narratorIds.add(narratorId);
			logger.debug("Worktree watcher: narrator added to existing watcher", {
				worktreePath,
				narratorId,
				totalNarrators: existing.narratorIds.size,
			});
			return;
		}

		const entry: WatcherEntry = {
			chapterId,
			narratorIds: new Set([narratorId]),
			locale,
			processing: false,
			pendingProcess: false,
			rateLimit: { windowStart: Date.now(), count: 0, warned: false },
		};

		this._entries.set(worktreePath, entry);

		// Start the native parcel watcher through an isolated worker process.
		// If disabled or unhealthy, keep the registry alive and use polling fallback.
		if (isNativeWatcherEnabled()) {
			parcelWatcher.watch(worktreePath).catch((err) => {
				logger.warn("Failed to start native worktree watcher", {
					worktreePath,
					error: String(err),
				});
				this._startFallbackPolling(worktreePath, entry, String(err));
			});
		} else {
			this._startFallbackPolling(worktreePath, entry, "native watcher disabled");
		}

		// Capture initial HEAD SHA
		gitService
			.getHeadCommit(worktreePath)
			.then((sha) => {
				if (this._entries.has(worktreePath)) {
					entry.lastHeadSha = sha;
				}
			})
			.catch(() => {});

		logger.info("Worktree watcher started", {
			worktreePath,
			chapterId,
			narratorId,
			activeEntries: this._entries.size,
		});
	},

	/**
	 * Remove a narrator's interest in a worktree.
	 * When the last narrator leaves, the watcher is closed.
	 */
	unwatch(worktreePath: string, narratorId: string): void {
		const entry = this._entries.get(worktreePath);
		if (!entry) return;

		entry.narratorIds.delete(narratorId);
		if (entry.narratorIds.size === 0) {
			this._removeEntry(worktreePath);
			logger.info("Worktree watcher stopped (no narrators left)", {
				worktreePath,
				activeEntries: this._entries.size,
			});
		} else {
			logger.debug("Worktree watcher: narrator removed", {
				worktreePath,
				narratorId,
				remaining: entry.narratorIds.size,
			});
		}
	},

	/**
	 * Force-close a watcher for a worktree path (e.g. before dormant removes the directory).
	 */
	unwatchAll(worktreePath: string): void {
		if (this._entries.has(worktreePath)) {
			this._removeEntry(worktreePath);
			logger.info("Worktree watcher force-stopped", {
				worktreePath,
				activeEntries: this._entries.size,
			});
		}
	},

	/** Shut down all watchers (server shutdown or startup recovery). */
	shutdown(): void {
		const count = this._entries.size;
		const paths = [...this._entries.keys()];
		for (const path of paths) {
			this._removeEntry(path);
		}
		// Also shut down the underlying parcel watcher
		parcelWatcher.shutdown().catch(() => {});
		if (count > 0) {
			logger.info("All worktree watchers shut down", { count });
		}
	},

	/** Internal: debounced handler for file change events with rate limiting. */
	_onFileChange(
		worktreePath: string,
		eventCount = 1,
		changedPaths?: readonly { path: string; kind: WatchedChangeKind }[],
	): void {
		const entry = this._entries.get(worktreePath);
		if (!entry) return;

		// Accumulate the paths seen during this debounce window so the flush can
		// attribute them. Bounded, because a runaway writer must not grow this set
		// without limit — beyond the cap the flush falls back to a git-status diff.
		if (changedPaths?.length) {
			if (!entry.pendingPaths) entry.pendingPaths = new Map();
			for (const change of changedPaths) {
				// An already-tracked path is updated in place rather than counted again:
				// re-seeing a path is not new information about the batch's size, and
				// letting it trip the cap would truncate a window that fit.
				if (entry.pendingPaths.size >= MAX_PENDING_PATHS && !entry.pendingPaths.has(change.path)) {
					entry.pendingPathsTruncated = true;
					break;
				}
				// Last kind wins. The coalescer upstream has already resolved the
				// interesting sequences (add-then-delete cancels, delete-then-add becomes
				// an update) within its own window; across windows, the latest observation
				// is the best available answer.
				entry.pendingPaths.set(change.path, change.kind);
			}
		}

		const normalizedEventCount = Math.max(1, eventCount);
		const now = Date.now();
		const rl = entry.rateLimit;
		if (now - rl.windowStart > RATE_WINDOW_MS) {
			rl.windowStart = now;
			rl.count = 0;
			rl.warned = false;
		}
		rl.count += normalizedEventCount;
		if (rl.count > RATE_LIMIT) {
			if (entry.debounceTimer) {
				clearTimeout(entry.debounceTimer);
				entry.debounceTimer = undefined;
			}
			if (!rl.warned) {
				rl.warned = true;
				logger.warn("Worktree watcher rate limit exceeded, suppressing events", {
					worktreePath,
					eventsInWindow: rl.count,
					eventCount: normalizedEventCount,
					windowMs: RATE_WINDOW_MS,
				});
			}
			return;
		}

		if (entry.debounceTimer) clearTimeout(entry.debounceTimer);
		entry.debounceTimer = setTimeout(() => {
			entry.debounceTimer = undefined;
			this._enqueueProcessChange(worktreePath, entry);
		}, DEBOUNCE_MS);
	},

	/** Internal: native watcher failed/disabled; enable polling for all active worktrees. */
	_onWatcherBackendUnavailable(reason: string): void {
		for (const [worktreePath, entry] of this._entries) {
			this._startFallbackPolling(worktreePath, entry, reason);
		}
	},

	/** Internal: start low-frequency git status polling fallback. */
	_startFallbackPolling(worktreePath: string, entry: WatcherEntry, reason: string): void {
		if (entry.pollTimer) return;
		logger.info("Worktree watcher fallback polling started", {
			worktreePath,
			chapterId: entry.chapterId,
			reason,
			intervalMs: FALLBACK_POLL_INTERVAL_MS,
		});
		entry.pollTimer = setInterval(() => {
			if (!this._entries.has(worktreePath)) return;
			// Flagged as polled: this tick has no evidence anything was written, so
			// `_processChange` may skip the snapshot work when the status is unchanged.
			// Without that, polling — which is the *default* path, since the native watcher
			// requires an opt-in env var — ran a whole-tree capture plus a DAG advance every
			// interval per chapter, forever, on an idle workspace.
			this._enqueueProcessChange(worktreePath, entry, true);
		}, FALLBACK_POLL_INTERVAL_MS);
	},

	/**
	 * Internal: coalesce concurrent git-status refreshes per worktree.
	 *
	 * `polled` distinguishes a timer tick — which is speculative, since nothing said
	 * anything changed — from a watcher event, which is evidence of a write. Only the
	 * speculative kind is allowed to skip snapshot work on an unchanged status.
	 *
	 * A coalesced follow-up stays speculative when only polls arrived. Any native
	 * event makes it a confirmed write, so overlapping idle polls cannot accidentally
	 * invalidate shared caches while real filesystem events still force a refresh.
	 */
	_enqueueProcessChange(worktreePath: string, entry: WatcherEntry, polled = false): void {
		if (!this._entries.has(worktreePath)) return;
		if (entry.processing) {
			entry.pendingProcessPolled = entry.pendingProcess
				? entry.pendingProcessPolled === true && polled
				: polled;
			entry.pendingProcess = true;
			return;
		}

		entry.processing = true;
		this._processChange(worktreePath, entry, polled)
			.catch((err) => {
				logger.debug("Worktree watcher change processing failed", {
					worktreePath,
					error: String(err),
				});
			})
			.finally(() => {
				entry.processing = false;
				if (!this._entries.has(worktreePath)) return;
				if (entry.pendingProcess) {
					const pendingPolled = entry.pendingProcessPolled === true;
					entry.pendingProcess = false;
					entry.pendingProcessPolled = undefined;
					this._enqueueProcessChange(worktreePath, entry, pendingPolled);
				}
			});
	},

	/** Internal: process a debounced file change event. */
	async _processChange(worktreePath: string, entry: WatcherEntry, polled = false): Promise<void> {
		const { chapterId, narratorIds } = entry;

		// Claim the paths accumulated during this window before any await, so a
		// concurrent event batch starts a fresh set rather than mutating this one.
		const changedPaths = entry.pendingPaths;
		const pathsTruncated = entry.pendingPathsTruncated === true;
		entry.pendingPaths = undefined;
		entry.pendingPathsTruncated = false;

		// Announce the individual paths BEFORE the git-status query, and independently of
		// whether the status signature moved.
		//
		// Both halves of that matter for a file tree. Firing first means the tree updates
		// without waiting on git subprocesses that tell it nothing it needs. Firing
		// unconditionally is what makes it correct at all: the tree shows every file,
		// including ones git never reports — `.gitignore`d build output, untracked
		// scratch files, and anything in a directory git considers clean. Gating this on
		// `statusChanged` would have made those rows silently permanent.
		this._emitPathChanges(worktreePath, entry, changedPaths, pathsTruncated);

		// Native events are evidence of a write. A speculative poll is not: get a
		// fresh lightweight status without evicting line stats or notifying directory
		// subscribers until the status/metadata comparison establishes a refresh.
		if (!polled) invalidateStatus(worktreePath);
		const statusSummary = await getStatusSummaryCached(worktreePath, { ttlMs: 0 });
		const currentHead = statusSummary.headSha;
		const previousHead = entry.lastHeadSha;
		const statusSignature = getStatusSignature(statusSummary);
		const statusChanged = statusSignature !== entry.lastStatusSignature;

		// Structural status cannot distinguish another edit to an already-dirty path.
		// Polls therefore compare bounded file/index metadata, never line counts or blobs.
		// Missing/partial evidence refreshes conservatively; the sweep still covers edits
		// invisible to metadata. Native events are themselves evidence and always refresh.
		const metadata = polled
			? await worktreeWatcherMetadata.probe(worktreePath, statusSummary)
			: undefined;
		const metadataChanged =
			metadata !== undefined &&
			(metadata.uncertain || metadata.fingerprint !== entry.lastMetadataFingerprint);
		const skipped = entry.skippedPolls ?? 0;
		const sweepDue = skipped >= MAX_SKIPPED_POLLS;
		const refreshNeeded =
			!polled ||
			statusChanged ||
			metadataChanged ||
			!!changedPaths?.size ||
			pathsTruncated ||
			sweepDue;
		if (refreshNeeded) {
			// Only changed/uncertain/sweep polls invalidate shared status and line stats.
			// Keep using this tick's fresh status below rather than querying it twice.
			if (polled) invalidateStatus(worktreePath);
			entry.skippedPolls = 0;
			await this._recordExternalChanges(worktreePath, entry, changedPaths, pathsTruncated);
		} else {
			entry.skippedPolls = skipped + 1;
		}
		entry.lastMetadataFingerprint = metadata?.uncertain ? undefined : metadata?.fingerprint;

		if (refreshNeeded) {
			entry.lastStatusSignature = statusSignature;

			// Strip files array from WS broadcast to keep payloads small
			const { files: _files, ...statusWithoutFiles } = statusSummary;

			// Broadcast git status to all subscribed narrators
			for (const narratorId of narratorIds) {
				eventBus.emit({
					type: "narrator:ws_broadcast",
					narratorId,
					message: {
						type: "git_status",
						narratorId,
						chapterId,
						toolUseId: "",
						status: statusWithoutFiles as typeof statusSummary,
					},
				});
			}
		}

		// Detect new commits (HEAD changed)
		if (currentHead && previousHead && currentHead !== previousHead) {
			entry.lastHeadSha = currentHead;

			// Sync commit history from git log
			try {
				const newCount = await commitSyncService.syncChapterCommits(chapterId);
				if (newCount > 0) {
					eventBus.emit({ type: "chapter:commits_updated", chapterId, newCount });
					for (const narratorId of narratorIds) {
						eventBus.emit({
							type: "narrator:ws_broadcast",
							narratorId,
							message: {
								type: "commits_updated",
								narratorId,
								chapterId,
								newCount,
							},
						});
					}
				}
			} catch (err) {
				logger.debug("Commit sync failed (watcher)", {
					chapterId,
					error: String(err),
				});
				// Tell the client its commit list is now stale. Without this the panel kept
				// showing cached rows with no indication that the last sync had failed.
				for (const narratorId of narratorIds) {
					eventBus.emit({
						type: "narrator:ws_broadcast",
						narratorId,
						message: {
							type: "commit_sync_error",
							narratorId,
							chapterId,
							code: "WORKTREE_WATCHER_COMMIT_SYNC_FAILED",
							error: String(err),
							fallback: true,
							backgroundSync: true,
						},
					});
				}
			}
		} else if (currentHead && !previousHead) {
			entry.lastHeadSha = currentHead;
		}

		if (refreshNeeded) {
			eventBus.emit({ type: "chapter:files_changed", chapterId, worktreePath });
		}
	},

	/**
	 * Internal: publish the individual paths seen in this window to attached narrators.
	 *
	 * Paths are made worktree-RELATIVE before they leave the server. The tree already
	 * knows its own root, and the absolute form would leak the host's directory layout
	 * (including the OS account name) to every subscriber of a narrator.
	 *
	 * Nothing is emitted when the window carried no paths, which is the normal case:
	 * the native watcher is opt-in (`NARRAFORK_ENABLE_NATIVE_WATCHER`), so the default
	 * path is git-status polling, which observes no paths at all. A subscriber must
	 * therefore treat this event as an accelerator and not as its only route to
	 * freshness — see `truncated` for the other case where the list is not the whole
	 * truth.
	 */
	_emitPathChanges(
		worktreePath: string,
		entry: WatcherEntry,
		changedPaths: Map<string, WatchedChangeKind> | undefined,
		truncated: boolean,
	): void {
		if (entry.narratorIds.size === 0) return;
		if (!changedPaths?.size && !truncated) return;

		const changes: { path: string; kind: WatchedChangeKind }[] = [];
		for (const [absolutePath, kind] of changedPaths ?? []) {
			// Forward slashes: the wire contract is a "/"-separated worktree-relative
			// path, and the client's `parentPath()` splits on "/" only. A Windows path
			// separated by backslashes therefore read as a root-level entry, so a file
			// created or deleted inside a subdirectory invalidated the ROOT listing rather
			// than that directory, and never appeared until the panel was remounted.
			const relPath = toForwardSlash(relative(worktreePath, absolutePath));
			// Outside the root: nothing a tree rooted here can place.
			if (!relPath || relPath.startsWith("..")) continue;
			changes.push({ path: relPath, kind });
		}
		if (changes.length === 0 && !truncated) return;

		// Delivered per narrator over the WS only.
		//
		// There is deliberately no parallel `eventBus` broadcast: this payload is
		// narrator-scoped, every consumer reaches it through the socket, and a bus event
		// nothing subscribes to is worse than absent — the next reader assumes some other
		// path depends on it and preserves it through refactors that should have dropped it.
		// `chapter:files_changed` above remains the pathless, graph-facing signal.
		for (const narratorId of entry.narratorIds) {
			eventBus.emit({
				type: "narrator:ws_broadcast",
				narratorId,
				message: {
					type: "workspace_paths_changed",
					narratorId,
					chapterId: entry.chapterId,
					toolUseId: "",
					changes,
					truncated,
				},
			});
		}
	},

	/**
	 * Internal: attribute changes this watcher saw that no tool claimed, and record a
	 * workspace boundary for them.
	 *
	 * "External" means a terminal command, an editor, or a build script — anything
	 * outside the tool path. The tool path shadows its own writes via
	 * `markRecentlyAttributed`, so whatever remains here genuinely came from
	 * elsewhere and would otherwise be invisible in the modification view.
	 *
	 * A tree snapshot is taken only while a narrator is attached: capturing on every
	 * idle-period edit would pay `write-tree` for changes no session is going to ask
	 * about.
	 */
	async _recordExternalChanges(
		worktreePath: string,
		entry: WatcherEntry,
		changedPaths: Map<string, WatchedChangeKind> | undefined,
		truncated: boolean,
		options?: { skipSnapshot?: boolean },
	): Promise<void> {
		if (entry.narratorIds.size === 0) return;
		// Attribute to any attached narrator: the workspace timeline is shared by all
		// of them, and an external edit belongs to none in particular.
		const [narratorId] = entry.narratorIds;

		if (changedPaths?.size) {
			const unclaimed: string[] = [];
			for (const absolutePath of changedPaths.keys()) {
				// Forward slashes, because that is how the attribution shadow is keyed
				// (`track-file-change.ts` and the fs route both normalize before recording).
				// A backslash key never matched, so on Windows every agent Write/Edit was
				// recorded a SECOND time as an external change: the same file listed twice
				// under two spellings, an "external change" badge on the agent's own edit,
				// and scoped revert warning about changes made outside the session.
				const relPath = toForwardSlash(relative(worktreePath, absolutePath));
				if (!relPath || relPath.startsWith("..")) continue;
				if (wasRecentlyAttributed(worktreePath, relPath)) continue;
				unclaimed.push(relPath);
			}
			if (unclaimed.length > 0) {
				try {
					await recordAttributions(
						{
							deviceId: LOCAL_DEVICE_ID,
							workspacePath: worktreePath,
							narratorId: null,
							action: "external",
							toolName: null,
							toolUseId: null,
						},
						unclaimed,
					);
				} catch (err) {
					logger.debug("Failed to record external file attributions", {
						worktreePath,
						error: String(err),
					});
				}
			}
		}

		if (truncated) {
			logger.debug("External change batch truncated; attributed in aggregate", {
				worktreePath,
				cap: MAX_PENDING_PATHS,
			});
		}

		// A boundary here is what makes an external edit revertable at all — but only
		// when something actually changed. See `_processChange` for why an unchanged git
		// status is sufficient grounds to skip it.
		if (options?.skipSnapshot) return;
		if (!settings.chapters.treeSnapshotsEnabled) return;
		// The hot-path variant: this capture shares the narrator tool path's shadow
		// lock, so it must never queue an unbounded scan behind (or ahead of) one.
		const treeHash = await worktreeTreeSnapshot.tryCaptureHot(worktreePath, LOCAL_DEVICE_ID);
		if (treeHash) {
			// Link it into the snapshot DAG with the tree just captured. Without this the
			// lineage would only advance on narrator tool calls, so a fork taken after
			// the user edited in their own editor — or after a build script wrote — would
			// start from a state that predates those writes and silently lose them.
			await advanceChapterSnapshot(worktreePath, treeHash, "external workspace change");
			eventBus.emit({
				type: "chapter:external_change_recorded",
				chapterId: entry.chapterId,
				worktreePath,
				treeHash,
				narratorId,
			});
		}
	},

	/** Internal: close and remove a watcher entry. */
	_removeEntry(worktreePath: string): void {
		const entry = this._entries.get(worktreePath);
		if (!entry) return;
		if (entry.debounceTimer) clearTimeout(entry.debounceTimer);
		if (entry.pollTimer) clearInterval(entry.pollTimer);
		entry.pendingProcess = false;
		this._entries.delete(worktreePath);

		// Stop the underlying parcel watcher for this path
		parcelWatcher.unwatch(worktreePath).catch(() => {});
	},
};
