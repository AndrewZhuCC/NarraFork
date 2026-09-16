import { describe, expect, test } from "bun:test";
import {
	MAX_DIAGNOSTIC_JSON_CHARS,
	normalizeApiRequestDiagnostics,
	parseErrorDiagnostics,
} from "../error-diagnostics";
import {
	ApiRequestDumpCollector,
	BoundedUtf8Capture,
	captureResponseStream,
	HARD_DUMP_MAX_BYTES,
	MAX_DUMP_EVENT_COUNT,
	responseDumpBudget,
	sanitizeHeaders,
} from "../request-dump";

describe("bounded diagnostic capture", () => {
	test("full WS frames do not duplicate the prepared history and crowd out the response", () => {
		const collector = new ApiRequestDumpCollector({}, 4 * 1024 * 1024);
		const prepared = {
			transport: "websocket",
			body: {
				type: "response.create",
				input: [{ role: "user", content: "x".repeat(2300000) }],
				tools: [{ type: "function", name: "Read" }],
				client_metadata: { session_id: "session" },
			},
		};
		collector.beginResponseAttempt(prepared, 4 * 1024 * 1024);
		const actual = {
			...prepared,
			body: {
				...prepared.body,
				client_metadata: {
					...prepared.body.client_metadata,
					"x-codex-turn-state": "routing-state",
				},
			},
		};
		collector.setRequest(actual);
		collector.setResponseMeta({ status: 200, headers: { "x-request-id": "upstream-id" } });
		collector.appendResponseText(
			'data: {"type":"response.completed","response":{"usage":{"input_tokens_details":{"cached_tokens":3968}}}}\n\n',
		);
		collector.finishResponseCapture(true);
		const result = collector.snapshot();
		expect(result.initialRequest === undefined).toBe(true);
		expect(JSON.stringify(result.request?.body) === JSON.stringify(actual.body)).toBe(true);
		expect(result.response?.headers?.["x-request-id"]).toBe("upstream-id");
		expect(result.response?.bodyText).toContain('"cached_tokens":3968');
		expect(result.captureTruncated).toBeUndefined();
		expect(Buffer.byteLength(JSON.stringify(result))).toBeLessThan(4 * 1024 * 1024);
	});

	test("oversized requests cannot erase response status, headers and available response text", () => {
		const collector = new ApiRequestDumpCollector({}, 4096);
		collector.beginResponseAttempt({ body: { input: "x".repeat(10000) } }, 4096);
		collector.setResponseMeta({ status: 200, headers: { "x-request-id": "kept" } });
		collector.appendResponseText("small complete reply");
		collector.finishResponseCapture(true);
		const result = collector.snapshot();
		expect(result.captureTruncated).toBe(true);
		expect(result.response?.status).toBe(200);
		expect(result.response?.headers?.["x-request-id"]).toBe("kept");
		expect(result.response?.bodyText).toBe("small complete reply");
		expect(Buffer.byteLength(JSON.stringify(result))).toBeLessThanOrEqual(4096);
	});

	test("preserves the logical WS request and the actual frame without modifying either", () => {
		const collector = new ApiRequestDumpCollector({ provider: "openai" }, 4096);
		const full = { transport: "websocket", body: { input: [{ role: "user", content: "before" }] } };
		const delta = {
			transport: "websocket",
			body: { input: [], previous_response_id: "resp-before" },
		};
		collector.beginResponseAttempt(full, 4096);
		collector.setRequest(delta);
		full.body.input[0].content = "changed later";
		collector.appendResponseText("completed");
		collector.finishResponseCapture(true);
		const result = collector.snapshot();
		expect(result.initialRequest?.body).toEqual({ input: [{ role: "user", content: "before" }] });
		expect(result.request?.body).toEqual(delta.body);
		expect(result.response?.bodyText).toBe("completed");
		expect(result.captureTruncated).toBeUndefined();
	});

	test("bounds request, response and combined UTF8 snapshot including escaped strings", () => {
		const collector = new ApiRequestDumpCollector({}, 1024);
		collector.beginResponseAttempt(
			{
				body: { text: '中文😀\\"\n'.repeat(10000) },
				headers: { Authorization: "Bearer secret-value" },
			},
			32 * 1024 * 1024,
		);
		collector.appendResponseText("x".repeat(10000));
		collector.finishResponseCapture(true);
		const result = collector.snapshot();
		expect(result.captureTruncated).toBe(true);
		expect(Buffer.byteLength(JSON.stringify(result), "utf8")).toBeLessThanOrEqual(1024);
		expect(JSON.stringify(result)).not.toContain("secret-value");
	});

	test("bounded snapshot is independently available without changing normal full-dump retention", () => {
		const collector = new ApiRequestDumpCollector();
		collector.setRequest({ body: { text: "x".repeat(10000) } });
		expect(collector.snapshot(1024).captureTruncated).toBe(true);
		expect(collector.snapshot().request?.body).toEqual({ text: "x".repeat(10000) });
		expect(collector.snapshot().captureTruncated).toBeUndefined();
	});

	test("depth and node limits stop pathological request graphs", () => {
		const collector = new ApiRequestDumpCollector({}, 4096);
		const circular: Record<string, unknown> = { input: Array(100000).fill(null) };
		circular.self = circular;
		collector.setRequest({ body: circular });
		const result = collector.snapshot();
		expect(result.captureTruncated).toBe(true);
		expect(Buffer.byteLength(JSON.stringify(result))).toBeLessThanOrEqual(4096);
	});
});

