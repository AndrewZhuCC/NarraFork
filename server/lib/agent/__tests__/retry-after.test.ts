import { describe, expect, test } from "bun:test";
import { parseCompactDurationMs, parseRetryAfterMs } from "../retry-after";

const NOW = Date.parse("2026-10-19T12:00:00.000Z");

describe("parseRetryAfterMs", () => {
	test("delta-seconds retry-after, including the observed weekly-wall value", () => {
		expect(parseRetryAfterMs({ "retry-after": "374160" }, NOW)).toBe(374_160_000);
		expect(parseRetryAfterMs({ "Retry-After": "2" }, NOW)).toBe(2_000);
	});

	test("HTTP-date retry-after", () => {
		expect(parseRetryAfterMs({ "retry-after": "Mon, 19 Oct 2026 12:05:00 GMT" }, NOW)).toBe(
			300_000,
		);
	});

	test("retry-after-ms wins over retry-after", () => {
		expect(parseRetryAfterMs({ "retry-after-ms": "1500", "retry-after": "60" }, NOW)).toBe(1_500);
	});

	test("rate-limit reset headers: epoch seconds, deltas and durations; latest wins", () => {
		const epoch = String(Math.floor((NOW + 90_000) / 1000));
		expect(parseRetryAfterMs({ "x-ratelimit-reset": epoch }, NOW)).toBe(90_000);
		expect(parseRetryAfterMs({ "ratelimit-reset": "30" }, NOW)).toBe(30_000);
		expect(
			parseRetryAfterMs(
				{ "x-ratelimit-reset-requests": "1s", "x-ratelimit-reset-tokens": "6m0s" },
				NOW,
			),
		).toBe(360_000);
	});

	test("no usable hint", () => {
		expect(parseRetryAfterMs(undefined, NOW)).toBeNull();
		expect(parseRetryAfterMs({ "content-type": "application/json" }, NOW)).toBeNull();
		expect(parseRetryAfterMs({ "retry-after": "soon" }, NOW)).toBeNull();
	});
});

describe("parseCompactDurationMs", () => {
	test("parses Go-style durations", () => {
		expect(parseCompactDurationMs("1h2m3s")).toBe(3_723_000);
		expect(parseCompactDurationMs("20ms")).toBe(20);
		expect(parseCompactDurationMs("1.5s")).toBe(1_500);
		expect(parseCompactDurationMs("abc")).toBeNull();
	});
});
