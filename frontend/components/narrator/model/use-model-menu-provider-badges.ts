import { useMemo } from "react";
import { useCurrentUser } from "../../../hooks/useAuth";
import { useKimiProviderBadges } from "./use-kimi-provider-badges";
import { useOpencodeProviderBadges } from "./use-opencode-provider-badges";
import { useProviderBalanceBadges } from "./use-provider-balance-badges";

/**
 * Everything a model picker shows next to a provider group's name: subscription
 * allowance bars (OpenCode Go, Kimi) and pay-as-you-go balances. Queries run only
 * while `enabled` (the menu is open).
 */
export function useModelMenuProviderBadges(
	settingsData:
		| {
				customApiProviders?: unknown;
				opencodeUsageGenerations?: unknown;
				nugProviders?: unknown;
				customApiQuotas?: unknown;
				deepseekBalanceGenerations?: unknown;
				providerBalanceGenerations?: unknown;
				tokendance?: unknown;
		  }
		| undefined,
	enabled: boolean,
) {
	const { data: user } = useCurrentUser();
	const opencodeProviderBadges = useOpencodeProviderBadges(settingsData, enabled);
	const kimiProviderBadges = useKimiProviderBadges(settingsData, enabled);
	const providerBalances = useProviderBalanceBadges(settingsData, enabled, user?.role === "admin");
	const providerBadges = useMemo(
		() =>
			opencodeProviderBadges || kimiProviderBadges
				? { ...kimiProviderBadges, ...opencodeProviderBadges }
				: undefined,
		[opencodeProviderBadges, kimiProviderBadges],
	);
	return { providerBadges, providerBalances };
}
