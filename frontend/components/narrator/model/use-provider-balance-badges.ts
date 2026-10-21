import type { ProviderBalanceSnapshot } from "@shared/provider-balance";
import type { TokenDanceBalance } from "@shared/tokendance";
import { useQuery } from "@tanstack/react-query";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { useTokenDanceBalance } from "../../../hooks/useTokenDanceBalance";
import { api } from "../../../lib/api";
import type { DeepSeekBalanceCache } from "../../../lib/api/types";
import { formatLocaleNumber } from "../../../lib/intl-format";
import { formatProviderBalance } from "../../../lib/provider-balance-format";
import { formatDeepSeekBalance, isDeepSeekProviderBaseUrl } from "./deepseek-balance-format";
import { isKimiProviderBaseUrl } from "./kimi-usage-format";
import { isOpencodeProviderBaseUrl } from "./opencode-usage-format";
import { tokenDanceQuotaView } from "./tokendance-quota-format";

/** Pay-as-you-go balance shown in a model-picker provider header. */
export interface ProviderBalanceBadge {
	text: string;
	tooltip?: string;
	/** The last refresh failed or the value is old; drawn dimmed. */
	stale?: boolean;
}

export type ProviderBalanceBadges = Record<string, ProviderBalanceBadge>;

interface ProviderConfigLike {
	id: string;
	prefix?: string;
	baseUrl?: string;
	disabled?: boolean;
}

type Translate = (key: string, options?: Record<string, unknown>) => string;

export interface ProviderBalanceSources {
	customApiProviders: ProviderConfigLike[];
	nugProviders: ProviderConfigLike[];
	nugQuotas?: Record<string, { balance: number | null; detailedQuotaBalance?: string | null }>;
	customApiQuotas?: Record<
		string,
		{ quotaBalance: string | null; detailedQuotaBalance?: string | null }
	>;
	deepseekBalances?: Record<string, DeepSeekBalanceCache>;
	deepseekGenerations?: Record<string, unknown>;
	providerBalances?: Record<string, ProviderBalanceSnapshot>;
	providerBalanceGenerations?: Record<string, unknown>;
	tokenDance?: { generation: number; data: TokenDanceBalance | undefined };
	isAdmin: boolean;
	now: number;
	t: Translate;
	settingsT: Translate;
}

/**
 * Balance badges per provider prefix, from the same caches the status bar reads.
 * Pure for tests. Subscription allowances (Kimi, OpenCode Go) are drawn as bars
 * by their own badges and are skipped here; a value fetched for a replaced
 * account (generation mismatch) is never shown.
 */
export function buildProviderBalanceBadges(input: ProviderBalanceSources): ProviderBalanceBadges {
	const out: ProviderBalanceBadges = {};
	for (const provider of input.nugProviders) {
		if (provider.disabled || !provider.prefix) continue;
		const quota = input.nugQuotas?.[provider.id];
		if (quota?.balance == null || !Number.isFinite(quota.balance)) continue;
		out[provider.prefix] = {
			text: formatLocaleNumber(quota.balance, { maximumFractionDigits: 2 }),
			tooltip: quota.detailedQuotaBalance?.trim() || undefined,
		};
	}
	for (const provider of input.customApiProviders) {
		if (provider.disabled || !provider.prefix) continue;
		if (isKimiProviderBaseUrl(provider.baseUrl) || isOpencodeProviderBaseUrl(provider.baseUrl)) {
			continue;
		}
		if (isDeepSeekProviderBaseUrl(provider.baseUrl)) {
			const balance = input.deepseekBalances?.[provider.id];
			const generation = input.deepseekGenerations?.[provider.id];
			if (!balance || balance.generation !== generation || balance.fetchedAt === 0) continue;
			const failed = balance.error != null || balance.hasError === true;
			if (failed && balance.balanceInfos.length === 0) continue;
			// Show the last good numbers dimmed rather than an "unavailable" label.
			const view = formatDeepSeekBalance({ ...balance, error: null, hasError: false }, input.t);
			out[provider.prefix] = {
				text: view.quotaBalance,
				tooltip: view.detailedQuotaBalance,
				stale: failed,
			};
			continue;
		}
		const snapshot = input.providerBalances?.[provider.id];
		const generation = input.providerBalanceGenerations?.[provider.id];
		if (snapshot?.balance && snapshot.generation === generation) {
			const view = formatProviderBalance(snapshot, input.settingsT, false, input.now);
			out[provider.prefix] = {
				text: view.quotaBalance,
				tooltip: view.detailedQuotaBalance,
				stale: snapshot.error != null,
			};
			continue;
		}
		const quota = input.customApiQuotas?.[provider.id];
		const text = quota?.quotaBalance?.trim();
		if (text) {
			out[provider.prefix] = {
				text,
				tooltip: quota?.detailedQuotaBalance?.trim() || undefined,
			};
		}
	}
	if (input.tokenDance) {
		const { generation, data } = input.tokenDance;
		const view = tokenDanceQuotaView(
			data,
			generation,
			input.isAdmin,
			input.t as Parameters<typeof tokenDanceQuotaView>[3],
		);
		if (view && data?.generation === generation && data.balance != null) {
			out.tokendance = {
				text: view.balance,
				tooltip: view.detailsText,
				stale: data.hasError,
			};
		}
	}
	return out;
}

