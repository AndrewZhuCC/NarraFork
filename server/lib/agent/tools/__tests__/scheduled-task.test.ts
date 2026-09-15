import { describe, expect, test } from "bun:test";
import { summarizeSubagentToolCall } from "../../../../services/subagent-activity";
import { getBuiltinRoutine, getBuiltinToolNames } from "../../../builtin-routines";
import { OPTIONAL_TOOLS } from "../index";
import { scheduledTaskTool } from "../scheduled-task";
import { isScheduledTaskReadAction, SCHEDULED_TASK_ACTIONS } from "../scheduled-task-actions";

describe("ScheduledTask registration", () => {
	test("is optional and off by default", () => {
		const routine = getBuiltinRoutine("scheduled_task");
		expect(routine?.defaultEnabled).toBeUndefined();
		expect(routine?.tool).toBeDefined();
		if (!routine?.tool) throw new Error("scheduled_task routine is missing its tool definition");
		expect(getBuiltinToolNames(routine.tool)).toEqual(["ScheduledTask"]);
		expect(OPTIONAL_TOOLS.has("ScheduledTask")).toBe(true);
	});

	/**
	 * The permission layer classifies by action, so a new action reaching the tool
	 * without being classified would be silently gated as mutating. That is the safe
	 * direction, but it should be a deliberate choice rather than an oversight, so the
	 * two lists are pinned together here.
	 */
	test("every action is classified as read or mutating", () => {
		const reads = SCHEDULED_TASK_ACTIONS.filter(isScheduledTaskReadAction);
		expect(reads).toEqual(["list", "get", "runs"]);
		const mutating = SCHEDULED_TASK_ACTIONS.filter((a) => !isScheduledTaskReadAction(a));
		expect(mutating).toEqual(["create", "update", "enable", "disable", "delete", "run_now"]);
	});

	test("unknown action strings are not treated as reads", () => {
		for (const value of ["", "LIST", "purge", "run", undefined, null, 1, {}]) {
			expect(isScheduledTaskReadAction(value)).toBe(false);
		}
	});
});

/**
 * These reject before any service call, so they need no database. Each asserts the
 * tool refuses rather than letting the service produce a less specific error.
 */
