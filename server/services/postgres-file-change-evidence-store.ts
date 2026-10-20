import { withPgRetry } from "@server/db/pg-retry";
import {
	fileChangeBlobs,
	fileChangeEffects,
	fileChangeOperations,
	fileChangeScopes,
	fileHistoryClock,
	narratorToolCalls,
} from "@server/db/postgres-schema";
import { generateId } from "@server/lib/id";
import type {
	FileChangeExecutionOutcome,
	FileChangeSettlement,
} from "@shared/file-change-protocol";
import { and, asc, eq, isNull, type SQL, sql } from "drizzle-orm";
import type { BunSQLDatabase } from "drizzle-orm/bun-sql";
import {
	assertCurrentOperation,
	assertNoDispatchTool,
	type BeginFileChangeOperation,
	type BeginFileChangeOperationResult,
	evidenceInternals as E,
	type FileChangeEffectCursor,
	type FileChangeEffectRecord,
	type FileChangeNoDispatchProof,
	type FileChangeOperationCursor,
	type FileChangeOperationRecord,
	type FileChangeScopeRecord,
	type NoDispatchTool,
	type PrepareFileChangeScope,
	type RecordFileChangeEffect,
} from "./file-change-evidence";

type Tx = Parameters<Parameters<BunSQLDatabase["transaction"]>[0]>[0];

/** Async PostgreSQL counterpart. Transactions contain only bounded metadata SQL. */
export class PostgresFileChangeEvidenceStore {
	constructor(
		private readonly database: BunSQLDatabase,
		private readonly now: () => string = () => new Date().toISOString(),
	) {}

	private runSection<T>(work: (tx: Tx) => Promise<T>): Promise<T> {
		return withPgRetry(() => this.database.transaction((tx) => work(tx)));
	}

	async prepareScope(input: PrepareFileChangeScope): Promise<FileChangeScopeRecord> {
		const values = E.normalizeScope(input);
		return this.runSection(async (tx) => {
			const timestamp = this.now();
			// A unique scope insert handles concurrent first use without any workspace lock.
			await tx
				.insert(fileChangeScopes)
				.values({
					...values,
					id: input.id ?? generateId(),
					createdAt: timestamp,
					updatedAt: timestamp,
				})
				.onConflictDoNothing();
			const [existing] = await tx
				.select()
				.from(fileChangeScopes)
				.where(
					and(
						eq(fileChangeScopes.sourceInstanceId, values.sourceInstanceId),
						eq(fileChangeScopes.deviceId, values.deviceId),
						eq(fileChangeScopes.workspaceInstanceId, values.workspaceInstanceId),
					),
				)
				.limit(1);
			if (!existing) throw E.fail("IDENTITY_CONFLICT", "Historical scope ID is already in use");
			const record = existing as FileChangeScopeRecord;
			E.assertScopeMetadata(record, input, values);
			return E.boundedRecord(record);
		});
	}

	async beginOperation(input: BeginFileChangeOperation): Promise<BeginFileChangeOperationResult> {
		const values = E.normalizeOperation(input);
		return this.runSection(async (tx) => {
			// Serialize only sequence allocation. Recheck the request after this brief row lock.
			await lockJournalClock(tx);
			const [existing] = await tx
				.select()
				.from(fileChangeOperations)
				.where(operationIdentity(values))
				.limit(1)
				.then((rows) => rows as FileChangeOperationRecord[]);
			if (existing) {
				E.assertOperationReplay(existing, values);
				return { ...existing, replayed: true };
			}
			const timestamp = this.now();
			const [row] = await tx
				.insert(fileChangeOperations)
				.values({
					...values,
					id: generateId(),
					journalSeq: await nextJournalSeq(tx),
					startedAt: timestamp,
					updatedAt: timestamp,
				})
				.returning();
			return { ...E.boundedRecord(row as FileChangeOperationRecord), replayed: false };
		});
	}

