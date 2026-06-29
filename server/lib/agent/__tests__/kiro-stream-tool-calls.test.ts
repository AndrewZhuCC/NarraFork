import { describe, expect, test } from "bun:test";
import { KiroTextToolCallStreamParser, parseSSEStream } from "../kiro-protocol";
import type { ParsedStreamEvent } from "../provider";

/** Build an SSE body where each entry becomes one `data:` frame. */
function sseStreamFromFrames(frames: object[]): ReadableStream<Uint8Array> {
	const body = frames.map((f) => `data: ${JSON.stringify(f)}\n\n`).join("");
	return new ReadableStream({
		start(controller) {
			controller.enqueue(new TextEncoder().encode(body));
			controller.close();
		},
	});
}

/** Feed each delta through the streaming parser and collect emitted text + tool uses. */
function runParser(deltas: string[]): {
	text: string;
	tools: Array<{ name: string; input: Record<string, unknown> }>;
} {
	const parser = new KiroTextToolCallStreamParser();
	let text = "";
	const tools: Array<{ name: string; input: Record<string, unknown> }> = [];
	for (const delta of deltas) {
		const { text: t, toolUses } = parser.push(delta);
		text += t;
		for (const tu of toolUses) tools.push({ name: tu.name, input: tu.input });
	}
	const { text: t, toolUses } = parser.flush();
	text += t;
	for (const tu of toolUses) tools.push({ name: tu.name, input: tu.input });
	return { text, tools };
}

describe("KiroTextToolCallStreamParser", () => {
	test("parses an invoke block split across many deltas", () => {
		const { text, tools } = runParser([
			"计划已批准。",
			"让我先用 todo 列表跟踪进度。\n\n",
			"call\n",
			'<invoke name="Task',
			'Create">',
			'<parameter name="todos">[{"id":"t0","content":"x"}]',
			"</parameter>",
			"</invoke>",
		]);

		expect(text).toBe("计划已批准。让我先用 todo 列表跟踪进度。\n\n");
		expect(text).not.toContain("<invoke");
		expect(text).not.toContain("call");
		expect(tools).toHaveLength(1);
		expect(tools[0].name).toBe("TaskCreate");
		expect(tools[0].input).toEqual({ todos: [{ id: "t0", content: "x" }] });
	});

	test("parses a block delivered as a single delta", () => {
		const { text, tools } = runParser([
			'<invoke name="Bash"><parameter name="command">pwd</parameter></invoke>',
		]);
		expect(text).toBe("");
		expect(tools).toEqual([{ name: "Bash", input: { command: "pwd" } }]);
	});

	test("parses two consecutive invoke blocks in one stream", () => {
		const { text, tools } = runParser([
			"先做两件事\n\n",
			'<invoke name="Bash"><parameter name="command">ls</parameter></invoke>',
			'<invoke name="Bash"><parameter name="command">pwd</parameter></invoke>',
		]);
		expect(text).toBe("先做两件事\n\n");
		expect(tools).toEqual([
			{ name: "Bash", input: { command: "ls" } },
			{ name: "Bash", input: { command: "pwd" } },
		]);
	});

	test("parses three complete invoke blocks contained in a single delta", () => {
		const { text, tools } = runParser([
			"前言\n\n" +
				'<invoke name="A"><parameter name="x">1</parameter></invoke>' +
				'<invoke name="B"><parameter name="x">2</parameter></invoke>' +
				'<invoke name="C"><parameter name="x">3</parameter></invoke>',
		]);
		expect(text).toBe("前言\n\n");
		expect(tools).toEqual([
			{ name: "A", input: { x: "1" } },
			{ name: "B", input: { x: "2" } },
			{ name: "C", input: { x: "3" } },
		]);
	});

	test("parses multiple blocks interleaved with text in one delta", () => {
		const { text, tools } = runParser([
			'<invoke name="A"><parameter name="x">1</parameter></invoke>' +
				"中间文字" +
				'<invoke name="B"><parameter name="x">2</parameter></invoke>' +
				"结尾",
		]);
		expect(text).toBe("中间文字结尾");
		expect(tools).toEqual([
			{ name: "A", input: { x: "1" } },
			{ name: "B", input: { x: "2" } },
		]);
	});

	test("parses multiple blocks that are each split across deltas", () => {
		const { text, tools } = runParser([
			'<invoke name="Ta',
			'sk"><parameter name="x">1</parameter></inv',
			"oke>",
			"文字",
			'<invoke name="Bash"><parameter name="cmd',
			'">ls</parameter></invoke>',
		]);
		expect(text).toBe("文字");
		expect(tools).toEqual([
			{ name: "Task", input: { x: "1" } },
			{ name: "Bash", input: { cmd: "ls" } },
		]);
	});

	test("emits a still-open second block verbatim on flush while keeping the first", () => {
		const { text, tools } = runParser([
			'<invoke name="A"><parameter name="x">1</parameter></invoke>',
			'<invoke name="B"><parameter name="x">2',
		]);
		expect(tools).toEqual([{ name: "A", input: { x: "1" } }]);
		expect(text).toBe('<invoke name="B"><parameter name="x">2');
	});

	test("does not hold back ordinary text", () => {
		const { text, tools } = runParser(["你好，", "这是普通回复，", "没有工具调用。"]);
		expect(text).toBe("你好，这是普通回复，没有工具调用。");
		expect(tools).toHaveLength(0);
	});

	test("retains a partial <invoke prefix until it is disambiguated", () => {
		const parser = new KiroTextToolCallStreamParser();
		// A trailing partial "<inv" could grow into "<invoke" — hold it back.
		const first = parser.push("做点事 <inv");
		expect(first.text).toBe("做点事 ");
		// Once it turns out not to be an invoke, the held text is released.
		const second = parser.push("alid> 继续");
		expect(first.text + second.text).toBe("做点事 <invalid> 继续");
		const flushed = parser.flush();
		expect(flushed.text).toBe("");
	});

	test("does not hold back a mid-string < that cannot be an invoke", () => {
		const { text, tools } = runParser(["a < b ", "c"]);
		expect(text).toBe("a < b c");
		expect(tools).toHaveLength(0);
	});

	test("emits an unterminated invoke block verbatim on flush", () => {
		const { text, tools } = runParser([
			"准备\n\n",
			'<invoke name="Bash"><parameter name="command">sleep',
		]);
		expect(tools).toHaveLength(0);
		expect(text).toBe('准备\n\n<invoke name="Bash"><parameter name="command">sleep');
	});

	test("decodes XML entities and coerces typed parameters", () => {
		const { tools } = runParser([
			'<invoke name="Bash">',
			'<parameter name="command">echo &amp; done</parameter>',
			'<parameter name="timeout">1000</parameter>',
			'<parameter name="run_in_background">true</parameter>',
			"</invoke>",
		]);
		expect(tools[0].input).toEqual({
			command: "echo & done",
			timeout: 1000,
			run_in_background: true,
		});
	});

	test("coerces the reflection-gate `confirm` parameter to a boolean", () => {
		// Reproduces a leaked DangerConfirm tool call (Kiro/NUG/NKP). The `confirm`
		// parameter must coerce to boolean true so z.literal(true) schema validation
		// passes; otherwise the danger gate silently falls back to cancelling.
		const { tools } = runParser([
			"call\n",
			'<invoke name="DangerConfirm">',
			'<parameter name="confirm">true</parameter>',
			'<parameter name="reflection">cleaning up my own temp log file</parameter>',
			"</invoke>",
		]);
		expect(tools).toHaveLength(1);
		expect(tools[0].name).toBe("DangerConfirm");
		expect(tools[0].input).toEqual({
			confirm: true,
			reflection: "cleaning up my own temp log file",
		});
	});
});