describe("incremental response capture", () => {
	test("UTF8 byte boundaries preserve prefixes and seal on truncation", () => {
		const c = new BoundedUtf8Capture(5);
		c.append("中😀");
		c.append("a");
		expect(c.text()).toBe("中");
		expect(c.received).toBe(8);
		const split = new BoundedUtf8Capture(7);
		const bytes = new TextEncoder().encode("中😀x");
		for (const byte of bytes) split.append(new Uint8Array([byte]));
		expect(split.text()).toBe("中😀");
		expect(split.kept).toBe(7);
		expect(responseDumpBudget(-1)).toBe(HARD_DUMP_MAX_BYTES);
		expect(responseDumpBudget(Infinity)).toBe(HARD_DUMP_MAX_BYTES);
		expect(new BoundedUtf8Capture(0).text()).toBe("");
	});

	test("many tiny chunks stay a correct bounded prefix across storage blocks", () => {
		const capture = new BoundedUtf8Capture(65537);
		for (let i = 0; i < 100000; i++) capture.append(new Uint8Array([97]));
		expect(capture.text()).toBe("a".repeat(65537));
		expect(capture.received).toBe(100000);
		const partial = new BoundedUtf8Capture(5);
		partial.append(new TextEncoder().encode("中😀"));
		expect(partial.text()).toBe("中");
		expect(capture.kept).toBe(65537);
	});

	test("disabled capture does not wrap the source", () => {
		const source = new ReadableStream<Uint8Array>();
		expect(captureResponseStream(source).stream).toBe(source);
	});

	test("HTTP early exit cancels without draining and keeps received bytes", async () => {
		const dump = new ApiRequestDumpCollector();
		dump.beginResponseAttempt({ transport: "http" }, 32);
		let pulls = 0;
		let cancelled = false;
		const capture = captureResponseStream(
			new ReadableStream(
				{
					pull(controller) {
						pulls++;
						controller.enqueue(new TextEncoder().encode("raw中"));
					},
					cancel() {
						cancelled = true;
					},
				},
				{ highWaterMark: 0 },
			),
			dump,
		);
		await capture.stream.getReader().read();
		capture.finish();
		expect(pulls).toBe(1);
		expect(cancelled).toBe(true);
		expect(dump.snapshot().response).toMatchObject({ bodyText: "raw中", bodyIncomplete: true });
	});

	test("stream error preserves the prefix and attempts share a byte budget", async () => {
		const dump = new ApiRequestDumpCollector();
		dump.beginResponseAttempt({ transport: "websocket", body: "first" }, 5);
		dump.appendResponseText("abc");
		dump.beginResponseAttempt({ transport: "http", body: "second" }, 5);
		let reads = 0;
		const tap = captureResponseStream(
			new ReadableStream(
				{
					pull(controller) {
						if (reads++ === 0) controller.enqueue(new TextEncoder().encode("def"));
						else controller.error(new Error("broken"));
					},
				},
				{ highWaterMark: 0 },
			),
			dump,
		);
		const reader = tap.stream.getReader();
		await reader.read();
		await expect(reader.read()).rejects.toThrow("broken");
		expect(dump.snapshot().response).toMatchObject({
			bodyText: "de",
			bodyTruncated: true,
			error: "broken",
		});
		expect(dump.snapshot().attempts?.[0].response?.bodyText).toBe("abc");
		expect(dump.snapshot().initialRequest?.body).toBe("first");
		expect(dump.snapshot().request?.body).toBe("second");
	});
});

