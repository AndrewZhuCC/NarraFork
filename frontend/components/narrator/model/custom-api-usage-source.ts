/**
 * Which cached usage source may speak for the provider the panel is currently showing.
 *
 * Both sources are keyed by provider id, and each is guarded by the provider id it is
 * CURRENTLY configured for rather than by "does this id appear in that source's cache".
 * That distinction is the whole point: React Query keeps the last `data` of a query that
 * has been disabled, so after an admin edits a provider from a Kimi endpoint to an
 * OpenCode Go one, the Kimi cache still holds an entry for that same id and its query is
 * disabled — it will never refetch to drop it. Reading that entry unguarded displayed the
 * previous account's allowance under the new one, which is exactly what the per-provider
 * isolation is supposed to prevent.
 *
 * Extracted from `NarratorPanel` so the precedence is testable without mounting the panel
 * (which carries queries, a WS subscription and scroll containers).
 */
import type { ProviderBalanceSnapshot } from "../../../../shared/provider-balance";
import { isSub2ApiSubscriptionSnapshot } from "./sub2api-subscription-quota-view";

export function usageSourceFor(input: {
	providerId: string;
	opencodeProviderId: string | null;
	kimiProviderId: string | null;
}): "opencode" | "kimi" | null {
	if (input.providerId === input.opencodeProviderId) return "opencode";
	if (input.providerId === input.kimiProviderId) return "kimi";
	return null;
}

/** Stream-provided text wins over auto-detected balances, including after remount. */
export function customApiBalanceDisplayFor(input: {
	providerId: string | null;
	pushedQuotas?: Record<
		string,
		{ quotaBalance: string | null; detailedQuotaBalance?: string | null }
	>;
	snapshot: ProviderBalanceSnapshot | null | undefined;
}) {
	const cached = input.providerId ? input.pushedQuotas?.[input.providerId] : undefined;
	const pushedQuota = cached?.quotaBalance?.trim() ? cached : null;
	return {
		pushedQuota,
		// The query already isolates provider/generation; this layer only selects the source.
		subscriptionSnapshot:
			!pushedQuota && isSub2ApiSubscriptionSnapshot(input.snapshot) ? input.snapshot : null,
	};
}
