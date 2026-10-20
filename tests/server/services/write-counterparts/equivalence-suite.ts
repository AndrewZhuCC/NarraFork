/**
 * The wrapper-equivalence behavior suite, shared by the SQLite runner
 * (`server/services/__tests__/write-counterparts-equivalence.test.ts`, always run)
 * and the PostgreSQL runner (`tests/server/services/write-counterparts/pg-equivalence.test.ts`,
 * run under `PG_INTEGRATION=1` against a real PostgreSQL 17).
 *
 * WHAT IS BEING PINNED
 * --------------------
 * The five atomicity wrappers each have a PostgreSQL counterpart whose sections
 * must produce the SAME durable facts and the SAME domain verdicts as the SQLite
 * original. Each scenario below drives one domain through a backend-agnostic
 * facade, asserting the expected outcomes as it goes and returning a JSON
 * projection of everything observable: statuses, counters, fences, error codes,
 * history-callback invocations. The PG runner additionally asserts its
 * projections DEEP-EQUAL the SQLite runner's — the equivalence claim itself.
 *
 * Error identity is compared on the error's `code` field (every wrapper error
 * class carries one), never on message text.
 */

import { expect } from "bun:test";
import { createHash } from "node:crypto";
import type { FileChangeBlobRef, FileChangeState } from "@shared/file-change-protocol";
import type { FileChangeBlobRelease } from "../../../../server/services/file-change-blob-store";
import type {
	BeginFileChangeOperation,
	PrepareFileChangeScope,
	RecordFileChangeEffect,
} from "../../../../server/services/file-change-evidence";
import type { FileChangeScopeIdentity } from "../../../../server/services/file-change-identity";
import { createFileChangeIdentity } from "../../../../server/services/file-change-identity";
import type {
	RevertFileResult,
	RevertJournalContext,
} from "../../../../server/services/revert-mutation-journal";
import type {
	BeginRevertPlan,
	RevertPlanManifestProof,
	RevertPlanOwner,
	RevertPlanSummary,
} from "../../../../server/services/revert-plan-service";
import {
	fingerprintRevertPlanFiles,
	revertPlanHeaderDigest,
} from "../../../../server/services/revert-plan-service";

// ─────────────────────────────────────────────────────────────────────────────
// Facades (one per domain, uniform async shape over both backends)
// ─────────────────────────────────────────────────────────────────────────────

export interface BlobCatalogFacade {
	getBudget(): Promise<Record<string, unknown> | null>;
	initializeNamespace(input: {
		expectedGeneration: number | null;
		quotaBytes?: number;
	}): Promise<Record<string, unknown>>;
	reserve(input: {
		expectedGeneration: number;
		ownerEpoch: string;
		expectedSize: number;
		signal: AbortSignal;
	}): Promise<{
		reservationId: string;
		ownerEpoch: string;
		generation: number;
		release(input: FileChangeBlobRelease): Promise<void>;
	}>;
	getMetadata(input: {
		expectedGeneration: number;
		ref: FileChangeBlobRef;
	}): Promise<Record<string, unknown> | null>;
}

export interface EvidenceFacade {
	prepareScope(input: PrepareFileChangeScope): Promise<Record<string, unknown>>;
	beginOperation(input: BeginFileChangeOperation): Promise<Record<string, unknown>>;
	recordEffect(input: RecordFileChangeEffect): Promise<Record<string, unknown>>;
	finishOperation(
		operationId: string,
		outcome: "succeeded" | "failed" | "interrupted",
	): Promise<Record<string, unknown>>;
	getOperation(operationId: string): Promise<Record<string, unknown> | null>;
	getEffect(operationId: string, mutationId: string): Promise<Record<string, unknown> | null>;
}

export interface PlanFacade {
	begin(input: BeginRevertPlan): Promise<RevertPlanSummary>;
	appendFiles(
		owner: RevertPlanOwner,
		planId: string,
		inputs: import("../../../../server/services/revert-plan-service").AppendRevertPlanFile[],
	): Promise<
		Pick<{ id: string; fileKey: string; sequence: number }, "id" | "fileKey" | "sequence">[]
	>;
	finalize(
		owner: RevertPlanOwner,
		planId: string,
		proof: RevertPlanManifestProof,
	): Promise<RevertPlanSummary>;
}

