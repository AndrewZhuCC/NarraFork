import { useQuery } from "@tanstack/react-query";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { api } from "../../../lib/api";
import type { OpencodeUsageCache } from "../../../lib/api/types";
import { isOpencodeProviderBaseUrl } from "./opencode-usage-format";
import {
	buildOpencodeQuotaView,
	formatOpencodeBadgeText,
	formatOpencodeSummaryLines,
	OPENCODE_LEVEL_COLORS,
	opencodeWindowLabel,
} from "./opencode-usage-view";
import type { QuotaBarSpec } from "./QuotaWindowBars";

interface ProviderConfigLike {
	id: string;
	prefix?: string;
	baseUrl?: string;
	disabled?: boolean;
}

/**
 * Model-picker group badge. With `bars`, the header draws one vertical bar per
 * limit window (same glyph as the status bar); `text` stays the accessible label.
 */
export interface ProviderQuotaBadge {
	text: string;
	color: string;
	tooltip?: string;
	bars?: QuotaBarSpec[];
	/** The last refresh failed; bars are drawn dimmed. */
	stale?: boolean;
}

export type OpencodeProviderBadges = Record<string, ProviderQuotaBadge>;

/**
 * Build model-picker group badges for every enabled OpenCode Go provider.
 *
 * Pure so the generation check is testable: an entry whose generation no
 * longer matches the one settings handed us belongs to a replaced account and
 * is skipped, same rule as `useOpencodeUsage`.
 */
export function buildOpencodeProviderBadges(input: {
	providers: ProviderConfigLike[];
	usages: Record<string, OpencodeUsageCache> | undefined;
	generations: Record<string, unknown> | undefined;
	now: number;
	t: (key: string, options?: Record<string, unknown>) => string;
}): OpencodeProviderBadges {
	const out: OpencodeProviderBadges = {};
	for (const provider of input.providers) {
		if (provider.disabled || !provider.prefix || !isOpencodeProviderBaseUrl(provider.baseUrl)) {
			continue;
		}
		const usage = input.usages?.[provider.id];
		const generation = input.generations?.[provider.id];
		if (!usage || typeof generation !== "string" || usage.generation !== generation) continue;
		const view = buildOpencodeQuotaView(usage, input.now);
		if (!view) continue;
		out[provider.prefix] = {
			text: formatOpencodeBadgeText(view, input.t),
			color: view.failed && view.level === "unknown" ? "gray" : OPENCODE_LEVEL_COLORS[view.level],
			tooltip: formatOpencodeSummaryLines(view, input.now, input.t).join("\n"),
			bars: view.windows.map((window) => ({
				id: window.key,
				label: opencodeWindowLabel(window.key, input.t),
				remainingPercent: window.remainingPercent,
				level: window.level,
			})),
			stale: view.failed,
		};
	}
	return out;
}

/**
 * Picker badges, fetched only while the picker is open and an OpenCode Go
 * provider exists. Shares the `/api/opencode/usages` server cache (and its
 * stale-while-revalidate throttle), so opening the menu never forces an
 * upstream `/usage` call of its own.
 */
export function useOpencodeProviderBadges(
	settingsData: { customApiProviders?: unknown; opencodeUsageGenerations?: unknown } | undefined,
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
	const hasOpencode = providers.some((p) => !p.disabled && isOpencodeProviderBaseUrl(p.baseUrl));
	const { data } = useQuery({
		queryKey: ["opencode", "usages", "picker"],
		queryFn: ({ signal }) => api.opencodeGetUsages({ signal }),
		enabled: enabled && hasOpencode,
		staleTime: 30_000,
	});
	return useMemo(() => {
		if (!hasOpencode || !data) return undefined;
		const badges = buildOpencodeProviderBadges({
			providers,
			usages: data,
			generations: settingsData?.opencodeUsageGenerations as Record<string, unknown> | undefined,
			now: Date.now(),
			t,
		});
		return Object.keys(badges).length > 0 ? badges : undefined;
	}, [hasOpencode, data, providers, settingsData?.opencodeUsageGenerations, t]);
}
