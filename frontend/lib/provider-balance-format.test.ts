import { expect, test } from "bun:test";
import type { ProviderBalanceSnapshot } from "@shared/provider-balance";
import { createInstance } from "i18next";
import en from "../locales/en/settings.json";
import zh from "../locales/zh-CN/settings.json";
import { formatProviderBalance } from "./provider-balance-format";

const snapshot = (): ProviderBalanceSnapshot => ({
	generation: "A",
	status: "supported",
	adapter: "new-api",
	balance: {
		scope: "key",
		unlimited: false,
		amounts: [{ unit: "QUOTA", remaining: 0, used: 2, total: 2, window: "5h" }],
	},
	fetchedAt: 1000,
	attemptedAt: 1000,
	error: null,
});
for (const [lng, resource] of [
	["en", en],
	["zh-CN", zh],
] as const) {
	const i18n = createInstance();
	await i18n.init({
		lng,
		resources: { [lng]: { settings: resource } },
		defaultNS: "settings",
		interpolation: { escapeValue: false },
	});
	const t = (key: string, options?: Record<string, unknown>) => String(i18n.t(key, options));
	test(`${lng}: zero quota preserves scope, window and real unit with used/total details`, () => {
		const result = formatProviderBalance(snapshot(), t, false, 1000);
		expect(result.quotaBalance).toContain(t("providerBalance.key"));
		expect(result.quotaBalance).toContain(`5h: 0 ${t("providerBalance.quotaUnit")}`);
		expect(result.quotaBalance).not.toContain("USD");
		expect(result.detailedQuotaBalance).toContain(
			t("providerBalance.used", { amount: `2 ${t("providerBalance.quotaUnit")}` }),
		);
		expect(result.detailedQuotaBalance).toContain(
			t("providerBalance.total", { amount: `2 ${t("providerBalance.quotaUnit")}` }),
		);
	});
	test(`${lng}: unlimited never displays placeholder numeric balance`, () => {
		const value = snapshot();
		if (!value.balance) throw new Error("Expected balance fixture");
		value.balance.unlimited = true;
		const result = formatProviderBalance(value, t, false, 1000);
		expect(result.quotaBalance).toContain(t("providerBalance.unlimited"));
		expect(result.detailedQuotaBalance).not.toContain("5h: 0");
		expect(result.detailedQuotaBalance).not.toContain(t("providerBalance.used", { amount: "2" }));
	});
	test(`${lng}: unknown, wallet, subscription and tokens do not become zero or dollars`, () => {
		const value = snapshot();
		if (!value.balance) throw new Error("Expected balance fixture");
		value.balance.scope = "subscription";
		value.balance.amounts = [{ unit: "TOKENS", remaining: null }];
		const result = formatProviderBalance(value, t, false, 1000);
		expect(result.quotaBalance).toContain(t("providerBalance.subscription"));
		expect(result.quotaBalance).toContain(t("providerBalance.unknown"));
		value.balance.amounts[0].remaining = -1;
		expect(formatProviderBalance(value, t, false, 1000).quotaBalance).toContain(
			`-1 ${t("providerBalance.tokens")}`,
		);
		value.balance.scope = "wallet";
		value.balance.amounts = [{ unit: "CNY", remaining: 1.25 }];
		expect(formatProviderBalance(value, t, false, 1000).quotaBalance).toContain(
			`${t("providerBalance.wallet")}: 1.25 CNY`,
		);
	});
	test(`${lng}: tiny nonzero balances never round to zero`, () => {
		const value = snapshot();
		if (!value.balance) throw new Error("Expected balance fixture");
		value.balance.amounts = [{ unit: "USD", remaining: 1e-8 }];
		expect(formatProviderBalance(value, t, false, 1000).quotaBalance).toContain("0.00000001 USD");
		value.balance.amounts[0].remaining = -1e-8;
		expect(formatProviderBalance(value, t, false, 1000).quotaBalance).toContain("-0.00000001 USD");
	});
	test(`${lng}: failure retains the last amount and marks it outdated`, () => {
		const value = snapshot();
		if (!value.balance) throw new Error("Expected balance fixture");
		value.error = "request_failed";
		const result = formatProviderBalance(value, t, false, 1000);
		expect(result.quotaBalance).toContain("5h: 0");
		expect(result.quotaBalance).toContain(t("providerBalance.failed"));
		expect(result.quotaBalance).toContain(t("providerBalance.stale"));
		expect(formatProviderBalance(snapshot(), t, false, 122_000).quotaBalance).toContain(
			t("providerBalance.stale"),
		);
	});
	test(`${lng}: pending/unsupported and missing balances never show fake zero`, () => {
		for (const status of ["pending", "probing", "unsupported", "error"] as const) {
			const value = snapshot();
			if (!value.balance) throw new Error("Expected balance fixture");
			value.status = status;
			value.balance = null;
			expect(formatProviderBalance(value, t).quotaBalance).toBe(t(`providerBalance.${status}`));
		}
	});
}
