import { afterEach, expect, spyOn, test } from "bun:test";
import type { RecordingRequest, RequestRecording } from "@shared/request-recording";
import { setOutboundFetchOverrideForTest } from "../../net/outbound-fetch";
import { AnthropicProvider } from "../anthropic-provider";
import { GeminiInteractionsProvider } from "../gemini-interactions-provider";
import { GeminiProvider } from "../gemini-provider";
import { OpenAIProvider } from "../openai-provider";
import type { ChatParams, ProviderAdapter } from "../provider";
import { ApiRequestDumpCollector } from "../request-dump";

const config = {
	id: "recording-test",
	name: "Recording test",
	prefix: "test",
	apiKey: "credential-secret",
	baseUrl: "https://transport.test/v1",
	defaultModel: "test",
};
const anthropicChunk =
	'event: message_start\ndata: {"type":"message_start","message":{"id":"msg-1","usage":{"input_tokens":1}}}\n\nevent: content_block_delta\ndata: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"live"}}\n\n';
const cases: Array<{
	name: string;
	provider: () => ProviderAdapter;
	model: string;
	chunk: string;
}> = [
	{
		name: "Anthropic",
		provider: () => new AnthropicProvider(config),
		model: "test:claude-test",
		chunk: anthropicChunk,
	},
	{
		name: "OpenAI",
		provider: () => new OpenAIProvider({ ...config, apiMode: "responses" }),
		model: "test:gpt-test",
		chunk: 'data: {"type":"response.output_text.delta","delta":"live","output_index":0}\n\n',
	},
	{
		name: "Gemini",
		provider: () => new GeminiProvider(config),
		model: "test:gemini-2.5-flash",
		chunk: 'data: {"candidates":[{"content":{"parts":[{"text":"live"}]}}]}\n\n',
	},
	{
		name: "Gemini Interactions",
		provider: () => new GeminiInteractionsProvider(config),
		model: "test:gemini-3-flash-preview",
		chunk:
			'event: step.start\ndata: {"event_type":"step.start","index":0,"step":{"type":"model_output","content":""}}\n\nevent: step.delta\ndata: {"event_type":"step.delta","index":0,"delta":{"text":"live"}}\n\n',
	},
];
function params(model: string, dump: ApiRequestDumpCollector): ChatParams {
	return {
		conversationId: "recording-conversation",
		content: "hello",
		model,
		cwd: "/tmp",
		history: [],
		tools: [],
		toolResults: [],
		signal: new AbortController().signal,
		requestDump: dump,
	};
}
function recorder() {
	const requests: RecordingRequest[] = [];
	const chunks: string[] = [];
	const meta: Record<string, unknown>[] = [];
	const recording: RequestRecording = {
		requestId: "live-recording",
		pin: () => {},
		request: (request) => requests.push(request),
		responseMeta: (value) => meta.push(value),
		responseChunk: (value) =>
			chunks.push(typeof value === "string" ? value : new TextDecoder().decode(value)),
		frame: () => {},
		finish: () => {},
	};
	return { requests, chunks, meta, dump: new ApiRequestDumpCollector(recording) };
}
afterEach(() => {
	setOutboundFetchOverrideForTest(null);
});

for (const scenario of cases) {
	test(`${scenario.name} forwards the actual serialized request and streams live without clone or drain on consumer cancellation`, async () => {
		const recorded = recorder();
		let sentBody: RequestInit["body"];
		let sentUrl = "";
		let pulls = 0;
		let cancelled = false;
		const clone = spyOn(Response.prototype, "clone").mockImplementation(() => {
			throw new Error("must not clone responses");
		});
		setOutboundFetchOverrideForTest(async (input, init) => {
			sentBody = init?.body;
			sentUrl = String(input);
			return new Response(
				new ReadableStream<Uint8Array>(
					{
						pull(controller) {
							pulls++;
							controller.enqueue(new TextEncoder().encode(scenario.chunk));
						},
						cancel() {
							cancelled = true;
						},
					},
					{ highWaterMark: 0 },
				),
				{ headers: { "content-type": "text/event-stream", "x-upstream-test": "live" } },
			);
		});
		try {
			const stream = scenario.provider().chat(params(scenario.model, recorded.dump));
			for (let index = 0; index < 8; index++) {
				const event = await stream.next();
				if (event.value?.text) break;
			}
			expect(typeof sentBody).toBe("string");
			expect(recorded.requests[0]?.bodyText).toBe(String(sentBody));
			expect(recorded.requests[0]?.url).toBe(sentUrl);
			expect(recorded.chunks.join("")).toContain(scenario.chunk);
			expect(recorded.meta.find((value) => value.status !== undefined)).toMatchObject({
				status: 200,
			});
			const before = pulls;
			await stream.return(undefined);
			expect(cancelled).toBe(true);
			expect(pulls).toBe(before);
			expect(clone).not.toHaveBeenCalled();
			expect(Object.assign({}, ...recorded.meta)).toMatchObject({ bodyIncomplete: true });
		} finally {
			clone.mockRestore();
		}
	});
}

test("Anthropic fallback captures both actual URLs and rejected response before corrected stream", async () => {
	const recorded = recorder();
	let calls = 0;
	setOutboundFetchOverrideForTest(async () => {
		calls++;
		if (calls === 1)
			return new Response("wrong endpoint", { status: 404, headers: { "x-path": "original" } });
		return new Response(anthropicChunk, { headers: { "content-type": "text/event-stream" } });
	});
	const provider = new AnthropicProvider({ ...config, baseUrl: "https://transport.test" });
	for await (const _event of provider.chat(params("test:claude-test", recorded.dump))) {
	}
	expect(recorded.requests.map((request) => request.url)).toEqual([
		"https://transport.test/messages",
		"https://transport.test/v1/messages",
	]);
	expect(recorded.chunks.join("")).toContain("wrong endpoint");
	expect(recorded.chunks.join("")).toContain(anthropicChunk);
	expect(recorded.meta.find((value) => value.status !== undefined)?.status).toBe(404);
});

test("fetch cancellation preserves actual request and error metadata without a response", async () => {
	const recorded = recorder();
	const controller = new AbortController();
	setOutboundFetchOverrideForTest(async () => {
		controller.abort();
		throw new DOMException("aborted on transport", "AbortError");
	});
	const provider = new AnthropicProvider(config);
	const request = { ...params("test:claude-test", recorded.dump), signal: controller.signal };
	await expect(provider.chat(request).next()).rejects.toThrow("aborted on transport");
	expect(recorded.requests).toHaveLength(1);
	expect(recorded.meta.some((value) => String(value.error).includes("aborted on transport"))).toBe(
		true,
	);
});
