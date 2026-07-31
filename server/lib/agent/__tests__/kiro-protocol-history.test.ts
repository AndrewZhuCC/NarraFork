import { afterEach, beforeEach, describe, expect, test } from "bun:test";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import type { KiroChatRequest, KiroToolResult } from "../../kiro-adapter";
import { StreamStaleError } from "../../stream-timeout";
import { setUploadsDirForTests } from "../../uploads";
import { hostHistoryRuntime } from "../codewhisperer-host-runtime";
import { isRetryableError } from "../error-handling";
import { buildKiroHistory, normalizeKiroChatRequest, parseSSEStream } from "../kiro-protocol";
import type { DbMessage, ParsedStreamEvent } from "../provider";

const SAMPLE_PNG_BASE64 =
	"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGP4z8DwHwAFAAH/iZk9HQAAAABJRU5ErkJggg==";
let testUploadsRoot = "";

function registerTestImage(narratorId: string, imageId: string, ext = ".png"): string {
	const dir = resolve(testUploadsRoot, narratorId);
	mkdirSync(dir, { recursive: true });
	const filePath = resolve(dir, `${imageId}${ext}`);
	writeFileSync(filePath, Buffer.from(SAMPLE_PNG_BASE64, "base64"));
	return filePath;
}

function makeUserMessage(overrides: Partial<DbMessage> = {}): DbMessage {
	return {
		id: "user-1",
		role: "user",
		contentJson: [],
		contentText: null,
		parentToolUseId: null,
		messageUuid: null,
		toolCalls: [],
		...overrides,
	};
}

function makeAssistantMessage(overrides: Partial<DbMessage> = {}): DbMessage {
	return {
		id: "assistant-1",
		role: "assistant",
		contentJson: [],
		contentText: null,
		parentToolUseId: null,
		messageUuid: null,
		toolCalls: [],
		...overrides,
	};
}

function makeSysMessage(overrides: Partial<DbMessage> = {}): DbMessage {
	return {
		id: "sys-1",
		role: "sys",
		contentJson: [],
		contentText: null,
		parentToolUseId: null,
		messageUuid: null,
		toolCalls: [],
		...overrides,
	};
}

function streamFromText(text: string): ReadableStream<Uint8Array> {
	return new ReadableStream({
		start(controller) {
			controller.enqueue(new TextEncoder().encode(text));
			controller.close();
		},
	});
}

beforeEach(() => {
	testUploadsRoot = mkdtempSync(join(tmpdir(), "narrafork-kiro-history-"));
	setUploadsDirForTests(testUploadsRoot);
});

afterEach(() => {
	setUploadsDirForTests(null);
	if (testUploadsRoot) {
		rmSync(testUploadsRoot, { recursive: true, force: true });
		testUploadsRoot = "";
	}
});

