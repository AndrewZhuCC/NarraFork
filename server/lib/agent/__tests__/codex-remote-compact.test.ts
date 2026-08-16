import { describe, expect, test } from "bun:test";
import { CodexProvider } from "../codex-provider";
import {
	clipMessagesBeforeUnpairedToolCalls,
	findUnpairedCodexFunctionCallIds,
	OpenAIProvider,
	prepareCodexCompactInput,
} from "../openai-provider";
import type { DbMessage } from "../provider";
import { isCodexRemoteCompactAdapter, isCodexRemoteCompactEnabled } from "../provider";

describe("isCodexRemoteCompactAdapter", () => {
	test("accepts official Codex OAuth and API-key Codex relays", () => {
		expect(isCodexRemoteCompactAdapter(new CodexProvider({ useRemoteCompact: true }))).toBe(true);
		expect(
			isCodexRemoteCompactAdapter(
				new OpenAIProvider({
					id: "relay",
					name: "Codex Relay",
					prefix: "relay",
					apiKey: "sk-test",
					baseUrl: "https://example.invalid/v1",
					defaultModel: "gpt-5.3-codex",
					apiMode: "codex",
					codexRemoteCompact: true,
				}),
			),
		).toBe(true);
	});

	test("rejects non-Codex OpenAI-compatible providers", () => {
		expect(
			isCodexRemoteCompactAdapter(
				new OpenAIProvider({
					id: "openai",
					name: "OpenAI",
					prefix: "openai",
					apiKey: "sk-test",
					baseUrl: "https://api.openai.com/v1",
					defaultModel: "gpt-4.1",
					apiMode: "responses",
				}),
			),
		).toBe(false);
	});
});

describe("isCodexRemoteCompactEnabled", () => {
	test("uses each adapter's own switch", () => {
		expect(isCodexRemoteCompactEnabled(new CodexProvider({ useRemoteCompact: false }))).toBe(false);
		expect(isCodexRemoteCompactEnabled(new CodexProvider({ useRemoteCompact: true }))).toBe(true);
		expect(
			isCodexRemoteCompactEnabled(
				new OpenAIProvider({
					id: "relay",
					name: "Codex Relay",
					prefix: "relay",
					apiKey: "sk-test",
					baseUrl: "https://example.invalid/v1",
					defaultModel: "gpt-5.3-codex",
					apiMode: "codex",
					codexRemoteCompact: false,
				}),
			),
		).toBe(false);
		expect(
			isCodexRemoteCompactEnabled(
				new OpenAIProvider({
					id: "relay",
					name: "Codex Relay",
					prefix: "relay",
					apiKey: "sk-test",
					baseUrl: "https://example.invalid/v1",
					defaultModel: "gpt-5.3-codex",
					apiMode: "codex",
					codexRemoteCompact: true,
				}),
			),
		).toBe(true);
	});
});

