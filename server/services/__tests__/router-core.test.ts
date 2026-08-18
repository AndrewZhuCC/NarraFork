/**
 * pi-router core 纯函数测试(移植自 pi-deepseek-route verify.mjs 断言思路)。
 */
import { describe, expect, test } from "bun:test";
import {
	bandFor,
	bandOf,
	clamp01,
	classifyTask,
	coreFor,
	GUIDE_BASE,
	GUIDE_BOOST,
	GUIDE_COMMIT,
	GUIDE_DEEP,
	guideFor,
	isChatTask,
	isComplexTask,
	isFlashModel,
	MODE_MIXED,
	MODE_REACT,
	MODE_SPEC,
	MODE_WEAK,
	messageText,
	parseMode,
	personaFor,
	testinessFor,
} from "../router/core";

describe("classifyTask", () => {
	test("build keywords → react (1)", () => {
		expect(classifyTask("写一个计算器应用")).toBe(1);
		expect(classifyTask("帮我开发一个网页")).toBe(1);
		expect(classifyTask("create a new project")).toBe(1);
	});
	test("fix keywords → spec (0)", () => {
		expect(classifyTask("修复这个bug")).toBe(0);
		expect(classifyTask("帮我排查一下报错")).toBe(0);
		expect(classifyTask("refactor this module")).toBe(0);
	});
	test("ambiguous / unmatched → weak", () => {
		expect(classifyTask("你好")).toBe("weak");
		expect(classifyTask("请解释一下量子计算")).toBe("weak");
		expect(classifyTask("随便聊聊")).toBe("weak");
	});
	test("more react hits than spec hits wins", () => {
		// 两个 build 关键词 vs 一个 fix 关键词 → react
		expect(classifyTask("写一个应用并修复它的bug")).toBe(1);
	});
});

describe("isComplexTask", () => {
	test("long text (>120 chars) is complex", () => {
		const long =
			"请帮我做一个功能完整的管理系统，包含用户管理、权限管理、日志管理、配置管理，".repeat(5);
		expect(long.length).toBeGreaterThan(120);
		expect(isComplexTask(long)).toBe(true);
	});
	test("architectural keywords are complex", () => {
		expect(isComplexTask("详细设计系统架构")).toBe(true);
		expect(isComplexTask("分析一下整体架构")).toBe(true);
		expect(isComplexTask("refactor the architecture")).toBe(true);
	});
	test("simple short task is not complex", () => {
		expect(isComplexTask("修复这个bug")).toBe(false);
		expect(isComplexTask("写一个脚本")).toBe(false);
	});
});

describe("isChatTask", () => {
	test("greetings → chat", () => {
		expect(isChatTask("你好")).toBe(true);
		expect(isChatTask("hello")).toBe(true);
		expect(isChatTask("在吗？")).toBe(true);
		expect(isChatTask("thanks!")).toBe(true);
	});
	test("empty / non-string → chat", () => {
		expect(isChatTask("")).toBe(true);
		expect(isChatTask("   ")).toBe(true);
		expect(isChatTask(null as unknown as string)).toBe(true);
	});
	test("short text without task keywords → chat", () => {
		expect(isChatTask("好的")).toBe(true);
		expect(isChatTask("继续")).toBe(true);
	});
	test("task text → not chat", () => {
		expect(isChatTask("写一个计算器应用")).toBe(false);
		expect(isChatTask("修复这个bug")).toBe(false);
	});
});

describe("bandOf / bandFor", () => {
	test("stable regions", () => {
		expect(bandOf(0)).toBe("spec");
		expect(bandOf(0.1)).toBe("spec");
		expect(bandOf(1)).toBe("react");
		expect(bandOf(0.8)).toBe("react");
		expect(bandOf("weak")).toBe("weak");
	});
	test("transition band", () => {
		expect(bandOf(0.3)).toBe("transition");
		expect(bandOf(null)).toBe("spec"); // clamp01(null)=0
	});
	test("bandFor human names", () => {
		expect(bandFor(0)).toBe("spec");
		expect(bandFor(MODE_MIXED)).toBe("mixed");
		expect(bandFor(MODE_REACT)).toBe("react");
		expect(bandFor(MODE_WEAK)).toBe("weak");
	});
});

