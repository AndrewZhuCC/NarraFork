import { afterAll, beforeEach, describe, expect, mock, spyOn, test } from "bun:test";
import { z } from "zod/v4";
import * as apiRequestTracker from "../../api-request-tracker";
import type { ProviderAdapter } from "../provider";
import { toolRegistry } from "../tool-registry";
import type { AgentConfig } from "../types";

/**
 * Reflection decision tools run as soon as their input is complete. The decision
 * settles the gate and aborts its controller, but a self-made decision must leave
 * the reflection request streaming long enough to collect its natural usage tail.
 */
const DECISION_TOOL = "EagerReflectionDecision";
const SECOND_DECISION_TOOL = "EagerReflectionSecondDecision";
const USAGE = {
	promptTokens: 128,
	inputTokens: 128,
	completionTokens: 9,
	cachedInputTokens: 64,
};

let gateAbort = new AbortController();
let decisions: string[] = [];
let streamCompleted = false;
let releaseTail: (() => void) | undefined;
let scenario: "self-decision" | "takeover" | "double-decision" = "self-decision";

const provider: ProviderAdapter = {
	formatTools: (tools) => tools.map(({ name, description }) => ({ name, description })),
	buildHistory: async () => ({ history: [] }),
	injectSystemPrompt: () => {},
	async *chat(params) {
		params.onRequestStart?.();
		const tail = Promise.withResolvers<void>();
		releaseTail = tail.resolve;
		if (scenario === "takeover") yield { text: "still thinking" };
		else
			yield {
				toolUses: [
					{ toolUseId: "tu-decision", name: DECISION_TOOL, input: {} },
					...(scenario === "double-decision"
						? [{ toolUseId: "tu-second", name: SECOND_DECISION_TOOL, input: {} }]
						: []),
				],
			};
		// The provider keeps streaming after the decision call: trailing text, then usage.
		if (scenario === "takeover") gateAbort.abort();
		const aborted = new Promise<never>((_, reject) => {
			if (params.signal.aborted) reject(new Error("Aborted"));
			params.signal.addEventListener("abort", () => reject(new Error("Aborted")), {
				once: true,
			});
		});
		await Promise.race([tail.promise, aborted]);
		yield { text: "decided" };
		yield { usage: USAGE };
		streamCompleted = true;
	},
	generate: async () => "",
	generateWithMeta: async () => ({ text: "" }),
	generateWithHistory: async () => "",
};

// Observe the durable completion boundary without needing a persisted narrator fixture.
const finishedRequests = spyOn(apiRequestTracker, "finishApiRequest").mockImplementation(
	async (handle) => handle.id,
);
// mock.restore() alone does not restore Bun's module mocks.
const realProviderModule = { ...(await import("../provider")) };
mock.module("../provider", () => ({
	...realProviderModule,
	getProvider: () => provider,
	resolveProviderAndModel: () => ({
		requestedProvider: "test",
		requestedModel: "test:model",
		provider: "test",
		adapter: provider,
		model: "test:model",
	}),
}));
const { runReflectionLoop } = await import("../loop");

for (const name of [DECISION_TOOL, SECOND_DECISION_TOOL]) {
	toolRegistry.register({
		name,
		description: "Fake reflection decision; settling it aborts the gate like the real gates",
		reflectionOnly: true,
		parameters: z.object({}),
		execute: async () => {
			decisions.push(name);
			// The real gates race the decision and abort the reflection controller when it
			// settles (`decision.finally(() => reflectionAbort.abort())`).
			queueMicrotask(() => gateAbort.abort());
			// The response is still streaming when the decision lands.
			queueMicrotask(() => releaseTail?.());
			return { output: "decision accepted" };
		},
	});
}

afterAll(() => {
	finishedRequests.mockRestore();
	mock.module("../provider", () => realProviderModule);
	for (const name of [DECISION_TOOL, SECOND_DECISION_TOOL]) toolRegistry.unregister(name);
	mock.restore();
});

beforeEach(() => {
	gateAbort = new AbortController();
	decisions = [];
	streamCompleted = false;
	releaseTail = undefined;
	finishedRequests.mockClear();
});

function parentConfig(): AgentConfig {
	return {
		narratorId: "n-reflection-eager",
		conversationId: "conv-reflection-eager",
		provider: "test",
		model: "test:model",
		cwd: "/tmp",
		signal: new AbortController().signal,
		permissionHandler: async () => ({ behavior: "allow" }),
	};
}

function run() {
	return runReflectionLoop({
		parentConfig: parentConfig(),
		rows: [],
		prompt: "decide",
		abortController: gateAbort,
		reflectionLoop: {
			allowedTools: [DECISION_TOOL, SECOND_DECISION_TOOL],
			context: { kind: "exitPlanMode", requestId: "eager-request" },
		},
	});
}

function expectNaturalCompletion() {
	expect(streamCompleted).toBe(true);
	expect(finishedRequests).toHaveBeenCalledTimes(1);
	expect(finishedRequests.mock.calls[0][1]).toMatchObject({
		errorMessage: null,
		usage: { inputTokens: 128, outputTokens: 9, cachedInputTokens: 64 },
	});
}

describe("reflection decisions made mid-stream", () => {
	test("a self-made decision lets the reflection request finish cleanly with usage", async () => {
		scenario = "self-decision";
		const observed = await run();

		expect(decisions).toEqual([DECISION_TOOL]);
		expect(observed.decisionSucceeded).toBe(true);
		expect(observed.errors).toEqual([]);
		expectNaturalCompletion();
	});

	test("a takeover without a self-made decision still cuts the request at once", async () => {
		scenario = "takeover";
		const observed = await run();

		expect(decisions).toEqual([]);
		expect(streamCompleted).toBe(false);
		expect(observed.errors).toContain("Aborted");
		expect(finishedRequests).toHaveBeenCalledTimes(1);
		expect(finishedRequests.mock.calls[0][1]).toMatchObject({ errorMessage: "Aborted" });
	});

	test("a second decision in the same response does not start after the first settled", async () => {
		scenario = "double-decision";
		const observed = await run();

		expect(decisions).toEqual([DECISION_TOOL]);
		expect(observed.decisionSucceeded).toBe(true);
		expect(observed.errors).toEqual([]);
		expectNaturalCompletion();
	});
});