describe("OpenAIProvider.compactRemote", () => {
	test("posts /responses for API-key Codex relays when enabled", async () => {
		const provider = new OpenAIProvider({
			id: "relay",
			name: "Codex Relay",
			prefix: "relay",
			apiKey: "sk-test",
			baseUrl: "https://example.invalid/v1",
			defaultModel: "gpt-5.3-codex",
			apiMode: "codex",
			codexRemoteCompact: true,
		});
		const originalFetch = globalThis.fetch;
		let capturedUrl = "";
		let capturedBody: Record<string, unknown> = {};
		globalThis.fetch = (async (input: unknown, init?: RequestInit) => {
			capturedUrl = String(input);
			capturedBody = JSON.parse(String(init?.body ?? "{}")) as Record<string, unknown>;
			return new Response(
				JSON.stringify({
					output: [{ type: "compaction", id: "cmp_test", encrypted_content: "enc" }],
				}),
				{
					status: 200,
					headers: { "content-type": "application/json" },
				},
			);
		}) as typeof fetch;
		try {
			const result = await provider.compactRemote(
				[{ type: "message", role: "user" }],
				"relay:gpt-5.3-codex",
				{ conversationId: "conv-compact" },
			);
			expect(capturedUrl).toBe("https://example.invalid/v1/responses");
			expect(capturedBody.model).toBe("gpt-5.3-codex");
			expect(capturedBody.input).toEqual([
				{ type: "message", role: "user" },
				{ type: "compaction_trigger" },
			]);
			expect(capturedBody.stream).toBe(true);
			expect(capturedBody.store).toBe(false);
			expect(capturedBody.prompt_cache_key).toBe("conv-compact");
			expect(capturedBody.tool_choice).toBe("auto");
			expect(capturedBody.include).toEqual(["reasoning.encrypted_content"]);
			expect(capturedBody.stream_options).toEqual({
				reasoning_summary_delivery: "sequential_cutoff",
			});
			expect((capturedBody.reasoning as { context?: string }).context).toBe("all_turns");
			expect(capturedBody).toHaveProperty("client_metadata");
			expect(capturedBody).not.toHaveProperty("instructions");
			expect(result.items).toEqual([
				{ type: "compaction", id: "cmp_test", encrypted_content: "enc" },
			]);
		} finally {
			globalThis.fetch = originalFetch;
		}
	});

	test("rewrites item ids to official prefixes and appends compaction_trigger", async () => {
		const provider = new OpenAIProvider({
			id: "relay",
			name: "Codex Relay",
			prefix: "relay",
			apiKey: "sk-test",
			baseUrl: "https://example.invalid/v1",
			defaultModel: "gpt-5.3-codex",
			apiMode: "codex",
			codexRemoteCompact: true,
		});
		const originalFetch = globalThis.fetch;
		let capturedBody: Record<string, unknown> = {};
		globalThis.fetch = (async (_input: unknown, init?: RequestInit) => {
			capturedBody = JSON.parse(String(init?.body ?? "{}")) as Record<string, unknown>;
			return new Response(
				JSON.stringify({
					output: [{ type: "compaction", id: "cmp_test", encrypted_content: "enc" }],
				}),
				{ status: 200, headers: { "content-type": "application/json" } },
			);
		}) as typeof fetch;
		try {
			await provider.compactRemote(
				[
					{ type: "message", role: "user", id: "abc123" },
					{ type: "reasoning", id: "rs1" },
					{ type: "function_call", id: "fc1", call_id: "c1" },
					{ type: "function_call_output", call_id: "c1", output: "ok" },
				],
				"relay:gpt-5.3-codex",
			);
			const input = capturedBody.input as Array<Record<string, unknown>>;
			expect(input[0].id).toBe("msg_abc123");
			expect(input[1].id).toBe("rs_rs1");
			expect(input[2].id).toBe("fc_fc1");
			expect(input[2].call_id).toBe("call_c1");
			expect(input[3]).toMatchObject({ type: "function_call_output", call_id: "call_c1" });
			expect(input[input.length - 1]).toEqual({ type: "compaction_trigger" });
		} finally {
			globalThis.fetch = originalFetch;
		}
	});

	test("rejects empty input before calling the API", async () => {
		const provider = new OpenAIProvider({
			id: "relay",
			name: "Codex Relay",
			prefix: "relay",
			apiKey: "sk-test",
			baseUrl: "https://example.invalid/v1",
			defaultModel: "gpt-5.3-codex",
			apiMode: "codex",
			codexRemoteCompact: true,
		});
		let called = false;
		const originalFetch = globalThis.fetch;
		globalThis.fetch = (async () => {
			called = true;
			return new Response("should not run", { status: 500 });
		}) as unknown as typeof fetch;
		try {
			await expect(provider.compactRemote([], "relay:gpt-5.3-codex")).rejects.toThrow(
				"non-empty input",
			);
			expect(called).toBe(false);
		} finally {
			globalThis.fetch = originalFetch;
		}
	});

	test("parses SSE compaction items from /responses", async () => {
		const provider = new OpenAIProvider({
			id: "relay",
			name: "Codex Relay",
			prefix: "relay",
			apiKey: "sk-test",
			baseUrl: "https://example.invalid/v1",
			defaultModel: "gpt-5.3-codex",
			apiMode: "codex",
			codexRemoteCompact: true,
		});
		const originalFetch = globalThis.fetch;
		globalThis.fetch = (async () => {
			const sse: string = [
				"event: response.output_item.done",
				'data: {"type":"response.output_item.done","item":{"id":"cmp_sse","type":"compaction","encrypted_content":"enc"}}',
				"",
				"event: response.completed",
				'data: {"type":"response.completed","response":{"output":[{"id":"cmp_sse","type":"compaction","encrypted_content":"enc"}]}}',
				"",
			].join("\n");
			return new Response(sse, {
				status: 200,
				headers: { "content-type": "text/event-stream" },
			});
		}) as unknown as typeof fetch;
		try {
			const result = await provider.compactRemote(
				[{ type: "message", role: "user" }],
				"relay:gpt-5.3-codex",
			);
			expect(result.items).toEqual([
				{ id: "cmp_sse", type: "compaction", encrypted_content: "enc" },
			]);
		} finally {
			globalThis.fetch = originalFetch;
		}
	});

	test("appends trailing tool outputs and keeps a single trigger last", async () => {
		const provider = new OpenAIProvider({
			id: "relay",
			name: "Codex Relay",
			prefix: "relay",
			apiKey: "sk-test",
			baseUrl: "https://example.invalid/v1",
			defaultModel: "gpt-5.3-codex",
			apiMode: "codex",
			codexRemoteCompact: true,
		});
		const originalFetch = globalThis.fetch;
		let capturedBody: Record<string, unknown> = {};
		globalThis.fetch = (async (_input: unknown, init?: RequestInit) => {
			capturedBody = JSON.parse(String(init?.body ?? "{}")) as Record<string, unknown>;
			return new Response(
				JSON.stringify({
					output: [{ type: "compaction", id: "cmp_test", encrypted_content: "enc" }],
				}),
				{ status: 200, headers: { "content-type": "application/json" } },
			);
		}) as typeof fetch;
		try {
			await provider.compactRemote(
				[
					{ type: "function_call", call_id: "call_read1", name: "Read" },
					{ type: "compaction_trigger" },
				],
				"relay:gpt-5.3-codex",
				{
					trailingToolResults: [
						{ type: "function_call_output", call_id: "call_read1", output: "ok" },
					],
				},
			);
			const input = capturedBody.input as Array<Record<string, unknown>>;
			expect(input.map((item) => item.type)).toEqual([
				"function_call",
				"function_call_output",
				"compaction_trigger",
			]);
			expect(input[1]).toMatchObject({ call_id: "call_read1", output: "ok" });
		} finally {
			globalThis.fetch = originalFetch;
		}
	});

	test("rejects JSON 200 output that is only a chat message", async () => {
		const provider = new OpenAIProvider({
			id: "relay",
			name: "Codex Relay",
			prefix: "relay",
			apiKey: "sk-test",
			baseUrl: "https://example.invalid/v1",
			defaultModel: "gpt-5.3-codex",
			apiMode: "codex",
			codexRemoteCompact: true,
		});
		const originalFetch = globalThis.fetch;
		globalThis.fetch = (async () =>
			new Response(
				JSON.stringify({
					output: [
						{ type: "message", role: "assistant", content: [{ type: "output_text", text: "hi" }] },
					],
				}),
				{ status: 200, headers: { "content-type": "application/json" } },
			)) as unknown as typeof fetch;
		try {
			await expect(
				provider.compactRemote([{ type: "message", role: "user" }], "relay:gpt-5.3-codex"),
			).rejects.toThrow("did not contain output items");
		} finally {
			globalThis.fetch = originalFetch;
		}
	});

	test("parses compaction from output_item.done when completed output is empty", async () => {
		const provider = new OpenAIProvider({
			id: "relay",
			name: "Codex Relay",
			prefix: "relay",
			apiKey: "sk-test",
			baseUrl: "https://example.invalid/v1",
			defaultModel: "gpt-5.3-codex",
			apiMode: "codex",
			codexRemoteCompact: true,
		});
		const originalFetch = globalThis.fetch;
		globalThis.fetch = (async () =>
			new Response(
				[
					"event: response.output_item.done",
					'data: {"type":"response.output_item.done","item":{"id":"cmp_only","type":"compaction","encrypted_content":"enc"}}',
					"",
					"event: response.completed",
					'data: {"type":"response.completed","response":{"output":[]}}',
					"",
				].join("\n"),
				{ status: 200, headers: { "content-type": "text/event-stream" } },
			)) as unknown as typeof fetch;
		try {
			const result = await provider.compactRemote(
				[{ type: "message", role: "user" }],
				"relay:gpt-5.3-codex",
			);
			expect(result.items).toEqual([
				{ id: "cmp_only", type: "compaction", encrypted_content: "enc" },
			]);
		} finally {
			globalThis.fetch = originalFetch;
		}
	});

	test("throws when remote compact is disabled or the protocol is not Codex", async () => {
		const disabled = new OpenAIProvider({
			id: "relay",
			name: "Codex Relay",
			prefix: "relay",
			apiKey: "sk-test",
			baseUrl: "https://example.invalid/v1",
			defaultModel: "gpt-5.3-codex",
			apiMode: "codex",
			codexRemoteCompact: false,
		});
		const responses = new OpenAIProvider({
			id: "openai",
			name: "OpenAI",
			prefix: "openai",
			apiKey: "sk-test",
			baseUrl: "https://example.invalid/v1",
			defaultModel: "gpt-4.1",
			apiMode: "responses",
		});
		await expect(
			disabled.compactRemote([{ type: "message", role: "user" }], "relay:gpt-5.3-codex"),
		).rejects.toThrow("disabled");
		await expect(
			responses.compactRemote([{ type: "message", role: "user" }], "openai:gpt-4.1"),
		).rejects.toThrow("only supported for Codex");
	});

	test("surfaces HTTP 400 from the compact endpoint", async () => {
		const provider = new OpenAIProvider({
			id: "relay",
			name: "Codex Relay",
			prefix: "relay",
			apiKey: "sk-test",
			baseUrl: "https://example.invalid/v1",
			defaultModel: "gpt-5.3-codex",
			apiMode: "codex",
			codexRemoteCompact: true,
		});
		const originalFetch = globalThis.fetch;
		globalThis.fetch = (async () =>
			new Response(JSON.stringify({ error: { message: "Invalid request payload" } }), {
				status: 400,
				headers: { "content-type": "application/json" },
			})) as unknown as typeof fetch;
		try {
			await expect(
				provider.compactRemote([{ type: "message", role: "user" }], "relay:gpt-5.3-codex"),
			).rejects.toThrow(/400|Invalid request/);
		} finally {
			globalThis.fetch = originalFetch;
		}
	});

	test("rejects a 200 compact stream that has no compaction items", async () => {
		const provider = new OpenAIProvider({
			id: "relay",
			name: "Codex Relay",
			prefix: "relay",
			apiKey: "sk-test",
			baseUrl: "https://example.invalid/v1",
			defaultModel: "gpt-5.3-codex",
			apiMode: "codex",
			codexRemoteCompact: true,
		});
		const originalFetch = globalThis.fetch;
		globalThis.fetch = (async () =>
			new Response(
				[
					"event: response.completed",
					'data: {"type":"response.completed","response":{"output":[]}}',
					"",
				].join("\n"),
				{ status: 200, headers: { "content-type": "text/event-stream" } },
			)) as unknown as typeof fetch;
		try {
			await expect(
				provider.compactRemote([{ type: "message", role: "user" }], "relay:gpt-5.3-codex"),
			).rejects.toThrow("did not contain output items");
		} finally {
			globalThis.fetch = originalFetch;
		}
	});
});

