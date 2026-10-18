/**
 * Runtime registration and event subscriptions between narrators.
 *
 * Both are plain, bounded registrations: who a narrator takes its capability /
 * model source from, who may control it, what its current run is bound to, and who
 * receives its terminal events. None of it depends on a parent/child relationship,
 * so two unrelated narrators can be related the same way a delegation is.
 *
 * Delivery is not implemented here; the publication outbox consumes subscriptions.
 *
 * Internal service only: no route exposes these writers, and model input never
 * reaches them.
 *
 * Policy source: stored in the indexed `policy_source_narrator_id` column with a
 * plain FK, so a source cannot be deleted while a dependent still names it and its
 * dependents are an index lookup. `narratorService.remove` deletes delegated children
 * first; any other dependent must be re-sourced (or cleared) before the source goes.
 */
import { and, asc, count, eq, gt, isNull, or } from "drizzle-orm";
import { db } from "../db";
import { narratorEventSubscriptions, narrators } from "../db/schema";
import { NotFoundError, ValidationError } from "../lib/errors";
import { generateId } from "../lib/id";
import {
	MAX_POLICY_SOURCE_DEPTH,
	policySourceIdOf,
	RUNTIME_SOURCE_COLUMNS,
	type RuntimeSourceRow,
} from "../lib/runtime-source";
import {
	MAX_SUBSCRIPTIONS_PER_SOURCE,
	type NarratorEventSubscriptionInput,
	narratorEventSubscriptionInputSchema,
	parseRuntimeRegistration,
	type RuntimeRegistration,
	readRuntimeRegistrationColumn,
	registerRun,
} from "../lib/validators/runtime-registration";
import { canReadNarrator, canWriteNarrator, type NarratorPrincipal } from "./narrator-acl";

export type NarratorEventSubscription = typeof narratorEventSubscriptions.$inferSelect;

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

// === Subscriptions ===

/**
 * Authorization seam for subscriptions: a human principal must be able to read the
 * source and drive the recipient. Server-originated registrations (a delegation the
 * server itself set up) pass no principal. Subscribing grants nothing else — not
 * Send, Stop, model changes or history access.
 */
export async function assertCanSubscribe(
	sourceId: string,
	recipientId: string,
	principal: NarratorPrincipal | null,
): Promise<void> {
	const rows = await db.query.narrators.findMany({
		where: (table, { inArray }) => inArray(table.id, [sourceId, recipientId]),
		columns: {
			id: true,
			ownerUserId: true,
			visibility: true,
			writeAudience: true,
			type: true,
			aclRootNarratorId: true,
			chapterId: true,
			contextProjectId: true,
		},
	});
	const source = rows.find((row) => row.id === sourceId);
	const recipient = rows.find((row) => row.id === recipientId);
	if (!source) throw new NotFoundError("Narrator", sourceId);
	if (!recipient) throw new NotFoundError("Narrator", recipientId);
	if (!principal) return;
	if (!(await canReadNarrator(source, principal))) throw new NotFoundError("Narrator", sourceId);
	if (!(await canWriteNarrator(recipient, principal)))
		throw new NotFoundError("Narrator", recipientId);
}

function scopeWhere(sourceId: string, recipientId: string, runId: string | null) {
	return and(
		eq(narratorEventSubscriptions.sourceNarratorId, sourceId),
		eq(narratorEventSubscriptions.recipientNarratorId, recipientId),
		runId === null
			? isNull(narratorEventSubscriptions.runId)
			: eq(narratorEventSubscriptions.runId, runId),
	);
}

/**
 * Register (or update in place) the subscription for one source/recipient/scope.
 * At most {@link MAX_SUBSCRIPTIONS_PER_SOURCE} subscriptions exist per source;
 * beyond that registration is refused rather than evicting anything.
 */
