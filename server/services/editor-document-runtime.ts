import { join, relative, sep } from "node:path";
import { EDITOR_FILE_MAX_BYTES, type EditorOperationResult } from "../../shared/editor-document";
import type { EditorApplyResult, EditorQueryResult } from "../../shared/editor-provider-contracts";
import { resolveBackend } from "../lib/agent/execution/registry";
import { AppError } from "../lib/errors";
import { hotSafe } from "../lib/hot-safe";
import { logger } from "../lib/logger";
import { getNarraforkHome } from "../lib/narrafork-home";
import { settings } from "../lib/settings";
import { RemoteBackend } from "./device-remote-backend";
import {
	type EditorDocumentDependencies,
	EditorDocumentError,
	EditorDocumentService,
} from "./editor-document-service";
import {
	createEditorFileAtomicIo,
	editorOperationNamespace,
	editorWriterResourceKey,
	validateEditorApplyResult,
} from "./editor-file-atomic-io";
import type { FileChangeEffectRecord, FileChangeOperationRecord } from "./file-change-evidence";
import { createFileChangeLocalIo } from "./file-change-local-io";
import {
	EditorFileChangeUncertainError,
	executeEditorFileChange,
	getCurrentLocalFileChangeRuntime,
	getDefaultLocalFileChangeRuntime,
} from "./file-change-runtime";
import { workspaceContextService } from "./workspace-context-service";
import { withWorkspaceAdmission } from "./worktree-lifecycle-guard";
import { closeClaim, openClaim, sealClaim } from "./worktree-write-claims";

function remoteQueryRequest(operation: FileChangeOperationRecord, effect: FileChangeEffectRecord) {
	if (
		!operation.ownerUserId ||
		!operation.requestDigest ||
		!operation.workspaceLeaseId ||
		operation.editorRawSnapshotRevision === null ||
		effect.intendedAfterStateJson.kind !== "regular" ||
		effect.identityJson.deviceId === "local" ||
		effect.requestDigest !== operation.requestDigest
	)
		throw new Error("Original remote editor intent unavailable");
	const namespace = editorOperationNamespace(
		operation.sourceInstanceId,
		operation.ownerUserId,
		effect.identityJson.deviceId,
	);
	return {
		namespace,
		resourceKey: editorWriterResourceKey(namespace, operation.id, effect.id),
		operationId: operation.sourceId,
		requestDigest: operation.requestDigest,
	};
}
function remoteResult(
	operation: FileChangeOperationRecord,
	effect: FileChangeEffectRecord,
): EditorOperationResult {
	const operationId = operation.sourceId;
	if (
		operation.editorRawSnapshotRevision === null ||
		!operation.workspaceLeaseId ||
		operation.settlement !== "settled" ||
		effect.settlement !== "settled" ||
		!effect.executionConfirmed
	)
		return { status: "uncertain", operationId };
	if (effect.executionReceiptJson?.outcome === "not_applied")
		return { status: "failed", operationId };
	const after = effect.observedAfterStateJson,
		intended = effect.intendedAfterStateJson;
	if (
		effect.executionReceiptJson?.outcome !== "applied" ||
		after.kind !== "regular" ||
		intended.kind !== "regular" ||
		after.blob.digest !== intended.blob.digest ||
		after.blob.sizeBytes !== intended.blob.sizeBytes
	)
		return { status: "uncertain", operationId };
	return {
		status: "saved",
		operationId,
		result: {
			status: "saved",
			operationId,
			hashKind: "raw-sha256",
			hash: after.blob.digest,
			bytes: after.blob.sizeBytes,
			snapshotRevision: operation.editorRawSnapshotRevision,
		},
	};
}
async function acknowledgeEditorReceipt(
	operation: FileChangeOperationRecord,
	effect: FileChangeEffectRecord,
	authorize: () => Promise<void>,
	receipt: EditorApplyResult,
) {
	const runtime = await getCurrentLocalFileChangeRuntime();
	if (runtime.evidence.getOperation(operation.id)?.settlement !== "settled") return;
	// A response alone is insufficient: original evidence and lease must already be durable.
	runtime.settleEditorReceipt(operation.id, receipt);
	await authorize();
	const backend = resolveBackend({ requested: effect.identityJson.deviceId });
	if (!(backend instanceof RemoteBackend)) throw new Error("Original editor device unavailable");
	const rpc = backend.editorWriteRpc();
	try {
		await rpc.call("editor.query", {
			...remoteQueryRequest(operation, effect),
			acknowledgeTerminal: true,
		});
	} finally {
		await rpc.close();
	}
}

