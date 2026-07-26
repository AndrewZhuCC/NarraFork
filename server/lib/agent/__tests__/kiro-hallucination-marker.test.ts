import { describe, expect, test } from "bun:test";
import {
	KiroHallucinationMarkerFilter,
	KiroStreamToolCallAccumulator,
	shouldFilterHallucinatedMarkers,
} from "../kiro-protocol";
import type { ParsedStreamEvent } from "../provider";

/** Feed deltas through the raw filter and collect emitted text. */
function runFilter(deltas: string[]): string {
	const filter = new KiroHallucinationMarkerFilter();
	let out = "";
	for (const delta of deltas) out += filter.push(delta);
	out += filter.flush();
	return out;
}

/** Feed deltas through the stream accumulator for a given model and collect emitted text. */
function runAccumulator(model: string | undefined, deltas: string[]): string {
	const acc = new KiroStreamToolCallAccumulator({ parseTextToolCalls: true, model });
	let out = "";
	for (const delta of deltas) {
		const parsed: ParsedStreamEvent = { text: delta };
		out += acc.process(parsed).text ?? "";
	}
	const trailing = acc.finalize();
	if (trailing?.text) out += trailing.text;
	return out;
}

describe("shouldFilterHallucinatedMarkers", () => {
	test("matches opus-4.8 model variants", () => {
		expect(shouldFilterHallucinatedMarkers("nug2:kiro:claude-opus-4.8")).toBe(true);
		expect(shouldFilterHallucinatedMarkers("kiro:claude-opus-4-8")).toBe(true);
		expect(shouldFilterHallucinatedMarkers("claude-opus-4.8")).toBe(true);
	});

	test("matches opus-5 model variants", () => {
		expect(shouldFilterHallucinatedMarkers("nug2:kiro:claude-opus-5")).toBe(true);
		expect(shouldFilterHallucinatedMarkers("kiro:claude-opus-5")).toBe(true);
		expect(shouldFilterHallucinatedMarkers("claude-opus-5")).toBe(true);
		expect(shouldFilterHallucinatedMarkers("claude-opus-5.1-thinking")).toBe(true);
	});

	test("does not match other models", () => {
		expect(shouldFilterHallucinatedMarkers("claude-opus-4.6")).toBe(false);
		expect(shouldFilterHallucinatedMarkers("claude-sonnet-4.5")).toBe(false);
		expect(shouldFilterHallucinatedMarkers("claude-opus-4.1")).toBe(false);
		expect(shouldFilterHallucinatedMarkers("claude-opus-4-5")).toBe(false);
		expect(shouldFilterHallucinatedMarkers("claude-sonnet-5")).toBe(false);
		// A longer version number must not be swallowed by the `opus-5` branch.
		expect(shouldFilterHallucinatedMarkers("claude-opus-50")).toBe(false);
		expect(shouldFilterHallucinatedMarkers("claude-opus-51-thinking")).toBe(false);
		expect(shouldFilterHallucinatedMarkers("gpt-5.6-terra")).toBe(false);
		expect(shouldFilterHallucinatedMarkers(undefined)).toBe(false);
	});
});

