import { describe, expect, it } from "bun:test";
import {
	AnthropicProvider,
	parseAnthropicSSEStream,
} from "../../../server/lib/agent/anthropic-provider";
import { LoopTranscript } from "../../../server/lib/agent/loop-transcript";
import type { DbMessage } from "../../../server/lib/agent/provider";
import type { ContentBlock } from "../../../server/lib/agent/types";

/** A step as the loop keeps it, its calls settled, projected like every request. */
async function liveHistory(
	provider: AnthropicProvider,
	blocks: ContentBlock[],
	results: Record<string, string> = {},
): Promise<unknown[]> {
	const transcript = new LoopTranscript();
	transcript.appendStep(blocks);
	for (const [toolUseId, output] of Object.entries(results))
		transcript.settleTool(toolUseId, { output });
	return (await provider.buildHistory(transcript.rows, "claude-sonnet-4-5")).history;
}

function sseStream(events: Array<Record<string, unknown>>): ReadableStream<Uint8Array> {
	const encoder = new TextEncoder();
	return new ReadableStream({
		start(controller) {
			for (const event of events) {
				controller.enqueue(encoder.encode(`data: ${JSON.stringify(event)}\n\n`));
			}
			controller.close();
		},
	});
}

describe("AnthropicProvider thinking continuation", () => {
	it("preserves separate thinking block indexes and signatures from SSE", async () => {
		const events = await Array.fromAsync(
			parseAnthropicSSEStream(
				sseStream([
					{ type: "message_start", message: { id: "msg_1" } },
					{ type: "content_block_start", index: 0, content_block: { type: "thinking" } },
					{
						type: "content_block_delta",
						index: 0,
						delta: { type: "thinking_delta", thinking: "first" },
					},
					{
						type: "content_block_delta",
						index: 0,
						delta: { type: "signature_delta", signature: "sig-1" },
					},
					{ type: "content_block_stop", index: 0 },
					{ type: "content_block_start", index: 2, content_block: { type: "thinking" } },
					{
						type: "content_block_delta",
						index: 2,
						delta: { type: "thinking_delta", thinking: "second" },
					},
					{
						type: "content_block_delta",
						index: 2,
						delta: { type: "signature_delta", signature: "sig-2" },
					},
					{ type: "content_block_stop", index: 2 },
				]),
			),
		);

		const reasoningEvents = events.filter((event) => event.reasoning);
		expect(reasoningEvents).toEqual([
			expect.objectContaining({
				reasoning: "first",
				reasoningOutputIndex: 0,
				reasoningMetadata: { anthropic: { blockIndex: 0 } },
			}),
			expect.objectContaining({
				reasoning: "second",
				reasoningOutputIndex: 2,
				reasoningMetadata: { anthropic: { blockIndex: 2 } },
			}),
		]);

		const signatureEvents = events.filter((event) => event.reasoningMetadata?.anthropic?.signature);
		expect(signatureEvents).toEqual([
			expect.objectContaining({
				reasoningOutputIndex: 0,
				reasoningMetadata: { anthropic: { blockIndex: 0, signature: "sig-1" } },
			}),
			expect.objectContaining({
				reasoningOutputIndex: 2,
				reasoningMetadata: { anthropic: { blockIndex: 2, signature: "sig-2" } },
			}),
		]);
	});

	it("replays multiple persisted reasoning blocks as separate signed thinking blocks", async () => {
		const provider = new AnthropicProvider({
			id: "test",
			name: "Test",
			prefix: "anthropic",
			apiKey: "test-key",
			baseUrl: "https://api.anthropic.com/v1",
			defaultModel: "claude-sonnet-4-5",
		});
		const dbMessages: DbMessage[] = [
			{
				id: "u1",
				role: "user",
				contentJson: [{ type: "text", text: "run tools" }],
				contentText: "run tools",
				parentToolUseId: null,
				messageUuid: null,
			},
			{
				id: "a1",
				role: "assistant",
				contentJson: [
					{
						type: "reasoning",
						text: "first",
						providerMetadata: {
							anthropic: { blockIndex: 0, signature: "sig-1" },
							signatureSource: "anthropic",
						},
					},
					{ type: "text", text: "ok" },
					{
						type: "reasoning",
						text: "second",
						providerMetadata: {
							anthropic: { blockIndex: 2, signature: "sig-2" },
							signatureSource: "anthropic",
						},
					},
					{ type: "tool_use", id: "toolu_1" },
				],
				contentText: "ok",
				parentToolUseId: null,
				messageUuid: null,
				toolCalls: [
					{
						toolUseId: "toolu_1",
						toolName: "Read",
						inputJson: { file_path: "/tmp/a" },
						outputJson: "done",
						status: "success",
					},
				],
			},
		];

		const { history } = await provider.buildHistory(dbMessages, "claude-sonnet-4-5");
		const assistant = (history as Array<{ role: string; content: unknown }>).find(
			(message) => message.role === "assistant",
		);

		expect(assistant?.content).toEqual([
			{ type: "thinking", thinking: "first", signature: "sig-1" },
			{ type: "text", text: "ok" },
			{ type: "thinking", thinking: "second", signature: "sig-2" },
			{ type: "tool_use", id: "toolu_1", name: "Read", input: { file_path: "/tmp/a" } },
		]);
	});

	it("drops signatures minted by a different upstream but keeps thinking text", async () => {
		const provider = new AnthropicProvider({
			id: "test",
			name: "Test",
			prefix: "anthropic",
			apiKey: "test-key",
			baseUrl: "https://api.anthropic.com/v1",
			defaultModel: "claude-sonnet-4-5",
		});
		const dbMessages: DbMessage[] = [
			{
				id: "u1",
				role: "user",
				contentJson: [{ type: "text", text: "run" }],
				contentText: "run",
				parentToolUseId: null,
				messageUuid: null,
			},
			{
				id: "a1",
				role: "assistant",
				contentJson: [
					// Minted by a NUG channel — must not be replayed to a direct anthropic upstream.
					{
						type: "reasoning",
						text: "foreign",
						providerMetadata: {
							anthropic: { blockIndex: 0, signature: "sig-foreign" },
							signatureSource: "nug:antigravity",
						},
					},
					// Legacy block with no recorded source — also dropped, conservatively.
					{
						type: "reasoning",
						text: "legacy",
						providerMetadata: { anthropic: { blockIndex: 1, signature: "sig-legacy" } },
					},
					{ type: "redacted_thinking", data: "opaque", signatureSource: "nug:antigravity" },
					{ type: "text", text: "answer" },
				],
				contentText: "answer",
				parentToolUseId: null,
				messageUuid: null,
				toolCalls: [],
			},
		];

		const { history } = await provider.buildHistory(dbMessages, "claude-sonnet-4-5");
		const assistant = (history as Array<{ role: string; content: unknown }>).find(
			(message) => message.role === "assistant",
		);

		// Signatures are blanked, the cross-source redacted block is dropped
		// entirely, and thinking text survives.
		expect(assistant?.content).toEqual([
			{ type: "thinking", thinking: "foreign", signature: "" },
			{ type: "thinking", thinking: "legacy", signature: "" },
			{ type: "text", text: "answer" },
		]);
	});

	it("relay models skip the signature-source check and drop redacted thinking", async () => {
		// A relay model mints no credentials: reasoning text crosses upstreams
		// freely, a foreign signature is echoed verbatim (nothing verifies it),
		// and redacted_thinking — an Anthropic-specific opaque payload — is
		// dropped because no relay gateway understands it.
		const provider = new AnthropicProvider({
			id: "test",
			name: "Test",
			prefix: "relay",
			apiKey: "test-key",
			baseUrl: "https://relay.invalid/v1",
			defaultModel: "GLM-5.1",
		});
		const dbMessages: DbMessage[] = [
			{
				id: "u1",
				role: "user",
				contentJson: [{ type: "text", text: "run" }],
				contentText: "run",
				parentToolUseId: null,
				messageUuid: null,
			},
			{
				id: "a1",
				role: "assistant",
				contentJson: [
					{
						type: "reasoning",
						text: "cross-provider",
						providerMetadata: {
							anthropic: { blockIndex: 0, signature: "sig-foreign" },
							signatureSource: "some-other-upstream",
						},
					},
					{
						type: "reasoning",
						text: "unsigned",
						providerMetadata: { anthropic: { blockIndex: 1 } },
					},
					{ type: "redacted_thinking", data: "opaque", signatureSource: "some-other-upstream" },
					{ type: "text", text: "answer" },
				],
				contentText: "answer",
				parentToolUseId: null,
				messageUuid: null,
				toolCalls: [],
			},
		];

		const { history } = await provider.buildHistory(dbMessages, "GLM-5.1");
		const assistant = (history as Array<{ role: string; content: unknown }>).find(
			(message) => message.role === "assistant",
		);

		expect(assistant?.content).toEqual([
			{ type: "thinking", thinking: "cross-provider", signature: "sig-foreign" },
			{ type: "thinking", thinking: "unsigned", signature: "" },
			{ type: "text", text: "answer" },
		]);
	});

	it("preserves redacted thinking blocks from SSE for replay", async () => {
		const events = await Array.fromAsync(
			parseAnthropicSSEStream(
				sseStream([
					{ type: "message_start", message: { id: "msg_1" } },
					{
						type: "content_block_start",
						index: 1,
						content_block: { type: "redacted_thinking", data: "opaque-redacted-data" },
					},
					{ type: "content_block_stop", index: 1 },
				]),
			),
		);

		expect(events).toContainEqual({
			redactedThinking: { data: "opaque-redacted-data", outputIndex: 1 },
		});
	});

	it("keeps redacted thinking in the loop's own assistant trajectory", async () => {
		const provider = new AnthropicProvider({
			id: "test",
			name: "Test",
			prefix: "anthropic",
			apiKey: "test-key",
			baseUrl: "https://api.anthropic.com/v1",
			defaultModel: "claude-sonnet-4-5",
		});
		const history = await liveHistory(
			provider,
			[
				{
					type: "redacted_thinking",
					data: "opaque-redacted-data",
					outputIndex: 0,
					signatureSource: provider.getActiveReasoningSource(),
				},
				{ type: "text", text: "ok", outputIndex: 1 },
				{
					type: "tool_use",
					toolUseId: "toolu_1",
					name: "Read",
					input: { file_path: "/tmp/a" },
					outputIndex: 2,
				},
			],
			{ toolu_1: "contents" },
		);

		expect(history[0]).toEqual({
			role: "assistant",
			content: [
				{ type: "redacted_thinking", data: "opaque-redacted-data" },
				{ type: "text", text: "ok" },
				{ type: "tool_use", id: "toolu_1", name: "Read", input: { file_path: "/tmp/a" } },
			],
		});
	});

	/**
	 * An interrupted stream can open a thinking block and fill in its signature
	 * without ever delivering a thinking_delta, which used to reach the wire as
	 * `{ type: "thinking", thinking: "", signature: "..." }` and get rejected with
	 * "messages.N.content.M.thinking: ... too short". Since the block lived in
	 * replayed history, every later turn failed at the same index.
	 */
	it("drops a signature-only reasoning block instead of sending empty thinking", async () => {
		const provider = new AnthropicProvider({
			id: "test",
			name: "Test",
			prefix: "anthropic",
			apiKey: "test-key",
			baseUrl: "https://api.anthropic.com/v1",
			defaultModel: "claude-sonnet-4-5",
		});
		const signatureSource = provider.getActiveReasoningSource();
		const thought = (text: string, blockIndex: number, signature: string): ContentBlock => ({
			type: "reasoning",
			text,
			providerMetadata: { signatureSource, anthropic: { blockIndex, signature } },
		});
		const transcript = new LoopTranscript();
		transcript.appendInput("question");
		transcript.appendStep([
			// Signature arrived, thinking text never did.
			thought("", 0, "sig-orphan"),
			thought("   ", 1, "sig-blank"),
			thought("real", 2, "sig-real"),
			{ type: "text", text: "answer" },
		]);
		transcript.appendInput("next");
		const { history } = await provider.buildHistory(transcript.rows, "claude-sonnet-4-5");

		// What the upstream receives: the empty block never enters the history, and the
		// blank one is dropped by the request's last-line sanitizer.
		let sent: { messages?: Array<{ role: string; content: unknown }> } = {};
		const originalFetch = globalThis.fetch;
		globalThis.fetch = (async (_input: string | URL | Request, init?: RequestInit) => {
			sent = JSON.parse(String(init?.body));
			return new Response("mocked failure", { status: 400 });
		}) as typeof fetch;
		try {
			for await (const _event of provider.chat({
				conversationId: "empty-thinking",
				content: "",
				model: "anthropic:claude-sonnet-4-5",
				cwd: ".",
				history,
				tools: [],
				toolResults: [],
				signal: new AbortController().signal,
			})) {
				/* consume */
			}
		} catch {
			/* Mocked upstream failure. */
		} finally {
			globalThis.fetch = originalFetch;
		}

		expect(sent.messages?.find((message) => message.role === "assistant")).toEqual({
			role: "assistant",
			content: [
				{ type: "thinking", thinking: "real", signature: "sig-real" },
				{ type: "text", text: "answer" },
			],
		});
	});

	/**
	 * The interrupted-stream shape end to end: content_block_start opens the
	 * thinking block, signature_delta lands, the stream stops with no
	 * thinking_delta. The stop event carries signature metadata but no reasoning
	 * text, which is what produced the empty block upstream rejected.
	 */
	it("emits no reasoning text when a thinking block is interrupted before any delta", async () => {
		const events = await Array.fromAsync(
			parseAnthropicSSEStream(
				sseStream([
					{ type: "message_start", message: { id: "msg_1" } },
					{ type: "content_block_start", index: 0, content_block: { type: "thinking" } },
					{
						type: "content_block_delta",
						index: 0,
						delta: { type: "signature_delta", signature: "sig-orphan" },
					},
					{ type: "content_block_stop", index: 0 },
				]),
			),
		);

		expect(events.filter((event) => event.reasoning)).toEqual([]);
		// The signature still surfaces, so the block is only recoverable as metadata.
		expect(events).toContainEqual(
			expect.objectContaining({
				reasoningMetadata: { anthropic: { blockIndex: 0, signature: "sig-orphan" } },
			}),
		);
	});
});
