import { describe, expect, test } from "bun:test";
import { KiroProvider } from "@server/lib/agent/kiro-provider";
import { RemoteProviderAdapter } from "@server/lib/agent/remote-provider-adapter";
import { ensureNonEmptySchema } from "@server/lib/agent/tool-registry";
import {
	toKiroChatRequest,
	toKiroGenerateRequest,
	toKiroRequestParts,
	toKiroTools,
} from "@shared/codewhisperer/canonical-request";
import { buildKiroRequest, normalizeKiroChatRequest } from "@shared/codewhisperer/protocol";
import type {
	KiroChatRequest,
	KiroHistoryAssistantMessage,
	KiroHistoryMessage,
	KiroHistoryUserMessage,
} from "@shared/codewhisperer/protocol-types";

/**
 * Upstream parity for the out-of-process Kiro provider.
 *
 * The plugin receives the host's provider-neutral DTO and must rebuild Kiro's wire format
 * from it, while the in-core `KiroProvider` builds the same format directly. Only the
 * downstream direction (upstream SSE → host events) had coverage; the upward direction had
 * none, which is how the plugin shipped sending `{"role":"assistant",...}` to a service that
 * requires `{"assistantResponseMessage":{...}}` with every test green.
 *
 * These cases drive both paths from one conversation and compare the resulting request
 * bodies, so a divergence fails here rather than as a 400 inside a narrator session.
 *
 * `examples/` is deliberately outside the tsconfig `paths`, so the plugin's own modules are
 * imported relatively where needed — the reach stays visible.
 */

const MODEL = "claude-sonnet-4.5";
const PREFIXED_MODEL = `kiro-ext:${MODEL}`;
const CONVERSATION_ID = "conv-parity";

interface ToolSpec {
	name: string;
	description: string;
	rawJsonSchema: Record<string, unknown>;
}

const BASH_TOOL: ToolSpec = {
	name: "bash",
	description: "Run a shell command",
	rawJsonSchema: {
		type: "object",
		properties: { command: { type: "string" } },
		required: ["command"],
	},
};

function remoteAdapter(): RemoteProviderAdapter {
	return new RemoteProviderAdapter({
		rpc: {
			chat: () => {
				throw new Error("unused");
			},
			generate: () => {
				throw new Error("unused");
			},
		} as never,
		providerTypeId: "com.narrafork.kiro-external",
		providerInstanceId: "com.narrafork.kiro-external/kiro",
		providerPrefix: "kiro-ext",
		config: {},
		modelCatalog: new Map(),
	});
}

/**
 * A conversation script, replayed through both providers.
 *
 * Expressed as operations rather than pre-built history because each provider owns its own
 * history representation — that difference is exactly what is under test.
 */
type Turn =
	| { kind: "system"; text: string }
	| { kind: "user"; text: string; images?: Array<{ format: string; base64: string }> }
	| {
			kind: "assistant";
			text: string;
			toolUses?: Array<{ toolUseId: string; name: string; input: Record<string, unknown> }>;
			reasoning?: Array<{ text: string; signature?: string }>;
	  }
	| {
			kind: "toolResult";
			toolUseId: string;
			output: string;
			isError?: boolean;
			images?: Array<{ format: string; base64: string }>;
			toolName?: string;
	  };

interface Scenario {
	turns: Turn[];
	current: string;
	currentImages?: Array<{ format: string; base64: string }>;
	tools?: ToolSpec[];
}

/**
 * Replay a scenario through a provider adapter.
 *
 * Tool results are held until the next user turn (or the current message), which is how the
 * agent loop feeds them: `formatToolResult` produces them, then `pushUserTurn` attaches them.
 */
