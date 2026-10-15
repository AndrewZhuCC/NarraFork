import { afterEach, describe, expect, test } from "bun:test";
import { extractOpenAIUsage } from "../../usage-tracking";
import { OpenAIProvider, parseResponsesAPIEvent } from "../openai-provider";

const originalFetch = globalThis.fetch;
afterEach(() => {
	globalThis.fetch = originalFetch;
});

const providerConfig = {
	id: "test-openai-usage",
	name: "Test OpenAI usage",
	prefix: "openai",
	apiKey: "test-key",
	baseUrl: "https://example.com/v1",
	defaultModel: "gpt-5",
};

describe("OpenAI raw usage normalization", () => {
	for (const total of [100, 60, 20, 0]) {
		for (const protocol of ["responses", "completions"] as const) {
			test(`${protocol} total=${total} clamps after subtracting read and write`, () => {
				const raw =
					protocol === "responses"
						? {
								input_tokens: total,
								output_tokens: 8,
								input_tokens_details: { cached_tokens: 40, cache_write_tokens: 20 },
							}
						: {
								prompt_tokens: total,
								completion_tokens: 8,
								prompt_tokens_details: { cached_tokens: 40, cache_creation_tokens: 20 },
							};
				expect(extractOpenAIUsage(raw)).toMatchObject({
					inputTokens: Math.max(total - 60, 0),
					outputTokens: 8,
					cachedInputTokens: 40,
					cacheCreationInputTokens: 20,
				});
			});
		}
	}

	for (const read of [
		"cache_read_input_tokens",
		"cache_read_tokens",
		"cached_tokens",
		"cached_input_tokens",
	]) {
		for (const write of [
			"cache_write_tokens",
			"cache_creation_input_tokens",
			"cache_write_input_tokens",
			"cache_creation_tokens",
		]) {
			test(`supports top-level ${read} and ${write}`, () => {
				expect(extractOpenAIUsage({ prompt_tokens: 100, [read]: 40, [write]: 20 })).toMatchObject({
					inputTokens: 40,
					cachedInputTokens: 40,
					cacheCreationInputTokens: 20,
				});
			});
		}
	}

	test("missing cache counters remain unknown rather than fabricated zero", () => {
		const usage = extractOpenAIUsage({ prompt_tokens: 100, completion_tokens: 8 });
		expect(usage.inputTokens).toBe(100);
		expect(usage.cachedInputTokens).toBeUndefined();
		expect(usage.cacheCreationInputTokens).toBeUndefined();
	});

	test("nested explicit zero wins; missing individual fields fall through", () => {
		expect(
			extractOpenAIUsage({
				input_tokens: 100,
				input_tokens_details: { cached_tokens: 0, cache_write_tokens: 0 },
				prompt_tokens_details: { cached_tokens: 40, cache_creation_tokens: 20 },
				cache_read_input_tokens: 50,
				cache_write_tokens: 30,
			}),
		).toMatchObject({ inputTokens: 100, cachedInputTokens: 0, cacheCreationInputTokens: 0 });
		expect(
			extractOpenAIUsage({
				input_tokens: 100,
				input_tokens_details: {},
				prompt_tokens_details: { cached_tokens: 40, cache_creation_tokens: 20 },
			}),
		).toMatchObject({ inputTokens: 40, cachedInputTokens: 40, cacheCreationInputTokens: 20 });
	});

	test("nested write aliases precede creation aliases across both protocols", () => {
		expect(
			extractOpenAIUsage({
				input_tokens: 100,
				input_tokens_details: { cache_creation_tokens: 40 },
				prompt_tokens_details: { cache_write_tokens: 20 },
			}),
		).toMatchObject({ inputTokens: 80, cacheCreationInputTokens: 20 });
		expect(
			extractOpenAIUsage({
				input_tokens: 100,
				input_tokens_details: { cache_creation_tokens: 40 },
				prompt_tokens_details: { cache_write_tokens: 0 },
				cache_write_tokens: 50,
			}),
		).toMatchObject({ inputTokens: 100, cacheCreationInputTokens: 0 });
	});

	test("top-level aliases select the first positive finite value", () => {
		expect(
			extractOpenAIUsage({
				input_tokens: 100,
				cache_read_input_tokens: 0,
				cache_read_tokens: 40,
				cached_tokens: 60,
				cache_write_tokens: 0,
				cache_creation_input_tokens: 20,
				cache_write_input_tokens: 30,
			}),
		).toMatchObject({ inputTokens: 40, cachedInputTokens: 40, cacheCreationInputTokens: 20 });
	});

	for (const invalid of [-10, Number.NaN, Number.POSITIVE_INFINITY, Number.NEGATIVE_INFINITY]) {
		test(`invalid nested cache ${invalid} clamps to zero without falling back`, () => {
			expect(
				extractOpenAIUsage({
					input_tokens: 100,
					input_tokens_details: { cached_tokens: invalid, cache_write_tokens: invalid },
					cache_read_tokens: 40,
					cache_write_tokens: 20,
				}),
			).toMatchObject({ inputTokens: 100, cachedInputTokens: 0, cacheCreationInputTokens: 0 });
		});
		test(`invalid top-level cache ${invalid} cannot raise or poison net input`, () => {
			expect(
				extractOpenAIUsage({
					input_tokens: 100,
					cache_read_input_tokens: invalid,
					cache_write_tokens: invalid,
				}),
			).toMatchObject({ inputTokens: 100, cachedInputTokens: 0, cacheCreationInputTokens: 0 });
			expect(
				extractOpenAIUsage({
					input_tokens: 100,
					cache_read_input_tokens: invalid,
					cache_read_tokens: 40,
					cache_write_tokens: invalid,
					cache_creation_tokens: 20,
				}),
			).toMatchObject({ inputTokens: 40, cachedInputTokens: 40, cacheCreationInputTokens: 20 });
		});
	}

	test("Responses events retain full context prompt and disjoint input", () => {
		const events = parseResponsesAPIEvent(
			{
				type: "response.completed",
				response: {
					usage: { input_tokens: 100, output_tokens: 8, cached_tokens: 40, cache_write_tokens: 20 },
				},
			},
			new Map(),
			new Map(),
		);
		expect(events.find((event) => event.usage)?.usage).toMatchObject({
			promptTokens: 100,
			inputTokens: 40,
			completionTokens: 8,
			cachedInputTokens: 40,
			cacheCreationInputTokens: 20,
		});
	});
});

