import { afterEach, describe, expect, test } from "bun:test";
import { z } from "zod/v4";
import type { AnthropicProviderConfig, OpenAIProviderConfig } from "../../settings";
import { AnthropicProvider } from "../anthropic-provider";
import { OpenAIProvider } from "../openai-provider";
import type { ChatParams, DbMessage, ProviderAdapter } from "../provider";
import {
	DEFERRED_TOOL_PLACEHOLDER,
	defaultToolLoadingModes,
	deriveToolLoadingState,
	formatRequestTools,
	projectToolLoadingHistory,
	resolveToolLoadingStrategy,
	type ToolLoadingState,
	type ToolLoadingStrategy,
	toolLoadingChangeOf,
} from "../tool-loading";
import type { ResolvedToolDefinition } from "../types";

function tool(name: string): ResolvedToolDefinition {
	return {
		name,
		description: `${name} tool`,
		parameters: z.object({ value: z.string() }),
		execute: async () => ({ output: "", isError: false }),
	} as unknown as ResolvedToolDefinition;
}

const READ = tool("Read");
const EVAL = tool("Eval");
const TERMINAL = tool("Terminal");
const CANDIDATES = [READ, EVAL, TERMINAL];
const MANAGED = new Set(["Eval", "Terminal"]);
const resolveDefinition = (name: string) => CANDIDATES.find((t) => t.name === name) ?? null;

let rowId = 0;
function row(role: DbMessage["role"], text: string, extra: Partial<DbMessage> = {}): DbMessage {
	rowId++;
	return {
		id: `row-${rowId}`,
		role,
		contentJson: [{ type: "text", text }],
		contentText: text,
		parentToolUseId: null,
		messageUuid: null,
		...extra,
	};
}

function notice(kind: "tool_loaded" | "tool_unloaded", toolNames: string[]): DbMessage {
	const text = `[${kind} ${toolNames.join(",")}]`;
	return row("user", text, {
		contentJson: [{ type: kind, toolName: toolNames[0], toolNames, text }],
	});
}

describe("tool loading strategy resolution", () => {
	test("name defaults honour the documented version floors", () => {
		expect(defaultToolLoadingModes("claude-opus-5-5")).toEqual(["anthropic_tool_addition"]);
		expect(defaultToolLoadingModes("claude-opus-4-8")).toEqual(["anthropic_tool_addition"]);
		expect(defaultToolLoadingModes("claude-opus-4-7")).toEqual([]);
		expect(defaultToolLoadingModes("claude-sonnet-5-5")).toEqual(["anthropic_tool_addition"]);
		expect(defaultToolLoadingModes("claude-sonnet-5")).toEqual([]);
		expect(defaultToolLoadingModes("claude-3-5-sonnet-20241022")).toEqual([]);
		expect(defaultToolLoadingModes("claude-fable-5")).toEqual(["anthropic_tool_addition"]);
		// An id without a parseable version stays enabled.
		expect(defaultToolLoadingModes("claude-custom")).toEqual(["anthropic_tool_addition"]);

		expect(defaultToolLoadingModes("gpt-5.4")).toEqual(["openai_additional_tools"]);
		expect(defaultToolLoadingModes("gpt-5.1-codex")).toEqual([]);
		expect(defaultToolLoadingModes("gpt-4o")).toEqual([]);
		expect(defaultToolLoadingModes("gpt-5.5", { codexBackend: true })).toEqual([]);
		expect(defaultToolLoadingModes("gpt-5.6-sol", { codexBackend: true })).toEqual([
			"openai_additional_tools",
		]);
		expect(defaultToolLoadingModes("gpt-6-astra", { codexBackend: true })).toEqual([
			"openai_additional_tools",
		]);

		// Open-weights gpt-oss is served by third parties and falls to the generic default.
		expect(defaultToolLoadingModes("gpt-oss-120b")).toEqual(["custom_injection"]);

		expect(defaultToolLoadingModes("deepseek-v4-pro")).toEqual(["anthropic_deferred_tail"]);
		expect(defaultToolLoadingModes("anthropic:glm-5.1")).toEqual(["custom_injection"]);
	});

	test("a mode is only used on the protocol it targets", () => {
		const anthropic = { protocol: "anthropic-messages" as const };
		const completions = { protocol: "completions-compatible" as const };
		expect(resolveToolLoadingStrategy("claude-opus-5-5", anthropic)).toBe(
			"anthropic_tool_addition",
		);
		expect(resolveToolLoadingStrategy("claude-opus-5-5", completions)).toBe("replace_tools");
		expect(resolveToolLoadingStrategy("glm-5.1", completions)).toBe("custom_injection");
		expect(resolveToolLoadingStrategy("glm-5.1", undefined)).toBe("replace_tools");
		// The gateway's routed name decides the defaults.
		expect(
			resolveToolLoadingStrategy("nug-alias", { protocol: "openai-responses", model: "gpt-5.5" }),
		).toBe("openai_additional_tools");
	});

	test("declared card modes replace the defaults; an empty list disables", () => {
		const anthropic = { protocol: "anthropic-messages" as const };
		expect(resolveToolLoadingStrategy("claude-opus-5-5", anthropic, [])).toBe("replace_tools");
		expect(
			resolveToolLoadingStrategy("claude-opus-5-5", anthropic, [
				"openai_additional_tools",
				"bogus",
				"custom_injection",
			]),
		).toBe("custom_injection");
		expect(resolveToolLoadingStrategy("claude-opus-5-5", anthropic, null)).toBe(
			"anthropic_tool_addition",
		);
	});
});

