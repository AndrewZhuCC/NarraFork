import { createHash } from "node:crypto";
import { setImmediate as yieldToEventLoop } from "node:timers/promises";
import { EDITOR_FILE_MAX_BYTES } from "../../shared/editor-document";
import {
	EDITOR_TARGET_QUEUE_FEATURE,
	type EditorApplyRequest,
	type EditorApplyResult,
	type EditorHelperCapabilities,
	type EditorStageHandle,
} from "../../shared/editor-provider-contracts";
import type { EditorRpc, FsStatResult } from "../lib/agent/execution/rpc-types";
import { AppError } from "../lib/errors";
import type { FileChangeLocalIo, LocalFileApplyResult } from "./file-change-local-io";
import { readOrInitializeFileChangeSourceInstanceId } from "./file-change-runtime";

export interface EditorAtomicIoOptions {
	/** Trusted provider binding; the remote device whose RPC this adapter uses. */
	deviceId: string;
	read: FileChangeLocalIo["read"];
	/** Bound to the original device; calls share its existing multiplexed connection. */
	rpc: EditorRpc;
	/** Synchronous, invocation-local transfer; never another receipt store/cache.
	 * `query` is the exact executor query/ACK binding for this receipt. */
	onReceipt?(receipt: EditorApplyResult, query: EditorReceiptQuery): void;
}
export type EditorReceiptQuery = Pick<
	EditorApplyRequest,
	"namespace" | "resourceKey" | "operationId" | "requestDigest"
>;
export function editorOperationNamespace(
	sourceInstanceId: string,
	userId: string,
	deviceId: string,
) {
	return createHash("sha256")
		.update(JSON.stringify(["editor-native-v1", sourceInstanceId, userId, deviceId]))
		.digest("hex");
}
/** Separate from UI read/watch/draft keys. Recovery re-derives it from the original
 * editor operationId alone, so it never depends on Host history being available. */
export function editorWriterResourceKey(namespace: string, operationId: string) {
	return createHash("sha256")
		.update(JSON.stringify(["editor-write-resource-v2", namespace, operationId]))
		.digest("hex");
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
/** Remote only. The executor's exact-op receipt is the real outcome; optional Host
 * history never grants or blocks dispatch, and no Host-side receipt store exists. */
export function createEditorFileAtomicIo(options: EditorAtomicIoOptions): FileChangeLocalIo {
	return {
		deviceId: options.deviceId,
		read: options.read,
		async apply(input): Promise<LocalFileApplyResult> {
			const parentEffects = { createdPaths: [], possiblePaths: [] };
			let dispatched = false;
			let stageId: string | undefined;
			const editor = input.editor;
			if (!editor || input.backend.kind !== "remote" || input.backend.deviceId !== options.deviceId)
				return {
					kind: "not_applied",
					parentEffects,
					error: reject("Remote editor requires its device-bound provider"),
				};
			const { rpc } = options;
			let owner: Pick<EditorApplyRequest, "namespace" | "resourceKey"> | undefined;
			try {
				input.signal.throwIfAborted();
				await input.assertTarget();
				if (input.before.mode !== null && (input.before.mode & 0o222) === 0)
					throw reject("File is read-only");
				if (
					input.nextBytes.length > EDITOR_FILE_MAX_BYTES ||
					(input.before.bytes?.length ?? 0) > EDITOR_FILE_MAX_BYTES
				)
					throw reject("Editor bytes exceed 20 MiB");
				if (input.backend.pathFlavor !== "posix" && input.backend.pathFlavor !== "windows")
					throw reject("Editor filesystem path grammar required");
				// Digests come from the actual bytes, so a history outage cannot block a save.
				const intendedDigest = await rawDigest(input.nextBytes, input.signal);
				const beforeDigest =
					input.before.bytes === null ? null : await rawDigest(input.before.bytes, input.signal);
				if (
					(editor.rawDigest !== undefined && editor.rawDigest !== intendedDigest) ||
					(editor.beforeRawDigest !== undefined && editor.beforeRawDigest !== beforeDigest)
				)
					throw reject("Editor preparation digest changed");
				const namespace = editorOperationNamespace(
					editor.sourceInstanceId ?? (await readOrInitializeFileChangeSourceInstanceId()),
					editor.userId,
					input.backend.deviceId,
				);
				owner = {
					namespace,
					resourceKey: editorWriterResourceKey(namespace, editor.sourceId),
				};
				const capabilities = await rpc.call<EditorHelperCapabilities>(
					"editor.capabilities",
					{},
					input.signal,
				);
				if (
					capabilities?.runtimeEpoch !== editor.runtime.runtimeEpoch ||
					!capabilities.features?.includes(EDITOR_TARGET_QUEUE_FEATURE)
				)
					throw reject(
						"Upgrade the executor: editor.target-queue.v1 is required",
						"EDITOR_PROVIDER_UNSUPPORTED",
					);
				const root = await rpc.call<FsStatResult>(
					"fs.stat",
					{ path: editor.root, editorIdentity: true, editorRoot: editor.root },
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
				const stage = await rpc.call<EditorStageHandle>(
					"editor.stage.open",
					{ ...owner, maxBytes: input.nextBytes.length, idleTimeoutMs: 10_000 },
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
					{ ...owner, stageId, totalBytes: input.nextBytes.length, rawDigest: intendedDigest },
					input.signal,
				);
				const request: EditorApplyRequest = {
					...owner,
					coordination: "target-queue-v1",
					operationId: editor.sourceId,
					requestDigest: editor.requestDigest,
					stageId,
					identity: {
						deviceId: input.backend.deviceId,
						pathFlavor: input.backend.pathFlavor,
						canonicalPath: input.canonicalPath,
						workspaceIdentity: root.rootIdentity,
						runtimeEpoch: root.runtimeEpoch,
						runtimeGeneration: editor.runtime.runtimeGeneration,
						// Connection identity only; coordination retires persistent fence admission.
						fencingToken: editor.runtime.runtimeGeneration,
					},
					expectedRawHash: beforeDigest,
					metadataPolicy: "preserve",
				};
				// Staging can outlive the initial permission check: reauthorize right before dispatch.
				await input.assertTarget();
				input.signal.throwIfAborted();
				input.onDispatch();
				dispatched = true;
				const receipt = validateEditorApplyResult(
					await rpc.call<EditorApplyResult>("editor.apply", request, input.signal),
					request,
					intendedDigest,
					input.nextBytes.length,
				);
				options.onReceipt?.(receipt, {
					...owner,
					operationId: request.operationId,
					requestDigest: request.requestDigest,
				});
				return {
					kind:
						receipt.outcome === "committed"
							? "applied"
							: receipt.outcome === "rejected"
								? "not_applied"
								: "target_mutation_unknown",
					editorReceipt: receipt,
					parentEffects,
					error:
						receipt.outcome === "committed"
							? null
							: reject(
									receipt.error?.message ?? "Editor mutation outcome unknown",
									receipt.error?.code,
								),
				};
			} catch (error) {
				return {
					kind: dispatched ? "target_mutation_unknown" : "not_applied",
					parentEffects,
					error,
				};
			} finally {
				// Real IO stays owned by the executor's target queue, even after cancellation.
				if (stageId && owner && !dispatched)
					await rpc
						.call(
							"editor.stage.abort",
							{ ...owner, stageId, reason: "cancel" },
							AbortSignal.timeout(5_000),
						)
						.catch(() => {});
			}
		},
	};
}
