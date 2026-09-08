/**
 * loop-reflection-cache-prefix.test.ts — a reflection loop must reuse the parent
 * conversation's cacheable prefix byte-for-byte.
 *
 * Prompt caching is an exact prefix match, so the reflection request has to start
 * with the same system prompt bytes as the parent turn it interrupts. The parent
 * `agentLoop` already injected the system prompt into the history array it hands
 * down, so the nested loop must NOT inject it a second time: a duplicate lands at
 * the very front of the request and shifts every following byte, forcing the whole
 * (often 100k+ token) prefix to be re-billed at full price instead of a cache read.
 */

import { afterAll, describe, expect, mock, spyOn, test } from "bun:test";
import { z } from "zod/v4";
import type { ProviderAdapter } from "../provider";
import { toolRegistry } from "../tool-registry";
import { dangerCancelTool, dangerConfirmTool } from "../tools/danger-reflection";
import type { AgentConfig } from "../types";

const DECISION_TOOL_NAME = "CachePrefixDecisionTool";
const SYSTEM_PROMPT = "PARENT_SYSTEM_PROMPT";

/** Requests the fake provider saw, in order. */
let captured: Array<{
	history: unknown[];
	content: string;
	tools: unknown[];
	parentTools?: unknown[];
}> = [];
const PARENT_TOOL_NAME = "CachePrefixParentTool";
let parentGateTools: unknown[] | undefined;

const testProvider: ProviderAdapter = {
	formatTools: (tools) => {
		if (parentGateTools && tools.some((tool) => tool.name === dangerCancelTool.name)) {
			// Dynamic schemas may be backed by shared objects. Do not mutate the saved prefix.
			(parentGateTools[0] as { parameters: { description: string } }).parameters.description =
				"changed while formatting reflection";
		}
		return tools.map((t) => ({ name: t.name }));
	},
	// Mirrors the real providers: prepend a marker entry representing the system prompt.
	injectSystemPrompt: (history, systemPrompt) => {
		(history as unknown[]).unshift({ role: "system", content: systemPrompt });
	},
	buildHistory: async () => ({ history: [], trailingToolResults: [] }),
	async *chat(params) {
		const parentTurn = parentGateTools && params.content === "parent turn";
		params.onRequestStart?.(parentTurn ? { codexTools: parentGateTools } : undefined);
		captured.push({
			history: structuredClone(params.history),
			content: params.content,
			tools: structuredClone(params.tools),
			parentTools: structuredClone(params.reflectionParentTools),
		});
		if (parentGateTools) {
			yield {
				toolUses: parentTurn
					? [{ toolUseId: "parent-call", name: PARENT_TOOL_NAME, input: {} }]
					: [
							{ toolUseId: "forbidden-call", name: PARENT_TOOL_NAME, input: {} },
							{ toolUseId: "cancel-call", name: dangerCancelTool.name, input: { reason: "test" } },
						],
			};
			return;
		}
		yield {
			toolUses: [{ toolUseId: "tu_decide", name: DECISION_TOOL_NAME, input: { value: "ok" } }],
		};
	},
	formatToolResult: (toolUseId, output, isError) => ({ toolUseId, output, isError }),
	pushUserTurn: () => {},
	pushAssistantTurn: () => {},
	generate: async () => "",
	generateWithMeta: async () => ({ text: "" }),
	generateWithHistory: async () => "",
};

// Match startup order: router registration reads the fully initialized core tools.
await import("@server/services/narrator-session");
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

const { agentLoop, runReflectionLoop } = await import("../loop");

toolRegistry.register({
	name: DECISION_TOOL_NAME,
	description: "Fake reflection decision tool",
	reflectionOnly: true,
	parameters: z.object({ value: z.string() }),
	execute: async () => ({ output: "decided" }),
});

afterAll(() => {
	mock.module("../provider", () => realProviderModule);
	toolRegistry.unregister(DECISION_TOOL_NAME);
	mock.restore();
});

function parentConfig(): AgentConfig {
	return {
		narratorId: "n-cache-prefix",
		conversationId: "conv-cache-prefix",
		model: "test:model",
		provider: "test",
		cwd: "/tmp",
		systemPrompt: SYSTEM_PROMPT,
		signal: new AbortController().signal,
		permissionHandler: async () => ({ behavior: "allow" }),
	};
}

/** The history shape a parent agentLoop hands down: system prompt already injected. */
function parentHistory(): unknown[] {
	return [
		{ role: "system", content: SYSTEM_PROMPT },
		{ role: "user", content: "first user turn" },
		{ role: "assistant", content: "some work" },
	];
}

async function runReflection(
	history: unknown[],
	injectParentSystemPrompt?: boolean,
	lastCodexTools?: AgentConfig["lastCodexTools"],
): Promise<unknown[]> {
	captured = [];
	await runReflectionLoop({
		parentConfig: { ...parentConfig(), lastCodexTools },
		history,
		prompt: "REFLECTION_PROMPT",
		reflectionLoop: {
			allowedTools: [DECISION_TOOL_NAME],
			context: { kind: "taskReflection", requestId: "req-cache" },
		},
		label: "Cache prefix test loop",
		...(injectParentSystemPrompt === undefined ? {} : { injectParentSystemPrompt }),
	});
	expect(captured.length).toBe(1);
	return captured[0].history;
}

