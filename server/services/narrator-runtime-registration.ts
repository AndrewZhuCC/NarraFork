/**
 * Runtime registration between narrators: a plain, bounded record of who a narrator
 * takes its capability / model source from, who may control it and what its current
 * run is bound to. None of it depends on a parent/child relationship, so two
 * unrelated narrators can be related the same way a delegation is.
 *
 * Internal service only: no route exposes these writers, and model input never
 * reaches them.
 *
 * Policy source: stored in the indexed `policy_source_narrator_id` column, so its
 * dependents are an index lookup. `narratorService.remove` deletes delegated children
 * and freezes every other dependent's effective ceiling before the source goes.
 */
import { eq } from "drizzle-orm";
import { db } from "../db";
import { narrators } from "../db/schema";
import { NotFoundError, ValidationError } from "../lib/errors";
import {
	MAX_POLICY_SOURCE_DEPTH,
	policySourceIdOf,
	RUNTIME_SOURCE_COLUMNS,
	type RuntimeSourceRow,
} from "../lib/runtime-source";
import {
	parseRuntimeRegistration,
	type RuntimeRegistration,
	readRuntimeRegistrationColumn,
	registerRun,
} from "../lib/validators/runtime-registration";

// === Registration ===

export async function getRuntimeRegistration(
	narratorId: string,
): Promise<RuntimeRegistration | null> {
	const row = await db.query.narrators.findFirst({
		where: eq(narrators.id, narratorId),
		columns: { runtimeRegistrationJson: true },
	});
	if (!row) throw new NotFoundError("Narrator", narratorId);
	return readRuntimeRegistrationColumn(row.runtimeRegistrationJson);
}

/** Refuse a policy source that is missing, the narrator itself, or would close a cycle. */
async function assertPolicySource(narratorId: string, sourceId: string): Promise<void> {
	let current: string | null = sourceId;
	for (let depth = 0; current; depth++) {
		if (current === narratorId) throw new ValidationError("Policy source would form a cycle");
		if (depth >= MAX_POLICY_SOURCE_DEPTH) throw new ValidationError("Policy source chain too deep");
		const row: RuntimeSourceRow | undefined = await db.query.narrators.findFirst({
			where: eq(narrators.id, current),
			columns: RUNTIME_SOURCE_COLUMNS,
		});
		if (!row) throw new NotFoundError("Narrator", current);
		current = policySourceIdOf(row);
	}
}

/**
 * Register (or clear, with `null`) the narrator whose ceiling, execution rules,
 * permission-mode updates and followed model this narrator takes. Any narrator may be
 * a source. Server-side callers decide; model input never reaches this.
 */
export async function setPolicySource(narratorId: string, sourceId: string | null): Promise<void> {
	if (sourceId) await assertPolicySource(narratorId, sourceId);
	const updated = await db
		.update(narrators)
		.set({ policySourceNarratorId: sourceId, updatedAt: new Date().toISOString() })
		.where(eq(narrators.id, narratorId))
		.returning({ id: narrators.id });
	if (updated.length === 0) throw new NotFoundError("Narrator", narratorId);
}

/**
 * Replace a narrator's registration. The whole value is validated and size-checked;
 * `null` clears it. Server-side callers decide what to register — nothing here
 * accepts a registration from model input.
 */
export async function setRuntimeRegistration(
	narratorId: string,
	registration: RuntimeRegistration | null,
): Promise<RuntimeRegistration | null> {
	const next = registration ? parseRuntimeRegistration(registration) : null;
	const updated = await db
		.update(narrators)
		.set({ runtimeRegistrationJson: next, updatedAt: new Date().toISOString() })
		.where(eq(narrators.id, narratorId))
		.returning({ id: narrators.id });
	if (updated.length === 0) throw new NotFoundError("Narrator", narratorId);
	return next;
}

/** A human input changes the identity of the same run, not its deadline/relations. */
export async function registerRunPrincipal(
	narratorId: string,
	logicalRunId: string,
	userId: string,
): Promise<void> {
	const source = await db.query.narrators.findFirst({
		where: eq(narrators.id, narratorId),
		columns: { runtimeRegistrationJson: true },
	});
	if (!source) throw new NotFoundError("Narrator", narratorId);
	const registration = readRuntimeRegistrationColumn(source.runtimeRegistrationJson);
	if (registration?.run?.logicalRunId !== logicalRunId) return;
	await db
		.update(narrators)
		.set({
			runtimeRegistrationJson: registerRun(
				registration,
				logicalRunId,
				{ principal: { userId } },
				undefined,
			),
		})
		.where(eq(narrators.id, narratorId));
}
