import { useQuery } from "@tanstack/react-query";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { api } from "../../../lib/api";
import type { KimiUsageCache } from "../../../lib/api/types";
import { buildKimiQuotaView, isKimiProviderBaseUrl } from "./kimi-usage-format";
import {
	formatQuotaBadgeText,
	formatQuotaSummaryLines,
	QUOTA_LEVEL_COLORS,
	type QuotaPanelT,
} from "./quota-panel-view";
import type { OpencodeProviderBadges } from "./use-opencode-provider-badges";

interface ProviderConfigLike {
	id: string;
	prefix?: string;
	baseUrl?: string;
	disabled?: boolean;
}

/**
 * Model-picker group badges (one bar per Kimi limit window) for every enabled
 * provider currently pointed at a Kimi endpoint. Pure for tests; an entry for a
 * provider re-pointed elsewhere is skipped, same rule as the status bar.
 */
export function buildKimiProviderBadges(input: {
	providers: ProviderConfigLike[];
	usages: Record<string, KimiUsageCache> | undefined;
	now: number;
	t: QuotaPanelT;
}): OpencodeProviderBadges {
	const out: OpencodeProviderBadges = {};
	for (const provider of input.providers) {
		if (provider.disabled || !provider.prefix || !isKimiProviderBaseUrl(provider.baseUrl)) {
			continue;
		}
		const view = buildKimiQuotaView(input.usages?.[provider.id], input.t);
		if (!view) continue;
		out[provider.prefix] = {
			text: formatQuotaBadgeText(view, input.t),
			color: view.failed && view.level === "unknown" ? "gray" : QUOTA_LEVEL_COLORS[view.level],
			tooltip: formatQuotaSummaryLines(view, input.now, input.t).join("\n"),
			bars: view.windows,
			stale: view.failed,
		};
	}
	return out;
}

/**
 * Kimi picker badges, fetched only while the picker is open and a Kimi provider
 * exists. Shares the `["kimi", "usages"]` query (and the server's
 * stale-while-revalidate cache) with the status bar.
 */
export function useKimiProviderBadges(
	settingsData: { customApiProviders?: unknown } | undefined,
	enabled: boolean,
): OpencodeProviderBadges | undefined {
	const { t } = useTranslation("narrator");
	const providers = useMemo(
		() =>
			(Array.isArray(settingsData?.customApiProviders)
				? settingsData.customApiProviders
				: []) as ProviderConfigLike[],
		[settingsData?.customApiProviders],
	);
	const hasKimi = providers.some((p) => !p.disabled && isKimiProviderBaseUrl(p.baseUrl));
	const { data } = useQuery({
		queryKey: ["kimi", "usages"],
		queryFn: api.kimiGetUsages,
		enabled: enabled && hasKimi,
		staleTime: 30_000,
	});
	return useMemo(() => {
		if (!hasKimi || !data) return undefined;
		const badges = buildKimiProviderBadges({ providers, usages: data, now: Date.now(), t });
		return Object.keys(badges).length > 0 ? badges : undefined;
	}, [hasKimi, data, providers, t]);
}
