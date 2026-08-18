import { afterAll, describe, expect, mock, test } from "bun:test";
import { z } from "zod/v4";
import type { ProviderAdapter } from "../provider";
import { toolRegistry } from "../tool-registry";
import type { AgentConfig, AgentEvent, AgentLoopExtension } from "../types";

// 观察点：mock provider 记录 agentLoop 实际发出的请求内容
// 用对象属性而非顶层 let——TS 流程分析不会对对象属性做赋值收窄，
// 避免 expect(...).toBe(...) 时把变量误判为 undefined 而报 TS2769。
const observed: {
	injectedSystemPrompt: string | undefined;
	sentHistory: unknown[] | undefined;
	sentTools: unknown[] | undefined;
} = {
	injectedSystemPrompt: undefined,
	sentHistory: undefined,
	sentTools: undefined,
};

// 函数内赋值不会触发外部作用域的控制流收窄，expect 时类型保持 string|undefined。
function resetObserved(): void {
	observed.injectedSystemPrompt = undefined;
	observed.sentHistory = undefined;
	observed.sentTools = undefined;
}

const testProvider: ProviderAdapter = {
	formatTools: (tools) => tools,
	buildHistory: async () => ({ history: [], trailingToolResults: [] }),
	injectSystemPrompt: (history, systemPrompt) => {
		observed.injectedSystemPrompt = systemPrompt;
		history.push({ role: "system", content: systemPrompt });
	},
	async *chat(params) {
		params.onRequestStart?.();
		observed.sentHistory = params.history;
		observed.sentTools = params.tools;
		yield { text: "hello from extension test provider" };
		return;
	},
	formatToolResult: (toolUseId, output, isError) => ({ toolUseId, output, isError }),
	pushUserTurn: () => {},
	pushAssistantTurn: () => {},
	generate: async () => "",
	generateWithMeta: async () => ({ text: "" }),
	generateWithHistory: async () => "",
};

// Snapshot real provider before mocking; afterAll re-points it back (Bun mock.module is global and leaks; mock.restore() does not undo it).
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

toolRegistry.register({
	name: "LoopExtToolA",
	description: "Test tool A for extension tool anchoring",
	parameters: z.object({}),
	execute: async () => ({ output: "a" }),
});
toolRegistry.register({
	name: "LoopExtToolB",
	description: "Test tool B for extension tool anchoring",
	parameters: z.object({}),
	execute: async () => ({ output: "b" }),
});

afterAll(() => {
	mock.module("../provider", () => realProviderModule);
	toolRegistry.unregister("LoopExtToolA");
	toolRegistry.unregister("LoopExtToolB");
});

function makeConfig(
	signal: AbortSignal,
	extensions: AgentLoopExtension[],
	overrides: Partial<AgentConfig> = {},
): AgentConfig {
	return {
		narratorId: "n-loop-extensions",
		conversationId: "conv-loop-extensions",
		model: "test:model",
		provider: "test",
		cwd: "/tmp",
		signal,
		maxTurns: 1,
		permissionHandler: async () => ({ behavior: "allow" }),
		loopExtensions: extensions,
		...overrides,
	};
}

async function drain(
	ac: AbortController,
	extensions: AgentLoopExtension[],
	overrides: Partial<AgentConfig> = {},
): Promise<AgentEvent[]> {
	const events: AgentEvent[] = [];
	for await (const event of agentLoop(makeConfig(ac.signal, extensions, overrides), "hi", [])) {
		events.push(event);
	}
	return events;
}

describe("agentLoop request-level extensions", () => {
	test("onSystemPrompt suffix 拼接到空 systemPrompt", async () => {
		resetObserved();
		const ac = new AbortController();
		const ext: AgentLoopExtension = {
			onSystemPrompt: (ctx) => {
				expect(ctx.turnIndex).toBe(0);
				expect(ctx.provider).toBe("test");
				return { systemPromptSuffix: "persona-suffix" };
			},
		};
		await drain(ac, [ext]);
		expect(observed.injectedSystemPrompt).toBe("persona-suffix");
	});

	test("onSystemPrompt suffix 以换行分隔拼接到非空 systemPrompt", async () => {
		resetObserved();
		const ac = new AbortController();
		const ext: AgentLoopExtension = {
			onSystemPrompt: () => ({ systemPromptSuffix: "persona-suffix" }),
		};
		await drain(ac, [ext], { systemPrompt: "base prompt" });
		expect(observed.injectedSystemPrompt).toBe("base prompt\n\npersona-suffix");
	});

	test("onRequest appendMessages 进入本轮 history", async () => {
		resetObserved();
		const ac = new AbortController();
		const guidance = { role: "user", content: "near-field guidance" };
		const ext: AgentLoopExtension = {
			onRequest: (ctx) => {
				expect(ctx.turnIndex).toBe(0);
				expect(ctx.userText).toBe("hi");
				return { appendMessages: [guidance] };
			},
		};
		await drain(ac, [ext]);
		expect(observed.sentHistory).toContain(guidance);
	});

	test("onRequest allowTools 白名单过滤工具面", async () => {
		resetObserved();
		const ac = new AbortController();
		const ext: AgentLoopExtension = {
			onRequest: () => ({ allowTools: new Set(["LoopExtToolA"]) }),
		};
		await drain(ac, [ext]);
		const sent = observed.sentTools;
		expect(sent).toBeDefined();
		const names = (sent as unknown as Array<{ name: string }>).map((t) => t.name);
		expect(names).toContain("LoopExtToolA");
		expect(names).not.toContain("LoopExtToolB");
	});

	test("扩展抛异常不影响主流程", async () => {
		resetObserved();
		const ac = new AbortController();
		const ext: AgentLoopExtension = {
			onSystemPrompt: () => {
				throw new Error("persona boom");
			},
			onRequest: () => {
				throw new Error("request boom");
			},
		};
		const events = await drain(ac, [ext]);
		expect(observed.sentHistory).toBeDefined();
		expect(observed.sentTools).toBeDefined();
		expect(events.some((event) => event.type === "stream_text")).toBe(true);
		expect(events.some((event) => event.type === "error")).toBe(false);
	});
});
