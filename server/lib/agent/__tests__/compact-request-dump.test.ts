import { afterEach, beforeEach, describe, expect, mock, spyOn, test } from "bun:test";
import { apiRequests, narrators } from "@server/db/schema";
import { settings } from "@server/lib/settings";
import { cleanDb, getTestDb } from "../../../../tests/setup";
import { RequestRecorderClient } from "../../request-recorder";
import type { RecorderOperation } from "../../request-recorder-protocol";
import { AnthropicProvider } from "../anthropic-provider";
import { clearCodexResponsesWebSocketSessions } from "../codex-websocket";
import { GeminiInteractionsProvider } from "../gemini-interactions-provider";
import { GeminiProvider } from "../gemini-provider";
import { GrokProvider } from "../grok-provider";
import { OpenAIProvider } from "../openai-provider";
import type { ProviderAdapter } from "../provider";

const { db, sqlite } = getTestDb();
const realDbModule = { ...(await import("@server/db")) };
mock.module("@server/db", () => ({ ...realDbModule, db, sqlite }));
const { trackApiRequest } = await import("../../api-request-tracker");
const { agentGenerateWithMetaResolved } = await import("../index");
const realFetch = globalThis.fetch;
const key = "sk-compact-evidence-test-key";
const config = {
	id: "dump-test",
	name: "Dump test",
	prefix: "dump-test",
	apiKey: key,
	baseUrl: "https://example.invalid/v1",
	defaultModel: "gpt-6-luna",
};
const model = "dump-test:gpt-6-luna";
const narratorId = "compact-evidence-narrator";
const tracking = { kind: "compact" as const, provider: "dump-test", model, narratorId };
const compactOutput = { type: "compaction", encrypted_content: "test-encrypted" };
const history = [{ role: "user", content: "compact this conversation" }];
let sentBody: unknown;
let sentUrl = "";
let operations: RecorderOperation[] = [];
let configure: ReturnType<typeof spyOn>;
let capture: ReturnType<typeof spyOn>;

beforeEach(() => {
	const now = new Date().toISOString();
	db.insert(narrators).values({ id: narratorId, createdAt: now, updatedAt: now }).run();
	settings.requestEvidence = {
		ringEnabled: true,
		ringRequestBytes: 1024 * 1024,
		ringResponseBytes: 1024 * 1024,
		autoPin: "errors",
	};
	operations = [];
	configure = spyOn(RequestRecorderClient.prototype, "configure").mockImplementation(() => {});
	capture = spyOn(RequestRecorderClient.prototype, "capture").mockImplementation((operation) => {
		operations.push(operation);
		return true;
	});
	sentBody = undefined;
	sentUrl = "";
});
afterEach(async () => {
	configure.mockRestore();
	capture.mockRestore();
	globalThis.fetch = realFetch;
	await clearCodexResponsesWebSocketSessions();
	cleanDb(sqlite);
});

function respond(body: string, status = 200, contentType = "application/json") {
	globalThis.fetch = (async (input: unknown, init?: RequestInit) => {
		sentUrl = String(input);
		sentBody = JSON.parse(String(init?.body));
		return new Response(body, { status, headers: { "content-type": contentType } });
	}) as typeof fetch;
}
function remoteProvider(baseUrl = config.baseUrl) {
	return new OpenAIProvider({
		...config,
		baseUrl,
		apiMode: "codex",
		codexRemoteCompact: true,
		codexWebSocket: false,
	});
}
function compact(provider = remoteProvider()) {
	return trackApiRequest(tracking, (requestDump) =>
		provider.compactRemote(history, model, {
			conversationId: "compact-evidence-http",
			requestDump,
		}),
	);
}
function record() {
	const rows = db.select().from(apiRequests).all();
	expect(rows).toHaveLength(1);
	return rows[0];
}
function evidence() {
	const requestMeta = Object.assign(
		{},
		...operations.flatMap((operation) =>
			operation.type === "meta" && operation.side === "request" ? [operation.value] : [],
		),
	);
	const responseMeta = Object.assign(
		{},
		...operations.flatMap((operation) =>
			operation.type === "meta" && operation.side === "response" ? [operation.value] : [],
		),
	);
	const text = (side: "request" | "response") =>
		operations
			.flatMap((operation) =>
				operation.type === "chunk" && operation.side === side
					? [Buffer.from(operation.data).toString()]
					: [],
			)
			.join("");
	return {
		requestMeta,
		responseMeta,
		requestText: text("request"),
		responseText: text("response"),
	};
}