describe("OpenAI partial accounting", () => {
	for (const apiMode of ["responses", "completions", "codex"] as const) {
		for (const correction of [undefined, 60, 100, 0]) {
			test(`${apiMode} retains missing counters and applies cache correction ${correction}`, async () => {
				const responses = apiMode !== "completions";
				const initial = responses
					? { input_tokens: 100, input_tokens_details: { cached_tokens: 40 } }
					: { prompt_tokens: 100, prompt_tokens_details: { cached_tokens: 40 } };
				const trailing = responses
					? {
							input_tokens: 0,
							output_tokens: 8,
							...(correction === undefined ? {} : { cached_tokens: correction }),
						}
					: {
							prompt_tokens: 0,
							completion_tokens: 8,
							...(correction === undefined ? {} : { cached_tokens: correction }),
						};
				const frames = responses
					? [
							{ type: "response.output_text.delta", delta: "ok" },
							{ type: "response.usage", usage: initial },
							{ type: "response.completed", response: { status: "completed", usage: trailing } },
						]
					: [
							{ choices: [{ index: 0, delta: { content: "ok" }, finish_reason: null }] },
							{ choices: [], usage: initial },
							{ choices: [], usage: trailing },
						];
				globalThis.fetch = (async () =>
					new Response(frames.map((frame) => `data: ${JSON.stringify(frame)}\n\n`).join(""), {
						headers: { "content-type": "text/event-stream" },
					})) as unknown as typeof fetch;
				const provider = new OpenAIProvider({ ...providerConfig, apiMode });
				const expectedRead = correction ?? 40;
				const generated = await provider.generateWithMeta("prompt", "openai:gpt-5");
				expect(generated.usage).toMatchObject({
					inputTokens: 100 - expectedRead,
					cachedInputTokens: expectedRead,
					outputTokens: 8,
				});
				const streamed = [];
				for await (const parsed of provider.chat({
					model: "openai:gpt-5",
					content: "prompt",
					cwd: ".",
					conversationId: "partial-usage",
					history: [],
					tools: [],
					toolResults: [],
					signal: new AbortController().signal,
				}))
					streamed.push(parsed);
				expect(streamed.filter((parsed) => parsed.usage).at(-1)?.usage).toMatchObject({
					promptTokens: 100,
					inputTokens: 100 - expectedRead,
					cachedInputTokens: expectedRead,
					completionTokens: 8,
				});
			});
		}
	}

	test("Responses/Codex parser keeps partial cache writes and resets with each request map", () => {
		const tools = new Map();
		const reasoning = new Map();
		parseResponsesAPIEvent(
			{ usage: { input_tokens: 100, cached_tokens: 40, cache_write_tokens: 10 } },
			tools,
			reasoning,
		);
		const final = parseResponsesAPIEvent(
			{ usage: { output_tokens: 8, cache_write_tokens: 20 } },
			tools,
			reasoning,
		);
		expect(final[0]?.usage).toMatchObject({
			promptTokens: 100,
			inputTokens: 40,
			cachedInputTokens: 40,
			cacheCreationInputTokens: 20,
			completionTokens: 8,
		});
		const next = parseResponsesAPIEvent({ usage: { output_tokens: 9 } }, new Map(), new Map());
		expect(next[0]?.usage).toEqual({ completionTokens: 9 });
	});
});

