import { describe, expect, test } from "bun:test";
import type { OpencodeUsageCache } from "../../../lib/api/types";
import {
	buildOpencodeQuotaView,
	formatOpencodeBadgeText,
	formatOpencodeCountdown,
	formatOpencodeSummaryLines,
	levelForWindow,
	nextOpencodeWeekStartUtc,
} from "./opencode-usage-view";
import { buildOpencodeProviderBadges } from "./use-opencode-provider-badges";

const t = (key: string, params?: Record<string, unknown>) =>
	params ? `${key}:${JSON.stringify(params)}` : key;

/** Narrow a value the test just asserted exists, without a non-null assertion. */
function required<T>(value: T | null | undefined): T {
	if (value == null) throw new Error("expected a value");
	return value;
}

// Monday 2026-10-19 12:00 UTC.
const NOW = Date.parse("2026-10-19T12:00:00.000Z");

function usage(overrides: Partial<OpencodeUsageCache> = {}): OpencodeUsageCache {
	return {
		rolling: { status: "ok", percent: 37, resetsAt: "2026-10-19T14:30:00.000Z" },
		weekly: { status: "ok", percent: 12, resetsAt: "2026-10-26T00:00:00.000Z" },
		monthly: { status: "ok", percent: 50, resetsAt: "2026-11-15T12:00:00.000Z" },
		generation: "g1",
		fetchedAt: NOW - 10_000,
		error: null,
		...overrides,
	};
}

describe("levelForWindow", () => {
	test("thresholds on the remaining share", () => {
		const at = (percent: number) => levelForWindow({ status: "ok", percent, resetsAt: null });
		expect(at(10)).toBe("ok");
		expect(at(75)).toBe("warning");
		expect(at(90)).toBe("critical");
		expect(at(100)).toBe("exhausted");
		expect(levelForWindow({ status: "rate-limited", percent: 40, resetsAt: null })).toBe(
			"exhausted",
		);
		expect(levelForWindow({ status: "ok", percent: null, resetsAt: null })).toBe("unknown");
	});
});

describe("buildOpencodeQuotaView", () => {
	test("three windows with remaining shares; the lowest remaining bounds the badge", () => {
		const view = buildOpencodeQuotaView(usage(), NOW);
		expect(view?.windows.map((w) => [w.key, w.remainingPercent])).toEqual([
			["fiveHour", 63],
			["weekly", 88],
			["monthly", 50],
		]);
		expect(view?.limiting?.key).toBe("monthly");
		expect(view?.level).toBe("ok");
		expect(formatOpencodeBadgeText(required(view), t)).toBe(
			'opencode.badgeRemaining:{"label":"opencode.shortMonthly","percent":50}',
		);
	});

	test("an exhausted window wins, and the latest-refilling one among them", () => {
		const view = buildOpencodeQuotaView(
			usage({
				rolling: { status: "rate-limited", percent: 100, resetsAt: "2026-10-19T14:00:00Z" },
				weekly: { status: "rate-limited", percent: 100, resetsAt: "2026-10-26T00:00:00Z" },
			}),
			NOW,
		);
		expect(view?.level).toBe("exhausted");
		expect(view?.limiting?.key).toBe("weekly");
		expect(formatOpencodeBadgeText(required(view), t)).toBe(
			'opencode.badgeExhausted:{"label":"opencode.shortWeekly"}',
		);
	});

	test("a weekly window without resetsAt falls back to next Monday 00:00 UTC", () => {
		const view = buildOpencodeQuotaView(
			usage({ weekly: { status: "ok", percent: 20, resetsAt: null } }),
			NOW,
		);
		const weekly = view?.windows.find((w) => w.key === "weekly");
		expect(weekly?.resetAt).toBe(Date.parse("2026-10-26T00:00:00Z"));
		expect(weekly?.resetDerived).toBe(true);
		// The rolling and monthly windows are never guessed.
		const monthly = buildOpencodeQuotaView(
			usage({ monthly: { status: "ok", percent: 20, resetsAt: null } }),
			NOW,
		)?.windows.find((w) => w.key === "monthly");
		expect(monthly?.resetAt).toBeNull();
	});

	test("missing windows are omitted; absolute amounts are passed through only in pairs", () => {
		const view = buildOpencodeQuotaView(
			usage({
				rolling: { status: "ok", percent: 25, resetsAt: null, used: 1.5, limit: 6 },
				weekly: null,
				monthly: null,
			}),
			NOW,
		);
		expect(view?.windows).toHaveLength(1);
		expect(view?.windows[0]).toMatchObject({ used: 1.5, limit: 6, remainingPercent: 75 });
	});

	test("degrades gracefully: cold cache hides, a failed fetch still shows a placeholder", () => {
		expect(buildOpencodeQuotaView(undefined, NOW)).toBeNull();
		expect(
			buildOpencodeQuotaView(usage({ rolling: null, weekly: null, monthly: null }), NOW),
		).toBeNull();
		const failed = buildOpencodeQuotaView(
			usage({ rolling: null, weekly: null, monthly: null, error: undefined, hasError: true }),
			NOW,
		);
		expect(failed?.failed).toBe(true);
		expect(formatOpencodeBadgeText(required(failed), t)).toBe("opencode.unavailable");
	});

	test("a balance alone is still worth showing", () => {
		const view = buildOpencodeQuotaView(
			usage({ rolling: null, weekly: null, monthly: null, balance: 4.5 }),
			NOW,
		);
		expect(view?.balance).toBe(4.5);
		expect(formatOpencodeSummaryLines(required(view), NOW, t)).toEqual([
			'opencode.balanceLine:{"amount":"4.5"}',
		]);
	});
});