describe("KiroHallucinationMarkerFilter", () => {
	test("removes a bare marker line (marker followed by newline)", () => {
		expect(runFilter(["文本\ncourse\ncourse\n更多"])).toBe("文本\n\n\n更多");
	});

	test("removes a marker that prefixes real content on the line", () => {
		expect(runFilter(["\ncourse Look at PointCloudViewer edl"])).toBe(
			"\nLook at PointCloudViewer edl",
		);
	});

	test("removes the Chinese 课 marker", () => {
		expect(runFilter(["行1\n课 邻居数组顺序"])).toBe("行1\n邻居数组顺序");
	});

	test("removes card and call markers", () => {
		expect(runFilter(["a\ncard\nb\ncall next"])).toBe("a\n\nb\nnext");
	});

	test("removes repeated bare 'count' marker lines", () => {
		expect(runFilter(["正文\ncount\ncount\ncount\ncount\n结尾"])).toBe("正文\n\n\n\n\n结尾");
	});

	test("removes 'count' before any following content", () => {
		expect(runFilter(["行1\ncount 邻居数组顺序"])).toBe("行1\n邻居数组顺序");
		expect(runFilter(["行1\ncount还有两处"])).toBe("行1\n还有两处");
		expect(runFilter(["行1\ncount，继续"])).toBe("行1\n，继续");
		expect(runFilter(["行1\ncount. 验证"])).toBe("行1\n验证");
		expect(runFilter(["a\ncount Look at edl"])).toBe("a\nLook at edl");
	});

	test("keeps a longer word that merely starts with 'count'", () => {
		expect(runFilter(["a\ncounts are fine"])).toBe("a\ncounts are fine");
		expect(runFilter(["a\ncounter += 1"])).toBe("a\ncounter += 1");
	});

	test("handles a 'count' marker split across deltas", () => {
		expect(runFilter(["a\ncou", "nt 邻居"])).toBe("a\n邻居");
		expect(runFilter(["a\ncount", "还有"])).toBe("a\n还有");
		expect(runFilter(["a\ncoun", "ts are fine"])).toBe("a\ncounts are fine");
	});

	test("preserves leading indentation", () => {
		expect(runFilter(["a\n    course foo"])).toBe("a\n    foo");
	});

	test("keeps 'course' when it is mid-sentence, not at a line head", () => {
		expect(runFilter(["Of course this is fine."])).toBe("Of course this is fine.");
	});

	test("keeps a longer word that merely starts with a marker", () => {
		expect(runFilter(["\ncoursework is due"])).toBe("\ncoursework is due");
		expect(runFilter(["\ncalled the function"])).toBe("\ncalled the function");
	});

	test("handles a marker split across two deltas", () => {
		expect(runFilter(["前文\nco", "urse Look here"])).toBe("前文\nLook here");
	});

	test("handles a marker split right after the newline", () => {
		expect(runFilter(["前文\n", "course\n", "next"])).toBe("前文\n\nnext");
	});

	test("handles the newline itself split from the marker", () => {
		expect(runFilter(["前文", "\ncourse foo"])).toBe("前文\nfoo");
	});

	test("does not drop trailing partial that turns out to be real text", () => {
		expect(runFilter(["a\ncou", "pon codes"])).toBe("a\ncoupon codes");
		expect(runFilter(["a\ncar", "go build"])).toBe("a\ncargo build");
	});

	test("leaves text with no markers untouched", () => {
		const text = "line one\nline two\n\nline three with course inside";
		expect(runFilter([text])).toBe(text);
	});

	test("handles CRLF line endings", () => {
		expect(runFilter(["a\r\ncourse foo"])).toBe("a\r\nfoo");
	});

	test("removes a Latin marker glued directly onto Chinese content (no space)", () => {
		expect(runFilter(["行1\ncourse还有两处 mock.module 的路径也要改。"])).toBe(
			"行1\n还有两处 mock.module 的路径也要改。",
		);
		expect(runFilter(["行1\ncourse用 replace_all。"])).toBe("行1\n用 replace_all。");
		expect(runFilter(["行1\ncourse执行。"])).toBe("行1\n执行。");
	});

	test("removes a Latin marker followed by Chinese punctuation", () => {
		expect(runFilter(["行1\ncall，继续"])).toBe("行1\n，继续");
	});

	test("removes a Latin marker followed by an ASCII period and space", () => {
		expect(runFilter(["行1\ncourse. 验证审查目录里现在四份产物"])).toBe(
			"行1\n验证审查目录里现在四份产物",
		);
		expect(runFilter(["行1\ncourse. run."])).toBe("行1\nrun.");
		expect(runFilter(["a\ncourse.\nb"])).toBe("a\n\nb");
	});

	test("removes a Latin marker with trailing period split across deltas", () => {
		expect(runFilter(["行1\ncourse.", " 验证审查"])).toBe("行1\n验证审查");
		expect(runFilter(["行1\ncourse", ". 验证审查"])).toBe("行1\n验证审查");
		expect(runFilter(["行1\ncour", "se. run."])).toBe("行1\nrun.");
	});

	test("removes a Latin marker glued to Chinese split across deltas", () => {
		expect(runFilter(["行1\ncourse", "还有两处"])).toBe("行1\n还有两处");
		expect(runFilter(["行1\ncou", "rse用 replace_all"])).toBe("行1\n用 replace_all");
	});

	test("does not touch the CJK marker 课 when part of a real Chinese word", () => {
		expect(runFilter(["行1\n课件：mock.module 出现2次。"])).toBe(
			"行1\n课件：mock.module 出现2次。",
		);
		expect(runFilter(["行1\n课程安排"])).toBe("行1\n课程安排");
	});

	test("still removes the bare CJK marker 课 at a line head", () => {
		expect(runFilter(["行1\n课 邻居数组"])).toBe("行1\n邻居数组");
		expect(runFilter(["行1\n课\n下一行"])).toBe("行1\n\n下一行");
	});

	test("keeps a longer Latin word even when followed by Chinese", () => {
		expect(runFilter(["\ncoursework是作业"])).toBe("\ncoursework是作业");
	});

	test("never strips the very first line of the stream", () => {
		expect(runFilter(["count = 0\nnext"])).toBe("count = 0\nnext");
		expect(runFilter(["course foo"])).toBe("course foo");
	});
});

