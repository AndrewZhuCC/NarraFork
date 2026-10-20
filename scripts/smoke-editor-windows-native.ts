import { strict as assert } from "node:assert";
import { createHash, randomUUID } from "node:crypto";
import { chmod, mkdir, readFile, realpath, rename, writeFile } from "node:fs/promises";
import { join } from "node:path";
import type { ExecutionBackend } from "../server/lib/agent/execution/backend";
import { targetPathSemantics } from "../server/lib/agent/execution/path-semantics";
import type { EditorRpc, FsStatResult } from "../server/lib/agent/execution/rpc-types";
import { EditorNativeSession } from "../server/routes/editor-native-rpc-fixture";
import {
	createEditorFileAtomicIo,
	editorOperationNamespace,
	editorWriterResourceKey,
} from "../server/services/editor-file-atomic-io";
import {
	fileChangeLocalIo,
	type LocalFileApplyInput,
} from "../server/services/file-change-local-io";
import type {
	EditorApplyResult,
	EditorQueryResult,
	EditorStageHandle,
} from "../shared/editor-provider-contracts";

// Explicit precompiled protocol fixture, not a production Host transport or fallback.
assert(process.platform === "win32" || process.argv[5] === "--allow-posix-fixture");
const windows = process.platform === "win32",
	artifactRoot = process.argv[2],
	expectedSha = process.argv[3],
	root = process.argv[4] ?? artifactRoot;
assert(artifactRoot && root && /^[a-f0-9]{64}$/.test(expectedSha ?? ""));
const binary = join(artifactRoot, "editor-helper.exe");
const hash = (value: Uint8Array | string) => createHash("sha256").update(value).digest("hex");
assert.equal(hash(await readFile(binary)), expectedSha);
const workspace = join(root, "workspace"),
	state = join(root, "engine"),
	epoch = "native-protocol-fixture";
await mkdir(workspace, { recursive: true });
const canonical = await realpath(workspace),
	target = join(canonical, "target.txt");
const flavor = windows ? "windows" : "posix";
// Direct native protocol adapter only. Full authenticated RemoteBackend + SQLite
// persistence is independently covered by editor-document-remote-persistence.test.ts.
const backend = {
	kind: "remote",
	deviceId: "local",
	pathFlavor: flavor,
	paths: targetPathSemantics(flavor),
} as ExecutionBackend;
let fence = 0,
	dispatches = 0,
	applies = 0;
const peer = () => EditorNativeSession.open(binary, canonical, state, epoch);
async function input(bytes: Uint8Array): Promise<LocalFileApplyInput> {
	const before = await fileChangeLocalIo.read(target),
		id = randomUUID();
	assert(before.bytes);
	return {
		backend,
		lexicalPath: target,
		canonicalPath: target,
		before,
		nextBytes: bytes,
		signal: AbortSignal.timeout(30_000),
		assertTarget: async () => {
			assert.equal(await realpath(workspace), canonical);
		},
		onDispatch: () => {
			dispatches++;
		},
		editor: {
			sourceId: id,
			userId: "fixture-user",
			sourceInstanceId: "fixture-source",
			snapshotRevision: ++fence,
			requestDigest: hash(id),
			root: canonical,
			runtime: { runtimeEpoch: epoch, runtimeGeneration: 0 },
			rawDigest: hash(bytes),
			beforeRawDigest: hash(before.bytes),
		},
	};
}
function owner(value: LocalFileApplyInput) {
	assert(value.editor?.sourceInstanceId);
	const namespace = editorOperationNamespace(
		value.editor.sourceInstanceId,
		value.editor.userId,
		"local",
	);
	return {
		namespace,
		resourceKey: editorWriterResourceKey(namespace, value.editor.sourceId),
		operationId: value.editor.sourceId,
		requestDigest: value.editor.requestDigest,
	};
}
async function apply(value: LocalFileApplyInput, drop = false) {
	const session = await peer();
	const rpc: EditorRpc = {
		call: async <T>(method: string, params: object, signal?: AbortSignal) => {
			const result = await session.call<T>(method, params, signal);
			if (method === "editor.apply") {
				applies++;
				if (drop) throw new Error("Fixture lost committed reply");
			}
			return result;
		},
	};
	try {
		return await createEditorFileAtomicIo({
			deviceId: "local",
			read: fileChangeLocalIo.read,
			rpc,
		}).apply(value);
	} finally {
		await session.close();
	}
}
async function query(value: LocalFileApplyInput) {
	const session = await peer();
	try {
		const request = owner(value),
			proof = await session.call<EditorQueryResult>("editor.query", request);
		assert.equal(proof.outcome, "committed");
		assert.equal(proof.result?.rawDigest, hash(value.nextBytes));
		assert.equal(proof.result?.totalBytes, value.nextBytes.length);
		await assert.rejects(() =>
			session.call("editor.query", { ...request, requestDigest: hash("wrong") }),
		);
		await assert.rejects(() =>
			session.call("editor.query", { ...request, resourceKey: "wrong-resource" }),
		);
		const foreign = await session.call<EditorQueryResult>("editor.query", {
			...request,
			namespace: "wrong-owner",
		});
		assert.equal(foreign.outcome, "not_found");
		assert.equal(foreign.result, null);
		await session.call("editor.query", { ...request, acknowledgeTerminal: true });
		return proof;
	} finally {
		await session.close();
	}
}
await writeFile(target, "before");
const large = new Uint8Array(20 * 1024 * 1024).fill(65);
for (const bytes of [new Uint8Array(), new TextEncoder().encode("abc"), large]) {
	const value = await input(bytes);
	assert.equal((await apply(value)).kind, "applied");
	assert.equal(hash(await readFile(target)), hash(bytes));
	await query(value);
}
const utf16 = Buffer.concat([Buffer.from([255, 254]), Buffer.from("after\r\n", "utf16le")]);
const lost = await input(utf16),
	beforeApplies = applies;