/**
 * Picker balance badges for every provider that reports a pay-as-you-go balance.
 * Fetched only while the picker is open; every source is a server-side cache
 * (stale-while-revalidate), so opening the menu never waits on an upstream.
 */
export function useProviderBalanceBadges(
	settingsData:
		| {
				customApiProviders?: unknown;
				nugProviders?: unknown;
				customApiQuotas?: unknown;
				deepseekBalanceGenerations?: unknown;
				providerBalanceGenerations?: unknown;
				tokendance?: unknown;
		  }
		| undefined,
	enabled: boolean,
	isAdmin: boolean,
): ProviderBalanceBadges | undefined {
	const { t } = useTranslation("narrator");
	const { t: settingsT } = useTranslation("settings");
	const customApiProviders = useMemo(
		() =>
			(Array.isArray(settingsData?.customApiProviders)
				? settingsData.customApiProviders
				: []) as ProviderConfigLike[],
		[settingsData?.customApiProviders],
	);
	const nugProviders = useMemo(
		() =>
			(Array.isArray(settingsData?.nugProviders)
				? settingsData.nugProviders
				: []) as ProviderConfigLike[],
		[settingsData?.nugProviders],
	);
	const deepseekGenerations = settingsData?.deepseekBalanceGenerations as
		| Record<string, unknown>
		| undefined;
	const providerBalanceGenerations = settingsData?.providerBalanceGenerations as
		| Record<string, unknown>
		| undefined;
	const hasDeepSeek = customApiProviders.some(
		(p) => !p.disabled && isDeepSeekProviderBaseUrl(p.baseUrl),
	);
	const hasProviderBalances = customApiProviders.some(
		(p) => !p.disabled && typeof providerBalanceGenerations?.[p.id] === "string",
	);
	const hasNug = nugProviders.some((p) => !p.disabled);

	const { data: nugQuotas } = useQuery({
		queryKey: ["nug", "quotas"],
		queryFn: api.nugGetQuotas,
		enabled: enabled && hasNug,
		staleTime: 30_000,
	});
	const { data: deepseekBalances } = useQuery({
		queryKey: ["deepseek", "balances", "picker"],
		queryFn: ({ signal }) => api.deepseekGetBalances({ signal }),
		enabled: enabled && hasDeepSeek,
		staleTime: 30_000,
	});
	const { data: providerBalances } = useQuery({
		queryKey: ["provider-balances", "picker"],
		queryFn: ({ signal }) => api.providerGetBalances({ signal }),
		enabled: enabled && hasProviderBalances,
		staleTime: 30_000,
		retry: false,
	});
	const tokenDanceConnection = settingsData?.tokendance as
		| { connected?: boolean; disabled?: boolean; generation?: number }
		| undefined;
	const tokenDanceGeneration =
		tokenDanceConnection?.connected &&
		!tokenDanceConnection.disabled &&
		Number.isSafeInteger(tokenDanceConnection.generation)
			? (tokenDanceConnection.generation as number)
			: undefined;
	const tokenDanceBalance = useTokenDanceBalance(tokenDanceGeneration, enabled);

	return useMemo(() => {
		const badges = buildProviderBalanceBadges({
			customApiProviders,
			nugProviders,
			nugQuotas,
			customApiQuotas: settingsData?.customApiQuotas as ProviderBalanceSources["customApiQuotas"],
			deepseekBalances,
			deepseekGenerations,
			providerBalances,
			providerBalanceGenerations,
			tokenDance:
				tokenDanceGeneration !== undefined
					? { generation: tokenDanceGeneration, data: tokenDanceBalance.data }
					: undefined,
			isAdmin,
			now: Date.now(),
			t,
			settingsT,
		});
		return Object.keys(badges).length > 0 ? badges : undefined;
	}, [
		customApiProviders,
		nugProviders,
		nugQuotas,
		settingsData?.customApiQuotas,
		deepseekBalances,
		deepseekGenerations,
		providerBalances,
		providerBalanceGenerations,
		tokenDanceGeneration,
		tokenDanceBalance.data,
		isAdmin,
		t,
		settingsT,
	]);
}