export const executeEditorDocumentFileChange: EditorDocumentDependencies["execute"] = async (
	request,
) => {
	const { remote, resourceKey } = request;
	if (remote && !(remote.backend instanceof RemoteBackend))
		throw new AppError("Unsupported editor backend", 503, "EDITOR_PROVIDER_UNSUPPORTED");
	if (!resourceKey)
		throw new AppError("Editor resource binding unavailable", 503, "EDITOR_PROVIDER_UNSUPPORTED");
	const backend = remote?.backend;
	let validatedReceipt: EditorApplyResult | undefined;
	const io =
		remote && backend instanceof RemoteBackend
			? createEditorFileAtomicIo({
					open: async () => backend.editorWriteRpc(),
					read: (path, signal) =>
						backend.editorObservation(
							{
								deviceId: backend.deviceId,
								pathFlavor: backend.pathFlavor,
								canonicalPath: path,
								workspaceIdentity: remote.rootIdentity,
								...remote.runtime,
								fencingToken: backend.runtimeGeneration,
							},
							resourceKey,
							signal,
						),
					onReceipt: (receipt) => {
						validatedReceipt = receipt;
					},
				})
			: createFileChangeLocalIo(undefined, {
					maxBytes: EDITOR_FILE_MAX_BYTES,
					createParents: false,
					cancelledMutationIsUnknown: true,
				});
	const run = () =>
		withWorkspaceAdmission(
			{ deviceId: backend?.deviceId ?? "local", path: request.cwd },
			async () => {
				await request.authorize();
				return executeEditorFileChange({ ...request, io });
			},
		);
	if (request.workspaceRevision === undefined)
		throw new AppError("Editor workspace revision unavailable", 409, "EDITOR_IDENTITY_CHANGED");
	try {
		return await workspaceContextService.withRevision(
			request.narratorId,
			request.workspaceRevision,
			undefined,
			run,
		);
	} finally {
		if (
			remote &&
			validatedReceipt &&
			validatedReceipt.outcome !== "uncertain" &&
			request.authorizeReceipt
		) {
			try {
				const runtime = await getCurrentLocalFileChangeRuntime();
				const namespace = await runtime.verifyNamespace();
				const operation = runtime.evidence.getEditorOperation(
					namespace.sourceInstanceId,
					request.requestId,
				);
				const effects = operation
					? runtime.evidence.listEffects(operation.id, { limit: 2 }).items
					: [];
				if (
					operation &&
					operation.ownerUserId === request.userId &&
					operation.narratorId === request.narratorId &&
					effects.length === 1
				)
					await acknowledgeEditorReceipt(
						operation,
						effects[0],
						request.authorizeReceipt,
						validatedReceipt,
					);
			} catch (error) {
				logger.debug("Editor engine ACK deferred", { error: String(error) });
			}
		}
	}
};