describe("compact request recorder evidence", () => {
	test.each([
		[400, "application/json", '{"error":{"message":"invalid compact input"}}'],
		[
			200,
			"text/event-stream",
			'event: response.failed\ndata: {"type":"response.failed","response":{"status":"failed","error":{"message":"response protection is unavailable"}}}\n\n',
		],
	])("remote HTTP retains actual failure (%s)", async (status, contentType, responseBody) => {
		respond(responseBody, status, contentType);
		await expect(compact()).rejects.toThrow();
		const row = record();
		const saved = evidence();
		expect(row.errorMessage).toBeTruthy();
		expect(JSON.parse(saved.requestText)).toEqual(sentBody);
		expect(saved.requestMeta.url).toBe(sentUrl);
		expect(saved.responseText).toBe(responseBody);
		expect(saved.responseMeta.status).toBe(status);
		expect(saved.responseMeta.error).toBe(row.errorMessage);
		expect(JSON.stringify(operations)).not.toContain(key);
		expect(operations).toContainEqual(
			expect.objectContaining({ type: "end", error: row.errorMessage, status: "error" }),
		);
		const recordingRequestId = row.recordingRequestId;
		if (!recordingRequestId) throw new Error("Expected a recording request ID");
		expect(operations).toContainEqual({
			type: "bind",
			requestId: recordingRequestId,
			apiRequestId: row.id,
		});
		expect(row).not.toHaveProperty("rawDumpJson");
	});

	test.each([
		[false, "off", false],
		[true, "off", true],
		[true, "errors", true],
		[false, "all", true],
	] as const)("success respects live recording ring=%s autoPin=%s", async (ringEnabled, autoPin, recorded) => {
		settings.requestEvidence = { ...settings.requestEvidence, ringEnabled, autoPin };
		respond(JSON.stringify({ status: "completed", output: [compactOutput] }));
		expect((await compact()).items).toEqual([compactOutput]);
		expect(record().errorMessage).toBeNull();
		expect(operations.some((operation) => operation.type === "begin")).toBe(recorded);
		if (recorded) expect(JSON.parse(evidence().requestText)).toEqual(sentBody);
	});

	test("disabled recording keeps error metadata without raw content", async () => {
		settings.requestEvidence = { ...settings.requestEvidence, ringEnabled: false, autoPin: "off" };
		respond('{"error":{"message":"invalid compact input"}}', 400);
		await expect(compact()).rejects.toThrow();
		expect(record().errorMessage).toContain("invalid compact input");
		expect(operations.filter((operation) => operation.type !== "bind")).toEqual([]);
	});

	test("transport failure retains the prepared request and recording association", async () => {
		globalThis.fetch = (async () => {
			throw new Error("test network disconnected");
		}) as unknown as typeof fetch;
		await expect(compact()).rejects.toThrow("Network request failed");
		const row = record();
		expect(evidence().requestMeta.transport).toBe("http");
		expect(JSON.parse(evidence().requestText)).toHaveProperty("input");
		expect(evidence().responseMeta.error).toContain("Network request failed");
		expect(JSON.parse(row.diagnosticsJson ?? "{}").recordingRequestId).toBe(row.recordingRequestId);
	});

	test("WebSocket 1008 retains exact frame and close reason without HTTP fallback", async () => {
		let frame: unknown;
		let httpRequests = 0;
		const server = Bun.serve({
			hostname: "127.0.0.1",
			port: 0,
			fetch(req, srv) {
				if (srv.upgrade(req)) return undefined;
				httpRequests++;
				return new Response(
					'data: {"type":"response.completed","response":{"id":"resp_warm","status":"completed","output":[]}}\n\n',
					{ headers: { "content-type": "text/event-stream" } },
				);
			},
			websocket: {
				message(ws, raw) {
					frame = JSON.parse(String(raw));
					ws.close(1008, "missing first response.create message");
				},
			},
		});
		try {
			const provider = remoteProvider(`http://127.0.0.1:${server.port}/v1`);
			const conversationId = "compact-evidence-ws";
			for await (const _event of provider.chat({
				model,
				history,
				tools: [],
				cwd: process.cwd(),
				toolResults: [],
				content: ".",
				conversationId,
				signal: new AbortController().signal,
			})) {
				/* warm template */
			}
			await expect(
				trackApiRequest(tracking, (requestDump) =>
					provider.compactRemote(history, model, {
						conversationId,
						useWebSocket: true,
						requestDump,
						signal: AbortSignal.timeout(3000),
					}),
				),
			).rejects.toThrow("1008");
			const saved = evidence();
			expect(saved.requestMeta.transport).toBe("websocket");
			expect(JSON.parse(saved.requestText)).toEqual(frame);
			expect(operations).toContainEqual(
				expect.objectContaining({
					type: "meta",
					side: "response",
					value: expect.objectContaining({ type: "websocket.close", code: 1008 }),
				}),
			);
			expect(saved.responseMeta.error).toContain("missing first response.create message");
			expect(JSON.stringify(operations)).not.toContain(key);
			expect(httpRequests).toBe(1);
			expect(record().recordingRequestId).toBeTruthy();
		} finally {
			await clearCodexResponsesWebSocketSessions();
			server.stop(true);
		}
	});
});

