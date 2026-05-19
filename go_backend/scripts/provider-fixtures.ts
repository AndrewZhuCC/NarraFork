#!/usr/bin/env bun

import { mkdirSync, mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { z } from "zod/v4";
import { AnthropicProvider } from "../../server/lib/agent/anthropic-provider";
import { ClineProvider } from "../../server/lib/agent/cline-provider";
import { CodexProvider } from "../../server/lib/agent/codex-provider";
import { KiroProvider } from "../../server/lib/agent/kiro-provider";
import { NkpProvider } from "../../server/lib/agent/nkp-provider";
import { NugProvider } from "../../server/lib/agent/nug-provider";
import { appendCodexNativeTools, OpenAIProvider } from "../../server/lib/agent/openai-provider";
import type { ResolvedToolDefinition } from "../../server/lib/agent/types";
import { __setCodexManagerForTests, CodexManager } from "../../server/lib/codex-manager";
import {
	initKiro as initNativeKiro,
	kiroChatRaw as nativeKiroChatRaw,
} from "../../server/lib/kiro/index";
import { __setKiroModuleForTests } from "../../server/lib/kiro-adapter";
import { prepareNkpRequest } from "../../server/lib/nkp-ext";
import { setNugCachedModels } from "../../server/lib/nug-model-cache";

const readTool: ResolvedToolDefinition = {
	name: "Read",
	description: "Read a file",
	parameters: z.object({ file_path: z.string() }),
};

const openaiProvider = new OpenAIProvider({
	id: "fixture-openai",
	name: "Fixture OpenAI",
	prefix: "openai",
	apiKey: "fixture-key",
	baseUrl: "https://example.invalid/v1",
	defaultModel: "gpt-5.3-codex",
	apiMode: "codex",
});
const codexTools = openaiProvider.formatTools([readTool]) as unknown[];
appendCodexNativeTools(codexTools, "gpt-5.3-codex");

const nkpProvider = new NkpProvider({
	id: "fixture-nkp",
	name: "Fixture NKP",
	prefix: "nkp",
	apiKey: "fixture-key",
	baseUrl: "https://nkp.example.invalid",
	defaultModel: "claude-sonnet-4.5",
});
const nkpTools = nkpProvider.formatTools([readTool]);
const nkpRequest: Record<string, unknown> = {
	conversationState: {
		conversationId: "conv_fixture",
		currentMessage: {
			userInputMessage: {
				modelId: "claude-sonnet-4.5",
				content: "hi",
			},
		},
	},
};
prepareNkpRequest(nkpRequest);
const workspaceState = (nkpRequest.conversationState as Record<string, unknown>).workspaceState as
	| Record<string, unknown>
	| undefined;

const anthropicProvider = new AnthropicProvider({
	id: "fixture-anthropic",
	name: "Fixture Anthropic",
	prefix: "anthropic",
	apiKey: "fixture-key",
	baseUrl: "https://anthropic.example.invalid/v1",
	defaultModel: "claude-sonnet-4.5",
	officialApi: true,
});
const anthropicTools = anthropicProvider.formatTools([readTool]);

async function collectOpenAIChatEvents(): Promise<Record<string, unknown>[]> {
	const server = Bun.serve({
		port: 0,
		fetch(request) {
			if (new URL(request.url).pathname !== "/chat/completions") {
				return new Response("not found", { status: 404 });
			}
			return new Response(
				[
					`data: {"choices":[{"index":0,"delta":{"content":"ts chat"}}]}`,
					`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"tool_ts_chat","type":"function","function":{"name":"Read"}}]}}]}`,
					`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\\"file_path\\":"}}]}}]}`,
					`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\\"chat.txt\\"}"}}]}}]}`,
					`data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
					`data: {"choices":[],"usage":{"prompt_tokens":4,"completion_tokens":5,"completion_tokens_details":{"reasoning_tokens":2},"prompt_tokens_details":{"cached_tokens":1}}}`,
					"data: [DONE]",
					"",
				].join("\n\n"),
				{ headers: { "Content-Type": "text/event-stream" } },
			);
		},
	});
	try {
		const provider = new OpenAIProvider({
			id: "fixture-openai-chat-runtime",
			name: "Fixture OpenAI Chat Runtime",
			prefix: "openai",
			apiKey: "fixture-key",
			baseUrl: `http://127.0.0.1:${server.port}`,
			defaultModel: "gpt-test",
			apiMode: "completions",
		});
		const events: Record<string, unknown>[] = [];
		for await (const event of provider.chat({
			conversationId: "conv_fixture",
			content: "hello",
			model: "openai:gpt-test",
			cwd: process.cwd(),
			history: [],
			tools: provider.formatTools([readTool]),
			toolResults: [],
			signal: new AbortController().signal,
		})) {
			events.push(event as Record<string, unknown>);
		}
		return events;
	} finally {
		server.stop(true);
	}
}

async function collectOpenAIResponsesEvents(): Promise<Record<string, unknown>[]> {
	const server = Bun.serve({
		port: 0,
		fetch(request) {
			if (new URL(request.url).pathname !== "/responses") {
				return new Response("not found", { status: 404 });
			}
			return new Response(
				[
					`data: {"type":"response.created","response":{"id":"resp_ts"}}`,
					`data: {"type":"response.output_text.delta","delta":"ts openai"}`,
					`data: {"type":"response.completed","response":{"id":"resp_ts","usage":{"input_tokens":2,"output_tokens":3}}}`,
					"data: [DONE]",
					"",
				].join("\n\n"),
				{ headers: { "Content-Type": "text/event-stream" } },
			);
		},
	});
	try {
		const provider = new OpenAIProvider({
			id: "fixture-openai-runtime",
			name: "Fixture OpenAI Runtime",
			prefix: "openai",
			apiKey: "fixture-key",
			baseUrl: `http://127.0.0.1:${server.port}`,
			defaultModel: "gpt-test",
			apiMode: "responses",
		});
		const events: Record<string, unknown>[] = [];
		for await (const event of provider.chat({
			conversationId: "conv_fixture",
			content: "hello",
			model: "openai:gpt-test",
			cwd: process.cwd(),
			history: [],
			tools: [],
			toolResults: [],
			signal: new AbortController().signal,
		})) {
			events.push(event as Record<string, unknown>);
		}
		return events;
	} finally {
		server.stop(true);
	}
}

async function collectAnthropicEvents(): Promise<Record<string, unknown>[]> {
	const server = Bun.serve({
		port: 0,
		fetch(request) {
			if (new URL(request.url).pathname !== "/messages") {
				return new Response("not found", { status: 404 });
			}
			return new Response(
				[
					`event: message_start\ndata: {"type":"message_start","message":{"id":"msg_ts","usage":{"input_tokens":2,"cache_read_input_tokens":1}}}`,
					`event: content_block_start\ndata: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
					`event: content_block_delta\ndata: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"ts think"}}`,
					`event: content_block_delta\ndata: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig_ts"}}`,
					`event: content_block_stop\ndata: {"type":"content_block_stop","index":0}`,
					`event: content_block_start\ndata: {"type":"content_block_start","index":3,"content_block":{"type":"redacted_thinking","data":"redacted_ts"}}`,
					`event: content_block_stop\ndata: {"type":"content_block_stop","index":3}`,
					`event: content_block_start\ndata: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`,
					`event: content_block_delta\ndata: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"ts anthropic"}}`,
					`event: content_block_start\ndata: {"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"tool_ts","name":"Read","input":{}}}`,
					`event: content_block_delta\ndata: {"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\\"file_path\\":\\"ts.txt\\"}"}}`,
					`event: content_block_stop\ndata: {"type":"content_block_stop","index":2}`,
					`event: message_delta\ndata: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":3,"cache_creation_input_tokens":4,"cache_creation":{"ephemeral_5m_input_tokens":5,"ephemeral_1h_input_tokens":6}}}`,
					`event: message_stop\ndata: {"type":"message_stop"}`,
					"",
				].join("\n\n"),
				{ headers: { "Content-Type": "text/event-stream" } },
			);
		},
	});
	try {
		const provider = new AnthropicProvider({
			id: "fixture-anthropic-runtime",
			name: "Fixture Anthropic Runtime",
			prefix: "anthropic",
			apiKey: "fixture-key",
			baseUrl: `http://127.0.0.1:${server.port}`,
			defaultModel: "claude-test",
			officialApi: true,
		});
		const events: Record<string, unknown>[] = [];
		for await (const event of provider.chat({
			conversationId: "conv_fixture",
			content: "hello",
			model: "anthropic:claude-test",
			cwd: process.cwd(),
			history: [],
			tools: [],
			toolResults: [],
			reasoningEffort: "medium",
			signal: new AbortController().signal,
		})) {
			events.push(event as Record<string, unknown>);
		}
		return events;
	} finally {
		server.stop(true);
	}
}

async function collectAnthropicRequestFixture(): Promise<Record<string, unknown>> {
	let officialBody: Record<string, unknown> = {};
	let officialBeta = "";
	const officialServer = Bun.serve({
		port: 0,
		async fetch(request) {
			officialBeta = request.headers.get("anthropic-beta") ?? "";
			officialBody = (await request.json().catch(() => ({}))) as Record<string, unknown>;
			return new Response(
				[
					`event: message_start\ndata: {"type":"message_start","message":{"id":"msg_req"}}`,
					`event: content_block_delta\ndata: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`,
					`event: message_stop\ndata: {"type":"message_stop"}`,
					"",
				].join("\n\n"),
				{ headers: { "Content-Type": "text/event-stream" } },
			);
		},
	});
	try {
		const official = new AnthropicProvider({
			id: "fixture-anthropic-request-official",
			name: "Fixture Anthropic Request Official",
			prefix: "anthropic",
			apiKey: "fixture-key",
			baseUrl: `http://127.0.0.1:${officialServer.port}`,
			defaultModel: "claude-sonnet-4.5",
			officialApi: true,
		});
		for await (const _event of official.chat({
			conversationId: "conv_fixture",
			content: "hello",
			model: "anthropic:claude-sonnet-4.5",
			cwd: process.cwd(),
			history: [],
			tools: [],
			toolResults: [],
			reasoningEffort: "medium",
			signal: new AbortController().signal,
		})) {
			// Drain stream.
		}
	} finally {
		officialServer.stop(true);
	}

	let compatibleBody: Record<string, unknown> = {};
	const compatibleServer = Bun.serve({
		port: 0,
		async fetch(request) {
			compatibleBody = (await request.json().catch(() => ({}))) as Record<string, unknown>;
			return new Response(
				[
					`event: message_start\ndata: {"type":"message_start","message":{"id":"msg_compatible"}}`,
					`event: content_block_delta\ndata: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`,
					`event: message_stop\ndata: {"type":"message_stop"}`,
					"",
				].join("\n\n"),
				{ headers: { "Content-Type": "text/event-stream" } },
			);
		},
	});
	try {
		const compatible = new AnthropicProvider({
			id: "fixture-anthropic-request-compatible",
			name: "Fixture Anthropic Request Compatible",
			prefix: "anthropic",
			apiKey: "fixture-key",
			baseUrl: `http://127.0.0.1:${compatibleServer.port}`,
			defaultModel: "claude-test",
			officialApi: false,
		});
		for await (const _event of compatible.chat({
			conversationId: "conv_fixture",
			content: "hello",
			model: "anthropic:claude-test",
			cwd: process.cwd(),
			history: [
				{
					role: "assistant",
					content: [
						{ type: "thinking", thinking: "private", signature: "sig" },
						{ type: "redacted_thinking", data: "secret" },
						{ type: "text", text: "visible" },
					],
				},
			],
			tools: [],
			toolResults: [],
			signal: new AbortController().signal,
		})) {
			// Drain stream.
		}
	} finally {
		compatibleServer.stop(true);
	}
	const compatibleMessages = compatibleBody.messages as Array<Record<string, unknown>> | undefined;
	const cleanedContent = compatibleMessages?.[0]?.content as
		| Array<Record<string, unknown>>
		| undefined;
	return {
		officialBeta,
		officialThinkingType: (officialBody.thinking as Record<string, unknown> | undefined)?.type,
		compatibleCleanedTypes: cleanedContent?.map((block) => block.type) ?? [],
	};
}

async function collectClineEvents(): Promise<Record<string, unknown>[]> {
	const server = Bun.serve({
		port: 0,
		fetch(request) {
			if (new URL(request.url).pathname !== "/chat/completions") {
				return new Response("not found", { status: 404 });
			}
			return new Response(
				[
					`data: {"choices":[{"index":0,"delta":{"content":"ts cline"}}]}`,
					`data: {"choices":[],"usage":{"prompt_tokens":2,"completion_tokens":3}}`,
					"data: [DONE]",
					"",
				].join("\n\n"),
				{ headers: { "Content-Type": "text/event-stream" } },
			);
		},
	});
	try {
		const provider = new ClineProvider({
			id: "fixture-cline-runtime",
			name: "Fixture Cline Runtime",
			prefix: "cline",
			accessToken: "fixture-token",
			baseUrl: `http://127.0.0.1:${server.port}`,
			defaultModel: "anthropic/claude-sonnet-4",
		});
		const events: Record<string, unknown>[] = [];
		for await (const event of provider.chat({
			conversationId: "conv_fixture",
			content: "hello",
			model: "cline:anthropic/claude-sonnet-4",
			cwd: process.cwd(),
			history: [],
			tools: [],
			toolResults: [],
			signal: new AbortController().signal,
		})) {
			events.push(event as Record<string, unknown>);
		}
		return events;
	} finally {
		server.stop(true);
	}
}

async function collectKiroEvents(): Promise<Record<string, unknown>> {
	let capturedRequest: Record<string, unknown> | undefined;
	__setKiroModuleForTests({
		isKiroAvailable: () => true,
		async *kiroChatRaw(request) {
			capturedRequest = request as unknown as Record<string, unknown>;
			yield {
				eventType: "credentialIdEvent",
				data: { credentialId: "kiro_cred_ts" },
			};
			yield {
				eventType: "reasoningContentEvent",
				data: { content: "ts kiro reasoning" },
			};
			yield {
				eventType: "assistantResponseEvent",
				data: {
					content: "ts kiro",
					messageId: "msg_kiro",
					conversationId: "conv_kiro_response",
				},
			};
			yield {
				eventType: "toolUseEvent",
				data: { toolUseId: "tool_kiro", name: "Read", input: '{"file_path":"kiro.txt"}' },
			};
			yield {
				eventType: "toolUseEvent",
				data: { toolUseId: "tool_kiro", name: "Read", stop: true },
			};
			yield {
				eventType: "contextUsageEvent",
				data: { contextUsagePercentage: 12.5 },
			};
			yield {
				eventType: "meteringEvent",
				data: { usage: 7, unit: "credit", unitPlural: "credits" },
			};
		},
	});
	try {
		const provider = new KiroProvider();
		const events: Record<string, unknown>[] = [];
		for await (const event of provider.chat({
			conversationId: "conv_kiro_fixture",
			content: "hello",
			model: "kiro:claude-sonnet-4.5",
			cwd: process.cwd(),
			history: [],
			tools: [],
			toolResults: [],
			signal: new AbortController().signal,
		})) {
			events.push(event as Record<string, unknown>);
		}
		return {
			events,
			request: capturedRequest,
		};
	} finally {
		__setKiroModuleForTests(null);
	}
}

function u32be(value: number): Uint8Array {
	const out = new Uint8Array(4);
	new DataView(out.buffer).setUint32(0, value, false);
	return out;
}

function u16be(value: number): Uint8Array {
	const out = new Uint8Array(2);
	new DataView(out.buffer).setUint16(0, value, false);
	return out;
}

function concatBytes(parts: Uint8Array[]): Uint8Array {
	const total = parts.reduce((sum, part) => sum + part.length, 0);
	const out = new Uint8Array(total);
	let offset = 0;
	for (const part of parts) {
		out.set(part, offset);
		offset += part.length;
	}
	return out;
}

function awsStringHeader(name: string, value: string): Uint8Array {
	const encoder = new TextEncoder();
	const nameBytes = encoder.encode(name);
	const valueBytes = encoder.encode(value);
	return concatBytes([
		new Uint8Array([nameBytes.length]),
		nameBytes,
		new Uint8Array([7]),
		u16be(valueBytes.length),
		valueBytes,
	]);
}

function awsEventFrame(eventType: string, data: Record<string, unknown>): Uint8Array {
	const encoder = new TextEncoder();
	const headers = concatBytes([
		awsStringHeader(":message-type", "event"),
		awsStringHeader(":event-type", eventType),
	]);
	const payload = encoder.encode(JSON.stringify(data));
	const totalLength = 12 + headers.length + payload.length + 4;
	const prelude = concatBytes([u32be(totalLength), u32be(headers.length)]);
	const preludeCrc = u32be(Bun.hash.crc32(prelude));
	const withoutMessageCrc = concatBytes([prelude, preludeCrc, headers, payload]);
	const messageCrc = u32be(Bun.hash.crc32(withoutMessageCrc));
	return concatBytes([withoutMessageCrc, messageCrc]);
}

async function collectKiroNativeModuleEvents(): Promise<Record<string, unknown>> {
	const home = mkdtempSync(join(tmpdir(), "narrafork-kiro-native-fixture-"));
	const credentialsPath = join(home, "credentials.json");
	const configPath = join(home, "config.json");
	writeFileSync(
		credentialsPath,
		JSON.stringify([
			{
				id: "kiro-native-fixture",
				refreshToken: "refresh-fixture",
				accessToken: "access-fixture",
				expiresAt: new Date(Date.now() + 3600_000).toISOString(),
				priority: 0,
				disabled: false,
				availableModels: ["deepseek-3.2"],
			},
		]),
	);
	writeFileSync(configPath, JSON.stringify({ region: "us-east-1", kiroVersion: "1.26.1" }));
	const frames = concatBytes([
		awsEventFrame("assistantResponseEvent", {
			content: "ts kiro native",
			messageId: "msg_kiro_native",
		}),
		awsEventFrame("contextUsageEvent", { contextUsagePercentage: 33 }),
	]);
	const originalFetch = globalThis.fetch;
	let requestModel = "";
	let requestTarget = "";
	globalThis.fetch = async (_input, init) => {
		const body = JSON.parse(String(init?.body ?? "{}")) as Record<string, unknown>;
		const state = body.conversationState as Record<string, unknown>;
		const current = state.currentMessage as Record<string, unknown>;
		const user = current.userInputMessage as Record<string, unknown>;
		requestModel = String(user.modelId ?? "");
		requestTarget = new Headers(init?.headers).get("x-amz-target") ?? "";
		return new Response(
			new ReadableStream({
				start(controller) {
					controller.enqueue(frames);
					controller.close();
				},
			}),
			{ status: 200 },
		);
	};
	try {
		const initialized = initNativeKiro({
			credentialsPath,
			configPath,
			defaultModel: "deepseek-3.2" as never,
		});
		const events: Record<string, unknown>[] = [];
		for await (const event of nativeKiroChatRaw({
			conversationState: {
				conversationId: "conv_kiro_native_fixture",
				currentMessage: {
					userInputMessage: {
						content: "hello",
						modelId: "deepseek-3.2" as never,
						userInputMessageContext: {},
					},
				},
			},
		})) {
			events.push(event as unknown as Record<string, unknown>);
		}
		return { initialized, requestModel, requestTarget, events };
	} finally {
		globalThis.fetch = originalFetch;
	}
}

async function collectNkpEvents(): Promise<Record<string, unknown>[]> {
	const server = Bun.serve({
		port: 0,
		fetch(request) {
			if (new URL(request.url).pathname !== "/v1/kiro/chat") {
				return new Response("not found", { status: 404 });
			}
			return new Response(
				[
					`event: assistantResponseEvent\ndata: {"content":"ts nkp","messageId":"msg_nkp"}`,
					"",
				].join("\n\n"),
				{ headers: { "Content-Type": "text/event-stream" } },
			);
		},
	});
	try {
		const provider = new NkpProvider({
			id: "fixture-nkp-runtime",
			name: "Fixture NKP Runtime",
			prefix: "nkp",
			apiKey: "fixture-key",
			baseUrl: `http://127.0.0.1:${server.port}`,
			defaultModel: "claude-sonnet-4.5",
		});
		const events: Record<string, unknown>[] = [];
		for await (const event of provider.chat({
			conversationId: "conv_fixture",
			content: "hello",
			model: "nkp:claude-sonnet-4.5",
			cwd: process.cwd(),
			history: [],
			tools: [],
			toolResults: [],
			signal: new AbortController().signal,
		})) {
			events.push(event as Record<string, unknown>);
		}
		return events;
	} finally {
		server.stop(true);
	}
}

async function collectNugKiroEvents(): Promise<Record<string, unknown>[]> {
	const server = Bun.serve({
		port: 0,
		fetch(request) {
			if (new URL(request.url).pathname !== "/v1/kiro/chat") {
				return new Response("not found", { status: 404 });
			}
			return new Response(
				[
					`event: assistantResponseEvent\ndata: {"content":"ts nug kiro","messageId":"msg_nug_kiro"}`,
					`event: contextUsageEvent\ndata: {"contextUsagePercentage":22}`,
					`event: meteringEvent\ndata: {"usage":4,"unit":"credit","unitPlural":"credits"}`,
					"",
				].join("\n\n"),
				{ headers: { "Content-Type": "text/event-stream" } },
			);
		},
	});
	try {
		setNugCachedModels("fixture-nug-kiro-runtime", [
			{
				id: "kiro:claude-sonnet-4.5",
				name: "Fixture NUG Kiro",
				channel: "kiro",
				channel_type: "kiro",
				model: "claude-sonnet-4.5",
				available: true,
			},
		]);
		const provider = new NugProvider({
			id: "fixture-nug-kiro-runtime",
			name: "Fixture NUG Kiro Runtime",
			prefix: "nug",
			apiKey: "fixture-key",
			baseUrl: `http://127.0.0.1:${server.port}`,
			defaultModel: "kiro:claude-sonnet-4.5",
		});
		const events: Record<string, unknown>[] = [];
		for await (const event of provider.chat({
			conversationId: "conv_fixture",
			content: "hello",
			model: "nug:kiro:claude-sonnet-4.5",
			cwd: process.cwd(),
			history: [],
			tools: [],
			toolResults: [],
			signal: new AbortController().signal,
		})) {
			events.push(event as Record<string, unknown>);
		}
		return events;
	} finally {
		server.stop(true);
	}
}

async function collectNugOpenAIEvents(): Promise<Record<string, unknown>[]> {
	const server = Bun.serve({
		port: 0,
		async fetch(request) {
			const url = new URL(request.url);
			if (url.pathname !== "/v1/chat/completions") {
				return new Response("not found", { status: 404 });
			}
			const body = (await request.json().catch(() => ({}))) as Record<string, unknown>;
			return new Response(
				[
					`data: {"choices":[{"index":0,"delta":{"content":"ts nug openai"}}]}`,
					`data: {"choices":[],"usage":{"prompt_tokens":2,"completion_tokens":3},"_request_model":${JSON.stringify(body.model)}}`,
					"data: [DONE]",
					"",
				].join("\n\n"),
				{ headers: { "Content-Type": "text/event-stream" } },
			);
		},
	});
	try {
		setNugCachedModels("fixture-nug-runtime", [
			{
				id: "openai:gpt-test",
				name: "Fixture NUG OpenAI",
				channel: "openai",
				channel_type: "openai",
				model: "gpt-test",
				available: true,
			},
		]);
		const provider = new NugProvider({
			id: "fixture-nug-runtime",
			name: "Fixture NUG Runtime",
			prefix: "nug",
			apiKey: "fixture-key",
			baseUrl: `http://127.0.0.1:${server.port}`,
			defaultModel: "openai:gpt-test",
		});
		const events: Record<string, unknown>[] = [];
		for await (const event of provider.chat({
			conversationId: "conv_fixture",
			content: "hello",
			model: "nug:openai:gpt-test",
			cwd: process.cwd(),
			history: [],
			tools: [],
			toolResults: [],
			signal: new AbortController().signal,
		})) {
			events.push(event as Record<string, unknown>);
		}
		return events;
	} finally {
		server.stop(true);
	}
}

function writeCodexFixtureCredentials(home: string): void {
	mkdirSync(join(home, ".narrafork"), { recursive: true });
	writeFileSync(
		join(home, ".narrafork", "codex-credentials.json"),
		JSON.stringify([
			{
				id: "plus-busy",
				refreshToken: "plus-refresh",
				accessToken: "plus-token",
				expiresAt: Date.now() + 3600_000,
				accountId: "plus",
				priority: 0,
				disabled: false,
				usage: {
					plan_type: "plus",
					queriedAt: new Date().toISOString(),
					primary_window: {
						used_percent: 90,
						remaining_percent: 10,
						reset_at: Math.floor(Date.now() / 1000) + 3600,
						reset_after_seconds: 3600,
						window_type: "5h",
					},
				},
			},
			{
				id: "pro-a",
				refreshToken: "pro-a-refresh",
				accessToken: "pro-a-token",
				expiresAt: Date.now() + 3600_000,
				accountId: "pro-a",
				priority: 1,
				disabled: false,
				usage: {
					plan_type: "pro",
					queriedAt: new Date().toISOString(),
					primary_window: {
						used_percent: 20,
						remaining_percent: 80,
						reset_at: Math.floor(Date.now() / 1000) + 3600,
						reset_after_seconds: 3600,
						window_type: "5h",
					},
				},
			},
			{
				id: "pro-b",
				refreshToken: "pro-b-refresh",
				accessToken: "pro-b-token",
				expiresAt: Date.now() + 3600_000,
				accountId: "pro-b",
				priority: 2,
				disabled: false,
				usage: {
					plan_type: "pro",
					queriedAt: new Date().toISOString(),
					primary_window: {
						used_percent: 40,
						remaining_percent: 60,
						reset_at: Math.floor(Date.now() / 1000) + 3600,
						reset_after_seconds: 3600,
						window_type: "5h",
					},
				},
			},
		]),
	);
}

async function collectCodexProviderFixture(): Promise<Record<string, unknown>> {
	const home = mkdtempSync(join(tmpdir(), "narrafork-codex-provider-fixture-"));
	writeCodexFixtureCredentials(home);
	const manager = new CodexManager({ homeDir: home, registerProcessHooks: false });
	manager.setLoadBalancingMode("tier-balanced");
	manager.setTierOrder(["pro", "plus", "free"]);
	__setCodexManagerForTests(manager);
	const originalFetch = globalThis.fetch;
	let requestBody: Record<string, unknown> | undefined;
	let authorization = "";
	globalThis.fetch = async (input, init) => {
		const url =
			typeof input === "string" ? input : input instanceof URL ? input.toString() : input.url;
		if (url.endsWith("/responses")) {
			requestBody = JSON.parse(String(init?.body ?? "{}")) as Record<string, unknown>;
			const headers = new Headers(init?.headers);
			authorization = headers.get("authorization") ?? "";
			return new Response(
				[
					`data: {"type":"response.created","response":{"id":"resp_codex"}}`,
					`data: {"type":"response.output_text.delta","delta":"ts codex provider"}`,
					`data: {"type":"response.completed","response":{"id":"resp_codex","usage":{"input_tokens":2,"output_tokens":3}}}`,
					"data: [DONE]",
					"",
				].join("\n\n"),
				{ headers: { "Content-Type": "text/event-stream" } },
			);
		}
		return originalFetch(input, init);
	};
	try {
		const provider = new CodexProvider({ useWebSocket: false });
		const events: Record<string, unknown>[] = [];
		for await (const event of provider.chat({
			conversationId: "conv_codex_fixture",
			content: "hello",
			model: "codex:gpt-5.3-codex",
			cwd: process.cwd(),
			history: [],
			tools: [],
			toolResults: [],
			stickySessionKey: "sticky-codex-fixture",
			signal: new AbortController().signal,
		})) {
			events.push(event as Record<string, unknown>);
		}
		return {
			events,
			requestBody,
			authorizationPrefix: authorization.slice(0, "Bearer ".length),
		};
	} finally {
		globalThis.fetch = originalFetch;
		__setCodexManagerForTests(undefined);
	}
}

async function collectCodexRebuildHistoryFixture(): Promise<Record<string, unknown>> {
	const home = mkdtempSync(join(tmpdir(), "narrafork-codex-rebuild-history-fixture-"));
	mkdirSync(join(home, ".narrafork"), { recursive: true });
	writeFileSync(
		join(home, ".narrafork", "codex-credentials.json"),
		JSON.stringify([
			{
				id: "bad",
				accessToken: "bad-token",
				accountId: "bad-acct",
				priority: 0,
				disabled: false,
			},
			{
				id: "good",
				accessToken: "good-token",
				accountId: "good-acct",
				priority: 1,
				disabled: false,
			},
		]),
	);
	const manager = new CodexManager({ homeDir: home, registerProcessHooks: false });
	__setCodexManagerForTests(manager);
	let requests = 0;
	let firstAuthorization = "";
	const originalFetch = globalThis.fetch;
	globalThis.fetch = async (input, init) => {
		const url =
			typeof input === "string" ? input : input instanceof URL ? input.toString() : input.url;
		if (!url.endsWith("/responses")) {
			return originalFetch(input, init);
		}
		requests++;
		if (requests === 1) {
			firstAuthorization = new Headers(init?.headers).get("authorization") ?? "";
		}
		const encoder = new TextEncoder();
		let sentPartial = false;
		return new Response(
			new ReadableStream({
				pull(controller) {
					if (!sentPartial) {
						sentPartial = true;
						controller.enqueue(
							encoder.encode(
								[
									`data: {"type":"response.created","response":{"id":"resp_codex_rebuild"}}`,
									`data: {"type":"response.output_text.delta","delta":"partial"}`,
									"",
								].join("\n\n"),
							),
						);
						return;
					}
					controller.error(
						new Error('usage_limit_reached: usage limit reached {"resets_at":2000000000}'),
					);
				},
			}),
			{ headers: { "Content-Type": "text/event-stream" } },
		);
	};
	try {
		const provider = new CodexProvider({ useWebSocket: false });
		const events: Record<string, unknown>[] = [];
		let error: Record<string, unknown> | null = null;
		try {
			for await (const event of provider.chat({
				conversationId: "conv_codex_rebuild_fixture",
				content: "hello",
				model: "codex:gpt-5.3-codex",
				cwd: process.cwd(),
				history: [],
				tools: [],
				toolResults: [],
				stickySessionKey: "nar-rebuild-fixture",
				signal: new AbortController().signal,
			})) {
				events.push(event as Record<string, unknown>);
			}
		} catch (err) {
			if (err && typeof err === "object") {
				error = {
					name: stringValue((err as Record<string, unknown>).name),
					message: stringValue((err as Record<string, unknown>).message),
					code: stringValue((err as Record<string, unknown>).code),
					previousCredentialId: stringValue((err as Record<string, unknown>).previousCredentialId),
					operation: stringValue((err as Record<string, unknown>).operation),
				};
			} else {
				error = { name: typeof err, message: String(err ?? "") };
			}
		}
		const snapshot = manager.snapshot();
		const failedCredentialId = stringValue(error?.previousCredentialId ?? events[0]?.credentialId);
		const failedEntry = snapshot.entries.find((entry) => entry.id === failedCredentialId);
		return {
			requestCount: requests,
			firstAuthorizationPrefix: firstAuthorization.slice(0, "Bearer ".length),
			firstCredentialId: stringValue(events[0]?.credentialId),
			failedCredentialId,
			partialText: events.map((event) => stringValue(event.text)).join(""),
			events,
			error,
			failedDisabledReason: failedEntry?.disabledReason ?? null,
			failedQuotaResetsAt: failedEntry?.quotaResetsAt ?? null,
			available: snapshot.available,
		};
	} finally {
		globalThis.fetch = originalFetch;
		__setCodexManagerForTests(undefined);
	}
}

async function collectProviderErrorFixtures(): Promise<Record<string, unknown>> {
	const openAIChatServer = Bun.serve({
		port: 0,
		fetch(request) {
			if (new URL(request.url).pathname !== "/chat/completions") {
				return new Response("not found", { status: 404 });
			}
			return new Response(
				[
					`data: {"error":{"message":"ts rate limited","type":"rate_limit_error","code":"rate_limit"}}`,
					"",
				].join("\n\n"),
				{ headers: { "Content-Type": "text/event-stream" } },
			);
		},
	});
	const openAIChat: Record<string, unknown>[] = [];
	try {
		const provider = new OpenAIProvider({
			id: "fixture-openai-chat-error",
			name: "Fixture OpenAI Chat Error",
			prefix: "openai",
			apiKey: "fixture-key",
			baseUrl: `http://127.0.0.1:${openAIChatServer.port}`,
			defaultModel: "gpt-test",
			apiMode: "completions",
		});
		for await (const event of provider.chat({
			conversationId: "conv_fixture",
			content: "hello",
			model: "openai:gpt-test",
			cwd: process.cwd(),
			history: [],
			tools: [],
			toolResults: [],
			signal: new AbortController().signal,
		})) {
			openAIChat.push(event as Record<string, unknown>);
		}
	} finally {
		openAIChatServer.stop(true);
	}

	const openAIResponsesServer = Bun.serve({
		port: 0,
		fetch(request) {
			if (new URL(request.url).pathname !== "/responses") {
				return new Response("not found", { status: 404 });
			}
			return new Response(
				[
					`data: {"type":"response.created","response":{"id":"resp_error_ts"}}`,
					`data: {"type":"response.failed","response":{"id":"resp_error_ts","error":{"message":"ts failed","code":"server_error"}}}`,
					"",
				].join("\n\n"),
				{ headers: { "Content-Type": "text/event-stream" } },
			);
		},
	});
	const openAIResponses: Record<string, unknown>[] = [];
	try {
		const provider = new OpenAIProvider({
			id: "fixture-openai-responses-error",
			name: "Fixture OpenAI Responses Error",
			prefix: "openai",
			apiKey: "fixture-key",
			baseUrl: `http://127.0.0.1:${openAIResponsesServer.port}`,
			defaultModel: "gpt-test",
			apiMode: "responses",
		});
		for await (const event of provider.chat({
			conversationId: "conv_fixture",
			content: "hello",
			model: "openai:gpt-test",
			cwd: process.cwd(),
			history: [],
			tools: [],
			toolResults: [],
			signal: new AbortController().signal,
		})) {
			openAIResponses.push(event as Record<string, unknown>);
		}
	} finally {
		openAIResponsesServer.stop(true);
	}

	const anthropicServer = Bun.serve({
		port: 0,
		fetch(request) {
			if (new URL(request.url).pathname !== "/messages") {
				return new Response("not found", { status: 404 });
			}
			return new Response(
				[
					`event: error\ndata: {"type":"error","error":{"type":"overloaded_error","message":"ts overloaded"}}`,
					"",
				].join("\n\n"),
				{ headers: { "Content-Type": "text/event-stream" } },
			);
		},
	});
	const anthropic: Record<string, unknown>[] = [];
	try {
		const provider = new AnthropicProvider({
			id: "fixture-anthropic-error",
			name: "Fixture Anthropic Error",
			prefix: "anthropic",
			apiKey: "fixture-key",
			baseUrl: `http://127.0.0.1:${anthropicServer.port}`,
			defaultModel: "claude-test",
			officialApi: true,
		});
		for await (const event of provider.chat({
			conversationId: "conv_fixture",
			content: "hello",
			model: "anthropic:claude-test",
			cwd: process.cwd(),
			history: [],
			tools: [],
			toolResults: [],
			signal: new AbortController().signal,
		})) {
			anthropic.push(event as Record<string, unknown>);
		}
	} finally {
		anthropicServer.stop(true);
	}

	return { openAIChat, openAIResponses, anthropic };
}

async function collectCodexManagerFixture(): Promise<Record<string, unknown>> {
	const home = mkdtempSync(join(tmpdir(), "narrafork-codex-fixture-"));
	writeCodexFixtureCredentials(home);
	const manager = new CodexManager({ homeDir: home, registerProcessHooks: false });
	manager.setLoadBalancingMode("tier-balanced");
	manager.setTierOrder(["pro", "plus", "free"]);
	const ctx = await manager.acquireContext("session-fixture");
	const stickyAgain = await manager.acquireContext("session-fixture");
	const hasMoreAfterQuota = manager.reportQuotaExhausted(ctx.id, Date.now() + 3600_000);
	const afterQuota = await manager.acquireContext("session-fixture");
	manager.reportFailure("plus-busy");
	manager.reportFailure("plus-busy");
	manager.reportFailure("plus-busy");
	const snapshot = manager.snapshot();
	const quotaEntry = snapshot.entries.find((entry) => entry.id === ctx.id);
	const failureEntry = snapshot.entries.find((entry) => entry.id === "plus-busy");
	manager.dispose();
	return {
		selectedId: ctx.id,
		selectedToken: ctx.token,
		selectedPlanType: ctx.credential.usage?.plan_type,
		stickyAgainId: stickyAgain.id,
		afterQuotaId: afterQuota.id,
		hasMoreAfterQuota,
		quotaDisabledReason: quotaEntry?.disabledReason,
		failureDisabledReason: failureEntry?.disabledReason,
		stickySessionCount: snapshot.stickySessionCount,
		effectiveTierOrder: snapshot.effectiveTierOrder,
		available: snapshot.available,
		usageKnownAccounts: snapshot.usageSummary.totalKnownUsageAccounts,
		usageForecastPoints: snapshot.usageForecast.points.length,
	};
}

function stringValue(value: unknown): string {
	if (typeof value === "string") return value;
	if (value == null) return "";
	return String(value);
}

const fixture = {
	codexToolTypes: codexTools.map((tool) => (tool as Record<string, unknown>).type),
	codexFunctionToolShape: codexTools.find(
		(tool) => (tool as Record<string, unknown>).type === "function",
	),
	nkpToolShape: nkpTools[0],
	nkpWorkspaceStateShape: {
		hasSessionEpoch: typeof workspaceState?.sessionEpoch === "number",
		traceIdLength: typeof workspaceState?.traceId === "string" ? workspaceState.traceId.length : 0,
	},
	anthropicToolShape: anthropicTools[0],
	anthropicSystemCacheControl: {
		type: "text",
		cacheControlType: "ephemeral",
	},
	tsOpenAIChatEvents: await collectOpenAIChatEvents(),
	tsOpenAIResponsesEvents: await collectOpenAIResponsesEvents(),
	tsAnthropicEvents: await collectAnthropicEvents(),
	tsAnthropicRequest: await collectAnthropicRequestFixture(),
	tsClineEvents: await collectClineEvents(),
	tsKiroProvider: await collectKiroEvents(),
	tsKiroNativeModule: await collectKiroNativeModuleEvents(),
	tsNkpEvents: await collectNkpEvents(),
	tsNugKiroEvents: await collectNugKiroEvents(),
	tsNugOpenAIEvents: await collectNugOpenAIEvents(),
	tsCodexProvider: await collectCodexProviderFixture(),
	tsCodexRebuildHistory: await collectCodexRebuildHistoryFixture(),
	tsProviderErrors: await collectProviderErrorFixtures(),
	tsCodexManager: await collectCodexManagerFixture(),
};

process.stdout.write(`${JSON.stringify(fixture)}\n`);
process.exit(0);
