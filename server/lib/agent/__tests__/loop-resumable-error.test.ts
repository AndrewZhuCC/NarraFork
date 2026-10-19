import { afterAll, afterEach, beforeEach, describe, expect, mock, spyOn, test } from "bun:test";
import { RequestRecorderClient } from "../../request-recorder";
import type { AnthropicProviderConfig } from "../../settings";
import { settings, usesStatefulModel } from "../../settings";
import { AnthropicProvider } from "../anthropic-provider";
import type { ProviderAdapter } from "../provider";
import { ApiRequestDumpCollector } from "../request-dump";
import type { AgentConfig, AgentEvent } from "../types";

const provider = new AnthropicProvider({
	id: "loop-resumable-anthropic",
	name: "Loop Resumable Anthropic",
	prefix: "loop-resumable",
	apiKey: "test-key",
	baseUrl: "https://example.com/v1",
	defaultModel: "claude-test",
} satisfies AnthropicProviderConfig);

const realProviderModule = { ...(await import("../provider")) };

mock.module("../provider", () => ({
	...realProviderModule,
	getProvider: () => provider as ProviderAdapter,
	resolveProviderAndModel: () => ({
		requestedProvider: "loop-resumable",
		requestedModel: "loop-resumable:claude-test",
		provider: "loop-resumable",
		adapter: provider as ProviderAdapter,
		model: "loop-resumable:claude-test",
	}),
}));

const { agentLoop } = await import("../loop");
const originalFetch = globalThis.fetch;

/**
 * Simulate the exact NUG gateway SSE shape (writeSSEError): a partial text
 * delta followed by an `event: error` carrying `diagnostics.resumable`. This
 * is what happens when the upstream stream disconnects after forwarding
 * client-visible payload — NUG clamps `retryable` to false but flags
 * `resumable: true` for transient transport failures.
 */
function partialTextThenResumableError(options: {
	text: string;
	resumable: boolean;
	retryable?: boolean;
	statusCode?: number;
}): Response {
	const events = [
		'event: message_start\ndata: {"type":"message_start","message":{"id":"msg_partial","usage":{"input_tokens":5}}}\n\n',
		'event: content_block_start\ndata: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}\n\n',
		`event: content_block_delta\ndata: ${JSON.stringify({
			type: "content_block_delta",
			index: 0,
			delta: { type: "text_delta", text: options.text },
		})}\n\n`,
		`event: error\ndata: ${JSON.stringify({
			type: "error",
			code: options.statusCode ?? 502,
			message: "upstream stream error",
			error: { type: "error", code: options.statusCode ?? 502, message: "upstream stream error" },
			diagnostics: {
				source: "channel",
				phase: "upstream_error",
				reason: "stream_read_error",
				statusCode: options.statusCode ?? 502,
				retryable: options.retryable ?? false,
				resumable: options.resumable,
			},
		})}\n\n`,
	];
	return new Response(events.join(""), {
		status: 200,
		headers: { "content-type": "text/event-stream" },
	});
}

function installResponses(responses: Response[]): () => number {
	let calls = 0;
	globalThis.fetch = (async () => {
		const response = responses[calls++];
		if (!response) throw new Error(`Unexpected fetch call ${calls}`);
		return response;
	}) as unknown as typeof fetch;
	return () => calls;
}

/**
 * The gateway sets `resumable` once it forwarded any client-visible SSE
 * payload, but its notion of "visible" includes non-content events (queue
 * notices, a tool chunk carrying only an id, lifecycle frames). Those leave
 * nothing persistable on our side. The pass must still hand off for a
 * continuation (the next request is assembled from the latest state) rather
 * than dying on a transient transport fault or replaying in place.
 */
function emptyThenResumableError(): Response {
	const events = [
		'event: message_start\ndata: {"type":"message_start","message":{"id":"msg_empty","usage":{"input_tokens":2}}}\n\n',
		`event: error\ndata: ${JSON.stringify({
			type: "error",
			code: 502,
			message: "upstream stream error",
			error: { type: "error", code: 502, message: "upstream stream error" },
			diagnostics: {
				source: "channel",
				phase: "upstream_error",
				reason: "stream_read_error",
				statusCode: 502,
				retryable: false,
				resumable: true,
			},
		})}\n\n`,
	];
	return new Response(events.join(""), {
		status: 200,
		headers: { "content-type": "text/event-stream" },
	});
}

function makeConfig(signal: AbortSignal, overrides: Partial<AgentConfig> = {}): AgentConfig {
	return {
		narratorId: "n-loop-resumable",
		conversationId: "conv-loop-resumable",
		model: "loop-resumable:claude-test",
		provider: "loop-resumable",
		cwd: "/tmp",
		signal,
		permissionHandler: async () => ({ behavior: "allow" }),
		toolFilter: () => false,
		maxTransientRetries: 1,
		retryBackoffCeilMs: 0,
		...overrides,
	};
}

