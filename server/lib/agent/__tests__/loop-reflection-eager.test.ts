import { afterAll, describe, expect, mock, test } from "bun:test";
import { z } from "zod/v4";
import type { ProviderAdapter } from "../provider";
import { toolRegistry } from "../tool-registry";
import type { AgentConfig, AgentEvent } from "../types";

const REFLECTION_TOOL_NAME = "TestReflectDecisionTool";
const NORMAL_TOOL_NAME = "TestReflectNormalTool";

/** Records the order in which each tool's execute() started. */
const executionOrder: string[] = [];

const KNOWLEDGE_TOOLS = ["KnowledgeCreate", "KnowledgeEdit", "KnowledgeReview", "KnowledgeAdmin"];
let scenario: "reflection" | "normal" | "knowledge" = "reflection";
let knowledgeToolName = KNOWLEDGE_TOOLS[0];
let knowledgeChunks = false;
let streamReleased = false;
let knowledgeRequests = 0;
const permissionAfterRelease: boolean[] = [];

const testProvider: ProviderAdapter = {
	formatTools: (tools) => tools,
	buildHistory: async () => ({ history: [], trailingToolResults: [] }),
	injectSystemPrompt: () => {},
	async *chat(params) {
		params.onRequestStart?.();
		if (scenario === "knowledge") {
			if (knowledgeRequests++ > 0) {
				yield { text: "done" };
				return;
			}
			try {
				if (knowledgeChunks) {
					yield {
						toolUseChunk: {
							toolUseId: "tu_knowledge",
							name: knowledgeToolName,
							input: '{"value":"x"}',
							outputIndex: 0,
						},
					};
					yield { toolUseChunk: { toolUseId: "tu_knowledge", stop: true } };
				} else {
					yield {
						toolUses: [
							{ toolUseId: "tu_knowledge", name: knowledgeToolName, input: { value: "x" } },
						],
					};
				}
				yield { text: "trailing text before provider cleanup" };
				await new Promise((resolve) => setTimeout(resolve, 1));
				if (params.signal.aborted) throw new Error("Aborted");
			} finally {
				// Models the WebSocket session's busy flag being cleared in finally.
				streamReleased = true;
			}
			return;
		}
		const toolName = scenario === "reflection" ? REFLECTION_TOOL_NAME : NORMAL_TOOL_NAME;
		// Emit the decision tool call, then keep streaming trailing text so there is a
		// window during which an eager execution (if any) would have already started.
		yield {
			toolUses: [{ toolUseId: "tu_decision", name: toolName, input: { value: "x" } }],
		};
		yield { text: "trailing text after tool call" };
		// Hang until the caller aborts mid-stream (mirrors a real provider stream that is
		// still open when the reflection decision settles and aborts the reflection signal).
		while (!params.signal.aborted) {
			await new Promise((resolve) => setTimeout(resolve, 1));
		}
		throw new Error("Aborted");
	},
	formatToolResult: (toolUseId, output, isError) => ({ toolUseId, output, isError }),
	pushUserTurn: () => {},
	pushAssistantTurn: () => {},
	generate: async () => "",
	generateWithMeta: async () => ({ text: "" }),
	generateWithHistory: async () => "",
};

// Snapshot real provider before mocking; afterAll re-points it back (Bun mock.module is global and leaks; mock.restore() does not undo it).
const realProviderModule = { ...(await import("../provider")) };

mock.module("../provider", () => ({
	getProvider: () => testProvider,
	resolveProviderAndModel: () => ({
		requestedProvider: "test",
		requestedModel: "test:model",
		provider: "test",
		adapter: testProvider,
		model: "test:model",
	}),
}));

const { agentLoop } = await import("../loop");

toolRegistry.register({
	name: REFLECTION_TOOL_NAME,
	description: "Fake reflection decision tool",
	reflectionOnly: true,
	parameters: z.object({ value: z.string() }),
	execute: async (args) => {
		executionOrder.push(REFLECTION_TOOL_NAME);
		return { output: `reflect:${args.value}` };
	},
});

toolRegistry.register({
	name: NORMAL_TOOL_NAME,
	description: "Fake normal (eager-eligible) tool",
	parameters: z.object({ value: z.string() }),
	execute: async (args) => {
		executionOrder.push(NORMAL_TOOL_NAME);
		return { output: `normal:${args.value}` };
	},
});