function replay(
	provider: KiroProvider | RemoteProviderAdapter,
	scenario: Scenario,
): { history: unknown[]; trailingToolResults: unknown[] } {
	const history: unknown[] = [];
	let pending: unknown[] = [];
	const signatureSource = provider.getActiveReasoningSource();

	for (const turn of scenario.turns) {
		if (turn.kind === "system") {
			provider.injectSystemPrompt(history, turn.text, MODEL, "en");
			continue;
		}
		if (turn.kind === "user") {
			provider.pushUserTurn(history, turn.text, MODEL, pending, turn.images);
			pending = [];
			continue;
		}
		if (turn.kind === "toolResult") {
			pending.push(
				provider.formatToolResult(
					turn.toolUseId,
					turn.output,
					turn.isError ?? false,
					turn.images,
					turn.toolName,
				),
			);
			continue;
		}
		const reasoningBlocks = turn.reasoning?.map((block) => ({
			text: block.text,
			...(block.signature
				? {
						providerMetadata: {
							anthropic: { signature: block.signature },
							signatureSource,
							...(provider instanceof RemoteProviderAdapter
								? {
										plugin: {
											providerTypeId: "com.narrafork.kiro-external",
											source: signatureSource,
											format: "anthropic",
											data: { anthropic: { signature: block.signature } },
										},
									}
								: {}),
						},
					}
				: {}),
		}));
		provider.pushAssistantTurn(history, turn.text, turn.toolUses ?? [], reasoningBlocks as never);
	}
	return { history, trailingToolResults: pending };
}

/** Build the request the in-core provider would send. */
function builtinRequest(scenario: Scenario): KiroChatRequest {
	const provider = new KiroProvider();
	const { history, trailingToolResults } = replay(provider, scenario);
	const tools = provider.formatTools((scenario.tools ?? []) as never);
	const request = buildKiroRequest({
		conversationId: CONVERSATION_ID,
		content: scenario.current,
		model: MODEL,
		cwd: "/workspace",
		history,
		tools,
		toolResults: trailingToolResults,
		...(scenario.currentImages ? { images: scenario.currentImages } : {}),
	});
	return normalizeKiroChatRequest(request, {});
}

/** Build the request the plugin would send, from the canonical DTO the host hands it. */
function pluginRequest(scenario: Scenario): KiroChatRequest {
	const provider = remoteAdapter();
	const { history, trailingToolResults } = replay(provider, scenario);
	const tools = provider.formatTools((scenario.tools ?? []) as never);
	// Matches how `RemoteProviderAdapter.buildChatParams` assembles `request.current`.
	return toKiroChatRequest({
		conversationId: CONVERSATION_ID,
		modelId: MODEL,
		history,
		tools,
		current: {
			text: scenario.current,
			toolResults: trailingToolResults,
			...(scenario.currentImages
				? {
						images: scenario.currentImages.map((image) => ({
							mediaType: `image/${image.format}`,
							dataBase64: image.base64,
						})),
					}
				: {}),
		},
		cwd: "/workspace",
		locale: "en",
	});
}

function historyOf(request: KiroChatRequest): KiroHistoryMessage[] {
	return request.conversationState.history ?? [];
}

function isUser(message: KiroHistoryMessage): message is KiroHistoryUserMessage {
	return "userInputMessage" in message;
}

function isAssistant(message: KiroHistoryMessage): message is KiroHistoryAssistantMessage {
	return "assistantResponseMessage" in message;
}

/**
 * Every history entry must be a legal Kiro message.
 *
 * This is the assertion the plugin previously had no equivalent of: canonical blocks carry a
 * `role` key and no `userInputMessage`, so they fail here immediately.
 */
function expectValidKiroHistory(request: KiroChatRequest): void {
	for (const message of historyOf(request)) {
		const valid = isUser(message) || isAssistant(message);
		expect(valid, `illegal history entry: ${JSON.stringify(message)}`).toBe(true);
		expect(message, "canonical role leaked into Kiro history").not.toHaveProperty("role");
		expect(message, "canonical content leaked into Kiro history").not.toHaveProperty("content");
		if (isUser(message)) {
			// Kiro requires a modelId on every user message; the service rejects one without.
			expect(typeof message.userInputMessage.modelId).toBe("string");
			expect(message.userInputMessage.modelId.length).toBeGreaterThan(0);
		}
	}
	const current = request.conversationState.currentMessage.userInputMessage;
	expect(typeof current.content).toBe("string");
	expect(current.modelId).toBe(MODEL);
	for (const tool of current.userInputMessageContext?.tools ?? []) {
		// Tools must be wrapped in `toolSpecification`; a bare `{name, inputSchema}` is the
		// canonical shape and is rejected upstream.
		expect(tool, "tool must be wrapped in toolSpecification").toHaveProperty("toolSpecification");
		expect(tool.toolSpecification.inputSchema).toHaveProperty("json");
	}
	for (const result of current.userInputMessageContext?.toolResults ?? []) {
		expect(result).not.toHaveProperty("type");
		expect(Array.isArray(result.content)).toBe(true);
	}
}

