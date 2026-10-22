import type { ProviderBalanceSnapshot } from "../../../../shared/provider-balance";
import { formatFullLocaleDateTime } from "../../../lib/format";
import {
	parseQuotaReset,
	type QuotaPanelT,
	type QuotaPanelView,
	quotaLevelForRemaining,
	quotaWindowFromAmounts,
	worstQuotaLevel,
} from "./quota-panel-view";

/** A retained balance must not take over the UI during a manual reprobe. */
export function isSub2ApiSubscriptionSnapshot(
	snapshot: ProviderBalanceSnapshot | null | undefined,
): snapshot is ProviderBalanceSnapshot & {
	balance: NonNullable<ProviderBalanceSnapshot["balance"]>;
} {
	return (
		snapshot?.status === "supported" &&
		snapshot.adapter === "sub2api" &&
		snapshot.balance?.scope === "subscription"
	);
}

/** Pure subscription-only projection; never infers reset times from a window start. */
export function buildSub2ApiSubscriptionQuotaView(
	snapshot: ProviderBalanceSnapshot | null | undefined,
	t: QuotaPanelT,
	locale: string,
	options: { queryFailed?: boolean; now?: number } = {},
): QuotaPanelView | null {
	if (!isSub2ApiSubscriptionSnapshot(snapshot)) return null;
	const now = options.now ?? Date.now();
	const { balance } = snapshot;
	const rows: QuotaPanelView["rows"] = [];
	if (balance.planName?.trim()) {
		rows.push({ label: t("sub2apiSubscription.planName"), value: balance.planName });
	}
	const expiresAt = parseQuotaReset(balance.expiresAt);
	if (expiresAt != null) {
		rows.push({
			label: t("sub2apiSubscription.expiresAt"),
			value: formatFullLocaleDateTime(new Date(expiresAt), locale),
		});
	}
	const expired = expiresAt != null && expiresAt <= now;
	if (expired) {
		rows.push({
			label: t("sub2apiSubscription.status"),
			value: t("sub2apiSubscription.expiredDetail"),
		});
	}
	const weeklyStart = parseQuotaReset(
		balance.amounts.find((amount) => amount.unit === "USD" && amount.window === "weekly")
			?.windowStart,
	);
	if (weeklyStart != null) {
		rows.push({
			label: t("sub2apiSubscription.weeklyWindowStart"),
			value: formatFullLocaleDateTime(new Date(weeklyStart), locale),
		});
	}
	rows.push({
		label: t("sub2apiSubscription.resetTime"),
		value: t("sub2apiSubscription.resetUnavailable"),
	});
	let limiting: QuotaPanelView["limiting"] = null;
	let leastRemaining = Number.POSITIVE_INFINITY;
	const windows =
		expired || balance.unlimited
			? []
			: (["daily", "weekly", "monthly"] as const).flatMap((id) => {
					const amount = balance.amounts.find(
						(entry) => entry.unit === "USD" && entry.window === id,
					);
					if (!amount || amount.total == null || amount.total <= 0 || amount.used == null) {
						return [];
					}
					const window = quotaWindowFromAmounts({
						id,
						label: t(`sub2apiSubscription.${id}`),
						shortLabel: t(`sub2apiSubscription.short${id}`),
						used: amount.used,
						limit: amount.total,
						remaining: amount.remaining,
						resetAt: null,
					});
					const remaining = Math.max(0, amount.remaining ?? amount.total - amount.used);
					// Rounded badge percentages must not mark a positive USD allowance exhausted.
					window.level = quotaLevelForRemaining((remaining / amount.total) * 100, remaining <= 0);
					if (remaining < leastRemaining) {
						limiting = window;
						leastRemaining = remaining;
					}
					return [window];
				});
	const refreshFailed = snapshot.error != null || options.queryFailed === true;
	const failed = refreshFailed || snapshot.fetchedAt <= 0 || now - snapshot.fetchedAt > 120_000;
	return {
		title: t("sub2apiSubscription.title"),
		unavailableText: t(
			expired
				? "sub2apiSubscription.expired"
				: balance.unlimited
					? "sub2apiSubscription.unlimited"
					: "sub2apiSubscription.unknown",
		),
		windows,
		limiting,
		level: worstQuotaLevel(windows.map((window) => window.level)),
		rows,
		failed,
		failureDetail: refreshFailed ? t("sub2apiSubscription.refreshFailed") : null,
		fetchedAt: snapshot.fetchedAt,
	};
}
