/**
 * The Codex HTTP chat path (provider.chat) must present the same client shape as
 * the WebSocket transport and the one-shot utility calls. It previously set only
 * `prompt_cache_key` + `parallel_tool_calls: true` and built its own reasoning
 * block, so a relay that fingerprints request shape saw three different clients
 * depending on which NarraFork path issued the call.
 *
 * Locks the outbound HTTP chat request against the shared stable contract.
 */
import { describe, expect, spyOn, test } from "bun:test";
import { ORIGINATOR, ORIGINATOR_CODEX } from "../../user-agent";
import { CodexProvider } from "../codex-provider";
import * as codexWebSocket from "../codex-websocket";
import { applyCodexReflectionTools, OpenAIProvider } from "../openai-provider";
import type { ChatParams } from "../provider";
import { resolveToolJsonSchema } from "../tool-registry";
import { dangerCancelTool, dangerConfirmTool } from "../tools/danger-reflection";

type Captured = {
	url: string;
	headers: Record<string, string>;
	body: Record<string, unknown>;
	websocketBody?: Record<string, unknown>;
};

async function captureChat(
	overrides: Partial<ChatParams> & {
		userAgentMode?: "narrafork" | "claude-code" | "codex" | "custom";
	} = {},
	apiMode: "codex" | "responses" | "completions" = "codex",
	transport: "http" | "ws" | "native-ws" = "http",
	failWebSocket = false,
): Promise<Captured> {
	const { userAgentMode = "codex", ...chatOverrides } = overrides;
	const provider =
		transport === "native-ws"
			? new CodexProvider({ useWebSocket: true })
			: new OpenAIProvider({
					id: "codex",
					name: "Codex",
					prefix: "codex",
					apiKey: "sk-test-key",
					baseUrl: "https://chatgpt.com/backend-api/codex",
					defaultModel: "gpt-5.3-codex",
					apiMode,
					codexWebSocket: transport === "ws",
					// Match codexFingerprintConfig() used by the built-in CodexProvider.
					userAgentMode,
					// biome-ignore lint/suspicious/noExplicitAny: test config subset
				} as any);

	if (provider instanceof CodexProvider) {
		Object.assign(provider, {
			getMaxFailoverAttempts: () => 1,
			getContext: async () => ({
				id: "test-credential",
				token: "sk-test-key",
				credential: { accountId: "test-account" },
			}),
			refreshUsageOnUse: () => {},
			manager: { reportSuccess: () => {} },
		});
	}
	let captured: Captured | undefined;
	let websocketBody: Record<string, unknown> | undefined;
	const websocket =
		transport === "http"
			? undefined
			: spyOn(codexWebSocket, "streamCodexResponsesWebSocket").mockImplementation(
					async function* (options) {
						websocketBody = structuredClone(options.request);
						if (failWebSocket)
							throw new codexWebSocket.CodexWebSocketFallbackError("test fallback", 404);
						captured = {
							url: codexWebSocket.buildCodexResponsesWebSocketUrl(options.baseUrl),
							headers: codexWebSocket.buildHandshakeHeaders(options),
							body: websocketBody,
						};
						yield { text: "ok" };
					},
				);
	const realFetch = globalThis.fetch;
	globalThis.fetch = (async (input: unknown, init?: RequestInit) => {
		const headers: Record<string, string> = {};
		const raw = init?.headers;
		if (raw instanceof Headers) {
			raw.forEach((v, k) => {
				headers[k.toLowerCase()] = v;
			});
		} else if (Array.isArray(raw)) {
			for (const [k, v] of raw) headers[String(k).toLowerCase()] = String(v);
		} else {
			for (const [k, v] of Object.entries((raw ?? {}) as Record<string, string>)) {
				headers[k.toLowerCase()] = v;
			}
		}
		captured = {
			url: String(input),
			headers,
			body: JSON.parse(String(init?.body ?? "{}")) as Record<string, unknown>,
		};
		const sse = 'data: {"type":"response.completed","response":{"output":[]}}\n\n';
		return new Response(sse, { status: 200, headers: { "content-type": "text/event-stream" } });
		// biome-ignore lint/suspicious/noExplicitAny: fetch stub
	}) as any;

	try {
		for await (const _event of provider.chat({
			conversationId: "conv-http-1",
			content: "hello",
			model: "codex:gpt-5.3-codex",
			cwd: process.cwd(),
			history: [],
			tools: [],
			toolResults: [],
			signal: new AbortController().signal,
			reasoningEffort: "high",
			...chatOverrides,
		})) {
			// Drain the stream so the request completes.
		}
	} finally {
		globalThis.fetch = realFetch;
		websocket?.mockRestore();
	}

	if (!captured) throw new Error("no request captured");
	return { ...captured, websocketBody };
}