describe("KiroHallucinationMarkerFilter code fence exemption", () => {
	test("leaves marker-looking code inside a fenced block untouched", () => {
		const text = ["前文", "```ts", "count = 0;", "call(foo);", "course();", "```", "后文"].join(
			"\n",
		);
		expect(runFilter([text])).toBe(text);
	});

	test("resumes filtering after the fence closes", () => {
		const input = "前文\n```ts\ncount = 0;\n```\ncount 邻居数组\n";
		expect(runFilter([input])).toBe("前文\n```ts\ncount = 0;\n```\n邻居数组\n");
	});

	test("tracks fence state across streaming deltas", () => {
		const out = runFilter([
			"前文\n``",
			"`py\n",
			"count",
			" = 0\n",
			"call(x)\n",
			"```\n",
			"count\n",
		]);
		expect(out).toBe("前文\n```py\ncount = 0\ncall(x)\n```\n\n");
	});

	test("supports tilde fences and indented fences", () => {
		const text = ["前文", "~~~", "count = 0", "~~~", "  ```", "  call(x)", "  ```"].join("\n");
		expect(runFilter([text])).toBe(text);
	});

	test("a different fence char inside a block does not close it", () => {
		const text = ["前文", "```md", "~~~", "count = 0", "~~~", "```"].join("\n");
		expect(runFilter([text])).toBe(text);
	});

	test("a shorter backtick run does not close a longer fence", () => {
		const text = ["前文", "````", "```", "count = 0", "````"].join("\n");
		expect(runFilter([text])).toBe(text);
	});

	test("a delimiter with an info string does not close a fence", () => {
		const text = ["前文", "```ts", "count = 0", "```ts", "call(x)", "```"].join("\n");
		expect(runFilter([text])).toBe(text);
	});

	test("an unclosed fence protects the rest of the stream", () => {
		const text = "前文\n```ts\ncount = 0;\ncall(foo);";
		expect(runFilter([text])).toBe(text);
	});

	test("inline backticks do not start a fence", () => {
		expect(runFilter(["前文\n用 `count` 计数\ncount 邻居"])).toBe("前文\n用 `count` 计数\n邻居");
	});

	test("mid-line inline code does not stall the stream", () => {
		const filter = new KiroHallucinationMarkerFilter();
		expect(filter.push("前文\n使用 ")).toBe("前文\n使用 ");
		expect(filter.push("`x`")).toBe("`x`");
		expect(filter.flush()).toBe("");
	});
});

describe("KiroStreamToolCallAccumulator marker filtering", () => {
	test("strips markers for opus-4.8 while streaming", () => {
		const out = runAccumulator("nug2:kiro:claude-opus-4.8", [
			"现在 PointCloudViewer\n",
			"course\n",
			"course Look at edl",
		]);
		expect(out).toBe("现在 PointCloudViewer\n\nLook at edl");
	});

	test("strips repeated 'count' markers for opus-5 while streaming", () => {
		const out = runAccumulator("nug2:kiro:claude-opus-5", [
			"正在检查\n",
			"count\n",
			"count\n",
			"count 邻居数组顺序\n",
			"count Look at edl\n",
			"count",
		]);
		expect(out).toBe("正在检查\n\n\n邻居数组顺序\nLook at edl\n");
	});

	test("leaves text untouched for non-matching models", () => {
		const deltas = ["现在 PointCloudViewer\n", "course\n", "course Look at edl"];
		const out = runAccumulator("nug2:kiro:claude-opus-4.6", deltas);
		expect(out).toBe("现在 PointCloudViewer\ncourse\ncourse Look at edl");
		expect(runAccumulator("nug2:kiro:claude-sonnet-5", ["a\n", "count\n", "b"])).toBe(
			"a\ncount\nb",
		);
	});

	test("still lifts leaked tool calls after marker filtering", () => {
		const acc = new KiroStreamToolCallAccumulator({
			parseTextToolCalls: true,
			model: "claude-opus-4.8",
		});
		let text = "";
		const tools: string[] = [];
		const feed = (delta: string) => {
			const parsed = acc.process({ text: delta });
			text += parsed.text ?? "";
			for (const tu of parsed.toolUses ?? []) tools.push(tu.name);
		};
		feed("正文\ncourse\n\ncall\n");
		feed('<invoke name="Read"><parameter name="file_path">/tmp/x</parameter></invoke>');
		const trailing = acc.finalize();
		if (trailing?.text) text += trailing.text;
		for (const tu of trailing?.toolUses ?? []) tools.push(tu.name);
		expect(tools).toEqual(["Read"]);
		expect(text.includes("course")).toBe(false);
	});
});
