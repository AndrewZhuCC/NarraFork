import { setImmediate as yieldToEventLoop } from "node:timers/promises";
import { withPgRetry } from "@server/db/pg-retry";
import {
	fileChangeBlobs,
	revertOperationFiles as files,
	revertOperations as operations,
} from "@server/db/postgres-schema";
import { FILE_CHANGE_LIMITS } from "@shared/file-change-protocol";
import { and, asc, eq, sql } from "drizzle-orm";
import type { BunSQLDatabase } from "drizzle-orm/bun-sql";
import {
	assertNewPlan,
	pageLimit,
	type RevertFileResult,
	type RevertJournalContext,
	type RevertJournalFile,
	type RevertJournalOperation,
	type RevertJournalOptions,
	RevertMutationJournalError,
	resultHistoryStatus,
	timestamp,
	validateResult,
} from "./revert-mutation-journal";
import type { RevertPlanManifestProof } from "./revert-plan-service";

type Tx = Parameters<Parameters<BunSQLDatabase["transaction"]>[0]>[0];
export interface PostgresRevertJournalHistoryCommit {
	db: BunSQLDatabase;
	apply(tx: Tx): Promise<void>;
}
/** PostgreSQL uses short async SQL transactions; file IO never enters a retry callback. */
export class PostgresRevertMutationJournal {
	private readonly now: () => string;
	constructor(
		private readonly database: BunSQLDatabase,
		options: RevertJournalOptions,
		_plans?: unknown,
	) {
		this.now = options.now ?? (() => new Date().toISOString());
	}
	private runSection<T>(body: (tx: Tx) => Promise<T>): Promise<T> {
		return withPgRetry(() => this.database.transaction((tx) => body(tx)));
	}
	private async operation(
		tx: Tx | BunSQLDatabase,
		ctx: RevertJournalContext,
		lock = false,
	): Promise<RevertJournalOperation> {
		const query = tx
			.select()
			.from(operations)
			.where(
				and(
					eq(operations.id, ctx.planId),
					eq(operations.requestedBySubjectKey, ctx.owner.subjectKey),
					sql`${operations.narratorId} IS NOT DISTINCT FROM ${ctx.owner.narratorId}`,
					sql`${operations.projectId} IS NOT DISTINCT FROM ${ctx.owner.projectId}`,
				),
			)
			.limit(1);
		const rows = lock ? await query.for("update") : await query;
		const row = rows[0];
		if (!row) throw fail("NOT_FOUND", "Plan not found in the authorized context");
		if (row.planHash !== ctx.planHash) throw fail("REQUEST_CONFLICT", "Confirmed plan changed");
		return row as RevertJournalOperation;
	}
	private async file(tx: Tx, operationId: string, fileId: string): Promise<RevertJournalFile> {
		const [row] = await tx
			.select()
			.from(files)
			.where(and(eq(files.id, fileId), eq(files.revertOperationId, operationId)))
			.limit(1);
		if (!row) throw fail("NOT_FOUND", "File is outside the fixed plan");
		return row as RevertJournalFile;
	}
	private async update(
		tx: Tx,
		operation: RevertJournalOperation,
		patch: Partial<RevertJournalOperation>,
	): Promise<RevertJournalOperation> {
		const [row] = await tx
			.update(operations)
			.set({ ...patch, updatedAt: timestamp(this.now(), operation.updatedAt) })
			.where(eq(operations.id, operation.id))
			.returning();
		return row as RevertJournalOperation;
	}
	getOperation(ctx: RevertJournalContext) {
		return this.operation(this.database, ctx);
	}
	async listFiles(ctx: RevertJournalContext, options: { cursor?: string; limit?: number } = {}) {
		const operation = await this.getOperation(ctx);
		const limit = pageLimit(options.limit);
		const rows = await this.database
			.select()
			.from(files)
			.where(
				and(
					eq(files.revertOperationId, operation.id),
					options.cursor ? sql`${files.fileKey} > ${options.cursor}` : undefined,
				),
			)
			.orderBy(asc(files.fileKey))
			.limit(limit + 1);
		const items = rows.slice(0, limit);
		return {
			items,
			hasMore: rows.length > limit,
			nextCursor: rows.length > limit ? items[items.length - 1].fileKey : null,
		};
	}
	async startExecution(
		ctx: RevertJournalContext,
		_proof?: RevertPlanManifestProof,
		options: { signal?: AbortSignal } = {},
	) {
		options.signal?.throwIfAborted();
		return this.runSection(async (tx) => {
			const operation = await this.operation(tx, ctx, true);
			if (operation.status !== "prepared") return { operation, started: false };
			assertNewPlan(operation);
			if (Date.parse(operation.expiresAt) <= Date.parse(this.now()))
				throw fail("EXPIRED", "Preview expired");
			return {
				operation: await this.update(tx, operation, { status: "applying", reason: null }),
				started: true,
			};
		});
	}
	startFile(ctx: RevertJournalContext, fileId: string) {
		return this.runSection(async (tx) => {
			const operation = await this.operation(tx, ctx, true);
			assertNewPlan(operation);
			if (operation.status !== "applying") return false;
			const file = await this.file(tx, operation.id, fileId);
			if (file.status !== "prepared") return false;
			await tx
				.update(files)
				.set({
					status: "applying",
					receiptJson: { version: 3, status: "started" },
					updatedAt: this.now(),
				})
				.where(eq(files.id, fileId));
			await this.update(tx, operation, {});
			return true;
		});
	}
	recordResult(ctx: RevertJournalContext, fileId: string, result: RevertFileResult) {
		validateResult(result);
		return this.runSection(async (tx) => {
			const operation = await this.operation(tx, ctx, true);
			assertNewPlan(operation);
			const file = await this.file(tx, operation.id, fileId);
			if (file.status !== "prepared" && file.status !== "applying") return file;
			if (operation.status !== "applying")
				throw fail("INVALID_TRANSITION", "Execution already finished");
			const after = result.observedAfter;
			const before =
				result.status === "applied" && file.beforeBlobDigest
					? (
							await tx
								.select({
									digest: fileChangeBlobs.digest,
									sizeBytes: fileChangeBlobs.sizeBytes,
									status: fileChangeBlobs.status,
								})
								.from(fileChangeBlobs)
								.where(eq(fileChangeBlobs.digest, file.beforeBlobDigest))
								.limit(1)
						)[0]
					: null;
			const [updated] = await tx
				.update(files)
				.set({
					status: result.status,
					reason: result.reason,
					receiptJson: {
						version: 3,
						status: result.status,
						historyStatus: resultHistoryStatus(file, result, before),
						reason: result.reason,
					},
					observedAfterStateJson: after ?? null,
					observedAfterBlobDigest:
						result.status === "applied" && after?.kind === "regular" ? after.blob.digest : null,
					updatedAt: this.now(),
				})
				.where(eq(files.id, fileId))
				.returning();
			await this.update(tx, operation, {
				appliedFileCount: operation.appliedFileCount + (result.status === "applied" ? 1 : 0),
			});
			return updated;
		});
	}
	async finishFiles(
		ctx: RevertJournalContext,
		options: { cancelled?: boolean; reason?: string | null } = {},
	) {
		const original = await this.getOperation(ctx);
		if (original.status !== "applying") return original;
		let cursor: string | undefined;
		let count = 0;
		let successful = 0;
		let touched = false;
		do {
			const page = await this.listFiles(ctx, { cursor, limit: 32 });
			for (const file of page.items) {
				count++;
				if (file.status === "applied" || file.status === "unchanged") successful++;
				if (["applied", "unknown", "applying"].includes(file.status)) touched = true;
			}
			if (count > FILE_CHANGE_LIMITS.revertFiles) throw fail("BUDGET_EXCEEDED", "Too many files");
			cursor = page.nextCursor ?? undefined;
			if (cursor) await yieldToEventLoop();
		} while (cursor);
		return this.runSection(async (tx) => {
			const operation = await this.operation(tx, ctx, true);
			if (operation.updatedAt !== original.updatedAt) throw fail("STALE", "File results changed");
			if (count !== operation.fileCount) throw fail("INCOMPLETE_FILES", "Fixed file set changed");
			const status = !operation.coverageComplete
				? "partial"
				: successful === count
					? "files_verified"
					: touched || successful > 0
						? "partial"
						: options.cancelled
							? "cancelled"
							: "failed";
			return this.update(tx, operation, {
				status,
				reason:
					options.reason ?? (operation.coverageComplete ? null : "SELECTED_HISTORY_UNAVAILABLE"),
			});
		});
	}
	failHistory(ctx: RevertJournalContext, reason: string) {
		return this.runSection(async (tx) => {
			const operation = await this.operation(tx, ctx, true);
			if (operation.status !== "files_verified") return operation;
			return this.update(tx, operation, {
				status: operation.fileCount ? "partial" : "failed",
				reason: reason.slice(0, 256),
			});
		});
	}
	commit(ctx: RevertJournalContext, history: PostgresRevertJournalHistoryCommit) {
		if (history.db !== this.database)
			throw fail("DURABILITY_BOUNDARY", "History must use the same database");
		return this.runSection(async (tx) => {
			const operation = await this.operation(tx, ctx, true);
			if (operation.status === "committed") return operation;
			assertNewPlan(operation);
			if (operation.status !== "files_verified")
				throw fail("FILES_UNVERIFIED", "Every required file must succeed");
			await history.apply(tx);
			return this.update(tx, operation, { status: "committed", reason: null });
		});
	}
}
function fail(code: string, message: string) {
	return new RevertMutationJournalError(code, message);
}
export function createPostgresRevertMutationJournal(
	database: BunSQLDatabase,
	options: RevertJournalOptions,
	plans?: unknown,
) {
	return new PostgresRevertMutationJournal(database, options, plans);
}
