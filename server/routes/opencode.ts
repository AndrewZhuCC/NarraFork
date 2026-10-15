import { Hono } from "hono";
import {
	getAllOpencodeCachedUsages,
	type OpencodeUsageCache,
	refreshAllOpencodeUsages,
	refreshStaleOpencodeUsages,
} from "../lib/opencode-usage-cache";
import { requireAdmin } from "../middleware/auth";

/**
 * OpenCode Go subscription usage endpoints.
 *
 * GET returns the per-provider cache immediately and kicks off a background
 * refresh for entries older than the staleness window (stale-while-revalidate),
 * so the status bar never blocks on the upstream API — same shape as
 * `/api/kimi/usages`, which is also readable by every signed-in user because the
 * numbers describe the deployment's own plan allowance and are what the panel
 * has to render for whoever is using a Go model. Reads cannot amplify upstream
 * traffic: the background refresh is throttled per provider per staleness window,
 * so N readers polling at any rate produce at most one upstream call.
 *
 * The `error` field is ADMIN-ONLY within that response; see
 * {@link redactUsagesForRole}.
 *
 * POST (force refresh) stays admin-only: it bypasses the staleness throttle and
 * reaches out to opencode.ai on demand.
 */

export const opencodeRoutes = new Hono();

/**
 * Strip the upstream error text for non-admins, keeping the usage numbers.
 *
 * The cache stores a fixed message rather than the caught exception, but that
 * message still describes the deployment's provider account rather than the
 * requesting user's allowance, and the route is open to every signed-in user.
 * `hasError` replaces it rather than the field simply vanishing: the status bar
 * must show "usage unavailable" instead of presenting the last cached
 * percentages as if they were current. An admin gets the text, because they are
 * the one who can act on it.
 */
export function redactUsagesForRole(
	usages: Record<string, OpencodeUsageCache>,
	isAdmin: boolean,
): Record<
	string,
	OpencodeUsageCache | (Omit<OpencodeUsageCache, "error"> & { hasError: boolean })
> {
	if (isAdmin) return usages;
	const out: Record<string, Omit<OpencodeUsageCache, "error"> & { hasError: boolean }> = {};
	for (const [providerId, cache] of Object.entries(usages)) {
		const { error, ...rest } = cache;
		out[providerId] = { ...rest, hasError: error !== null };
	}
	return out;
}

// Authentication is enforced by the /api middleware in app.ts.
opencodeRoutes.get("/usages", (c) => {
	void refreshStaleOpencodeUsages().catch(() => {});
	const isAdmin = c.get("user")?.role === "admin";
	return c.json(redactUsagesForRole(getAllOpencodeCachedUsages(), isAdmin));
});

opencodeRoutes.post("/usages/refresh", requireAdmin, (c) => {
	// Deliberately NOT awaited: the upstream call carries a 15s timeout, and
	// awaiting it here holds the HTTP request open for that long on a slow or
	// unreachable upstream. The client invalidates its query and re-reads.
	void refreshAllOpencodeUsages().catch(() => {});
	return c.json(getAllOpencodeCachedUsages());
});