describe("tool loading state", () => {
	test("tools loaded without a notice row belong to the base set", () => {
		const state = deriveToolLoadingState(
			[row("user", "hi"), notice("tool_loaded", ["Eval"])],
			MANAGED,
			["Eval", "Terminal"],
		);
		expect([...state.base]).toEqual(["Terminal"]);
		expect(state.loaded).toEqual(["Eval"]);
		expect([...state.current].sort()).toEqual(["Eval", "Terminal"]);
	});

	test("an unload inside the history keeps the tool in the base set", () => {
		const state = deriveToolLoadingState(
			[notice("tool_unloaded", ["Terminal"]), notice("tool_loaded", ["Terminal"])],
			MANAGED,
			["Terminal"],
		);
		expect([...state.base]).toEqual(["Terminal"]);
		expect(state.loaded).toEqual(["Terminal"]);
	});

	test("legacy rows naming a routine id expand to its registry tools", () => {
		const legacy = row("user", "loaded", {
			contentJson: [{ type: "tool_loaded", toolName: "Eval", text: "loaded" }],
		});
		const state = deriveToolLoadingState([legacy], MANAGED, ["Eval"]);
		expect(state.loaded).toEqual(["Eval"]);
		expect(state.base.size).toBe(0);
	});
});

function format(tools: ResolvedToolDefinition[]): unknown[] {
	return tools.map((t) => ({ name: t.name }));
}

describe("request tool array", () => {
	const state: ToolLoadingState = deriveToolLoadingState(
		[notice("tool_loaded", ["Eval"])],
		MANAGED,
		["Eval", "Terminal"],
	);

	test("replace_tools follows the currently loaded set", () => {
		expect(formatRequestTools(format, CANDIDATES, state, "replace_tools")).toEqual([
			{ name: "Read" },
			{ name: "Eval" },
			{ name: "Terminal" },
		]);
	});

	test("additive modes keep only the base set", () => {
		for (const strategy of ["openai_additional_tools", "custom_injection"] as const) {
			expect(formatRequestTools(format, CANDIDATES, state, strategy)).toEqual([
				{ name: "Read" },
				{ name: "Terminal" },
			]);
		}
		expect(formatRequestTools(format, CANDIDATES, state, "anthropic_tool_addition")).toEqual([
			{ name: "Read" },
			{ name: "Terminal" },
			DEFERRED_TOOL_PLACEHOLDER,
		]);
	});

	test("the deferred tail appends loaded tools after the placeholder", () => {
		expect(formatRequestTools(format, CANDIDATES, state, "anthropic_deferred_tail")).toEqual([
			{ name: "Read" },
			{ name: "Terminal" },
			DEFERRED_TOOL_PLACEHOLDER,
			{ name: "Eval", defer_loading: true },
		]);
	});

	test("the array is identical before and after a load", () => {
		const before = deriveToolLoadingState([row("user", "hi")], MANAGED, []);
		const after = deriveToolLoadingState(
			[row("user", "hi"), notice("tool_loaded", ["Eval"])],
			MANAGED,
			["Eval"],
		);
		for (const strategy of [
			"anthropic_tool_addition",
			"openai_additional_tools",
			"custom_injection",
		] as const) {
			expect(formatRequestTools(format, CANDIDATES, after, strategy)).toEqual(
				formatRequestTools(format, CANDIDATES, before, strategy),
			);
		}
		// The tail only grows at its end.
		const tailBefore = formatRequestTools(format, CANDIDATES, before, "anthropic_deferred_tail");
		const tailAfter = formatRequestTools(format, CANDIDATES, after, "anthropic_deferred_tail");
		expect(tailAfter.slice(0, tailBefore.length)).toEqual(tailBefore);
	});
});

