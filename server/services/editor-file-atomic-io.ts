import { createHash } from "node:crypto";
import { join } from "node:path";
import { setImmediate as yieldToEventLoop } from "node:timers/promises";
import { EDITOR_FILE_MAX_BYTES } from "../../shared/editor-document";
import {
	EDITOR_TARGET_QUEUE_FEATURE,
	type EditorApplyRequest,
	type EditorApplyResult,
	type EditorHelperCapabilities,
	type EditorStageHandle,
	type EditorWriteOwner,
} from "../../shared/editor-provider-contracts";
import type { FsStatResult } from "../lib/agent/execution/rpc-types";
import { AppError } from "../lib/errors";
import { logger } from "../lib/logger";
import { getNarraforkHome } from "../lib/narrafork-home";
import {
	type EditorHelperResolver,
	EditorHelperSession,
	type EditorRpc,
	resolveEditorHelper,
	withEditorHelperMetadata,
} from "./editor-helper";
import {
	type EditorRemoteOperationStore,
	editorOperationNamespace,
} from "./editor-remote-operation-store";
import {
	type FileChangeLocalIo,
	fileChangeLocalIo,
	type LocalFileApplyInput,
	type LocalFileApplyResult,
} from "./file-change-local-io";
import { readOrInitializeFileChangeSourceInstanceId } from "./file-change-runtime";