for (const apiMode of ["responses", "completions", "codex"] as const) {
	for (const json of [false, true]) {
		for (const total of [100, 60, 20]) {
			test(`${apiMode} auxiliary ${json ? "JSON" : "SSE"} total=${total} normalizes exactly once`, async () => {
				const responses = apiMode !== "completions";
				const usage = responses
					? {
							input_tokens: total,
							output_tokens: 8,
							input_tokens_details: { cached_tokens: 40, cache_creation_tokens: 20 },
							output_tokens_details: { reasoning_tokens: 3 },
						}
					: {
							prompt_tokens: total,
							completion_tokens: 8,
							prompt_tokens_details: { cached_tokens: 40, cache_write_tokens: 20 },
							completion_tokens_details: { reasoning_tokens: 3 },
						};
				const payload = responses
					? {
							status: "completed",
							output: [{ type: "message", content: [{ type: "output_text", text: "ok" }] }],
							usage,
						}
					: { choices: [{ message: { content: "ok" }, finish_reason: "stop" }], usage };
				const events = responses
					? [
							{ type: "response.output_text.delta", delta: "ok" },
							{ type: "response.completed", response: payload },
						]
					: [
							{ choices: [{ index: 0, delta: { content: "ok" }, finish_reason: "stop" }] },
							{ choices: [], usage },
						];
				globalThis.fetch = (async () =>
					new Response(
						json
							? JSON.stringify(payload)
							: events.map((event) => `data: ${JSON.stringify(event)}\n\n`).join(""),
						{ headers: { "content-type": json ? "application/json" : "text/event-stream" } },
					)) as unknown as typeof fetch;
				const provider = new OpenAIProvider({ ...providerConfig, apiMode });
				const result = await provider.generateWithMeta("prompt", "openai:gpt-5");
				expect(result.text).toBe("ok");
				expect(result.usage).toMatchObject({
					inputTokens: Math.max(total - 60, 0),
					outputTokens: 8,
					cachedInputTokens: 40,
					cacheCreationInputTokens: 20,
					reasoningTokens: 3,
				});
			});
		}
	}
}

for (const apiMode of ["responses", "completions"] as const) {
	test(`${apiMode} chat exposes normalized counters with full prompt`, async () => {
		const event =
			apiMode === "responses"
				? {
						type: "response.completed",
						response: {
							status: "completed",
							usage: {
								input_tokens: 100,
								output_tokens: 8,
								cache_read_tokens: 40,
								cache_write_input_tokens: 20,
							},
						},
					}
				: {
						choices: [{ index: 0, delta: {}, finish_reason: "stop" }],
						usage: {
							prompt_tokens: 100,
							completion_tokens: 8,
							cache_read_tokens: 40,
							cache_write_input_tokens: 20,
						},
					};
		globalThis.fetch = (async () =>
			new Response(`data: ${JSON.stringify(event)}\n\n`, {
				headers: { "content-type": "text/event-stream" },
			})) as unknown as typeof fetch;
		const provider = new OpenAIProvider({ ...providerConfig, apiMode });
		const events = [];
		for await (const parsed of provider.chat({
			model: "openai:gpt-5",
			content: "prompt",
			cwd: ".",
			conversationId: "usage-test",
			history: [],
			tools: [],
			toolResults: [],
			signal: new AbortController().signal,
		}))
			events.push(parsed);
		expect(events.find((parsed) => parsed.usage)?.usage).toMatchObject({
			promptTokens: 100,
			inputTokens: 40,
			completionTokens: 8,
			cachedInputTokens: 40,
			cacheCreationInputTokens: 20,
		});
	});
}