describe("personaFor", () => {
	test("spec / mixed / react / weak personas", () => {
		expect(personaFor(MODE_SPEC)).toContain("helpful software engineer assistant");
		expect(personaFor(MODE_MIXED)).toContain("Work directly");
		expect(personaFor(MODE_REACT)).toContain("hands-on");
		// weak 带按模型分流:无 modelId / 非 flash → WEAK_PRO(spec 句,无 anchors)
		expect(personaFor(MODE_WEAK)).toContain("helpful software engineer assistant");
		expect(personaFor(MODE_WEAK)).not.toContain("Think deeply first");
		expect(personaFor(MODE_WEAK, "deepseek-v4-pro")).toContain(
			"helpful software engineer assistant",
		);
		// flash 模型 → WEAK_FLASH(neutral + anchors + deep-first)
		expect(personaFor(MODE_WEAK, "deepseek-v4-flash")).toContain("helpful assistant");
		expect(personaFor(MODE_WEAK, "deepseek-v4-flash")).toContain("Think deeply first");
		expect(personaFor(MODE_WEAK, "deepseek-v4-flash-free")).toContain("Think deeply first");
	});
});

describe("guideFor", () => {
	test("round 1-2 → GUIDE_BASE; rounds 3+ → GUIDE_BOOST", () => {
		expect(guideFor(1, "修复这个bug")).toContain(GUIDE_BASE);
		expect(guideFor(2, "修复这个bug")).toContain(GUIDE_BASE);
		expect(guideFor(3, "修复这个bug")).toContain(GUIDE_BOOST);
		expect(guideFor(5, "修复这个bug")).toContain(GUIDE_BOOST);
	});
	test("simple task → GUIDE_COMMIT; complex → GUIDE_DEEP", () => {
		expect(guideFor(1, "修复这个bug")).toContain(GUIDE_COMMIT);
		expect(guideFor(1, "修复这个bug")).not.toContain(GUIDE_DEEP);
		expect(guideFor(1, "详细设计系统架构")).toContain(GUIDE_DEEP);
	});
});

describe("parseMode / clamp01", () => {
	test("band names", () => {
		expect(parseMode("spec")).toBe(0);
		expect(parseMode("react")).toBe(1);
		expect(parseMode("weak")).toBe("weak");
		expect(parseMode("router")).toBe("weak");
		expect(parseMode("auto")).toBe("auto");
		expect(parseMode("mixed")).toBe(0.3);
		expect(parseMode("balanced")).toBe(0.3);
	});
	test("numbers 0-100 and 0.0-1.0", () => {
		expect(parseMode("0")).toBe(0);
		expect(parseMode("50")).toBe(0.5);
		expect(parseMode("100")).toBe(1);
		expect(parseMode("0.7")).toBe(0.7);
		expect(parseMode("1.0")).toBe(1);
		expect(parseMode(50)).toBe(0.5);
	});
	test("invalid → null", () => {
		expect(parseMode("abc")).toBe(null);
		expect(parseMode(undefined)).toBe(null);
		expect(parseMode(null)).toBe(null);
		// Number("") === 0 → pi 版原样行为:空串解析为 0(spec)
		expect(parseMode("")).toBe(0);
	});
	test("clamp01 clamps", () => {
		expect(clamp01(1.5)).toBe(1);
		expect(clamp01(-1)).toBe(0);
		expect(clamp01(0.3)).toBe(0.3);
		expect(clamp01(undefined)).toBe(0);
	});
});

describe("isFlashModel", () => {
	test("flash family matches; others don't", () => {
		expect(isFlashModel("deepseek-v4-flash")).toBe(true);
		expect(isFlashModel("deepseek-flash")).toBe(true);
		expect(isFlashModel("Flash")).toBe(true);
		expect(isFlashModel("claude-sonnet-4")).toBe(false);
		expect(isFlashModel(undefined)).toBe(false);
		expect(isFlashModel(null)).toBe(false);
	});
});

describe("coreFor (NarraFork tool names)", () => {
	test("spec → read-first", () => {
		expect(coreFor(MODE_SPEC)).toEqual(["Read", "Edit", "Glob", "Grep"]);
	});
	test("mixed → everything core", () => {
		expect(coreFor(MODE_MIXED)).toEqual(["Read", "Write", "Edit", "Glob", "Grep"]);
	});
	test("weak → edit (shell added dynamically by extension)", () => {
		expect(coreFor(MODE_WEAK)).toEqual(["Edit"]);
	});
	test("react → write-first", () => {
		expect(coreFor(MODE_REACT)).toEqual(["Read", "Write", "Edit"]);
	});
});

describe("testinessFor", () => {
	test("react suppressed / spec normal / weak light", () => {
		expect(testinessFor(MODE_REACT)).toBe("suppressed");
		expect(testinessFor(MODE_SPEC)).toBe("normal");
		expect(testinessFor(MODE_WEAK)).toBe("light");
	});
});

describe("messageText", () => {
	test("string content passes through", () => {
		expect(messageText("hello")).toBe("hello");
	});
	test("content blocks are joined", () => {
		expect(
			messageText([
				{ type: "text", text: "a" },
				{ type: "text", text: "b" },
			]),
		).toBe("a b");
	});
	test("nullish / non-array → empty", () => {
		expect(messageText(null)).toBe("");
		expect(messageText(42)).toBe("");
	});
});
