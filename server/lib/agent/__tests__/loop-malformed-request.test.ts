import { afterAll, beforeEach, describe, expect, mock, test } from "bun:test";
import { readFile, rm } from "node:fs/promises";
import { getNarraforkPath } from "../../narrafork-home";
import { MALFORMED_REQUEST_DUMP_DIR } from "../malformed-request-dump";
import type { ProviderAdapter } from "../provider";
import type { AgentConfig, AgentEvent } from "../types";
import { ApiError } from "../types";

const UPSTREAM_BODY = JSON.stringify({
	__type: "com.amazon.kiro.runtimeservice#ValidationException",
	message: "Improperly formed request.",
	reason: "REQUEST_BODY_INVALID",
});

const REQUEST_BODY = {
	conversationState: {
		conversationId: "conv-malformed",
		history: [
			{
				assistantResponseMessage: {
					content: "",
					toolUses: [{ toolUseId: "tu-orphan", name: "Bash", input: { command: "echo hi" } }],
				},
			},
		],
		currentMessage: {
			userInputMessage: { content: "go", modelId: "claude-sonnet-4.5" },
		},
	},
};

/** "throw" = provider throws (kiro plugin path); "stream" = SSE error event (NUG relay path). */
let mode: "throw" | "stream" | "unrelated" = "throw";

const testProvider: ProviderAdapter = {
	mayLeakXmlToolCalls: true,
	formatTools: (tools) => tools,
	buildHistory: async () => ({ history: [], trailingToolResults: [] }),
	injectSystemPrompt: () => {},
	async *chat(params) {
		params.requestDump?.setRequest({
			transport: "kiro-plugin",
			url: "https://q.us-east-1.amazonaws.com",
			headers: { Authorization: "Bearer secret-token" },
			body: REQUEST_BODY,
		});
		params.onRequestStart?.();
		if (mode === "throw") {
			params.requestDump?.setResponseMeta({ status: 400 });
			params.requestDump?.setResponseBodyText(UPSTREAM_BODY);
			throw new ApiError(400, `Error: ${UPSTREAM_BODY}`, {
				schema: "narrafork.error-diagnostics.v1",
				statusCode: 400,
			});
		}
		if (mode === "stream") {
			yield {
				invalidState: {
					reason: "validation_exception",
					message: "Improperly formed request.",
					diagnostics: { schema: "narrafork.error-diagnostics.v1", statusCode: 400 },
				},
			};
			return;
		}
		throw new ApiError(400, "some other bad request", {
			schema: "narrafork.error-diagnostics.v1",
			statusCode: 400,
		});
	},
	formatToolResult: (toolUseId, output, isError) => ({ toolUseId, output, isError }),
	pushUserTurn: () => {},
	pushAssistantTurn: () => {},
	generate: async () => "",
	generateWithMeta: async () => ({ text: "" }),
	generateWithHistory: async () => "",
};

const realProviderModule = { ...(await import("../provider")) };

mock.module("../provider", () => ({
	...realProviderModule,
	getProvider: () => testProvider,
	resolveProviderAndModel: () => ({
		requestedProvider: "test",
		requestedModel: "test:model",
		provider: "test",
		adapter: testProvider,
		model: "test:model",
	}),
}));

const { agentLoop } = await import("../loop");

const dumpDir = getNarraforkPath(MALFORMED_REQUEST_DUMP_DIR);

beforeEach(async () => {
	await rm(dumpDir, { recursive: true, force: true }).catch(() => {});
});

afterAll(async () => {
	await rm(dumpDir, { recursive: true, force: true }).catch(() => {});
	mock.module("../provider", () => realProviderModule);
	mock.restore();
});

function makeConfig(signal: AbortSignal): AgentConfig {
	return {
		narratorId: "n-malformed-loop",
		conversationId: "conv-malformed",
		model: "test:model",
		provider: "test",
		cwd: "/tmp",
		signal,
		permissionHandler: async () => ({ behavior: "allow" }),
		toolFilter: () => false,
		maxTransientRetries: 0,
		retryBackoffCeilMs: 0,
		maxTurns: 1,
	};
}

async function runLoop(): Promise<AgentEvent[]> {
	const events: AgentEvent[] = [];
	const controller = new AbortController();
	for await (const event of agentLoop(makeConfig(controller.signal), "go", [])) {
		events.push(event);
	}
	return events;
}

type ApiEnd = Extract<AgentEvent, { type: "api_request_end" }>;
type CaptureRecord = {
	capture?: { reason?: string; filePath?: string | null; summary?: Record<string, unknown> };
};

describe("agentLoop malformed-request-body capture", () => {
	test("captures the request when the provider throws the upstream ValidationException", async () => {
		mode = "throw";
		const events = await runLoop();

		const apiEnd = events.find((e): e is ApiEnd => e.type === "api_request_end");
		expect(apiEnd).toBeDefined();
		// Forced so the capture survives even with dumping disabled / errors-only.
		expect(apiEnd?.forceDumpPersist).toBe(true);

		const record = apiEnd?.rawDump as CaptureRecord;
		expect(record.capture?.reason).toBe("malformed_request_body");
		const filePath = record.capture?.filePath;
		expect(typeof filePath).toBe("string");
		// The structural summary flags the orphaned toolUse that likely caused the rejection.
		expect(record.capture?.summary?.unmatchedToolUseIds).toEqual(["tu-orphan"]);
		// The DB-side record does not carry the request body itself.
		expect(JSON.stringify(record)).not.toContain("conversationId");

		const saved = JSON.parse(await readFile(filePath as string, "utf8")) as {
			request: { body: unknown; headers: Record<string, string> };
			response: { status?: number; bodyText?: string };
		};
		// The exact request body that upstream rejected is on disk, verbatim.
		expect(saved.request.body).toEqual(REQUEST_BODY);
		expect(JSON.stringify(saved.request.headers)).not.toContain("secret-token");
		expect(saved.response.status).toBe(400);
		expect(saved.response.bodyText).toContain("REQUEST_BODY_INVALID");
	});

	test("captures the request when the rejection arrives as a stream error event", async () => {
		mode = "stream";
		const events = await runLoop();

		const apiEnd = events.find((e): e is ApiEnd => e.type === "api_request_end");
		expect(apiEnd?.forceDumpPersist).toBe(true);
		const record = apiEnd?.rawDump as CaptureRecord;
		expect(record.capture?.reason).toBe("malformed_request_body");

		const saved = JSON.parse(await readFile(record.capture?.filePath as string, "utf8")) as {
			request: { body: unknown };
		};
		expect(saved.request.body).toEqual(REQUEST_BODY);
	});

	test("does not capture unrelated 400 errors", async () => {
		mode = "unrelated";
		const events = await runLoop();

		const apiEnd = events.find((e): e is ApiEnd => e.type === "api_request_end");
		expect(apiEnd?.forceDumpPersist).toBeFalsy();
		expect((apiEnd?.rawDump as CaptureRecord)?.capture).toBeUndefined();
	});
});
