import { describe, expect, test } from "bun:test";
import type { ProviderBalanceSnapshot } from "../../../../shared/provider-balance";
import { customApiBalanceDisplayFor, usageSourceFor } from "./custom-api-usage-source";

const subscriptionSnapshot = {
	generation: "g1",
	status: "supported",
	adapter: "sub2api",
	balance: {
		scope: "subscription",
		unlimited: false,
		amounts: [{ unit: "USD", window: "daily", total: 10, used: 2, remaining: 8 }],
	},
	fetchedAt: 1,
	attemptedAt: 1,
	error: null,
} satisfies ProviderBalanceSnapshot;

describe("customApiBalanceDisplayFor", () => {
	test.each([
		"专用额度 75%",
		"0",
	])("pushed text %s and its details suppress the subscription panel", (quotaBalance) => {
		const pushed = {
			quotaBalance,
			detailedQuotaBalance: "站点套餐\n每日：USD 0.004\n重置：由站点提供",
		};
		const display = customApiBalanceDisplayFor({
			providerId: "p1",
			pushedQuotas: { p1: pushed },
			snapshot: subscriptionSnapshot,
		});
		expect(display.pushedQuota).toBe(pushed);
		expect(display.subscriptionSnapshot).toBeNull();
	});

	test.each([
		undefined,
		null,
		"",
		"   ",
	])("missing or empty pushed balance %s leaves the subscription fallback", (quotaBalance) => {
		const display = customApiBalanceDisplayFor({
			providerId: "p1",
			pushedQuotas: quotaBalance === undefined ? undefined : { p1: { quotaBalance } },
			snapshot: subscriptionSnapshot,
		});
		expect(display.pushedQuota).toBeNull();
		expect(display.subscriptionSnapshot).toBe(subscriptionSnapshot);
	});

	test("pushes for another provider never replace the active provider's subscription", () => {
		const display = customApiBalanceDisplayFor({
			providerId: "p2",
			pushedQuotas: { p1: { quotaBalance: "Old provider" } },
			snapshot: subscriptionSnapshot,
		});
		expect(display.pushedQuota).toBeNull();
		expect(display.subscriptionSnapshot).toBe(subscriptionSnapshot);
	});

	test("polling updates, stale snapshots and failed auto-detection cannot take back a pushed display", () => {
		const pushed = { quotaBalance: "Site plan", detailedQuotaBalance: "Site-specific details" };
		for (const snapshot of [
			subscriptionSnapshot,
			{ ...subscriptionSnapshot, fetchedAt: 2 },
			{ ...subscriptionSnapshot, error: "request_failed" as const },
			null,
		]) {
			const display = customApiBalanceDisplayFor({
				providerId: "p1",
				pushedQuotas: { p1: pushed },
				snapshot,
			});
			expect(display.pushedQuota).toBe(pushed);
			expect(display.subscriptionSnapshot).toBeNull();
		}
	});

	test("clearing the pushed display restores the subscription fallback", () => {
		const input = { providerId: "p1", snapshot: subscriptionSnapshot };
		expect(
			customApiBalanceDisplayFor({ ...input, pushedQuotas: { p1: { quotaBalance: "Site plan" } } })
				.subscriptionSnapshot,
		).toBeNull();
		expect(
			customApiBalanceDisplayFor({
				...input,
				pushedQuotas: { p1: { quotaBalance: null, detailedQuotaBalance: null } },
			}).subscriptionSnapshot,
		).toBe(subscriptionSnapshot);
	});
});

describe("usageSourceFor", () => {
	test("picks the source the provider is currently configured for", () => {
		expect(
			usageSourceFor({ providerId: "p1", opencodeProviderId: "p1", kimiProviderId: null }),
		).toBe("opencode");
		expect(
			usageSourceFor({ providerId: "p1", opencodeProviderId: null, kimiProviderId: "p1" }),
		).toBe("kimi");
		expect(
			usageSourceFor({ providerId: "p1", opencodeProviderId: null, kimiProviderId: null }),
		).toBeNull();
	});

	test("a provider edited from Kimi to Go gets no Kimi fallback", () => {
		// Regression: React Query keeps a disabled query's last data, so after the id stops
		// being a Kimi provider the Kimi cache still holds its old entry and never refetches.
		// Before the guard, that stale entry was read under the Go configuration and the
		// previous account's allowance was displayed.
		expect(
			usageSourceFor({
				providerId: "same-id",
				opencodeProviderId: "same-id",
				kimiProviderId: null,
			}),
		).toBe("opencode");
	});

	test("the reverse edit does not surface a stale Go allowance either", () => {
		expect(
			usageSourceFor({
				providerId: "same-id",
				opencodeProviderId: null,
				kimiProviderId: "same-id",
			}),
		).toBe("kimi");
	});

	test("opencode wins when both ids somehow match", () => {
		// Not reachable through settings (a provider has one baseUrl), but the precedence is
		// stated so the branch order in the panel is not load-bearing by accident.
		expect(
			usageSourceFor({ providerId: "p1", opencodeProviderId: "p1", kimiProviderId: "p1" }),
		).toBe("opencode");
	});
});
