/**
 * Short-TTL cache for legacy status consumers. The service-level query coordinator
 * shares subprocesses with fresh management reads and owns cancellation/queue bounds.
 * This layer caches only successful, non-invalidated results; each subscriber still
 * goes through the coordinator so one HTTP disconnect cannot cancel other readers.
 */
import { logger } from "../lib/logger";
import { invalidateBoundaries } from "./git-commit-boundary-cache";
import { gitDiscoveryCache } from "./git-discovery-cache";
import { type GitStatusSummary, gitService, withGitRequestContext } from "./git-service";
import {
	clearGitStatusQueries,
	getLocalGitStatusQueryKey,
	invalidateLocalGitStatusQueries,
	localGitStatusScopesOverlap,
} from "./git-status-query";
import { normalizeWorkspacePath } from "./git-workspace";

/** How long a cached status is considered fresh (ms). */
const DEFAULT_TTL_MS = 800;

/**
 * Maximum retained entries.
 *
 * `dropStatus` removes a worktree's entry at each of the three sites that destroy one, so
 * in normal operation the map tracks live worktrees and stays small. This is the backstop
 * for the paths that are not a clean destroy: a directory removed outside NarraFork, a
 * chapter whose cleanup failed partway, or any future caller that queries a transient path
 * and never announces its end. Each entry holds a `GitStatusSummary` with up to 200
 * `GitStatusFile` records, so an unbounded map is a slow leak rather than a trivial one.
 *
 * Eviction is safe by construction: an entry is a cache of a value that can always be
 * recomputed from git, so losing one costs a query and never correctness. Sized well above
 * any plausible number of simultaneously active worktrees, so a real workload is never
 * evicting the entries it is about to reuse.
 *
 * Matches `git-commit-boundary-cache`'s approach, which caps for the same reason. The two
 * numbers differ because the key spaces do: boundaries are keyed by (workspace, HEAD, path
 * set) and churn with every staging change, while this is keyed by workspace alone.
 */
const MAX_ENTRIES = 128;

interface CacheEntry {
	/** Resolved status, or null while the first query is in flight. */
	value: GitStatusSummary | null;
	/** Epoch ms when `value` was produced. */
	producedAt: number;
	/** Readers remain independent; the service coordinator shares their subprocess. */
	pending: number;
	/** An invalidated query must not repopulate the short-TTL cache. */
	version: number;
}

/** Insertion-ordered, so the first key is the least recently used. */
const cache = new Map<string, CacheEntry>();

/**
 * Evict least-recently-used entries until the cache is within its cap.
 *
 * Keep entries with active readers until they settle, so completion can publish into
 * the same generation-checked cache slot. Subprocess sharing is handled below this cache.
 */
function evictIfNeeded(): void {
	if (cache.size <= MAX_ENTRIES) return;
	for (const [key, entry] of cache) {
		if (cache.size <= MAX_ENTRIES) return;
		if (entry.pending > 0) continue;
		cache.delete(key);
	}
}

/** Mark `key` as most recently used by reinserting it at the end. */
function touch(key: string, entry: CacheEntry): void {
	cache.delete(key);
	cache.set(key, entry);
}

export async function getStatusSummaryCached(
	rawPath: string,
	opts: { ttlMs?: number; signal?: AbortSignal } = {},
): Promise<GitStatusSummary> {
	opts.signal?.throwIfAborted();
	const key = getLocalGitStatusQueryKey(rawPath);
	const ttl = opts.ttlMs ?? DEFAULT_TTL_MS;
	let entry = cache.get(key);
	if (entry?.value && ttl > 0 && Date.now() - entry.producedAt < ttl) {
		touch(key, entry);
		return entry.value;
	}
	if (entry) touch(key, entry);
	else {
		entry = { value: null, producedAt: 0, pending: 0, version: 0 };
		cache.set(key, entry);
	}
	const live = entry;
	const version = live.version;
	live.pending++;
	try {
		// Each reader subscribes independently (especially its cancellation signal).
		// gitService coordinates these with uncached REST/remote management reads.
		const read = () => gitService.getStatusSummary(rawPath);
		const summary = await (opts.signal ? withGitRequestContext(opts.signal, read) : read());
		if (cache.get(key) === live && live.version === version) {
			live.value = summary;
			live.producedAt = Date.now();
		}
		return summary;
	} catch (error) {
		logger.debug("Status query failed", { workspacePath: key, error: String(error) });
		throw error;
	} finally {
		live.pending--;
		if (live.pending === 0 && !live.value && cache.get(key) === live) cache.delete(key);
		evictIfNeeded();
	}
}

