/**
 * Retire runtime rows chapters left behind.
 *
 * Containers and port allocations used to belong to a chapter. Chapters are gone, and a
 * row owned by neither a chapter nor a worktree resource has nothing that will ever stop
 * or release it: the container keeps running and the port stays taken. This runs once in
 * the background at startup, stops those containers and deletes their rows.
 *
 * Per-user terminal layouts and volume-snapshot applications that pointed at a chapter
 * are unreachable for the same reason (every reader keys them by narrator or worktree
 * resource), so their rows go too.
 *
 * Bounded per run (`BATCH`) so a large backlog cannot pin startup; whatever is left is
 * picked up on the next start. Terminals are left alone: an ownerless terminal is the
 * same shape as a recovered orphan, is handled by the terminal service and can be ended
 * from the admin terminal page — killing a process the user may still want is not this
 * cleanup's call.
 */
import { and, inArray, isNotNull, isNull } from "drizzle-orm";
import { db } from "../db";
import {
	containerInstances,
	portAllocations,
	terminalViewState,
	volumeSnapshotApplications,
} from "../db/schema";
import { logger } from "../lib/logger";
import { supportsContainers } from "../lib/platform";
import { getPodmanStatus, stopContainers } from "./container-service";

const BATCH = 200;

export async function retireLegacyChapterResources(): Promise<void> {
	const rows = await db
		.select({ id: containerInstances.id, containerId: containerInstances.containerId })
		.from(containerInstances)
		.where(isNull(containerInstances.worktreeResourceId))
		.limit(BATCH);
	if (rows.length > 0) {
		const running = rows.flatMap((row) => (row.containerId ? [row.containerId] : []));
		let stopped = 0;
		if (running.length > 0 && supportsContainers() && (await getPodmanStatus()).installed) {
			stopped = await stopContainers(running);
		}
		await db.delete(containerInstances).where(
			inArray(
				containerInstances.id,
				rows.map((row) => row.id),
			),
		);
		logger.info("Retired containers left by removed chapters", {
			rows: rows.length,
			stopped,
		});
	}

	const ports = await db
		.delete(portAllocations)
		.where(and(isNull(portAllocations.worktreeResourceId), isNotNull(portAllocations.port)))
		.returning({ port: portAllocations.port });
	if (ports.length > 0) {
		logger.info("Released ports held by removed chapters", { count: ports.length });
	}

	const views = await db
		.select({ id: terminalViewState.id })
		.from(terminalViewState)
		.where(and(isNull(terminalViewState.narratorId), isNull(terminalViewState.worktreeResourceId)))
		.limit(BATCH);
	if (views.length > 0) {
		await db.delete(terminalViewState).where(
			inArray(
				terminalViewState.id,
				views.map((row) => row.id),
			),
		);
	}

	const applications = await db
		.select({ id: volumeSnapshotApplications.id })
		.from(volumeSnapshotApplications)
		.where(isNull(volumeSnapshotApplications.targetWorktreeResourceId))
		.limit(BATCH);
	if (applications.length > 0) {
		await db.delete(volumeSnapshotApplications).where(
			inArray(
				volumeSnapshotApplications.id,
				applications.map((row) => row.id),
			),
		);
	}
	if (views.length > 0 || applications.length > 0) {
		logger.info("Removed records that pointed at removed chapters", {
			terminalLayouts: views.length,
			snapshotApplications: applications.length,
		});
	}
}
