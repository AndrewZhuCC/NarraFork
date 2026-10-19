import { Hono } from "hono";
import {
	type DeepSeekBalanceCache,
	getAllDeepSeekCachedBalances,
	refreshAllDeepSeekBalances,
	refreshStaleDeepSeekBalances,
} from "../lib/deepseek-balance-cache";
import { requireAdmin } from "../middleware/auth";

export const deepseekRoutes = new Hono();

export function redactBalancesForRole(
	balances: Record<string, DeepSeekBalanceCache>,
	isAdmin: boolean,
): Record<
	string,
	DeepSeekBalanceCache | (Omit<DeepSeekBalanceCache, "error"> & { hasError: boolean })
> {
	if (isAdmin) return balances;
	return Object.fromEntries(
		Object.entries(balances).map(([id, { error, ...cache }]) => [
			id,
			{ ...cache, hasError: error !== null },
		]),
	);
}

// Authentication is enforced by the /api middleware in app.ts.
// Return immediately; upstream work is coalesced and throttled by the cache.
deepseekRoutes.get("/balances", (c) => {
	void refreshStaleDeepSeekBalances().catch(() => {});
	return c.json(
		redactBalancesForRole(getAllDeepSeekCachedBalances(), c.get("user")?.role === "admin"),
	);
});

deepseekRoutes.post("/balances/refresh", requireAdmin, (c) => {
	void refreshAllDeepSeekBalances().catch(() => {});
	return c.json(getAllDeepSeekCachedBalances());
});