	async beginNoDispatchOperation(
		input: BeginFileChangeOperation,
		proof: FileChangeNoDispatchProof,
	): Promise<FileChangeOperationRecord> {
		const { values, terminal } = E.normalizeNoDispatch(input, proof);
		return this.runSection(async (tx) => {
			const [tool] = await tx
				.select(noDispatchToolColumns)
				.from(narratorToolCalls)
				.where(eq(narratorToolCalls.id, values.sourceId))
				.limit(1)
				.for("update")
				.then((rows) => rows as NoDispatchTool[]);
			assertNoDispatchTool(tool, input, proof);
			const [existing] = await tx
				.select()
				.from(fileChangeOperations)
				.where(operationIdentity(values))
				.limit(1)
				.then((rows) => rows as FileChangeOperationRecord[]);
			if (existing) {
				E.assertNoDispatchReplay(existing, tool, values, terminal);
				const [effect] = await tx
					.select({ id: fileChangeEffects.id })
					.from(fileChangeEffects)
					.where(eq(fileChangeEffects.operationId, existing.id))
					.limit(1);
				if (effect) throw E.fail("REQUEST_CONFLICT", "No-dispatch history cannot contain effects");
				return existing;
			}
			E.assertNoDispatchLive(tool, proof);
			await lockJournalClock(tx);
			const timestamp = this.now();
			const [recorded] = await tx
				.insert(fileChangeOperations)
				.values({
					...values,
					...terminal,
					id: generateId(),
					journalSeq: await nextJournalSeq(tx),
					startedAt: timestamp,
					finishedAt: timestamp,
					updatedAt: timestamp,
				})
				.returning();
			E.boundedRecord(recorded);
			const [linked] = await tx
				.update(narratorToolCalls)
				.set({
					fileChangeOperationId: recorded.id,
					...(proof.beforeInvocation ? { status: "fail" as const } : {}),
				})
				.where(
					and(
						eq(narratorToolCalls.id, tool.id),
						eq(narratorToolCalls.executionAttempt, values.attempt),
						eq(narratorToolCalls.status, tool.status),
						tool.startedAt === null
							? isNull(narratorToolCalls.executionStartedAt)
							: eq(narratorToolCalls.executionStartedAt, tool.startedAt),
						tool.deviceId === null
							? isNull(narratorToolCalls.executionDeviceId)
							: eq(narratorToolCalls.executionDeviceId, tool.deviceId),
						tool.generation === null
							? isNull(narratorToolCalls.runtimeGeneration)
							: eq(narratorToolCalls.runtimeGeneration, tool.generation),
						isNull(narratorToolCalls.fileChangeOperationId),
					),
				)
				.returning({ id: narratorToolCalls.id });
			if (!linked)
				throw E.fail("REQUEST_CONFLICT", "Tool attempt was already linked to other history");
			return recorded as FileChangeOperationRecord;
		});
	}

	async recordEffect(input: RecordFileChangeEffect): Promise<FileChangeEffectRecord> {
		const normalized = E.normalizeEffect(input);
		return this.runSection(async (tx) => {
			const operation = await requireOperation(tx, input.operationId);
			assertCurrentOperation(operation);
			const values = E.effectValues(operation, normalized);
			const [existing] = await tx
				.select()
				.from(fileChangeEffects)
				.where(eq(fileChangeEffects.mutationId, values.mutationId))
				.limit(1)
				.then((rows) => rows as FileChangeEffectRecord[]);
			if (existing) {
				E.assertEffectReplay(existing, values);
				return existing;
			}
			if (operation.preparedEffectCount >= operation.expectedEffectCount)
				throw E.fail("BUDGET_EXCEEDED", "Operation already has its expected file results");
			const [scope] = await tx
				.select()
				.from(fileChangeScopes)
				.where(eq(fileChangeScopes.id, input.identity.scopeId))
				.limit(1);
			E.assertEffectScope(
				scope as FileChangeScopeRecord | undefined,
				operation,
				normalized.identity,
			);
			for (const state of [normalized.before, normalized.intendedAfter, normalized.observedAfter]) {
				const ref = E.stateBlob(state);
				if (!ref) continue;
				const [blob] = await tx
					.select({ sizeBytes: fileChangeBlobs.sizeBytes, status: fileChangeBlobs.status })
					.from(fileChangeBlobs)
					.where(eq(fileChangeBlobs.digest, ref.digest))
					.limit(1);
				E.assertBlobMetadata(blob, ref);
			}
			const timestamp = this.now();
			const [effect] = await tx
				.insert(fileChangeEffects)
				.values({
					...values,
					id: generateId(),
					createdAt: timestamp,
					updatedAt: timestamp,
				})
				.returning();
			E.boundedRecord(effect);
			const counts = E.recordedCounts(operation, normalized);
			await tx
				.update(fileChangeOperations)
				.set({
					...counts,
					...E.operationAggregate(
						{ ...operation, ...counts },
						await outcomeFlags(tx, operation.id),
					),
					updatedAt: timestamp,
				})
				.where(eq(fileChangeOperations.id, operation.id));
			return effect as FileChangeEffectRecord;
		});
	}

