import { describe, expect, test } from "bun:test";
import { NarratorLoadingMetrics } from "./narrator-loading-metrics";

describe("bounded narrator loading diagnostics", () => {
	test("records monotonic phases without carrying late events across switches", () => {
		let time = 0;
		const metrics = new NarratorLoadingMetrics(() => time);
		const first = metrics.start("A");
		time = 20;
		metrics.mark(first, "tail-start");
		const second = metrics.start("A");
		time = 50;
		metrics.mark(first, "first-paint");
		metrics.mark(second, "tail-ready", { rows: 40, bytes: 800 });
		metrics.mark(second, "tail-ready", { rows: 100 });
		expect(metrics.snapshot()).toEqual([
			{
				narratorId: "A",
				observations: [
					{ phase: "switch", elapsedMs: 0 },
					{ phase: "tail-ready", elapsedMs: 30, rows: 40, bytes: 800 },
				],
			},
		]);
	});

	test("bounds retained narrators and returns detached snapshots", () => {
		const metrics = new NarratorLoadingMetrics(() => 0, 2);
		const discarded = metrics.start("A");
		metrics.start("B");
		metrics.start("C");
		metrics.mark(discarded, "first-paint");
		expect(metrics.current("A")).toBeUndefined();
		const snapshot = metrics.snapshot();
		expect(snapshot.map((trace) => trace.narratorId)).toEqual(["B", "C"]);
		snapshot[0].observations.length = 0;
		expect(metrics.snapshot()[0].observations.length).toBe(1);
		metrics.clear();
		expect(metrics.snapshot()).toEqual([]);
	});

	test("rejects invalid counters and uses zero for a clock that went backwards", () => {
		let time = 5;
		const metrics = new NarratorLoadingMetrics(() => time);
		const token = metrics.start("A");
		time = 0;
		metrics.mark(token, "layout-ready", { rows: -1, bytes: Number.POSITIVE_INFINITY });
		expect(metrics.snapshot()[0].observations[1]).toEqual({
			phase: "layout-ready",
			elapsedMs: 0,
			rows: undefined,
			bytes: undefined,
		});
	});
});