describe("history projection", () => {
	const rows = () => [
		notice("tool_loaded", ["Eval"]),
		notice("tool_unloaded", ["Eval"]),
		notice("tool_loaded", ["Eval"]),
	];
	const state = (messages: DbMessage[]) => deriveToolLoadingState(messages, MANAGED, ["Eval"]);

	function project(strategy: ToolLoadingStrategy) {
		const messages = rows();
		return projectToolLoadingHistory(messages, state(messages), strategy, resolveDefinition);
	}

	test("inline additions define every load and remove every unload", () => {
		const changes = project("anthropic_tool_addition").map(toolLoadingChangeOf);
		expect(changes[0]?.additions.map((a) => [a.name, !!a.definition])).toEqual([["Eval", true]]);
		expect(changes[1]?.removals).toEqual(["Eval"]);
		expect(changes[2]?.additions.map((a) => a.name)).toEqual(["Eval"]);
	});

	test("the deferred tail references tools by name and sends no removals", () => {
		const changes = project("anthropic_deferred_tail").map(toolLoadingChangeOf);
		expect(changes[0]?.additions).toEqual([{ name: "Eval" }]);
		expect(changes[1]).toBeUndefined();
		expect(changes[2]?.additions).toEqual([{ name: "Eval" }]);
	});

	test("a removal never names a base tool that no longer resolves", () => {
		const messages = [notice("tool_unloaded", ["Terminal"])];
		const loadingState = deriveToolLoadingState(messages, MANAGED, []);
		expect([...loadingState.base]).toEqual(["Terminal"]);
		const projected = projectToolLoadingHistory(
			messages,
			loadingState,
			"anthropic_tool_addition",
			(name) => (name === "Terminal" ? null : resolveDefinition(name)),
		);
		expect(toolLoadingChangeOf(projected[0])).toBeUndefined();
	});

	test("additional_tools declares a tool once per epoch", () => {
		const changes = project("openai_additional_tools").map(toolLoadingChangeOf);
		expect(changes[0]?.additions.map((a) => a.name)).toEqual(["Eval"]);
		expect(changes[1]).toBeUndefined();
		expect(changes[2]).toBeUndefined();
	});

	test("custom injection writes the schema into the first load notice", () => {
		const projected = project("custom_injection");
		expect(projected[0].contentText).toContain("<tool_definitions>");
		expect(projected[0].contentText).toContain('"name":"Eval"');
		expect(projected[1].contentText).not.toContain("<tool_definitions>");
		expect(projected[2].contentText).not.toContain("<tool_definitions>");
		expect((projected[0].contentJson as Array<{ text: string }>)[0].text).toBe(
			projected[0].contentText ?? "",
		);
	});

	test("replace_tools leaves rows untouched", () => {
		const messages = rows();
		const projected = projectToolLoadingHistory(
			messages,
			state(messages),
			"replace_tools",
			resolveDefinition,
		);
		expect(projected).toEqual(messages);
		expect(projected.map(toolLoadingChangeOf)).toEqual([undefined, undefined, undefined]);
	});
});

// === Wire shapes ===

const ORIGINAL_FETCH = globalThis.fetch;
let captured: Array<{ headers: Headers; body: Record<string, unknown> }> = [];