describe("ApiRequestDumpCollector.setResponseEventsWithLimit", () => {
	test("keeps all events when under both caps", () => {
		const c = new ApiRequestDumpCollector();
		const events = [{ a: 1 }, { b: 2 }, { c: 3 }];
		c.setResponseEventsWithLimit(events, 100, 1024 * 1024);
		expect(c.snapshot().response?.events).toEqual(events);
	});

	test("truncates by element count and appends a marker", () => {
		const c = new ApiRequestDumpCollector();
		const events = Array.from({ length: 10 }, (_, i) => ({ i }));
		c.setResponseEventsWithLimit(events, 4, 1024 * 1024);
		const stored = c.snapshot().response?.events as unknown[];
		// 4 kept + 1 truncation marker
		expect(stored).toHaveLength(5);
		expect(stored.slice(0, 4)).toEqual(events.slice(0, 4));
		expect(stored[4]).toEqual({ _truncated: 6 });
	});

	test("truncates by byte size from the tail", () => {
		const c = new ApiRequestDumpCollector();
		// Each event serializes to a sizable string; cap bytes so only a few fit.
		const events = Array.from({ length: 20 }, (_, i) => ({ i, pad: "x".repeat(100) }));
		c.setResponseEventsWithLimit(events, MAX_DUMP_EVENT_COUNT, 300);
		const stored = c.snapshot().response?.events as unknown[];
		// At least one event dropped → truncation marker present.
		const marker = stored.at(-1) as { _truncated?: number };
		expect(marker._truncated).toBeGreaterThan(0);
		// Final serialized size is within a small multiple of the cap (kept + marker).
		expect(JSON.stringify(stored).length).toBeLessThan(600);
	});

	test("negative caps disable limiting", () => {
		const c = new ApiRequestDumpCollector();
		const events = Array.from({ length: 5 }, (_, i) => ({ i }));
		c.setResponseEventsWithLimit(events, -1, -1);
		expect(c.snapshot().response?.events).toEqual(events);
	});
});

describe("sanitizeHeaders", () => {
	test("masks provider API-key headers, including Gemini's x-goog-api-key", () => {
		const masked = sanitizeHeaders({
			authorization: "Bearer sk-super-secret-token",
			"x-api-key": "anthropic-secret-key",
			"x-goog-api-key": "AIzaSyGeminiSecretKey123",
			"content-type": "application/json",
		}) as Record<string, string>;
		// Secrets are masked (never stored verbatim in a request dump).
		expect(masked.authorization).not.toContain("super-secret");
		expect(masked["x-api-key"]).not.toContain("anthropic-secret-key");
		expect(masked["x-goog-api-key"]).not.toContain("AIzaSyGeminiSecretKey123");
		expect(masked["x-goog-api-key"]).toContain("********");
		// Non-sensitive headers pass through untouched.
		expect(masked["content-type"]).toBe("application/json");
	});

	test("matches sensitive header names case-insensitively", () => {
		const masked = sanitizeHeaders({ "X-Goog-Api-Key": "AIzaSyAnotherSecretKey" }) as Record<
			string,
			string
		>;
		expect(masked["X-Goog-Api-Key"]).not.toContain("AIzaSyAnotherSecretKey");
		expect(masked["X-Goog-Api-Key"]).toContain("********");
	});
});

describe("error diagnostics", () => {
	test("stores normalized diagnostics on the request dump", () => {
		const collector = new ApiRequestDumpCollector({ provider: "nug" });
		collector.setDiagnostics(
			normalizeApiRequestDiagnostics({
				source: "gateway",
				statusCode: 502,
				requestId: "req-123",
				responseHeaders: {
					"x-request-id": "req-123",
					authorization: "Bearer secret",
				},
			}),
		);
		expect(collector.snapshot().diagnostics).toEqual({
			schema: "narrafork.error-diagnostics.v1",
			source: "gateway",
			statusCode: 502,
			requestId: "req-123",
			responseHeaders: { "x-request-id": "req-123" },
		});
	});

	test("enforces an independent total diagnostics size limit", () => {
		const diagnostics = normalizeApiRequestDiagnostics({
			message: "m".repeat(20_000),
			responseSnippet: "r".repeat(20_000),
			cause: "c".repeat(20_000),
			requestId: "req-123",
		});
		expect(Buffer.byteLength(JSON.stringify(diagnostics), "utf8")).toBeLessThanOrEqual(
			MAX_DIAGNOSTIC_JSON_CHARS,
		);
		expect(diagnostics?.requestId).toBe("req-123");
	});

	test("keeps the byte ceiling with large identity fields and unicode", () => {
		const diagnostics = normalizeApiRequestDiagnostics({
			source: "源".repeat(8192),
			requestId: "请求".repeat(8192),
			providerRequestId: "网关".repeat(8192),
		});
		expect(Buffer.byteLength(JSON.stringify(diagnostics), "utf8")).toBeLessThanOrEqual(
			MAX_DIAGNOSTIC_JSON_CHARS,
		);
	});

	test("preserves the HTTP status supplied as a parser default", () => {
		const diagnostics = parseErrorDiagnostics(
			{ error: { type: "server_error", message: "boom" } },
			{ statusCode: 503, source: "gateway" },
		);
		expect(diagnostics?.statusCode).toBe(503);
	});
});
