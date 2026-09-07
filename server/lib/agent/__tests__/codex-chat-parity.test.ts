/**
 * The Codex HTTP chat path (provider.chat) must present the same client shape as
 * the WebSocket transport and the one-shot utility calls. It previously set only
 * `prompt_cache_key` + `parallel_tool_calls: true` and built its own reasoning
 * block, so a relay that fingerprints request shape saw three different clients
 * depending on which NarraFork path issued the call.
 *
 * Locks the outbound HTTP chat request against the shared stable contract.
 */
import { describe, expect, test } from "bun:test";
import { ORIGINATOR_CODEX } from "../../user-agent";
import { OpenAIProvider } from "../openai-provider";
import type { ChatParams } from "../provider";
import { resolveToolJsonSchema } from "../tool-registry";
import { dangerCancelTool, dangerConfirmTool } from "../tools/danger-reflection";

type Captured = { url: string; headers: Record<string, string>; body: Record<string, unknown> };

async function captureChat(
	overrides: Partial<ChatParams> = {},
	apiMode: "codex" | "responses" | "completions" = "codex",
): Promise<Captured> {
	const provider = new OpenAIProvider({
		id: "codex",
		name: "Codex",
		prefix: "codex",
		apiKey: "sk-test-key",
		baseUrl: "https://chatgpt.com/backend-api/codex",
		defaultModel: "gpt-5.3-codex",
		apiMode,
		// Match codexFingerprintConfig() used by the built-in CodexProvider.
		userAgentMode: "codex",
		// biome-ignore lint/suspicious/noExplicitAny: test config subset
	} as any);

	let captured: Captured | undefined;
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
			...overrides,
		})) {
			// Drain the stream so the request completes.
		}
	} finally {
		globalThis.fetch = realFetch;
	}

	if (!captured) throw new Error("no request captured");
	return captured;
}

describe("codex HTTP chat request parity", () => {
	test("sends the stable managed Codex client headers", async () => {
		const req = await captureChat();

		expect(req.headers.originator).toBe(ORIGINATOR_CODEX);
		expect(req.headers["x-codex-installation-id"]).toBeTruthy();
		expect(req.headers["user-agent"]).toMatch(new RegExp(`^${ORIGINATOR_CODEX}/[^ ]+ `));
		// The Codex Responses transport is always streamed.
		expect(req.headers.accept).toBe("text/event-stream");
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
		// The real client uses the hyphenated key here, matching the header name.
		expect(clientMetadata["x-codex-installation-id"]).toBe(req.headers["x-codex-installation-id"]);
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

describe("Codex HTTP reflection tool prefix", () => {
	test("keeps the parent wire prefix and parallel mode while restricting real decision tools", async () => {
		let parentTools: unknown[] | undefined;
		const normal: Partial<ChatParams> = {
			history: [
				{ role: "developer", content: "parent instructions" },
				{ role: "user", content: "shared history" },
			],
			tools: [parentFunction, { type: "web_search" }, { type: "image_generation" }],
			onRequestStart: (info) => {
				parentTools = structuredClone(info?.codexHttpTools);
			},
		};
		const parent = await captureChat(normal);
		expect(JSON.stringify(parentTools)).toBe(JSON.stringify(parent.body.tools));
		const before = JSON.stringify(parentTools);
		const reflection = await captureChat({
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
		expect(input.slice(0, -2)).toEqual((parent.body.input as unknown[]).slice(0, -1));
		expect(input.at(-1)).toEqual({
			type: "additional_tools",
			role: "developer",
			tools: decisionTools,
		});
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
		expect((await captureChat(normal)).body).toEqual(parent.body);
	});

	test("does not redeclare tools already in the parent list", async () => {
		const req = await captureChat({
			tools: [parentFunction, ...decisionTools],
			reflectionParentTools: [parentFunction],
		});
		expect((req.body.input as Array<{ tools?: unknown[] }>).at(-1)?.tools).toEqual(decisionTools);
		expect(req.body.tool_choice).toEqual({
			type: "allowed_tools",
			mode: "required",
			tools: [parentFunction, ...decisionTools].map((tool) => ({
				type: "function",
				name: tool.name,
			})),
		});
	});

	test("an explicitly empty parent stays empty; no native tools are injected", async () => {
		const req = await captureChat({ tools: decisionTools, reflectionParentTools: [] });
		expect(req.body.tools).toEqual([]);
		expect((req.body.input as Array<{ tools?: unknown[] }>).at(-1)?.tools).toEqual(decisionTools);
	});

	for (const apiMode of ["responses", "completions"] as const) {
		test(`${apiMode} keeps its existing request shape`, async () => {
			let reported: unknown;
			const params = {
				tools: structuredClone(decisionTools),
				onRequestStart: (info?: { codexHttpTools?: unknown[] }) => {
					reported = info?.codexHttpTools;
				},
			};
			const normal = await captureChat(params, apiMode);
			const reflection = await captureChat(
				{ ...params, reflectionParentTools: [parentFunction] },
				apiMode,
			);
			expect(reflection.body).toEqual(normal.body);
			expect(reported).toBeUndefined();
		});
	}
});