describe("parseSSEStream — leaked tool calls split across frames", () => {
	test("lifts a split invoke block out of streamed text", async () => {
		const body = sseStreamFromFrames([
			{ content: "计划已批准。\n\n" },
			{ content: "call\n" },
			{ content: '<invoke name="TaskCreate">' },
			{ content: '<parameter name="todos">[{"id":"t0"}]' },
			{ content: "</parameter></invoke>" },
		]);

		const events: ParsedStreamEvent[] = [];
		for await (const evt of parseSSEStream(body, { parseTextToolCalls: true })) {
			events.push(evt);
		}

		const text = events.map((e) => e.text ?? "").join("");
		const toolUses = events.flatMap((e) => e.toolUses ?? []);
		expect(text).toBe("计划已批准。\n\n");
		expect(text).not.toContain("<invoke");
		expect(text).not.toContain("call");
		expect(toolUses).toHaveLength(1);
		expect(toolUses[0].name).toBe("TaskCreate");
		expect(toolUses[0].input).toEqual({ todos: [{ id: "t0" }] });
	});

	test("keeps text verbatim when parseTextToolCalls is disabled", async () => {
		const body = sseStreamFromFrames([
			{ content: '<invoke name="Bash">' },
			{ content: '<parameter name="command">pwd</parameter></invoke>' },
		]);

		const events: ParsedStreamEvent[] = [];
		for await (const evt of parseSSEStream(body)) {
			events.push(evt);
		}
		const text = events.map((e) => e.text ?? "").join("");
		const toolUses = events.flatMap((e) => e.toolUses ?? []);
		expect(text).toBe('<invoke name="Bash"><parameter name="command">pwd</parameter></invoke>');
		expect(toolUses).toHaveLength(0);
	});
});
