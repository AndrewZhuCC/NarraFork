import { describe, expect, test } from "bun:test";
import type { KimiUsageCache } from "../../../lib/api/types";
import { buildKimiProviderBadges } from "./use-kimi-provider-badges";

const t = (key: string, params?: Record<string, unknown>) =>
	params ? `${key}:${JSON.stringify(params)}` : key;

const NOW = Date.parse("2026-10-19T12:00:00.000Z");

function usage(overrides: Partial<KimiUsageCache> = {}): KimiUsageCache {
	return {
		fiveHour: { used: 30, limit: 100, remaining: 70, resetTime: "2026-10-19T14:00:00.000Z" },
		weekly: { used: 80, limit: 100, remaining: 20, resetTime: null },
		monthly: null,
		extraWindows: [],
		fetchedAt: NOW,
		error: null,
		...overrides,
	};
}

describe("buildKimiProviderBadges", () => {
	const providers = [
		{ id: "kimi", prefix: "km", baseUrl: "https://api.kimi.com/coding/v1" },
		{ id: "other", prefix: "oa", baseUrl: "https://api.openai.com/v1" },
		{ id: "off", prefix: "off", baseUrl: "https://api.kimi.com/coding/v1", disabled: true },
	];

	test("one bar per reported window for enabled Kimi providers", () => {
		const badges = buildKimiProviderBadges({
			providers,
			usages: { kimi: usage(), other: usage(), off: usage() },
			now: NOW,
			t,
		});
		expect(Object.keys(badges)).toEqual(["km"]);
		expect(badges.km?.bars?.map((bar) => [bar.id, bar.remainingPercent, bar.level])).toEqual([
			["fiveHour", 70, "ok"],
			["weekly", 20, "warning"],
		]);
		expect(badges.km?.color).toBe("yellow");
		expect(badges.km?.stale).toBe(false);
	});

	test("a provider with no cached usage gets no badge", () => {
		expect(buildKimiProviderBadges({ providers, usages: {}, now: NOW, t })).toEqual({});
	});
});
