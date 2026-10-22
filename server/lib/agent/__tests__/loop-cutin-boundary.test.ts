import { afterAll, describe, expect, mock, spyOn, test } from "bun:test";
import { z } from "zod/v4";
import { AnthropicProvider } from "../anthropic-provider";
import { OpenAIProvider } from "../openai-provider";
import type {
	BuiltHistory,
	ChatParams,
	DbMessage,
	ParsedStreamEvent,
	ProviderAdapter,
} from "../provider";
import { toolRegistry } from "../tool-registry";
import { uniquifyDbMessageToolUseIds } from "../tool-use-id-dedup";
import { type AgentConfig, type AgentEvent, ApiError } from "../types";

/**
 * A cut-in ("插队 · 下次请求时发送") queued while a tool is already running must be
 * answered by the request that follows THAT tool call.
 *
 * The regression this pins: `shouldStop` was only consulted in the post-stream tool
 * phase, but the primary narrator eagerly STARTS every tool of the turn as its input
 * finishes streaming. Eagerly started tools are awaited and delivered by the execution
 * phase (never skipped), so every remaining tool of the turn still ran and the queued
 * message effectively waited for the request after next — reported as "下次请求时发送"
 * behaving like "下下次请求时发送".
 *
 * Subagents were unaffected because they pass `deferEagerToolsForSafeStop`, which is
 * why this went unnoticed: the same code with one flag flipped behaved correctly.
 */

const SLOW = "CutInSlowTool";
const FAST = "CutInFastTool";

const executed: string[] = [];
let releaseSlow: (() => void) | undefined;
/** Invoked mid-stream, right after the slow tool's input finishes arriving. */
let queueCutIn: (() => void) | undefined;

/**
 * Let the slow tool finish, waiting for it to actually start first.
 *
 * When it starts depends on the configuration under test: eagerly during streaming,
 * or in the post-stream tool phase under `deferEagerToolsForSafeStop`. Releasing
 * blindly would no-op in the deferred case and hang the turn.
 */
async function releaseSlowOnceStarted(): Promise<void> {
	for (let i = 0; i < 2000 && !releaseSlow; i++) {
		await new Promise((resolve) => setTimeout(resolve, 1));
	}
	releaseSlow?.();
}

const testProvider: ProviderAdapter = {
	formatTools: (tools) => tools,
	buildHistory: async () => ({ history: [], trailingToolResults: [] }),
	injectSystemPrompt: () => {},
	async *chat(params) {
		params.onRequestStart?.();
		// One long-running tool, then three more streamed in the SAME turn — the shape
		// of a model that keeps working while the user types a cut-in.
		yield {
			toolUseChunk: {
				toolUseId: "tu_slow",
				name: SLOW,
				input: JSON.stringify({ value: "in-flight" }),
				outputIndex: 0,
			},
		};
		yield { toolUseChunk: { toolUseId: "tu_slow", stop: true } };

		queueCutIn?.();
		await new Promise((resolve) => setTimeout(resolve, 10));

		let outputIndex = 1;
		for (const name of ["b", "c", "d"]) {
			yield {
				toolUseChunk: {
					toolUseId: `tu_${name}`,
					name: FAST,
					input: JSON.stringify({ value: name }),
					outputIndex: outputIndex++,
				},
			};
			yield { toolUseChunk: { toolUseId: `tu_${name}`, stop: true } };
			await new Promise((resolve) => setTimeout(resolve, 5));
		}
	},
	generate: async () => "",
	generateWithMeta: async () => ({ text: "" }),
	generateWithHistory: async () => "",
};

const realProviderModule = { ...(await import("../provider")) };
mock.module("../provider", () => ({
	getProvider: () => testProvider,
	resolveProviderAndModel: (model: string) => ({
		requestedProvider: "test",
		requestedModel: model,
		provider: "test",
		adapter: testProvider,
		model,
	}),
}));

