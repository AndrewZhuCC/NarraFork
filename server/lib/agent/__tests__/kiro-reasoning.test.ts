import { describe, expect, test } from "bun:test";
import type {
	KiroHistoryAssistantMessage,
	KiroHistoryMessage,
	StreamEvent,
} from "../../kiro-adapter";
import {
	buildKiroHistory,
	buildKiroReasoningContent,
	parseKiroStreamEvent,
} from "../kiro-protocol";
import { KiroProvider } from "../kiro-provider";
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

	test("parses leaked XML invoke blocks from assistant content when enabled", () => {
		const content = [
			"更新 packet staging 与 correction 初始化条目，反映坐标查表已闭合。",
			"",
			"count",
			'<invoke name="Edit">',
			'<parameter name="file_path">D:/GoSLAM Mapping Master Pro/docs/example.md</parameter>',
			'<parameter name="old_string">old value</parameter>',
			'<parameter name="new_string">`GeneralParser<LidarPointXYZIRT>` &amp; LUT</parameter>',
			"</invoke>",
		].join("\n");

		const parsed = parseKiroStreamEvent(
			{ eventType: "assistantResponseEvent", data: { content } },
			{ parseTextToolCalls: true },
		);

		expect(parsed.text).toBe(
			"更新 packet staging 与 correction 初始化条目，反映坐标查表已闭合。\n\n",
		);
		expect(parsed.text).not.toContain("<invoke");
		expect(parsed.text).not.toContain("count");
		expect(parsed.toolUses).toHaveLength(1);
		const toolUse = parsed.toolUses?.[0];
		expect(toolUse?.toolUseId.startsWith("kiro_xml_")).toBe(true);
		expect(toolUse?.name).toBe("Edit");
		expect(toolUse?.input).toEqual({
			file_path: "D:/GoSLAM Mapping Master Pro/docs/example.md",
			old_string: "old value",
			new_string: "`GeneralParser<LidarPointXYZIRT>` & LUT",
		});
	});

	test("removes short marker text after blank-line boundary before leaked invoke blocks", () => {
		const content = [
			"准备执行工具。",
			"",
			"call",
			'<invoke name="Bash">',
			'<parameter name="command">pwd</parameter>',
			"</invoke>",
		].join("\n");

		const parsed = parseKiroStreamEvent(
			{ eventType: "assistantResponseEvent", data: { content } },
			{ parseTextToolCalls: true },
		);

		expect(parsed.text).toBe("准备执行工具。\n\n");
		expect(parsed.text).not.toContain("call");
		expect(parsed.toolUses?.[0].name).toBe("Bash");
		expect(parsed.toolUses?.[0].input).toEqual({ command: "pwd" });
	});

	test("keeps non-blank-line short text before leaked invoke blocks", () => {
		const content = [
			"准备执行工具。",
			"ok",
			'<invoke name="Bash">',
			'<parameter name="command">pwd</parameter>',
			"</invoke>",
		].join("\n");

		const parsed = parseKiroStreamEvent(
			{ eventType: "assistantResponseEvent", data: { content } },
			{ parseTextToolCalls: true },
		);

		expect(parsed.text).toBe("准备执行工具。\nok\n");
		expect(parsed.toolUses?.[0].name).toBe("Bash");
	});

	test("keeps leaked XML invoke blocks as text for plain generation parsing", () => {
		const content = '<invoke name="Bash"><parameter name="command">pwd</parameter></invoke>';
		const parsed = parseKiroStreamEvent({ eventType: "assistantResponseEvent", data: { content } });
		expect(parsed.text).toBe(content);
		expect(parsed.toolUses).toBeUndefined();
	});

	test("strips leaked XML without duplicating native tool uses", () => {
		const content = '<invoke name="Bash"><parameter name="command">pwd</parameter></invoke>';
		const parsed = parseKiroStreamEvent(
			{
				eventType: "assistantResponseEvent",
				data: {
					content,
					toolUses: [{ toolUseId: "native_1", name: "Bash", input: { command: "pwd" } }],
				},
			},
			{ parseTextToolCalls: true },
		);
		expect(parsed.text).toBe("");
		expect(parsed.toolUses).toEqual([
			{ toolUseId: "native_1", name: "Bash", input: { command: "pwd" } },
		]);
	});

	test("keeps distinct leaked XML tool calls alongside native tool uses", () => {
		const content = [
			'<invoke name="Edit">',
			'<parameter name="file_path">/tmp/a.txt</parameter>',
			'<parameter name="old_string">a</parameter>',
			'<parameter name="new_string">b</parameter>',
			"</invoke>",
		].join("\n");
		const parsed = parseKiroStreamEvent(
			{
				eventType: "assistantResponseEvent",
				data: {
					content,
					toolUses: [{ toolUseId: "native_1", name: "Bash", input: { command: "pwd" } }],
				},
			},
			{ parseTextToolCalls: true },
		);

		expect(parsed.text).toBe("");
		expect(parsed.toolUses).toHaveLength(2);
		expect(parsed.toolUses?.[0]).toEqual({
			toolUseId: "native_1",
			name: "Bash",
			input: { command: "pwd" },
		});
		expect(parsed.toolUses?.[1].name).toBe("Edit");
		expect(parsed.toolUses?.[1].input).toEqual({
			file_path: "/tmp/a.txt",
			old_string: "a",
			new_string: "b",
		});
	});

	test("coerces common leaked XML parameter scalar types", () => {
		const content = [
			'<invoke name="Bash">',
			'<parameter name="command">pwd</parameter>',
			'<parameter name="timeout">1000</parameter>',
			'<parameter name="run_in_background">true</parameter>',
			"</invoke>",
		].join("\n");
		const parsed = parseKiroStreamEvent(
			{ eventType: "assistantResponseEvent", data: { content } },
			{ parseTextToolCalls: true },
		);

		expect(parsed.toolUses?.[0].input).toEqual({
			command: "pwd",
			timeout: 1000,
			run_in_background: true,
		});
	});

	test("keeps legitimate short prose before leaked invoke blocks", () => {
		const content = [
			"运行测试",
			'<invoke name="Bash">',
			'<parameter name="command">bun test</parameter>',
			"</invoke>",
		].join("\n");
		const parsed = parseKiroStreamEvent(
			{ eventType: "assistantResponseEvent", data: { content } },
			{ parseTextToolCalls: true },
		);

		expect(parsed.text).toBe("运行测试\n");
		expect(parsed.toolUses?.[0].name).toBe("Bash");
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
		const result = buildKiroReasoningContent([{ text: "visible" }], [{ data: "ZW5j" }]);
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