export interface EditorAtomicIoOptions {
	deviceId?: string;
	resourceKey: string;
	store: EditorRemoteOperationStore;
	read?: FileChangeLocalIo["read"];
	open?(input: LocalFileApplyInput): Promise<EditorRpc>;
	resolveHelper?: EditorHelperResolver;
	stateDir?: string;
}
function reject(message: string, code = "EDITOR_APPLY_REJECTED") {
	return new AppError(message, 409, code);
}
export function validateEditorApplyResult(
	result: EditorApplyResult,
	request: Pick<EditorApplyRequest, "operationId">,
	digest: string,
	bytes: number,
) {
	if (
		!result ||
		result.operationId !== request.operationId ||
		!["committed", "rejected", "uncertain"].includes(result.outcome) ||
		(result.outcome === "committed" &&
			(result.rawDigest !== digest || result.totalBytes !== bytes)) ||
		(result.outcome !== "committed" && (result.rawDigest !== null || result.totalBytes !== null))
	)
		throw new Error("Invalid editor durable receipt");
	return result;
}
/** Bound hashing work per turn; never publish blobs merely to obtain a CAS digest. */
async function rawDigest(bytes: Uint8Array, signal: AbortSignal): Promise<string> {
	const digest = createHash("sha256");
	for (let offset = 0; offset < bytes.length; offset += 64 * 1024) {
		signal.throwIfAborted();
		digest.update(bytes.subarray(offset, offset + 64 * 1024));
		if (offset % (1024 * 1024) === 0) await yieldToEventLoop();
	}
	signal.throwIfAborted();
	return digest.digest("hex");
}
/** A provider owns actual execution; optional file history never grants or blocks dispatch. */
export function createEditorFileAtomicIo(options: EditorAtomicIoOptions): FileChangeLocalIo {
	return {
		deviceId: options.deviceId ?? "local",
		read:
			options.read ??
			((path, signal) => {
				if (options.deviceId && options.deviceId !== "local")
					return Promise.reject(
						reject("Remote editor read provider unavailable", "EDITOR_PROVIDER_UNSUPPORTED"),
					);
				return fileChangeLocalIo.read(path, signal, EDITOR_FILE_MAX_BYTES);
			}),
		async apply(input): Promise<LocalFileApplyResult> {
			const parentEffects = { createdPaths: [], possiblePaths: [] };
			let dispatched = false;
			let associated = false;
			let rpc: EditorRpc | undefined;
			let stageId: string | undefined;
			let owner: EditorWriteOwner | undefined;
			const editor = input.editor;
			if (!editor)
				return { kind: "not_applied", parentEffects, error: reject("Editor context required") };
			try {
				input.signal.throwIfAborted();
				await input.assertTarget();
				if (input.backend.deviceId !== "local" && (!options.open || !options.read))
					throw reject(
						"Remote editor requires explicit provider IO",
						"EDITOR_PROVIDER_UNSUPPORTED",
					);
				if (input.before.mode !== null && (input.before.mode & 0o222) === 0)
					throw reject("File is read-only");
				if (
					input.nextBytes.length > EDITOR_FILE_MAX_BYTES ||
					(input.before.bytes?.length ?? 0) > EDITOR_FILE_MAX_BYTES
				)
					throw reject("Editor bytes exceed 20 MiB");
				const intendedDigest = await rawDigest(input.nextBytes, input.signal);
				const beforeDigest =
					input.before.bytes === null ? null : await rawDigest(input.before.bytes, input.signal);
				if (
					(editor.rawDigest !== undefined && editor.rawDigest !== intendedDigest) ||
					(editor.beforeRawDigest !== undefined && editor.beforeRawDigest !== beforeDigest)
				)
					throw reject("Editor preparation digest changed");
				owner = {
					namespace: editorOperationNamespace(
						editor.sourceInstanceId ?? (await readOrInitializeFileChangeSourceInstanceId()),
						editor.userId,
						input.backend.deviceId,
					),
					resourceKey: options.resourceKey,
				};
				if (options.open) rpc = await options.open(input);
				else
					rpc = await EditorHelperSession.open(
						options.resolveHelper ?? resolveEditorHelper,
						editor.root,
						options.stateDir ?? join(getNarraforkHome(), "editor-native"),
						editor.runtime.runtimeEpoch,
						{ signal: input.signal },
					);
				const capabilities = await rpc.call<EditorHelperCapabilities>(
					"editor.capabilities",
					{},
					input.signal,
				);
				if (
					capabilities.runtimeEpoch !== editor.runtime.runtimeEpoch ||
					!capabilities.features?.includes(EDITOR_TARGET_QUEUE_FEATURE)
				)
					throw reject(
						"Upgrade the executor: editor.target-queue.v1 is required",
						"EDITOR_PROVIDER_UNSUPPORTED",
					);
				const root = await rpc.call<FsStatResult>(
					"fs.stat",
					{
						path: editor.root,
						editorIdentity: true,
						editorRoot: editor.root,
					},
					input.signal,
				);
				if (
					!root.isDirectory ||
					!root.rootIdentity ||
					!root.runtimeEpoch ||
					root.canonicalRoot !== editor.root ||
					root.runtimeEpoch !== editor.runtime.runtimeEpoch ||
					(editor.rootIdentity !== undefined && root.rootIdentity !== editor.rootIdentity)
				)
					throw reject("Editor root/session identity changed");
				const parent = await rpc.call<FsStatResult>(
					"fs.stat",
					{
						path: input.backend.paths.dirname(input.canonicalPath),
						editorIdentity: true,
						editorRoot: editor.root,
					},
					input.signal,
				);
				if (!parent.isDirectory) throw reject("Editor target parent must already exist");
				if (input.backend.pathFlavor !== "posix" && input.backend.pathFlavor !== "windows")
					throw reject("Editor filesystem path grammar required");
				const identity = {
					deviceId: input.backend.deviceId,
					pathFlavor: input.backend.pathFlavor,
					canonicalPath: input.canonicalPath,
					workspaceIdentity: root.rootIdentity,
					runtimeEpoch: root.runtimeEpoch,
					runtimeGeneration: editor.runtime.runtimeGeneration,
					// Connection identity only. coordination explicitly retires persistent fence admission.
					fencingToken: editor.runtime.runtimeGeneration,
				};
				const stage = await rpc.call<EditorStageHandle>(
					"editor.stage.open",
					{
						...owner,
						maxBytes: EDITOR_FILE_MAX_BYTES,
						idleTimeoutMs: 10_000,
					},
					input.signal,
				);
				if (
					!stage?.stageId ||
					stage.stageId.length > 128 ||
					stage.resourceKey !== owner.resourceKey
				)
					throw reject("Invalid editor stage");
				stageId = stage.stageId;
				let seq = 0;
				for (let offset = 0; offset < input.nextBytes.length; offset += 65536) {
					const chunk = input.nextBytes.subarray(offset, offset + 65536);
					await rpc.call(
						"editor.stage.append",
						{
							...owner,
							stageId,
							seq: seq++,
							offset,
							bytes: chunk.length,
							digest: createHash("sha256").update(chunk).digest("hex"),
							dataB64: Buffer.from(chunk).toString("base64"),
						},
						input.signal,
					);
				}
				await rpc.call(
					"editor.stage.seal",
					{
						...owner,
						stageId,
						totalBytes: input.nextBytes.length,
						rawDigest: intendedDigest,
					},
					input.signal,
				);
				const request: EditorApplyRequest = {
					...owner,
					coordination: "target-queue-v1",
					operationId: editor.sourceId,
					requestDigest: editor.requestDigest,
					stageId,
					identity,
					expectedRawHash: beforeDigest,
					metadataPolicy: "preserve",
				};
				const saved = editor.preparedResult as { hash: string; bytes: number };
				if (
					!saved ||
					!/^[a-f0-9]{64}$/.test(saved.hash) ||
					saved.bytes !== input.nextBytes.length ||
					!Number.isSafeInteger(editor.snapshotRevision) ||
					editor.snapshotRevision < 0
				)
					throw reject("Missing editor snapshot recovery metadata");
				await withEditorHelperMetadata(rpc, () =>
					options.store.put({
						version: 2,
						createdAt: Date.now(),
						userId: editor.userId,
						narratorId: editor.narratorId,
						...(editor.historyOperationId ? { historyOperationId: editor.historyOperationId } : {}),
						saved: {
							hash: saved.hash,
							bytes: saved.bytes,
							snapshotRevision: editor.snapshotRevision,
							...(editor.hashKind ? { hashKind: editor.hashKind } : {}),
						},
						deviceId: input.backend.deviceId,
						operationId: editor.sourceId,
						requestDigest: editor.requestDigest,
						...(owner as EditorWriteOwner),
						root: editor.root,
						identity,
						rawDigest: intendedDigest,
						bytes: input.nextBytes.length,
					}),
				);
				associated = true;
				await input.assertTarget();
				input.signal.throwIfAborted();
				input.onDispatch();
				dispatched = true;
				const result = validateEditorApplyResult(
					await rpc.call<EditorApplyResult>("editor.apply", request, input.signal),
					request,
					intendedDigest,
					input.nextBytes.length,
				);
				if (result.outcome !== "uncertain") {
					try {
						await withEditorHelperMetadata(rpc, () =>
							options.store.markSettled(editor.sourceId, result),
						);
						await rpc.call(
							"editor.query",
							{
								...owner,
								operationId: editor.sourceId,
								requestDigest: editor.requestDigest,
								acknowledgeTerminal: true,
							},
							AbortSignal.timeout(5_000),
						);
					} catch (error) {
						// The engine retains its unacknowledged proof; a valid receipt remains the real outcome.
						logger.debug("Editor receipt acknowledgement deferred", { error: String(error) });
					}
				}
				return {
					kind:
						result.outcome === "committed"
							? "applied"
							: result.outcome === "rejected"
								? "not_applied"
								: "target_mutation_unknown",
					editorReceipt: result,
					parentEffects,
					error:
						result.outcome === "committed"
							? null
							: reject(
									result.error?.message ?? "Editor mutation outcome unknown",
									result.error?.code,
								),
				};
			} catch (error) {
				return {
					kind: dispatched ? "target_mutation_unknown" : "not_applied",
					parentEffects,
					error,
				};
			} finally {
				if (rpc && stageId && !dispatched)
					await rpc
						.call(
							"editor.stage.abort",
							{ ...owner, stageId, reason: "cancel" },
							AbortSignal.timeout(5_000),
						)
						.catch(() => {});
				if (associated && !dispatched) {
					await rpc?.close();
					rpc = undefined;
					await options.store
						.markSettled(editor.sourceId, {
							operationId: editor.sourceId,
							outcome: "rejected",
							rawDigest: null,
							totalBytes: null,
							error: {
								code: "EDITOR_NOT_DISPATCHED",
								message: "Host rejected before editor.apply dispatch",
							},
						})
						.catch(() => {});
				}
				// Local helpers join child close, not merely RPC rejection. Remote real IO remains
				// owned by the negotiated executor target queue, even after transport cancellation.
				await rpc?.close();
			}
		},
	};
}
