import { describe, expect, spyOn, test } from "bun:test";
import { stallTimeBetween, startEventLoopMonitor, trackInFlight } from "./event-loop-monitor";
import { logger } from "./logger";

function block(ms: number): void {
	const until = performance.now() + ms;
	while (performance.now() < until) {}
}

const tick = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

describe("event loop stall accounting", () => {
	test("attributes a synchronous block to the request that caused it", async () => {
		const warn = spyOn(logger, "warn");
		startEventLoopMonitor({ intervalMs: 10, warnThresholdMs: 100, warnThrottleMs: 0 });
		await tick(30);

		// Measured at request end, like the Slow API middleware: before the overdue tick has run.
		const startedAt = performance.now();
		const done = trackInFlight("GET /api/blocking");
		block(200);
		done();
		const endedAt = performance.now();
		const { stalledMs, maxStallMs } = stallTimeBetween(startedAt, endedAt);
		expect(stalledMs).toBeGreaterThanOrEqual(150);
		expect(maxStallMs).toBeGreaterThanOrEqual(150);
		expect(stalledMs).toBeLessThanOrEqual(endedAt - startedAt + 0.01);

		// Served right after the block, before the overdue tick: it overlaps but cannot be the cause.
		trackInFlight("GET /api/late")();
		await tick(30);

		// Recorded by the tick, the same window still reports the stall once.
		expect(stallTimeBetween(startedAt, endedAt).stalledMs).toBeCloseTo(stalledMs, -1);
		const lagWarning = warn.mock.calls.find(([message]) => message === "Event loop lag detected");
		const logged = JSON.stringify(lagWarning?.[1]);
		expect(logged).toContain("GET /api/blocking");
		expect(logged).not.toContain("GET /api/late");
		warn.mockRestore();

		const after = performance.now();
		await tick(30);
		expect(stallTimeBetween(after, performance.now()).stalledMs).toBeLessThan(50);
	});
});