export async function upsertNarratorEventSubscription(
	input: NarratorEventSubscriptionInput,
	principal: NarratorPrincipal | null = null,
): Promise<NarratorEventSubscription> {
	const parsed = narratorEventSubscriptionInputSchema.parse(input);
	const runId = parsed.runId ?? null;
	if (parsed.sourceNarratorId === parsed.recipientNarratorId)
		throw new ValidationError("A narrator cannot subscribe to itself");
	await assertCanSubscribe(parsed.sourceNarratorId, parsed.recipientNarratorId, principal);
	const now = new Date().toISOString();
	return db.transaction((tx) => {
		const where = scopeWhere(parsed.sourceNarratorId, parsed.recipientNarratorId, runId);
		const existing = tx.select().from(narratorEventSubscriptions).where(where).get();
		if (existing) {
			return tx
				.update(narratorEventSubscriptions)
				.set({
					events: parsed.events,
					wake: parsed.wake,
					...(principal && { authorizedByUserId: principal.userId }),
					updatedAt: now,
				})
				.where(eq(narratorEventSubscriptions.id, existing.id))
				.returning()
				.get();
		}
		const total =
			tx
				.select({ n: count() })
				.from(narratorEventSubscriptions)
				.where(eq(narratorEventSubscriptions.sourceNarratorId, parsed.sourceNarratorId))
				.get()?.n ?? 0;
		if (total >= MAX_SUBSCRIPTIONS_PER_SOURCE)
			throw new ValidationError(
				`A narrator may have at most ${MAX_SUBSCRIPTIONS_PER_SOURCE} event subscriptions`,
			);
		return tx
			.insert(narratorEventSubscriptions)
			.values({
				id: generateId(),
				sourceNarratorId: parsed.sourceNarratorId,
				recipientNarratorId: parsed.recipientNarratorId,
				runId,
				events: parsed.events,
				wake: parsed.wake,
				origin: parsed.origin,
				// A human registration is attributed to that human, never to a caller-chosen id.
				authorizedByUserId: principal ? principal.userId : (parsed.authorizedByUserId ?? null),
				createdAt: now,
				updatedAt: now,
			})
			.returning()
			.get();
	});
}

/** Remove one subscription. Returns whether it existed. */
export async function deleteNarratorEventSubscription(id: string): Promise<boolean> {
	const removed = await db
		.delete(narratorEventSubscriptions)
		.where(eq(narratorEventSubscriptions.id, id))
		.returning({ id: narratorEventSubscriptions.id });
	return removed.length > 0;
}

/**
 * Subscriptions that apply to one run of a source: the session-wide ones plus those
 * scoped to that run. Bounded by the per-source cap.
 */
export async function listSubscriptionsForSourceRun(
	sourceId: string,
	runId: string | null,
): Promise<NarratorEventSubscription[]> {
	const scope = isNull(narratorEventSubscriptions.runId);
	return db
		.select()
		.from(narratorEventSubscriptions)
		.where(
			and(
				eq(narratorEventSubscriptions.sourceNarratorId, sourceId),
				runId === null ? scope : or(scope, eq(narratorEventSubscriptions.runId, runId)),
			),
		)
		.orderBy(asc(narratorEventSubscriptions.id))
		.limit(MAX_SUBSCRIPTIONS_PER_SOURCE);
}

/** Cursor page of a recipient's subscriptions, ordered by id. */
export async function listSubscriptionsForRecipient(
	recipientId: string,
	opts: { cursor?: string; limit?: number } = {},
): Promise<{ items: NarratorEventSubscription[]; nextCursor: string | null }> {
	const limit = Math.min(Math.max(opts.limit ?? 50, 1), 200);
	const rows = await db
		.select()
		.from(narratorEventSubscriptions)
		.where(
			and(
				eq(narratorEventSubscriptions.recipientNarratorId, recipientId),
				opts.cursor ? gt(narratorEventSubscriptions.id, opts.cursor) : undefined,
			),
		)
		.orderBy(asc(narratorEventSubscriptions.id))
		.limit(limit + 1);
	const items = rows.slice(0, limit);
	return { items, nextCursor: rows.length > limit ? (items.at(-1)?.id ?? null) : null };
}