assert.equal((await apply(lost, true)).kind, "target_mutation_unknown");
assert.equal(applies - beforeApplies, 1);
const recovered = await query(lost);
assert.notEqual(hash("after\r\n"), hash(utf16));
assert.deepEqual(await readFile(target), utf16);
const next = await input(new TextEncoder().encode("next"));
assert.equal((await apply(next)).kind, "applied");
await query(next);
await chmod(target, 0o444);
const denied = await input(new Uint8Array([65])),
	beforeDispatches = dispatches;
assert.equal((await apply(denied)).kind, "not_applied");
assert.equal(dispatches, beforeDispatches);
assert.equal((await readFile(target)).toString(), "next");
// Windows has a native read-only attribute; retain the original Windows-only
// protocol assertion. POSIX mode-bit refusal above belongs to the Host IO fence.
if (windows) {
	const readonlyPeer = await peer();
	try {
		const binding = { namespace: "native-readonly", resourceKey: "native-readonly-resource" },
			body = new Uint8Array([65]);
		const stage = await readonlyPeer.call<EditorStageHandle>("editor.stage.open", {
			...binding,
			maxBytes: 1,
			idleTimeoutMs: 10000,
		});
		await readonlyPeer.call("editor.stage.append", {
			...binding,
			stageId: stage.stageId,
			seq: 0,
			offset: 0,
			bytes: 1,
			digest: hash(body),
			dataB64: Buffer.from(body).toString("base64"),
		});
		await readonlyPeer.call("editor.stage.seal", {
			...binding,
			stageId: stage.stageId,
			totalBytes: 1,
			rawDigest: hash(body),
		});
		const identity = await readonlyPeer.call<FsStatResult>("fs.stat", {
			path: canonical,
			editorIdentity: true,
			editorRoot: canonical,
		});
		const result = await readonlyPeer.call<EditorApplyResult>("editor.apply", {
			...binding,
			coordination: "target-queue-v1",
			stageId: stage.stageId,
			operationId: randomUUID(),
			requestDigest: hash("readonly"),
			expectedRawHash: hash("next"),
			metadataPolicy: "preserve",
			identity: {
				deviceId: "local",
				pathFlavor: flavor,
				canonicalPath: target,
				workspaceIdentity: identity.rootIdentity,
				runtimeEpoch: epoch,
				runtimeGeneration: 0,
				fencingToken: ++fence,
			},
		});
		assert.equal(result.outcome, "rejected");
	} finally {
		await readonlyPeer.close();
		await chmod(target, 0o666);
	}
} else await chmod(target, 0o666);
// A stale native root identity cannot authorize a recreated directory with the same path.
const rootPeer = await peer();
try {
	const before = await rootPeer.call<FsStatResult>("fs.stat", {
		path: canonical,
		editorIdentity: true,
		editorRoot: canonical,
	});
	const binding = { namespace: "root-replacement", resourceKey: "root-replacement-resource" },
		body = new Uint8Array([65]);
	const stage = await rootPeer.call<EditorStageHandle>("editor.stage.open", {
		...binding,
		maxBytes: 1,
		idleTimeoutMs: 10000,
	});
	await rootPeer.call("editor.stage.append", {
		...binding,
		stageId: stage.stageId,
		seq: 0,
		offset: 0,
		bytes: 1,
		digest: hash(body),
		dataB64: Buffer.from(body).toString("base64"),
	});
	await rootPeer.call("editor.stage.seal", {
		...binding,
		stageId: stage.stageId,
		totalBytes: 1,
		rawDigest: hash(body),
	});
	await rename(workspace, `${workspace}-retired`);
	await mkdir(workspace);
	await writeFile(target, "replacement");
	const replacedRequest = {
		...binding,
		coordination: "target-queue-v1",
		stageId: stage.stageId,
		operationId: randomUUID(),
		requestDigest: hash("root-replaced"),
		expectedRawHash: hash("replacement"),
		metadataPolicy: "preserve",
		identity: {
			deviceId: "local",
			pathFlavor: flavor,
			canonicalPath: target,
			workspaceIdentity: before.rootIdentity,
			runtimeEpoch: epoch,
			runtimeGeneration: 0,
			fencingToken: ++fence,
		},
	};
	// The connection binding refuses a replaced root before any intent is recorded;
	// a rejected receipt is equally acceptable. Neither may touch the new target.
	const rejected = await rootPeer
		.call<EditorApplyResult>("editor.apply", replacedRequest)
		.catch((error: Error) => {
			assert.match(error.message, /EDITOR_IDENTITY_CHANGED/);
			return { outcome: "refused" as const };
		});
	assert.ok(rejected.outcome === "rejected" || rejected.outcome === "refused");
	assert.equal((await readFile(target)).toString(), "replacement");
} finally {
	await rootPeer.close();
}
console.log(
	JSON.stringify({
		gate: windows ? "EDITOR_WINDOWS_NATIVE" : "EDITOR_COMPILED_NATIVE",
		bytes: large.length,
		actualPlatform: process.platform,
		binarySha256: expectedSha,
		dispatches,
		lostReplyApplies: applies - beforeApplies - 1,
		coldProof: recovered.outcome,
		separateTextRawHashes: hash("after\r\n") !== hash(utf16),
		rootReplacementRejected: true,
		readonlyMetadataRejected: windows,
	}),
);
