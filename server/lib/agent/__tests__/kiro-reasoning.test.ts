import { describe, expect, test } from "bun:test";
import type {
	KiroHistoryAssistantMessage,
	KiroHistoryMessage,
	StreamEvent,
} from "../../kiro-adapter";
import { KiroProvider } from "../kiro-provider";
import {
	buildKiroHistory,
	buildKiroReasoningContent,
	parseKiroStreamEvent,
} from "../kiro-protocol";
import type { DbMessage } from "../provider";

function assistantOf(history: KiroHistoryMessage[]): KiroHistoryAssistantMessage {
	const msg = history.find(
		(m): m is KiroHistoryAssistantMessage => "assistantResponseMessage" in m,
	);
	if (!msg) throw new Error("no assistant message in history");
	return msg;
}

describe("kiro reasoning — receive side (parseKiroStreamEvent)", () => {
	test("parses reasoning text, signature, and redactedContent", () => {
		const evt: StreamEvent = {
			eventType: "reasoningContentEvent",
			data: { text: "thinking hard", signature: "sig_abc", redactedContent: "cmVkYWN0ZWQ=" },
		};
		const parsed = parseKiroStreamEvent(evt);
		expect(parsed.reasoning).toBe("thinking hard");
		expect(parsed.reasoningMetadata?.anthropic?.signature).toBe("sig_abc");
		expect(parsed.redactedThinking?.data).toBe("cmVkYWN0ZWQ=");
	});

	test("handles plain reasoning text without signature/redacted", () => {
		const parsed = parseKiroStreamEvent({
			eventType: "reasoningContentEvent",
			data: { content: "just text" },
		});
		expect(parsed.reasoning).toBe("just text");
		expect(parsed.reasoningMetadata).toBeUndefined();
		expect(parsed.redactedThinking).toBeUndefined();
	});
});

describe("kiro reasoning — buildKiroReasoningContent helper", () => {
	test("returns undefined when there is nothing to echo", () => {
		expect(buildKiroReasoningContent(undefined, undefined)).toBeUndefined();
		expect(buildKiroReasoningContent([], [])).toBeUndefined();
	});

	test("merges reasoning text and keeps the last signature", () => {
		const result = buildKiroReasoningContent(
			[
				{ text: "step 1", providerMetadata: { anthropic: { signature: "sig_1" } } },
				{ text: "step 2", providerMetadata: { anthropic: { signature: "sig_2" } } },
			],
			undefined,
		);
		expect(result).toEqual({ reasoningText: { text: "step 1\nstep 2", signature: "sig_2" } });
	});

	test("carries redactedContent alongside text", () => {
		const result = buildKiroReasoningContent(
			[{ text: "visible" }],
			[{ data: "ZW5j" }],
		);
		expect(result).toEqual({
			reasoningText: { text: "visible" },
			redactedContent: "ZW5j",
		});
	});

	test("supports redacted-only thinking with no visible text", () => {
		const result = buildKiroReasoningContent([], [{ data: "ZW5j" }]);
		expect(result).toEqual({ redactedContent: "ZW5j" });
	});
});

describe("kiro reasoning — pushAssistantTurn (KiroProvider)", () => {
	const provider = new KiroProvider();

	test("writes reasoning_content (snake_case) into the assistant message", () => {
		const history: KiroHistoryMessage[] = [];
		provider.pushAssistantTurn(
			history,
			"Here is the answer",
			[],
			[{ text: "let me think", providerMetadata: { anthropic: { signature: "sig_x" } } }],
			undefined,
			undefined,
			undefined,
			undefined,
			[{ data: "cmVk" }],
		);
		const assistant = assistantOf(history).assistantResponseMessage;
		expect(assistant.content).toBe("Here is the answer");
		expect((assistant as Record<string, unknown>).reasoning_content).toEqual({
			reasoningText: { text: "let me think", signature: "sig_x" },
			redactedContent: "cmVk",
		});
	});

	test("omits reasoning_content when no reasoning is present", () => {
		const history: KiroHistoryMessage[] = [];
		provider.pushAssistantTurn(history, "plain reply", []);
		const assistant = assistantOf(history).assistantResponseMessage;
		expect((assistant as Record<string, unknown>).reasoning_content).toBeUndefined();
	});
});

describe("kiro reasoning — buildKiroHistory (DB → history rebuild)", () => {
	test("rebuilds reasoning_content from stored reasoning/redacted_thinking blocks", async () => {
		const dbMessages: DbMessage[] = [
			{
				id: "user-1",
				role: "user",
				contentJson: [],
				contentText: "hi",
				parentToolUseId: null,
				messageUuid: null,
				toolCalls: [],
			},
			{
				id: "assistant-1",
				role: "assistant",
				contentJson: [
					{
						type: "reasoning",
						text: "deep thought",
						providerMetadata: { anthropic: { signature: "sig_db" } },
					},
					{ type: "redacted_thinking", data: "cmVkX2Ri" },
					{ type: "text", text: "final answer" },
				],
				contentText: null,
				parentToolUseId: null,
				messageUuid: null,
				toolCalls: [],
			},
		];

		const { history } = await buildKiroHistory(dbMessages, "claude-sonnet-4");
		const assistant = assistantOf(history as KiroHistoryMessage[]).assistantResponseMessage;
		expect(assistant.content).toBe("final answer");
		expect((assistant as Record<string, unknown>).reasoning_content).toEqual({
			reasoningText: { text: "deep thought", signature: "sig_db" },
			redactedContent: "cmVkX2Ri",
		});
	});

	test("leaves reasoning_content unset for assistant messages without reasoning", async () => {
		const dbMessages: DbMessage[] = [
			{
				id: "assistant-1",
				role: "assistant",
				contentJson: [{ type: "text", text: "no thinking here" }],
				contentText: null,
				parentToolUseId: null,
				messageUuid: null,
				toolCalls: [],
			},
		];
		const { history } = await buildKiroHistory(dbMessages, "claude-sonnet-4");
		const assistant = assistantOf(history as KiroHistoryMessage[]).assistantResponseMessage;
		expect((assistant as Record<string, unknown>).reasoning_content).toBeUndefined();
	});
});
