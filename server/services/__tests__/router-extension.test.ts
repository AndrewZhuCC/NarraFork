/**
 * router extension 钩子测试:用最小 ctx 驱动 onSystemPrompt / onRequest。
 *
 * 覆盖:白名单外模型零干预、白名单+构建任务(react persona + 首轮收窄 + 恢复)、
 * weak 带近距离引导(去重)、寒暄让位、override 访问器、空白名单默认关闭。
 *
 * 门控读 agent.routerModels 配置白名单;本文件 mock 该 getter 模拟"已配置
 * /flash/i 白名单"的运行时,再单独验证空白名单零干预。mock.module 必须在
 * 被测模块首次加载前生效,故 extension/core 用动态 import。
 */
import { describe, expect, mock, test } from "bun:test";
import type {
	AgentLoopExtensionContext,
	AgentLoopPersonaResult,
	AgentLoopRequestResult,
} from "../../lib/agent/types";

// 模拟已配置白名单:匹配 /flash/i 的模型启用路由。其余 settings 接口原样走真实现。
mock.module("../../lib/settings", () => {
	const actual: Record<string, unknown> = { ...require("../../lib/settings") };
	return {
		...actual,
		getRouterModels: () => [{ pattern: "/flash/i" }],
	};
});

const { GUIDE_DEEP, MODE_REACT, MODE_WEAK } = await import("../router/core");
const { getRouterModeFor, getRouterSessionState, PERSONA_MARKER, routerLoopExtension } =
	await import("../router/extension");

interface TestCtx {
	narratorId: string;
	conversationId: string;
	model: string;
	provider: string;
	cwd: string;
	locale: string;
	userText: string;
	turnIndex: number;
	history: unknown[];
	tools: { name: string }[];
}

const TOOL_NAMES = ["Read", "Write", "Edit", "Glob", "Grep", "Bash", "WebSearch"];

// weak 任务文本:长度 >24(避开 isChatTask 的短文本让位),且不含 REACT/SPEC
// 关键词 → classifyTask 返回 "weak";含"分析" → isComplexTask 命中 GUIDE_DEEP。
const WEAK_TASK = "请帮我分析一下这个项目当前存在的问题，并给出具体的改进建议";

function makeCtx(overrides: Partial<TestCtx> = {}): TestCtx {
	return {
		narratorId: "n-test-1",
		conversationId: "c-test-1",
		model: "deepseek-v4-flash",
		provider: "kiro",
		cwd: "/tmp/work",
		locale: "zh-CN",
		userText: "写一个计算器应用",
		turnIndex: 0,
		history: [],
		tools: TOOL_NAMES.map((name) => ({ name })),
		...overrides,
	};
}

function sysPromptCtx(overrides: Partial<TestCtx> = {}): AgentLoopExtensionContext {
	return makeCtx(overrides) as unknown as AgentLoopExtensionContext;
}

function requestCtx(overrides: Partial<TestCtx> = {}): AgentLoopExtensionContext {
	return makeCtx(overrides) as unknown as AgentLoopExtensionContext;
}

/** 同步调用 onSystemPrompt 并归一化为 AgentLoopPersonaResult | null。 */
function runSystemPrompt(ctx: AgentLoopExtensionContext): AgentLoopPersonaResult | null {
	const result = routerLoopExtension.onSystemPrompt?.(ctx);
	if (result instanceof Promise) throw new Error("onSystemPrompt must be synchronous");
	return result ?? null;
}

/** 同步调用 onRequest 并归一化为 AgentLoopRequestResult | null。 */
function runRequest(ctx: AgentLoopExtensionContext): AgentLoopRequestResult | null {
	const result = routerLoopExtension.onRequest?.(ctx);
	if (result instanceof Promise) throw new Error("onRequest must be synchronous");
	return result ?? null;
}

describe("routerLoopExtension.onSystemPrompt", () => {
	test("not-whitelisted model → zero intervention", () => {
		const result = runSystemPrompt(
			sysPromptCtx({ narratorId: "n-nonflash", model: "claude-sonnet-4" }),
		);
		expect(result).toBeNull();
	});

	test("whitelisted + build task → react persona + first-turn core lease", () => {
		const id = "n-react-1";
		const result = runSystemPrompt(sysPromptCtx({ narratorId: id, userText: "写一个计算器应用" }));
		expect(result?.systemPromptSuffix).toContain(PERSONA_MARKER);
		expect(result?.systemPromptSuffix).toContain("hands-on"); // REACT_PERSONA
		const state = getRouterSessionState(id);
		expect(state.firstUserText).toBe("写一个计算器应用");
		expect(state.lease?.core).toEqual(["Read", "Write", "Edit", "Bash"]);
		expect(state.lease?.full).toEqual(TOOL_NAMES);
	});

	test("whitelisted + fix task → spec persona + read-first core lease", () => {
		const id = "n-spec-1";
		const result = runSystemPrompt(sysPromptCtx({ narratorId: id, userText: "修复这个bug" }));
		expect(result?.systemPromptSuffix).toContain("helpful software engineer assistant");
		expect(getRouterSessionState(id).lease?.core).toEqual(["Read", "Edit", "Glob", "Grep", "Bash"]);
	});

	test("whitelisted + weak task → weak persona + edit core lease", () => {
		const id = "n-weak-1";
		const result = runSystemPrompt(sysPromptCtx({ narratorId: id, userText: WEAK_TASK }));
		expect(result?.systemPromptSuffix).toContain("helpful assistant"); // WEAK_FLASH
		expect(getRouterSessionState(id).lease?.core).toEqual(["Edit", "Bash"]);
	});

	test("chat greeting → stand down without caching; routing resumes later", () => {
		const id = "n-chat-1";
		const result = runSystemPrompt(sysPromptCtx({ narratorId: id, userText: "你好" }));
		expect(result).toBeNull();
		expect(getRouterSessionState(id).firstUserText).toBeNull();
		// 下一条真实任务消息 → 恢复路由(同 narrator 新 loop)
		const next = runSystemPrompt(sysPromptCtx({ narratorId: id, userText: "写一个工具" }));
		expect(next?.systemPromptSuffix).toContain(PERSONA_MARKER);
	});
});

