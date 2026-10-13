import { describe, expect, test } from "bun:test";
import { type InterchangeSession, withFreshToolUseIds } from "./model";

function counter() {
	let n = 0;
	return () => `fresh-${++n}`;
}

const base: InterchangeSession = {
	source: "claude-code",
	externalId: "s",
	createdAt: "2026-01-01T00:00:00.000Z",
	updatedAt: "2026-01-01T00:00:00.000Z",
	messages: [
		{ role: "user", timestamp: "t0", blocks: [{ type: "text", text: "go" }] },
		{
			role: "assistant",
			timestamp: "t1",
			blocks: [
				{ type: "text", text: "two calls" },
				{ type: "tool_use", id: "orig-a", name: "Bash", input: {} },
				{ type: "tool_use", id: "orig-agent", name: "Agent", input: {}, subagentRef: "sub" },
			],
		},
		{ role: "tool_result", timestamp: "t2", toolUseId: "orig-a", output: "a", isError: false },
		{ role: "tool_result", timestamp: "t2", toolUseId: "orig-agent", output: "r", isError: false },
		// A result with no call keeps a fresh id of its own, not a borrowed one.
		{ role: "tool_result", timestamp: "t3", toolUseId: "orphan", output: "?", isError: false },
	],
	subagents: [
		{
			ref: "sub",
			parentToolUseId: "orig-agent",
			messages: [
				{
					role: "assistant",
					timestamp: "t1",
					// The same id inside the child thread is a different call.
					blocks: [{ type: "tool_use", id: "orig-a", name: "Read", input: {} }],
				},
				{ role: "tool_result", timestamp: "t1", toolUseId: "orig-a", output: "c", isError: false },
			],
		},
	],
	warnings: [],
};

describe("withFreshToolUseIds", () => {
	test("rewrites every id and keeps calls, results and subagents paired", () => {
		const out = withFreshToolUseIds(base, counter());
		const assistant = out.messages[1];
		if (assistant?.role !== "assistant") throw new Error("shape");
		const [, bash, agent] = assistant.blocks;
		expect(bash?.type === "tool_use" && bash.id).toBe("fresh-1");
		expect(agent?.type === "tool_use" && agent.id).toBe("fresh-2");
		// Unrelated fields survive the copy.
		expect(agent?.type === "tool_use" && agent.subagentRef).toBe("sub");

		const results = out.messages.filter((m) => m.role === "tool_result");
		expect(results.map((m) => m.role === "tool_result" && m.toolUseId)).toEqual([
			"fresh-1",
			"fresh-2",
			"fresh-3",
		]);
		expect(out.subagents[0]?.parentToolUseId).toBe("fresh-2");

		// Child thread ids come from their own map, so they never equal the parent's.
		const child = out.subagents[0]?.messages ?? [];
		const childCall = child[0]?.role === "assistant" ? child[0].blocks[0] : undefined;
		const childResult = child[1];
		expect(childCall?.type === "tool_use" && childCall.id).toBe("fresh-4");
		expect(childResult?.role === "tool_result" && childResult.toolUseId).toBe("fresh-4");
	});

	test("does not mutate its input", () => {
		const snapshot = JSON.stringify(base);
		withFreshToolUseIds(base, counter());
		expect(JSON.stringify(base)).toBe(snapshot);
	});

	test("a subagent whose parent call is missing gets an id nothing else uses", () => {
		const out = withFreshToolUseIds(
			{ ...base, subagents: [{ ...base.subagents[0], parentToolUseId: "gone", messages: [] }] },
			counter(),
		);
		const used = new Set(
			out.messages.flatMap((m) =>
				m.role === "tool_result"
					? [m.toolUseId]
					: m.role === "assistant"
						? m.blocks.flatMap((b) => (b.type === "tool_use" ? [b.id] : []))
						: [],
			),
		);
		expect(used.has(out.subagents[0]?.parentToolUseId ?? "")).toBe(false);
	});
});
