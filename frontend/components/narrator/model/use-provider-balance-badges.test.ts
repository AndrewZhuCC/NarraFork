import { describe, expect, test } from "bun:test";
import { buildProviderRefreshTargets } from "./use-model-menu-refresh-props";
import {
	buildProviderBalanceBadges,
	type ProviderBalanceSources,
} from "./use-provider-balance-badges";

const t = (key: string, options?: Record<string, unknown>) =>
	options ? `${key}:${JSON.stringify(options)}` : key;

function sources(overrides: Partial<ProviderBalanceSources>): ProviderBalanceSources {
	return {
		customApiProviders: [],
		nugProviders: [],
		isAdmin: false,
		now: 1_000_000,
		t,
		settingsT: t,
		...overrides,
	};
}

describe("buildProviderBalanceBadges", () => {
	test("NUG balances and generic custom API quotas are keyed by prefix", () => {
		const badges = buildProviderBalanceBadges(
			sources({
				nugProviders: [
					{ id: "n1", prefix: "nug2" },
					{ id: "n2", prefix: "off", disabled: true },
				],
				nugQuotas: { n1: { balance: 1559.75 }, n2: { balance: 3 } },
				customApiProviders: [{ id: "c1", prefix: "relay", baseUrl: "https://relay.example/v1" }],
				customApiQuotas: { c1: { quotaBalance: "$4.20", detailedQuotaBalance: "details" } },
			}),
		);
		expect(Object.keys(badges).sort()).toEqual(["nug2", "relay"]);
		expect(badges.relay).toEqual({ text: "$4.20", tooltip: "details" });
	});

	test("subscription endpoints are left to their allowance bars", () => {
		const badges = buildProviderBalanceBadges(
			sources({
				customApiProviders: [
					{ id: "k", prefix: "kimi", baseUrl: "https://api.kimi.com/coding/v1" },
					{ id: "o", prefix: "go", baseUrl: "https://opencode.ai/zen/go/v1" },
				],
				customApiQuotas: { k: { quotaBalance: "x" }, o: { quotaBalance: "y" } },
			}),
		);
		expect(badges).toEqual({});
	});

	test("DeepSeek balances from a replaced account (generation mismatch) are hidden", () => {
		const provider = { id: "d", prefix: "deepseek", baseUrl: "https://api.deepseek.com" };
		const balance = {
			generation: "g1",
			isAvailable: true,
			balanceInfos: [
				{ currency: "CNY", totalBalance: "10.00", grantedBalance: "0", toppedUpBalance: "10.00" },
			],
			fetchedAt: 1,
			error: null,
		};
		const shown = buildProviderBalanceBadges(
			sources({
				customApiProviders: [provider],
				deepseekBalances: { d: balance },
				deepseekGenerations: { d: "g1" },
			}),
		);
		expect(shown.deepseek?.text).toBe("CNY 10.00");
		const hidden = buildProviderBalanceBadges(
			sources({
				customApiProviders: [provider],
				deepseekBalances: { d: balance },
				deepseekGenerations: { d: "g2" },
			}),
		);
		expect(hidden).toEqual({});
	});
});

describe("buildProviderRefreshTargets", () => {
	test("maps every protocol's prefix to its refresh endpoint", () => {
		const settings = {
			openaiProviders: [{ id: "o", prefix: "oa" }],
			anthropicProviders: [{ id: "a", prefix: "an" }],
			geminiProviders: [{ id: "g", prefix: "ge", disabled: true }],
			nugProviders: [{ id: "n", prefix: "nug" }],
		};
		expect(buildProviderRefreshTargets(settings, { nug: true, admin: false })).toEqual({
			oa: { kind: "openai", id: "o" },
			an: { kind: "anthropic", id: "a" },
			nug: { kind: "nug", id: "n" },
		});
		const admin = buildProviderRefreshTargets(settings, { nug: false, admin: true });
		expect(admin.nug).toBeUndefined();
		expect(admin.CommandCode).toEqual({ kind: "commandcode" });
		expect(admin.tokendance).toEqual({ kind: "tokendance" });
	});
});