	async finishOperation(
		operationId: string,
		outcome: Exclude<FileChangeExecutionOutcome, "running">,
	): Promise<FileChangeOperationRecord> {
		E.assertString(operationId, "operationId");
		E.assertTerminalOutcome(outcome);
		return this.runSection(async (tx) => {
			const operation = await requireOperation(tx, operationId);
			assertCurrentOperation(operation);
			if (operation.executionOutcome !== "running") {
				if (operation.executionOutcome !== outcome)
					throw E.fail("REQUEST_CONFLICT", "A terminal execution result is immutable");
				return operation;
			}
			const [row] = await tx
				.update(fileChangeOperations)
				.set({
					...E.operationAggregate(
						{ ...operation, executionOutcome: outcome },
						await outcomeFlags(tx, operationId),
					),
					executionOutcome: outcome,
					finishedAt: this.now(),
					updatedAt: this.now(),
				})
				.where(eq(fileChangeOperations.id, operationId))
				.returning();
			return E.boundedRecord(row as FileChangeOperationRecord);
		});
	}

	async getScope(scopeId: string): Promise<FileChangeScopeRecord | null> {
		E.assertString(scopeId, "scopeId");
		const [row] = await this.database
			.select()
			.from(fileChangeScopes)
			.where(eq(fileChangeScopes.id, scopeId))
			.limit(1)
			.then((rows) => rows as FileChangeScopeRecord[]);
		return row ?? null;
	}
	async getOperation(operationId: string): Promise<FileChangeOperationRecord | null> {
		E.assertString(operationId, "operationId");
		const [row] = await this.database
			.select()
			.from(fileChangeOperations)
			.where(eq(fileChangeOperations.id, operationId))
			.limit(1)
			.then((rows) => rows as FileChangeOperationRecord[]);
		return row ?? null;
	}
	async getEffect(operationId: string, mutationId: string): Promise<FileChangeEffectRecord | null> {
		E.assertString(operationId, "operationId");
		E.assertString(mutationId, "mutationId");
		const [row] = await this.database
			.select()
			.from(fileChangeEffects)
			.where(
				and(
					eq(fileChangeEffects.operationId, operationId),
					eq(fileChangeEffects.mutationId, mutationId),
				),
			)
			.limit(1)
			.then((rows) => rows as FileChangeEffectRecord[]);
		return row ?? null;
	}
	async listEffects(
		operationId: string,
		options: { cursor?: FileChangeEffectCursor; limit?: number } = {},
	) {
		E.assertString(operationId, "operationId");
		const limit = E.pageLimit(options.limit);
		E.validateEffectCursor(options.cursor);
		const c = options.cursor;
		const rows = await this.database
			.select()
			.from(fileChangeEffects)
			.where(
				and(
					eq(fileChangeEffects.operationId, operationId),
					c
						? sql`(${fileChangeEffects.fileKey}, ${fileChangeEffects.phase}) > (${c.fileKey}, ${c.phase})`
						: undefined,
				),
			)
			.orderBy(asc(fileChangeEffects.fileKey), asc(fileChangeEffects.phase))
			.limit(limit + 1);
		return E.boundedPage(rows as FileChangeEffectRecord[], limit, E.effectCursor);
	}
	async listPendingOperations(options: {
		settlement: Exclude<FileChangeSettlement, "settled">;
		cursor?: FileChangeOperationCursor;
		limit?: number;
	}) {
		E.validatePendingPage(options);
		const limit = E.pageLimit(options.limit);
		const c = options.cursor;
		const rows = await this.database
			.select({
				id: fileChangeOperations.id,
				sourceInstanceId: fileChangeOperations.sourceInstanceId,
				sourceKind: fileChangeOperations.sourceKind,
				sourceId: fileChangeOperations.sourceId,
				attempt: fileChangeOperations.attempt,
				requestDigest: fileChangeOperations.requestDigest,
				projectId: fileChangeOperations.projectId,
				narratorId: fileChangeOperations.narratorId,
				ownerUserId: fileChangeOperations.ownerUserId,
				expectedEffectCount: fileChangeOperations.expectedEffectCount,
				preparedEffectCount: fileChangeOperations.preparedEffectCount,
				settledEffectCount: fileChangeOperations.settledEffectCount,
				unresolvedEffectCount: fileChangeOperations.unresolvedEffectCount,
				evidenceBytes: fileChangeOperations.evidenceBytes,
				settlement: fileChangeOperations.settlement,
				executionOutcome: fileChangeOperations.executionOutcome,
				effectOutcome: fileChangeOperations.effectOutcome,
				coverage: fileChangeOperations.coverage,
				attributionGrade: fileChangeOperations.attributionGrade,
				reason: fileChangeOperations.reason,
				updatedAt: fileChangeOperations.updatedAt,
			})
			.from(fileChangeOperations)
			.where(
				and(
					eq(fileChangeOperations.settlement, options.settlement),
					c
						? sql`(${fileChangeOperations.updatedAt}, ${fileChangeOperations.id}) > (${c.updatedAt}, ${c.id})`
						: undefined,
				),
			)
			.orderBy(asc(fileChangeOperations.updatedAt), asc(fileChangeOperations.id))
			.limit(limit + 1);
		return E.boundedPage(rows, limit, (row) => ({ updatedAt: row.updatedAt, id: row.id }));
	}
}