export interface JournalFacade {
	startExecution(
		ctx: RevertJournalContext,
		proof: RevertPlanManifestProof,
	): Promise<{ operation: Record<string, unknown>; started: boolean }>;
	startFile(ctx: RevertJournalContext, fileId: string): Promise<boolean>;
	recordResult(
		ctx: RevertJournalContext,
		fileId: string,
		result: RevertFileResult,
	): Promise<Record<string, unknown>>;
	finishFiles(
		ctx: RevertJournalContext,
		options?: { cancelled?: boolean; reason?: string | null },
	): Promise<Record<string, unknown>>;
	failHistory(ctx: RevertJournalContext, reason: string): Promise<Record<string, unknown>>;
	commit(ctx: RevertJournalContext, history: HistoryProbe): Promise<Record<string, unknown>>;
}

/** The commit probe: the suite counts invocations; the facade adapts sync/async. */
export interface HistoryProbe {
	calls: () => number;
	/** The facade wraps this into whatever callback shape its backend takes. */
	apply(): void;
}

// ─────────────────────────────────────────────────────────────────────────────
// Fixture kit (the parts only a concrete backend can supply)
// ─────────────────────────────────────────────────────────────────────────────

export interface EquivalenceKit {
	blob: BlobCatalogFacade;
	evidence: EvidenceFacade;
	plan: PlanFacade;
	journal: JournalFacade;
	/** The namespace key every facade is bound to. */
	namespaceKey: string;
	/** The owner epoch every lease claim is made under. */
	ownerEpoch: string;
	/** Install a scope row directly (prepareScope + verify shape), returning its identity. */
	seedScope(overrides?: {
		canonicalRoot?: string;
		workspaceInstanceId?: string;
	}): Promise<FileChangeScopeIdentity>;
	/** The fixed plan file's metadata row, as the journal's claim requires it. */
	fixedFileMetadata(
		planId: string,
		fileId: string,
	): Promise<import("../../../../server/services/revert-plan-service").RevertPlanFileMetadata>;
	/** Whether the history callback's write committed with the marker. */
	historyWriteVisible(): Promise<boolean>;
	/** A revert operation row projection, for assertions. */
	readRevertOperation(planId: string): Promise<Record<string, unknown>>;
}

// ─────────────────────────────────────────────────────────────────────────────
// Shared fixture helpers
// ─────────────────────────────────────────────────────────────────────────────

export function digestOf(text: string): string {
	return createHash("sha256").update(text).digest("hex");
}

export function blobRef(text: string, sizeBytes: number): FileChangeBlobRef {
	return { algorithm: "sha256", digest: digestOf(text), sizeBytes };
}

function regularState(text: string, sizeBytes: number): FileChangeState {
	return { kind: "regular", blob: blobRef(text, sizeBytes), mode: null };
}

function identityOf(
	scope: Readonly<FileChangeScopeIdentity>,
	path: string,
): import("@shared/file-change-protocol").FileChangeIdentity {
	// Built through the production constructor, so the identity is byte-identical
	// to what the services compute when they re-derive it from the scope.
	return createFileChangeIdentity(scope as FileChangeScopeIdentity, {
		deviceId: scope.deviceId,
		pathFlavor: scope.pathFlavor,
		lexicalPath: `${scope.canonicalRoot}/${path}`,
		canonicalPath: `${scope.canonicalRoot}/${path}`,
		objectRole: "entry",
	});
}

/** Publish a ready blob through the catalog's own reserve→release flow. */
export async function publishBlob(
	blob: BlobCatalogFacade,
	generation: number,
	ownerEpoch: string,
	text: string,
	sizeBytes: number,
): Promise<FileChangeBlobRef> {
	const ref = blobRef(text, sizeBytes);
	const lease = await blob.reserve({
		expectedGeneration: generation,
		ownerEpoch,
		expectedSize: sizeBytes,
		signal: new AbortController().signal,
	});
	await lease.release({ ref, published: true });
	return ref;
}

/** Idempotent metadata initialization, without invalidating historical bodies. */
export async function readyNamespace(blob: BlobCatalogFacade): Promise<number> {
	const current = await blob.initializeNamespace({
		expectedGeneration: null,
		quotaBytes: 1_000_000,
	});
	return Number(current.generation);
}