describe("countdowns", () => {
	test("next Monday 00:00 UTC strictly after now", () => {
		expect(nextOpencodeWeekStartUtc(NOW)).toBe(Date.parse("2026-10-26T00:00:00Z"));
		expect(nextOpencodeWeekStartUtc(Date.parse("2026-10-25T23:59:00Z"))).toBe(
			Date.parse("2026-10-26T00:00:00Z"),
		);
	});

	test("formats days/hours, hours/minutes, minutes, and due", () => {
		expect(formatOpencodeCountdown(3 * 86_400_000 + 4 * 3_600_000, t)).toBe(
			'opencode.countdownDH:{"d":3,"h":4}',
		);
		expect(formatOpencodeCountdown(2 * 3_600_000 + 15 * 60_000, t)).toBe(
			'opencode.countdownHM:{"h":2,"m":15}',
		);
		expect(formatOpencodeCountdown(30_000, t)).toBe('opencode.countdownM:{"m":1}');
		expect(formatOpencodeCountdown(0, t)).toBe("opencode.resetDue");
	});
});

describe("buildOpencodeProviderBadges", () => {
	const providers = [
		{ id: "go", prefix: "ocg", baseUrl: "https://opencode.ai/inference/go/anthropic/v1" },
		{ id: "zen", prefix: "zen", baseUrl: "https://opencode.ai/zen/v1" },
		{ id: "off", prefix: "off", baseUrl: "https://opencode.ai/zen/go/v1", disabled: true },
	];

	test("badges enabled Go providers by prefix with a colour and a tooltip", () => {
		const badges = buildOpencodeProviderBadges({
			providers,
			usages: { go: usage({ generation: "g1" }), off: usage({ generation: "x" }) },
			generations: { go: "g1", off: "x" },
			now: NOW,
			t,
		});
		expect(Object.keys(badges)).toEqual(["ocg"]);
		expect(badges.ocg?.color).toBe("teal");
		expect(badges.ocg?.tooltip?.split("\n")).toHaveLength(3);
		expect(badges.ocg?.bars?.map((bar) => [bar.id, bar.remainingPercent])).toEqual([
			["fiveHour", 63],
			["weekly", 88],
			["monthly", 50],
		]);
	});

	test("skips an allowance whose generation belongs to a replaced account", () => {
		expect(
			buildOpencodeProviderBadges({
				providers,
				usages: { go: usage({ generation: "old" }) },
				generations: { go: "new" },
				now: NOW,
				t,
			}),
		).toEqual({});
	});
});
