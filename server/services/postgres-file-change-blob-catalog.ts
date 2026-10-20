import { withPgRetry } from "@server/db/pg-retry";
import {
	fileChangeBlobs as blobs,
	fileChangeStorageBudgets as budgets,
	fileChangeBlobReservations as reservations,
} from "@server/db/postgres-schema";
import { generateId } from "@server/lib/id";
import { FILE_CHANGE_LIMITS, type FileChangeBlobRef } from "@shared/file-change-protocol";
import { and, asc, eq, sql } from "drizzle-orm";
import type { BunSQLDatabase } from "drizzle-orm/bun-sql";
import {
	blobCatalogInternals as B,
	FILE_CHANGE_BLOB_BUDGET_ID,
	type FileChangeBlobBudgetMetadata,
	type FileChangeBlobMetadata,
	type FileChangeBlobReservationToken,
} from "./file-change-blob-catalog";
import type { FileChangeBlobAdmission, FileChangeBlobRelease } from "./file-change-blob-store";

type Tx = Parameters<Parameters<BunSQLDatabase["transaction"]>[0]>[0];
export type PostgresBlobReservationToken = FileChangeBlobReservationToken;
export interface PostgresBlobCatalogLease extends PostgresBlobReservationToken {
	release(input: FileChangeBlobRelease): Promise<void>;
}