const semanticProviders: [string, () => ProviderAdapter][] = [
	["Codex", remoteProvider],
	["OpenAI Responses", () => new OpenAIProvider({ ...config, apiMode: "responses" })],
	["OpenAI Completions", () => new OpenAIProvider({ ...config, apiMode: "completions" })],
	["Anthropic", () => new AnthropicProvider(config)],
	["Gemini", () => new GeminiProvider(config)],
	["Gemini Interactions", () => new GeminiInteractionsProvider(config)],
	["Grok", () => new GrokProvider(config)],
];
function semantic(provider: ProviderAdapter) {
	return agentGenerateWithMetaResolved(
		"conversation to summarize",
		{
			requestedProvider: "dump-test",
			requestedModel: model,
			provider: "dump-test",
			model,
			adapter: provider,
		},
		"summarize",
		{ signal: AbortSignal.timeout(3000) },
		{ kind: "compact", narratorId },
	);
}
test.each(semanticProviders)("semantic compact records %s failures", async (_name, create) => {
	const responseBody = '{"error":{"message":"test compact rejection"}}';
	respond(responseBody, 400);
	await expect(semantic(create())).rejects.toThrow();
	const saved = evidence();
	expect(JSON.parse(saved.requestText)).toEqual(sentBody);
	expect(saved.requestMeta.url).toBe(sentUrl);
	expect(saved.responseText).toBe(responseBody);
	expect(saved.responseMeta.status).toBe(400);
	expect(JSON.stringify(operations)).not.toContain(key);
	expect(record().recordingRequestId).toBeTruthy();
});
test("empty semantic compact ends its recording as failed", async () => {
	respond('{"status":"completed","output":[]}');
	await expect(semantic(remoteProvider())).rejects.toThrow("returned empty output");
	expect(evidence().responseText).toContain('"output":[]');
	expect(operations).toContainEqual(expect.objectContaining({ type: "end", status: "error" }));
});