const { agentLoop } = await import("../loop");

toolRegistry.register({
	name: SLOW,
	description: "Long-running tool for cut-in boundary tests",
	parameters: z.object({ value: z.string() }),
	execute: async (args) => {
		executed.push(`slow:${args.value}`);
		await new Promise<void>((resolve) => {
			releaseSlow = resolve;
		});
		return { output: `done:${args.value}` };
	},
});

toolRegistry.register({
	name: FAST,
	description: "Fast tool for cut-in boundary tests",
	parameters: z.object({ value: z.string() }),
	execute: async (args) => {
		executed.push(`fast:${args.value}`);
		return { output: `done:${args.value}` };
	},
});

afterAll(() => {
	releaseSlow?.();
	mock.module("../provider", () => realProviderModule);
	toolRegistry.unregister(SLOW);
	toolRegistry.unregister(FAST);
	mock.restore();
});

/**
 * Run one turn, queueing a cut-in while the first tool is still executing.
 *
 * `shouldStop` mirrors the host's one-shot contract (`evaluateSoftStopRequest`):
 * the request is consumed by whoever observes it first, so a loop that asks twice
 * and keeps only the second answer would see `false` and carry on.
 */
async function runTurnWithCutIn(overrides: Partial<AgentConfig> = {}): Promise<{
	executed: string[];
	skipped: string[];
	softStopAsks: number;
}> {
	executed.length = 0;
	releaseSlow = undefined;
	let queued = false;
	let softStopAsks = 0;

	queueCutIn = () => {
		queued = true;
		// The tool that was running when the cut-in arrived still finishes.
		void releaseSlowOnceStarted();
	};

	const events: AgentEvent[] = [];
	for await (const event of agentLoop(
		{
			narratorId: "n-cutin",
			conversationId: "conv-cutin",
			model: "test:model",
			provider: "test",
			cwd: "/tmp",
			signal: new AbortController().signal,
			permissionHandler: async () => ({ behavior: "allow" }),
			shouldStop: () => {
				softStopAsks++;
				if (!queued) return false;
				queued = false; // one-shot, like the real flag
				return true;
			},
			...overrides,
		} satisfies AgentConfig,
		"do a long task",
		{ rows: [] },
	)) {
		events.push(event);
	}

	return {
		executed: [...executed],
		skipped: events
			.filter((e) => e.type === "tool_result" && e.metadata?.skippedForSoftStop === true)
			.map((e) => (e as Extract<AgentEvent, { type: "tool_result" }>).toolUseId),
		softStopAsks,
	};
}

