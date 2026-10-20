/**
 * The PostgreSQL implementation of the equivalence kit: the PG counterparts
 * (`PostgresFileChangeBlobCatalog`, `PostgresWorkspaceLeaseStore`,
 * `PostgresFileChangeEvidenceStore`, `PostgresRevertPlanStore`,
 * `PostgresRevertMutationJournal`) behind the suite's facades.
 *
 * The two workspace facade methods that compose a whole write lifecycle
 * (`runCleanWrite` / `runUncertainWrite`) mirror what the SQLite coordinator's
 * `withWrite` finalizer does — admit → register → settle → release, and admit →
 * register → end-quarantined → detach-scope-guard (lease ranges retained) — because the PG
 * store exposes the durable SECTIONS while the coordinator exposes the composed
 * lifecycle. That composition is the wrapper's equivalence claim for this domain.
 */
import { eq } from "drizzle-orm";
import type { BunSQLDatabase } from "drizzle-orm/bun-sql";
import {
	fileChangeStorageBudgets,
	revertOperationFiles,
	revertOperations,
} from "../../../../server/db/postgres-schema";
import { FILE_CHANGE_BLOB_BUDGET_ID } from "../../../../server/services/file-change-blob-catalog";
import { createPostgresFileChangeBlobCatalog } from "../../../../server/services/postgres-file-change-blob-catalog";
import { createPostgresFileChangeEvidenceStore } from "../../../../server/services/postgres-file-change-evidence-store";
import { createPostgresRevertMutationJournal } from "../../../../server/services/postgres-revert-journal-store";
import { createPostgresRevertPlanStore } from "../../../../server/services/postgres-revert-plan-store";
import type {
	BlobCatalogFacade,
	EquivalenceKit,
	EvidenceFacade,
	JournalFacade,
	PlanFacade,
} from "./equivalence-suite";

const NAMESPACE = "equivalence-sqlite";
const OWNER_EPOCH = "sqlite-epoch-1";
const _RUNTIME = { runtimeEpoch: "sqlite-runtime-epoch", runtimeGeneration: 7 };

let serial = 0;

export function makePostgresKit(pgDb: BunSQLDatabase): EquivalenceKit {
	const blobCatalog = createPostgresFileChangeBlobCatalog({ db: pgDb, namespaceKey: NAMESPACE });
	const evidence = createPostgresFileChangeEvidenceStore(pgDb);
	const plans = createPostgresRevertPlanStore(pgDb, { namespaceKey: NAMESPACE });
	const journal = createPostgresRevertMutationJournal(pgDb, { namespaceKey: NAMESPACE }, plans);

	const blob: BlobCatalogFacade = {
		getBudget: () => blobCatalog.getBudget(),
		initializeNamespace: (input) => blobCatalog.initializeNamespace(input),

		reserve: async (input) => {
			const lease = await blobCatalog.reserve(input);
			return {
				reservationId: lease.reservationId,
				ownerEpoch: lease.ownerEpoch,
				generation: lease.generation,
				release: (releaseInput) => lease.release(releaseInput),
			};
		},
		getMetadata: (input) => blobCatalog.getMetadata(input),
	};

	const evidenceFacade: EvidenceFacade = {
		prepareScope: (input) => evidence.prepareScope(input),
		beginOperation: (input) => evidence.beginOperation(input),
		recordEffect: (input) => evidence.recordEffect(input),
		finishOperation: (operationId, outcome) => evidence.finishOperation(operationId, outcome),
		getOperation: (operationId) => evidence.getOperation(operationId),
		getEffect: (operationId, mutationId) => evidence.getEffect(operationId, mutationId),
	};

	const plan: PlanFacade = {
		begin: (input) => plans.begin(input),
		appendFiles: (owner, planId, inputs) => plans.appendFiles(owner, planId, inputs),
		finalize: (owner, planId, proof) => plans.finalize(owner, planId, proof),
	};

	const journalFacade: JournalFacade = {
		startExecution: (ctx, proof) => journal.startExecution(ctx, proof),
		startFile: (ctx, fileId) => journal.startFile(ctx, fileId),
		recordResult: (ctx, fileId, result) => journal.recordResult(ctx, fileId, result),
		finishFiles: (ctx, options) => journal.finishFiles(ctx, options),
		failHistory: (ctx, reason) => journal.failHistory(ctx, reason),
		commit: async (ctx, probe) =>
			journal.commit(ctx, {
				db: pgDb,
				apply: async (tx) => {
					probe.apply();
					await tx
						.update(fileChangeStorageBudgets)
						.set({ updatedAt: "history-marker" })
						.where(eq(fileChangeStorageBudgets.id, FILE_CHANGE_BLOB_BUDGET_ID));
				},
			}),
	};

	return {
		blob,
		evidence: evidenceFacade,
		plan,
		journal: journalFacade,
		namespaceKey: NAMESPACE,
		ownerEpoch: OWNER_EPOCH,
		seedScope: async (overrides = {}) => {
			const n = ++serial;
			const canonicalRoot = overrides.canonicalRoot ?? `/equiv/${n}`;
			const workspaceInstanceId = overrides.workspaceInstanceId ?? `workspace-${n}`;
			const prepared = await evidence.prepareScope({
				sourceInstanceId: "source-1",
				deviceId: "device-1",
				workspaceInstanceId,
				pathFlavor: "posix",
				canonicalRoot,
			});
			return {
				id: prepared.id,
				sourceInstanceId: prepared.sourceInstanceId,
				deviceId: prepared.deviceId,
				workspaceInstanceId: prepared.workspaceInstanceId,
				pathFlavor: prepared.pathFlavor,
				canonicalRoot: prepared.canonicalRoot,
			};
		},
		fixedFileMetadata: async (planId, fileId) => {
			const rows = await pgDb
				.select()
				.from(revertOperationFiles)
				.where(eq(revertOperationFiles.id, fileId));
			const row = rows[0] as
				| typeof import("../../../../server/db/schema").revertOperationFiles.$inferSelect
				| undefined;
			if (!row || row.revertOperationId !== planId) throw new Error("plan file vanished");
			return {
				id: row.id,
				fileKey: row.fileKey,
				sequence: row.sequence,
				identityJson: row.identityJson,
				expectedStateJson: row.expectedStateJson,
				desiredStateJson: row.desiredStateJson,
				applyMutationId: row.applyMutationId,
				applyRequestDigest: row.applyRequestDigest,
				compensateMutationId: row.compensateMutationId,
				compensateRequestDigest: row.compensateRequestDigest,
				status: row.status,
			};
		},
		historyWriteVisible: async () => {
			const rows = await pgDb
				.select()
				.from(fileChangeStorageBudgets)
				.where(eq(fileChangeStorageBudgets.id, FILE_CHANGE_BLOB_BUDGET_ID));
			return rows[0]?.updatedAt === "history-marker";
		},
		readRevertOperation: async (planId) => {
			const rows = await pgDb
				.select()
				.from(revertOperations)
				.where(eq(revertOperations.id, planId));
			const row = rows[0];
			if (!row) throw new Error("revert operation vanished");
			return {
				status: row.status,
				appliedFileCount: row.appliedFileCount,
				reason: row.reason,
				coverageComplete: row.coverageComplete,
			};
		},
	};
}
