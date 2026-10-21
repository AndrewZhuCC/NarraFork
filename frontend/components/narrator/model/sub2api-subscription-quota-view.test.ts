import { describe, expect, test } from "bun:test";
import i18next from "i18next";
import type {
	ProviderBalanceAmount,
	ProviderBalanceSnapshot,
} from "../../../../shared/provider-balance";
import { formatFullLocaleDateTime } from "../../../lib/format";
import en from "../../../locales/en/narrator.json";
import zh from "../../../locales/zh-CN/narrator.json";
import {
	buildSub2ApiSubscriptionQuotaView,
	isSub2ApiSubscriptionSnapshot,
} from "./sub2api-subscription-quota-view";

const NOW = Date.parse("2026-10-21T12:00:00Z");
const instance = i18next.createInstance();
await instance.init({
	lng: "en",
	resources: { en: { narrator: en }, "zh-CN": { narrator: zh } },
	defaultNS: "narrator",
	interpolation: { escapeValue: false },
});
const t = instance.getFixedT("en", "narrator");
function amount(window = "daily", used = 20, total = 100): ProviderBalanceAmount {
	return { unit: "USD", window, used, total, remaining: total - used };
}
function snapshot(amounts = [amount()]): ProviderBalanceSnapshot {
	return {
		adapter: "sub2api",
		status: "supported",
		generation: "g1",
		balance: { scope: "subscription", unlimited: false, amounts },
		fetchedAt: NOW,
		attemptedAt: NOW,
		error: null,
	};
}
function view(data = snapshot(), options: { now?: number; queryFailed?: boolean } = {}) {
	const result = buildSub2ApiSubscriptionQuotaView(data, t, "en", { now: NOW, ...options });
	if (!result) throw new Error("Expected subscription view");
	return result;
}

