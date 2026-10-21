import { describe, expect, test } from "bun:test";
import type { KimiUsageCache } from "../../../lib/api/types";
import { buildKimiQuotaView, isKimiProviderBaseUrl, type KimiTFunction } from "./kimi-usage-format";

// Identity-style t: returns the key with interpolated params for assertions.
const t: KimiTFunction = (key, options) => {
	if (!options) return key;
	const params = Object.entries(options)
		.map(([k, v]) => `${k}=${String(v)}`)
		.join(",");
	return `${key}[${params}]`;
};

const BASE: KimiUsageCache = {
	fiveHour: { used: 71, limit: 100, remaining: 29, resetTime: "2026-08-21T13:35:46.841Z" },
	weekly: { used: 34, limit: 100, remaining: 66, resetTime: "2026-08-27T17:35:46.841Z" },
	monthly: null,
	extraWindows: [],
	fetchedAt: 1787308913388,
	error: null,
};

describe("buildKimiQuotaView", () => {
	test("maps each reported window to a progress bar, limited by the least left", () => {
		const view = buildKimiQuotaView(BASE, t);
		expect(view?.title).toBe("kimi.title");
		expect(view?.windows.map((w) => [w.id, w.usedPercent, w.remainingPercent])).toEqual([
			["fiveHour", 71, 29],
			["weekly", 34, 66],
		]);
		expect(view?.windows[0]?.used).toBe(71);
		expect(view?.windows[0]?.limit).toBe(100);
		expect(view?.windows[0]?.resetAt).toBe(Date.parse("2026-08-21T13:35:46.841Z"));
		expect(view?.limiting?.id).toBe("fiveHour");
		expect(view?.level).toBe("ok");
		expect(view?.failed).toBe(false);
	});

	test("derives used from remaining when upstream omits it", () => {
		const usage: KimiUsageCache = {
			...BASE,
			fiveHour: { used: null, limit: 100, remaining: 8, resetTime: null },
		};
		const window = buildKimiQuotaView(usage, t)?.windows[0];
		expect(window?.remainingPercent).toBe(8);
		expect(window?.level).toBe("critical");
	});

	test("a window with no usable limit has no percentage", () => {
		const usage: KimiUsageCache = {
			...BASE,
			fiveHour: { used: 5, limit: null, remaining: null, resetTime: "not-a-date" },
			weekly: null,
		};
		const window = buildKimiQuotaView(usage, t)?.windows[0];
		expect(window?.usedPercent).toBeNull();
		expect(window?.level).toBe("unknown");
		expect(window?.resetAt).toBeNull();
	});

	test("spent window is exhausted and bounds the account", () => {
		const usage: KimiUsageCache = {
			...BASE,
			weekly: { used: 100, limit: 100, remaining: 0, resetTime: null },
		};
		const view = buildKimiQuotaView(usage, t);
		expect(view?.limiting?.id).toBe("weekly");
		expect(view?.level).toBe("exhausted");
	});

	test("extra windows keep their upstream label", () => {
		const usage: KimiUsageCache = {
			...BASE,
			extraWindows: [{ label: "Daily", used: 1, limit: 4, remaining: 3, resetTime: null }],
		};
		const extra = buildKimiQuotaView(usage, t)?.windows.at(-1);
		expect(extra?.label).toBe("Daily");
		expect(extra?.remainingPercent).toBe(75);
	});

	test("a failed refresh is flagged; only admins get the reason", () => {
		const nonAdmin = buildKimiQuotaView(
			{ ...BASE, fiveHour: null, weekly: null, error: undefined, hasError: true },
			t,
		);
		expect(nonAdmin?.failed).toBe(true);
		expect(nonAdmin?.failureDetail).toBeNull();
		const admin = buildKimiQuotaView({ ...BASE, error: "401 Unauthorized" }, t);
		expect(admin?.failed).toBe(true);
		expect(admin?.failureDetail).toBe("401 Unauthorized");
	});

	test("nothing to show for a cold cache", () => {
		expect(buildKimiQuotaView({ ...BASE, fiveHour: null, weekly: null }, t)).toBeNull();
		expect(buildKimiQuotaView(undefined, t)).toBeNull();
	});
});

describe("isKimiProviderBaseUrl", () => {
	test("matches kimi.com and kimi.ai hosts", () => {
		expect(isKimiProviderBaseUrl("https://api.kimi.com/coding/")).toBe(true);
		expect(isKimiProviderBaseUrl("https://api.kimi.ai/coding/v1")).toBe(true);
		expect(isKimiProviderBaseUrl("https://kimi.com")).toBe(true);
	});

	test("rejects non-kimi hosts and invalid URLs", () => {
		expect(isKimiProviderBaseUrl("https://api.moonshot.cn/v1")).toBe(false);
		expect(isKimiProviderBaseUrl("https://kimi.com.evil.example")).toBe(false);
		expect(isKimiProviderBaseUrl("not a url")).toBe(false);
		expect(isKimiProviderBaseUrl(undefined)).toBe(false);
	});
});