const SCENARIOS: Array<{ name: string; scenario: Scenario }> = [
	{
		name: "system prompt, one exchange, tool call and result",
		scenario: {
			turns: [
				{ kind: "system", text: "You are a helpful assistant." },
				{ kind: "user", text: "list the files" },
				{
					kind: "assistant",
					text: "Let me check.",
					toolUses: [{ toolUseId: "t1", name: "bash", input: { command: "ls" } }],
				},
				{ kind: "toolResult", toolUseId: "t1", output: "a.txt\nb.txt", toolName: "bash" },
			],
			current: "thanks",
			tools: [BASH_TOOL],
		},
	},
	{
		name: "multi-turn with reasoning and a signature",
		scenario: {
			turns: [
				{ kind: "system", text: "Think carefully." },
				{ kind: "user", text: "what is 2+2" },
				{
					kind: "assistant",
					text: "4",
					reasoning: [{ text: "simple arithmetic", signature: "SIG-1" }],
				},
				{ kind: "user", text: "and 3+3" },
				{ kind: "assistant", text: "6", reasoning: [{ text: "also simple" }] },
			],
			current: "and 4+4",
		},
	},
	{
		name: "failed tool result",
		scenario: {
			turns: [
				{ kind: "user", text: "run it" },
				{
					kind: "assistant",
					text: "",
					toolUses: [{ toolUseId: "t9", name: "bash", input: { command: "false" } }],
				},
				{
					kind: "toolResult",
					toolUseId: "t9",
					output: "exit 1",
					isError: true,
					toolName: "bash",
				},
			],
			current: "why did it fail",
			tools: [BASH_TOOL],
		},
	},
	{
		name: "consecutive tool calls without an intervening user turn",
		scenario: {
			turns: [
				{ kind: "user", text: "do two things" },
				{
					kind: "assistant",
					text: "first",
					toolUses: [{ toolUseId: "a1", name: "bash", input: { command: "one" } }],
				},
				{ kind: "toolResult", toolUseId: "a1", output: "done one", toolName: "bash" },
				{ kind: "user", text: "" },
				{
					kind: "assistant",
					text: "second",
					toolUses: [{ toolUseId: "a2", name: "bash", input: { command: "two" } }],
				},
				{ kind: "toolResult", toolUseId: "a2", output: "done two", toolName: "bash" },
			],
			current: "all done?",
			tools: [BASH_TOOL],
		},
	},
	{
		name: "images on the current turn",
		scenario: {
			turns: [
				{ kind: "system", text: "Look at images." },
				{ kind: "user", text: "here is one" },
				{ kind: "assistant", text: "I see it." },
			],
			current: "and this one",
			currentImages: [{ format: "png", base64: "AAAB" }],
		},
	},
];

describe("kiro-external canonical request: produces legal Kiro wire format", () => {
	for (const { name, scenario } of SCENARIOS) {
		test(name, () => {
			expectValidKiroHistory(pluginRequest(scenario));
		});
	}
});

describe("kiro-external canonical request: parity with the in-core provider", () => {
	for (const { name, scenario } of SCENARIOS) {
		test(name, () => {
			const builtin = builtinRequest(scenario);
			const plugin = pluginRequest(scenario);

			// The conversation as the model sees it must match: same turn sequence, same
			// roles, same text, same tool calls and results.
			expect(plugin.conversationState.history).toEqual(builtin.conversationState.history);
			expect(plugin.conversationState.currentMessage).toEqual(
				builtin.conversationState.currentMessage,
			);
		});
	}
});

