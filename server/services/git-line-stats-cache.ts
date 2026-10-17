import type { GitLineStats } from "@shared/git-line-stats";
import { GitStatusQueryCoordinator, localGitStatusScopesOverlap } from "./git-status-query";

export const GIT_LINE_STATS_TTL_MS = 5000;
// Statistics have their own execution and result budget, but the same lifecycle.
const queries = new GitStatusQueryCoordinator<GitLineStats>(undefined, GIT_LINE_STATS_TTL_MS);

/** Authorized callers supply the device/generation/cwd key, never credentials. */
export function getGitLineStatsCached(
	key: string,
	load: (signal: AbortSignal) => Promise<GitLineStats>,
	signal?: AbortSignal,
	ttlMs?: number,
): Promise<GitLineStats> {
	return queries.query(key, load, { signal, ttlMs });
}

export const invalidateGitLineStats = queries.invalidate.bind(queries);
export const validateGitLineStatsCache = queries.validate.bind(queries);
export const clearGitLineStatsCache = queries.clear.bind(queries);

export function invalidateLocalGitLineStats(path: string): void {
	queries.invalidateMatching((key) => localGitStatusScopesOverlap(key, path));
}

export function dropLocalGitLineStats(path: string): void {
	queries.dropMatching((key) => localGitStatusScopesOverlap(key, path));
}
