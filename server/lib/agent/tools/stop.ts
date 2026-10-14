import { backgroundTaskService } from "@server/services/background-task-service";
import { assertOAuthNarratorRuntimeActive } from "@server/services/oauth-narrator-runtime-policy";
import { authorizeStopTask, resolveOwnedStopTask } from "@server/services/stop-task-policy";
import { z } from "zod/v4";
import type { ToolDefinition, ToolResult } from "../types";

const parameters = z.object({
	type: z.enum(["bash", "agent"]).describe("The kind of background task to cancel."),
	id: z.string().trim().min(1).describe("The background task ID or alias owned by this narrator."),
});

export const stopTool: ToolDefinition = {
	name: "Stop",
	description:
		"Cancel a running background Bash task or agent owned by this narrator. " +
		'Provide both type ("bash" or "agent") and id (task ID or alias). ' +
		"This only cancels tracked background work; it cannot stop the NarraFork server or arbitrary PIDs. " +
		"Use Bash to launch commands, Agent to launch/archive/unarchive agents, and Await to wait for results.",
	parameters,
	rawJsonSchema: {
		type: "object",
		properties: {
			type: {
				type: "string",
				enum: ["bash", "agent"],
				description: "The kind of background task to cancel.",
			},
			id: {
				type: "string",
				minLength: 1,
				description: "The background task ID or alias owned by this narrator. Must not be blank.",
			},
		},
		required: ["type", "id"],
		additionalProperties: false,
	},
	getRawJsonSchema(config) {
		const schema = stopTool.rawJsonSchema as Record<string, unknown>;
		if (!config.parentNarratorId) return schema;
		const properties = schema.properties as Record<string, Record<string, unknown>>;
		return {
			...schema,
			properties: {
				...properties,
				type: { ...properties.type, enum: ["bash"] },
			},
		};
	},
	async execute(args, ctx): Promise<ToolResult> {
		// The executor validates input but passes the original, untransformed object.
		const parsed = parameters.safeParse(args);
		if (!parsed.success) {
			return { output: `Invalid Stop parameters: ${parsed.error.message}`, isError: true };
		}
		const { type, id } = parsed.data;
		if (ctx.parentNarratorId && type === "agent") {
			return {
				output: 'Subagents may only use Stop({ type: "bash", id }) for their own background tasks.',
				isError: true,
			};
		}
		try {
			const task = await resolveOwnedStopTask(ctx.narratorId, parsed.data);
			// Imports/child validation can yield after final admission. Re-read the live
			// OAuth ceiling immediately before cancellation, not from model input or cwd.
			const authorizeCancellation = async () => {
				await ctx.recheckAuthorization?.();
				const runtime = await assertOAuthNarratorRuntimeActive(ctx.narratorId, ctx.userId);
				if (runtime) {
					if (!runtime.allowedTools.has("Stop"))
						throw new Error("OAuth tool capability was revoked");
					const authorization = await authorizeStopTask(
						ctx.narratorId,
						{ type, id: task.id },
						{
							permissionMode: runtime.permissionMode,
							allowKnowledgeWrite: runtime.allowKnowledgeWrite,
							deviceAccess: runtime.policy.deviceAccess,
							oauthClientId: runtime.clientId,
							grantId: runtime.grantId,
						},
					);
					return () => {
						ctx.signal.throwIfAborted();
						authorization.assertStillCurrent();
					};
				}
				ctx.signal.throwIfAborted();
				return () => ctx.signal.throwIfAborted();
			};

			let cancelled: boolean;
			if (type === "agent") {
				// Never pass an arbitrary task/narrator ID to the cancellation registry.
				const { narratorService } = await import("@server/services/narrator-service");
				const { isSubagentVariant } = await import("@server/lib/narrator-utils");
				const narratorId = task.subagentNarratorId;
				const narrator = narratorId
					? await narratorService.getById(narratorId).catch(() => null)
					: null;
				if (
					!narrator ||
					!isSubagentVariant(narrator.variant) ||
					narrator.parentNarratorId !== ctx.narratorId
				) {
					return {
						output: `Background agent task ${id} is not a direct child subagent of this narrator.`,
						isError: true,
					};
				}
				const { cancelBackgroundTask } = await import("@server/services/narrator-subagent");
				const assertAuthorized = await authorizeCancellation();
				assertAuthorized();
				cancelled = await cancelBackgroundTask(narrator.id);
			} else {
				const assertAuthorized = await authorizeCancellation();
				assertAuthorized();
				cancelled = await backgroundTaskService.cancel(task.id, { assertAuthorized });
			}
			return cancelled
				? { output: `Background ${type} task ${id} has been cancelled.` }
				: {
						output: `Background ${type} task ${id} is not running (may have already completed, failed, or been cancelled).`,
						isError: true,
					};
		} catch (error) {
			return {
				output: `Stop error: ${error instanceof Error ? error.message : String(error)}`,
				isError: true,
			};
		}
	},
};
