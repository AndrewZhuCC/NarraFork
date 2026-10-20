import { describe, expect, spyOn, test } from "bun:test";
import { stallTimeBetween, startEventLoopMonitor, trackInFlight } from "./event-loop-monitor";
import { logger } from "./logger";

function block(ms: number): void {
	const until = performance.now() + ms;
	while (performance.now() < until) {}
}

const tick = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

describe("event loop stall accounting", () => {
	test("attributes a synchronous block to the overlapping window only", async () => {
		const warn = spyOn(logger, "warn");
		startEventLoopMonitor({ intervalMs: 10, warnThresholdMs: 100, warnThrottleMs: 0 });
		await tick(30);
		const startedAt = performance.now();
		const done = trackInFlight("GET /api/blocking");
		block(200);
		done();
		await tick(30);
		const endedAt = performance.now();

		const { stalledMs, maxStallMs } = stallTimeBetween(startedAt, endedAt);
		expect(stalledMs).toBeGreaterThanOrEqual(150);
		expect(maxStallMs).toBeGreaterThanOrEqual(150);
		expect(stalledMs).toBeLessThanOrEqual(endedAt - startedAt);
		// The blocker finished before the late tick fired, yet the lag warning still names it.
		const lagWarning = warn.mock.calls.find(([message]) => message === "Event loop lag detected");
		expect(JSON.stringify(lagWarning?.[1])).toContain("GET /api/blocking");
		warn.mockRestore();

		await tick(30);
		const after = performance.now();
		await tick(30);
		expect(stallTimeBetween(after, performance.now()).stalledMs).toBeLessThan(50);
	});
});