describe("ScheduledTask argument validation", () => {
	const ctx = {
		narratorId: "n1",
		cwd: "/tmp",
		signal: new AbortController().signal,
		locale: "en",
		requestPermission: async () => ({ behavior: "allow" as const }),
	};

	async function run(args: Record<string, unknown>) {
		return scheduledTaskTool.execute(args, ctx);
	}

	test("accepts task effort tiers and null without inventing an override", () => {
		for (const reasoningEffort of [null, "none", "low", "medium", "high", "xhigh", "max"]) {
			const result = scheduledTaskTool.parameters.parse({
				action: "update",
				id: "t1",
				task: { reasoningEffort },
			});
			expect(result).toMatchObject({ task: { reasoningEffort } });
		}
		const result = scheduledTaskTool.parameters.parse({
			action: "update",
			id: "t1",
			task: { name: "x" },
		});
		expect(result).toMatchObject({ task: { name: "x" } });
		expect((result as { task: object }).task).not.toHaveProperty("reasoningEffort");
	});

	test("round-trips task effort through create, list and update", async () => {
		const created = await run({
			action: "create",
			task: {
				name: "Effort test",
				cronExpr: "0 9 * * *",
				prompt: "test",
				enabled: false,
				reasoningEffort: "low",
			},
		});
		expect(created.isError).toBeFalsy();
		const task = JSON.parse(created.output);
		try {
			expect(task.reasoningEffort).toBe("low");
			const listed = JSON.parse((await run({ action: "list" })).output);
			expect(listed.find((row: { id: string }) => row.id === task.id)?.reasoningEffort).toBe("low");
			const changed = await run({
				action: "update",
				id: task.id,
				task: { reasoningEffort: "max" },
			});
			expect(JSON.parse(changed.output).reasoningEffort).toBe("max");
			const cleared = await run({ action: "update", id: task.id, task: { reasoningEffort: null } });
			expect(JSON.parse(cleared.output).reasoningEffort).toBeNull();
		} finally {
			await run({ action: "delete", id: task.id });
		}
	});

	test("rejects an invalid task effort before reaching the service", async () => {
		const result = await run({ action: "update", id: "t1", task: { reasoningEffort: "extreme" } });
		expect(result.isError).toBe(true);
		expect(result.output).toContain("reasoningEffort");
	});

	test("rejects an unknown action without reaching the service", async () => {
		const result = await run({ action: "purge" });
		expect(result.isError).toBe(true);
		expect(result.output).toContain("Invalid ScheduledTask arguments");
	});

	test("requires an id for id-based actions", async () => {
		for (const action of ["get", "update", "enable", "disable", "delete", "run_now", "runs"]) {
			const result = await run({ action, task: { name: "x" } });
			expect(result.isError).toBe(true);
			expect(result.output).toContain("'id' is required");
		}
	});

	test("create names every missing required field at once", async () => {
		const result = await run({ action: "create", task: { name: "Nightly" } });
		expect(result.isError).toBe(true);
		expect(result.output).toContain("task.cronExpr");
		expect(result.output).toContain("task.prompt");
	});

	test("rejects a syntactically valid cron that never fires", async () => {
		const result = await run({
			action: "create",
			// Feb 30th parses but has no next run; croner returns null instead of throwing.
			task: { name: "Never", cronExpr: "0 0 30 2 *", prompt: "hi" },
		});
		expect(result.isError).toBe(true);
		expect(result.output).toContain("Invalid cron expression");
	});

	test("rejects an unknown timezone", async () => {
		const result = await run({
			action: "create",
			task: { name: "Bad tz", cronExpr: "0 9 * * *", prompt: "hi", timezone: "Mars/Olympus" },
		});
		expect(result.isError).toBe(true);
		expect(result.output).toContain("Mars/Olympus");
	});

	test("chapter runContext names the specific missing id", async () => {
		const result = await run({
			action: "create",
			task: {
				name: "In chapter",
				cronExpr: "0 9 * * *",
				prompt: "hi",
				runContext: "chapter",
				projectId: "p1",
			},
		});
		expect(result.isError).toBe(true);
		expect(result.output).toContain("chapterId");
		expect(result.output).not.toContain("projectId and");
	});

	test("update with no fields is refused instead of writing an empty patch", async () => {
		const result = await run({ action: "update", id: "t1", task: {} });
		expect(result.isError).toBe(true);
		expect(result.output).toContain("at least one field");
	});

	test("run-history limit is capped by the schema", async () => {
		const result = await run({ action: "runs", id: "t1", limit: 5000 });
		expect(result.isError).toBe(true);
		expect(result.output).toContain("Invalid ScheduledTask arguments");
	});

	test("an over-long prompt is refused before it can be persisted", async () => {
		const result = await run({
			action: "create",
			task: { name: "Huge", cronExpr: "0 9 * * *", prompt: "x".repeat(50_001) },
		});
		expect(result.isError).toBe(true);
		expect(result.output).toContain("Invalid ScheduledTask arguments");
	});
});

describe("ScheduledTask activity summary", () => {
	test("prefers the task name over the opaque id", () => {
		expect(
			summarizeSubagentToolCall("ScheduledTask", {
				action: "create",
				taskName: "Nightly sync",
				// `targetId` is the hints field the query maps `$.id` into.
				targetId: "AbCdEfGhIjKlMnOpQrStU",
			}),
		).toBe("create: Nightly sync");
	});

	test("falls back to the id when no name is present", () => {
		expect(
			summarizeSubagentToolCall("ScheduledTask", { action: "delete", targetId: "task-123" }),
		).toBe("delete: task-123");
	});

	test("never surfaces the prompt body", () => {
		const summary = summarizeSubagentToolCall("ScheduledTask", {
			action: "create",
			taskName: "Report",
			// A hints object never carries `prompt`, but assert the formatter ignores it
			// even if a future query starts selecting one.
			prompt: "SECRET INSTRUCTION",
		} as never);
		expect(summary).not.toContain("SECRET");
	});
});