const noDispatchToolColumns = {
	id: narratorToolCalls.id,
	narratorId: narratorToolCalls.narratorId,
	toolUseId: narratorToolCalls.toolUseId,
	toolName: narratorToolCalls.toolName,
	version: narratorToolCalls.executionIdentityVersion,
	origin: narratorToolCalls.executionOriginToolCallId,
	checkpoint: narratorToolCalls.isFileHistoryCheckpoint,
	attempt: narratorToolCalls.executionAttempt,
	status: narratorToolCalls.status,
	startedAt: narratorToolCalls.executionStartedAt,
	deviceId: narratorToolCalls.executionDeviceId,
	generation: narratorToolCalls.runtimeGeneration,
	cwd: narratorToolCalls.executionCwd,
	pathFlavor: narratorToolCalls.executionPathFlavor,
	lexicalPath: narratorToolCalls.resolvedFilePath,
	canonicalPath: narratorToolCalls.canonicalFilePath,
	operationId: narratorToolCalls.fileChangeOperationId,
};
function operationIdentity(input: ReturnType<typeof E.normalizeOperation>): SQL {
	return and(
		eq(fileChangeOperations.sourceInstanceId, input.sourceInstanceId),
		eq(fileChangeOperations.sourceKind, input.sourceKind),
		eq(fileChangeOperations.sourceId, input.sourceId),
		eq(fileChangeOperations.attempt, input.attempt),
	) as SQL;
}
async function lockJournalClock(tx: Tx) {
	await tx.insert(fileHistoryClock).values({ id: 1, lastSeq: 0 }).onConflictDoNothing();
	await tx
		.select({ lastSeq: fileHistoryClock.lastSeq })
		.from(fileHistoryClock)
		.where(eq(fileHistoryClock.id, 1))
		.limit(1)
		.for("update");
}
async function nextJournalSeq(tx: Tx): Promise<number> {
	const [clock] = await tx
		.update(fileHistoryClock)
		.set({ lastSeq: sql`${fileHistoryClock.lastSeq} + 1` })
		.where(eq(fileHistoryClock.id, 1))
		.returning({ lastSeq: fileHistoryClock.lastSeq });
	if (!clock) throw E.fail("JOURNAL_UNAVAILABLE", "File history clock is unavailable");
	return clock.lastSeq;
}
async function requireOperation(tx: Tx, id: string): Promise<FileChangeOperationRecord> {
	const [row] = await tx
		.select()
		.from(fileChangeOperations)
		.where(eq(fileChangeOperations.id, id))
		.limit(1)
		.for("update");
	if (!row) throw E.fail("NOT_FOUND", "File-change operation not found");
	return row as FileChangeOperationRecord;
}
async function outcomeFlags(tx: Tx, operationId: string) {
	const exists = async (condition: SQL) => {
		const [row] = await tx
			.select({ id: fileChangeEffects.id })
			.from(fileChangeEffects)
			.where(and(eq(fileChangeEffects.operationId, operationId), condition))
			.limit(1);
		return row !== undefined;
	};
	return {
		changed: await exists(eq(fileChangeEffects.outcome, "changed")),
		unknown: await exists(eq(fileChangeEffects.outcome, "unknown")),
		unmeasured: await exists(eq(fileChangeEffects.attributionGrade, "unknown")),
	};
}
export function createPostgresFileChangeEvidenceStore(
	database: BunSQLDatabase,
	now?: () => string,
): PostgresFileChangeEvidenceStore {
	return new PostgresFileChangeEvidenceStore(database, now);
}
