import { describe, expect, test } from "bun:test";
import type { OpencodeUsageCache } from "../../../lib/api/types";
import {
	formatOpencodeBarText,
	formatOpencodeDetailsText,
	isOpencodeProviderBaseUrl,
	opencodeUsageFailed,
} from "./opencode-usage-format";

/**
 * Translation stub that echoes the key, so assertions read as the key plus its
 * interpolation and do not depend on the locale files.
 */
const t = (key: string, options?: Record<string, unknown>) =>
	options && Object.keys(options).length ? `${key}:${JSON.stringify(options)}` : key;

function usage(overrides: Partial<OpencodeUsageCache> = {}): OpencodeUsageCache {
	return {
		rolling: { status: "ok", percent: 37, resetsAt: "2026-10-15T17:56:27.780Z" },
		weekly: { status: "ok", percent: 12, resetsAt: "2026-10-19T00:00:00.000Z" },
		monthly: { status: "rate-limited", percent: 100, resetsAt: "2026-11-15T12:39:31.000Z" },
		generation: "gen-1",
		fetchedAt: 1_700_000_000_000,
		error: null,
		...overrides,
	};
}

describe("formatOpencodeBarText", () => {
	test("shows what is LEFT of the 5-hour window, not what was spent", () => {
		// The upstream `percent` is the share already spent; the bar follows the
		// "remaining" convention the Kimi and Codex indicators use, so 37% spent is
		// reported as 63% left.
		expect(formatOpencodeBarText(usage(), t)).toBe('opencode.rollingShort:{"percent":63}');
	});

	test("clamps the remaining percentage into 0-100", () => {
		expect(
			formatOpencodeBarText(usage({ rolling: { status: "ok", percent: 130, resetsAt: null } }), t),
		).toBe('opencode.rollingShort:{"percent":0}');
		expect(
			formatOpencodeBarText(usage({ rolling: { status: "ok", percent: -20, resetsAt: null } }), t),
		).toBe('opencode.rollingShort:{"percent":100}');
	});

	test("falls back to unavailable when the rolling window is missing and the fetch failed", () => {
		expect(formatOpencodeBarText(usage({ rolling: null, error: "boom" }), t)).toBe(
			"opencode.unavailable",
		);
		expect(
			formatOpencodeBarText(usage({ rolling: null, error: undefined, hasError: true }), t),
		).toBe("opencode.unavailable");
	});

	test("returns null so the panel can fall back to the generic quota string", () => {
		expect(formatOpencodeBarText(usage({ rolling: null }), t)).toBeNull();
		expect(
			formatOpencodeBarText(usage({ rolling: { status: "ok", percent: null, resetsAt: null } }), t),
		).toBeNull();
	});

	test("treats a non-admin hasError as a failure, not a success", () => {
		expect(opencodeUsageFailed(usage({ error: undefined, hasError: true }))).toBe(true);
		expect(opencodeUsageFailed(usage({ error: null }))).toBe(false);
	});
});

describe("formatOpencodeDetailsText", () => {
	test("lists the 5-hour, weekly and monthly windows one per line with their resets", () => {
		const lines = formatOpencodeDetailsText(usage(), t).split("\n");
		expect(lines).toHaveLength(3);
		expect(lines[0]).toContain("opencode.window5h");
		expect(lines[0]).toContain('"percent":37');
		expect(lines[0]).toContain("opencode.resetSuffix");
		expect(lines[1]).toContain("opencode.windowWeekly");
		expect(lines[2]).toContain("opencode.windowMonthly");
	});

	test("omits a window the response did not include", () => {
		const text = formatOpencodeDetailsText(usage({ weekly: null, monthly: null }), t);
		expect(text).not.toContain("opencode.windowWeekly");
		expect(text).not.toContain("opencode.windowMonthly");
		expect(text).toContain("opencode.window5h");
	});

	test("reports the fetch error to admins and a bare failure to everyone else", () => {
		expect(formatOpencodeDetailsText(usage({ error: "upstream 500" }), t)).toContain(
			'opencode.fetchError:{"error":"upstream 500"}',
		);
		expect(formatOpencodeDetailsText(usage({ error: undefined, hasError: true }), t)).toContain(
			"opencode.fetchFailed",
		);
	});

	test("ignores an unparseable reset time instead of printing it raw", () => {
		// Only the rolling window is present here: the fixture's other windows carry
		// valid reset times, so leaving them in would prove nothing about this case.
		const text = formatOpencodeDetailsText(
			usage({
				rolling: { status: "ok", percent: 10, resetsAt: "not a date" },
				weekly: null,
				monthly: null,
			}),
			t,
		);
		expect(text).not.toContain("opencode.resetSuffix");
	});
});

describe("isOpencodeProviderBaseUrl", () => {
	test("matches both Go plan spellings", () => {
		expect(isOpencodeProviderBaseUrl("https://opencode.ai/zen/go/v1")).toBe(true);
		expect(isOpencodeProviderBaseUrl("https://opencode.ai/inference/go/v1/")).toBe(true);
	});

	test("rejects the pay-as-you-go Zen endpoint and other hosts", () => {
		for (const baseUrl of [
			"https://opencode.ai/zen/v1",
			"https://opencode.ai/",
			"https://example.com/zen/go/v1",
			"http://opencode.ai/zen/go/v1",
			"https://opencode.ai.evil.test/zen/go/v1",
			"",
			undefined,
			"invalid",
		]) {
			expect(isOpencodeProviderBaseUrl(baseUrl)).toBe(false);
		}
	});
});
