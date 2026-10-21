import { describe, expect, it } from "bun:test";
import { ACTIVITY_TOOL_ROWS, type ActivityState, applyActivityEvent } from "./dashboard-activity";

function run(events: Record<string, unknown>[], state: ActivityState = new Map()): ActivityState {
	return events.reduce((current, event) => applyActivityEvent(current, event), state);
}

describe("applyActivityEvent", () => {
	it("seeds from the snapshot and follows a tool through its lifecycle", () => {
		const state = run([
			{
				type: "activity_snapshot",
				narratorId: "n",
				toolCalls: [
					{
						toolUseId: "old",
						toolName: "Read",
						status: "success",
						inputSummary: { file_path: "a.ts" },
					},
				],
			},
			{
				type: "tool_started",
				narratorId: "n",
				toolUseId: "t1",
				toolName: "Bash",
				// A full-stream frame: the summary is derived from the raw input.
				input: { command: "bun test", content: "x".repeat(5000) },
			},
			{ type: "tool_completed", narratorId: "n", toolUseId: "t1", status: "success" },
		]);
		expect(state.get("n")?.tools).toEqual([
			{
				toolUseId: "old",
				toolName: "Read",
				status: "success",
				inputSummary: { file_path: "a.ts" },
			},
			{
				toolUseId: "t1",
				toolName: "Bash",
				status: "success",
				inputSummary: { command: "bun test" },
			},
		]);
	});

	it("ignores subagent child events and never moves a finished tool backwards", () => {
		const state = run([
			{ type: "tool_started", narratorId: "n", toolUseId: "t", toolName: "Bash" },
			{ type: "tool_completed", narratorId: "n", toolUseId: "t", status: "success" },
			{ type: "tool_executing", narratorId: "n", toolUseId: "t" },
			{
				type: "tool_started",
				narratorId: "n",
				toolUseId: "child",
				toolName: "Read",
				parentToolUseId: "t",
			},
		]);
		expect(state.get("n")?.tools).toEqual([
			{ toolUseId: "t", toolName: "Bash", status: "success" },
		]);
	});

	it("keeps only the latest rows and tracks status, title and background work", () => {
		const events: Record<string, unknown>[] = Array.from(
			{ length: ACTIVITY_TOOL_ROWS + 2 },
			(_, index) => ({
				type: "tool_started",
				narratorId: "n",
				toolUseId: `t${index}`,
				toolName: "Grep",
			}),
		);
		const state = run([
			...events,
			{ type: "status_change", narratorId: "n", status: "waiting", substatus: ["reflecting"] },
			{ type: "title_updated", narratorId: "n", title: "Fix CI" },
			{
				type: "background_task_count_changed",
				narratorId: "n",
				activeBackgroundTaskCount: 3,
				activeBackgroundWorkCount: 2,
			},
		]);
		const activity = state.get("n");
		expect(activity?.tools.map((tool) => tool.toolUseId)).toEqual(["t2", "t3", "t4", "t5"]);
		expect(activity).toMatchObject({
			status: "waiting",
			substatus: ["reflecting"],
			title: "Fix CI",
			activeBackgroundWorkCount: 2,
		});
	});

	it("returns the same state for frames that change nothing", () => {
		const state: ActivityState = new Map();
		expect(applyActivityEvent(state, { type: "text", narratorId: "n" })).toBe(state);
		expect(
			applyActivityEvent(state, { type: "tool_completed", narratorId: "n", toolUseId: "x" }),
		).toBe(state);
	});
});