function systemEntries(history: unknown[]): string[] {
	return (history as Array<{ role?: string; content?: unknown }>)
		.filter((m) => m?.role === "system")
		.map((m) => String(m.content));
}

describe("reflection loop cacheable prefix", () => {
	test("predeclares reflection definitions separately from active tools", async () => {
		let declarations: unknown[] | undefined;
		let active: unknown;
		const chat = spyOn(testProvider, "chat").mockImplementation(async function* (params) {
			declarations = params.reflectionTools;
			active = params.tools;
			yield { text: "done" };
		});
		try {
			for await (const _event of agentLoop({ ...parentConfig(), maxTurns: 1 }, "normal", [])) {
				/* drain */
			}
			expect(declarations).toContainEqual({ name: DECISION_TOOL_NAME });
			expect(active).not.toContainEqual({ name: DECISION_TOOL_NAME });
		} finally {
			chat.mockRestore();
		}
	});
	test("does not duplicate the system prompt already present in the parent history", async () => {
		const history = await runReflection(parentHistory());

		// Exactly one copy — a second copy is the cache-prefix bug.
		expect(systemEntries(history)).toEqual([SYSTEM_PROMPT]);
	});

	test("keeps the reflection prefix byte-identical to the parent prefix", async () => {
		const parent = parentHistory();
		const history = await runReflection(parent);

		// The parent turn's prefix must survive verbatim, in order, at the front.
		expect(history.slice(0, parent.length)).toEqual(parent);
	});

	test("the reflection prompt is the current turn, not part of the prefix", async () => {
		const parent = parentHistory();
		await runReflection(parent);

		// The gate's question rides in `content`, after the shared prefix, so it
		// cannot shift the cached bytes.
		expect(captured[0].content).toBe("REFLECTION_PROMPT");
	});

	test("still injects when the caller supplies a raw history without one", async () => {
		// A caller building its own history has no injected prompt to inherit.
		const history = await runReflection([], true);

		expect(systemEntries(history)).toEqual([SYSTEM_PROMPT]);
	});

	test("an empty inherited history injects the system prompt by default", async () => {
		const history = await runReflection([]);

		// Nothing was inherited, so suppressing injection would drop the prompt entirely.
		expect(systemEntries(history)).toEqual([SYSTEM_PROMPT]);
	});

	test("only reuses a snapshot from the same provider and model", async () => {
		for (const [provider, model, matches] of [
			["test", "test:model", true],
			["other", "test:model", false],
			["test", "test:other", false],
		] as const) {
			const tools = [{ type: "function", name: PARENT_TOOL_NAME }];
			await runReflection(parentHistory(), undefined, { provider, model, tools });
			expect(captured[0].parentTools).toEqual(matches ? tools : []);
			expect(captured[0].tools).toEqual([{ name: DECISION_TOOL_NAME }]);
		}
	});

	test("inherits actual parent wire tools without granting execution of parent tools", async () => {
		let parentExecutions = 0;
		let cancellations = 0;
		const tools = [
			{
				name: PARENT_TOOL_NAME,
				description: "Parent operation",
				parameters: z.object({}),
				execute: async () => {
					parentExecutions++;
					return { output: "must not run" };
				},
			},
			{ ...dangerConfirmTool, execute: async () => ({ output: "confirm" }) },
			{
				...dangerCancelTool,
				execute: async () => {
					cancellations++;
					return { output: "cancel" };
				},
			},
		];
		const originals = tools.map((tool) => toolRegistry.get(tool.name));
		for (const tool of tools) toolRegistry.register(tool);
		try {
			captured = [];
			// The provider's final list deliberately differs from formatTools().
			parentGateTools = [
				{
					type: "function",
					name: PARENT_TOOL_NAME,
					description: "wire snapshot",
					parameters: { type: "object", description: "original schema" },
				},
				{ type: "web_search" },
			];
			const expectedTools = structuredClone(parentGateTools);
			const config = parentConfig();
			config.maxTurns = 1;
			config.toolFilter = (tool) => tool.name === PARENT_TOOL_NAME;
			const history: unknown[] = [];
			config.permissionHandler = async () => {
				const observed = await runReflectionLoop({
					parentConfig: config,
					history,
					prompt: "reflect",
					reflectionLoop: {
						allowedTools: [dangerConfirmTool.name, dangerCancelTool.name],
						context: { kind: "dangerReflection", requestId: "req-real-tools" },
					},
				});
				expect(observed.toolResults.some((result) => result.isError)).toBe(true);
				return { behavior: "deny", message: "test cancelled" };
			};
			for await (const _event of agentLoop(config, "parent turn", history)) {
				/* drain */
			}
			expect(captured).toHaveLength(2);
			expect(captured[0].parentTools).toBeUndefined();
			expect(captured[1].parentTools).toEqual(expectedTools);
			expect(config.lastCodexTools?.tools).toEqual(expectedTools);
			expect(parentGateTools).not.toEqual(expectedTools);
			expect(captured[1].tools).toEqual([
				{ name: dangerConfirmTool.name },
				{ name: dangerCancelTool.name },
			]);
			expect(parentExecutions).toBe(0);
			expect(cancellations).toBe(1);
		} finally {
			parentGateTools = undefined;
			for (const tool of tools) toolRegistry.unregister(tool.name);
			for (const tool of originals) if (tool) toolRegistry.register(tool);
		}
	});
});
