import { afterAll, describe, expect, mock, spyOn, test } from "bun:test";
import { createHash } from "node:crypto";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { RecordingSettings, SavedRecording } from "@shared/request-recording";
import { startApiRequest } from "../../api-request-tracker";
import { RequestRecorderClient } from "../../request-recorder";
import {
	REQUEST_RECORDING_SAVED_DIR,
	type RecorderOperation,
} from "../../request-recorder-protocol";
import { RequestRecorderStore } from "../../request-recorder-store";
import { settings } from "../../settings";
import type { ProviderAdapter } from "../provider";
import type { AgentConfig, AgentEvent } from "../types";
import { ApiError } from "../types";

/**
 * Request evidence settings must be live: saving `requestEvidence` affects the next
 * provider request in an already-running process without rebuilding the narrator
 * session. Whether a recorder bridge exists is decided at each attempt start; whether the
 * recording is kept (auto-pin) is decided by the recorder when the request ends.
 */

/** Whether the last provider.chat() received a dump collector. */
let lastChatHadDump = false;
/** Whether any provider.chat() in the current run received a dump collector. */
let anyChatHadDump = false;
let chatCount = 0;
let failBeforeStart = false;
let responseScenario:
	| "empty"
	| "nameless-tool"
	| "no-start"
	| "401"
	| "503"
	| "disconnect"
	| undefined;

