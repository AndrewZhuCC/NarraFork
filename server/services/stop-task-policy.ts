import { and, eq } from "drizzle-orm";
import { db } from "../db";
import {
	backgroundTasks,
	integrationResourceBindings,
	narratorToolCalls,
	remoteDevices,
} from "../db/schema";
import { isBashToolName } from "../lib/agent/tool-name";
import { backgroundTaskService } from "./background-task-service";
import type { RuntimePermissionConstraint } from "./narrator-permission";
import { resolveTaskAlias } from "./subagent-alias";

/** Resolve control authority from the tracked task, never from input.device or session cwd. */
export async function resolveOwnedStopTask(narratorId: string, input: Record<string, unknown>) {
	if (
		(input.type !== "bash" && input.type !== "agent") ||
		typeof input.id !== "string" ||
		!input.id.trim()
	)
		throw new Error("Invalid Stop parameters: provide task type and a non-blank id.");
	const id = input.id.trim();
	const resolvedId = resolveTaskAlias(narratorId, id);
	let task = await backgroundTaskService.getById(resolvedId);
	if (!task && resolvedId === id) task = await backgroundTaskService.getByAlias(id, narratorId);
	if (!task) throw new Error(`Background task ${id} does not exist.`);
	if (task.parentNarratorId !== narratorId)
		throw new Error(`Background task ${id} does not belong to this narrator.`);
	if (task.type !== input.type)
		throw new Error(
			`Background task ${id} has type ${task.type}, not ${input.type}. Stop type must match the task.`,
		);
	return task;
}

/** No filesystem/backend probes: offline devices remain cancellable when allowed. */
export async function authorizeStopTask(
	narratorId: string,
	input: Record<string, unknown>,
	constraint: RuntimePermissionConstraint,
) {
	const task = await resolveOwnedStopTask(narratorId, input);
	if (!constraint.deviceAccess || !constraint.oauthClientId || !constraint.grantId)
		throw new Error("OAuth device access policy is missing.");
	// Agent tasks persist a child narrator, not an execution target. A child's mutable
	// default device is NOT its full reach (per-call overrides and workspace switches).
	if (
		task.type !== "bash" ||
		!task.toolCallId ||
		!Number.isSafeInteger(task.executionAttempt) ||
		(task.executionAttempt ?? 0) <= 0
	)
		throw new Error(
			"Cannot reliably identify the target device; OAuth does not authorize cancellation.",
		);
	const source = db.query.narratorToolCalls
		.findFirst({
			where: eq(narratorToolCalls.id, task.toolCallId),
			columns: {
				narratorId: true,
				toolName: true,
				toolUseId: true,
				executionAttempt: true,
				executionIdentityVersion: true,
				isFileHistoryCheckpoint: true,
				executionOriginToolCallId: true,
				executionStartedAt: true,
				executionDeviceId: true,
			},
		})
		.sync();
	if (
		!source ||
		source.narratorId !== narratorId ||
		!isBashToolName(source.toolName) ||
		(task.toolUseId !== null && source.toolUseId !== task.toolUseId) ||
		source.executionAttempt !== task.executionAttempt ||
		source.executionIdentityVersion !== 1 ||
		source.isFileHistoryCheckpoint ||
		source.executionOriginToolCallId !== null ||
		!source.executionStartedAt ||
		!source.executionDeviceId?.trim()
	)
		throw new Error(
			"Cannot reliably identify the target device; OAuth does not authorize cancellation.",
		);
	const toolCallId = task.toolCallId;
	const deviceId = source.executionDeviceId;
	const taskIdentity = () =>
		db
			.select({
				parent: backgroundTasks.parentNarratorId,
				type: backgroundTasks.type,
				call: backgroundTasks.toolCallId,
				attempt: backgroundTasks.executionAttempt,
				toolUseId: backgroundTasks.toolUseId,
			})
			.from(backgroundTasks)
			.where(eq(backgroundTasks.id, task.id))
			.get();
	const expectedTask = {
		parent: narratorId,
		type: task.type,
		call: toolCallId,
		attempt: task.executionAttempt,
		toolUseId: task.toolUseId,
	};
	if (JSON.stringify(taskIdentity()) !== JSON.stringify(expectedTask))
		throw new Error("Stop task binding changed during authorization.");
	const captureIdentity = () =>
		JSON.stringify({
			task: taskIdentity(),
			source: db
				.select({
					narrator: narratorToolCalls.narratorId,
					toolName: narratorToolCalls.toolName,
					toolUseId: narratorToolCalls.toolUseId,
					attempt: narratorToolCalls.executionAttempt,
					version: narratorToolCalls.executionIdentityVersion,
					checkpoint: narratorToolCalls.isFileHistoryCheckpoint,
					startedAt: narratorToolCalls.executionStartedAt,
					device: narratorToolCalls.executionDeviceId,
					origin: narratorToolCalls.executionOriginToolCallId,
				})
				.from(narratorToolCalls)
				.where(eq(narratorToolCalls.id, toolCallId))
				.get(),
			device: db
				.select({ id: remoteDevices.id, scope: remoteDevices.scope })
				.from(remoteDevices)
				.where(eq(remoteDevices.id, deviceId))
				.get(),
			binding: db
				.select({
					source: integrationResourceBindings.sourceId,
					sourceType: integrationResourceBindings.sourceType,
					authority: integrationResourceBindings.authorityId,
					authorityType: integrationResourceBindings.authorityType,
					revision: integrationResourceBindings.revision,
				})
				.from(integrationResourceBindings)
				.where(
					and(
						eq(integrationResourceBindings.resourceType, "device"),
						eq(integrationResourceBindings.resourceId, deviceId),
					),
				)
				.get(),
		});
	const identity = captureIdentity();
	const { classifyDeviceAccessGroup, createOAuthTaskControlFence } = await import(
		"./narrator-permission"
	);
	const oauthFence = createOAuthTaskControlFence(narratorId, constraint);
	const assertStillCurrent = () => {
		oauthFence.assertStillCurrent();
		if (captureIdentity() !== identity)
			throw new Error("Stop target authorization changed before cancellation.");
	};
	const group = await classifyDeviceAccessGroup(deviceId, {
		oauthClientId: constraint.oauthClientId,
		grantId: constraint.grantId,
	});
	const level = constraint.deviceAccess[group];
	// Read-only retains cancellation of already-running work, matching legacy Bash stop.
	if (level !== "readOnly" && level !== "readWrite")
		throw new Error(`OAuth device access policy denies this device group: ${group}`);
	assertStillCurrent();
	return { task, assertStillCurrent };
}