describe("codex HTTP chat request parity", () => {
	test("sends the stable managed Codex client headers", async () => {
		const req = await captureChat();

		expect(req.headers.originator).toBe(ORIGINATOR_CODEX);
		// Official /responses never projects the installation id onto a header.
		expect(req.headers["x-codex-installation-id"]).toBeUndefined();
		expect(req.headers["user-agent"]).toMatch(new RegExp(`^${ORIGINATOR_CODEX}/[^ ]+ `));
		// The Codex Responses transport is always streamed.
		expect(req.headers.accept).toBe("text/event-stream");
	});

	/**
	 * User-reported bug lock: selecting NarraFork UA on the Codex provider must
	 * change both identity headers, not just User-Agent. originator is what
	 * request dumps and relays surface first.
	 */
	test("NarraFork UA mode presents NarraFork on User-Agent and originator", async () => {
		const req = await captureChat({ userAgentMode: "narrafork" });

		expect(req.headers.originator).toBe(ORIGINATOR);
		expect(req.headers["user-agent"]).toMatch(new RegExp(`^${ORIGINATOR}/[^ ]+ `));
		expect(req.headers["user-agent"]).not.toMatch(/^codex-tui\//);
		// Protocol identity fields still travel in the body; only the presented
		// client name changes. No direct installation-id header.
		expect(req.headers["x-codex-installation-id"]).toBeUndefined();
	});

	/**
	 * Parity here means matching a real non-lite Codex request: top-level
	 * instructions + tools. The lite header is the opt-in for the *other* contract
	 * (instructions/tools removed, additional_tools spliced into input,
	 * parallel_tool_calls off), so sending it alongside this body matches no real
	 * client and upstream rejects the pairing outright.
	 */
	test("does not claim the responses-lite contract while sending a non-lite body", async () => {
		const req = await captureChat();

		expect(req.headers["x-openai-internal-codex-responses-lite"]).toBeUndefined();
		expect(req.body.tools).toBeDefined();
		expect(req.body.instructions).toBeTruthy();
	});

	test("correlates the conversation identity across headers and body", async () => {
		const req = await captureChat();
		const clientMetadata = req.body.client_metadata as Record<string, string>;

		expect(req.headers["session-id"]).toBe("conv-http-1");
		expect(req.headers["thread-id"]).toBe("conv-http-1");
		expect(req.body.prompt_cache_key).toBe("conv-http-1");
		expect(clientMetadata.session_id).toBe("conv-http-1");
		expect(clientMetadata.thread_id).toBe("conv-http-1");
		// The real client uses the hyphenated key here and carries it ONLY in
		// client_metadata — never as a matching direct header.
		expect(clientMetadata["x-codex-installation-id"]).toBeTruthy();
		expect(req.headers["x-codex-installation-id"]).toBeUndefined();
	});

	test("sends the same stable body fields as the WebSocket and utility paths", async () => {
		const req = await captureChat();

		expect(req.body.tool_choice).toBe("auto");
		// Parallel tool calls stay on outside the responses-lite contract.
		expect(req.body.parallel_tool_calls).toBe(true);
		// No reasoning.context: the real client only sends it in responses-lite
		// mode; the non-lite shape omits it so the server default applies.
		expect(req.body.reasoning).toEqual({ effort: "high", summary: "auto" });
		expect(req.body.include).toEqual(["reasoning.encrypted_content"]);
		expect(req.body.text).toEqual({ verbosity: "low" });
		expect(req.body.store).toBe(false);
		expect(req.body.stream).toBe(true);
	});

	test("normalizes the requested reasoning effort onto the Codex ladder", async () => {
		const req = await captureChat({ reasoningEffort: "medium" });

		expect(req.body.reasoning).toEqual({ effort: "medium", summary: "auto" });
	});

	test("sends the window identity and omits the turn-metadata blob", async () => {
		const req = await captureChat();
		const clientMetadata = req.body.client_metadata as Record<string, string>;

		expect(req.headers["x-codex-turn-metadata"]).toBeUndefined();
		// Window id is conversation-stable and mirrored between the direct
		// header and client_metadata, like the real client's compatibility
		// projection of CodexResponsesMetadata.
		expect(req.headers["x-codex-window-id"]).toMatch(
			/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/,
		);
		expect(clientMetadata["x-codex-window-id"]).toBe(req.headers["x-codex-window-id"]);
	});
});

const decisionTools = [dangerConfirmTool, dangerCancelTool].map((tool) => ({
	type: "function",
	name: tool.name,
	description: tool.description,
	parameters: resolveToolJsonSchema(tool),
	strict: false,
}));
const parentFunction = {
	type: "function",
	name: "Read",
	description: "Original parent definition",
	parameters: { type: "object", properties: {} },
	strict: false,
};

test("an empty active list cannot enable predeclared or parent tools", () => {
	for (const reflection of [false, true]) {
		const body: Record<string, unknown> = { tools: [], input: [] };
		applyCodexReflectionTools(body, {
			tools: [],
			...(reflection
				? { reflectionParentTools: [parentFunction, { type: "web_search" }] }
				: { reflectionTools: decisionTools }),
		});
		expect(body.tool_choice).toBe("none");
		expect(body.input).toEqual([]);
	}
});

for (const transport of ["http", "ws", "native-ws"] as const) {
	describe(`Codex ${transport} reflection tool prefix`, () => {
		const capture = (overrides: Partial<ChatParams> = {}) =>
			captureChat(overrides, "codex", transport);
		test("full inherited declarations only enable the explicit reflection ceiling", async () => {
			const parent = await capture({
				tools: [parentFunction, ...decisionTools],
				reflectionTools: decisionTools,
			});
			const parentTools = structuredClone(parent.body.tools) as unknown[];
			for (const allowed of [[dangerCancelTool.name], []]) {
				const reflection = await capture({
					tools: structuredClone(parentTools),
					reflectionParentTools: parentTools,
					reflectionAllowedTools: allowed,
				});
				expect(reflection.body.tools).toEqual(parent.body.tools);
				expect(reflection.body.tool_choice).toEqual(
					allowed.length
						? {
								type: "allowed_tools",
								mode: "required",
								tools: [{ type: "function", name: dangerCancelTool.name }],
							}
						: "none",
				);
			}
		});
		test("keeps the parent wire prefix and parallel mode while restricting real decision tools", async () => {
			let parentTools: unknown[] | undefined;
			const normal: Partial<ChatParams> = {
				history: [
					{ role: "developer", content: "parent instructions" },
					{ role: "user", content: "shared history" },
				],
				tools: [parentFunction, { type: "web_search" }, { type: "image_generation" }],
				reflectionTools: decisionTools,
				onRequestStart: (info) => {
					parentTools = structuredClone(info?.codexTools);
				},
			};
			const parent = await capture(normal);
			expect((parent.body.tools as unknown[]).slice(-2)).toEqual(decisionTools);
			expect(parent.body.tool_choice).toEqual({
				type: "allowed_tools",
				mode: "auto",
				tools: [
					{ type: "function", name: parentFunction.name },
					{ type: "web_search" },
					{ type: "image_generation" },
				],
			});
			expect(JSON.stringify(parentTools)).toBe(JSON.stringify(parent.body.tools));
			const before = JSON.stringify(parentTools);
			const reflection = await capture({
				...normal,
				content: "Call exactly one of DangerConfirm or DangerCancel.",
				tools: structuredClone(decisionTools),
				reflectionParentTools: parentTools,
				onRequestStart: undefined,
			});
			expect(JSON.stringify(reflection.body.tools)).toBe(before);
			expect(JSON.stringify(parentTools)).toBe(before);
			expect(reflection.body.instructions).toBe(parent.body.instructions);
			const input = reflection.body.input as unknown[];
			expect(input.slice(0, -1)).toEqual((parent.body.input as unknown[]).slice(0, -1));
			expect(input.some((item) => (item as { type?: string }).type === "additional_tools")).toBe(
				false,
			);
			expect(reflection.headers["x-openai-internal-codex-responses-lite"]).toBeUndefined();
			expect(
				(reflection.body.client_metadata as Record<string, unknown>)
					.ws_request_header_x_openai_internal_codex_responses_lite,
			).toBeUndefined();
			expect(reflection.body.tool_choice).toEqual({
				type: "allowed_tools",
				mode: "required",
				tools: decisionTools.map((tool) => ({ type: "function", name: tool.name })),
			});
			for (const key of [
				"parallel_tool_calls",
				"prompt_cache_key",
				"reasoning",
				"include",
				"text",
				"client_metadata",
				"store",
				"stream",
			]) {
				expect(reflection.body[key]).toEqual(parent.body[key]);
			}
			expect(reflection.body.parallel_tool_calls).toBe(true);
			expect((await capture(normal)).body).toEqual(parent.body);
		});

		test("does not redeclare tools already in the parent list", async () => {
			const req = await capture({
				tools: [parentFunction, ...decisionTools],
				reflectionParentTools: [parentFunction],
			});
			expect(req.body.tools).toEqual([parentFunction, ...decisionTools]);
			expect(
				(req.body.input as Array<{ type?: string }>).some(
					(item) => item.type === "additional_tools",
				),
			).toBe(false);
			expect(req.body.tool_choice).toEqual({
				type: "allowed_tools",
				mode: "required",
				tools: [parentFunction, ...decisionTools].map((tool) => ({
					type: "function",
					name: tool.name,
				})),
			});
		});

		test("does not append another definition when all decisions already exist", async () => {
			const parentTools = [parentFunction, ...decisionTools];
			const req = await capture({ tools: decisionTools, reflectionParentTools: parentTools });
			expect(req.body.tools).toEqual(parentTools);
			expect(
				(req.body.input as Array<{ type?: string }>).some(
					(item) => item.type === "additional_tools",
				),
			).toBe(false);
			expect(req.body.tool_choice).toEqual({
				type: "allowed_tools",
				mode: "required",
				tools: decisionTools.map((tool) => ({ type: "function", name: tool.name })),
			});
		});

		test("a missing snapshot declares only decisions without native or Lite tools", async () => {
			const req = await capture({ tools: decisionTools, reflectionParentTools: [] });
			expect(req.body.tools).toEqual(decisionTools);
			expect(
				(req.body.input as Array<{ type?: string }>).some(
					(item) => item.type === "additional_tools",
				),
			).toBe(false);
		});

		test("without a parent snapshot the normal request behavior stays unchanged", async () => {
			const req = await capture({ tools: structuredClone(decisionTools) });
			expect(req.body.tool_choice).toBe("auto");
			expect(req.body.parallel_tool_calls).toBe(true);
			expect(
				(req.body.input as Array<{ type?: string }>).some(
					(item) => item.type === "additional_tools",
				),
			).toBe(false);
		});

		if (transport !== "http") {
			test("HTTP fallback preserves the WebSocket parent tools and decision restriction", async () => {
				let parentTools: unknown[] | undefined;
				await capture({
					tools: [parentFunction],
					reflectionTools: decisionTools,
					onRequestStart: (info) => {
						parentTools = structuredClone(info?.codexTools);
					},
				});
				const reported: unknown[] = [];
				const req = await captureChat(
					{
						tools: decisionTools,
						reflectionParentTools: parentTools,
						onRequestStart: (info) => {
							reported.push(structuredClone(info?.codexTools));
						},
					},
					"codex",
					transport,
					true,
				);
				expect(req.url.startsWith("https:")).toBe(true);
				expect(req.websocketBody).toEqual(req.body);
				expect(req.body.tools).toEqual(parentTools);
				expect(reported).toEqual([parentTools, parentTools]);
				expect(req.body.tool_choice).toEqual({
					type: "allowed_tools",
					mode: "required",
					tools: decisionTools.map((tool) => ({ type: "function", name: tool.name })),
				});
			});
		}
	});
}

for (const apiMode of ["responses", "completions"] as const) {
	test(`${apiMode} keeps its existing request shape`, async () => {
		let reported: unknown;
		const params = {
			tools: structuredClone(decisionTools),
			onRequestStart: (info?: { codexTools?: unknown[] }) => {
				reported = info?.codexTools;
			},
		};
		const normal = await captureChat(params, apiMode);
		const reflection = await captureChat(
			{ ...params, reflectionParentTools: [parentFunction], reflectionTools: decisionTools },
			apiMode,
		);
		expect(reflection.body).toEqual(normal.body);
		expect(reported).toBeUndefined();
	});
}

for (const transport of ["http", "ws", "native-ws"] as const) {
	test(`Codex compact reuses the last ordinary ${transport} prefix across provider instances`, async () => {
		const conversationId = `compact-prefix-${transport}`;
		const history = [
			{ role: "user", content: "shared history" },
			{
				type: "message",
				id: "8f0834db-a8ff-4256-ba6e-3c2d67075577",
				role: "assistant",
				status: "completed",
				phase: "final_answer",
				content: [{ type: "output_text", text: "legacy reply" }],
			},
			{ role: "assistant", content: "" },
			{ type: "reasoning", id: "rs_original", encrypted_content: "original-cipher", summary: [] },
			{
				type: "function_call",
				id: "fc_original",
				call_id: "original-call",
				name: "Read",
				arguments: "{}",
			},
		];
		const trailingToolResults = [
			{
				type: "function_call_output",
				call_id: "original-call",
				output: "file contents",
				_images: [{ format: "png", base64: "AA==" }],
			},
		];
		const normal = await captureChat(
			{
				conversationId,
				content: "",
				history: [{ role: "developer", content: "stable project instructions" }, ...history],
				toolResults: trailingToolResults,
				tools: [parentFunction, { type: "web_search" }, { type: "image_generation" }],
				reflectionTools: decisionTools,
				serviceTier: "priority",
			},
			"codex",
			transport,
		);
		await captureChat(
			{
				conversationId,
				history: [{ role: "developer", content: "reflection-only instructions" }],
				tools: decisionTools,
				reflectionParentTools: normal.body.tools as unknown[],
			},
			"codex",
			transport,
		);

		const provider = new OpenAIProvider(
			{
				id: "codex",
				name: "Codex",
				prefix: "codex",
				apiKey: "sk-test-key",
				baseUrl: "https://chatgpt.com/backend-api/codex",
				defaultModel: "gpt-5.3-codex",
				apiMode: "codex",
				codexRemoteCompact: true,
				codexWebSocket: transport !== "http",
				codexAccountId: transport === "native-ws" ? "test-account" : undefined,
			},
			undefined,
			transport === "native-ws" ? "test-credential" : undefined,
		);
		const requests: Record<string, unknown>[] = [];
		const realFetch = globalThis.fetch;
		globalThis.fetch = (async (_url: unknown, init?: RequestInit) => {
			requests.push(JSON.parse(String(init?.body)));
			return new Response(
				JSON.stringify({
					status: "completed",
					output: [{ type: "compaction", encrypted_content: "compacted" }],
				}),
				{ headers: { "Content-Type": "application/json" } },
			);
		}) as typeof fetch;
		const ws = spyOn(codexWebSocket, "streamCodexResponsesWebSocket").mockImplementation(
			async function* (options) {
				expect(options.ephemeral).toBe(true);
				expect(options.conversationId).toBe(conversationId);
				requests.push(structuredClone(options.request));
				options.onResponseFrame?.(
					JSON.stringify({
						type: "response.output_item.done",
						item: { type: "compaction", encrypted_content: "compacted" },
					}),
				);
				options.onResponseFrame?.(
					JSON.stringify({
						type: "response.completed",
						response: { status: "completed", output: [] },
					}),
				);
				yield { text: "" };
			},
		);
		try {
			const result = await provider.compactRemote(history, "codex:gpt-5.3-codex", {
				conversationId,
				trailingToolResults,
			});
			expect(result.items).toEqual([{ type: "compaction", encrypted_content: "compacted" }]);
			const { input, ...prefix } = normal.body;
			expect(requests[0]).toEqual({
				...prefix,
				input: [...(input as unknown[]), { type: "compaction_trigger" }],
			});
			expect(
				(requests[0].input as Array<{ role?: string; id?: string }>).filter(
					(item) => item.role === "assistant",
				),
			).toEqual([{ role: "assistant", content: [{ type: "output_text", text: "legacy reply" }] }]);
			expect(requests[0].input).toContainEqual({
				role: "user",
				content: [{ type: "input_image", image_url: "data:image/png;base64,AA==" }],
			});
			expect(requests[0].tool_choice).toMatchObject({ type: "allowed_tools", mode: "auto" });
			expect(requests[0].service_tier).toBe("priority");
			await provider.compactRemote(history, "codex:gpt-5.3-codex", {
				conversationId,
				reasoningEffort: "low",
				trailingToolResults,
			});
			expect(requests[1].reasoning).toEqual({ ...(prefix.reasoning as object), effort: "low" });
			expect(ws).toHaveBeenCalledTimes(transport === "http" ? 0 : 2);
		} finally {
			globalThis.fetch = realFetch;
			ws.mockRestore();
		}
	});
}

test("Codex compact templates cannot cross conversations, models or credentials", async () => {
	const conversationId = "compact-prefix-isolation";
	await captureChat({
		conversationId,
		history: [{ role: "developer", content: "private prefix" }],
	});
	const requests: Record<string, unknown>[] = [];
	const realFetch = globalThis.fetch;
	globalThis.fetch = (async (_url: unknown, init?: RequestInit) => {
		requests.push(JSON.parse(String(init?.body)));
		return new Response(
			JSON.stringify({
				status: "completed",
				output: [{ type: "compaction", encrypted_content: "compacted" }],
			}),
			{ headers: { "Content-Type": "application/json" } },
		);
	}) as typeof fetch;
	try {
		for (const changed of ["conversation", "model", "credential", "provider"] as const) {
			const provider = new OpenAIProvider({
				id: changed === "provider" ? "other-provider" : "codex",
				name: "Codex",
				prefix: "codex",
				apiKey: changed === "credential" ? "another-key" : "sk-test-key",
				baseUrl: "https://chatgpt.com/backend-api/codex",
				defaultModel: "gpt-5.3-codex",
				apiMode: "codex",
				codexRemoteCompact: true,
			});
			await provider.compactRemote(
				[{ role: "user", content: "history" }],
				changed === "model" ? "codex:gpt-5.4" : "codex:gpt-5.3-codex",
				{ conversationId: changed === "conversation" ? "other-conversation" : conversationId },
			);
			expect(requests.at(-1)?.instructions).toBeUndefined();
			expect(requests.at(-1)?.reasoning).toMatchObject({
				summary: "detailed",
				context: "all_turns",
			});
		}
	} finally {
		globalThis.fetch = realFetch;
	}
});
