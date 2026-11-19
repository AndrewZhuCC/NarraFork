import { afterAll, afterEach, expect, mock, test } from "bun:test";
import { settings } from "../../settings";
import { forgetModelTraceRequest, getModelTraceRequest } from "../modeltrace-request";
import type { ChatParams, ProviderAdapter } from "../provider";
import type { AgentConfig } from "../types";

const id = "modeltrace-loop-test";
let outbound: ChatParams | undefined;
let startsRequest = true;
const adapter: ProviderAdapter = {
	formatTools: (tools) => tools.map(({ name }) => ({ name })),
	buildHistory: async (rows) => ({
		history: rows.map((row) => ({ role: row.role, content: row.contentText })),
	}),
	injectSystemPrompt: (history, text) => history.unshift({ system: text }),
	async *chat(params) {
		outbound = params;
		if (startsRequest) params.onRequestStart?.();
		yield { text: "finished", stopReason: "stop" };
	},
	generate: async () => "",
	generateWithMeta: async () => ({ text: "" }),
	generateWithHistory: async () => "",
};
const realProvider = { ...(await import("../provider")) };
mock.module("../provider", () => ({
	...realProvider,
	resolveProviderAndModel: (model?: string) =>
		model?.startsWith("codex:")
			? { adapter, provider: "codex", model }
			: { adapter, provider: "test", model: "test-model" },
}));
const { agentLoop } = await import("../loop");
afterEach(() => {
	forgetModelTraceRequest(id);
	outbound = undefined;
	startsRequest = true;
});
afterAll(() => {
	mock.module("../provider", () => realProvider);
	mock.restore();
});
function config(): AgentConfig {
	return {
		narratorId: id,
		conversationId: "parent-session-id",
		model: "test-model",
		provider: "test",
		cwd: "/workspace",
		systemPrompt: "parent system prompt",
		signal: new AbortController().signal,
		permissionHandler: async () => ({ behavior: "deny", message: "no tools" }),
		maxTurns: 1,
	};
}

test("captures the actual post-injection provider prefix, before later answer turns", async () => {
	for await (const _event of agentLoop(config(), "parent question", {
		rows: [
			{
				id: "old",
				role: "user",
				contentJson: [],
				contentText: "earlier history",
				parentToolUseId: null,
				messageUuid: null,
			},
		],
	})) {
		// Consume the single request, including history append after completion.
	}
	const snapshot = getModelTraceRequest(id);
	expect(snapshot?.adapter).toBe(adapter);
	expect(snapshot?.provider).toBe("test");
	expect(snapshot?.params.conversationId).toBe("parent-session-id");
	expect(snapshot?.params.stickySessionKey).toBe(id);
	// Public builds the full current user turn into history, not ChatParams.content.
	expect(snapshot?.params.content).toBe("");
	expect(snapshot?.params.history).toEqual([
		{ system: "parent system prompt" },
		{ role: "user", content: expect.stringContaining("earlier history") },
		{ role: "user", content: "parent question" },
	]);
	expect(snapshot?.params.tools).toEqual(outbound?.tools);
	expect(snapshot?.params).not.toHaveProperty("onRequestStart");
});

test("dispatches Ultrafast only for the named, allowlisted Codex model", async () => {
	const model = "codex:gpt-6.2-sol";
	settings.agent.codexUltrafastModels = ["gpt-6.2-sol", "test-model"];
	for (const [reference, effective, tier] of [
		[model, model, "ultrafast"],
		["auto:gpt-6.2-sol", model, "ultrafast"],
		["backup:gpt-6.2-sol", model, "ultrafast"],
		["__agg__:gpt-6.2-sol", model, undefined],
		["auto:gpt-other", model, undefined],
		["test:test-model", "test-model", undefined],
	] as const) {
		const run = {
			...config(),
			model: effective,
			provider: effective === model ? "codex" : "test",
			serviceTier: "ultrafast",
			getUltrafastModel: () => reference,
		};
		for await (const event of agentLoop(run, "check tier", { rows: [] })) {
			if (event.type === "error") throw new Error(event.message);
		}
		expect(outbound?.serviceTier).toBe(tier);
	}
});

test("does not capture local preparation that never starts an upstream request", async () => {
	startsRequest = false;
	for await (const _event of agentLoop(config(), "parent question", { rows: [] })) {
		// Some adapters can end during preparation without contacting the upstream.
	}
	expect(getModelTraceRequest(id)).toBeUndefined();
});