export function getEditorDocumentService(): EditorDocumentService {
	return hotSafe(
		"narrafork.editor-documents.v1",
		() =>
			new EditorDocumentService({
				root: join(getNarraforkHome(), "editor-transfers"),
				execute: executeEditorDocumentFileChange,
				writerUnavailableReason: () => "durable_executor_unavailable",
				writerAvailable: (binding) =>
					binding.writeTarget
						? binding.writeTarget.backend instanceof RemoteBackend &&
							binding.writeTarget.backend.supportsEditorDurableWrite
						: !binding.rawSource && (!binding.deviceId || binding.deviceId === "local"),
				atomicWriter: (binding) => binding.writeTarget?.backend instanceof RemoteBackend,
				rawWriter: {
					execute: executeEditorDocumentFileChange,
					queryOperation: queryEditorOperation,
				},
				queryOperation: queryEditorOperation,
				async afterWrite(binding, narratorId, requestId, run, actor) {
					if (binding.deviceId && binding.deviceId !== "local") return run();
					const rel = relative(binding.cwd, binding.canonicalPath);
					const claimPath =
						rel && rel !== ".." && !rel.startsWith(`..${sep}`) ? rel.split(sep).join("/") : null;
					const claimId = `human-${requestId}`;
					let changed = false;
					try {
						if (claimPath) openClaim(binding.cwd, narratorId, claimId, [claimPath]);
						await run();
						changed = true;
						try {
							if (claimPath) closeClaim(binding.cwd, claimId, [claimPath]);
						} catch (error) {
							logger.debug("Closing large editor claim failed", { error: String(error) });
						}
						try {
							const { interjectFileEditAsUserMessage } = await import("./file-edit-interject");
							await interjectFileEditAsUserMessage(narratorId, {
								filePath: binding.canonicalPath,
								worktreePath: binding.cwd,
								lineStats: null,
								locale: actor.locale ?? "en",
								userId: actor.userId,
							});
						} catch (error) {
							logger.debug("Large editor save notification failed", { error: String(error) });
						}
					} catch (error) {
						changed = error instanceof EditorFileChangeUncertainError;
						try {
							if (claimPath) sealClaim(binding.cwd, claimId);
						} catch (claimError) {
							logger.debug("Sealing large editor claim failed", { error: String(claimError) });
						}
						throw error;
					} finally {
						if (changed && claimPath) {
							try {
								const { invalidateWorkspaceTreeCache } = await import("./narrator-session-state");
								invalidateWorkspaceTreeCache(binding.cwd);
								if (settings.chapters.treeSnapshotsEnabled) {
									const { worktreeTreeSnapshot } = await import("./worktree-tree-snapshot");
									const tree = await worktreeTreeSnapshot.tryCaptureHot(binding.cwd, "local");
									if (tree) {
										const { advanceChapterSnapshot } = await import("./chapter-snapshot-ref");
										await advanceChapterSnapshot(binding.cwd, tree, "human editor save");
									}
								}
							} catch (error) {
								logger.debug("Large editor compatibility snapshot failed", {
									error: String(error),
								});
							}
						}
					}
				},
			}),
	);
}

export const queryEditorOperation: NonNullable<
	EditorDocumentDependencies["queryOperation"]
