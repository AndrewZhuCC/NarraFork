import { afterEach, describe, expect, test } from "bun:test";
import { mkdir, mkdtemp, rm, symlink, truncate, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { TOKEN_REPAIR_MAX_DUMP_BYTES, type TokenRepairCounters } from "@shared/usage-token-repair";
import { analyzeTokenRepairEvidence } from "./usage-token-repair-evidence";

const dirs: string[] = [];
afterEach(async () => {
	await Promise.all(dirs.splice(0).map((dir) => rm(dir, { recursive: true, force: true })));
});
const row = (input = 100, read = 40, write = 0, five = 0, hour = 0) => ({
	id: "request-one",
	provider: "unknown",
	model: null,
	inputTokens: input,
	cachedInputTokens: read,
	cacheCreationInputTokens: write,
	cacheCreation5mTokens: five,
	cacheCreation1hTokens: hour,
});
const oa = (total = 100, read = 40, write = 0) => ({
	input_tokens: total,
	output_tokens: 10,
	input_tokens_details: { cached_tokens: read, cache_write_tokens: write },
});
function dump(payload: unknown, url = "https://private.invalid/v1/responses"): string {
	return JSON.stringify({
		request: { url, headers: { Authorization: "secret" }, body: "private prompt" },
		response: { status: 200, bodyText: JSON.stringify(payload) },
	});
}
const response = (usage = oa()) => ({ object: "response", status: "completed", usage });
function stream(events: unknown[], url: string, done = false): string {
	return JSON.stringify({
		request: { url },
		response: {
			bodyText:
				events.map((event) => `data: ${JSON.stringify(event)}\n\n`).join("") +
				(done ? "data: [DONE]\n\n" : ""),
		},
	});
}
async function analyze(before = row(), payload = dump(response())) {
	return analyzeTokenRepairEvidence(before, payload);
}

describe("protocol evidence, never provider or model guesses", () => {
	test("OpenAI raw-inclusive legacy input is repaired and summary never leaks content", async () => {
		const evidence = await analyze();
		expect(evidence).toMatchObject({
			classification: "repairable",
			protocol: "openai",
			after: { inputTokens: 60, cachedInputTokens: 40 },
		});
		// Only the five counters are returned, never row identity/provider/model.
		expect(Object.keys(evidence.after ?? {}).sort()).toEqual(
			Object.keys(row())
				.filter((key) => !["id", "provider", "model"].includes(key))
				.sort(),
		);
		expect(JSON.stringify(evidence)).not.toMatch(/private|secret|Authorization|https:/);
	});
	test("correct uses evidence even for unknown provider", async () => {
		expect((await analyze(row(60))).classification).toBe("correct");
	});
	for (const total of [0, 20, 40, 60, 100]) {
		test(`OpenAI known raw conversion total=${total} includes write and clamps`, async () => {
			const evidence = await analyze(row(total, 40, 20), dump(response(oa(total, 40, 20))));
			expect(evidence.classification).toBe(total === 0 ? "correct" : "repairable");
			expect(evidence.after?.inputTokens).toBe(Math.max(total - 60, 0));
		});
	}
	test("PR44 conditional deduction did not subtract cache-write", async () => {
		expect(
			(await analyze(row(60, 40, 20), dump(response(oa(100, 40, 20))))).after?.inputTokens,
		).toBe(40);
	});
	test("all-cached PR44 conditional leaves raw input, now repairs to zero", async () => {
		expect((await analyze(row(40, 40), dump(response(oa(40, 40))))).after?.inputTokens).toBe(0);
	});
	test("unrecognized previous values stay ambiguous, including accidental double deduction", async () => {
		for (const value of [1, 20, 59, 61, 1000])
			expect((await analyze(row(value))).classification).toBe("ambiguous");
	});
	test("nested zero wins over compatible aliases", async () => {
		const usage = {
			...oa(100, 0),
			prompt_tokens_details: { cached_tokens: 50 },
			cache_read_tokens: 60,
		};
		expect((await analyze(row(100, 0), dump(response(usage)))).classification).toBe("correct");
	});
	test("top-level first positive compatibility counters and write alias precedence", async () => {
		const usage = {
			input_tokens: 100,
			input_tokens_details: { cache_creation_tokens: 30 },
			prompt_tokens_details: { cache_write_tokens: 20 },
			cache_read_input_tokens: 0,
			cache_read_tokens: 40,
		};
		expect((await analyze(row(100, 40, 20), dump({ object: "response", usage }))).after).toEqual(
			expect.objectContaining({
				inputTokens: 40,
				cachedInputTokens: 40,
				cacheCreationInputTokens: 20,
			}),
		);
	});
	test("legacy parser's missing write bucket is an exact recognized conversion", async () => {
		expect(
			(await analyze(row(100, 40, 0), dump(response(oa(100, 40, 20))))).after
				?.cacheCreationInputTokens,
		).toBe(20);
	});
	test("complete Completions JSON", async () => {
		const payload = {
			object: "chat.completion",
			choices: [{ finish_reason: "stop" }],
			usage: { prompt_tokens: 100, prompt_tokens_details: { cached_tokens: 40 } },
		};
		expect(
			(await analyze(row(), dump(payload, "https://relay.invalid/v1/chat/completions")))
				.classification,
		).toBe("repairable");
	});
	test("Completions SSE final usage follows finish event, is not added", async () => {
		const events = [
			{ object: "chat.completion.chunk", choices: [{ finish_reason: "stop" }] },
			{
				object: "chat.completion.chunk",
				choices: [],
				usage: { prompt_tokens: 100, prompt_tokens_details: { cached_tokens: 40 } },
			},
		];
		expect(
			(await analyze(row(), stream(events, "https://relay.invalid/v1/chat/completions"))).after
				?.inputTokens,
		).toBe(60);
	});
	test("Responses SSE ignores earlier snapshots and uses terminal response", async () => {
		const events = [
			{ type: "response.created", response: { usage: oa(10, 0) } },
			{ type: "response.completed", response: response() },
		];
		expect(
			(await analyze(row(), stream(events, "https://relay.invalid/v1/responses"))).after
				?.inputTokens,
		).toBe(60);
	});
	test("events array is accepted without raw response body", async () => {
		const raw = JSON.stringify({
			response: { events: [{ type: "response.completed", response: response() }] },
		});
		expect((await analyze(row(), raw)).classification).toBe("repairable");
	});
	for (const provider of ["nug", "unknown"]) {
		test(`${provider} wire Anthropic remains net, not guessed from provider`, async () => {
			const raw = dump(
				{
					type: "message",
					stop_reason: "end_turn",
					usage: { input_tokens: 100, cache_read_input_tokens: 40 },
				},
				"https://relay.invalid/v1/messages",
			);
			expect((await analyze({ ...row(), provider }, raw)).classification).toBe("correct");
			expect((await analyze({ ...row(60), provider }, raw)).classification).toBe("ambiguous");
		});
		test(`${provider} wire OpenAI is cache-inclusive`, async () => {
			expect((await analyze({ ...row(), provider })).after?.inputTokens).toBe(60);
		});
	}
	test("Anthropic TTL aggregate is max(raw, 5m+1h)", async () => {
		const raw = dump(
			{
				type: "message",
				usage: {
					input_tokens: 100,
					cache_read_input_tokens: 40,
					cache_creation_input_tokens: 2,
					cache_creation: { ephemeral_5m_input_tokens: 3, ephemeral_1h_input_tokens: 4 },
				},
			},
			"https://relay.invalid/v1/messages",
		);
		const evidence = await analyze(row(100, 40, 2, 3, 4), raw);
		expect(evidence.classification).toBe("repairable");
		expect(evidence.after?.cacheCreationInputTokens).toBe(7);
		expect(evidence.after?.inputTokens).toBe(100);
	});
	test("Anthropic SSE retains nonzero start input/cache despite delta zeroes", async () => {
		const events = [
			{
				type: "message_start",
				message: {
					usage: {
						input_tokens: 100,
						cache_read_input_tokens: 40,
						cache_creation_input_tokens: 7,
						cache_creation: { ephemeral_5m_input_tokens: 3, ephemeral_1h_input_tokens: 4 },
					},
				},
			},
			{
				type: "message_delta",
				usage: { input_tokens: 0, cache_read_input_tokens: 0, output_tokens: 8 },
			},
			{ type: "message_stop" },
		];
		const evidence = await analyze(
			row(100, 40, 7, 3, 4),
			stream(events, "https://relay.invalid/v1/messages"),
		);
		expect(evidence.classification).toBe("correct");
	});
	test("Gemini generateContent legacy total", async () => {
		const raw = dump(
			{
				candidates: [{ finishReason: "STOP" }],
				usageMetadata: { promptTokenCount: 100, cachedContentTokenCount: 40 },
			},
			"https://google.invalid/v1/models/gem:generateContent",
		);
		expect((await analyze(row(), raw)).after?.inputTokens).toBe(60);
	});
	test("Gemini streaming cumulative usage uses final snapshot, never sum", async () => {
		const events = [
			{ usageMetadata: { promptTokenCount: 50, cachedContentTokenCount: 20 } },
			{
				candidates: [{ finishReason: "STOP" }],
				usageMetadata: { promptTokenCount: 100, cachedContentTokenCount: 40 },
			},
		];
		const evidence = await analyze(
			row(),
			stream(events, "https://google.invalid/v1/models/gem:streamGenerateContent"),
		);
		expect(evidence.after?.inputTokens).toBe(60);
	});
	test("Gemini JSON chunk array is treated as completed cumulative response", async () => {
		const raw = dump(
			[
				{ usageMetadata: { promptTokenCount: 50, cachedContentTokenCount: 10 } },
				{ usageMetadata: { promptTokenCount: 100, cachedContentTokenCount: 40 } },
			],
			"https://google.invalid/v1/models/gem:streamGenerateContent",
		);
		expect((await analyze(row(), raw)).after?.inputTokens).toBe(60);
	});
	test("Gemini Interactions supports nested usage and named SSE event", async () => {
		const payload = {
			interaction: {
				status: "completed",
				usage: { total_input_tokens: 100, total_cached_tokens: 40 },
			},
		};
		const raw = JSON.stringify({
			request: { url: "https://google.invalid/v1/interactions" },
			response: { bodyText: `event: interaction.completed\ndata: ${JSON.stringify(payload)}\n\n` },
		});
		expect((await analyze(row(), raw)).after?.inputTokens).toBe(60);
	});
	test("Gemini full-cache conditional bug repairs to zero", async () => {
		const raw = dump(
			{ status: "completed", usage: { total_input_tokens: 40, total_cached_tokens: 40 } },
			"https://google.invalid/v1/interactions",
		);
		expect((await analyze(row(40, 40), raw)).after?.inputTokens).toBe(0);
	});
});

describe("insufficient and ambiguous evidence is never applied", () => {
	test("missing dump does not guess from provider", async () => {
		expect(
			(await analyzeTokenRepairEvidence({ ...row(), provider: "openai" }, null)).classification,
		).toBe("insufficient");
	});
	test("context snapshots are not upstream usage", async () => {
		expect(
			(
				await analyze(
					row(),
					JSON.stringify({ context: { inputTokens: 100 }, response: { bodyText: "{}" } }),
				)
			).classification,
		).toBe("insufficient");
	});
	for (const field of ["bodyTruncated", "bodyIncomplete", "errorTruncated"]) {
		test(`reject ${field}`, async () => {
			const raw = JSON.parse(dump(response()));
			raw.response[field] = true;
			expect((await analyze(row(), JSON.stringify(raw))).classification).toBe("insufficient");
		});
	}
	test("received/kept byte mismatch rejects capture", async () => {
		const raw = JSON.parse(dump(response()));
		raw.response.bodyBytesReceived = 1000;
		raw.response.bodyBytesKept = 500;
		expect((await analyze(row(), JSON.stringify(raw))).classification).toBe("insufficient");
	});
	for (const partial of [
		{ attempts: [{ requestText: "retry" }] },
		{ attemptsTruncated: true },
		{ initialRequest: {} },
		{ truncated: true },
	]) {
		test(`reject retry/partial ${Object.keys(partial)[0]}`, async () => {
			expect(
				(await analyze(row(), JSON.stringify({ ...JSON.parse(dump(response())), ...partial })))
					.classification,
			).toBe("insufficient");
		});
	}
	test("Responses created without completed does not supply terminal usage", async () => {
		expect(
			(
				await analyze(
					row(),
					stream(
						[{ type: "response.created", response: response() }],
						"https://relay.invalid/v1/responses",
					),
				)
			).classification,
		).toBe("insufficient");
	});
	test("Anthropic stream requires message_stop", async () => {
		expect(
			(
				await analyze(
					row(),
					stream(
						[{ type: "message_start", message: { usage: { input_tokens: 100 } } }],
						"https://relay.invalid/v1/messages",
					),
				)
			).classification,
		).toBe("insufficient");
	});
	test("malformed SSE tail is not salvaged", async () => {
		const raw = JSON.parse(
			stream(
				[{ type: "response.completed", response: response() }],
				"https://relay.invalid/v1/responses",
			),
		);
		raw.response.bodyText += "data: {bad\n\n";
		expect((await analyze(row(), JSON.stringify(raw))).classification).toBe("insufficient");
	});
	test("failed/incomplete terminal responses are refused", async () => {
		for (const type of ["response.failed", "response.incomplete", "interaction.failed"]) {
			expect(
				(
					await analyze(
						row(),
						stream([{ type, response: response() }], "https://relay.invalid/v1/responses"),
					)
				).classification,
			).toBe("insufficient");
		}
	});
	test("multiple request response IDs and conflicting totals are ambiguous", async () => {
		for (const events of [
			[
				{ type: "response.completed", response: { ...response(), id: "one" } },
				{ type: "response.completed", response: { ...response(), id: "two" } },
			],
			[
				{ type: "response.completed", response: response() },
				{ type: "response.completed", response: response(oa(200)) },
			],
		])
			expect(
				(await analyze(row(), stream(events, "https://relay.invalid/v1/responses"))).classification,
			).toBe("ambiguous");
	});
	test("protocol cannot contradict request endpoint", async () => {
		const raw = dump({
			type: "message",
			usage: { input_tokens: 100, cache_read_input_tokens: 40 },
		});
		expect((await analyze(row(), raw)).classification).toBe("ambiguous");
	});
	for (const invalid of [-1, 0.5, Number.MAX_SAFE_INTEGER + 1, "100", 1e100]) {
		test(`invalid upstream counter ${invalid} cannot produce repair`, async () => {
			const raw = dump({ object: "response", usage: { ...oa(), input_tokens: invalid } });
			expect((await analyze(row(), raw)).classification).toBe("ambiguous");
		});
	}
	test("invalid existing counter cannot produce repair", async () => {
		expect((await analyze({ ...row(), inputTokens: Number.NaN })).classification).toBe("ambiguous");
	});
	test("overflow TTL sum cannot produce repair", async () => {
		const raw = dump(
			{
				type: "message",
				usage: {
					input_tokens: 100,
					cache_creation: {
						ephemeral_5m_input_tokens: Number.MAX_SAFE_INTEGER,
						ephemeral_1h_input_tokens: 1,
					},
				},
			},
			"https://relay.invalid/v1/messages",
		);
		expect((await analyze(row(), raw)).classification).toBe("ambiguous");
	});
	test("abort is honored", async () => {
		const controller = new AbortController();
		controller.abort();
		expect(
			(await analyzeTokenRepairEvidence(row(), dump(response()), { signal: controller.signal }))
				.reason,
		).toBe("analysis_cancelled");
	});
});

describe("capture integrity regression cases", () => {
	test("real collector _truncated event marker invalidates even a retained terminal event", async () => {
		const raw = JSON.stringify({
			response: {
				events: [{ type: "response.completed", response: response() }, { _truncated: 1 }],
			},
		});
		expect((await analyze(row(), raw)).classification).toBe("insufficient");
	});
	test("truncation marker also invalidates an otherwise complete body", async () => {
		const raw = JSON.parse(dump(response()));
		raw.response.events = [{ _truncated: 5 }];
		expect((await analyze(row(), JSON.stringify(raw))).classification).toBe("insufficient");
	});
	test("wrapped events preserve named event and accept DONE", async () => {
		const raw = JSON.stringify({
			response: {
				events: [
					{ event: "response.completed", data: JSON.stringify({ response: response() }) },
					{ data: "[DONE]" },
				],
			},
		});
		expect((await analyze(row(), raw)).classification).toBe("repairable");
	});
	test("Interactions in-progress JSON is not terminal usage", async () => {
		const raw = dump(
			{ status: "in_progress", usage: { total_input_tokens: 100, total_cached_tokens: 40 } },
			"https://google.invalid/v1/interactions",
		);
		expect((await analyze(row(), raw)).classification).toBe("insufficient");
	});
	test("Interactions final metadata.total_usage is supported", async () => {
		const raw = stream(
			[
				{
					event_type: "interaction.in_progress",
					interaction: {
						status: "in_progress",
						usage: { total_input_tokens: 50, total_cached_tokens: 10 },
					},
				},
				{
					event_type: "interaction.completed",
					metadata: { total_usage: { total_input_tokens: 100, total_cached_tokens: 40 } },
				},
			],
			"https://google.invalid/v1/interactions",
		);
		expect((await analyze(row(), raw)).after?.inputTokens).toBe(60);
	});
	test("Anthropic partial TTL delta preserves sibling TTL counter", async () => {
		const raw = stream(
			[
				{
					type: "message_start",
					message: {
						usage: {
							input_tokens: 100,
							cache_read_input_tokens: 40,
							cache_creation_input_tokens: 7,
							cache_creation: { ephemeral_5m_input_tokens: 3, ephemeral_1h_input_tokens: 4 },
						},
					},
				},
				{ type: "message_delta", usage: { cache_creation: { ephemeral_5m_input_tokens: 3 } } },
				{ type: "message_stop" },
			],
			"https://relay.invalid/v1/messages",
		);
		expect((await analyze(row(100, 40, 7, 3, 4), raw)).classification).toBe("correct");
	});
	test("Anthropic incomplete message has no final usage", async () => {
		const raw = dump(
			{
				type: "message",
				stop_reason: null,
				usage: { input_tokens: 100, cache_read_input_tokens: 40 },
			},
			"https://relay.invalid/v1/messages",
		);
		expect((await analyze(row(), raw)).classification).toBe("insufficient");
	});
	test("negative cache/output and overflowing read+write are invalid evidence", async () => {
		for (const usage of [
			{ ...oa(), cache_read_tokens: -1 },
			{ ...oa(), output_tokens: -1 },
			oa(100, Number.MAX_SAFE_INTEGER, 1),
		]) {
			expect((await analyze(row(), dump(response(usage)))).classification).toBe("ambiguous");
		}
	});
});

describe("manual protocol hint stays subordinate to raw evidence", () => {
	const custom = "https://private.invalid/custom-api";
	const protocols = ["openai", "anthropic", "gemini"] as const;
	const hinted = (
		protocolHint: (typeof protocols)[number],
		payload: unknown,
		before = row(),
		url = custom,
	) => analyzeTokenRepairEvidence(before, dump(payload, url), { protocolHint });

	test("OpenAI hint explains bare upstream usage at custom URL", async () => {
		const payload = { usage: oa() };
		expect((await analyze(row(), dump(payload, custom))).classification).toBe("insufficient");
		const evidence = await hinted("openai", payload);
		expect(evidence).toMatchObject({
			classification: "repairable",
			protocol: "openai",
			manualProtocol: "openai",
			after: { inputTokens: 60 },
		});
		expect(evidence.evidence).toContain("manualProtocol=openai");
	});
	test("Anthropic hint preserves net bare usage and only repairs known TTL aggregate", async () => {
		const payload = { usage: { input_tokens: 100, cache_read_input_tokens: 40 } };
		expect((await analyze(row(), dump(payload, custom))).classification).toBe("insufficient");
		expect(await hinted("anthropic", payload)).toMatchObject({
			classification: "correct",
			protocol: "anthropic",
			manualProtocol: "anthropic",
			after: { inputTokens: 100 },
		});
		const ttl = {
			usage: {
				...payload.usage,
				cache_creation_input_tokens: 2,
				cache_creation: { ephemeral_5m_input_tokens: 3, ephemeral_1h_input_tokens: 4 },
			},
		};
		expect(await hinted("anthropic", ttl, row(100, 40, 2, 3, 4))).toMatchObject({
			classification: "repairable",
			after: { inputTokens: 100, cacheCreationInputTokens: 7 },
		});
	});
	for (const unfinished of [
		{ stop_reason: null },
		{ stop_reason: "" },
		{ stop_reason: "in_progress" },
		{ status: "in_progress" },
		{ status: "incomplete" },
		{ status: "failed" },
		{ status: null },
		{ incomplete_details: { reason: "unfinished" } },
		{ done: false },
		{ completed: false },
	]) {
		test(`Anthropic bare hint rejects explicit unfinished terminal ${JSON.stringify(unfinished)}`, async () => {
			const usage = {
				input_tokens: 100,
				cache_read_input_tokens: 40,
				cache_creation_input_tokens: 2,
				cache_creation: { ephemeral_5m_input_tokens: 3, ephemeral_1h_input_tokens: 4 },
			};
			expect(
				await hinted("anthropic", { ...unfinished, usage }, row(100, 40, 2, 3, 4)),
			).toMatchObject({
				classification: "insufficient",
				reason: "response_incomplete",
				manualProtocol: "anthropic",
				after: null,
			});
			// The same validation applies without hints when the endpoint identifies Anthropic.
			expect(
				(
					await analyze(
						row(100, 40, 2, 3, 4),
						dump({ ...unfinished, usage }, "https://relay.invalid/v1/messages"),
					)
				).classification,
			).toBe("insufficient");
		});
	}
	test("Anthropic bare final stop reasons keep reliable terminal usage repairable", async () => {
		const usage = {
			input_tokens: 100,
			cache_read_input_tokens: 40,
			cache_creation_input_tokens: 2,
			cache_creation: { ephemeral_5m_input_tokens: 3, ephemeral_1h_input_tokens: 4 },
		};
		for (const stop_reason of ["end_turn", "tool_use", "max_tokens", "stop_sequence"]) {
			expect(
				(
					await hinted(
						"anthropic",
						{ stop_reason, status: "completed", incomplete_details: null, usage },
						row(100, 40, 2, 3, 4),
					)
				).classification,
			).toBe("repairable");
		}
	});
	test("Gemini hint explains both bare Interactions usage and generateContent metadata", async () => {
		for (const payload of [
			{ usage: { total_input_tokens: 100, total_cached_tokens: 40 } },
			{ usageMetadata: { promptTokenCount: 100, cachedContentTokenCount: 40 } },
		]) {
			expect((await analyze(row(), dump(payload, custom))).classification).toBe("insufficient");
			expect(await hinted("gemini", payload)).toMatchObject({
				classification: "repairable",
				protocol: "gemini",
				manualProtocol: "gemini",
				after: { inputTokens: 60 },
			});
		}
	});
	test("manual choice never overrides explicit endpoint", async () => {
		for (const protocol of ["anthropic", "gemini"] as const) {
			expect(
				await hinted(protocol, { usage: oa() }, row(), "https://relay.invalid/v1/responses"),
			).toMatchObject({
				classification: "ambiguous",
				reason: "protocol_conflict",
				manualProtocol: protocol,
				after: null,
			});
		}
		expect(
			(
				await hinted(
					"openai",
					{ usage: { input_tokens: 100 } },
					row(),
					"https://relay.invalid/v1/messages",
				)
			).classification,
		).toBe("ambiguous");
		expect(
			(
				await hinted(
					"anthropic",
					{ usage: { input_tokens: 100 } },
					row(),
					"https://relay.invalid/v1/interactions",
				)
			).classification,
		).toBe("ambiguous");
	});
	test("manual choice never overrides explicit response protocol or token schema", async () => {
		for (const payload of [
			response(),
			{ type: "response.completed", response: response() },
			{ usage: oa() },
		])
			expect((await hinted("anthropic", payload)).classification).toBe("ambiguous");
		for (const payload of [
			{ type: "message", usage: { input_tokens: 100 } },
			{ usageMetadata: { promptTokenCount: 100 } },
			{ usage: { total_input_tokens: 100 } },
		])
			expect((await hinted("openai", payload)).classification).toBe("ambiguous");
	});
	test("matching hint does not change automatic protocol results", async () => {
		const automatic = await analyze();
		const manual = await hinted("openai", response(), row(), "https://relay.invalid/v1/responses");
		expect(manual.classification).toBe(automatic.classification);
		expect(manual.after).toEqual(automatic.after);
	});
	for (const protocolHint of protocols) {
		test(`${protocolHint} cannot rescue missing dump, missing raw counters, or snapshots`, async () => {
			const missing = await analyzeTokenRepairEvidence(row(), null, { protocolHint });
			expect(missing).toMatchObject({
				classification: "insufficient",
				manualProtocol: protocolHint,
				after: null,
			});
			expect(missing.evidence).toBe(`manualProtocol=${protocolHint};`);
			for (const payload of [
				{ usage: { output_tokens: 10 } },
				{ usage: { inputTokens: 100, cachedInputTokens: 40 } },
				{ context: { input_tokens: 100 } },
			])
				expect((await hinted(protocolHint, payload)).classification).toBe("insufficient");
		});
		test(`${protocolHint} cannot rescue capture truncation or retry ambiguity`, async () => {
			for (const partial of [
				{ attempts: [{}] },
				{
					response: {
						bodyIncomplete: true,
						bodyText: JSON.stringify({ usage: { input_tokens: 100 } }),
					},
				},
				{ truncated: true },
			]) {
				const raw = { ...JSON.parse(dump({ usage: { input_tokens: 100 } }, custom)), ...partial };
				expect(
					await analyzeTokenRepairEvidence(row(), JSON.stringify(raw), { protocolHint }),
				).toMatchObject({
					classification: "insufficient",
					manualProtocol: protocolHint,
					after: null,
				});
			}
		});
		test(`${protocolHint} cannot rescue invalid input counters`, async () => {
			const payload = { usage: { input_tokens: -1, cache_read_input_tokens: 40 } };
			expect(await hinted(protocolHint, payload)).toMatchObject({
				classification: "ambiguous",
				manualProtocol: protocolHint,
				after: null,
			});
		});
		test(`${protocolHint} refuses old counts without exact historical conversion`, async () => {
			const payload = {
				usage: { input_tokens: 100, cache_read_input_tokens: 40, cached_input_tokens: 40 },
			};
			expect(await hinted(protocolHint, payload, row(17))).toMatchObject({
				classification: "ambiguous",
				reason: "unrecognized_previous_conversion",
				manualProtocol: protocolHint,
				after: null,
			});
		});
	}
	test("manual summaries are bounded and contain no request details", async () => {
		const evidence = await hinted("openai", { usage: oa() });
		expect(evidence.evidence.length).toBeLessThan(512);
		expect(JSON.stringify(evidence)).not.toMatch(/https:|private|Authorization|secret|prompt/);
	});
});

describe("shared final-payload integrity across protocols", () => {
	const unfinished = [
		{ done: false },
		{ completed: false },
		{ status: "in_progress" },
		{ status: "incomplete" },
		{ status: null },
		{ incomplete_details: { reason: "unfinished" } },
	];
	const finished = { done: true, completed: true, status: "completed", incomplete_details: null };
	for (const protocol of ["openai", "anthropic", "gemini"] as const) {
		const usage =
			protocol === "anthropic"
				? {
						input_tokens: 100,
						cache_read_input_tokens: 40,
						cache_creation_input_tokens: 2,
						cache_creation: { ephemeral_5m_input_tokens: 3, ephemeral_1h_input_tokens: 4 },
					}
				: { input_tokens: 100, cached_input_tokens: 40 };
		const before = protocol === "anthropic" ? row(100, 40, 2, 3, 4) : row();
		const endpoint = `https://relay.invalid/v1/${protocol === "openai" ? "responses" : protocol === "anthropic" ? "messages" : "interactions"}`;
		for (const manual of [false, true]) {
			test(`${protocol} ${manual ? "manual custom API" : "automatic endpoint"} validates final done/completed/status/incomplete markers`, async () => {
				const options = manual ? { protocolHint: protocol } : undefined;
				const url = manual ? "https://relay.invalid/custom-api" : endpoint;
				for (const markers of unfinished) {
					const evidence = await analyzeTokenRepairEvidence(
						before,
						dump({ ...markers, usage }, url),
						options,
					);
					expect(evidence).toMatchObject({
						classification: "insufficient",
						reason: "response_incomplete",
						after: null,
					});
				}
				expect(
					(await analyzeTokenRepairEvidence(before, dump({ ...finished, usage }, url), options))
						.classification,
				).toBe("repairable");
			});
		}
		test(`${protocol} checks the terminal stream payload, not earlier in-progress frames`, async () => {
			const events = (markers: Record<string, unknown>): unknown[] =>
				protocol === "anthropic"
					? [
							{
								type: "message_start",
								message: { status: "in_progress", stop_reason: null, done: false, usage },
							},
							{ type: "message_stop", ...markers },
						]
					: protocol === "gemini"
						? [
								{
									event_type: "interaction.in_progress",
									interaction: {
										status: "in_progress",
										done: false,
										usage: { total_input_tokens: 50, total_cached_tokens: 10 },
									},
								},
								{ event_type: "interaction.completed", interaction: { ...markers, usage } },
							]
						: [
								{
									type: "response.created",
									response: { status: "in_progress", done: false, usage: oa(50, 10) },
								},
								{ type: "response.completed", response: { ...markers, usage } },
							];
			for (const markers of unfinished)
				expect((await analyze(before, stream(events(markers), endpoint))).classification).toBe(
					"insufficient",
				);
			expect((await analyze(before, stream(events(finished), endpoint))).classification).toBe(
				"repairable",
			);
		});
	}
	test("OpenAI cumulative completion usage can progress before the final complete snapshot", async () => {
		const events = [
			{
				object: "chat.completion.chunk",
				status: "in_progress",
				done: false,
				usage: { prompt_tokens: 50, prompt_tokens_details: { cached_tokens: 10 } },
			},
			{ object: "chat.completion.chunk", choices: [{ finish_reason: "stop" }] },
			{
				object: "chat.completion.chunk",
				done: true,
				usage: { prompt_tokens: 100, prompt_tokens_details: { cached_tokens: 40 } },
			},
		];
		const url = "https://relay.invalid/v1/chat/completions";
		expect((await analyze(row(), stream(events, url))).after?.inputTokens).toBe(60);
		const last = events[events.length - 1];
		expect(
			(
				await analyze(
					row(),
					stream([...events.slice(0, -1), { ...last, status: "in_progress", done: false }], url),
				)
			).classification,
		).toBe("insufficient");
	});
});

async function spilled() {
	const parent = await mkdtemp(join(tmpdir(), "nf-token-evidence-"));
	dirs.push(parent);
	const directory = join(parent, "request-dumps");
	await mkdir(directory);
	const path = join(directory, "2026-01-01T00-00-00Z_request-one_random.json");
	const data = JSON.stringify({
		schema: "narrafork.api-request-dump.v1",
		requestId: "request-one",
		dump: JSON.parse(dump(response())),
	});
	await writeFile(path, data);
	const pointer = {
		schema: "narrafork.api-request-dump-spill.v1",
		filePath: path,
		bytes: Buffer.byteLength(data),
		inlineTruncated: true,
		writtenForRequestId: "request-one",
	};
	return { parent, directory, path, data, pointer };
}
async function analyzeSpill(pointer: unknown, directory: string) {
	return analyzeTokenRepairEvidence(
		row(),
		JSON.stringify({ spill: pointer, response: { bodyText: "incomplete head" } }),
		{ dumpDirectory: directory },
	);
}
describe("bounded, ownership-verified request dump files", () => {
	test("verified complete pointer is read, never inline head", async () => {
		const { directory, pointer } = await spilled();
		expect((await analyzeSpill(pointer, directory)).classification).toBe("repairable");
	});
	for (const change of [
		{ writtenForRequestId: "other" },
		{ writtenForRequestId: undefined },
		{ reusedFromRequestId: "earlier" },
		{ truncated: true },
		{ schema: "unknown" },
		{ bytes: 1 },
		{ fileName: "client-path.json", filePath: undefined },
	]) {
		test(`unverified pointer ${JSON.stringify(change)} refuses inline fallback`, async () => {
			const { directory, pointer } = await spilled();
			expect((await analyzeSpill({ ...pointer, ...change }, directory)).classification).toBe(
				"insufficient",
			);
		});
	}
	test("envelope request ownership is verified independently of pointer", async () => {
		const { directory, pointer, path, data } = await spilled();
		const changed = data.replace('"requestId":"request-one"', '"requestId":"request-two"');
		await writeFile(path, changed);
		expect(
			(await analyzeSpill({ ...pointer, bytes: Buffer.byteLength(changed) }, directory))
				.classification,
		).toBe("insufficient");
	});
	test("missing spill does not use inline data", async () => {
		const { directory, pointer, path } = await spilled();
		await rm(path);
		expect((await analyzeSpill(pointer, directory)).classification).toBe("insufficient");
	});
	test("path traversal and sibling directory paths are refused", async () => {
		const { directory, pointer, parent } = await spilled();
		for (const filePath of [
			`${directory}/../request-dumps/anything.json`,
			join(parent, "outside.json"),
			"../../secret.json",
		]) {
			expect((await analyzeSpill({ ...pointer, filePath }, directory)).classification).toBe(
				"insufficient",
			);
		}
	});
	test("file symlink is refused even when target is within fixed directory", async () => {
		const { directory, pointer, path } = await spilled();
		const link = join(directory, "link.json");
		await symlink(path, link);
		expect((await analyzeSpill({ ...pointer, filePath: link }, directory)).classification).toBe(
			"insufficient",
		);
	});
	test("directory symlink is refused", async () => {
		const { parent, directory, pointer, path } = await spilled();
		const link = join(parent, "linked");
		await symlink(directory, link);
		expect(
			(await analyzeSpill({ ...pointer, filePath: join(link, path.split("/").at(-1) ?? "") }, link))
				.classification,
		).toBe("insufficient");
	});
	test("oversize pointer and actual file are refused before reading", async () => {
		const { directory, pointer, path } = await spilled();
		expect(
			(await analyzeSpill({ ...pointer, bytes: TOKEN_REPAIR_MAX_DUMP_BYTES + 1 }, directory))
				.reason,
		).toBe("dump_over_budget");
		await truncate(path, TOKEN_REPAIR_MAX_DUMP_BYTES + 1);
		expect((await analyzeSpill(pointer, directory)).classification).toBe("insufficient");
	});
	test("oversize inline JSON is refused", async () => {
		const raw = " ".repeat(TOKEN_REPAIR_MAX_DUMP_BYTES + 1);
		expect((await analyze(row(), raw)).reason).toBe("dump_over_budget");
	});
	test("existing counters are unchanged and only recognized after fields are returned", async () => {
		const before = row();
		const copy = { ...before };
		const evidence = await analyze(before);
		expect(before).toEqual(copy);
		expect(evidence.before).toEqual(
			Object.fromEntries(
				Object.entries(copy).filter(([key]) => !["id", "provider", "model"].includes(key)),
			) as unknown as TokenRepairCounters,
		);
	});
});