function captureFetch(): void {
	captured = [];
	globalThis.fetch = (async (_input, init) => {
		captured.push({
			headers: new Headers(init?.headers),
			body: JSON.parse(String(init?.body)) as Record<string, unknown>,
		});
		return new Response("upstream unavailable", { status: 500 });
	}) as typeof fetch;
}

afterEach(() => {
	globalThis.fetch = ORIGINAL_FETCH;
	captured = [];
});

async function send(adapter: ProviderAdapter, params: ChatParams) {
	const iterator = adapter.chat(params)[Symbol.asyncIterator]();
	try {
		await iterator.next();
	} catch {
		// The mock always fails after capturing the request.
	}
	return captured[captured.length - 1];
}

/**
 * Build one request the way the runtime does: derive state, project, build, format. The
 * history already ends with the persisted current input, so no separate content is sent.
 */
async function request(
	adapter: ProviderAdapter,
	model: string,
	strategy: ToolLoadingStrategy,
	messages: DbMessage[],
	current: string[],
) {
	const state = deriveToolLoadingState(messages, MANAGED, current);
	const projected = projectToolLoadingHistory(messages, state, strategy, resolveDefinition);
	const built = await adapter.buildHistory(projected, model);
	const tools = formatRequestTools(
		(selected) => adapter.formatTools(selected),
		CANDIDATES,
		state,
		strategy,
	);
	return send(adapter, {
		conversationId: "tool-loading",
		content: "",
		model,
		cwd: process.cwd(),
		history: built.history,
		tools,
		toolResults: [],
		toolLoadingStrategy: strategy,
		signal: new AbortController().signal,
		reasoningEffort: "none",
	} as ChatParams);
}

function stripCache<T>(value: T): T {
	return JSON.parse(
		JSON.stringify(value, (key, child) => (key === "cache_control" ? undefined : child)),
	) as T;
}

describe("anthropic_tool_addition wire", () => {
	const config: AnthropicProviderConfig = {
		id: "tool-loading-anthropic",
		name: "Tool loading",
		prefix: "tl_anthropic",
		apiKey: "sk-test",
		baseUrl: "https://example.invalid/v1",
		defaultModel: "claude-opus-5-5",
		officialApi: false,
	};

	test("a load keeps tools fixed and adds the tool in a system message", async () => {
		captureFetch();
		const provider = new AnthropicProvider(config);
		const model = "tl_anthropic:claude-opus-5-5";
		const first = row("user", "hello");
		const reply = row("assistant", "hi there");
		const loaded = notice("tool_loaded", ["Eval"]);
		const ask = row("user", "run it");

		const before = await request(
			provider,
			model,
			"anthropic_tool_addition",
			[first, reply, row("user", "anything else")],
			[],
		);
		const after = await request(
			provider,
			model,
			"anthropic_tool_addition",
			[first, reply, loaded, ask],
			["Eval"],
		);
		expect(after.body.tools).toEqual(before.body.tools);
		// Terminal is not loaded, and Eval arrives in the history instead.
		expect((after.body.tools as Array<{ name: string }>).map((t) => t.name)).toEqual([
			"Read",
			DEFERRED_TOOL_PLACEHOLDER.name,
		]);
		expect(after.headers.get("anthropic-beta")).toContain("inline-tools-2026-09-15");

		const messages = stripCache(after.body.messages) as Array<{ role: string; content: unknown }>;
		expect(messages.map((m) => m.role)).toEqual(["user", "assistant", "user", "system"]);
		expect(messages[3].content).toEqual([
			{
				type: "tool_addition",
				tool: {
					type: "tool_definition",
					definition: provider.formatTools([EVAL])[0],
				},
			},
		]);

		// The next request after the model answered replays the same prefix.
		const answer = row("assistant", "done");
		const next = await request(
			provider,
			model,
			"anthropic_tool_addition",
			[first, reply, loaded, ask, answer, row("user", "thanks")],
			["Eval"],
		);
		const nextMessages = stripCache(next.body.messages) as unknown[];
		expect(nextMessages.slice(0, messages.length)).toEqual(messages);
		expect(next.body.tools).toEqual(before.body.tools);
	});

	test("a removal is sent by reference", async () => {
		captureFetch();
		const provider = new AnthropicProvider(config);
		const sent = await request(
			provider,
			"tl_anthropic:claude-opus-5-5",
			"anthropic_tool_addition",
			[row("user", "a"), row("assistant", "b"), notice("tool_unloaded", ["Terminal"])],
			[],
		);
		const messages = stripCache(sent.body.messages) as Array<{ role: string; content: unknown }>;
		expect(messages.at(-1)).toEqual({
			role: "system",
			content: [{ type: "tool_removal", tool: { type: "tool_reference", name: "Terminal" } }],
		});
		// The unloaded tool stays declared within the epoch.
		expect((sent.body.tools as Array<{ name: string }>).map((t) => t.name)).toContain("Terminal");
	});
});

