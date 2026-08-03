import { describe, expect, test } from "bun:test";
import { providerStreamEventSchema } from "@server/lib/plugins/protocol";
import type { ParsedStreamEvent } from "@shared/codewhisperer/types";
// Relative rather than aliased: `examples/` is intentionally outside the tsconfig `paths`
// and `include`, so a plugin cannot be imported as if it were host code. The test reaches
// into the plugin deliberately, which is what makes the reach visible.
import {
	classifyKiroError,
	mapKiroEvent,
	type PluginStreamEvent,
	UsageAccumulator,
} from "../../../../examples/plugins/kiro-external/src/event-mapping";

/**
 * The Kiro → plugin-RPC event mapping.
 *
 * Two classes of failure are covered:
 *
 *  1. **Contract violations.** Every emitted event is validated against the host's real
 *     `providerStreamEventSchema` rather than an expected-shape literal. The schema is
 *     `.strict()`, so an extra or misnamed field fails here instead of at runtime inside a
 *     narrator session.
 *  2. **Silently wrong usage.** Kiro reports credits and a context percentage, not tokens.
 *     Emitting `0` token counts would make the host report a zero-cost request and compute
 *     context headroom from an imagined empty prompt, so absence is asserted explicitly.
 */

function mapOne(event: ParsedStreamEvent, usage = new UsageAccumulator()): PluginStreamEvent[] {
	const events = mapKiroEvent(event, usage);
	// Every event must satisfy the host contract, not just look plausible.
	for (const emitted of events) {
		const result = providerStreamEventSchema.safeParse(emitted);
		expect(result.success, `invalid event: ${JSON.stringify(emitted)}`).toBe(true);
	}
	return events;
}

function usageEventsOf(events: PluginStreamEvent[]) {
	return events.filter(
		(event): event is Extract<PluginStreamEvent, { type: "usage" }> => event.type === "usage",
	);
}