describe("kiro-external canonical request: tool conversion", () => {
	test("wraps tools in toolSpecification with a json schema", () => {
		const tools = toKiroTools([
			{ name: "bash", description: "run", inputSchema: { type: "object", properties: {} } },
		]);
		expect(tools).toHaveLength(1);
		expect(tools[0].toolSpecification.name).toBe("bash");
		expect(tools[0].toolSpecification.inputSchema.json).toBeDefined();
	});

	test("matches the host's ensureNonEmptySchema for schemas with no required property", () => {
		// Kiro truncates a tool whose schema has no required property. The converter carries
		// its own copy of this rule because the host helper imports zod and cannot be bundled
		// into a plugin; this pins the two together.
		const cases: Array<Record<string, unknown>> = [
			{ type: "object", properties: {} },
			{ type: "object", properties: { a: { type: "string" }, b: { type: "number" } } },
			{ type: "object", properties: { a: { type: "string" } }, required: ["a"] },
			{ type: "object" },
		];
		for (const schema of cases) {
			const converted = toKiroTools([{ name: "t", description: "", inputSchema: schema }]);
			expect(converted[0].toolSpecification.inputSchema.json).toEqual(
				ensureNonEmptySchema(structuredClone(schema)),
			);
		}
	});

	test("skips entries with no usable name", () => {
		expect(toKiroTools([{ description: "no name" }, null, "nope"])).toEqual([]);
	});
});

describe("kiro-external canonical request: reasoning round-trip", () => {
	test("echoes a signature as snake_case reasoning_content", () => {
		const request = toKiroChatRequest({
			conversationId: CONVERSATION_ID,
			modelId: MODEL,
			history: [
				{ role: "user", content: [{ type: "text", text: "q" }] },
				{
					role: "assistant",
					content: [
						{
							type: "reasoning",
							text: "thinking",
							continuation: {
								source: "kiro",
								format: "anthropic",
								data: { anthropic: { signature: "SIG-XYZ" } },
							},
						},
						{ type: "text", text: "answer" },
					],
				},
			],
			tools: [],
			current: { text: "next" },
		});
		const assistant = historyOf(request).find(isAssistant);
		// snake_case is required: the upstream rejects the camelCase spelling with a 400.
		expect(assistant?.assistantResponseMessage.reasoning_content).toEqual({
			reasoningText: { text: "thinking", signature: "SIG-XYZ" },
		});
	});

	test("replays thinking text without a signature when none survived", () => {
		const request = toKiroChatRequest({
			conversationId: CONVERSATION_ID,
			modelId: MODEL,
			history: [
				{ role: "user", content: [{ type: "text", text: "q" }] },
				{
					role: "assistant",
					content: [
						{ type: "reasoning", text: "unsigned thinking" },
						{ type: "text", text: "answer" },
					],
				},
			],
			tools: [],
			current: { text: "next" },
		});
		const assistant = historyOf(request).find(isAssistant);
		expect(assistant?.assistantResponseMessage.reasoning_content).toEqual({
			reasoningText: { text: "unsigned thinking" },
		});
	});

	test("keeps the thinking text but drops a non-anthropic signature", () => {
		const request = toKiroChatRequest({
			conversationId: CONVERSATION_ID,
			modelId: MODEL,
			history: [
				{ role: "user", content: [{ type: "text", text: "q" }] },
				{
					role: "assistant",
					content: [
						{
							type: "reasoning",
							text: "thinking",
							continuation: {
								source: "kiro",
								format: "gemini",
								data: { thoughtSignature: "NOT-ANTHROPIC" },
							},
						},
						{ type: "text", text: "answer" },
					],
				},
			],
			tools: [],
			current: { text: "next" },
		});
		const reasoning =
			historyOf(request).find(isAssistant)?.assistantResponseMessage.reasoning_content;
		// Echoing a signature minted by a different reasoning format would fail verification
		// upstream, so only the text survives.
		expect(reasoning?.reasoningText?.text).toBe("thinking");
		expect(reasoning?.reasoningText?.signature).toBeUndefined();
	});

	test("carries a redacted thinking block through", () => {
		const request = toKiroChatRequest({
			conversationId: CONVERSATION_ID,
			modelId: MODEL,
			history: [
				{ role: "user", content: [{ type: "text", text: "q" }] },
				{
					role: "assistant",
					content: [
						{ type: "redacted_reasoning", data: "REDACTED-B64", source: "kiro" },
						{ type: "text", text: "answer" },
					],
				},
			],
			tools: [],
			current: { text: "next" },
		});
		expect(
			historyOf(request).find(isAssistant)?.assistantResponseMessage.reasoning_content
				?.redactedContent,
		).toBe("REDACTED-B64");
	});

	test("drops a reasoning-only assistant turn exactly as the in-core path does", () => {
		// `isEmptyAssistant` does not count `reasoning_content` as payload, so an assistant
		// turn with thinking but no text or tool call is replaced by a synthetic "OK" to keep
		// alternation. That is upstream-protocol behaviour in `normalizeKiroChatRequest`, not a
		// converter decision — pinned here because it looks like signature loss when read from
		// the output alone, and because the two paths must agree on it.
		const scenario: Scenario = {
			turns: [
				{ kind: "user", text: "q" },
				{ kind: "assistant", text: "", reasoning: [{ text: "thinking", signature: "SIG" }] },
			],
			current: "next",
		};
		const plugin = pluginRequest(scenario);
		expect(plugin.conversationState.history).toEqual(
			builtinRequest(scenario).conversationState.history,
		);
		const assistant = historyOf(plugin).find(isAssistant);
		expect(assistant?.assistantResponseMessage.content).toBe("OK");
		expect(assistant?.assistantResponseMessage.reasoning_content).toBeUndefined();
	});
});

