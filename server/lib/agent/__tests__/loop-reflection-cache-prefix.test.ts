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
import { shouldPersistRawDump } from "../../api-request-tracker";
import { CacheDropDumpStore } from "../../cache-drop-dump-store";
import { settings } from "../../settings";
import type { ProviderAdapter } from "../provider";
import { toolRegistry } from "../tool-registry";
import { dangerCancelTool, dangerConfirmTool } from "../tools/danger-reflection";
import type { AgentConfig } from "../types";

const DECISION_TOOL_NAME = "CachePrefixDecisionTool";
const SYSTEM_PROMPT = "PARENT_SYSTEM_PROMPT";
let emittedTool = DECISION_TOOL_NAME;
let decisions = 0;

/** Requests the fake provider saw, in order. */
let captured: Array<{
	history: unknown[];
	content: string;
	tools: unknown[];
	parentTools?: unknown[];
	allowedTools?: readonly string[];
	identity: unknown;
}> = [];
const PARENT_TOOL_NAME = "CachePrefixParentTool";
let parentGateTools: unknown[] | undefined;

const testProvider: ProviderAdapter = {
	formatTools: (tools) =>
		tools.map((t) => ({
			name: t.name,
			description: t.description,
			input_schema: t.rawJsonSchema ?? z.toJSONSchema(t.parameters),
		})),
	// Mirrors the real providers: prepend a marker entry representing the system prompt.
	injectSystemPrompt: (history, systemPrompt) => {
		(history as unknown[]).unshift({ role: "system", content: systemPrompt });
	},
	buildHistory: async () => ({ history: [], trailingToolResults: [] }),
	async *chat(params) {
		const parentTurn = parentGateTools && params.content === "parent turn";
		params.onRequestStart?.(parentTurn ? { codexTools: parentGateTools } : undefined);
		if (parentTurn) {
			// The provider may mutate shared definitions after its concrete request was captured.
			(parentGateTools?.[0] as { parameters: { description: string } }).parameters.description =
				"changed after request capture";
		}
		captured.push({
			history: structuredClone(params.history),
			content: params.content,
			tools: structuredClone(params.tools),
			parentTools: structuredClone(params.reflectionParentTools),
			allowedTools: params.reflectionAllowedTools,
			identity: {
				conversationId: params.conversationId,
				stickySessionKey: params.stickySessionKey,
				metadata: params.metadata,
				model: params.model,
			},
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
			toolUses: [{ toolUseId: "tu_decide", name: emittedTool, input: { value: "ok" } }],
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
	execute: async () => {
		decisions++;
		return { output: "decided" };
	},
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
	test.each([
		false,
		true,
	])("cache-drop capture is independent of full dumping (enabled=%s)", async (enabled) => {
		const previous = {
			enabled: settings.agent.cacheDropDumpEnabled,
			full: settings.agent.requestDumpEnabled,
		};
		settings.agent.cacheDropDumpEnabled = enabled;
		settings.agent.requestDumpEnabled = false;
		const records = spyOn(CacheDropDumpStore.prototype, "record").mockResolvedValue(undefined);
		const collectors: boolean[] = [];
		const chat = spyOn(testProvider, "chat").mockImplementation(async function* (params) {
			collectors.push(!!params.requestDump);
			params.requestDump?.beginResponseAttempt({
				transport: "http",
				body: { history: params.history, content: params.content },
			});
			params.requestDump?.setResponseBodyText("actual response");
			params.onRequestStart?.();
			if (params.content === "normal diagnostic") {
				yield {
					text: "done",
					usage: { promptTokens: 10000, inputTokens: 10000, completionTokens: 1 },
				};
			} else {
				yield {
					toolUses: [
						{ toolUseId: "tu-diagnostic", name: DECISION_TOOL_NAME, input: { value: "ok" } },
					],
					usage: {
						promptTokens: 10000,
						inputTokens: 10000,
						completionTokens: 1,
						cachedInputTokens: 9000,
					},
				};
			}
		});
		try {
			for await (const _event of agentLoop(
				{ ...parentConfig(), maxTurns: 1 },
				"normal diagnostic",
				[],
			)) {
				/* drain */
			}
			await runReflectionLoop({
				parentConfig: parentConfig(),
				history: parentHistory(),
				prompt: "diagnostic reflection",
				reflectionLoop: {
					allowedTools: [DECISION_TOOL_NAME],
					context: { kind: "taskReflection", requestId: "diagnostic" },
				},
				label: "diagnostic",
			});
			expect(collectors).toEqual([enabled, enabled]);
			expect(records).toHaveBeenCalledTimes(enabled ? 2 : 0);
			if (enabled) {
				const normal = records.mock.calls[0][0];
				const reflection = records.mock.calls[1][0];
				expect(normal.kind).toBe("narrator");
				expect(normal.cachedInputTokens).toBeUndefined();
				expect(reflection.kind).toBe("reflection");
				expect(reflection.cachedInputTokens).toBe(9000);
				expect(reflection.narratorId).toBe(normal.narratorId);
				expect(reflection.conversationId).toBe(normal.conversationId);
				expect(shouldPersistRawDump({ rawDump: normal.dump })).toBe(false);
			}
		} finally {
			chat.mockRestore();
			records.mockRestore();
			settings.agent.cacheDropDumpEnabled = previous.enabled;
			settings.agent.requestDumpEnabled = previous.full;
		}
	});
	test("marks predeclared reflection definitions for the normal Codex execution ceiling", async () => {
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
			expect(declarations).toEqual(
				expect.arrayContaining([expect.objectContaining({ name: DECISION_TOOL_NAME })]),
			);
			expect(active).toEqual(
				expect.arrayContaining([expect.objectContaining({ name: DECISION_TOOL_NAME })]),
			);
		} finally {
			chat.mockRestore();
		}
	});
	test("ordinary loops cannot execute reflection decisions", async () => {
		const before = decisions;
		for await (const _event of agentLoop({ ...parentConfig(), maxTurns: 1 }, "normal", [])) {
			/* drain */
		}
		expect(decisions).toBe(before);
	});

	test("reflection rejects ordinary tools even with an allowing parent handler", async () => {
		let executed = false;
		const name = "CacheForbiddenTool";
		toolRegistry.register({
			name,
			description: "ordinary",
			parameters: z.object({ value: z.string() }),
			execute: async () => {
				executed = true;
				return { output: "bad" };
			},
		});
		emittedTool = name;
		try {
			const result = await runReflectionLoop({
				parentConfig: parentConfig(),
				history: parentHistory(),
				prompt: "reflect",
				reflectionLoop: {
					allowedTools: [DECISION_TOOL_NAME],
					context: { kind: "taskReflection", requestId: "denial" },
				},
			});
			expect(executed).toBe(false);
			expect(result.toolResults.some((r) => r.toolName === name && r.isError)).toBe(true);
		} finally {
			emittedTool = DECISION_TOOL_NAME;
			toolRegistry.unregister(name);
		}
	});
	for (const planMode of [false, true]) {
		test(`inherits complete ordered parent tools (planMode=${planMode})`, async () => {
			const dynamicName = "CacheDynamicTool";
			let resolutions = 0;
			toolRegistry.register({
				name: dynamicName,
				description: (config) => `${config.systemPrompt}:${++resolutions}`,
				getRawJsonSchema: (config) => ({
					type: "object",
					description: config.systemPrompt,
					properties: { value: { const: resolutions } },
				}),
				parameters: z.object({}),
				execute: async () => {
					throw new Error("Must not execute");
				},
			});
			try {
				captured = [];
				const characters: number[] = [];
				const config = {
					...parentConfig(),
					onToolsCharacters: (chars: number) => {
						characters.push(chars);
					},
					maxTurns: 1,
					planMode,
					toolFilter: (tool: { name: string }) => tool.name === dynamicName,
				};
				for await (const _event of agentLoop(config, "parent", [])) {
					/* drain */
				}
				const parentTools = captured[0].tools;
				const parentCharacters = [...characters];
				expect(parentCharacters).toHaveLength(1);
				expect(parentCharacters[0]).toBeGreaterThan(0);
				expect(parentTools.map((t) => (t as { name: string }).name)).toContain(DECISION_TOOL_NAME);
				await runReflectionLoop({
					parentConfig: config,
					history: parentHistory(),
					prompt: "reflect",
					reflectionLoop: {
						allowedTools: [DECISION_TOOL_NAME],
						context: { kind: "taskReflection", requestId: "snapshot" },
					},
				});
				expect(JSON.stringify(captured[1].tools)).toBe(JSON.stringify(parentTools));
				// Auxiliary reflection must not replace the parent's context accounting.
				expect(characters).toEqual(parentCharacters);
				expect(captured[1].identity).toEqual(captured[0].identity);
				expect(resolutions).toBe(1);
			} finally {
				toolRegistry.unregister(dynamicName);
			}
		});
	}

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
			expect(captured[0].tools).toEqual(
				expect.arrayContaining([expect.objectContaining({ name: DECISION_TOOL_NAME })]),
			);
			expect(captured[0].allowedTools).toEqual([DECISION_TOOL_NAME]);
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
			expect(captured[1].tools).toEqual(captured[0].tools);
			expect(captured[1].allowedTools).toEqual([dangerConfirmTool.name, dangerCancelTool.name]);
			expect(parentExecutions).toBe(0);
			expect(cancellations).toBe(1);
		} finally {
			parentGateTools = undefined;
			for (const tool of tools) toolRegistry.unregister(tool.name);
			for (const tool of originals) if (tool) toolRegistry.register(tool);
		}
	});
});