/**
 * Read the last cached status without triggering a query.
 * Returns null when nothing is cached.
 */
export function peekStatusSummary(rawPath: string): GitStatusSummary | null {
	return cache.get(getLocalGitStatusQueryKey(rawPath))?.value ?? null;
}

/**
 * Invalidate the cached status for a path, forcing the next read to refresh.
 * Call this after a write operation (commit, stage, discard) or on file change.
 */
export function invalidateStatus(rawPath: string): void {
	// Commit boundaries are derived from the same working tree, so they go stale on
	// exactly the events that invalidate a status. Invalidating both here — rather than
	// asking every call site to remember a second function — is what keeps them from
	// drifting apart. Synchronous on purpose: a deferred invalidation would leave a
	// window in which a refetch could still read boundaries from the old HEAD.
	invalidateBoundaries(rawPath);
	gitDiscoveryCache.invalidate(
		"local",
		rawPath,
		undefined,
		(a, b) => normalizeWorkspacePath(a) === normalizeWorkspacePath(b),
	);

	invalidateLocalGitStatusQueries(rawPath);
	for (const [key, entry] of cache) {
		if (!localGitStatusScopesOverlap(key, rawPath)) continue;
		entry.version++;
		entry.value = null;
		entry.producedAt = 0;
	}
}

/**
 * Drop a path's cache entry entirely — for when the worktree itself is gone.
 *
 * `invalidateStatus` is not a substitute: it nulls the value but keeps the key, precisely
 * because the path is expected to come back. Once the directory is destroyed nothing can
 * read the entry again, so keeping the key needlessly retains up to 200 file records
 * until eventual LRU eviction.
 *
 * Called from the three places that destroy a worktree, alongside their existing
 * `worktreeTreeSnapshot.destroy` call for the same reason — a per-path resource that
 * becomes unreachable once the directory is gone:
 *   - `chapter-service.ts` → `remove`
 *   - `chapter-service.ts` → `removeForProjectDeletion`
 *   - `storage-service.ts` → `cleanupOrphanedWorktrees`
 *
 * Boundaries need the same treatment, and `invalidateBoundaries` already deletes its keys,
 * so one `dropStatus` call at each site covers both caches.
 */
export function dropStatus(rawPath: string): void {
	invalidateLocalGitStatusQueries(rawPath);
	invalidateBoundaries(rawPath);
	gitDiscoveryCache.invalidate(
		"local",
		rawPath,
		undefined,
		(a, b) => normalizeWorkspacePath(a) === normalizeWorkspacePath(b),
	);
	for (const key of cache.keys()) {
		if (localGitStatusScopesOverlap(key, rawPath)) cache.delete(key);
	}
}

/** Clear the entire cache. Used on shutdown / test teardown. */
export function clearStatusCache(): void {
	clearGitStatusQueries();
	cache.clear();
}

/** Entry count, for tests asserting the LRU bound. */
export function statusCacheSize(): number {
	return cache.size;
}

/** The cap itself, so a test cannot drift from the value it is checking. */
export const STATUS_CACHE_MAX_ENTRIES = MAX_ENTRIES;

logger.debug("git-status-cache initialized", { ttlMs: DEFAULT_TTL_MS, maxEntries: MAX_ENTRIES });