describe("kiro-external canonical request: generate", () => {
	test("prompt mode carries the text as the current message", () => {
		const request = toKiroGenerateRequest({
			conversationId: "gen-1",
			modelId: MODEL,
			request: { mode: "prompt", text: "Summarise this", systemInstruction: "Be brief" },
		});
		// The original defect: generate shares no field names with chat, so routing it through
		// the chat converter read an absent `current` and sent an empty prompt.
		expect(request.conversationState.currentMessage.userInputMessage.content).toBe(
			"Summarise this",
		);
		const history = historyOf(request);
		expect(history.filter(isUser)[0]?.userInputMessage.content).toBe("Be brief");
		expect(history.filter(isAssistant)[0]?.assistantResponseMessage.content).toBeTruthy();
	});

	test("prompt mode works without a system instruction", () => {
		const request = toKiroGenerateRequest({
			conversationId: "gen-2",
			modelId: MODEL,
			request: { mode: "prompt", text: "Just this" },
		});
		expect(request.conversationState.currentMessage.userInputMessage.content).toBe("Just this");
		expect(request.conversationState.history).toBeUndefined();
	});

	test("history mode carries the instruction, reminder and content", () => {
		const request = toKiroGenerateRequest({
			conversationId: "gen-3",
			modelId: MODEL,
			request: {
				mode: "history",
				systemInstruction: "You generate titles",
				content: "A conversation about plugins",
			},
			reminder: "Reply with ONLY a short title.",
			ack: "Understood.",
		});
		const content = request.conversationState.currentMessage.userInputMessage.content;
		expect(content).toContain("Reply with ONLY a short title.");
		expect(content).toContain("A conversation about plugins");
		const history = historyOf(request);
		expect(history.filter(isUser)[0]?.userInputMessage.content).toBe("You generate titles");
		expect(history.filter(isAssistant)[0]?.assistantResponseMessage.content).toBe("Understood.");
	});

	test("never sends tools", () => {
		const request = toKiroGenerateRequest({
			conversationId: "gen-4",
			modelId: MODEL,
			request: { mode: "prompt", text: "hello" },
		});
		// A generate operation that emitted a tool call is a protocol violation the host
		// rejects, so no tool may be offered in the first place.
		const tools =
			request.conversationState.currentMessage.userInputMessage.userInputMessageContext?.tools;
		expect(tools === undefined || tools.length === 0).toBe(true);
	});

	test("an empty request still produces a sendable message", () => {
		const request = toKiroGenerateRequest({
			conversationId: "gen-5",
			modelId: MODEL,
			request: {},
		});
		// `normalizeKiroChatRequest` injects a continuation prompt rather than letting an
		// empty current turn reach the service, which rejects it.
		expect(
			request.conversationState.currentMessage.userInputMessage.content.length,
		).toBeGreaterThan(0);
	});
});

