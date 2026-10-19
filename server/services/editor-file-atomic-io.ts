import { createHash } from "node:crypto";
import { EDITOR_FILE_MAX_BYTES } from "../../shared/editor-document";
import type {
	EditorApplyRequest,
	EditorApplyResult,
	EditorStageHandle,
} from "../../shared/editor-provider-contracts";
import type { EditorRpc, FsStatResult } from "../lib/agent/execution/rpc-types";
import { AppError } from "../lib/errors";
import type { FileChangeLocalIo, LocalFileApplyResult } from "./file-change-local-io";

export interface EditorAtomicIoOptions {
	read: FileChangeLocalIo["read"];
	/** Bound to the original device; calls share its existing multiplexed connection. */
	rpc: EditorRpc;
	/** Synchronous, invocation-local transfer; never another receipt store/cache.
	 * `query` is the exact executor query/ACK binding for this receipt. */
	onReceipt?(receipt: EditorApplyResult, query: EditorReceiptQuery, hostOperationId: string): void;
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
/** Separate from UI read/watch/draft keys; recoverable entirely from the original journal. */
export function editorWriterResourceKey(namespace: string, operationId: string, effectId: string) {
	return createHash("sha256")
		.update(JSON.stringify(["editor-write-resource-v1", namespace, operationId, effectId]))
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
/** Remote only. Observation, intent, coordinator and settlement stay in the existing runtime. */
export function createEditorFileAtomicIo(options: EditorAtomicIoOptions): FileChangeLocalIo {
	return {
		read: options.read,
		async apply(input): Promise<LocalFileApplyResult> {
			const parentEffects = { createdPaths: [], possiblePaths: [] };
			let dispatched = false;
			let stageId: string | undefined;
			const editor = input.editor;
			if (
				!editor ||
				input.backend.kind !== "remote" ||
				editor.intended.kind !== "regular" ||
				(editor.before.kind !== "regular" && editor.before.kind !== "absent")
			)
				return {
					kind: "not_applied",
					parentEffects,
					error: reject("Remote editor requires verified before/intended evidence"),
				};
			const intended = editor.intended;
			const namespace = editorOperationNamespace(
				editor.scope.sourceInstanceId,
				editor.userId,
				input.backend.deviceId,
			);
			const owner = {
				namespace,
				resourceKey: editorWriterResourceKey(namespace, editor.operationId, editor.effectId),
			};
			try {
				input.signal.throwIfAborted();
				await input.assertTarget();
				if (input.before.mode !== null && (input.before.mode & 0o222) === 0)
					throw reject("File is read-only");
				if (input.nextBytes.length > EDITOR_FILE_MAX_BYTES)
					throw reject("Editor output exceeds 20 MiB");
				const { rpc } = options;
				const root = await rpc.call<FsStatResult>(
					"fs.stat",
					{
						path: editor.scope.canonicalRoot,
						editorIdentity: true,
						editorRoot: editor.scope.canonicalRoot,
					},
					input.signal,
				);
				if (
					!root.isDirectory ||
					!root.rootIdentity ||
					!root.runtimeEpoch ||
					root.canonicalRoot !== editor.scope.canonicalRoot ||
					root.runtimeEpoch !== editor.executionBinding.runtimeEpoch
				)
					throw reject("Editor root/session identity changed");
				const parent = await rpc.call<FsStatResult>(
					"fs.stat",
					{
						path: input.backend.paths.dirname(input.canonicalPath),
						editorIdentity: true,
						editorRoot: editor.scope.canonicalRoot,
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
					{
						...owner,
						stageId,
						totalBytes: input.nextBytes.length,
						rawDigest: intended.blob.digest,
					},
					input.signal,
				);
				const request: EditorApplyRequest = {
					...owner,
					operationId: editor.sourceId,
					requestDigest: editor.requestDigest,
					stageId,
					identity: {
						deviceId: input.backend.deviceId,
						pathFlavor: editor.scope.pathFlavor,
						canonicalPath: input.canonicalPath,
						workspaceIdentity: root.rootIdentity,
						runtimeEpoch: root.runtimeEpoch,
						runtimeGeneration: editor.executionBinding.runtimeGeneration,
						fencingToken: editor.executionBinding.fencingToken,
					},
					expectedRawHash: editor.before.kind === "absent" ? null : editor.before.blob.digest,
					metadataPolicy: "preserve",
				};
				await input.assertTarget();
				input.signal.throwIfAborted();
				input.onDispatch();
				dispatched = true;
				const receipt = validateEditorApplyResult(
					await rpc.call<EditorApplyResult>("editor.apply", request, input.signal),
					request,
					intended.blob.digest,
					input.nextBytes.length,
				);
				options.onReceipt?.(
					receipt,
					{
						namespace,
						resourceKey: owner.resourceKey,
						operationId: request.operationId,
						requestDigest: request.requestDigest,
					},
					editor.operationId,
				);
				if (receipt.outcome === "committed") return { kind: "applied", error: null, parentEffects };
				return {
					kind: receipt.outcome === "rejected" ? "not_applied" : "target_mutation_unknown",
					parentEffects,
					error: reject(
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
				if (stageId && !dispatched)
					await options.rpc
						.call("editor.stage.abort", { ...owner, stageId, reason: "cancel" })
						.catch(() => {});
			}
		},
	};
}