/** The error's comparable identity, never its message. */
export function errorCode(error: unknown): string {
	if (error && typeof error === "object" && "code" in error) return String(error.code);
	return String(error);
}

async function captureError(fn: () => Promise<unknown> | unknown): Promise<string> {
	try {
		await fn();
		return "NO_ERROR";
	} catch (error) {
		return errorCode(error);
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Scenario 1: the blob catalog
// ─────────────────────────────────────────────────────────────────────────────

export async function blobCatalogScenario(kit: EquivalenceKit): Promise<unknown> {
	const { blob, ownerEpoch } = kit;
	const projection: Record<string, unknown> = {};
	const initial = await blob.initializeNamespace({ expectedGeneration: null, quotaBytes: 1000 });
	expect(await blob.initializeNamespace({ expectedGeneration: 99 })).toEqual(initial);
	const lease = await blob.reserve({
		expectedGeneration: 99,
		ownerEpoch,
		expectedSize: 400,
		signal: new AbortController().signal,
	});
	projection.overQuota = await captureError(() =>
		blob.reserve({
			expectedGeneration: 0,
			ownerEpoch,
			expectedSize: 601,
			signal: new AbortController().signal,
		}),
	);
	expect(projection.overQuota).toBe("quota_exceeded");
	const ref = blobRef("blob-catalog-scenario", 400);
	await lease.release({ ref, published: true });
	await lease.release({ ref, published: true });
	expect((await blob.getBudget())?.usedBytes).toBe(400);
	expect((await blob.getMetadata({ expectedGeneration: 100, ref }))?.status).toBe("ready");
	projection.conflictingRelease = await captureError(() =>
		lease.release({ ref: null, published: false }),
	);
	const duplicate = await blob.reserve({
		expectedGeneration: 0,
		ownerEpoch,
		expectedSize: 400,
		signal: new AbortController().signal,
	});
	await duplicate.release({ ref, published: false });
	expect((await blob.getBudget())?.usedBytes).toBe(400);
	const failed = await blob.reserve({
		expectedGeneration: 0,
		ownerEpoch,
		expectedSize: 600,
		signal: new AbortController().signal,
	});
	await failed.release({ ref: null, published: false });
	const later = await blob.reserve({
		expectedGeneration: 0,
		ownerEpoch,
		expectedSize: 600,
		signal: new AbortController().signal,
	});
	await later.release({ ref: blobRef("later-publication", 600), published: true });
	projection.finalBudget = summarizeBudget(await blob.getBudget());
	expect((await blob.getBudget())?.usedBytes).toBe(1000);
	return projection;
}

function summarizeBudget(budget: Record<string, unknown> | null) {
	return budget
		? {
				status: budget.status,
				generation: budget.generation,
				usedBytes: budget.usedBytes,
				reservedBytes: budget.reservedBytes,
				quotaBytes: budget.quotaBytes,
			}
		: null;
}

// ─────────────────────────────────────────────────────────────────────────────
// Scenario 2: the workspace durable lease lifecycle
// ─────────────────────────────────────────────────────────────────────────────

// ─────────────────────────────────────────────────────────────────────────────
// Scenario 3: the evidence store
// ─────────────────────────────────────────────────────────────────────────────

export async function evidenceScenario(kit: EquivalenceKit): Promise<unknown> {
	const { evidence, blob, ownerEpoch } = kit;
	const projection: Record<string, unknown> = {};
	const generation = await readyNamespace(blob);
	const scopeInput: PrepareFileChangeScope = {
		sourceInstanceId: "source-1",
		deviceId: "device-1",
		workspaceInstanceId: "evidence-workspace",
		pathFlavor: "posix",
		canonicalRoot: "/equiv/evidence",
	};
	const scopeRow = await evidence.prepareScope(scopeInput);
	expect((await evidence.prepareScope(scopeInput)).id).toBe(scopeRow.id);
	projection.conflictingScopeId = await captureError(() =>
		evidence.prepareScope({ ...scopeInput, id: "different-id" }),
	);
	const scope: FileChangeScopeIdentity = { ...scopeInput, id: String(scopeRow.id) };
	await publishBlob(blob, generation, ownerEpoch, "evidence-before", 10);
	await publishBlob(blob, generation, ownerEpoch, "evidence-intended", 12);
	const input: BeginFileChangeOperation = {
		sourceInstanceId: "source-1",
		sourceKind: "editor",
		sourceId: "request-1",
		attempt: 1,
		requestDigest: digestOf("evidence-request"),
		expectedEffectCount: 1,
		executionBinding: null,
		actor: {
			kind: "human",
			subjectKey: "user:test",
			narratorId: null,
			userId: null,
			label: null,
			deleted: false,
			parentSubjectKey: null,
		},
	};
	const operation = await evidence.beginOperation(input);
	expect(operation).toMatchObject({
		evidenceVersion: 3,
		journalSeq: 1,
		replayed: false,
		executionBindingJson: null,
	});
	const replay = await evidence.beginOperation(input);
	expect(replay).toMatchObject({ id: operation.id, journalSeq: 1, replayed: true });
	projection.conflictingOperation = await captureError(() =>
		evidence.beginOperation({ ...input, requestDigest: digestOf("changed") }),
	);
	const operationId = String(operation.id);
	const result: RecordFileChangeEffect = {
		operationId,
		identity: identityOf(scope, "file.txt"),
		before: regularState("evidence-before", 10),
		intendedAfter: regularState("evidence-intended", 12),
		observedAfter: regularState("evidence-intended", 12),
		outcome: "applied",
	};
	const recorded = await evidence.recordEffect(result);
	expect(recorded).toMatchObject({
		diskOutcome: "applied",
		outcome: "changed",
		settlement: "settled",
		scopeRevision: null,
		executionReceiptJson: null,
	});
	expect((await evidence.recordEffect(result)).id).toBe(recorded.id);
	projection.overwriteResult = await captureError(() =>
		evidence.recordEffect({ ...result, outcome: "unknown" }),
	);
	projection.overDeclared = await captureError(() =>
		evidence.recordEffect({ ...result, identity: identityOf(scope, "other.txt") }),
	);
	await evidence.finishOperation(operationId, "succeeded");
	projection.conflictingFinish = await captureError(() =>
		evidence.finishOperation(operationId, "failed"),
	);
	const second = await evidence.beginOperation({ ...input, sourceId: "request-2" });
	expect(second.journalSeq).toBe(2);
	const unknown = await evidence.recordEffect({
		...result,
		operationId: String(second.id),
		outcome: "unknown",
	});
	expect(unknown).toMatchObject({
		outcome: "unknown",
		diskOutcome: "unknown",
		executionConfirmed: false,
	});
	await evidence.finishOperation(String(second.id), "failed");
	expect((await evidence.beginOperation({ ...input, sourceId: "request-2" })).replayed).toBe(true);
	const independent = await evidence.beginOperation({ ...input, sourceId: "request-3" });
	expect(
		(await evidence.recordEffect({ ...result, operationId: String(independent.id) })).diskOutcome,
	).toBe("applied");
	projection.operation = summarizeOperation(await evidence.getOperation(operationId));
	projection.effect = summarizeEffect(
		await evidence.getEffect(operationId, String(recorded.mutationId)),
	);
	projection.unknown = summarizeEffect(unknown);
	return projection;
}

function summarizeOperation(operation: Record<string, unknown> | null) {
	return operation
		? {
				settlement: operation.settlement,
				executionOutcome: operation.executionOutcome,
				effectOutcome: operation.effectOutcome,
				coverage: operation.coverage,
				attributionGrade: operation.attributionGrade,
				preparedEffectCount: operation.preparedEffectCount,
				settledEffectCount: operation.settledEffectCount,
				unresolvedEffectCount: operation.unresolvedEffectCount,
			}
		: null;
}

function summarizeEffect(effect: Record<string, unknown> | null) {
	return effect
		? {
				settlement: effect.settlement,
				outcome: effect.outcome,
				attributionGrade: effect.attributionGrade,
				executionConfirmed: effect.executionConfirmed,
			}
		: null;
}

// ─────────────────────────────────────────────────────────────────────────────
// Scenario 4 + 5: the revert plan and the mutation journal (one continuous flow)
// ─────────────────────────────────────────────────────────────────────────────

export async function revertFlowScenario(kit: EquivalenceKit): Promise<unknown> {
	const { blob, plan, journal, ownerEpoch } = kit;
	const projection: Record<string, unknown> = {};
	const generation = await readyNamespace(blob);
	const scope = await kit.seedScope({ canonicalRoot: "/equiv/revert" });
	const selector = await publishBlob(blob, generation, ownerEpoch, "revert-selector", 20);
	const planRef = await publishBlob(blob, generation, ownerEpoch, "revert-plan", 30);
	const history = await publishBlob(blob, generation, ownerEpoch, "revert-history", 40);
	const expectedBlob = await publishBlob(blob, generation, ownerEpoch, "revert-expected", 10);
	const desiredBlob = await publishBlob(blob, generation, ownerEpoch, "revert-desired", 12);
	const owner: RevertPlanOwner = {
		subjectKey: "narrator:revert-test",
		narratorId: null,
		projectId: null,
	};
	const make = async (idempotencyKey: string) => {
		const header = {
			...owner,
			idempotencyKey,
			requestDigest: digestOf(idempotencyKey),
			kind: "revert" as const,
			revertScope: "workspace" as const,
			selectorKind: "all" as const,
			selector,
			historyManifest: history,
			expectedMessageVersion: 1,
			expectedFileCount: 2,
		};
		const entries = [0, 1].map((sequence) => ({
			sequence,
			identity: identityOf(scope, `${sequence}.txt`),
			expected: { kind: "regular" as const, blob: expectedBlob, mode: 0o644 },
			desired: { kind: "regular" as const, blob: desiredBlob, mode: 0o644 },
		}));
		const fingerprint = fingerprintRevertPlanFiles(entries);
		const proof: RevertPlanManifestProof = {
			source: "trusted_published_planner_v1",
			headerDigest: revertPlanHeaderDigest(header),
			orderedFilesDigest: fingerprint.orderedFilesDigest,
			fileEvidenceBytes: fingerprint.fileEvidenceBytes,
			computation: "complete",
			selectorCoverage: "complete",
			historyCoverage: "complete",
			omittedFiles: 0,
			unknownFiles: 0,
		};
		const summary = await plan.begin({ ...header, plan: planRef, manifestProof: proof });
		const appended = await plan.appendFiles(owner, summary.id, entries);
		expect((await plan.appendFiles(owner, summary.id, entries)).map((file) => file.id)).toEqual(
			appended.map((file) => file.id),
		);
		expect((await plan.finalize(owner, summary.id, proof)).status).toBe("prepared");
		expect((await plan.finalize(owner, summary.id, proof)).status).toBe("prepared");
		const ctx = { owner, planId: summary.id, planHash: summary.planHash as string };
		expect((await journal.startExecution(ctx, proof)).started).toBe(true);
		expect((await journal.startExecution(ctx, proof)).started).toBe(false);
		return { ctx, entries, appended, header, summary, proof };
	};
	const a = await make("commit");
	for (const [index, file] of a.appended.entries()) {
		expect(await journal.startFile(a.ctx, file.id)).toBe(true);
		expect(await journal.startFile(a.ctx, file.id)).toBe(false);
		const recorded = await journal.recordResult(a.ctx, file.id, {
			status: "applied",
			reason: null,
			historyStatus: "complete",
			observedAfter: a.entries[index].desired,
		});
		expect(recorded.beforeBlobDigest).toBe(expectedBlob.digest);
		expect(recorded.receiptJson).toMatchObject({ historyStatus: "complete" });
	}
	expect((await journal.finishFiles(a.ctx)).status).toBe("files_verified");
	let calls = 0;
	const historyProbe: HistoryProbe = {
		calls: () => calls,
		apply: () => {
			calls++;
		},
	};
	expect((await journal.commit(a.ctx, historyProbe)).status).toBe("committed");
	expect((await journal.commit(a.ctx, historyProbe)).status).toBe("committed");
	expect(calls).toBe(1);
	projection.historyWritesCommitted = await kit.historyWriteVisible();
	projection.operationA = await kit.readRevertOperation(a.summary.id);
	const b = await make("partial");
	expect(await journal.startFile(b.ctx, b.appended[0].id)).toBe(true);
	await journal.recordResult(b.ctx, b.appended[0].id, {
		status: "applied",
		reason: null,
		historyStatus: "complete",
		observedAfter: b.entries[0].desired,
	});
	await journal.recordResult(b.ctx, b.appended[1].id, {
		status: "conflict",
		reason: "content_changed",
		historyStatus: "complete",
	});
	expect((await journal.finishFiles(b.ctx)).status).toBe("partial");
	projection.partialCommitRejected = await captureError(() => journal.commit(b.ctx, historyProbe));
	expect(calls).toBe(1);
	projection.operationB = await kit.readRevertOperation(b.summary.id);
	const c = await make("unknown");
	await journal.startFile(c.ctx, c.appended[0].id);
	await journal.recordResult(c.ctx, c.appended[0].id, {
		status: "unknown",
		reason: "timeout",
		historyStatus: "complete",
	});
	expect(
		(
			await journal.recordResult(c.ctx, c.appended[0].id, {
				status: "applied",
				reason: null,
				historyStatus: "complete",
			})
		).status,
	).toBe("unknown");
	await journal.recordResult(c.ctx, c.appended[1].id, {
		status: "not_attempted",
		reason: "cancelled",
		historyStatus: "complete",
	});
	expect((await journal.finishFiles(c.ctx, { cancelled: true })).status).toBe("partial");
	projection.operationC = await kit.readRevertOperation(c.summary.id);
	const d = await make("history-failed");
	for (const file of d.appended)
		await journal.recordResult(d.ctx, file.id, {
			status: "unchanged",
			reason: null,
			historyStatus: "complete",
		});
	await journal.finishFiles(d.ctx);
	projection.historyFailure = await captureError(() =>
		journal.commit(d.ctx, {
			calls: () => 0,
			apply: () => {
				throw new Error("history failure");
			},
		}),
	);
	expect((await kit.readRevertOperation(d.summary.id)).status).toBe("files_verified");
	await journal.failHistory(d.ctx, "message_version_changed");
	projection.operationD = await kit.readRevertOperation(d.summary.id);
	projection.compensationId = (
		await kit.fixedFileMetadata(a.summary.id, a.appended[0].id)
	).compensateMutationId;
	expect(projection.compensationId).toBeNull();
	return projection;
}

/** The same bounded-query regression runs on real SQLite and real PostgreSQL. */
export async function revertPlanBatchScenario(kit: EquivalenceKit, queries: string[]) {
	const generation = await readyNamespace(kit.blob);
	const scope = await kit.seedScope({ canonicalRoot: "/equiv/plan-batches" });
	const selector = await publishBlob(kit.blob, generation, kit.ownerEpoch, "batch-selector", 20);
	const planRef = await publishBlob(kit.blob, generation, kit.ownerEpoch, "batch-plan", 30);
	const history = await publishBlob(kit.blob, generation, kit.ownerEpoch, "batch-history", 40);
	const desired = {
		kind: "regular" as const,
		mode: 0o644,
		blob: await publishBlob(kit.blob, generation, kit.ownerEpoch, "batch-desired", 12),
	};
	// Deliberately absent from the catalog; a live fingerprint is not an archival dependency.
	const expected = {
		kind: "regular" as const,
		mode: 0o644,
		blob: blobRef("batch-unarchived-current", 32 * 1024 * 1024),
	};
	const owner: RevertPlanOwner = {
		subjectKey: "human:batch-test",
		narratorId: null,
		projectId: null,
	};
	const projection = [];
	for (const count of [32, 1000]) {
		const fileExpected =
			count === 32 ? { ...expected, blob: blobRef("batch-late-current", 10) } : expected;
		const entries = Array.from({ length: count }, (_, sequence) => ({
			sequence,
			identity: identityOf(scope, `${sequence}.txt`),
			expected: fileExpected,
			desired,
		}));
		const header = {
			...owner,
			idempotencyKey: `batches-${count}`,
			requestDigest: digestOf(`batches-${count}`),
			kind: "revert" as const,
			revertScope: "workspace" as const,
			selectorKind: "all" as const,
			selector,
			historyManifest: history,
			expectedMessageVersion: 1,
			expectedFileCount: count,
		};
		const fingerprint = fingerprintRevertPlanFiles(entries);
		expect(fingerprint.fileEvidenceBytes).toBe(count * desired.blob.sizeBytes);
		const proof: RevertPlanManifestProof = {
			source: "trusted_published_planner_v1",
			headerDigest: revertPlanHeaderDigest(header),
			orderedFilesDigest: fingerprint.orderedFilesDigest,
			fileEvidenceBytes: fingerprint.fileEvidenceBytes,
			computation: "complete",
			selectorCoverage: "complete",
			historyCoverage: "complete",
			omittedFiles: 0,
			unknownFiles: 0,
		};
		const summary = await kit.plan.begin({ ...header, plan: planRef, manifestProof: proof });
		queries.length = 0;
		let first: { id: string; fileKey: string; sequence: number }[] = [];
		for (let index = 0; index < count; index += 32) {
			const appended = await kit.plan.appendFiles(
				owner,
				summary.id,
				entries.slice(index, index + 32),
			);
			if (index === 0) first = appended;
		}
		const fileQueries = () =>
			queries.filter(
				(query) => query.startsWith("select ") && query.includes('from "revert_operation_files"'),
			);
		const appendQueries = fileQueries();
		expect(appendQueries).toHaveLength(Math.ceil(count / 32));
		expect(
			appendQueries.every(
				(query) =>
					query.includes('"sequence" in (') &&
					query.includes('"file_key" in (') &&
					query.includes("limit "),
			),
		).toBe(true);
		if (count === 32)
			await publishBlob(kit.blob, generation, kit.ownerEpoch, "batch-late-current", 10);
		expect(await kit.plan.appendFiles(owner, summary.id, entries.slice(0, 32))).toEqual(first);
		expect(
			await captureError(() =>
				kit.plan.appendFiles(owner, summary.id, [{ ...entries[1], sequence: 0 }]),
			),
		).toBe("REVERT_PLAN_REQUEST_CONFLICT");
		expect(
			await captureError(() =>
				kit.plan.appendFiles(owner, summary.id, [
					{ ...entries[0], identity: identityOf(scope, "collision.txt") },
				]),
			),
		).toBe("REVERT_PLAN_DUPLICATE_FILE");
		expect(
			await captureError(() =>
				kit.plan.appendFiles(owner, summary.id, [{ ...entries[0], expected: { kind: "absent" } }]),
			),
		).toBe("REVERT_PLAN_REQUEST_CONFLICT");
		queries.length = 0;
		expect((await kit.plan.finalize(owner, summary.id, proof)).status).toBe("prepared");
		const finalPages = fileQueries().filter((query) => query.includes("order by"));
		expect(finalPages).toHaveLength(Math.ceil(count / 32));
		const ctx = { owner, planId: summary.id, planHash: summary.planHash as string };
		await kit.journal.startExecution(ctx, proof);
		if (count === 32) {
			const noOpState = {
				kind: "regular" as const,
				mode: 0o644,
				blob: blobRef("batch-no-op-unarchived", 64),
			};
			const noOpEntry = {
				sequence: 0,
				identity: identityOf(scope, "no-op.txt"),
				expected: noOpState,
				desired: noOpState,
			};
			const noOpFingerprint = fingerprintRevertPlanFiles([noOpEntry]);
			expect(noOpFingerprint.fileEvidenceBytes).toBe(0);
			const noOpHeader = {
				...header,
				idempotencyKey: "batch-no-op",
				requestDigest: digestOf("batch-no-op"),
				expectedFileCount: 1,
			};
			const noOpProof = {
				...proof,
				headerDigest: revertPlanHeaderDigest(noOpHeader),
				orderedFilesDigest: noOpFingerprint.orderedFilesDigest,
				fileEvidenceBytes: 0,
			};
			const noOp = await kit.plan.begin({ ...noOpHeader, plan: planRef, manifestProof: noOpProof });
			const [noOpFile] = await kit.plan.appendFiles(owner, noOp.id, [noOpEntry]);
			expect((await kit.plan.finalize(owner, noOp.id, noOpProof)).status).toBe("prepared");
			expect((await kit.fixedFileMetadata(noOp.id, noOpFile.id)).desiredStateJson).toEqual(
				noOpState,
			);
		}
		const recorded = await kit.journal.recordResult(ctx, first[0].id, {
			status: "applied",
			reason: null,
			historyStatus: "complete",
			observedAfter: desired,
		});
		expect(recorded.beforeBlobDigest).toBeNull();
		expect(recorded.receiptJson).toMatchObject({ historyStatus: "unavailable" });
		projection.push({
			count,
			appendQueries: appendQueries.length,
			finalizeQueries: finalPages.length,
			fileEvidenceBytes: fingerprint.fileEvidenceBytes,
		});
	}
	return projection;
}
