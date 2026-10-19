import { describe, expect, test } from "bun:test";
import type { DeepSeekBalanceCache } from "../../../lib/api/types";
import { formatDeepSeekBalance, isDeepSeekProviderBaseUrl } from "./deepseek-balance-format";

const t = (key: string, options?: Record<string, unknown>) =>
	options ? `${key}:${options.currency} ${options.amount}` : key;
const balance: DeepSeekBalanceCache = {
	generation: "account-a",
	isAvailable: true,
	balanceInfos: [
		{ currency: "CNY", totalBalance: "110.00", grantedBalance: "10.00", toppedUpBalance: "100.00" },
	],
	fetchedAt: 1,
	error: null,
};

describe("DeepSeek provider detection", () => {
	test("accepts official root, versioned and Anthropic base URLs", () => {
		for (const path of ["", "/v1", "/anthropic/"]) {
			expect(isDeepSeekProviderBaseUrl(`https://api.deepseek.com${path}`)).toBe(true);
		}
	});
	test("rejects relays, suffix spoofs, insecure URLs and credentials", () => {
		for (const url of [
			undefined,
			"not a url",
			"http://api.deepseek.com",
			"https://deepseek.com",
			"https://relay.example/deepseek",
			"https://api.deepseek.com.evil.example",
			"https://api.deepseek.com:8443",
			"https://api.deepseek.com/relay",
			"https://api.deepseek.com?key=secret",
			"https://api.deepseek.com#relay",
			"https://user:secret@api.deepseek.com",
		]) {
			expect(isDeepSeekProviderBaseUrl(url)).toBe(false);
		}
	});
});

describe("DeepSeek balance display", () => {
	test("preserves decimal amounts and includes all balance types", () => {
		expect(formatDeepSeekBalance(balance, t)).toEqual({
			quotaBalance: "CNY 110.00",
			detailedQuotaBalance:
				"deepseek.available\ndeepseek.total:CNY 110.00\ndeepseek.granted:CNY 10.00\ndeepseek.toppedUp:CNY 100.00",
		});
	});
	test("shows multiple currencies independently", () => {
		const result = formatDeepSeekBalance(
			{
				...balance,
				balanceInfos: [
					...balance.balanceInfos,
					{
						currency: "USD",
						totalBalance: "0.123456",
						grantedBalance: "0",
						toppedUpBalance: "0.123456",
					},
				],
			},
			t,
		);
		expect(result.quotaBalance).toBe("CNY 110.00 / USD 0.123456");
		expect(result.detailedQuotaBalance).toContain("deepseek.total:USD 0.123456");
	});
	test("shows zero balances without treating them as missing", () => {
		expect(
			formatDeepSeekBalance(
				{
					...balance,
					balanceInfos: [
						{
							currency: "CNY",
							totalBalance: "0.00",
							grantedBalance: "0.00",
							toppedUpBalance: "0.00",
						},
					],
				},
				t,
			).quotaBalance,
		).toBe("CNY 0.00");
	});
	test("shows account unavailable while preserving details", () => {
		const result = formatDeepSeekBalance({ ...balance, isAvailable: false }, t);
		expect(result.quotaBalance).toBe("deepseek.accountUnavailable");
		expect(result.detailedQuotaBalance).toContain("deepseek.total:CNY 110.00");
	});
	test("failed refresh never presents old balances as current or leaks errors", () => {
		for (const entry of [
			{ ...balance, error: "secret" },
			{ ...balance, hasError: true },
		]) {
			expect(formatDeepSeekBalance(entry, t)).toEqual({
				quotaBalance: "deepseek.unavailable",
				detailedQuotaBalance: "deepseek.unavailable",
			});
		}
		expect(formatDeepSeekBalance(balance, t, true).quotaBalance).toBe("deepseek.unavailable");
	});
	test("distinguishes loading, transport failure and empty results", () => {
		expect(formatDeepSeekBalance(undefined, t).quotaBalance).toBe("deepseek.loading");
		expect(
			formatDeepSeekBalance({ ...balance, isAvailable: null, balanceInfos: [], fetchedAt: 0 }, t)
				.quotaBalance,
		).toBe("deepseek.loading");
		expect(formatDeepSeekBalance(undefined, t, true).quotaBalance).toBe("deepseek.unavailable");
		expect(
			formatDeepSeekBalance({ ...balance, isAvailable: null, balanceInfos: [] }, t).quotaBalance,
		).toBe("deepseek.unavailable");
	});
});
