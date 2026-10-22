/**
 * Loop-control notices are rows, not in-memory text.
 *
 * The turn nudge used to be appended inside the last tool result, and the broken-call
 * reminder lived only in the next turn's string, so a request rebuilt from the database
 * never said what the live one had. Both are now notice rows the host persists
 * (`deliverInjectionRow`), placed after the step's results; a host that cannot persist
 * (a reflection loop) still sends their text from memory.
 */

import { afterAll, describe, expect, mock, test } from "bun:test";
import { z } from "zod/v4";
import type { ChatParams, ProviderAdapter } from "../provider";
import { toolRegistry } from "../tool-registry";
import type { AgentConfig, AgentEvent } from "../types";

const TOOL = "LoopNoticeTool";

let script: (request: number) => Array<Record<string, unknown>> = () => [{ text: "done" }];
const requests: Array<Pick<ChatParams, "content" | "toolResults">> = [];

const provider: ProviderAdapter = {
	formatTools: (tools) => tools,
	buildHistory: async () => ({ history: [], trailingToolResults: [] }),
	injectSystemPrompt: () => {},
	async *chat(params) {
		requests.push({ content: params.content, toolResults: structuredClone(params.toolResults) });
		params.onRequestStart?.();
		for (const event of script(requests.length - 1)) yield event;
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
	getProvider: () => provider,
	resolveProviderAndModel: () => ({
		requestedProvider: "test",
		requestedModel: "test:model",
		provider: "test",
		adapter: provider,
		model: "test:model",
	}),
}));
const { agentLoop } = await import("../loop");

toolRegistry.register({
	name: TOOL,
	description: "Loop notice test tool",
	parameters: z.object({ value: z.string() }),
	execute: async (args) => ({ output: `ran:${args.value}` }),
});

afterAll(() => {
	mock.module("../provider", () => realProviderModule);
	toolRegistry.unregister(TOOL);
});

function config(overrides: Partial<AgentConfig> = {}): AgentConfig {
	return {
		narratorId: "loop-notices",
		conversationId: "loop-notices",
		model: "test:model",
		provider: "test",
		cwd: "/tmp",
		signal: new AbortController().signal,
		permissionHandler: async () => ({ behavior: "allow" }),
		...overrides,
	};
}

function toolCall(request: number, input: Record<string, unknown>): Record<string, unknown> {
	return { toolUses: [{ toolUseId: `call-${request}`, name: TOOL, input, outputIndex: 0 }] };
}

async function run(cfg: AgentConfig): Promise<AgentEvent[]> {
	requests.length = 0;
	const events: AgentEvent[] = [];
	for await (const event of agentLoop(cfg, "start", [])) events.push(event);
	return events;
}

function resultOutputs(): string[] {
	return requests.flatMap((request) =>
		(request.toolResults as Array<{ output: string }>).map((result) => result.output),
	);
}

describe("turn nudge", () => {
	test("is a row after the step's results, never inside the last result", async () => {
		script = (request) =>
			request < 9 ? [toolCall(request, { value: `v${request}` })] : [{ text: "done" }];
		const delivered: Array<{ source: string; content: string }> = [];
		await run(
			config({
				maxTurns: 10,
				deliverInjectionRow: (injection) => {
					delivered.push({ source: injection.source, content: injection.content });
					return { text: injection.content, messageId: `row-${delivered.length}` };
				},
			}),
		);
		const nudges = delivered.filter((row) => row.source === "turn_nudge");
		// 80% of 10 turns: only the step at turn index 8 nudges.
		expect(nudges).toEqual([
			{
				source: "turn_nudge",
				content:
					"[SYSTEM: You have used 9 of 10 turns. Please wrap up your work soon — summarize remaining steps if you cannot finish in time.]",
			},
		]);
		expect(resultOutputs().some((output) => output.includes("wrap up"))).toBe(false);
		// The next request carries it once, after that step's results.
		expect(requests[9]?.content).toBe(nudges[0]?.content);
		expect(requests[9]?.toolResults).toHaveLength(1);
	});
});

describe("broken call", () => {
	test("its reminder is a row and the stored result is marked by its sanitized input", async () => {
		script = (request) =>
			request === 0 ? [toolCall(request, { _raw: '{"value":' })] : [{ text: "done" }];
		const delivered: Array<{ source: string; content: string }> = [];
		const events = await run(
			config({
				deliverInjectionRow: (injection) => {
					delivered.push({ source: injection.source, content: injection.content });
					return { text: injection.content, messageId: "broken-row" };
				},
			}),
		);
		const result = events.find(
			(event): event is Extract<AgentEvent, { type: "tool_result" }> =>
				event.type === "tool_result",
		);
		expect(result?.brokenInputOverride).toBeDefined();
		const reminder = delivered.find((row) => row.source === "broken_call");
		expect(reminder?.content).toContain(
			`Your previous ${TOOL} call(s) had invalid or missing input`,
		);
		// The broken pair is left out of the next request; the reminder says why.
		expect(requests[1]?.toolResults).toEqual([]);
		expect(requests[1]?.content).toBe(reminder?.content ?? "missing reminder");
	});

	test("a host without a store still sends the reminder from memory", async () => {
		script = (request) =>
			request === 0 ? [toolCall(request, { _raw: '{"value":' })] : [{ text: "done" }];
		await run(config());
		expect(requests[1]?.content).toContain(
			`Your previous ${TOOL} call(s) had invalid or missing input`,
		);
	});
});
