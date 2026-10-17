import { describe, expect, spyOn, test } from "bun:test";
import type { RecordingFrame } from "@shared/request-recording";
import {
	CACHE_DROP_TTL_MS,
	CacheDropDetector,
	type CacheDropSample,
} from "../cache-drop-dump-store";

function fixture() {
	let clock = 1_800_000_000_000;
	let sequence = 0;
	const detector = new CacheDropDetector(() => clock);
	const sample = (overrides: Partial<CacheDropSample> = {}): CacheDropSample => ({
		requestId: `request-${++sequence}`,
		narratorId: "narrator-a",
		provider: "anthropic",
		model: "model-a",
		kind: "narrator",
		conversationId: "conversation-a",
		createdAt: new Date(++clock).toISOString(),
		startedAt: clock,
		inputTokens: 100_000,
		cachedInputTokens: 93_056,
		...overrides,
	});
	return {
		detector,
		sample,
		advance: (ms: number) => {
			clock += ms;
		},
	};
}

describe("metadata-only cache-drop detection", () => {
	test("associates before2, 93056->3968 trigger, and the next reflection without retaining bodies", () => {
		const { detector, sample } = fixture();
		const first = sample();
		const second = sample();
		detector.record(first);
		detector.record(second);
		const trigger = sample({ cachedInputTokens: 3968 });
		expect(detector.record(trigger)).toEqual([
			{
				role: "trigger",
				triggerRequestId: trigger.requestId,
				previousRequestId: second.requestId,
				beforeRequestIds: [first.requestId, second.requestId],
				previousCachedInputTokens: 93056,
				cachedInputTokens: 3968,
			},
		]);
		const after = detector.record(sample({ kind: "reflection", cachedInputTokens: 3968 }));
		expect(after).toHaveLength(1);
		expect(after[0]).toMatchObject({ role: "after", triggerRequestId: trigger.requestId });
		expect(after[0]).not.toHaveProperty("dump");
		expect(detector.record(sample({ cachedInputTokens: 3968 }))).toEqual([]);
	});

	test("errors and absent cache usage neither trigger nor replace a valid baseline", () => {
		const { detector, sample } = fixture();
		const baseline = sample();
		detector.record(baseline);
		expect(detector.record(sample({ cachedInputTokens: undefined }))).toEqual([]);
		expect(detector.record(sample({ cachedInputTokens: 0, errorMessage: "network" }))).toEqual([]);
		const [incident] = detector.record(sample({ cachedInputTokens: 0, inputTokens: 0 }));
		expect(incident.previousRequestId).toBe(baseline.requestId);
	});

	test("requires an absolute 8192-token drop, not a relative drop or increased input", () => {
		const { detector, sample } = fixture();
		detector.record(sample({ cachedInputTokens: 9000 }));
		expect(detector.record(sample({ cachedInputTokens: 4500 }))).toEqual([]);
		expect(detector.record(sample({ cachedInputTokens: 4500, inputTokens: 200000 }))).toEqual([]);
		detector.record(sample({ cachedInputTokens: 132992 }));
		expect(detector.record(sample({ cachedInputTokens: 100352 }))[0]).toMatchObject({
			role: "trigger",
			previousCachedInputTokens: 132992,
			cachedInputTokens: 100352,
		});
	});

	test.each([
		"provider",
		"model",
		"conversationId",
		"contextEpoch",
	] as const)("changing %s establishes a fresh baseline even on an error", (key) => {
		const { detector, sample } = fixture();
		detector.record(sample());
		expect(
			detector.record(
				sample({ [key]: key === "contextEpoch" ? 2 : "different", errorMessage: "error" }),
			),
		).toEqual([]);
		expect(detector.record(sample({ cachedInputTokens: 0 }))).toEqual([]);
	});

	test("reset fences old starts including same-millisecond starts, without affecting another narrator", () => {
		const { detector, sample, advance } = fixture();
		detector.record(sample());
		detector.record(sample({ narratorId: "other" }));
		const old = sample();
		detector.resetNarrator("narrator-a");
		advance(1);
		expect(detector.record(old)).toEqual([]);
		expect(detector.record(sample({ cachedInputTokens: 0 }))).toEqual([]);
		expect(detector.record(sample({ narratorId: "other", cachedInputTokens: 0 }))[0].role).toBe(
			"trigger",
		);
	});

	test("a late completion cannot replace a newer comparison baseline", () => {
		const { detector, sample } = fixture();
		const old = sample();
		detector.record(sample({ cachedInputTokens: 10000 }));
		expect(detector.record(old)).toEqual([]);
		expect(detector.record(sample({ cachedInputTokens: 9000 }))).toEqual([]);
	});

	test("duplicate completions do not consume the after slot", () => {
		const { detector, sample } = fixture();
		detector.record(sample());
		const trigger = sample({ cachedInputTokens: 0 });
		detector.record(trigger);
		expect(detector.record(trigger)).toEqual([]);
		expect(detector.record(sample({ cachedInputTokens: 0 }))[0].role).toBe("after");
	});

	test("TTL, disable, and restart clear comparison state rather than restoring old bodies", () => {
		const { detector, sample, advance } = fixture();
		detector.record(sample());
		advance(CACHE_DROP_TTL_MS);
		detector.maintain();
		expect(detector.record(sample({ cachedInputTokens: 0 }))).toEqual([]);
		detector.record(sample());
		detector.clearRolling();
		expect(detector.record(sample({ cachedInputTokens: 0 }))).toEqual([]);
		expect(new CacheDropDetector().record(sample({ cachedInputTokens: 0 }))).toEqual([]);
	});

	test("a bounded narrator cache evicts old baselines", () => {
		const { detector, sample } = fixture();
		detector.record(sample());
		for (let i = 0; i < 256; i++) detector.record(sample({ narratorId: `other-${i}` }));
		expect(detector.record(sample({ cachedInputTokens: 0 }))).toEqual([]);
	});

	test("pins recorder identities and explicitly marks evicted before evidence as missing", async () => {
		const recorder = await import("../request-recorder");
		const { settings } = await import("../settings");
		const { AppError } = await import("../errors");
		const { ApiRequestDumpCollector } = await import("../agent/request-dump");
		const { recordCacheDropDiagnostic, resetCacheDropBaseline } = await import(
			"../cache-drop-diagnostics"
		);
		const previousEnabled = settings.agent.cacheDropDumpEnabled;
		settings.agent.cacheDropDumpEnabled = true;
		const { sample } = fixture();
		const narratorId = "cache-drop-recorder-bridge";
		const first = sample({ narratorId });
		const second = sample({ narratorId });
		const trigger = sample({ narratorId, cachedInputTokens: 0 });
		const pin = spyOn(recorder, "pinRecordingRequest").mockImplementation(() => {});
		const detail = spyOn(recorder, "getRequestRecordingDetail").mockImplementation(
			async (_, requestId) => {
				if (requestId === first.requestId) throw new AppError("evicted", 404, "NOT_FOUND");
				return {
					requestId,
					narratorId,
					provider: "fixture",
					model: "fixture",
					startedAt: first.createdAt,
					partial: false,
					truncated: false,
					requestPreview: "",
					responsePreview: "",
					previewTruncated: { request: false, response: false },
				};
			},
		);
		const frames: RecordingFrame[] = [];
		const pins: string[] = [];
		const collector = new ApiRequestDumpCollector({
			requestId: trigger.requestId,
			pin: (reason) => {
				pins.push(reason);
			},
			request: () => {},
			responseMeta: () => {},
			responseChunk: () => {},
			frame: (frame) => {
				frames.push(frame);
			},
			finish: () => {},
		});
		try {
			await recordCacheDropDiagnostic(first);
			await recordCacheDropDiagnostic(second);
			await recordCacheDropDiagnostic(trigger, collector);
			expect(pins).toEqual(["request"]);
			expect(pin).toHaveBeenCalledWith(first.requestId, "request");
			expect(pin).toHaveBeenCalledWith(second.requestId, "request");
			expect(frames).toEqual([
				expect.objectContaining({
					type: "cache_drop",
					metadata: expect.objectContaining({
						requestId: trigger.requestId,
						role: "trigger",
						related: [
							{ requestId: first.requestId, availability: "missing" },
							{
								requestId: second.requestId,
								availability: "available",
								partial: false,
								truncated: false,
							},
						],
					}),
				}),
			]);
			await recordCacheDropDiagnostic(
				sample({ narratorId, kind: "reflection", cachedInputTokens: 0 }),
				collector,
			);
			expect(frames[1].metadata).toMatchObject({
				role: "after",
				triggerRequestId: trigger.requestId,
			});
			expect(pin).toHaveBeenCalledWith(trigger.requestId, "request");
		} finally {
			resetCacheDropBaseline(narratorId);
			settings.agent.cacheDropDumpEnabled = previousEnabled;
			detail.mockRestore();
			pin.mockRestore();
		}
	});
});
