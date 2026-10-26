import { afterAll, beforeEach, describe, expect, mock, test } from "bun:test";
import { z } from "zod/v4";
import type { ProviderAdapter } from "../provider";
import { toolRegistry } from "../tool-registry";
import type { AgentConfig, AgentEvent } from "../types";

// These ordinary tools can ask permission or start a nested provider request. They
// must wait for the parent's WebSocket session to leave its provider finally block.
const PROVIDER_SESSION_TOOLS = ["Bash", "Write", "Edit", "StructSed"] as const;
const KNOWLEDGE_TOOLS = ["KnowledgeCreate", "KnowledgeEdit", "KnowledgeReview", "KnowledgeAdmin"];
const EAGER_READ_TOOL = "Read";
const sessionToolNames = [...PROVIDER_SESSION_TOOLS, EAGER_READ_TOOL];
let scenario: "knowledge" | "session_tool" = "session_tool";
let knowledgeToolName = KNOWLEDGE_TOOLS[0];
let knowledgeChunks = false;
let sessionToolName: (typeof PROVIDER_SESSION_TOOLS)[number] | typeof EAGER_READ_TOOL = "Bash";
let sessionToolInputOverride: Record<string, unknown> | undefined;
let requests = 0;
let streamReleased = false;
let onSessionToolStarted: (() => void) | undefined;
const executionOrder: string[] = [];
const permissionAfterRelease: boolean[] = [];
const executionAfterRelease: boolean[] = [];

const testProvider: ProviderAdapter = {
	formatTools: (tools) => tools.map(({ name, description }) => ({ name, description })),
	buildHistory: async () => ({ history: [] }),
	injectSystemPrompt: () => {},
	async *chat(params) {
		params.onRequestStart?.();
		if (requests++ > 0) {
			yield { text: "done" };
			return;
		}
		try {
			if (scenario === "knowledge") {
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
				await new Promise<void>((resolve) => setImmediate(resolve));
				if (params.signal.aborted) throw new Error("Aborted");
				return;
			}
			const sessionToolStarted = new Promise<void>((resolve) => {
				onSessionToolStarted = resolve;
			});
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
			if (sessionToolName === EAGER_READ_TOOL) await sessionToolStarted;
			else await new Promise<void>((resolve) => setImmediate(resolve));
		} finally {
			streamReleased = true;
		}
	},
	generate: async () => "",
	generateWithMeta: async () => ({ text: "" }),
	generateWithHistory: async () => "",
};

const realProviderModule = { ...(await import("../provider")) };
mock.module("../provider", () => ({
	...realProviderModule,
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

const originalKnowledgeTools = KNOWLEDGE_TOOLS.map((name) => toolRegistry.get(name));
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
			executionAfterRelease.push(streamReleased);
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
	mock.restore();
});

beforeEach(() => {
	scenario = "session_tool";
	sessionToolName = "Bash";
	sessionToolInputOverride = undefined;
	requests = 0;
	streamReleased = false;
	onSessionToolStarted = undefined;
	executionOrder.length = 0;
	permissionAfterRelease.length = 0;
	executionAfterRelease.length = 0;
});

function makeConfig(signal: AbortSignal): AgentConfig {
	return {
		narratorId: "n-reflection-session-safety",
		conversationId: "conv-reflection-session-safety",
		provider: "test",
		model: "test:model",
		cwd: "/tmp",
		signal,
		permissionHandler: async () => {
			permissionAfterRelease.push(streamReleased);
			return { behavior: "allow" };
		},
	};
}

async function run(abortMidStream = false): Promise<AgentEvent[]> {
	const ac = new AbortController();
	const events: AgentEvent[] = [];
	for await (const event of agentLoop(makeConfig(ac.signal), "run tool", { rows: [] })) {
		events.push(event);
		if (abortMidStream && event.type === "stream_text") ac.abort();
		if (!abortMidStream && event.type === "error") throw new Error(event.message);
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
					const events = await run(abort);

					expect(streamReleased).toBe(true);
					expect(executionOrder).toEqual(abort ? [] : [name]);
					expect(executionAfterRelease).toEqual(abort ? [] : [true]);
					if (abort) {
						expect(permissionAfterRelease).toEqual([]);
						expect(events.at(-1)).toEqual({ type: "error", message: "Aborted" });
					} else {
						expect(permissionAfterRelease.length).toBeGreaterThan(0);
						expect(permissionAfterRelease.every(Boolean)).toBe(true);
					}
				});
			}
		}
	}
});

describe("provider-session tools wait for the parent stream release", () => {
	for (const name of PROVIDER_SESSION_TOOLS) {
		test(`${name} permission and execution start after provider finally`, async () => {
			sessionToolName = name;
			await run();

			expect(streamReleased).toBe(true);
			expect(executionOrder).toEqual([name]);
			expect(executionAfterRelease).toEqual([true]);
			expect(permissionAfterRelease.length).toBeGreaterThan(0);
			expect(permissionAfterRelease.every(Boolean)).toBe(true);
		});
	}

	test("spec task Write remains post-stream", async () => {
		sessionToolName = "Write";
		sessionToolInputOverride = { file_path: "spec://tasks.json", content: '{"tasks":[]}' };
		await run();

		expect(executionOrder).toEqual(["Write"]);
		expect(executionAfterRelease).toEqual([true]);
		expect(permissionAfterRelease.length).toBeGreaterThan(0);
		expect(permissionAfterRelease.every(Boolean)).toBe(true);
	});

	test("ordinary Read still executes while the parent stream is open", async () => {
		sessionToolName = EAGER_READ_TOOL;
		await run();

		expect(streamReleased).toBe(true);
		expect(executionOrder).toEqual([EAGER_READ_TOOL]);
		expect(executionAfterRelease).toEqual([false]);
	});
});