const originalKnowledgeTools = KNOWLEDGE_TOOLS.map((name) => toolRegistry.get(name));
for (const name of KNOWLEDGE_TOOLS) {
	toolRegistry.register({
		name,
		description: "Knowledge write with internal approval",
		parameters: z.object({ value: z.string() }),
		async execute(args, ctx) {
			executionOrder.push(name);
			permissionAfterRelease.push(streamReleased);
			await ctx.requestPermission(name, args, ctx.currentToolUseId ?? "");
			return { output: "saved" };
		},
	});
}

afterAll(() => {
	for (let i = 0; i < KNOWLEDGE_TOOLS.length; i++) {
		const original = originalKnowledgeTools[i];
		if (original) toolRegistry.register(original);
		else toolRegistry.unregister(KNOWLEDGE_TOOLS[i]);
	}
	mock.module("../provider", () => realProviderModule);
	toolRegistry.unregister(REFLECTION_TOOL_NAME);
	toolRegistry.unregister(NORMAL_TOOL_NAME);
	mock.restore();
});

function makeConfig(signal: AbortSignal): AgentConfig {
	return {
		narratorId: "n-reflect-eager",
		conversationId: "conv-reflect-eager",
		model: "test:model",
		provider: "test",
		cwd: "/tmp",
		signal,
		permissionHandler: async () => ({ behavior: "allow" }),
	};
}

async function runUntilAbortMidStream(): Promise<AgentEvent[]> {
	const ac = new AbortController();
	const events: AgentEvent[] = [];
	for await (const event of agentLoop(makeConfig(ac.signal), "decide", [])) {
		events.push(event);
		// Abort while the provider stream is still open (trailing text just arrived),
		// mirroring the reflection abort race that produced spurious "Aborted" records.
		if (event.type === "stream_text") {
			await Promise.resolve();
			ac.abort();
		}
	}
	return events;
}

describe("knowledge approval waits for the parent provider stream to close", () => {
	for (const name of KNOWLEDGE_TOOLS) {
		for (const chunks of [false, true]) {
			for (const abort of [false, true]) {
				test(`${name}: chunks=${chunks}, abort=${abort}`, async () => {
					scenario = "knowledge";
					knowledgeToolName = name;
					knowledgeChunks = chunks;
					knowledgeRequests = 0;
					streamReleased = false;
					executionOrder.length = 0;
					permissionAfterRelease.length = 0;
					if (abort) {
						await runUntilAbortMidStream();
					} else {
						const config = makeConfig(new AbortController().signal);
						for await (const event of agentLoop(config, "save", [])) {
							if (event.type === "error") throw new Error(event.message);
						}
					}
					expect(streamReleased).toBe(true);
					expect(executionOrder).toEqual(abort ? [] : [name]);
					expect(permissionAfterRelease).toEqual(abort ? [] : [true]);
				});
			}
		}
	}
});

describe("reflection-only tools are not eager-executed mid-stream", () => {
	test("reflection decision tool is deferred until the post-stream tool phase", async () => {
		scenario = "reflection";
		executionOrder.length = 0;

		const events = await runUntilAbortMidStream();

		// The reflection tool must NOT execute eagerly during streaming. When the stream is
		// aborted mid-flight (before the post-stream tool phase), a deferred tool never runs,
		// so its execute() is never called. This is the exact behavior that prevents the
		// reflection decision from aborting the reflection provider.chat() mid-stream and
		// recording a spurious "Aborted" API request.
		expect(executionOrder).not.toContain(REFLECTION_TOOL_NAME);
		expect(events.at(-1)).toEqual({ type: "error", message: "Aborted" });
	});

	test("a normal eager-eligible tool still executes during streaming", async () => {
		scenario = "normal";
		executionOrder.length = 0;

		await runUntilAbortMidStream();

		// Baseline: a non-reflection tool IS eager-executed, so its execute() starts during
		// streaming even though the stream is aborted mid-flight. This guards against the fix
		// over-broadly disabling eager execution for ordinary tools.
		expect(executionOrder).toContain(NORMAL_TOOL_NAME);
	});
});