describe("buildKiroHistory", () => {
	test("replays persisted user images from the message owner narrator", async () => {
		registerTestImage("source-narrator", "img_hist");
		const dbMessages: DbMessage[] = [
			makeUserMessage({
				id: "user-image",
				narratorId: "source-narrator",
				contentJson: [
					{
						type: "image",
						imageId: "img_hist",
						filename: "sample.png",
						mediaType: "image/png",
					},
					{ type: "text", text: "Inspect this screenshot" },
				],
				contentText: "Inspect this screenshot",
			}),
			makeAssistantMessage({
				id: "assistant-after",
				contentJson: [{ type: "text", text: "done" }],
				contentText: "done",
			}),
		];

		// Images are host-owned, so the production path supplies the upload-backed
		// resolver from the host runtime binding rather than reaching into `uploads`.
		const result = await buildKiroHistory(
			dbMessages,
			"kiro:claude-sonnet-4.6",
			"forked-narrator",
			undefined,
			hostHistoryRuntime(),
		);
		const historyJson = JSON.stringify(result.history);

		expect(historyJson).toContain("Inspect this screenshot");
		expect(historyJson).toContain(SAMPLE_PNG_BASE64);
		expect(historyJson).toContain('"images"');
	});

	test("omits images when no host image resolver is supplied", async () => {
		registerTestImage("source-narrator", "img_no_resolver");
		const dbMessages: DbMessage[] = [
			makeUserMessage({
				id: "user-image-bare",
				narratorId: "source-narrator",
				contentJson: [
					{
						type: "image",
						imageId: "img_no_resolver",
						filename: "sample.png",
						mediaType: "image/png",
					},
					{ type: "text", text: "Inspect this screenshot" },
				],
				contentText: "Inspect this screenshot",
			}),
			makeAssistantMessage({
				id: "assistant-after-bare",
				contentJson: [{ type: "text", text: "done" }],
				contentText: "done",
			}),
		];

		const result = await buildKiroHistory(dbMessages, "kiro:claude-sonnet-4.6", "forked-narrator");
		const historyJson = JSON.stringify(result.history);

		// Text still replays; only the image attachment is skipped.
		expect(historyJson).toContain("Inspect this screenshot");
		expect(historyJson).not.toContain(SAMPLE_PNG_BASE64);
		expect(historyJson).not.toContain('"images"');
	});

	test("moves trailing sys spec continuation into trailing user text", async () => {
		const dbMessages: DbMessage[] = [
			makeUserMessage({ id: "user-start", contentText: "start" }),
			makeAssistantMessage({ id: "assistant-start", contentText: "ready" }),
			makeSysMessage({
				id: "sys-spec",
				contentJson: [
					{
						type: "text",
						text: "Continue working on the current Dynamic Spec task.",
					},
					{ type: "spec_continuation", task: "finish it", protected: false },
				],
				contentText: "Continue working on the current Dynamic Spec task.",
			}),
		];

		const result = await buildKiroHistory(dbMessages, "kiro:claude-sonnet-4.6");
		const historyJson = JSON.stringify(result.history);

		expect(result.trailingUserText).toBe("Continue working on the current Dynamic Spec task.");
		expect(historyJson).not.toContain("Continue working on the current Dynamic Spec task.");
		expect(result.history.at(-1)).toEqual({ assistantResponseMessage: { content: "ready" } });
	});
});

function makeToolUse(toolUseId: string) {
	return {
		toolUseId,
		name: "Bash",
		input: { command: "pwd" },
	};
}

function makeToolResult(toolUseId: string): KiroToolResult {
	return {
		toolUseId,
		content: [{ text: `result for ${toolUseId}` }],
		status: "success",
		isError: false,
	};
}

function makeKiroRequest(
	history: NonNullable<KiroChatRequest["conversationState"]["history"]>,
	current: { content?: string; toolResults?: KiroToolResult[] } = {},
): KiroChatRequest {
	return {
		conversationState: {
			conversationId: "conv-test",
			chatTriggerType: "MANUAL",
			history,
			currentMessage: {
				userInputMessage: {
					content: current.content ?? "continue",
					modelId: "claude-sonnet-4.6",
					origin: "AI_EDITOR",
					userInputMessageContext: {
						tools: [
							{
								toolSpecification: {
									name: "Bash",
									description: "Run a command",
									inputSchema: { json: { type: "object" } },
								},
							},
						],
						...(current.toolResults ? { toolResults: current.toolResults } : {}),
					},
				},
			},
		},
	};
}