describe("model input adoption boundary", () => {
	async function runInput(
		overrides: Partial<AgentConfig>,
		history: unknown[] = [],
		initialToolResults?: unknown[],
	) {
		for await (const _event of agentLoop(
			{
				narratorId: "n-consumption",
				conversationId: "conv-consumption",
				model: "test:model",
				provider: "test",
				cwd: ".",
				signal: new AbortController().signal,
				permissionHandler: async () => ({ behavior: "allow" }),
				...overrides,
			},
			"initial",
			history,
			initialToolResults,
		)) {
			/* Drain to the real provider-input boundary. */
		}
	}

	const historyFormats = ["responses", "completions", "anthropic"] as const;
	const completionNotice =
		"[System] Background agent history-probe completed; no task agents remain.";
	type HistoryRequest = Pick<ChatParams, "content" | "history" | "toolResults">;

	function historyProvider(format: (typeof historyFormats)[number]): ProviderAdapter {
		const config = {
			id: "history-probe",
			name: "History probe",
			prefix: "test",
			apiKey: "unused",
			baseUrl: "https://example.invalid/v1",
			defaultModel: "history-probe-model",
		};
		return format === "anthropic"
			? new AnthropicProvider(config)
			: new OpenAIProvider({ ...config, apiMode: format });
	}

	function modelStrings(value: unknown): string[] {
		if (typeof value === "string") return [value];
		if (Array.isArray(value)) return value.flatMap(modelStrings);
		if (value && typeof value === "object") return Object.values(value).flatMap(modelStrings);
		return [];
	}

	function occurrences(value: unknown, text: string): number {
		return modelStrings(value).reduce((count, part) => count + part.split(text).length - 1, 0);
	}

	function historyTool(id: string, value: string): ParsedStreamEvent {
		return { toolUses: [{ toolUseId: id, name: FAST, input: { value } }] };
	}

	async function runHistoryInput(
		format: (typeof historyFormats)[number],
		script: Array<ParsedStreamEvent[] | Error>,
		overrides: Partial<AgentConfig> = {},
		initial?: BuiltHistory,
	): Promise<HistoryRequest[]> {
		const provider = historyProvider(format);
		const requests: HistoryRequest[] = [];
		// Keep real provider history/result projections. Only upstream output is scripted;
		// the suite's default no-op pushUserTurn would hide this cross-request regression.
		const userTurn = spyOn(testProvider, "pushUserTurn").mockImplementation(
			provider.pushUserTurn.bind(provider),
		);
		const assistantTurn = spyOn(testProvider, "pushAssistantTurn").mockImplementation(
			provider.pushAssistantTurn.bind(provider),
		);
		const toolResult = spyOn(testProvider, "formatToolResult").mockImplementation(
			provider.formatToolResult.bind(provider),
		);
		const chat = spyOn(testProvider, "chat").mockImplementation(async function* (params) {
			params.onRequestStart?.();
			const requestIndex = requests.length;
			requests.push(
				structuredClone({
					content: params.content,
					history: params.history,
					toolResults: params.toolResults ?? [],
				}),
			);
			const response = script[requestIndex];
			if (!response) throw new Error("Unexpected history probe request");
			if (response instanceof Error) throw response;
			for (const event of response) yield event;
		});
		try {
			await runInput(
				{ maxTurns: 8, maxTransientRetries: 0, ...overrides },
				initial?.history,
				initial?.trailingToolResults,
			);
			return requests;
		} finally {
			chat.mockRestore();
			toolResult.mockRestore();
			assistantTurn.mockRestore();
			userTurn.mockRestore();
		}
	}

	function completedHistoryTool(id: string, toolUseId: string, value: string): DbMessage {
		return {
			id,
			role: "assistant",
			contentJson: [{ type: "tool_use", id: toolUseId, name: FAST, input: { value } }],
			contentText: null,
			parentToolUseId: null,
			messageUuid: null,
			toolCalls: [
				{
					toolUseId,
					toolName: FAST,
					inputJson: { value },
					outputJson: `done:${value}`,
					status: "success",
				},
			],
		};
	}

	function toolPairIds(request: Pick<HistoryRequest, "history" | "toolResults">) {
		const calls: string[] = [];
		const results: string[] = [];
		const visit = (value: unknown): void => {
			if (Array.isArray(value)) {
				for (const entry of value) visit(entry);
				return;
			}
			if (!value || typeof value !== "object") return;
			const block = value as Record<string, unknown>;
			if (block.type === "function_call" && typeof block.call_id === "string") {
				calls.push(block.call_id);
			} else if (
				(block.type === "tool_use" || block.type === "function") &&
				typeof block.id === "string"
			) {
				calls.push(block.id);
			}
			if (typeof block.tool_call_id === "string") results.push(block.tool_call_id);
			if (block.type === "function_call_output" && typeof block.call_id === "string") {
				results.push(block.call_id);
			}
			if (typeof block.tool_use_id === "string") results.push(block.tool_use_id);
			for (const entry of Object.values(block)) visit(entry);
		};
		visit([request.history, request.toolResults]);
		return { calls, results };
	}

	test.each([
		...historyFormats,
	])("%s keeps rebuilt first-turn tool IDs after reasoning-only compact", async (format) => {
		const provider = historyProvider(format);
		const rows = [
			completedHistoryTool("old-prefix", "call_same", "old-prefix"),
			completedHistoryTool("kept-tail", "call_same", "kept-tail"),
		];
		const initial = await provider.buildHistory(
			uniquifyDbMessageToolUseIds(rows),
			"history-probe-model",
		);
		const rebuilt = await provider.buildHistory(
			uniquifyDbMessageToolUseIds(rows.slice(1)),
			"history-probe-model",
		);
		let compacts = 0;
		const requests = await runHistoryInput(
			format,
			[
				[{ contextUsagePercentage: 96 }, { reasoning: "context is full" }],
				[historyTool("post-compact", "after-compact")],
				[{ text: "finished" }],
			],
			{
				onReasoningOnlyHighContext: async () => {
					compacts++;
					return { history: rebuilt.history, pendingToolResults: rebuilt.trailingToolResults };
				},
			},
			initial,
		);
		expect(compacts).toBe(1);
		expect(requests).toHaveLength(3);
		expect(toolPairIds(requests[0]).calls).toEqual(["call_same", "call_same_nfdup2"]);
		expect(toolPairIds(requests[1]).calls).toEqual(["call_same"]);
		expect(toolPairIds(requests[2]).calls).toEqual(["call_same", "post-compact"]);
		for (const request of requests) {
			const pairs = toolPairIds(request);
			expect(pairs.results).toEqual(pairs.calls);
		}
		expect(occurrences(requests[2], "call_same_nfdup2")).toBe(0);
		expect(occurrences(requests[2], "done:after-compact")).toBe(1);
	});

	test.each([
		"responses",
		"completions",
	] as const)("%s does not reappend initial tool results moved into rebuilt history", async (format) => {
		const provider = historyProvider(format);
		const row = completedHistoryTool("initial-tool", "initial-tool", "initial-tool");
		const initial = await provider.buildHistory([row], "history-probe-model");
		const rebuilt = await provider.buildHistory(
			[
				row,
				{
					id: "new-context",
					role: "sys",
					contentJson: [{ type: "text", text: "context added during preparation" }],
					contentText: "context added during preparation",
					parentToolUseId: null,
					messageUuid: null,
				},
			],
			"history-probe-model",
		);
		// Tool results are part of the projected history, never a separate trailing list.
		expect(initial.trailingToolResults).toEqual([]);
		expect(rebuilt.trailingToolResults).toEqual([]);
		let switched = false;
		const requests = await runHistoryInput(
			format,
			[[historyTool("after-rebuild", "after-rebuild")], [{ text: "finished" }]],
			{
				getModelOverride: () => (switched ? null : "test:rebuilt-model"),
				onBeforeTurn: async (turn, reason) => {
					if (!reason?.force) return null;
					expect(turn).toBe(0);
					expect(reason.cause).toBe("model_switch");
					switched = true;
					return { history: rebuilt.history, pendingToolResults: rebuilt.trailingToolResults };
				},
			},
			initial,
		);
		expect(switched).toBe(true);
		expect(requests).toHaveLength(2);
		for (const request of requests) {
			const pairs = toolPairIds(request);
			expect(pairs.results).toEqual(pairs.calls);
			expect(occurrences(request, "done:initial-tool")).toBe(1);
		}
		expect(occurrences(requests[1], "done:after-rebuild")).toBe(1);
	});

	test.each([
		...historyFormats,
	])("%s retains rebuilt tool results when the run started without any", async (format) => {
		const rebuilt = await historyProvider(format).buildHistory(
			[completedHistoryTool("newly-completed", "newly-completed", "newly-completed")],
			"history-probe-model",
		);
		expect(rebuilt.trailingToolResults).toEqual([]);
		let switched = false;
		const requests = await runHistoryInput(
			format,
			[[historyTool("after-rebuild", "after-rebuild")], [{ text: "finished" }]],
			{
				getModelOverride: () => (switched ? null : "test:rebuilt-model"),
				onBeforeTurn: async (_turn, reason) => {
					if (!reason?.force) return null;
					switched = true;
					return { history: rebuilt.history, pendingToolResults: rebuilt.trailingToolResults };
				},
			},
		);
		expect(requests).toHaveLength(2);
		expect(toolPairIds(requests[0]).calls).toEqual(["newly-completed"]);
		expect(toolPairIds(requests[1]).calls).toEqual(["newly-completed", "after-rebuild"]);
		for (const request of requests) {
			const pairs = toolPairIds(request);
			expect(pairs.results).toEqual(pairs.calls);
			expect(occurrences(request, "done:newly-completed")).toBe(1);
		}
	});

	test.each([
		...historyFormats,
	])("%s retains adopted completion input across later tool requests", async (format) => {
		let supplied = false;
		let consumed = 0;
		const requests = await runHistoryInput(
			format,
			[
				[historyTool("history-first", "first")],
				[historyTool("history-second", "second")],
				[historyTool("history-third", "third")],
				[{ text: "finished after adopting the completion" }],
			],
			{
				getAfterToolsInjections: () => {
					if (supplied) return "";
					supplied = true;
					return {
						text: completionNotice,
						onConsumed: () => {
							consumed++;
						},
					};
				},
			},
		);
		expect(requests).toHaveLength(4);
		expect(occurrences(requests[0], completionNotice)).toBe(0);
		expect(requests[1].content).toBe(completionNotice);
		expect(consumed).toBe(1);
		for (const request of requests.slice(1)) {
			expect(occurrences(request, completionNotice)).toBe(1);
			expect(occurrences(request, "done:first")).toBe(1);
		}
		for (const request of requests.slice(2)) {
			expect(request.content).toBe("");
			expect(occurrences(request.history, completionNotice)).toBe(1);
			expect(occurrences(request, "done:second")).toBe(1);
		}
	});

	async function rebuiltCompletionHistory(format: (typeof historyFormats)[number]) {
		const rows: DbMessage[] = [
			{
				id: "initial-row",
				role: "user",
				contentJson: [{ type: "text", text: "initial" }],
				contentText: "initial",
				parentToolUseId: null,
				messageUuid: null,
			},
			{
				id: "tool-row",
				role: "assistant",
				contentJson: [
					{ type: "tool_use", id: "history-first", name: FAST, input: { value: "first" } },
				],
				contentText: null,
				parentToolUseId: null,
				messageUuid: null,
				toolCalls: [
					{
						toolUseId: "history-first",
						toolName: FAST,
						inputJson: { value: "first" },
						outputJson: "done:first",
						status: "success",
					},
				],
			},
			{
				id: "completion-row",
				role: "sys",
				contentJson: [
					{ type: "system_injection", source: "bg_agent", modelText: completionNotice },
				],
				contentText: completionNotice,
				parentToolUseId: null,
				messageUuid: null,
			},
		];
		const built = await historyProvider(format).buildHistory(rows, "history-probe-model");
		return {
			history: built.history,
			pendingToolResults: built.trailingToolResults,
			// Use provenance from the real builder, not an assumption about source rows.
			replayedInjectionMessageIds: built.replayedInjectionMessageIds ?? [],
		};
	}

	test.each([...historyFormats])("%s sends a rebuilt completion row only once", async (format) => {
		let supplied = false;
		let consumed = 0;
		const replacement = await rebuiltCompletionHistory(format);
		const requests = await runHistoryInput(
			format,
			[
				[historyTool("history-first", "first")],
				[historyTool("history-second", "second")],
				[historyTool("history-third", "third")],
				[{ text: "finished" }],
			],
			{
				onBeforeTurn: async (turn) => (turn === 1 ? replacement : null),
				getAfterToolsInjections: () => {
					if (supplied) return "";
					supplied = true;
					return {
						text: completionNotice,
						parts: [{ messageId: "completion-row", text: completionNotice }],
						onConsumed: () => {
							consumed++;
						},
					};
				},
			},
		);
		expect(consumed).toBe(1);
		expect(requests).toHaveLength(4);
		for (const request of requests.slice(1)) {
			expect(occurrences(request, completionNotice)).toBe(1);
			expect(occurrences(request, "done:first")).toBe(1);
		}
	});

	test.each([
		...historyFormats,
	])("%s retains uncovered and same-text distinct input rows after rebuild", async (format) => {
		let supplied = false;
		const replacement = await rebuiltCompletionHistory(format);
		const newNotice = "A second background completion arrived after the rebuild snapshot.";
		const requests = await runHistoryInput(
			format,
			[
				[historyTool("history-first", "first")],
				[historyTool("history-second", "second")],
				[{ text: "finished" }],
			],
			{
				onBeforeTurn: async (turn) => (turn === 1 ? replacement : null),
				getAfterToolsInjections: () => {
					if (supplied) return "";
					supplied = true;
					const parts = [
						{ messageId: "completion-row", text: completionNotice },
						{ messageId: "distinct-completion-row", text: completionNotice },
						{ messageId: "new-completion-row", text: newNotice },
						{ text: "An unpersisted question hint remains pending." },
					];
					return { text: parts.map((part) => part.text).join("\n\n"), parts };
				},
			},
		);
		for (const request of requests.slice(1)) {
			expect(occurrences(request, completionNotice)).toBe(2);
			expect(occurrences(request, newNotice)).toBe(1);
			expect(occurrences(request, "An unpersisted question hint remains pending.")).toBe(1);
			expect(occurrences(request, "done:first")).toBe(1);
		}
	});

	test.each([
		...historyFormats,
	])("%s reprojects adopted input after a retry rebuild without another receipt", async (format) => {
		let supplied = false;
		let consumed = 0;
		let attempts = 0;
		let switchQueued = false;
		let rebuilt = 0;
		const replacement = await rebuiltCompletionHistory(format);
		const requests = await runHistoryInput(
			format,
			[
				[historyTool("history-first", "first")],
				new ApiError(502, "temporary upstream failure"),
				[historyTool("history-second", "second")],
				[{ text: "finished" }],
			],
			{
				maxTransientRetries: 1,
				retryBackoffCeilMs: 1,
				onModelInputConsumed: () => {
					if (++attempts === 2) switchQueued = true;
				},
				getModelOverride: () => (switchQueued ? "test:retry-model" : null),
				onBeforeTurn: async (_turn, reason) => {
					if (!reason?.force) return null;
					switchQueued = false;
					rebuilt++;
					return replacement;
				},
				getAfterToolsInjections: () => {
					if (supplied) return "";
					supplied = true;
					return {
						text: completionNotice,
						parts: [{ messageId: "completion-row", text: completionNotice }],
						onConsumed: () => {
							consumed++;
						},
					};
				},
			},
		);
		expect(rebuilt).toBe(1);
		expect(consumed).toBe(1);
		expect(requests).toHaveLength(4);
		for (const request of requests.slice(1)) {
			expect(occurrences(request, completionNotice)).toBe(1);
			expect(occurrences(request, "done:first")).toBe(1);
		}
	});

	test.each([
		...historyFormats,
	])("%s retains completion input while recovering truncated tool arguments", async (format) => {
		let supplied = false;
		let consumed = 0;
		const requests = await runHistoryInput(
			format,
			[
				[historyTool("truncated-first", "first")],
				[
					{ text: "starting another tool" },
					{
						toolUseChunk: {
							toolUseId: "truncated-ghost",
							name: FAST,
							input: '{"value":"unfinished',
						},
					},
				],
				[historyTool("truncated-third", "third")],
				[{ text: "finished after recovery" }],
			],
			{
				getAfterToolsInjections: () => {
					if (supplied) return "";
					supplied = true;
					return {
						text: completionNotice,
						onConsumed: () => {
							consumed++;
						},
					};
				},
			},
		);
		expect(requests).toHaveLength(4);
		expect(consumed).toBe(1);
		for (const request of requests.slice(1)) {
			expect(occurrences(request, completionNotice)).toBe(1);
			expect(occurrences(request, "done:first")).toBe(1);
		}
		expect(requests[2].content).toContain(FAST);
		// The first tool result was already appended during orphan recovery.
		expect(requests[2].toolResults).toEqual([]);
		expect(occurrences(requests[3].history, requests[2].content)).toBe(1);
	});

	test.each([
		...historyFormats,
	])("%s retains content-only recovery input without pending tool results", async (format) => {
		const requests = await runHistoryInput(format, [
			[
				{ text: "starting a tool" },
				{
					toolUseChunk: {
						toolUseId: "content-only-ghost",
						name: FAST,
						input: '{"value":"unfinished',
					},
				},
			],
			[historyTool("content-only-second", "second")],
			[{ text: "finished after content-only input" }],
		]);
		expect(requests).toHaveLength(3);
		expect(requests[1].content).toContain(FAST);
		expect(requests[1].toolResults).toEqual([]);
		expect(occurrences(requests[2].history, requests[1].content)).toBe(1);
	});

	test("aborted initial input is not acknowledged", async () => {
		const abort = new AbortController();
		abort.abort();
		let adopted = 0;
		await runInput({
			signal: abort.signal,
			onModelInputConsumed: () => {
				adopted++;
			},
		});
		expect(adopted).toBe(0);
	});

	test("runtime authorization failure cannot acknowledge input", async () => {
		let consumed = 0;
		const chat = spyOn(testProvider, "chat");
		try {
			await runInput({
				maxTransientRetries: 0,
				runtimeAuthorizationGuard: async () => {
					throw new Error("403 authorization revoked");
				},
				onModelInputConsumed: () => {
					consumed++;
				},
			});
			expect(consumed).toBe(0);
			expect(chat).not.toHaveBeenCalled();
		} finally {
			chat.mockRestore();
		}
	});

	test("after-tools preparation is acknowledged only when its bytes enter the following request", async () => {
		let calls = 0;
		let prepared = false;
		let consumed = 0;
		const chat = spyOn(testProvider, "chat").mockImplementation(async function* (params) {
			calls++;
			if (calls === 1) {
				expect(prepared).toBe(false);
				expect(consumed).toBe(0);
				yield {
					toolUses: [{ toolUseId: "consume-tool", name: FAST, input: { value: "injection" } }],
				};
			} else {
				expect(params.content).toContain("inbound message");
				expect(consumed).toBe(1);
				yield { text: "received" };
			}
		});
		try {
			await runInput({
				getAfterToolsInjections: () => {
					prepared = true;
					expect(consumed).toBe(0);
					return {
						text: "inbound message",
						onConsumed: () => {
							consumed++;
						},
					};
				},
			});
			expect(calls).toBe(2);
			expect(consumed).toBe(1);
		} finally {
			chat.mockRestore();
		}
	});

	test("interrupt after persisting an injection leaves its receipt unconsumed", async () => {
		const abort = new AbortController();
		let consumed = 0;
		let calls = 0;
		const chat = spyOn(testProvider, "chat").mockImplementation(async function* () {
			calls++;
			yield {
				toolUses: [{ toolUseId: "interrupted-consume", name: FAST, input: { value: "injection" } }],
			};
		});
		try {
			await runInput({
				signal: abort.signal,
				getAfterToolsInjections: () => {
					abort.abort();
					return {
						text: "persisted but never adopted",
						parts: [{ text: "persisted but never adopted", messageId: "interrupted-row" }],
						onConsumed: () => {
							consumed++;
						},
					};
				},
			});
			expect(calls).toBe(1);
			expect(consumed).toBe(0);
		} finally {
			chat.mockRestore();
		}
	});

	test("an upstream failure after adoption does not retract the receipt", async () => {
		let consumed = 0;
		const chat = spyOn(testProvider, "chat").mockImplementation(async function* () {
			expect(consumed).toBe(1);
			yield { text: "partial reply" };
			throw new Error("403 upstream rejected request");
		});
		try {
			await runInput({
				maxTransientRetries: 0,
				onModelInputConsumed: () => {
					consumed++;
				},
			});
			expect(consumed).toBe(1);
		} finally {
			chat.mockRestore();
		}
	});

	test("a rebuilt history replaces the old receipt source before the next request", async () => {
		const original: unknown[] = [];
		const replacement: unknown[] = [];
		const adopted: unknown[][] = [];
		let calls = 0;
		const chat = spyOn(testProvider, "chat").mockImplementation(async function* () {
			calls++;
			if (calls === 1)
				yield {
					toolUses: [{ toolUseId: "replace-history", name: FAST, input: { value: "rebuild" } }],
				};
			else yield { text: "rebuilt" };
		});
		try {
			await runInput(
				{
					onModelInputConsumed: (history) => {
						adopted.push(history);
					},
					onBeforeTurn: async () => ({ history: replacement, pendingToolResults: [] }),
				},
				original,
			);
			expect(adopted).toHaveLength(2);
			expect(adopted[0]).toBe(original);
			expect(adopted[1]).toBe(replacement);
		} finally {
			chat.mockRestore();
		}
	});

	test("receipt callback failure cannot retract input or make the loop retry it", async () => {
		let calls = 0;
		const initialHistory: unknown[] = [{ role: "user", content: "history input" }];
		const chat = spyOn(testProvider, "chat").mockImplementation(async function* () {
			calls++;
			yield { text: "received" };
		});
		try {
			await runInput(
				{
					onModelInputConsumed: (history) => {
						expect(history).toBe(initialHistory);
						throw new Error("receipt unavailable");
					},
				},
				initialHistory,
			);
			expect(calls).toBe(1);
		} finally {
			chat.mockRestore();
		}
	});
});

