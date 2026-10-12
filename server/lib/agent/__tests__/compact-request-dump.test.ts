import { afterEach, beforeEach, describe, expect, mock, test } from "bun:test";
import { apiRequests } from "@server/db/schema";
import { settings } from "@server/lib/settings";
import { cleanDb, getTestDb } from "../../../../tests/setup";
import { AnthropicProvider } from "../anthropic-provider";
import { clearCodexResponsesWebSocketSessions } from "../codex-websocket";
import { GeminiInteractionsProvider } from "../gemini-interactions-provider";
import { GeminiProvider } from "../gemini-provider";
import { GrokProvider } from "../grok-provider";
import { OpenAIProvider } from "../openai-provider";
import type { ProviderAdapter } from "../provider";
import type { ApiRequestDump } from "../request-dump";

const { db, sqlite } = getTestDb();
const realDbModule = { ...(await import("@server/db")) };
mock.module("@server/db", () => ({ ...realDbModule, db, sqlite }));
const { trackApiRequest } = await import("../../api-request-tracker");
const { agentGenerateWithMetaResolved } = await import("../index");

const realFetch = globalThis.fetch;
const key = "sk-compact-dump-test-key";
const config = {
	id: "dump-test",
	name: "Dump test",
	prefix: "dump-test",
	apiKey: key,
	baseUrl: "https://example.invalid/v1",
	defaultModel: "gpt-6-luna",
};
const model = "dump-test:gpt-6-luna";
const tracking = { kind: "compact" as const, provider: "dump-test", model };
const compactOutput = { type: "compaction", encrypted_content: "test-encrypted" };
const history = [{ role: "user", content: "compact this conversation" }];
let sentBody: unknown;
let sentUrl = "";

beforeEach(() => {
	settings.agent.requestDumpEnabled = true;
	settings.agent.requestDumpErrorsOnly = true;
	settings.agent.requestDumpMaxSize = 64 * 1024;
	sentBody = undefined;
	sentUrl = "";
});

afterEach(async () => {
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
			conversationId: "compact-dump-http",
			requestDump,
		}),
	);
}

function record() {
	const rows = db.select().from(apiRequests).all();
	expect(rows).toHaveLength(1);
	return rows[0];
}

function dumpOf(row = record()): ApiRequestDump {
	if (!row.rawDumpJson) throw new Error("Expected a persisted request dump");
	return JSON.parse(row.rawDumpJson) as ApiRequestDump;
}

describe("compact request dump", () => {
	test.each([
		[400, "application/json", '{"error":{"message":"invalid compact input"}}'],
		[
			200,
			"text/event-stream",
			'event: response.failed\ndata: {"type":"response.failed","response":{"status":"failed","error":{"message":"response protection is unavailable"}}}\n\n',
		],
	])("remote HTTP retains raw failure (%s)", async (status, contentType, responseBody) => {
		respond(responseBody, status, contentType);
		await expect(compact()).rejects.toThrow();
		const row = record();
		const dump = dumpOf(row);
		expect(row.errorMessage).toBeTruthy();
		expect(dump.request?.body).toEqual(sentBody);
		expect(dump.request?.url).toBe(sentUrl);
		expect(dump.response?.bodyText).toBe(responseBody);
		expect(dump.response?.status).toBe(status);
		expect(dump.response?.error).toBe(row.errorMessage ?? undefined);
		expect(row.rawDumpJson).not.toContain(key);
	});

	test.each([
		[false, false, false],
		[false, true, false],
		[true, true, false],
		[true, false, true],
	])("success follows enabled=%s, errorsOnly=%s", async (enabled, errorsOnly, persisted) => {
		settings.agent.requestDumpEnabled = enabled;
		settings.agent.requestDumpErrorsOnly = errorsOnly;
		respond(JSON.stringify({ status: "completed", output: [compactOutput] }));
		expect((await compact()).items).toEqual([compactOutput]);
		const row = record();
		expect(row.rawDumpJson !== null).toBe(persisted);
		if (persisted) expect(dumpOf(row).request?.body).toEqual(sentBody);
	});

	test("disabled dumping keeps error metadata but not raw content", async () => {
		settings.agent.requestDumpEnabled = false;
		respond('{"error":{"message":"invalid compact input"}}', 400);
		await expect(compact()).rejects.toThrow();
		const row = record();
		expect(row.errorMessage).toContain("invalid compact input");
		const dump = JSON.parse(row.rawDumpJson ?? "{}");
		expect(dump.request).toBeUndefined();
		expect(dump.response).toBeUndefined();
	});

	test("dump toggle does not alter remote request body", async () => {
		respond(JSON.stringify({ status: "completed", output: [compactOutput] }));
		await compact();
		const withDump = sentBody;
		settings.agent.requestDumpEnabled = false;
		await compact();
		expect(sentBody).toEqual(withDump);
	});

	test("transport failure still retains the prepared HTTP request", async () => {
		globalThis.fetch = (async () => {
			throw new Error("test network disconnected");
		}) as unknown as typeof fetch;
		await expect(compact()).rejects.toThrow("Network request failed");
		const dump = dumpOf();
		expect(dump.request?.transport).toBe("http");
		expect(dump.request?.body).toHaveProperty("input");
		expect(dump.response?.error).toContain("Network request failed");
		expect(dump.response?.bodyIncomplete).toBe(true);
	});

	test("WebSocket 1008 saves the actual frame and close reason without HTTP fallback", async () => {
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
			const conversationId = "compact-dump-ws";
			// Obtain a real ordinary-request template; compact may then select WebSocket.
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
				// Consume the ordinary request.
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
			const dump = dumpOf();
			expect(dump.request?.transport).toBe("websocket");
			expect(dump.request?.body).toEqual(frame);
			expect(dump.response?.bodyText).toContain("[websocket close 1008]");
			expect(dump.response?.error).toContain("missing first response.create message");
			expect(JSON.stringify(dump)).not.toContain(key);
			expect(httpRequests).toBe(1);
		} finally {
			await clearCodexResponsesWebSocketSessions();
			await server.stop(true);
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
		{ kind: "compact" },
	);
}

test.each(semanticProviders)("semantic compact captures %s failures", async (_name, create) => {
	const responseBody = '{"error":{"message":"test compact rejection"}}';
	respond(responseBody, 400);
	await expect(semantic(create())).rejects.toThrow();
	const row = record();
	const dump = dumpOf(row);
	expect(dump.request?.body).toEqual(sentBody);
	expect(dump.request?.url).toBe(sentUrl);
	expect(dump.response?.bodyText).toBe(responseBody);
	expect(dump.response?.status).toBe(400);
	expect(row.rawDumpJson).not.toContain(key);
});

test("empty semantic compact is a failed request, not a discarded success dump", async () => {
	respond('{"status":"completed","output":[]}');
	await expect(semantic(remoteProvider())).rejects.toThrow("returned empty output");
	expect(dumpOf().response?.bodyText).toContain('"output":[]');
});