describe("routerLoopExtension.onRequest", () => {
	test("not-whitelisted model → zero intervention", () => {
		const result = runRequest(requestCtx({ narratorId: "n-nonflash-2", model: "claude-sonnet-4" }));
		expect(result).toBeNull();
	});

	test("first turn narrows tools; turnIndex>=1 restores full surface", () => {
		const id = "n-react-2";
		runSystemPrompt(sysPromptCtx({ narratorId: id, userText: "写一个计算器应用" }));
		const rq0 = runRequest(requestCtx({ narratorId: id, turnIndex: 0 }));
		expect(rq0?.allowTools).toEqual(new Set(["Read", "Write", "Edit", "Bash"]));
		// 首轮锚定与引导可同时返回:react 带无引导,appendMessages 应缺失
		expect(rq0?.appendMessages).toBeUndefined();
		// turnIndex>=1 = 已有工具调用 → 放行全量并清除租约(整体返回 null)
		const rq1 = runRequest(requestCtx({ narratorId: id, turnIndex: 1 }));
		expect(rq1).toBeNull();
		expect(getRouterSessionState(id).lease).toBeNull();
	});

	test("weak band → guide appended after first user message (deduped)", () => {
		const id = "n-weak-2";
		const text = WEAK_TASK;
		runSystemPrompt(sysPromptCtx({ narratorId: id, userText: text }));
		const rq0 = runRequest(
			requestCtx({
				narratorId: id,
				turnIndex: 0,
				history: [{ role: "user", content: [{ type: "text", text }] }],
			}),
		);
		expect(rq0?.allowTools).toEqual(new Set(["Edit", "Bash"]));
		const guide = rq0?.appendMessages?.[0] as { content: { text: string }[] };
		expect(guide.content[0].text).toContain("Router:");
		// "分析" 命中 COMPLEX_RE → 有向深度引导
		expect(guide.content[0].text).toContain(GUIDE_DEEP);
		// 同一条 user 消息不再注入(turn 1 同时恢复全量 → 整体返回 null)
		const rq1 = runRequest(
			requestCtx({
				narratorId: id,
				turnIndex: 1,
				history: [{ role: "user", content: [{ type: "text", text }] }],
			}),
		);
		expect(rq1).toBeNull();
	});

	test("weak band skips router/system noise messages", () => {
		const id = "n-weak-3";
		const text = WEAK_TASK;
		runSystemPrompt(sysPromptCtx({ narratorId: id, userText: text }));
		// 最后一条是引导消息本身(Router: 开头)→ 不再追加引导
		const rq = runRequest(
			requestCtx({
				narratorId: id,
				turnIndex: 1,
				history: [
					{ role: "user", content: [{ type: "text", text }] },
					{ role: "user", content: [{ type: "text", text: "\n\nRouter: classify this task" }] },
				],
			}),
		);
		expect(rq).toBeNull();
	});

	test("chat greeting → stand down on request too", () => {
		const result = runRequest(
			requestCtx({
				narratorId: "n-chat-2",
				userText: "在吗？",
				history: [{ role: "user", content: "在吗？" }],
			}),
		);
		expect(result).toBeNull();
	});
});

describe("getRouterModeFor", () => {
	test("returns classification; override wins; null clears", () => {
		const id = "n-mode-1";
		runSystemPrompt(sysPromptCtx({ narratorId: id, userText: "写一个计算器应用" }));
		expect(getRouterModeFor(id, null)).toBe(MODE_REACT);
		getRouterSessionState(id).override = MODE_WEAK;
		expect(getRouterModeFor(id, null)).toBe(MODE_WEAK);
		getRouterSessionState(id).override = null;
		expect(getRouterModeFor(id, null)).toBe(MODE_REACT);
	});
});

describe("isRouterModel (whitelist gate)", () => {
	test("whitelisted pattern matches; others do not", async () => {
		const { isRouterModel } = await import("../router/extension");
		// mock 白名单为 /flash/i:
		expect(isRouterModel("deepseek-v4-flash")).toBe(true);
		expect(isRouterModel("deepseek-v4-flash-lite")).toBe(true);
		expect(isRouterModel("claude-sonnet-4")).toBe(false);
		expect(isRouterModel(undefined)).toBe(false);
		expect(isRouterModel(null)).toBe(false);
	});

	test("empty whitelist disables routing entirely", async () => {
		// 空白名单语义由 shared 的 matchesReasoningEffortBlocklist 保证:
		// 空列表 → 无 pattern 匹配 → false(默认关闭)。此处直接验证该共享函数。
		const { matchesReasoningEffortBlocklist } = await import(
			"../../../shared/reasoning-effort-support"
		);
		expect(matchesReasoningEffortBlocklist("deepseek-v4-flash", [])).toBe(false);
		expect(matchesReasoningEffortBlocklist("deepseek-v4-flash", null)).toBe(false);
		expect(matchesReasoningEffortBlocklist("deepseek-v4-flash", [{ pattern: "/flash/i" }])).toBe(
			true,
		);
	});
});
