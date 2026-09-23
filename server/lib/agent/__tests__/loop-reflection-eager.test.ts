import { afterAll, describe, expect, mock, test } from "bun:test";
import { z } from "zod/v4";
import type { ProviderAdapter } from "../provider";
import { toolRegistry } from "../tool-registry";
import type { AgentConfig, AgentEvent } from "../types";

const REFLECTION_TOOL_NAME = "TestReflectDecisionTool";
const NORMAL_TOOL_NAME = "TestReflectNormalTool";
const PROVIDER_SESSION_TOOLS = ["Bash", "Write", "Edit", "StructSed"] as const;
const EAGER_READ_TOOL = "Read";

/** Records the order in which each tool's execute() started. */
const executionOrder: string[] = [];

const KNOWLEDGE_TOOLS = ["KnowledgeCreate", "KnowledgeEdit", "KnowledgeReview", "KnowledgeAdmin"];
let scenario: "reflection" | "normal" | "reflection_then_normal" | "knowledge" | "session_tool" =
	"reflection";
let knowledgeToolName = KNOWLEDGE_TOOLS[0];
let knowledgeChunks = false;
let sessionToolName: (typeof PROVIDER_SESSION_TOOLS)[number] | typeof EAGER_READ_TOOL = "Bash";
let sessionToolInputOverride: Record<string, unknown> | undefined;
let streamReleased = false;
let knowledgeRequests = 0;
let sessionToolRequests = 0;
const permissionAfterRelease: boolean[] = [];
const executionAfterRelease: boolean[] = [];
let onNormalStarted: (() => void) | undefined;
let onSessionToolStarted: (() => void) | undefined;

const testProvider: ProviderAdapter = {
	formatTools: (tools) => tools.map(({ name, description }) => ({ name, description })),
	buildHistory: async () => ({ history: [], trailingToolResults: [] }),
	injectSystemPrompt: () => {},
	async *chat(params) {
		params.onRequestStart?.();
		if (scenario === "session_tool") {
			if (sessionToolRequests++ > 0) {
				yield { text: "done" };
				return;
			}
			try {
				let resolveSessionToolStarted!: () => void;
				const sessionToolStarted = new Promise<void>((resolve) => {
					resolveSessionToolStarted = resolve;
				});
				onSessionToolStarted = resolveSessionToolStarted;
				const input =
					sessionToolInputOverride ??
					(sessionToolName === "Bash"
						? { command: "custom-command" }
						: sessionToolName === "Read"
							? { file_path: "/tmp/input.txt" }
							: sessionToolName === "StructSed"
								? { file_path: "/tmp/output.txt", content: "x", dry_run: false }
								: { file_path: "/tmp/output.txt", content: "x" });
				yield {
					toolUses: [{ toolUseId: "tu_session_tool", name: sessionToolName, input }],
				};
				yield { text: "trailing text before provider cleanup" };
				if (sessionToolName === EAGER_READ_TOOL) {
					await sessionToolStarted;
				} else {
					await new Promise((resolve) => setTimeout(resolve, 1));
				}
			} finally {
				streamReleased = true;
			}
			return;
		}
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
		// The ordinary eager baseline has no preceding reflection barrier. A separate
		// scenario verifies that ordinary work cannot overtake such a barrier.
		const toolName = scenario === "normal" ? NORMAL_TOOL_NAME : REFLECTION_TOOL_NAME;
		yield {
			toolUses: [
				{ toolUseId: "tu_decision", name: toolName, input: { value: "x" } },
				...(scenario === "reflection_then_normal"
					? [{ toolUseId: "tu_after_reflection", name: NORMAL_TOOL_NAME, input: { value: "y" } }]
					: []),
			],
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
		onNormalStarted?.();
		return { output: `normal:${args.value}` };
	},
});

const originalKnowledgeTools = KNOWLEDGE_TOOLS.map((name) => toolRegistry.get(name));
const sessionToolNames = [...PROVIDER_SESSION_TOOLS, EAGER_READ_TOOL];
const originalSessionTools = sessionToolNames.map((name) => toolRegistry.get(name));
for (const name of sessionToolNames) {
	toolRegistry.register({
		name,
		description: `Provider-session boundary test tool ${name}`,
		parameters: z.object({
			command: z.string().optional(),
			file_path: z.string().optional(),
			content: z.string().optional(),
			dry_run: z.boolean().optional(),
		}),
		execute: async (args, ctx) => {
			executionOrder.push(name);
			executionAfterRelease.push(streamReleased);
			onSessionToolStarted?.();
			if (name !== EAGER_READ_TOOL) {
				await ctx.requestPermission(name, args, ctx.currentToolUseId ?? "");
			}
			return { output: `ran:${name}` };
		},
	});
}
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
	for (let i = 0; i < sessionToolNames.length; i++) {
		const original = originalSessionTools[i];
		if (original) toolRegistry.register(original);
		else toolRegistry.unregister(sessionToolNames[i]);
	}
	mock.module("../provider", () => realProviderModule);
	toolRegistry.unregister(REFLECTION_TOOL_NAME);
	toolRegistry.unregister(NORMAL_TOOL_NAME);
	mock.restore();
});