describe("prepareCodexCompactInput", () => {
	test("pairs a trailing function_call_output and keeps one trigger last", () => {
		const prepared = prepareCodexCompactInput(
			[
				{ type: "function_call", call_id: "call_read1", name: "Read" },
				{ type: "compaction_trigger" },
			],
			[{ type: "function_call_output", call_id: "call_read1", output: "file contents" }],
		);
		expect(prepared).toEqual([
			{ type: "function_call", call_id: "call_read1", name: "Read" },
			{ type: "function_call_output", call_id: "call_read1", output: "file contents" },
			{ type: "compaction_trigger" },
		]);
	});

	test("drops an unpaired in-flight function_call when no output exists", () => {
		const prepared = prepareCodexCompactInput([
			{ type: "reasoning", id: "rs_1", encrypted_content: "enc" },
			{ type: "function_call", call_id: "call_read1", name: "Read" },
		]);
		expect(prepared).toEqual([
			{ type: "reasoning", id: "rs_1", encrypted_content: "enc" },
			{ type: "compaction_trigger" },
		]);
	});

	test("findUnpairedCodexFunctionCallIds ignores calls that trailing output closes", () => {
		expect(
			findUnpairedCodexFunctionCallIds(
				[{ type: "function_call", call_id: "call_read1", name: "Read" }],
				[{ type: "function_call_output", call_id: "call_read1", output: "ok" }],
			),
		).toEqual([]);
		expect(
			findUnpairedCodexFunctionCallIds([
				{ type: "function_call", call_id: "call_read1", name: "Read" },
			]),
		).toEqual(["call_read1"]);
	});

	test("clipMessagesBeforeUnpairedToolCalls keeps the open assistant after the new boundary", () => {
		const user: DbMessage = {
			id: "user-1",
			role: "user",
			contentJson: [{ type: "text", text: "hi" }],
			contentText: "hi",
			parentToolUseId: null,
			messageUuid: null,
		};
		const openAssistant: DbMessage = {
			id: "asst-open",
			role: "assistant",
			contentJson: [{ type: "tool_use", id: "call_read1", name: "Read", input: {} }],
			contentText: null,
			parentToolUseId: null,
			messageUuid: null,
			toolCalls: [
				{
					toolUseId: "call_read1",
					toolName: "Read",
					inputJson: {},
					outputJson: null,
					status: "initializing",
				},
			],
		};
		const clipped = clipMessagesBeforeUnpairedToolCalls([user, openAssistant], ["call_read1"]);
		expect(clipped).toEqual({
			messages: [user],
			beforeMessageId: "asst-open",
			clipped: true,
		});
	});

	test("does not double-prefix official ids or duplicate an existing trigger", () => {
		const prepared = prepareCodexCompactInput([
			{ type: "message", role: "user", id: "msg_abc" },
			{ type: "function_call", call_id: "call_read1", name: "Read" },
			{ type: "function_call_output", call_id: "call_read1", output: "ok" },
			{ type: "compaction_trigger" },
		]);
		expect(prepared).toEqual([
			{ type: "message", role: "user", id: "msg_abc" },
			{ type: "function_call", call_id: "call_read1", name: "Read" },
			{ type: "function_call_output", call_id: "call_read1", output: "ok" },
			{ type: "compaction_trigger" },
		]);
	});
});
