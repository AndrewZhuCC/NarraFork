import { describe, expect, test } from "bun:test";
import { estimateJsonTokens } from "../estimate-json-tokens";
import { estimateTokens } from "../estimate-tokens";

describe("bounded exact JSON display estimate", () => {
	test("matches JSON escaping, CJK weighting and a single final ceil", async () => {
		for (const value of [
			'latin中文한글カナ😀\ud800\udfff\n\t"\\\u0000',
			{ key: "中\n文", nested: [true, false, 0, -0, 1.23e20, NaN, Infinity, null, undefined] },
			{
				skipped: undefined,
				symbol: Symbol("ignore"),
				fn: () => 1,
				date: new Date("2026-10-12T00:00:00Z"),
			},
			[Array(3), { toJSON: () => "projection" }, new Number(12), new String("wide字")],
		])
			expect(await estimateJsonTokens(value)).toBe(estimateTokens(JSON.stringify(value)));
		expect(estimateTokens("x".repeat(40))).toBe(12);
		expect(await estimateJsonTokens(["x".repeat(40), "字".repeat(100_000)])).toBe(
			estimateTokens(JSON.stringify(["x".repeat(40), "字".repeat(100_000)])),
		);
	});
	test("budget/abort/cycles are unknown, never a partial or zero estimate", async () => {
		const cycle: { next?: unknown } = {};
		cycle.next = cycle;
		for (const value of [cycle, 1n, undefined])
			expect(await estimateJsonTokens(value)).toBeUndefined();
		expect(await estimateJsonTokens(["oversized"], { maxBytes: 4 })).toBeUndefined();
		expect(await estimateJsonTokens(["expired"], { deadline: Date.now() - 1 })).toBeUndefined();
		expect(
			await estimateJsonTokens(["cancelled"], { signal: AbortSignal.abort() }),
		).toBeUndefined();
	});
	test("large text yields to timers instead of materializing a giant JSON string", async () => {
		let ticks = 0;
		const timer = setInterval(() => ticks++, 0);
		try {
			const estimate = await estimateJsonTokens([{ text: "中文\\\n".repeat(300_000) }]);
			expect(estimate).toBeGreaterThan(0);
			expect(ticks).toBeGreaterThan(0);
		} finally {
			clearInterval(timer);
		}
	});
});
