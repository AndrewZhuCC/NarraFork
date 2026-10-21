import type { ProviderBalanceAmount, ProviderBalanceSnapshot } from "@shared/provider-balance";
import { formatLocaleNumber } from "./intl-format";

type Translate = (key: string, options?: Record<string, unknown>) => string;

function amountText(value: number | null | undefined, unit: string, t: Translate): string {
	if (value == null || !Number.isFinite(value)) return t("providerBalance.unknown");
	const label =
		unit === "QUOTA"
			? t("providerBalance.quotaUnit")
			: unit === "TOKENS"
				? t("providerBalance.tokens")
				: unit;
	return `${formatLocaleNumber(value, { maximumSignificantDigits: 12 })} ${label}`;
}

function remainingText(amount: ProviderBalanceAmount, t: Translate): string {
	const value = amountText(amount.remaining, amount.unit, t);
	return amount.window ? `${amount.window}: ${value}` : value;
}

export function formatProviderBalance(
	snapshot: ProviderBalanceSnapshot | undefined,
	t: Translate,
	queryFailed = false,
	now = Date.now(),
): { quotaBalance: string; detailedQuotaBalance: string } {
	const balance = snapshot?.balance;
	if (!balance) {
		const text = t(`providerBalance.${queryFailed ? "failed" : (snapshot?.status ?? "pending")}`);
		return { quotaBalance: text, detailedQuotaBalance: text };
	}
	const scope = t(`providerBalance.${balance.scope}`);
	const values = balance.unlimited
		? t("providerBalance.unlimited")
		: balance.amounts.map((amount) => remainingText(amount, t)).join(" / ") ||
			t("providerBalance.unknown");
	const failed = queryFailed || snapshot.error != null;
	const stale = failed || snapshot.fetchedAt === 0 || now - snapshot.fetchedAt > 120_000;
	const suffix = [
		failed ? t("providerBalance.failed") : "",
		stale ? t("providerBalance.stale") : "",
	].filter(Boolean);
	const summary = `${scope}: ${values}${suffix.length ? ` · ${suffix.join(" · ")}` : ""}`;
	const lines = [summary];
	if (snapshot.adapter) lines.push(t("providerBalance.source", { source: snapshot.adapter }));
	if (!balance.unlimited) {
		for (const amount of balance.amounts) {
			const details = [remainingText(amount, t)];
			if (amount.used != null)
				details.push(
					t("providerBalance.used", { amount: amountText(amount.used, amount.unit, t) }),
				);
			if (amount.total != null)
				details.push(
					t("providerBalance.total", { amount: amountText(amount.total, amount.unit, t) }),
				);
			lines.push(details.join(" · "));
		}
	}
	return { quotaBalance: summary, detailedQuotaBalance: lines.join("\n") };
}