describe("kiro-external canonical request: signature survives across turns", () => {
	/**
	 * Store a signed thinking block the way the host would, then read it back with a fresh
	 * adapter — which is what happens on the next turn, since the provider registry has no
	 * adapter cache and calls `createAdapter()` per model resolution.
	 */
	function roundTrip(storedSource: string): unknown {
		const stored = {
			type: "reasoning",
			text: "thinking",
			providerMetadata: {
				signatureSource: storedSource,
				plugin: {
					providerTypeId: "com.narrafork.kiro-external",
					source: storedSource,
					format: "anthropic",
					data: { anthropic: { signature: "SIG-ROUNDTRIP" } },
				},
			},
		};
		const adapter = remoteAdapter();
		return adapter.buildHistory(
			[{ role: "assistant", contentJson: [stored], toolCalls: [] }] as never,
			PREFIXED_MODEL,
		);
	}

	test("the host's own source label round-trips", async () => {
		// This is why `startChat` does not send `reasoningSource` in `request_started`.
		const built = await roundTrip("plugin:com.narrafork.kiro-external:kiro-external/kiro");
		const source = remoteAdapter().getActiveReasoningSource();
		const stable = await roundTrip(source);
		const block = ((stable as { history: Array<{ content: Array<Record<string, unknown>> }> })
			.history[0]?.content ?? [])[0];
		expect(block?.continuation, "a stable source must survive to the next turn").toBeDefined();
		// Sanity: the arbitrary label above is not the stable one, so it must not survive.
		const stale = ((built as { history: Array<{ content: Array<Record<string, unknown>> }> })
			.history[0]?.content ?? [])[0];
		expect(stale?.continuation).toBeUndefined();
	});

	test('overriding the source with "kiro" loses the signature on the next turn', async () => {
		// Regression guard: the plugin used to emit `reasoningSource: "kiro"`, which the host
		// persisted on the block. A later turn runs with a fresh adapter whose source is back to
		// `plugin:<typeId>:<instanceId>`, so the stored label no longer matches and the
		// signature is dropped — silently losing thinking continuity.
		const built = await roundTrip("kiro");
		const block = ((built as { history: Array<{ content: Array<Record<string, unknown>> }> })
			.history[0]?.content ?? [])[0];
		expect(block?.continuation).toBeUndefined();
	});

	test("the plugin does not send reasoningSource", async () => {
		// Asserted against the built artifact as well as the source, since the artifact is what
		// actually runs.
		const source = await Bun.file(
			new URL("../../../../examples/plugins/kiro-external/src/server.ts", import.meta.url),
		).text();
		const requestStarted = source.slice(source.indexOf('type: "request_started"'));
		expect(requestStarted.slice(0, 120)).not.toContain("reasoningSource");
	});
});