async function runLoop(overrides: Partial<AgentConfig> = {}): Promise<AgentEvent[]> {
	const events: AgentEvent[] = [];
	const controller = new AbortController();
	for await (const event of agentLoop(makeConfig(controller.signal, overrides), "answer", [])) {
		events.push(event);
	}
	return events;
}

afterEach(() => {
	globalThis.fetch = originalFetch;
});

afterAll(() => {
	globalThis.fetch = originalFetch;
	mock.module("../provider", () => realProviderModule);
	mock.restore();
});

describe("agentLoop resumable-error handling (NUG continuation-after-forwarded-payload)", () => {
	test("ends the recording once with the error and correlates diagnostics to it", async () => {
		const evidence = settings.requestEvidence;
		settings.requestEvidence = { ...evidence, ringEnabled: true };
		// Observe the producer boundary only; no recorder worker or filesystem IO.
		const configure = spyOn(RequestRecorderClient.prototype, "configure").mockImplementation(
			() => {},
		);
		const capture = spyOn(RequestRecorderClient.prototype, "capture").mockImplementation(
			() => true,
		);
		const finished: Array<{ error?: string; status?: string } | undefined> = [];
		const originalFinish = ApiRequestDumpCollector.prototype.finish;
		const finish = spyOn(ApiRequestDumpCollector.prototype, "finish").mockImplementation(function (
			this: ApiRequestDumpCollector,
			value,
		) {
			finished.push(value);
			return originalFinish.call(this, value);
		});
		try {
			installResponses([partialTextThenResumableError({ text: "partial", resumable: true })]);
			const events = await runLoop();
			expect(finished).toHaveLength(1);
			expect(finished[0]?.status).toBe("error");
			expect(finished[0]?.error).toBeTruthy();
			// Diagnostics no longer ride in the recording; they travel on api_request_end
			// (persisted as bounded diagnostics_json) and point back at the recording.
			const end = events.find(
				(event): event is Extract<AgentEvent, { type: "api_request_end" }> =>
					event.type === "api_request_end",
			);
			expect(end?.requestId).toBeTruthy();
			expect(end?.diagnostics?.recordingRequestId).toBe(end?.requestId);
		} finally {
			settings.requestEvidence = evidence;
			finish.mockRestore();
			capture.mockRestore();
			configure.mockRestore();
		}
	});

	test("resumable=true with partial text yields resumable_error instead of retrying or terminating", async () => {
		const fetchCalls = installResponses([
			partialTextThenResumableError({ text: "partial answer before disconnect", resumable: true }),
		]);

		const events = await runLoop();

		// Must NOT retry the whole request — the gateway already forwarded payload.
		expect(fetchCalls()).toBe(1);
		expect(events.some((event) => event.type === "retrying")).toBe(false);
		expect(events.some((event) => event.type === "retryable_error")).toBe(false);
		// Partial text must be flushed as a block_complete before signalling resumable_error.
		const blockComplete = events.find(
			(event): event is Extract<AgentEvent, { type: "block_complete" }> =>
				event.type === "block_complete",
		);
		expect(blockComplete?.block).toMatchObject({
			type: "text",
			text: "partial answer before disconnect",
		});
		const resumableEvent = events.find((event) => event.type === "resumable_error");
		expect(resumableEvent).toMatchObject({
			type: "resumable_error",
			message: "upstream stream error",
			diagnostics: { resumable: true, retryable: false },
		});
		// resumable_error is a terminal yield for this loop pass — the caller
		// (narrator-session) is responsible for injecting a continuation turn.
		expect(events.at(-1)).toBe(resumableEvent);
		const end = events.find((event) => event.type === "api_request_end");
		if (!end || !resumableEvent) throw new Error("missing resumable request events");
		expect(resumableEvent.diagnostics).toBe(end.diagnostics);
		expect(resumableEvent.diagnostics?.recordingRequestId).toBe(end.requestId);
		if (!end.diagnostics) throw new Error("missing request diagnostics");
		end.diagnostics.apiRequestId = "resumable-durable-row";
		expect(resumableEvent.diagnostics?.apiRequestId).toBe("resumable-durable-row");
	});

	test("resumable=false (no NUG signal) with partial text falls back to invalid_state (terminal)", async () => {
		const fetchCalls = installResponses([
			partialTextThenResumableError({ text: "partial answer", resumable: false }),
		]);

		const events = await runLoop();

		expect(fetchCalls()).toBe(1);
		expect(events.some((event) => event.type === "resumable_error")).toBe(false);
		expect(events.some((event) => event.type === "retrying")).toBe(false);
		expect(events.at(-1)).toMatchObject({
			type: "invalid_state",
			diagnostics: { resumable: false },
		});
	});

	test("resumable=true with no local output hands off via resumable_error instead of failing", async () => {
		const fetchCalls = installResponses([emptyThenResumableError()]);

		const events = await runLoop({ maxTransientRetries: 1 });

		// No in-place replay: the pass ends and the caller assembles the next
		// request from the latest state.
		expect(fetchCalls()).toBe(1);
		expect(events.some((event) => event.type === "retrying")).toBe(false);
		expect(events.some((event) => event.type === "invalid_state")).toBe(false);
		expect(events.at(-1)).toMatchObject({
			type: "resumable_error",
			message: "upstream stream error",
			diagnostics: { resumable: true, retryable: false },
		});
	});

	test("resumable=true with no local output hands off regardless of the transient retry budget", async () => {
		const fetchCalls = installResponses([emptyThenResumableError()]);

		const events = await runLoop({ maxTransientRetries: 0 });

		// The handoff is not a retry, so a zero budget must not turn it into a
		// terminal failure.
		expect(fetchCalls()).toBe(1);
		expect(events.some((event) => event.type === "retrying")).toBe(false);
		expect(events.at(-1)).toMatchObject({ type: "resumable_error" });
	});

	test("hard non-retryable quota text vetoes an optimistic resumable=true even with partial output", async () => {
		const events3 = [
			'event: message_start\ndata: {"type":"message_start","message":{"id":"msg_quota","usage":{"input_tokens":2}}}\n\n',
			'event: content_block_start\ndata: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}\n\n',
			`event: content_block_delta\ndata: ${JSON.stringify({
				type: "content_block_delta",
				index: 0,
				delta: { type: "text_delta", text: "partial before quota cutoff" },
			})}\n\n`,
			`event: error\ndata: ${JSON.stringify({
				type: "error",
				code: 402,
				message: "insufficient_quota: check your plan and billing details",
				error: {
					type: "error",
					code: 402,
					message: "insufficient_quota: check your plan and billing details",
				},
				diagnostics: {
					source: "channel",
					phase: "upstream_error",
					reason: "insufficient_quota",
					statusCode: 402,
					retryable: false,
					resumable: true,
				},
			})}\n\n`,
		];
		const fetchCalls = installResponses([
			new Response(events3.join(""), {
				status: 200,
				headers: { "content-type": "text/event-stream" },
			}),
		]);

		const events = await runLoop();

		expect(fetchCalls()).toBe(1);
		expect(events.some((event) => event.type === "resumable_error")).toBe(false);
		expect(events.at(-1)).toMatchObject({ type: "invalid_state" });
	});
});