function makeConfig(signal: AbortSignal, overrides: Partial<AgentConfig> = {}): AgentConfig {
	return {
		narratorId: "n-reflect-eager",
		conversationId: "conv-reflect-eager",
		model: "test:model",
		provider: "test",
		cwd: "/tmp",
		signal,
		permissionHandler: async () => ({ behavior: "allow" }),
		...overrides,
	};
}

async function runUntilAbortMidStream(): Promise<AgentEvent[]> {
	const ac = new AbortController();
	const events: AgentEvent[] = [];
	const normalStarted = new Promise<void>((resolve) => {
		onNormalStarted = resolve;
	});
	for await (const event of agentLoop(makeConfig(ac.signal), "decide", [])) {
		events.push(event);
		// Abort while the provider stream is still open (trailing text just arrived),
		// mirroring the reflection abort race that produced spurious "Aborted" records.
		if (event.type === "stream_text") {
			// A tool_call announcement alone does not imply execute() has started.
			// Wait for real execution only in the independent, barrier-free baseline.
			if (scenario === "normal") await normalStarted;
			ac.abort();
		}
	}
	onNormalStarted = undefined;
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

describe("provider-session tools wait for the parent stream release", () => {
	for (const name of PROVIDER_SESSION_TOOLS) {
		test(`${name} permission and execution start after provider finally`, async () => {
			scenario = "session_tool";
			sessionToolName = name;
			sessionToolInputOverride = undefined;
			sessionToolRequests = 0;
			streamReleased = false;
			executionOrder.length = 0;
			executionAfterRelease.length = 0;
			permissionAfterRelease.length = 0;
			onSessionToolStarted = undefined;
			const config = makeConfig(new AbortController().signal, {
				permissionHandler: async () => {
					permissionAfterRelease.push(streamReleased);
					return { behavior: "allow" };
				},
			});
			for await (const event of agentLoop(config, "run session tool", [])) {
				if (event.type === "error") throw new Error(event.message);
			}
			expect(streamReleased).toBe(true);
			expect(executionOrder).toEqual([name]);
			expect(executionAfterRelease).toEqual([true]);
			expect(permissionAfterRelease.length).toBeGreaterThan(0);
			expect(permissionAfterRelease.every(Boolean)).toBe(true);
		});
	}

	test("spec task Write remains post-stream while ordinary Read remains eager", async () => {
		scenario = "session_tool";
		sessionToolName = "Write";
		sessionToolInputOverride = { file_path: "spec://tasks.json", content: '{"tasks":[]}' };
		sessionToolRequests = 0;
		streamReleased = false;
		executionOrder.length = 0;
		executionAfterRelease.length = 0;
		for await (const event of agentLoop(
			makeConfig(new AbortController().signal),
			"update tasks",
			[],
		)) {
			if (event.type === "error") throw new Error(event.message);
		}
		expect(executionAfterRelease).toEqual([true]);

		sessionToolName = EAGER_READ_TOOL;
		sessionToolInputOverride = undefined;
		sessionToolRequests = 0;
		streamReleased = false;
		executionOrder.length = 0;
		executionAfterRelease.length = 0;
		onSessionToolStarted = undefined;
		for await (const event of agentLoop(makeConfig(new AbortController().signal), "read", [])) {
			if (event.type === "error") throw new Error(event.message);
		}
		expect(executionOrder).toEqual([EAGER_READ_TOOL]);
		expect(executionAfterRelease).toEqual([false]);
	});
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

	test("ordinary tools after a reflection barrier do not execute mid-stream", async () => {
		scenario = "reflection_then_normal";
		executionOrder.length = 0;

		const events = await runUntilAbortMidStream();

		expect(executionOrder).toEqual([]);
		// Persisted but unstarted calls receive cancellation results, not execution.
		const results = events.filter((event) => event.type === "tool_result");
		expect(results).toHaveLength(2);
		expect(results.every((event) => event.isError && event.durationMs === 0)).toBe(true);
		expect(events.at(-1)).toEqual({ type: "error", message: "Aborted" });
	});

	test("a normal eager-eligible tool still executes during streaming", async () => {
		scenario = "normal";
		executionOrder.length = 0;

		const events = await runUntilAbortMidStream();

		// Baseline: a non-reflection tool IS eager-executed, so its execute() starts during
		// streaming even though the stream is aborted mid-flight. This guards against the fix
		// over-broadly disabling eager execution for ordinary tools.
		expect(executionOrder).toEqual([NORMAL_TOOL_NAME]);
		expect(events.some((event) => event.type === "assistant_message")).toBe(false);
		expect(events.filter((event) => event.type === "tool_result")).toMatchObject([
			{ toolUseId: "tu_decision", output: "normal:x", isError: false },
		]);
		expect(events.at(-1)).toEqual({ type: "error", message: "Aborted" });
	});
});
