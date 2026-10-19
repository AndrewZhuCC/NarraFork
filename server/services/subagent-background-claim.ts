/**
 * Claim an idle subagent as a running BACKGROUND task before continuing it.
 *
 * `preserveBackground` on a resume only means "do not clear the background flags";
 * a subagent that is not already background must be marked so explicitly. Both
 * failed-subagent recovery and parent Send continuations go through this claim, so
 * Await, Stop and TeamStatus all read one state source: the background_tasks row.
 */

import { and, eq } from "drizzle-orm";
import { db } from "../db";
import { backgroundTasks, narrators } from "../db/schema";
import { narratorTraitsLock } from "../lib/async-mutex";
import { logger } from "../lib/logger";
import { parseTraits } from "../lib/narrator-utils";
import { backgroundOccupancyStore } from "./background-occupancy-store";
import { backgroundTaskService } from "./background-task-service";
import { registerAndPersistSubagentAlias } from "./subagent-alias";

export async function claimSubagentBackgroundRun(input: {
	parentNarratorId: string;
	subagent: { id: string; title: string | null; subagentType: string | null };
	originToolUseId: string;
}): Promise<{ alias: string }> {
	const subagentId = input.subagent.id;
	const title = input.subagent.title?.trim() || subagentId;
	const subagentType = input.subagent.subagentType?.trim() || "general";

	// Stable alias so the model can Await it later. `title` may itself be the id
	// (untitled subagent); the alias builder recognizes that and falls back to a
	// short id instead of slugifying the whole nanoid.
	const { alias } = await registerAndPersistSubagentAlias(
		input.parentNarratorId,
		subagentId,
		title,
	);
	// Durable background task row (idempotent restart when it exists).
	await backgroundTaskService.createAgentTask({
		id: subagentId,
		parentNarratorId: input.parentNarratorId,
		subagentNarratorId: subagentId,
		subagentType,
		toolUseId: input.originToolUseId,
		alias,
		title,
	});
	// Under narratorTraitsLock with a fresh read, because `traits` is a whole-column
	// JSON array: writing an earlier copy would drop any trait written since.
	await narratorTraitsLock.acquire(subagentId, async () => {
		const current = await db.query.narrators.findFirst({
			where: eq(narrators.id, subagentId),
			columns: { traits: true },
		});
		await db
			.update(narrators)
			.set({
				isBackground: true,
				backgroundStatus: "running",
				backgroundResult: null,
				backgroundCompletedAt: null,
				traits: [...new Set([...parseTraits(current?.traits), "background"])],
				updatedAt: new Date().toISOString(),
			})
			.where(eq(narrators.id, subagentId));
		backgroundOccupancyStore.refreshNarrator(subagentId);
	});
	return { alias };
}

/**
 * Undo the "running background task" claim after a continuation failed to take off.
 *
 * Leaving `backgroundStatus: "running"` behind would make the row look like live work:
 * `Await` would wait for a result that can never arrive, and a later recovery pass would
 * see a task that is already running and skip it.
 */
export async function clearSubagentBackgroundRunClaim(
	subagentId: string,
	parentNarratorId: string,
	reason: string,
): Promise<void> {
	// Creation can throw after INSERT (e.g. while broadcasting). Check ownership
	// before compensating: an id conflict with another task must leave it untouched.
	try {
		const { hasActiveSubagentResumeRun } = await import("./subagent-resume");
		// A manual resume may have won the per-subagent lock while this claim was
		// prepared. Never withdraw the claim of that live continuation.
		if (hasActiveSubagentResumeRun(subagentId)) return;
		const task = await db.query.backgroundTasks.findFirst({
			where: and(
				eq(backgroundTasks.id, subagentId),
				eq(backgroundTasks.type, "agent"),
				eq(backgroundTasks.parentNarratorId, parentNarratorId),
				eq(backgroundTasks.subagentNarratorId, subagentId),
			),
			columns: { id: true },
		});
		// Await reads background_tasks first: clearing only the narrator flag leaves
		// a durable running task with no runner and can hold an await batch open.
		if (task) await backgroundTaskService.markFailed(subagentId, reason);
	} catch (err) {
		logger.error("Failed to settle an unstarted background task", {
			subagentId,
			error: String(err),
		});
	}
	const now = new Date().toISOString();
	await db
		.update(narrators)
		.set({ backgroundStatus: "failed", backgroundCompletedAt: now, updatedAt: now })
		.where(and(eq(narrators.id, subagentId), eq(narrators.backgroundStatus, "running")))
		.then(() => backgroundOccupancyStore.refreshNarrator(subagentId))
		.catch((err) => {
			logger.error("Failed to clear the background claim of an unstarted subagent", {
				subagentId,
				error: err instanceof Error ? err.message : String(err),
			});
		});
}