describe("normalizeKiroChatRequest", () => {
	test("merges consecutive assistant messages before matching tool results", () => {
		const request = makeKiroRequest(
			[
				{
					userInputMessage: {
						content: "start",
						modelId: "claude-sonnet-4.6",
						userInputMessageContext: {},
					},
				},
				{ assistantResponseMessage: { content: "Preparing report." } },
				{ assistantResponseMessage: { content: "", toolUses: [makeToolUse("tool_merge")] } },
				{
					userInputMessage: {
						content: "",
						modelId: "claude-sonnet-4.6",
						userInputMessageContext: { toolResults: [makeToolResult("tool_merge")] },
					},
				},
			],
			{ content: "next user prompt" },
		);

		normalizeKiroChatRequest(request);
		const history = request.conversationState.history ?? [];
		const mergedAssistant = history[1];

		expect(JSON.stringify(mergedAssistant)).toContain("Preparing report.");
		expect(JSON.stringify(mergedAssistant)).toContain("tool_merge");
		expect(JSON.stringify(history[2])).toContain("tool_merge");
		expect(JSON.stringify(request.conversationState.currentMessage)).not.toContain("tool_merge");
	});

	test("removes duplicate consumed tool results from currentMessage", () => {
		const request = makeKiroRequest(
			[
				{
					userInputMessage: {
						content: "start",
						modelId: "claude-sonnet-4.6",
						userInputMessageContext: {},
					},
				},
				{ assistantResponseMessage: { content: "", toolUses: [makeToolUse("tool_dup")] } },
				{
					userInputMessage: {
						content: "",
						modelId: "claude-sonnet-4.6",
						userInputMessageContext: { toolResults: [makeToolResult("tool_dup")] },
					},
				},
				{ assistantResponseMessage: { content: "" } },
			],
			{
				content: "[system] recover from truncated write",
				toolResults: [makeToolResult("tool_dup")],
			},
		);

		normalizeKiroChatRequest(request);
		const currentContext =
			request.conversationState.currentMessage.userInputMessage.userInputMessageContext;
		const requestJson = JSON.stringify(request);

		expect(currentContext?.toolResults).toBeUndefined();
		expect(currentContext?.tools?.length).toBe(1);
		expect((requestJson.match(/"toolUseId":"tool_dup"/g) ?? []).length).toBe(2);
	});

	test("drops orphaned tool results and removes unmatched assistant tool uses", () => {
		const request = makeKiroRequest(
			[
				{
					userInputMessage: {
						content: "start",
						modelId: "claude-sonnet-4.6",
						userInputMessageContext: {},
					},
				},
				{
					assistantResponseMessage: {
						content: "I will call a tool.",
						toolUses: [makeToolUse("tool_orphan_use")],
					},
				},
			],
			{
				content: "new prompt after pruning",
				toolResults: [makeToolResult("tool_orphan_result")],
			},
		);

		normalizeKiroChatRequest(request);
		const requestJson = JSON.stringify(request);

		expect(requestJson).toContain("I will call a tool.");
		expect(requestJson).not.toContain("tool_orphan_use");
		expect(requestJson).not.toContain("tool_orphan_result");
		expect(
			request.conversationState.currentMessage.userInputMessage.userInputMessageContext?.tools
				?.length,
		).toBe(1);
	});

	test("preserves a valid trailing tool-result replay in currentMessage", () => {
		const request = makeKiroRequest(
			[
				{
					userInputMessage: {
						content: "start",
						modelId: "claude-sonnet-4.6",
						userInputMessageContext: {},
					},
				},
				{ assistantResponseMessage: { content: "", toolUses: [makeToolUse("tool_replay")] } },
			],
			{ content: "", toolResults: [makeToolResult("tool_replay")] },
		);

		normalizeKiroChatRequest(request);
		const historyJson = JSON.stringify(request.conversationState.history ?? []);
		const currentContext =
			request.conversationState.currentMessage.userInputMessage.userInputMessageContext;

		expect(historyJson).toContain("tool_replay");
		expect(currentContext?.toolResults?.[0]?.toolUseId).toBe("tool_replay");
		expect(currentContext?.tools?.length).toBe(1);
	});
});

describe("parseSSEStream", () => {
	test("parses generic gateway queue events in Kiro-compatible streams", async () => {
		const events: ParsedStreamEvent[] = [];
		const body = streamFromText(
			"event: queueEvent\n" +
				'data: {"position":3,"queueDepth":8}\n\n' +
				'data: {"content":"ready"}\n\n',
		);

		for await (const event of parseSSEStream(body)) {
			events.push(event);
		}

		expect(events).toEqual([{ queueStatus: { position: 3, queueDepth: 8 } }, { text: "ready" }]);
	});

	// A half-open gateway connection delivers no bytes and no FIN. Without a guarded
	// read the generator hangs forever, leaving the in-flight tool call stuck at
	// "initializing" — its own timeout never arms because the tool never executes.
	test("raises StreamStaleError when the gateway stops sending", async () => {
		const body = new ReadableStream<Uint8Array>({
			start(controller) {
				controller.enqueue(new TextEncoder().encode('data: {"content":"partial"}\n\n'));
				// Never closes: emulates a stalled upstream.
			},
		});

		const events: ParsedStreamEvent[] = [];
		let caught: unknown;
		try {
			for await (const event of parseSSEStream(body, { staleTimeoutMs: 50 })) {
				events.push(event);
			}
		} catch (err) {
			caught = err;
		}

		expect(events).toEqual([{ text: "partial" }]);
		expect(caught).toBeInstanceOf(StreamStaleError);
		expect(isRetryableError(caught)).toBe(true);
	});
});