describe("agentLoop resumable handoff on stateful (responses/codex) models", () => {
	// Registering the mocked provider's prefix with apiMode "responses" flips
	// usesStatefulModel() for the loop, reproducing the production state of NUG
	// codex channels. The resumable handoff must not depend on the in-place
	// retry budget at all — that budget is 0 for stateful models, which is what
	// used to turn this interruption into a hard failure ("Error: 上游连接错误").
	const savedOpenaiProviders = settings.openaiProviders;
	beforeEach(() => {
		settings.openaiProviders = [
			{
				id: "loop-resumable-openai",
				name: "Loop Resumable OpenAI",
				prefix: "loop-resumable",
				apiKey: "test-key",
				baseUrl: "https://example.com/v1",
				defaultModel: "claude-test",
				apiMode: "responses",
			},
		];
		// Pin that the registration actually flips the stateful classification —
		// otherwise the tests below would pass vacuously.
		expect(usesStatefulModel("loop-resumable", "loop-resumable:claude-test")).toBe(true);
	});
	afterEach(() => {
		settings.openaiProviders = savedOpenaiProviders;
	});

	test("resumable=true with no local output hands off for continuation on a stateful model", async () => {
		const fetchCalls = installResponses([emptyThenResumableError()]);

		const events = await runLoop({ maxTransientRetries: 1 });

		expect(fetchCalls()).toBe(1);
		expect(events.some((event) => event.type === "retrying")).toBe(false);
		expect(events.some((event) => event.type === "invalid_state")).toBe(false);
		expect(events.at(-1)).toMatchObject({
			type: "resumable_error",
			diagnostics: { resumable: true, retryable: false },
		});
	});

	test("maxTransientRetries=0 does not turn the stateful resumable handoff into invalid_state", async () => {
		const fetchCalls = installResponses([emptyThenResumableError()]);

		const events = await runLoop({ maxTransientRetries: 0 });

		expect(fetchCalls()).toBe(1);
		expect(events.some((event) => event.type === "retrying")).toBe(false);
		expect(events.at(-1)).toMatchObject({ type: "resumable_error" });
	});
});