const testProvider: ProviderAdapter = {
	formatTools: (tools) => tools,
	buildHistory: async () => ({ history: [], trailingToolResults: [] }),
	injectSystemPrompt: () => {},
	async *chat(params) {
		chatCount++;
		if (failBeforeStart) throw new ApiError(401, "missing credential before transport");
		if (responseScenario === "no-start") return;
		lastChatHadDump = params.requestDump != null;
		if (params.requestDump) anyChatHadDump = true;
		params.requestDump?.setRequest({
			transport: "http",
			url: "https://example.test/v1/messages",
			body: { hello: "world" },
		});
		params.onRequestStart?.();
		params.requestDump?.setResponseMeta({
			status: responseScenario === "401" ? 401 : responseScenario === "503" ? 503 : 200,
		});
		if (responseScenario === "401" || responseScenario === "503")
			throw new ApiError(Number(responseScenario), "Rejected before first stream event");
		if (responseScenario === "disconnect")
			throw new Error("ECONNRESET: upstream connection closed");
		params.requestDump?.appendResponseText('{"ok":true}');
		if (responseScenario === "empty") {
			yield { usage: { promptTokens: 1, completionTokens: 0 }, stopReason: "end_turn" };
			return;
		}
		if (responseScenario === "nameless-tool") {
			yield { toolUseChunk: { toolUseId: "empty-tool", stop: false } };
			return;
		}
		yield { text: "ok" };
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
// These tests observe the producer/association boundary, not worker filesystem IO.
const configureRecorder = spyOn(RequestRecorderClient.prototype, "configure").mockImplementation(
	() => {},
);

afterAll(() => {
	configureRecorder.mockRestore();
	mock.module("../provider", () => realProviderModule);
	mock.restore();
});

function makeConfig(signal: AbortSignal): AgentConfig {
	return {
		narratorId: "n-dump-live",
		conversationId: "conv-dump-live",
		model: "test:model",
		provider: "test",
		cwd: "/tmp",
		signal,
		permissionHandler: async () => ({ behavior: "allow" }),
		retryBackoffCeilMs: 1,
	};
}

/** Apply a partial `requestEvidence` patch; returns a restore function. */
function setEvidence(patch: Partial<RecordingSettings>): () => void {
	const previous = settings.requestEvidence;
	settings.requestEvidence = { ...previous, ...patch };
	return () => {
		settings.requestEvidence = previous;
	};
}

async function runLoop(onEvent?: (event: AgentEvent) => void): Promise<AgentEvent[]> {
	const ac = new AbortController();
	const events: AgentEvent[] = [];
	anyChatHadDump = false;
	for await (const event of agentLoop(makeConfig(ac.signal), "go", [])) {
		events.push(event);
		onEvent?.(event);
	}
	return events;
}

describe("request evidence 开关实时生效", () => {
	test("保存后下一次请求即带上 recorder bridge，无需重启叙述者", async () => {
		const restore = setEvidence({ ringEnabled: false, autoPin: "off" });
		const discardCapture = spyOn(RequestRecorderClient.prototype, "capture").mockImplementation(
			() => true,
		);
		try {
			// Off: next user message after save-to-off records nothing.
			chatCount = 0;
			await runLoop();
			expect(chatCount).toBeGreaterThan(0);
			expect(anyChatHadDump).toBe(false);

			// Toggle on — simulates a settings PATCH updating the singleton mid-process.
			settings.requestEvidence = { ...settings.requestEvidence, ringEnabled: true };
			await runLoop();
			expect(anyChatHadDump).toBe(true);
			expect(lastChatHadDump).toBe(true);

			// Toggle off again: the subsequent request must stop recording immediately.
			settings.requestEvidence = { ...settings.requestEvidence, ringEnabled: false };
			await runLoop();
			expect(anyChatHadDump).toBe(false);

			// autoPin alone (ring off) still records: pinning needs a recording to keep.
			settings.requestEvidence = { ...settings.requestEvidence, autoPin: "errors" };
			await runLoop();
			expect(anyChatHadDump).toBe(true);
		} finally {
			restore();
			discardCapture.mockRestore();
		}
	});

	test("请求尚未onRequestStart便失败也结束录制并保留error", async () => {
		const restore = setEvidence({ ringEnabled: true });
		const operations: RecorderOperation[] = [];
		const capture = spyOn(RequestRecorderClient.prototype, "capture").mockImplementation(
			(operation) => {
				operations.push(operation);
				return true;
			},
		);
		try {
			failBeforeStart = true;
			const events = await runLoop();
			expect(events.some((event) => event.type === "api_request_start")).toBe(false);
			expect(events.some((event) => event.type === "error")).toBe(true);
			const begins = operations.filter((operation) => operation.type === "begin");
			const ends = operations.filter((operation) => operation.type === "end");
			expect(begins.length).toBeGreaterThan(0);
			expect(ends.length).toBe(begins.length);
			for (const end of ends) expect(end.error).toContain("missing credential before transport");
		} finally {
			failBeforeStart = false;
			capture.mockRestore();
			restore();
		}
	});

	test.each([
		"empty",
		"nameless-tool",
		"401",
		"503",
		"disconnect",
		"no-start",
	] as const)("autoPin=errors 保存最终判定为错误的 %s，并在懒start后保留API关联", async (scenario) => {
		const home = await mkdtemp(join(tmpdir(), "loop-recording-verdict-"));
		const store = new RequestRecorderStore(home);
		const evidence: RecordingSettings = {
			ringEnabled: true,
			ringRequestBytes: 1024 * 1024,
			ringResponseBytes: 1024 * 1024,
			autoPin: "errors",
		};
		const restore = setEvidence(evidence);
		const operations: RecorderOperation[] = [];
		let writes: Promise<void> = Promise.resolve();
		const capture = spyOn(RequestRecorderClient.prototype, "capture").mockImplementation(
			(operation) => {
				operations.push(operation);
				writes = writes.then(async () => {
					await store.execute(operation);
				});
				return true;
			},
		);
		try {
			await store.execute({ type: "configure", settings: evidence });
			responseScenario = scenario;
			const apiIds = new Map<string, string>();
			const events = await runLoop((event) => {
				if (event.type !== "api_request_start") return;
				const handle = startApiRequest({
					recordingRequestId: event.requestId,
					narratorId: "n-dump-live",
					provider: event.provider,
					model: event.model,
					kind: "narrator",
				});
				apiIds.set(event.requestId, handle.id);
			});
			await writes;
			const ends = operations.filter((operation) => operation.type === "end");
			const begins = operations.filter((operation) => operation.type === "begin");
			expect(ends.length).toBeGreaterThan(0);
			expect(ends).toHaveLength(begins.length);
			for (const end of ends) {
				expect(end.status).toBe("error");
				expect(end.error).toBeTruthy();
			}
			const listed = (await store.execute({ type: "list", narratorId: "n-dump-live" })) as {
				items: SavedRecording[];
			};
			expect(listed.items).toHaveLength(ends.length);
			for (const saved of listed.items) {
				const end = ends.find((candidate) => saved.requestIds.includes(candidate.requestId));
				expect(end).toBeDefined();
				const manifest = JSON.parse(
					await readFile(
						join(
							home,
							REQUEST_RECORDING_SAVED_DIR,
							createHash("sha256").update("n-dump-live").digest("hex"),
							saved.id,
							"manifest.json",
						),
						"utf8",
					),
				);
				expect(manifest.requests[0].error).toBe(end?.error);
				expect(manifest.requests[0].status).toBe("error");
				if (scenario !== "no-start") {
					const apiId = apiIds.get(saved.requestIds[0]);
					expect(apiId).toBeDefined();
					expect(saved.apiRequestIds).toEqual([apiId ?? "missing API association"]);
					const verdict = events.find(
						(event) => event.type === "api_request_end" && event.requestId === saved.requestIds[0],
					);
					if (verdict?.type === "api_request_end") expect(verdict.errorMessage).toBe(end?.error);
				}
			}
		} finally {
			responseScenario = undefined;
			capture.mockRestore();
			restore();
			await writes.catch(() => {});
			await rm(home, { recursive: true, force: true });
		}
	});

	test("consumer return 在最终判定前退出也结束录制，成功请求只在最终判定后finish", async () => {
		const restore = setEvidence({ ringEnabled: true });
		const operations: RecorderOperation[] = [];
		const capture = spyOn(RequestRecorderClient.prototype, "capture").mockImplementation(
			(operation) => {
				operations.push(operation);
				return true;
			},
		);
		try {
			const controller = new AbortController();
			for await (const event of agentLoop(makeConfig(controller.signal), "go", [])) {
				if (event.type !== "stream_text") continue;
				expect(operations.some((operation) => operation.type === "end")).toBe(false);
				break;
			}
			const ended = operations.filter((operation) => operation.type === "end");
			expect(ended).toHaveLength(1);
			expect(ended[0]).toMatchObject({
				status: "interrupted",
				error: "Attempt exited before final response classification",
			});
			operations.length = 0;
			await runLoop();
			const success = operations.filter((operation) => operation.type === "end");
			expect(success).toHaveLength(1);
			expect(success[0]).toMatchObject({ status: "completed", error: undefined });
		} finally {
			capture.mockRestore();
			restore();
		}
	});

	test("录制identity使用与start事件一致的冻结请求owner，而不是随后变化的session owner", async () => {
		const restore = setEvidence({ ringEnabled: true });
		const operations: RecorderOperation[] = [];
		const capture = spyOn(RequestRecorderClient.prototype, "capture").mockImplementation(
			(operation) => {
				operations.push(operation);
				return true;
			},
		);
		try {
			const config = makeConfig(new AbortController().signal);
			let ownerReads = 0;
			Object.defineProperty(config, "userId", { get: () => `shared-owner-${++ownerReads}` });
			const events: AgentEvent[] = [];
			for await (const event of agentLoop(config, "go", [])) events.push(event);
			const start = events.find((event) => event.type === "api_request_start");
			const begin = operations.find((operation) => operation.type === "begin");
			expect(start?.type === "api_request_start" ? start.userId : undefined).toBe(
				begin?.type === "begin" ? begin.identity.userId : undefined,
			);
			expect(begin?.type === "begin" ? begin.identity.userId : undefined).not.toBe(config.userId);
		} finally {
			capture.mockRestore();
			restore();
		}
	});

	test("默认循环录制实时上报请求/响应并绑定API行", async () => {
		const restore = setEvidence({ ringEnabled: true, autoPin: "off" });
		const operations: RecorderOperation[] = [];
		const capture = spyOn(RequestRecorderClient.prototype, "capture").mockImplementation(
			(operation) => {
				operations.push(operation);
				return true;
			},
		);
		try {
			const events = await runLoop();
			expect(anyChatHadDump).toBe(true);
			const begin = operations.find((operation) => operation.type === "begin");
			// autoPin=off: ring only, nothing is continuous or errors-only pinned.
			expect(begin).toMatchObject({ continuous: false, errorsOnly: false });
			expect(
				operations.some((operation) => operation.type === "chunk" && operation.side === "request"),
			).toBe(true);
			expect(
				operations.some((operation) => operation.type === "chunk" && operation.side === "response"),
			).toBe(true);
			expect(operations.some((operation) => operation.type === "end")).toBe(true);
			expect(operations.some((operation) => operation.type === "pin")).toBe(false);
			const start = events.find((event) => event.type === "api_request_start");
			expect(start).toBeDefined();
			if (start?.type === "api_request_start") {
				const handle = startApiRequest({
					recordingRequestId: start.requestId,
					narratorId: "n-dump-live",
					provider: start.provider,
					model: start.model,
					kind: "reflection",
				});
				expect(operations).toContainEqual({
					type: "bind",
					requestId: start.requestId,
					apiRequestId: handle.id,
				});
			}
			settings.requestEvidence = { ...settings.requestEvidence, ringEnabled: false };
			await runLoop();
			expect(anyChatHadDump).toBe(false);
		} finally {
			capture.mockRestore();
			restore();
		}
	});
});