describe("cut-in queued while a tool is running", () => {
	test("no later tool of the turn runs once the cut-in is queued", async () => {
		const result = await runTurnWithCutIn();

		// The tool that was already in flight completes — a cut-in must never abort it.
		expect(result.executed).toEqual(["slow:in-flight"]);
		// Everything the model streamed afterwards is reported as skipped, so the model
		// sees why it did not run instead of a silently missing tool_result.
		expect(result.skipped).toEqual(["tu_b", "tu_c", "tu_d"]);
	});

	test("subagents keep their existing behaviour", async () => {
		// Subagents already deferred eager execution; this pins that the shared gate did
		// not change their outcome.
		const result = await runTurnWithCutIn({ deferEagerToolsForSafeStop: true });

		expect(result.executed).toEqual(["slow:in-flight"]);
		expect(result.skipped).toEqual(["tu_b", "tu_c", "tu_d"]);
	});

	test("a granted soft stop is consumed once, not re-asked per boundary", async () => {
		// The host's flag is one-shot: re-asking after a grant returns the NEXT request's
		// answer, which would resurrect the turn the loop already agreed to end. Granting
		// on the first ask (whichever phase makes it) and refusing forever after is the
		// harshest version of that contract.
		let asks = 0;
		let granted = false;
		const result = await runTurnWithCutIn({
			shouldStop: () => {
				asks++;
				if (granted) return false;
				granted = true;
				void releaseSlowOnceStarted();
				return true;
			},
		});

		expect(asks).toBeGreaterThan(0);
		expect(result.executed).toEqual(["slow:in-flight"]);
		expect(result.skipped).toEqual(["tu_b", "tu_c", "tu_d"]);
	});
});