describe("kiro-external event mapping", () => {
	test("maps text deltas with the provider-native output index", () => {
		const events = mapOne({ text: "hello", textOutputIndex: 2 });
		expect(events).toEqual([{ type: "text.delta", text: "hello", outputIndex: 2 }]);
	});

	test("omits outputIndex rather than defaulting it to zero", () => {
		// Defaulting would collapse separate content blocks onto each other in the host's
		// interleaved reconstruction.
		const [event] = mapOne({ text: "hi" });
		expect(event).toEqual({ type: "text.delta", text: "hi" });
		expect("outputIndex" in event).toBe(false);
	});

	test("drops empty text instead of emitting a zero-length delta", () => {
		// The schema requires min length 1; an empty delta would be rejected downstream.
		expect(mapOne({ text: "" })).toEqual([]);
	});

	test("maps reasoning with the anthropic signature attached", () => {
		const events = mapOne({
			reasoning: "thinking",
			reasoningOutputIndex: 1,
			reasoningMetadata: { anthropic: { signature: "sig-abc" } } as never,
		});
		expect(events).toHaveLength(1);
		const event = events[0];
		if (event.type !== "reasoning.delta") throw new Error("expected reasoning.delta");
		expect(event.text).toBe("thinking");
		expect(event.outputIndex).toBe(1);
		// The signature must survive: upstream rejects replayed history without it.
		expect(event.metadata).toEqual({
			source: "kiro",
			format: "anthropic",
			data: { anthropic: { signature: "sig-abc" } },
		});
	});

	test("emits reasoning.metadata when a signature arrives without text", () => {
		const events = mapOne({
			reasoningMetadata: { anthropic: { signature: "sig-only" } } as never,
		});
		expect(events).toHaveLength(1);
		expect(events[0].type).toBe("reasoning.metadata");
	});

	test("maps redacted thinking", () => {
		const events = mapOne({ redactedThinking: { data: "encrypted-blob", outputIndex: 3 } });
		expect(events).toEqual([
			{ type: "reasoning.redacted", data: "encrypted-blob", outputIndex: 3, source: "kiro" },
		]);
	});

	test("maps a streaming tool call across start, delta and end", () => {
		const start = mapOne({ toolUseChunk: { toolUseId: "t1", name: "read", outputIndex: 0 } });
		expect(start).toEqual([
			{ type: "tool_call.start", toolUseId: "t1", name: "read", outputIndex: 0 },
		]);

		const delta = mapOne({ toolUseChunk: { toolUseId: "t1", input: '{"path":' } });
		expect(delta).toEqual([
			{ type: "tool_call.delta", toolUseId: "t1", argumentsDelta: '{"path":' },
		]);

		const end = mapOne({ toolUseChunk: { toolUseId: "t1", stop: true } });
		expect(end).toEqual([{ type: "tool_call.end", toolUseId: "t1" }]);
	});

	test("maps a fully-formed tool use", () => {
		const events = mapOne({
			toolUses: [{ toolUseId: "t2", name: "write", input: { path: "a.txt" }, outputIndex: 1 }],
		});
		expect(events).toEqual([
			{
				type: "tool_call.complete",
				toolUseId: "t2",
				name: "write",
				input: { path: "a.txt" },
				outputIndex: 1,
			},
		]);
	});

	test("maps credit metering to the usage event", () => {
		const events = mapOne({ metering: { unit: "credit", unitPlural: "credits", usage: 2.5 } });
		const [usageEvent] = usageEventsOf(events);
		expect(usageEvent.usage.metering).toEqual({
			unit: "credit",
			unitPlural: "credits",
			usage: 2.5,
		});
	});

	test("maps context usage percentage", () => {
		const [usageEvent] = usageEventsOf(mapOne({ contextUsagePercentage: 42 }));
		expect(usageEvent.usage.contextUsagePercentage).toBe(42);
	});

	test("clamps an out-of-range percentage instead of failing the request", () => {
		// The schema caps at 100; a bad upstream number must not abort a live stream.
		expect(
			usageEventsOf(mapOne({ contextUsagePercentage: 130 }))[0].usage.contextUsagePercentage,
		).toBe(100);
		expect(
			usageEventsOf(mapOne({ contextUsagePercentage: -5 }))[0].usage.contextUsagePercentage,
		).toBe(0);
	});

	test("never reports token counts Kiro did not send", () => {
		// The decisive assertion for this provider: Kiro meters credits, not tokens. A zero
		// would be read as a real measurement and corrupt both cost and context reporting.
		const usage = new UsageAccumulator();
		const events = mapKiroEvent(
			{ metering: { unit: "credit", unitPlural: "credits", usage: 1 }, contextUsagePercentage: 10 },
			usage,
		);
		const snapshot = usageEventsOf(events)[0].usage;
		for (const field of [
			"promptTokens",
			"inputTokens",
			"completionTokens",
			"cachedInputTokens",
		] as const) {
			expect(field in snapshot, `${field} must be absent, not zero`).toBe(false);
		}
	});

	test("ignores zero token counts but keeps real ones", () => {
		const usage = new UsageAccumulator();
		mapKiroEvent({ usage: { promptTokens: 0, completionTokens: 12 } } as never, usage);
		const snapshot = usage.snapshot();
		expect(snapshot).toBeDefined();
		expect("promptTokens" in (snapshot ?? {})).toBe(false);
		expect(snapshot?.completionTokens).toBe(12);
	});

	test("accumulates across events so done carries the final figures", () => {
		const usage = new UsageAccumulator();
		mapKiroEvent({ contextUsagePercentage: 20 }, usage);
		mapKiroEvent({ metering: { unit: "credit", unitPlural: "credits", usage: 3 } }, usage);
		usage.setContextWindow(200_000);
		expect(usage.snapshot()).toEqual({
			contextUsagePercentage: 20,
			metering: { unit: "credit", unitPlural: "credits", usage: 3 },
			contextWindow: 200_000,
		});
	});

	test("reports no usage at all when Kiro sent none", () => {
		const usage = new UsageAccumulator();
		mapKiroEvent({ text: "just text" }, usage);
		// An empty usage object would still be a claim; undefined is the honest answer.
		expect(usage.snapshot()).toBeUndefined();
	});

	test("emits content before the usage that describes it", () => {
		const events = mapOne({
			text: "answer",
			metering: { unit: "credit", unitPlural: "credits", usage: 1 },
		});
		expect(events.map((event) => event.type)).toEqual(["text.delta", "usage"]);
	});

	test("a done event carrying accumulated usage satisfies the contract", () => {
		const usage = new UsageAccumulator();
		mapKiroEvent({ metering: { unit: "credit", unitPlural: "credits", usage: 4 } }, usage);
		const done = {
			type: "done" as const,
			status: "completed" as const,
			stopReason: "end_turn" as const,
			credentialId: "cred-1",
			usage: usage.snapshot(),
		};
		expect(providerStreamEventSchema.safeParse(done).success).toBe(true);
	});
});

describe("kiro-external error classification", () => {
	test("marks quota exhaustion as non-retryable", () => {
		const error = Object.assign(new Error("Monthly limit reached"), { name: "KiroQuotaError" });
		const result = classifyKiroError(error);
		// Retrying cannot succeed until the quota window rolls over.
		expect(result).toMatchObject({
			classification: "api",
			code: "QUOTA_EXHAUSTED",
			retryable: false,
			statusCode: 429,
		});
	});

	test("marks auth failure as non-retryable", () => {
		const error = Object.assign(new Error("Bad credentials"), { name: "KiroAuthError" });
		expect(classifyKiroError(error)).toMatchObject({ code: "AUTH_FAILED", retryable: false });
	});

	test("marks missing configuration as invalid state", () => {
		const error = Object.assign(new Error("missing refreshToken"), {
			name: "MissingCredentialError",
		});
		expect(classifyKiroError(error)).toMatchObject({
			classification: "invalid_state",
			code: "NOT_CONFIGURED",
			retryable: false,
		});
	});

	test("treats abort as cancellation rather than an error", () => {
		const error = Object.assign(new Error("aborted"), { name: "AbortError" });
		expect(classifyKiroError(error).classification).toBe("cancelled");
	});

	test("treats an upstream API error as retryable", () => {
		const error = Object.assign(new Error("503"), { name: "KiroApiError" });
		expect(classifyKiroError(error)).toMatchObject({ code: "UPSTREAM_ERROR", retryable: true });
	});

	test("produces a contract-valid error payload", () => {
		const classified = classifyKiroError(new Error("socket hang up"));
		const event = { type: "error" as const, error: classified };
		expect(providerStreamEventSchema.safeParse(event).success).toBe(true);
	});
});
