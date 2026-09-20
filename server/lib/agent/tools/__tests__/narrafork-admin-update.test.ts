import { beforeEach, describe, expect, mock, test } from "bun:test";
import type { PermissionResult, ToolContext } from "../../types";

let adminAllowed = true;
let permission: PermissionResult = { behavior: "allow" };

const getUpdateStatus = mock(async (targetVersion?: string) => ({
	ready: true,
	canAutoRestart: true,
	version: targetVersion ?? "1.4.2",
	phase: "idle" as const,
}));
const applyUpdate = mock(
	async (options: {
		targetVersion?: string;
	}): Promise<{
		success: boolean;
		scheduled?: boolean;
		targetVersion?: string;
		error?: string;
	}> => ({
		success: true,
		scheduled: true,
		targetVersion: options.targetVersion,
	}),
);
const cancelPreparedUpdate = mock(() => ({
	cancelled: true,
	status: { scheduled: false, phase: "idle" as const },
}));

mock.module("../admin-common", () => ({
	isAdminUser: mock(async (userId?: string | null) => !!userId && adminAllowed),
	requestWritePermission: async (
		ctx: ToolContext,
		toolName: string,
		input: Record<string, unknown>,
	) => {
		const decision = await ctx.requestPermission(toolName, input, ctx.currentToolUseId ?? toolName);
		return decision.behavior === "allow"
			? null
			: decision.behavior === "deny" && decision.message
				? decision.message
				: `${toolName} was denied by the user.`;
	},
}));

mock.module("../../../../services/update-service", () => ({
	getUpdateStatus,
	applyUpdate,
	cancelPreparedUpdate,
}));

const { narraforkAdminTool } = await import("../narrafork-admin");

function context(userRole = "admin"): ToolContext & { userRole: string } {
	return {
		narratorId: "narrator-1",
		cwd: "/worktree",
		signal: new AbortController().signal,
		locale: "en",
		userId: "user-1",
		userRole,
		currentToolUseId: "tool-use-1",
		requestPermission: mock(async () => permission),
	};
}

beforeEach(() => {
	adminAllowed = true;
	permission = { behavior: "allow" };
	getUpdateStatus.mockClear();
	applyUpdate.mockClear();
	cancelPreparedUpdate.mockClear();
});

describe("NarraForkAdmin update actions", () => {
	test("rejects non-admin execution before touching the update service", async () => {
		adminAllowed = false;
		const ctx = context("user");

		const result = await narraforkAdminTool.execute({ action: "apply_update" }, ctx);

		expect(result.isError).toBe(true);
		expect(ctx.requestPermission).not.toHaveBeenCalled();
		expect(applyUpdate).not.toHaveBeenCalled();
	});

	test("does not schedule an update without approval", async () => {
		permission = { behavior: "deny", message: "Not approved" };
		const result = await narraforkAdminTool.execute(
			{ action: "apply_update", targetVersion: "1.4.2" },
			context(),
		);

		expect(result).toMatchObject({ isError: true, output: "Not approved" });
		expect(applyUpdate).not.toHaveBeenCalled();
	});

	test("schedules an approved update exactly once", async () => {
		const result = await narraforkAdminTool.execute(
			{ action: "apply_update", targetVersion: "1.4.2" },
			context(),
		);

		expect(result.isError).toBeFalsy();
		expect(applyUpdate).toHaveBeenCalledTimes(1);
		expect(applyUpdate).toHaveBeenCalledWith({ targetVersion: "1.4.2" });
	});

	test("reports update-service scheduling failures", async () => {
		applyUpdate.mockImplementationOnce(async () => ({
			success: false,
			error: "No verified prepared update file found",
		}));

		const result = await narraforkAdminTool.execute({ action: "apply_update" }, context());

		expect(result.isError).toBe(true);
		expect(result.output).toContain("No verified prepared update file found");
		expect(applyUpdate).toHaveBeenCalledTimes(1);
	});

	test("reads status directly without requesting write approval", async () => {
		const ctx = context();
		const result = await narraforkAdminTool.execute(
			{ action: "get_update_status", targetVersion: "1.4.2" },
			ctx,
		);

		expect(result.isError).toBeFalsy();
		expect(result.output).toContain('"version": "1.4.2"');
		expect(ctx.requestPermission).not.toHaveBeenCalled();
		expect(getUpdateStatus).toHaveBeenCalledWith("1.4.2");
	});

	test("cancels only after approval and delegates once", async () => {
		const result = await narraforkAdminTool.execute({ action: "cancel_update" }, context());

		expect(result.isError).toBeFalsy();
		expect(cancelPreparedUpdate).toHaveBeenCalledTimes(1);
		expect(cancelPreparedUpdate).toHaveBeenCalledWith("Cancelled by NarraForkAdmin");
	});
});