> = async (actor, operationId, recovery) => {
	actor.signal?.throwIfAborted();
	const runtime = await getDefaultLocalFileChangeRuntime();
	const namespace = await runtime.verifyNamespace(actor.signal);
	const operation = runtime.evidence.getEditorOperation(namespace.sourceInstanceId, operationId);
	if (
		!operation ||
		operation.ownerUserId !== actor.userId ||
		operation.narratorId !== actor.narratorId
	)
		throw new EditorDocumentError(
			"EDITOR_OPERATION_UNKNOWN",
			"No owned durable receipt is available; verify before retrying",
			404,
		);
	const effects = runtime.evidence.listEffects(operation.id, { limit: 2 }).items;
	const deviceId =
		operation.executionBindingJson?.deviceId ?? effects[0]?.identityJson.deviceId ?? "local";
	if (actor.authorizeOperation) await actor.authorizeOperation(deviceId);
	else if (deviceId !== "local")
		throw new EditorDocumentError(
			"EDITOR_OPERATION_UNKNOWN",
			"Original receipt device authorization unavailable",
			403,
		);
	if (deviceId !== "local") {
		if (
			effects.length !== 1 ||
			!actor.authorizeOperation ||
			operation.workspaceLeaseId === null ||
			operation.editorRawSnapshotRevision === null
		)
			return { status: "uncertain", operationId };
		const effect = effects[0];
		const authorize = actor.authorizeOperation.bind(actor, deviceId);
		const saved = remoteResult(operation, effect);
		if (operation.settlement === "settled") {
			// Retry only terminal ACK. Offline/failed ACK cannot downgrade proven SQLite results.
			if ((saved.status === "saved" && saved.result) || saved.status === "failed") {
				const receipt: EditorApplyResult =
					saved.status === "saved" && saved.result
						? {
								operationId,
								outcome: "committed",
								rawDigest: saved.result.hash,
								totalBytes: saved.result.bytes,
							}
						: { operationId, outcome: "rejected", rawDigest: null, totalBytes: null };
				await acknowledgeEditorReceipt(operation, effect, authorize, receipt).catch((error) =>
					logger.debug("Editor engine ACK deferred", { error: String(error) }),
				);
				await actor.authorizeOperation(deviceId);
			}
			return saved;
		}
		try {
			const backend = resolveBackend({ requested: deviceId });
			if (!(backend instanceof RemoteBackend)) return { status: "uncertain", operationId };
			const rpc = backend.editorWriteRpc();
			try {
				const request = remoteQueryRequest(operation, effect);
				const proof = await rpc.call<EditorQueryResult>("editor.query", request, actor.signal);
				await actor.authorizeOperation(deviceId);
				if (
					proof?.operationId !== operationId ||
					!proof.result ||
					proof.result.outcome !== proof.outcome ||
					(proof.outcome !== "committed" && proof.outcome !== "rejected")
				)
					return { status: "uncertain", operationId };
				const intended = effect.intendedAfterStateJson;
				if (intended.kind !== "regular") return { status: "uncertain", operationId };
				validateEditorApplyResult(
					proof.result,
					{ operationId },
					intended.blob.digest,
					intended.blob.sizeBytes,
				);
				runtime.settleEditorReceipt(operation.id, proof.result);
				// ACK failure never downgrades the durable Host result or replays apply.
				await actor.authorizeOperation(deviceId);
				await rpc
					.call("editor.query", { ...request, acknowledgeTerminal: true }, actor.signal)
					.catch((error) => logger.debug("Editor engine ACK deferred", { error: String(error) }));
			} finally {
				await rpc.close();
			}
		} catch (error) {
			await actor.authorizeOperation(deviceId);
			logger.debug("Editor remote query unavailable", { operationId, error: String(error) });
		}
		await actor.authorizeOperation(deviceId);
		const latest = runtime.evidence.getOperation(operation.id);
		const observed = runtime.evidence.getEffect(operation.id, effect.mutationId);
		return latest && observed
			? remoteResult(latest, observed)
			: { status: "uncertain", operationId };
	}
	if (operation.settlement !== "settled") return { status: "uncertain", operationId };
	if (operation.executionOutcome !== "succeeded") return { status: "failed", operationId };
	if (
		!recovery ||
		recovery.operationId !== operationId ||
		recovery.userId !== actor.userId ||
		recovery.narratorId !== actor.narratorId
	)
		return {
			status: "uncertain",
			operationId,
			error: {
				code: "EDITOR_RESULT_UNAVAILABLE",
				message:
					"The write settled, but its saved snapshot metadata is unavailable; verify before retrying",
			},
		};
	const after = effects[0]?.observedAfterStateJson;
	if (
		effects.length !== 1 ||
		after?.kind !== "regular" ||
		after.blob.digest !== recovery.rawDigest ||
		after.blob.sizeBytes !== recovery.bytes
	)
		return { status: "uncertain", operationId };
	return {
		status: "saved",
		operationId,
		result: {
			status: "saved",
			operationId,
			...(recovery.hashKind ? { hashKind: recovery.hashKind } : {}),
			hash: recovery.hash,
			bytes: recovery.bytes,
			snapshotRevision: recovery.snapshotRevision,
		},
	};
};

export function editorRuntimeUnavailable(error: unknown): never {
	if (error instanceof AppError) throw error;
	throw new AppError(
		"Editor operation receipt is unavailable; verify before retrying",
		503,
		"EDITOR_OPERATION_UNKNOWN",
	);
}