/** PostgreSQL metadata counterpart; legacy reservations never gate new captures. */
export class PostgresFileChangeBlobCatalog {
	private readonly db: BunSQLDatabase;
	private readonly namespaceKey: string;
	constructor(options: { db: BunSQLDatabase; namespaceKey: string }) {
		B.assertKey(options.namespaceKey, "namespaceKey");
		this.db = options.db;
		this.namespaceKey = options.namespaceKey;
	}
	private runSection<T>(work: (tx: Tx) => Promise<T>): Promise<T> {
		return withPgRetry(() => this.db.transaction((tx) => work(tx)));
	}
	async getBudget(): Promise<FileChangeBlobBudgetMetadata | null> {
		const [row] = await this.db
			.select()
			.from(budgets)
			.where(eq(budgets.id, FILE_CHANGE_BLOB_BUDGET_ID))
			.limit(1);
		if (!row) return null;
		B.checkBudget(row, this.namespaceKey);
		return Object.freeze(row as FileChangeBlobBudgetMetadata);
	}
	async initializeNamespace(
		input: { expectedGeneration?: number | null; quotaBytes?: number } = {},
	): Promise<FileChangeBlobBudgetMetadata> {
		if (input.quotaBytes !== undefined) B.assertNumber(input.quotaBytes, "quotaBytes");
		return this.runSection(async (tx) => {
			await tx
				.insert(budgets)
				.values({
					id: FILE_CHANGE_BLOB_BUDGET_ID,
					namespaceKey: this.namespaceKey,
					quotaBytes: input.quotaBytes ?? FILE_CHANGE_LIMITS.blobSoftQuotaBytes,
					updatedAt: B.now(),
				})
				.onConflictDoNothing();
			const rows = await tx.select().from(budgets).limit(2).for("update");
			if (rows.length !== 1 || rows[0].id !== FILE_CHANGE_BLOB_BUDGET_ID)
				throw B.fail(
					"namespace_mismatch",
					"A database must bind exactly one physical blob namespace",
				);
			const current = rows[0];
			B.checkBudget(current, this.namespaceKey);
			if (input.quotaBytes === undefined || input.quotaBytes === current.quotaBytes)
				return Object.freeze(current as FileChangeBlobBudgetMetadata);
			const [row] = await tx
				.update(budgets)
				.set({ quotaBytes: input.quotaBytes, updatedAt: B.now() })
				.where(eq(budgets.id, current.id))
				.returning();
			return Object.freeze(row as FileChangeBlobBudgetMetadata);
		});
	}
	admission(input: { expectedGeneration?: number; ownerEpoch: string }): FileChangeBlobAdmission {
		B.assertKey(input.ownerEpoch, "ownerEpoch");
		return Object.freeze({
			reserve: ({ expectedSize, signal }: { expectedSize: number; signal: AbortSignal }) =>
				this.reserve({ ...input, expectedSize, signal }),
		});
	}
	async reserve({
		expectedSize,
		ownerEpoch,
		signal,
	}: {
		expectedGeneration?: number;
		ownerEpoch: string;
		expectedSize: number;
		signal: AbortSignal;
	}): Promise<PostgresBlobCatalogLease> {
		B.assertKey(ownerEpoch, "ownerEpoch");
		B.checkSignal(signal);
		let releaseBytes: (() => void) | undefined;
		let generation: number;
		try {
			generation = await this.runSection(async (tx) => {
				// A retried read transaction must not keep its previous memory reservation.
				releaseBytes?.();
				const current = await this.requireBudget(tx, true);
				B.checkSignal(signal);
				releaseBytes = B.reserveBytes(this.namespaceKey, current, expectedSize);
				return current.generation;
			});
		} catch (error) {
			releaseBytes?.();
			throw error;
		}
		const releaseReservation = releaseBytes;
		let firstResult: FileChangeBlobRelease | undefined;
		let pending: Promise<void> | undefined;
		let settled = false;
		return Object.freeze({
			reservationId: generateId(),
			ownerEpoch,
			generation,
			release: async (value: FileChangeBlobRelease) => {
				try {
					const result = B.validateRelease(value);
					B.checkPublicationSize(result, expectedSize);
					if (firstResult && !B.sameRelease(firstResult, result))
						throw B.fail("release_conflict", "A publication result is immutable");
					firstResult = result;
					if (settled) return;
					pending ??= this.recordPublication(result);
					await pending;
					settled = true;
				} finally {
					pending = undefined;
					releaseReservation?.();
				}
			},
		});
	}
	private async recordPublication(result: FileChangeBlobRelease): Promise<void> {
		if (!result.ref) return;
		const ref = result.ref;
		await this.runSection(async (tx) => {
			const current = await this.requireBudget(tx, true);
			const [existing] = await tx.select().from(blobs).where(eq(blobs.digest, ref.digest)).limit(1);
			if (existing) B.checkBlob(existing, ref);
			if (existing?.status === "ready") return;
			const usedBytes = B.publicationUsedBytes(current.usedBytes, ref.sizeBytes);
			await tx
				.insert(blobs)
				.values(B.blobValues(ref, current.generation))
				.onConflictDoUpdate({
					target: blobs.digest,
					set: { status: "ready", updatedAt: B.now() },
				});
			await tx
				.update(budgets)
				.set({ usedBytes, updatedAt: B.now() })
				.where(eq(budgets.id, current.id));
		});
	}
	async getMetadata(input: {
		expectedGeneration?: number;
		ref: FileChangeBlobRef;
	}): Promise<FileChangeBlobMetadata | null> {
		const ref = B.validateRef(input.ref);
		await this.requireBudget(this.db);
		const [row] = await this.db.select().from(blobs).where(eq(blobs.digest, ref.digest)).limit(1);
		if (!row) return null;
		B.checkBlob(row, ref);
		return Object.freeze(row as FileChangeBlobMetadata);
	}
	async listReservations(input: {
		expectedGeneration?: number;
		status: "reserved" | "reconcile_required";
		limit?: number;
		after?: { createdAt: string; id: string };
	}) {
		const limit = B.reservationPage(input);
		const current = await this.requireBudget(this.db);
		const rows = await this.db
			.select()
			.from(reservations)
			.where(
				and(
					eq(reservations.budgetId, current.id),
					eq(reservations.status, input.status),
					input.after
						? sql`(${reservations.createdAt}, ${reservations.id}) > (${input.after.createdAt}, ${input.after.id})`
						: undefined,
				),
			)
			.orderBy(asc(reservations.createdAt), asc(reservations.id))
			.limit(limit + 1);
		return B.reservationPageResult(rows, limit);
	}
	private async requireBudget(tx: Tx | BunSQLDatabase, lock = false) {
		const query = tx
			.select()
			.from(budgets)
			.where(eq(budgets.id, FILE_CHANGE_BLOB_BUDGET_ID))
			.limit(1);
		const [row] = await (lock ? query.for("update") : query);
		if (!row) throw B.fail("namespace_unverified", "Blob catalog has not been initialized");
		B.checkBudget(row, this.namespaceKey);
		return row;
	}
}
export function createPostgresFileChangeBlobCatalog(options: {
	db: BunSQLDatabase;
	namespaceKey: string;
}): PostgresFileChangeBlobCatalog {
	return new PostgresFileChangeBlobCatalog(options);
}