describe("anthropic_deferred_tail wire", () => {
	test("the loaded tool is a deferred tail entry named by reference", async () => {
		captureFetch();
		const provider = new AnthropicProvider({
			id: "tool-loading-deepseek",
			name: "Tool loading DeepSeek",
			prefix: "tl_deepseek",
			apiKey: "sk-test",
			baseUrl: "https://example.invalid/anthropic",
			defaultModel: "deepseek-v4-pro",
			officialApi: false,
		});
		const sent = await request(
			provider,
			"tl_deepseek:deepseek-v4-pro",
			"anthropic_deferred_tail",
			[row("user", "a"), row("assistant", "b"), notice("tool_loaded", ["Eval"]), row("user", "c")],
			["Eval"],
		);
		const tools = sent.body.tools as Array<{ name: string; defer_loading?: boolean }>;
		expect(tools.map((t) => [t.name, t.defer_loading ?? false])).toEqual([
			["Read", false],
			[DEFERRED_TOOL_PLACEHOLDER.name, true],
			["Eval", true],
		]);
		expect(sent.headers.get("anthropic-beta")).toContain(
			"mid-conversation-tool-changes-2026-07-01",
		);
		const messages = stripCache(sent.body.messages) as Array<{ role: string; content: unknown }>;
		expect(messages.at(-1)).toEqual({
			role: "system",
			content: [{ type: "tool_addition", tool: { type: "tool_reference", name: "Eval" } }],
		});
	});
});

describe("openai_additional_tools wire", () => {
	const config: OpenAIProviderConfig = {
		id: "tool-loading-openai",
		name: "Tool loading OpenAI",
		prefix: "tl_openai",
		apiKey: "sk-test",
		baseUrl: "https://example.invalid/v1",
		defaultModel: "gpt-5.5",
		apiMode: "responses",
	};

	test("the load becomes an additional_tools input item before its notice", async () => {
		captureFetch();
		const provider = new OpenAIProvider(config);
		const model = "tl_openai:gpt-5.5";
		const first = row("user", "hello");
		const reply = row("assistant", "hi there");
		const loaded = notice("tool_loaded", ["Eval"]);

		const before = await request(
			provider,
			model,
			"openai_additional_tools",
			[first, reply, row("user", "more")],
			[],
		);
		// The notice is the current turn here, so its item ends the history.
		const current = await request(
			provider,
			model,
			"openai_additional_tools",
			[first, reply, loaded],
			["Eval"],
		);
		const later = await request(
			provider,
			model,
			"openai_additional_tools",
			[first, reply, loaded, row("assistant", "ok"), row("user", "go")],
			["Eval"],
		);
		expect(current.body.tools).toEqual(before.body.tools);
		expect(later.body.tools).toEqual(before.body.tools);
		const item = {
			type: "additional_tools",
			role: "developer",
			tools: provider.formatTools([EVAL]),
		};
		const input = current.body.input as unknown[];
		expect(input.at(-2)).toEqual(item);
		expect(later.body.input as unknown[]).toEqual(expect.arrayContaining([item]));
		// Rebuilt later, the item and notice keep their place: a growing prefix.
		expect((later.body.input as unknown[]).slice(0, input.length)).toEqual(input);
		expect(String(later.body.instructions ?? "")).not.toContain("additional_tools");
	});
});