describe("kiro-external canonical request: structural edge cases", () => {
	test("maps a tool role message onto the following user turn", () => {
		const request = toKiroChatRequest({
			conversationId: CONVERSATION_ID,
			modelId: MODEL,
			history: [
				{ role: "user", content: [{ type: "text", text: "go" }] },
				{
					role: "assistant",
					content: [{ type: "tool_call", toolUseId: "x1", name: "bash", input: {} }],
				},
				{
					role: "tool",
					content: [
						{
							type: "tool_result",
							toolUseId: "x1",
							content: [{ type: "text", text: "output" }],
							isError: false,
						},
					],
				},
				{ role: "assistant", content: [{ type: "text", text: "done" }] },
			],
			tools: [],
			current: { text: "next" },
		});
		// Kiro has no tool role: the result must ride on a user message positioned directly
		// after the assistant that called it, or `sanitizeKiroToolPairs` discards it.
		const carrier = historyOf(request).find(
			(message) =>
				isUser(message) &&
				(message.userInputMessage.userInputMessageContext?.toolResults?.length ?? 0) > 0,
		);
		expect(carrier).toBeDefined();
		const results =
			carrier && isUser(carrier)
				? carrier.userInputMessage.userInputMessageContext?.toolResults
				: undefined;
		expect(results?.[0]?.toolUseId).toBe("x1");
		expect(results?.[0]?.content).toEqual([{ text: "output" }]);
	});

	test("degrades a web_search block to text", () => {
		const request = toKiroChatRequest({
			conversationId: CONVERSATION_ID,
			modelId: MODEL,
			history: [
				{ role: "user", content: [{ type: "text", text: "search" }] },
				{
					role: "assistant",
					content: [
						{ type: "text", text: "Searching." },
						{ type: "web_search", id: "s1", query: "kiro protocol" },
					],
				},
			],
			tools: [],
			current: { text: "next" },
		});
		const assistant = historyOf(request).find(isAssistant);
		// Kiro has no web_search field; dropping it silently would hide that a search ran.
		expect(assistant?.assistantResponseMessage.content).toContain("kiro protocol");
	});

	test("lifts images out of a current-turn tool result", () => {
		const parts = toKiroRequestParts({
			conversationId: CONVERSATION_ID,
			modelId: MODEL,
			history: [],
			tools: [],
			current: {
				text: "look",
				toolResults: [
					{
						type: "tool_result",
						toolUseId: "r1",
						content: [
							{ type: "text", text: "[Image: shot.png (100 bytes)]" },
							{ type: "image", mediaType: "image/png", dataBase64: "IMGDATA" },
						],
						isError: false,
					},
				],
			},
		});
		// Kiro tool results carry text only, so the bytes have to move to the user message.
		expect(parts.images).toEqual([{ format: "png", source: { bytes: "IMGDATA" } }]);
		expect(parts.toolResults[0].content).toEqual([{ text: "[Image: shot.png (100 bytes)]" }]);
	});

	test("normalizes an unknown image media type to png", () => {
		const parts = toKiroRequestParts({
			conversationId: CONVERSATION_ID,
			modelId: MODEL,
			history: [],
			tools: [],
			current: {
				text: "x",
				images: [{ mediaType: "image/svg+xml", dataBase64: "SVG" }],
			},
		});
		expect(parts.images[0].format).toBe("png");
	});

	test("maps image/jpg onto the jpeg format Kiro accepts", () => {
		const parts = toKiroRequestParts({
			conversationId: CONVERSATION_ID,
			modelId: MODEL,
			history: [],
			tools: [],
			current: { text: "x", images: [{ mediaType: "image/jpg", dataBase64: "J" }] },
		});
		expect(parts.images[0].format).toBe("jpeg");
	});

	test("survives malformed canonical input without throwing", () => {
		const request = toKiroChatRequest({
			conversationId: CONVERSATION_ID,
			modelId: MODEL,
			history: [
				null,
				"nonsense",
				{ role: "user" },
				{ role: "user", content: "not an array" },
				{ role: "assistant", content: [{ noType: true }] },
				{ role: "unknown-role", content: [{ type: "text", text: "ignored" }] },
			] as never,
			tools: [{ name: "" }, null] as never,
			current: { text: "still works" },
		});
		// Everything here crossed a process boundary as JSON, so a malformed entry must be
		// skipped rather than crash the operation.
		expect(request.conversationState.currentMessage.userInputMessage.content).toBe("still works");
		expectValidKiroHistory(request);
	});

	test("omits history entirely when nothing survives conversion", () => {
		const request = toKiroChatRequest({
			conversationId: CONVERSATION_ID,
			modelId: MODEL,
			history: [{ role: "system", content: [] }],
			tools: [],
			current: { text: "hi" },
		});
		expect(request.conversationState.history).toBeUndefined();
	});
});
