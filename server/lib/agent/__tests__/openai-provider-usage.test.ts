import { afterEach, describe, expect, test } from "bun:test";
import { setOutboundFetchOverrideForTest } from "../../net/outbound-fetch";
import {
	OpenAIProvider,
	parseResponsesAPIEvent,
	type ResponsesAPIChunk,
	type ResponsesReasoningAccum,
	type ResponsesToolAccum,
} from "../openai-provider";
import type { ChatParams, ParsedStreamEvent } from "../provider";

const config = {
	id: "usage",
	name: "Usage",
	prefix: "usage",
	apiKey: "test-key",
	baseUrl: "https://example.invalid/v1",
	defaultModel: "gpt-5",
};

function sse(events: unknown[]): Response {
	return new Response(events.map((event) => `data: ${JSON.stringify(event)}\n\n`).join(""), {
		headers: { "content-type": "text/event-stream" },
	});
}

function chatParams(model = "usage:gpt-5"): ChatParams {
	return {
		conversationId: "usage-test",
		content: "hello",
		model,
		cwd: process.cwd(),
		history: [],
		tools: [],
		toolResults: [],
		signal: new AbortController().signal,
	};
}

function responsesUsage(chunk: ResponsesAPIChunk) {
	return parseResponsesAPIEvent(
		chunk,
		new Map<number, ResponsesToolAccum>(),
		new Map<number, ResponsesReasoningAccum>(),
	).find((event) => event.usage)?.usage;
}

async function collectUsages(stream: AsyncGenerator<ParsedStreamEvent>) {
	const usages: NonNullable<ParsedStreamEvent["usage"]>[] = [];
	for await (const event of stream) {
		if (event.usage) usages.push(event.usage);
	}
	return usages;
}

afterEach(() => setOutboundFetchOverrideForTest(null));

describe("OpenAI usage 口径归一", () => {
	test("Responses usage 从 inputTokens 扣除缓存，promptTokens 保留上下文足迹", () => {
		const usage = responsesUsage({
			type: "response.completed",
			response: {
				usage: {
					input_tokens: 302789,
					output_tokens: 326,
					input_tokens_details: { cached_tokens: 302336 },
					output_tokens_details: { reasoning_tokens: 72 },
				},
			},
		});

		expect(usage?.promptTokens).toBe(302789);
		expect(usage?.inputTokens).toBe(453);
		expect(usage?.cachedInputTokens).toBe(302336);
		expect(usage?.completionTokens).toBe(326);
		expect(usage?.reasoningTokens).toBe(72);
	});

	test("上游已扣减缓存时不二次扣减，避免负数", () => {
		const usage = responsesUsage({
			type: "response.completed",
			response: {
				usage: {
					input_tokens: 453,
					output_tokens: 10,
					input_tokens_details: { cached_tokens: 302336 },
				},
			},
		});

		expect(usage?.inputTokens).toBe(453);
		expect(usage?.cachedInputTokens).toBe(302336);
	});

	test("无缓存明细时 inputTokens 即纯输入", () => {
		const usage = responsesUsage({
			type: "response.completed",
			response: { usage: { input_tokens: 1200, output_tokens: 8 } },
		});

		expect(usage?.promptTokens).toBe(1200);
		expect(usage?.inputTokens).toBe(1200);
		expect(usage?.cachedInputTokens).toBeUndefined();
	});

	test("Chat Completions 的 usage-only chunk 同样归一", async () => {
		setOutboundFetchOverrideForTest(async () =>
			sse([
				{ choices: [{ delta: { content: "hi" }, finish_reason: "stop" }] },
				{
					choices: [],
					usage: {
						prompt_tokens: 81250,
						completion_tokens: 206,
						prompt_tokens_details: { cached_tokens: 80256 },
						completion_tokens_details: { reasoning_tokens: 85 },
					},
				},
			]),
		);

		const provider = new OpenAIProvider({ ...config, apiMode: "completions" });
		const usages = await collectUsages(provider.chat(chatParams()));
		const usage = usages.at(-1);

		expect(usage?.promptTokens).toBe(81250);
		expect(usage?.inputTokens).toBe(994);
		expect(usage?.cachedInputTokens).toBe(80256);
		expect(usage?.completionTokens).toBe(206);
		expect(usage?.reasoningTokens).toBe(85);
	});

	test("Responses 流式完成事件同样归一", async () => {
		setOutboundFetchOverrideForTest(async () =>
			sse([
				{ type: "response.output_text.delta", delta: "hi", output_index: 0, item_id: "msg_1" },
				{
					type: "response.completed",
					response: {
						usage: {
							input_tokens: 1200,
							output_tokens: 8,
							input_tokens_details: { cached_tokens: 1100 },
						},
					},
				},
			]),
		);

		const provider = new OpenAIProvider({ ...config, apiMode: "responses" });
		const usages = await collectUsages(provider.chat(chatParams()));
		const usage = usages.at(-1);

		expect(usage?.promptTokens).toBe(1200);
		expect(usage?.inputTokens).toBe(100);
		expect(usage?.cachedInputTokens).toBe(1100);
	});
});
