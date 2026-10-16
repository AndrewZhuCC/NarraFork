import type { GitLineStats } from "@shared/git-line-stats";
import { GitStatusQueryCoordinator, localGitStatusScopesOverlap } from "./git-status-query";

// Reuse the existing query lifecycle (bounded execution, independent cancellation,
// trailing refresh); only the result type differs from lightweight status.
const queries = new GitStatusQueryCoordinator<GitLineStats>();
const cache = new Map<string, { value?: GitLineStats; producedAt: number }>();
const TTL_MS = 800;
const MAX_ENTRIES = 128;

/** Authorized callers supply the device/generation/cwd key, never credentials. */
export async function getGitLineStatsCached(
	key: string,
	load: (signal: AbortSignal) => Promise<GitLineStats>,
	signal?: AbortSignal,
): Promise<GitLineStats> {
	signal?.throwIfAborted();
	let entry = cache.get(key);
	if (entry?.value && Date.now() - entry.producedAt < TTL_MS) {
		cache.delete(key);
		cache.set(key, entry);
		return entry.value;
	}
	if (!entry) {
		while (cache.size >= MAX_ENTRIES) {
			const oldest = cache.keys().next().value;
			if (oldest === undefined) break;
			cache.delete(oldest);
		}
		entry = { producedAt: 0 };
		cache.set(key, entry);
	}
	const current = entry;
	return queries.query(
		key,
		async (sharedSignal) => {
			try {
				const value = await load(sharedSignal);
				sharedSignal.throwIfAborted();
				// Invalidated/evicted reads may finish, but cannot republish stale numbers.
				if (cache.get(key) === current) {
					current.value = value;
					current.producedAt = Date.now();
				}
				return value;
			} catch (error) {
				if (cache.get(key) === current) cache.delete(key);
				throw error;
			}
		},
		{ signal },
	);
}

export function invalidateGitLineStats(key: string): void {
	queries.invalidate(key);
	cache.delete(key);
}

export function invalidateLocalGitLineStats(path: string): void {
	queries.invalidateMatching((key) => localGitStatusScopesOverlap(key, path));
	for (const key of cache.keys()) {
		if (localGitStatusScopesOverlap(key, path)) cache.delete(key);
	}
}

export function clearGitLineStatsCache(): void {
	queries.clear();
	cache.clear();
}