describe("sub2api subscription view", () => {
	test("orders USD windows and selects the tightest allowance", () => {
		const result = view(snapshot([amount("monthly", 20), amount("weekly", 90), amount()]));
		expect(result.windows.map((window) => window.id)).toEqual(["daily", "weekly", "monthly"]);
		expect(result.windows.map((window) => window.label)).toEqual([
			"Daily limit (USD)",
			"Weekly limit (USD)",
			"Monthly limit (USD)",
		]);
		expect(result.limiting?.id).toBe("weekly");
		expect(result.level).toBe("critical");
		expect(result.windows.every((window) => window.resetAt === null)).toBe(true);
	});

	test.each([
		[0, 100, "ok"],
		[74, 26, "ok"],
		[75, 25, "warning"],
		[90, 10, "critical"],
		[100, 0, "exhausted"],
		[120, 0, "exhausted"],
	] as const)("used %s yields %s percent remaining and %s", (used, remaining, level) => {
		const window = view(snapshot([amount("daily", used)])).windows[0];
		expect(window?.remainingPercent).toBe(remaining);
		expect(window?.usedPercent).toBe(Math.min(used, 100));
		expect(window?.level).toBe(level);
		expect(window?.used).toBe(used);
	});

	test("negative remaining is accepted and an exhausted period wins", () => {
		const result = view(snapshot([amount(), amount("weekly", 120), amount("monthly", 90)]));
		expect(result.windows[1]?.level).toBe("exhausted");
		expect(result.limiting?.id).toBe("weekly");
		expect(result.level).toBe("exhausted");
	});

	test.each([
		{ total: 0 },
		{ total: -1 },
		{ used: undefined },
		{ total: undefined },
		{ unit: "CNY" },
		{ unit: "TOKENS" },
		{ window: "1d" },
	])("invalid or unsupported amounts remain unknown: %j", (override) => {
		const result = view(snapshot([{ ...amount(), ...override }]));
		expect(result.windows).toEqual([]);
		expect(result.limiting).toBeNull();
		expect(result.level).toBe("unknown");
		expect(result.unavailableText).toBe("Subscription quota unknown");
		expect(result.failed).toBe(false);
	});

	test("minimum remaining USD wins even when the percentages disagree", () => {
		const result = view(snapshot([amount("weekly", 999, 1000), amount("daily", 0.8, 1)]));
		expect(result.windows.map((window) => window.remainingPercent)).toEqual([20, 0]);
		expect(result.limiting?.id).toBe("daily");
		expect(result.limiting?.level).toBe("warning");
		expect(result.windows[1]?.level).toBe("critical");
		expect(result.level).toBe("critical");
	});

	test.each([
		[999, "critical"],
		[995.1, "critical"],
		[999.99, "critical"],
		[1000, "exhausted"],
		[1001, "exhausted"],
	] as const)("USD allowance used %s of 1000 is %s despite rounded zero percent", (used, level) => {
		const result = view(snapshot([amount("weekly", used, 1000)]));
		expect(result.windows[0]?.remainingPercent).toBe(0);
		expect(result.windows[0]?.level).toBe(level);
		expect(result.level).toBe(level);
	});

	test("null remaining falls back to total minus used, ties follow daily/weekly/monthly order", () => {
		const result = view(
			snapshot([
				amount("monthly", 99, 100),
				amount("weekly", 9, 10),
				{ ...amount("daily", 0, 1), remaining: null },
			]),
		);
		expect(result.limiting?.id).toBe("daily");
		const exhausted = view(snapshot([amount("weekly", 200), amount("daily", 120)]));
		expect(exhausted.limiting?.id).toBe("daily");
	});

	test("only explicit unlimited suppresses bars, and expiry takes priority", () => {
		const data = snapshot([]);
		if (!data.balance) throw new Error("Missing balance");
		expect(view(data).unavailableText).toBe("Subscription quota unknown");
		data.balance.unlimited = true;
		expect(view(data).unavailableText).toBe("Subscription unlimited");
		data.balance.amounts = [amount()];
		expect(view(data).windows).toEqual([]);
		expect(view(data).limiting).toBeNull();
		data.balance.expiresAt = new Date(NOW).toISOString();
		expect(view(data).unavailableText).toBe("Subscription expired");
	});

	test("empty amounts stay clickable; unknown remaining can use verified usage", () => {
		expect(view(snapshot([])).unavailableText).toBe("Subscription quota unknown");
		expect(view(snapshot([{ ...amount(), remaining: null }])).windows[0]?.usedPercent).toBe(20);
	});

	test("metadata is separate from the generic heading and dates use the locale formatter", () => {
		const data = snapshot([{ ...amount("weekly"), windowStart: "2026-10-20T10:00:00Z" }]);
		if (!data.balance) throw new Error("Missing balance");
		data.balance.planName = "<img src=x onerror=alert(1)>";
		data.balance.expiresAt = "2026-11-01T00:00:00Z";
		const result = buildSub2ApiSubscriptionQuotaView(
			data,
			instance.getFixedT("zh-CN", "narrator"),
			"zh-CN",
			{ now: NOW },
		);
		expect(result?.title).toBe("sub2api 订阅额度");
		expect(result?.windows[0]?.shortLabel).toBe("周");
		expect(result?.rows).toEqual([
			{ label: "套餐名称", value: data.balance.planName },
			{
				label: "到期时间",
				value: formatFullLocaleDateTime(new Date(data.balance.expiresAt), "zh-CN"),
			},
			{
				label: "周窗口起点",
				value: formatFullLocaleDateTime(new Date("2026-10-20T10:00:00Z"), "zh-CN"),
			},
			{ label: "重置时间", value: "接口未提供重置时间。" },
		]);
		expect(result?.windows[0]?.resetAt).toBeNull();
	});

	test("invalid optional dates and blank names are omitted without losing valid numbers", () => {
		const data = snapshot([{ ...amount("weekly"), windowStart: "invalid" }]);
		if (!data.balance) throw new Error("Missing balance");
		Object.assign(data.balance, { planName: "  ", expiresAt: "invalid" });
		expect(view(data).rows).toEqual([
			{ label: "Reset time", value: "The API does not provide reset times." },
		]);
		expect(view(data).windows).toHaveLength(1);
	});

	test.each([
		NOW,
		NOW - 1,
	])("expired subscription (%s) is unavailable, not exhausted or failed", (expiry) => {
		const data = snapshot();
		if (!data.balance) throw new Error("Missing balance");
		data.balance.expiresAt = new Date(expiry).toISOString();
		const result = view(data);
		expect(result.windows).toEqual([]);
		expect(result.level).toBe("unknown");
		expect(result.unavailableText).toBe("Subscription expired");
		expect(result.rows.some((row) => row.value.includes("no longer available"))).toBe(true);
		expect(result.failed).toBe(false);
	});

	test("an unexpired subscription still shows actual allowances", () => {
		const data = snapshot();
		if (!data.balance) throw new Error("Missing balance");
		data.balance.expiresAt = new Date(NOW + 1).toISOString();
		expect(view(data).windows).toHaveLength(1);
	});

	test.each([
		"server",
		"query",
		"old",
		"missing",
	])("%s failure keeps cached numbers with only a fixed message", (failure) => {
		const data = snapshot();
		if (failure === "server") data.error = "request_failed";
		if (failure === "old") data.fetchedAt = NOW - 120_001;
		if (failure === "missing") data.fetchedAt = 0;
		const result = view(data, { queryFailed: failure === "query" });
		expect(result.failed).toBe(true);
		expect(result.windows[0]?.used).toBe(20);
		expect(result.failureDetail).toBe(
			failure === "server" || failure === "query" ? t("sub2apiSubscription.refreshFailed") : null,
		);
		if (result.failureDetail) expect(result.failureDetail).not.toContain("request_failed");
	});

	test("freshness boundary is exactly 120 seconds", () => {
		const data = snapshot();
		data.fetchedAt = NOW - 120_000;
		expect(view(data).failed).toBe(false);
		expect(view(data).failureDetail).toBeNull();
	});

	test("other modes, providers, missing data and probing statuses never take over", () => {
		const data = snapshot();
		if (!data.balance) throw new Error("Missing balance");
		const balance = data.balance;
		const candidates: Array<ProviderBalanceSnapshot | null | undefined> = [
			null,
			undefined,
			{ ...data, adapter: "new-api" },
			{ ...data, adapter: undefined },
			{ ...data, balance: null },
			...["wallet", "key"].map((scope) => ({
				...data,
				balance: { ...balance, scope: scope as "wallet" | "key" },
			})),
			...(["pending", "probing", "unsupported", "error"] as const).map((status) => ({
				...data,
				status,
			})),
		];
		for (const candidate of candidates) {
			expect(isSub2ApiSubscriptionSnapshot(candidate)).toBe(false);
			expect(buildSub2ApiSubscriptionQuotaView(candidate, t, "en")).toBeNull();
		}
		expect(isSub2ApiSubscriptionSnapshot({ ...data, error: "request_failed" })).toBe(true);
	});
});
