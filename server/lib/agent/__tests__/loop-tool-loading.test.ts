/**
 * The loop sends the history's loading state as a fixed tool array.
 *
 * Managed optional tools pass `toolFilter` unconditionally; the loop keeps only the base
 * set in the array, adds the deferred placeholder for the Anthropic modes, re-derives the
 * array when a rebuilt history moves the epoch boundary, and the executor rejects a call
 * to a managed tool that is no longer loaded even though it is still declared.
 */

import { afterAll, describe, expect, mock, test } from "bun:test";
import { z } from "zod/v4";
import type { ChatParams, ProviderAdapter } from "../provider";
import { DEFERRED_TOOL_PLACEHOLDER, type ToolLoadingState } from "../tool-loading";
import { toolRegistry } from "../tool-registry";
import type { AgentConfig } from "../types";

const CORE = "TlCoreTool";
const BASE = "TlBaseTool";
const LOADED = "TlLoadedTool";
const OWN = new Set([CORE, BASE, LOADED]);

const requests: Array<{ tools: string[]; strategy: ChatParams["toolLoadingStrategy"] }> = [];
const toolOutputs: string[] = [];
let script: Array<() => Record<string, unknown>> = [];

const testProvider: ProviderAdapter = {
	formatTools: (tools) => tools.map(({ name }) => ({ name })),
	toolLoadingTarget: () => ({ protocol: "anthropic-messages" }),
	buildHistory: async () => ({ history: [], trailingToolResults: [] }),
	injectSystemPrompt: () => {},
	async *chat(params) {
		requests.push({
			tools: (params.tools as Array<{ name: string }>)
				.map((tool) => tool.name)
				.filter((name) => OWN.has(name) || name === DEFERRED_TOOL_PLACEHOLDER.name),
			strategy: params.toolLoadingStrategy,
		});
		const next = script.shift();
		yield next ? next() : { text: "done" };
	},
	formatToolResult: (toolUseId, output, isError) => {
		toolOutputs.push(output);
		return { toolUseId, output, isError };
	},
	pushAssistantTurn: () => {},
	pushUserTurn: () => {},
	generate: async () => "",
	generateWithMeta: async () => ({ text: "" }),
	generateWithHistory: async () => "",
};

const realProviderModule = { ...(await import("../provider")) };

mock.module("../provider", () => ({
	getProvider: () => testProvider,
	resolveProviderAndModel: () => ({
		requestedProvider: "test",
		requestedModel: "test:claude-opus-5-5",
		provider: "test",
		adapter: testProvider,
		model: "test:claude-opus-5-5",
	}),
}));

const { agentLoop } = await import("../loop");

for (const name of OWN)
	toolRegistry.register({
		name,
		description: `${name} description`,
		parameters: z.object({}),
		execute: async () => ({ output: `${name} ran` }),
	});

afterAll(() => {
	mock.module("../provider", () => realProviderModule);
	for (const name of OWN) toolRegistry.unregister(name);
	mock.restore();
});

function state(base: string[], loaded: string[], current: string[]): ToolLoadingState {
	return {
		managed: new Set([BASE, LOADED]),
		base: new Set(base),
		loaded,
		current: new Set(current),
	};
}

function makeConfig(overrides: Partial<AgentConfig>): AgentConfig {
	return {
		narratorId: "n-tool-loading",
		conversationId: "conv-tool-loading",
		model: "test:claude-opus-5-5",
		provider: "test",
		cwd: "/tmp",
		signal: new AbortController().signal,
		permissionHandler: async () => ({ behavior: "allow" }),
		...overrides,
	} as AgentConfig;
}

async function drain(config: AgentConfig): Promise<void> {
	for await (const _event of agentLoop(config, "go", [])) {
		// Run the loop to completion.
	}
}

function reset(steps: Array<() => Record<string, unknown>>): void {
	requests.length = 0;
	toolOutputs.length = 0;
	script = steps;
}

const callTool = (name: string) => () => ({
	toolUses: [{ toolUseId: `tu_${name}`, name, input: {} }],
});

describe("loop tool loading", () => {
	test("keeps loaded tools out of the array and re-derives it after a rebuild", async () => {
		reset([callTool(CORE)]);
		await drain(
			makeConfig({
				toolLoading: state([BASE], [LOADED], [BASE, LOADED]),
				isToolLoaded: () => true,
				// A compact folds the load into the new epoch's base set.
				onBeforeTurn: async () => ({
					history: [],
					pendingToolResults: [],
					toolLoading: state([BASE, LOADED], [], [BASE, LOADED]),
				}),
			}),
		);
		expect(requests).toEqual([
			{
				tools: [CORE, BASE, DEFERRED_TOOL_PLACEHOLDER.name],
				strategy: "anthropic_tool_addition",
			},
			{
				tools: [CORE, BASE, LOADED, DEFERRED_TOOL_PLACEHOLDER.name],
				strategy: "anthropic_tool_addition",
			},
		]);
	});

	test("rejects a declared tool that is no longer loaded", async () => {
		reset([callTool(BASE)]);
		await drain(
			makeConfig({
				toolLoading: state([BASE], [], []),
				isToolLoaded: (name) => name !== BASE,
			}),
		);
		expect(requests[0]?.tools).toContain(BASE);
		expect(toolOutputs[0]).toContain(`Tool is not loaded in this session: ${BASE}`);
	});

	test("without a loading state every filtered tool is sent as before", async () => {
		reset([]);
		await drain(makeConfig({}));
		expect(requests[0]).toEqual({ tools: [CORE, BASE, LOADED], strategy: "replace_tools" });
	});
});
