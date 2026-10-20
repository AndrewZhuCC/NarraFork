import { afterEach, beforeEach, expect, spyOn, test } from "bun:test";
import { createHash, randomUUID } from "node:crypto";
import { chmod, lstat, mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { drizzle } from "drizzle-orm/bun-sqlite";
import type { EditorApplyResult, EditorQueryResult } from "../../shared/editor-provider-contracts";
import { testEnvironment } from "../../tests/preload";
import { openSchemaCopyInMemory } from "../../tests/sqlite-schema-image";
import { db as appDb, sqlite as isolatedTemplate } from "../db";
import * as relations from "../db/relations";
import * as schema from "../db/schema";
import { safeSpawn } from "../lib/spawn";
import { nativeEditorFixture } from "../routes/editor-native-fixture";
import {
	executeAtomicEditorFileChange,
	queryEditorOperation,
	withEditorOperationStore,
} from "./editor-document-runtime";
import { EditorDocumentService } from "./editor-document-service";
import { createEditorFileAtomicIo } from "./editor-file-atomic-io";
import { EditorHelperSession, type EditorRpc, editorHelperDiagnostics } from "./editor-helper";
import { EditorRemoteOperationStore } from "./editor-remote-operation-store";
import { fileChangeLocalIo, type LocalFileApplyInput } from "./file-change-local-io";
import {
	type EditorFileChangeRequest,
	LocalFileChangeRuntime,
	localFileChangeRuntimeBinding,
	withLocalFileChangeRuntime,
} from "./file-change-runtime";
import { workspaceContextService } from "./workspace-context-service";
import { withProtectionReservation } from "./worktree-lifecycle-guard";

const hash = (bytes: Uint8Array | string) => createHash("sha256").update(bytes).digest("hex");
const binary = resolve(
	import.meta.dir,
	`../../.narrafork/editor-phase8-executor${process.platform === "win32" ? ".exe" : ""}`,
);
let root: string, workspace: string, target: string, state: string;
let store: EditorRemoteOperationStore;
let sqlite: ReturnType<typeof openSchemaCopyInMemory>;
let runtime: LocalFileChangeRuntime;
let db: ReturnType<typeof database>;
function database(sqlite: ReturnType<typeof openSchemaCopyInMemory>) {
	return drizzle({ client: sqlite, schema: { ...schema, ...relations } });
}
const resolver = async () => {
	const bytes = await readFile(binary).catch(() => {
		throw new Error("Prepare actual fixture first: bun scripts/build-editor-test-helper.ts");
	});
	return {
		path: binary,
		filename: "fixture-executor",
		version: "0.9.2",
		size: bytes.length,
		sha256: hash(bytes),
	};
};
beforeEach(async () => {
	root = await mkdtemp(join(testEnvironment.isolatedHome, "editor-native-"));
	workspace = join(root, "workspace");
	target = join(workspace, "target.txt");
	state = join(root, "native");
	await mkdir(workspace);
	store = new EditorRemoteOperationStore(join(root, "receipts"));
	sqlite = openSchemaCopyInMemory(isolatedTemplate);
	sqlite.exec("PRAGMA foreign_keys = ON; PRAGMA busy_timeout = 0;");
	db = database(sqlite);
	db.insert(schema.users)
		.values({
			id: "native-user",
			username: "native-user",
			passwordHash: "fixture",
			role: "user",
			createdAt: new Date().toISOString(),
		})
		.run();
	db.insert(schema.narrators)
		.values({
			id: "native-narrator",
			title: "native fixture",
			createdAt: new Date().toISOString(),
			updatedAt: new Date().toISOString(),
		})
		.run();
	runtime = makeRuntime();
});
afterEach(async () => {
	sqlite.close(true);
	await rm(root, { recursive: true, force: true });
});
function makeRuntime() {
	return new LocalFileChangeRuntime({
		db,
		privateRoot: join(root, "evidence"),
		blobStoreOptions: { minimumFreeBytes: 0 },
	});
}
function request(
	next: Uint8Array,
	base: string | null,
	overrides: Partial<EditorFileChangeRequest<{ hash: string; bytes: number }>> = {},
): EditorFileChangeRequest<{ hash: string; bytes: number }> {
	return {
		requestId: randomUUID(),
		userId: "native-user",
		narratorId: "native-narrator",
		cwd: workspace,
		canonicalPath: target,
		lexicalPath: target,
		signal: new AbortController().signal,
		input: { digest: hash(next), baseHash: base, snapshotRevision: 4, hashKind: "raw-sha256" },
		authorize: async () => {},
		construct: (before) => {
			if ((before.bytes === null ? null : hash(before.bytes)) !== base)
				throw new Error("STALE_WRITE");
			return { nextBytes: next, result: { hash: hash(next), bytes: next.length }, lineStats: null };
		},
		io: createEditorFileAtomicIo({
			resourceKey: "fixture-resource",
			store,
			resolveHelper: resolver,
			stateDir: state,
		}),
		...overrides,
	};
}
async function peer() {
	const binding = localFileChangeRuntimeBinding();
	if (!binding) throw new Error("Local runtime unavailable");
	const session = await EditorHelperSession.open(resolver, workspace, state, binding.runtimeEpoch);
	await session.capabilities();
	return session;
}
async function query(operationId: string) {
	const record = await store.get(operationId);
	if (!record) throw new Error("Missing host intent");
	const session = await peer();
	try {
		return {
			record,
			result: await session.call<EditorQueryResult>("editor.query", {
				namespace: record.namespace,
				resourceKey: record.resourceKey,
				operationId,
				requestDigest: record.requestDigest,
			}),
		};
	} finally {
		await session.close();
	}
}

test("actual native helper: replace preserves mode and extended attributes; tools keep original inode", async () => {
	await writeFile(target, "before");
	await chmod(target, 0o640);
	let aclBefore: string | undefined;
	if (process.platform === "linux") {
		const acl = await safeSpawn({
			cmd: ["setfacl", "-m", "u:65534:r--", target],
			timeout: 5000,
			maxOutputBytes: 4096,
		});
		expect(acl.exitCode).toBe(0);
		aclBefore = (
			await safeSpawn({
				cmd: ["getfacl", "--omit-header", target],
				timeout: 5000,
				maxOutputBytes: 4096,
			})
		).stdout;
		const xattr = await safeSpawn({
			cmd: [
				"python3",
				"-c",
				"import os,sys; os.setxattr(sys.argv[1], 'user.editor-fixture', b'preserved')",
				target,
			],
			timeout: 5000,
			maxOutputBytes: 4096,
		});
		expect(xattr.exitCode).toBe(0);
	}
	const initial = await lstat(target);
	await runtime.executeEditor(request(new TextEncoder().encode("after\r\n"), hash("before")));
	expect(await readFile(target, "utf8")).toBe("after\r\n");
	const changed = await lstat(target);
	expect(changed.ino).not.toBe(initial.ino);
	expect(changed.mode & 0o777).toBe(0o640);
	if (process.platform === "linux") {
		const acl = await safeSpawn({
			cmd: ["getfacl", "--omit-header", target],
			timeout: 5000,
			maxOutputBytes: 4096,
		});
		if (aclBefore === undefined) throw new Error("Missing captured ACL fixture");
		expect(acl.exitCode).toBe(0);
		expect(acl.stdout).toBe(aclBefore);
		const xattr = await safeSpawn({
			cmd: [
				"python3",
				"-c",
				"import os,sys; assert os.getxattr(sys.argv[1], 'user.editor-fixture') == b'preserved'",
				target,
			],
			timeout: 5000,
			maxOutputBytes: 4096,
		});
		expect(xattr.exitCode).toBe(0);
	}
});

test("actual native helper streams 20 MiB; only one same-base managed save wins", async () => {
	const before = new Uint8Array(20 * 1024 * 1024).fill(65);
	await writeFile(target, before);
	const next = before.slice();
	next[0] = 66;
	const alternate = next.slice();
	alternate[0] = 67;
	const a = request(next, hash(before)),
		b = request(alternate, hash(before));
	const results = await Promise.allSettled([runtime.executeEditor(a), runtime.executeEditor(b)]);
	expect(results.filter((r) => r.status === "fulfilled")).toHaveLength(1);
	expect([hash(next), hash(alternate)]).toContain(hash(await readFile(target)));
}, 30_000);

test("expected absence cannot replace an existing target even if caller construction lies", async () => {
	await writeFile(target, "existing");
	const req = request(new Uint8Array([65]), null);
	// The shared runtime captures real before. A late target creation after read is rejected by native expected absence.
	await rm(target);
	req.construct = async () => {
		await writeFile(target, "racer");
		return {
			nextBytes: new Uint8Array([65]),
			result: { hash: hash(new Uint8Array([65])), bytes: 1 },
			lineStats: null,
		};
	};
	await expect(runtime.executeEditor(req)).rejects.toThrow();
	expect(await readFile(target, "utf8")).toBe("racer");
});

test("committed reply followed only by Host after-read failure recovers the original receipt and next save", async () => {
	await writeFile(target, "before");
	let committedReplies = 0;
	const open = async (): Promise<EditorRpc> => {
		const session = await peer();
		return {
			withMetadata: (run) => session.withMetadata(run),
			call: async <T>(method: string, params: object, signal?: AbortSignal) => {
				const reply = await session.call<T>(method, params, signal);
				if (method === "editor.apply") {
					expect((reply as EditorApplyResult).outcome).toBe("committed");
					committedReplies++;
				}
				return reply;
			},
			close: () => session.close(),
		};
	};
	const atomic = createEditorFileAtomicIo({ resourceKey: "fixture-resource", store, open });
	let reads = 0;
	const req = request(new TextEncoder().encode("after"), hash("before"), {
		io: {
			...atomic,
			read: async (path, options) => {
				if (++reads === 2)
					throw Object.assign(new Error("Host after-read denied"), { code: "EIO" });
				return fileChangeLocalIo.read(path, options);
			},
		},
	});
	await withLocalFileChangeRuntime(runtime, async () => {
		expect(await runtime.executeEditor(req)).toMatchObject({
			diskOutcome: "applied",
			historyStatus: "unavailable",
		});
		expect(committedReplies).toBe(1);
		expect(await readFile(target, "utf8")).toBe("after");
		const record = await store.get(req.requestId);
		if (!record || record.version !== 2 || !record.historyOperationId)
			throw new Error("Missing original receipt association");
		expect(runtime.evidence.listEffects(record.historyOperationId).items[0]).toMatchObject({
			diskOutcome: "applied",
		});
		expect(record.receipt?.outcome).toBe("committed");
		const session = await peer();
		try {
			const proof = await session.call<EditorQueryResult>("editor.query", {
				namespace: record.namespace,
				resourceKey: record.resourceKey,
				operationId: record.operationId,
				requestDigest: record.requestDigest,
			});
			expect(proof.outcome).toBe("committed");
			if (!proof.result) throw new Error("Missing committed receipt");
			await store.markSettled(record.operationId, proof.result);
			expect(db.select().from(schema.workspaceWriteLeases).all()).toEqual([]);
		} finally {
			await session.close();
		}
		await runtime.executeEditor(request(new TextEncoder().encode("next"), hash("after")));
		expect(await readFile(target, "utf8")).toBe("next");
		expect(committedReplies).toBe(1);
	});
});

test("lost success reply: query recovers the original result without quarantining the target", async () => {
	await writeFile(target, "before");
	let applies = 0;
	const lost = request(new TextEncoder().encode("saved"), hash("before"));
	const open = async (input: LocalFileApplyInput): Promise<EditorRpc> => {
		if (!input.editor) throw new Error("Missing editor context");
		const session = await EditorHelperSession.open(
			resolver,
			input.editor.root,
			state,
			input.editor.runtime.runtimeEpoch,
		);
		await session.capabilities();
		return {
			withMetadata: (run) => session.withMetadata(run),
			call: async <T>(method: string, params: object, signal?: AbortSignal) => {
				const result = await session.call<T>(method, params, signal);
				if (method === "editor.apply") {
					applies++;
					throw new Error("fixture drops committed reply");
				}
				return result;
			},
			close: () => session.close(),
		};
	};
	lost.io = createEditorFileAtomicIo({ resourceKey: "fixture-resource", store, open });
	await expect(runtime.executeEditor(lost)).rejects.toMatchObject({
		name: "EditorFileChangeUncertainError",
	});
	expect(await readFile(target, "utf8")).toBe("saved");
	expect(applies).toBe(1);
	const { record, result } = await query(lost.requestId);
	expect(result.outcome).toBe("committed");
	// A cold runtime does not need a target unlock, nor available history, to use real proof.
	runtime = makeRuntime();
	await store.markSettled(record.operationId, result.result as EditorApplyResult);
	await runtime.executeEditor(request(new TextEncoder().encode("again"), hash("saved")));
	expect(await readFile(target, "utf8")).toBe("again");
	expect(applies).toBe(1);
});

test("RPC failure retains the same-file slot until real helper close while another file progresses", async () => {
	await writeFile(target, "before");
	await runtime.initialize();
	const closing = Promise.withResolvers<void>(),
		drain = Promise.withResolvers<void>();
	let firstSettled = false,
		secondRead = false;
	const lost = request(Buffer.from("saved"), hash("before"), {
		io: createEditorFileAtomicIo({
			resourceKey: "fixture-resource",
			store,
			open: async () => {
				const session = await peer();
				return {
					withMetadata: (run) => session.withMetadata(run),
					call: async <T>(method: string, params: object, signal?: AbortSignal) => {
						const result = await session.call<T>(method, params, signal);
						if (method === "editor.apply") throw new Error("Host apply reply timed out");
						return result;
					},
					close: async () => {
						closing.resolve();
						await drain.promise;
						await session.close();
					},
				};
			},
		}),
	});
	const first = runtime
		.executeEditor(lost)
		.then(
			() => "unexpected-success",
			(error: Error) => error.name,
		)
		.finally(() => {
			firstSettled = true;
		});
	await closing.promise;
	const next = request(Buffer.from("next"), hash("saved"));
	if (!next.io) throw new Error("Missing provider fixture");
	const read = next.io.read;
	next.io.read = async (...args) => {
		secondRead = true;
		return read(...args);
	};
	const second = runtime.executeEditor(next);
	try {
		const other = join(workspace, "other.txt");
		await writeFile(other, "other-before");
		await runtime.executeEditor(
			request(Buffer.from("other-after"), hash("other-before"), {
				canonicalPath: other,
				lexicalPath: other,
			}),
		);
		expect(editorHelperDiagnostics().processes).toBe(1);
		expect(firstSettled).toBe(false);
		expect(secondRead).toBe(false);
		expect(await readFile(target, "utf8")).toBe("saved");
	} finally {
		drain.resolve();
		await first;
		await second;
	}
	expect(await first).toBe("EditorFileChangeUncertainError");
	expect(await readFile(target, "utf8")).toBe("next");
	expect(editorHelperDiagnostics().processes).toBe(0);
});

test("real helper saves and cold metadata-only query succeeds without any file history", async () => {
	await writeFile(target, "before");
	const history = spyOn(runtime, "initialize").mockRejectedValue(new Error("blob history offline"));
	const req = request(Buffer.from("saved"), hash("before"));
	try {
		expect(await runtime.executeEditor(req)).toMatchObject({
			diskOutcome: "applied",
			historyStatus: "unavailable",
		});
		const receipt = await store.get(req.requestId);
		expect(receipt).toMatchObject({ version: 2, receipt: { outcome: "committed" } });
		for (const field of ["leaseId", "effectId", "hostOperationId", "historyOperationId"])
			expect(receipt).not.toHaveProperty(field);
		expect(db.select().from(schema.fileChangeOperations).all()).toEqual([]);
		await rm(target);
		let checks = 0;
		const result = await withEditorOperationStore(new EditorRemoteOperationStore(store.root), () =>
			queryEditorOperation(
				{
					userId: "native-user",
					narratorId: "native-narrator",
					authorizeOperation: async (device) => {
						expect(device).toBe("local");
						checks++;
					},
					authorize: async () => {
						throw new Error("Receipt query must not reopen its target");
					},
				},
				req.requestId,
			),
		);
		expect(result).toMatchObject({
			status: "saved",
			result: { diskOutcome: "applied", historyStatus: "unavailable", hash: hash("saved") },
		});
		expect(checks).toBeGreaterThan(0);
	} finally {
		history.mockRestore();
	}
});

test("unacknowledged committed proof remains applied when Host receipt persistence fails", async () => {
	await writeFile(target, "before");
	const persistence = spyOn(store, "markSettled").mockRejectedValue(
		new Error("Host receipt temporarily unavailable"),
	);
	const req = request(Buffer.from("saved"), hash("before"));
	try {
		expect(await runtime.executeEditor(req)).toMatchObject({ diskOutcome: "applied" });
		expect((await store.get(req.requestId))?.receipt).toBeUndefined();
		expect((await query(req.requestId)).result.outcome).toBe("committed");
		expect(await readFile(target, "utf8")).toBe("saved");
	} finally {
		persistence.mockRestore();
	}
});

test("an incomplete remote provider never falls back to a Host path read", async () => {
	const host = spyOn(fileChangeLocalIo, "read").mockRejectedValue(new Error("Host IO forbidden"));
	try {
		const io = createEditorFileAtomicIo({
			deviceId: "remote-device",
			resourceKey: "fixture-resource",
			store,
		});
		await expect(io.read("/remote-only/target.txt")).rejects.toThrow(
			"Remote editor read provider unavailable",
		);
		expect(host).not.toHaveBeenCalled();
	} finally {
		host.mockRestore();
	}
});

test("legacy durable capability cannot dispatch the new target-queue protocol", async () => {
	await writeFile(target, "before");
	let applies = 0;
	const io = createEditorFileAtomicIo({
		resourceKey: "fixture-resource",
		store,
		open: async () => {
			const session = await peer();
			return {
				close: () => session.close(),
				call: async <T>(method: string, params: object, signal?: AbortSignal) => {
					if (method === "editor.apply") applies++;
					const value = await session.call<T>(method, params, signal);
					if (method === "editor.capabilities")
						return { ...value, features: ["editor.durable-write.v1"] } as T;
					return value;
				},
			};
		},
	});
	await expect(
		runtime.executeEditor(request(Buffer.from("saved"), hash("before"), { io })),
	).rejects.toThrow("editor.target-queue.v1");
	expect(applies).toBe(0);
	expect(await readFile(target, "utf8")).toBe("before");
});

test("metadata failure rejects before replacement; pre-dispatch cancellation and missing helper never truncate", async () => {
	await writeFile(target, "before");
	const initial = await lstat(target);
	await chmod(target, 0o444);
	await expect(
		runtime.executeEditor(request(new Uint8Array([65]), hash("before"))),
	).rejects.toThrow("read-only");
	await chmod(target, 0o644);
	const controller = new AbortController();
	controller.abort();
	await expect(
		runtime.executeEditor(
			request(new Uint8Array([65]), hash("before"), { signal: controller.signal }),
		),
	).rejects.toThrow();
	const missing = request(new Uint8Array([65]), hash("before"));
	missing.io = createEditorFileAtomicIo({
		resourceKey: "fixture-resource",
		store,
		resolveHelper: async () => {
			throw new Error("verified helper missing");
		},
	});
	await expect(runtime.executeEditor(missing)).rejects.toThrow("verified helper missing");
	expect(await readFile(target, "utf8")).toBe("before");
	expect((await lstat(target)).ino).toBe(initial.ino);
});

test("permission revoked after staging is rechecked before real helper dispatch", async () => {
	await writeFile(target, "before");
	let authorized = true;
	const original = store.put.bind(store);
	const publication = spyOn(store, "put").mockImplementation(async (value) => {
		await original(value);
		authorized = false;
	});
	const req = request(Buffer.from("never"), hash("before"), {
		authorize: async () => {
			if (!authorized) throw new Error("editor permission revoked");
		},
	});
	try {
		await expect(runtime.executeEditor(req)).rejects.toThrow("permission revoked");
	} finally {
		publication.mockRestore();
	}
	expect(await readFile(target, "utf8")).toBe("before");
	expect((await store.get(req.requestId))?.receipt).toMatchObject({
		outcome: "rejected",
		error: { code: "EDITOR_NOT_DISPATCHED" },
	});
	expect((await query(req.requestId)).result.outcome).toBe("not_found");
});

test("cancellation after Host intent but before apply retains positive no-dispatch proof", async () => {
	await writeFile(target, "before");
	const controller = new AbortController();
	const original = store.put.bind(store),
		publication = spyOn(store, "put").mockImplementation(async (value) => {
			await original(value);
			controller.abort();
		});
	const req = request(new TextEncoder().encode("never"), hash("before"), {
		signal: controller.signal,
	});
	try {
		await expect(runtime.executeEditor(req)).rejects.toThrow();
	} finally {
		publication.mockRestore();
	}
	expect(await readFile(target, "utf8")).toBe("before");
	const record = await store.get(req.requestId);
	expect(record?.receipt).toMatchObject({
		outcome: "rejected",
		error: { code: "EDITOR_NOT_DISPATCHED" },
	});
	expect((await query(req.requestId)).result.outcome).toBe("not_found");
	await runtime.executeEditor(request(new TextEncoder().encode("after"), hash("before")));
	expect(await readFile(target, "utf8")).toBe("after");
});

test("forced native Host receipt store takes durable proof, cold query survives, and next actual save is admitted", async () => {
	await writeFile(target, "before");
	const nativeRoot = join(root, "host-native-receipts");
	const native = new EditorRemoteOperationStore(nativeRoot, undefined, {
		root: workspace,
		stateDir: state,
		resolveHelper: resolver,
	});
	const req = request(new TextEncoder().encode("saved"), hash("before"));
	req.io = createEditorFileAtomicIo({
		resourceKey: "fixture-resource",
		store: native,
		resolveHelper: resolver,
		stateDir: state,
	});
	await runtime.executeEditor(req);
	const intent = await native.get(req.requestId);
	if (!intent) throw new Error("Native host intent missing");
	expect(intent.receipt?.outcome).toBe("committed");
	const session = await peer();
	try {
		const input = {
			namespace: intent.namespace,
			resourceKey: intent.resourceKey,
			operationId: intent.operationId,
			requestDigest: intent.requestDigest,
		};
		const proof = await session.call<EditorQueryResult>("editor.query", input);
		if (!proof.result) throw new Error("Missing positive native receipt");
		const positive = proof.result;
		await session.withMetadata(() => native.markSettled(req.requestId, positive));
		await session.call("editor.query", { ...input, acknowledgeTerminal: true });
	} finally {
		await session.close();
	}
	const cold = new EditorRemoteOperationStore(nativeRoot, undefined, {
		root: workspace,
		stateDir: join(root, "different-engine-state"),
		resolveHelper: resolver,
	});
	const proof = await cold.get(req.requestId);
	if (!proof?.receipt) throw new Error("Native cold proof missing");
	runtime = makeRuntime();
	expect(proof.receipt.outcome).toBe("committed");
	const next = request(new TextEncoder().encode("after-cold"), hash("saved"));
	next.io = createEditorFileAtomicIo({
		resourceKey: "fixture-resource",
		store: cold,
		resolveHelper: resolver,
		stateDir: state,
	});
	await runtime.executeEditor(next);
	expect(await readFile(target, "utf8")).toBe("after-cold");
});

test("UTF16 v1 through native Host store preserves separate text/raw hashes after lost reply and cold query", async () => {
	await writeFile(
		target,
		Buffer.concat([Buffer.from([0xff, 0xfe]), Buffer.from("before\r\n", "utf16le")]),
	);
	const nativeRoot = join(root, "v1-native-receipts"),
		native = new EditorRemoteOperationStore(nativeRoot, undefined, {
			root: workspace,
			stateDir: state,
			resolveHelper: resolver,
		});
	let drop = true,
		applies = 0;
	const io = createEditorFileAtomicIo({
		resourceKey: "v1-resource",
		store: native,
		open: async (input) => {
			if (!input.editor) throw new Error("Missing editor metadata");
			const session = await EditorHelperSession.open(
				resolver,
				input.editor.root,
				state,
				input.editor.runtime.runtimeEpoch,
			);
			await session.capabilities();
			return {
				withMetadata: (run) => session.withMetadata(run),
				call: async <T>(method: string, params: object, signal?: AbortSignal) => {
					const result = await session.call<T>(method, params, signal);
					if (method === "editor.apply") {
						applies++;
						if (drop) {
							drop = false;
							throw new Error("lost v1 committed reply");
						}
					}
					return result;
				},
				close: () => session.close(),
			};
		},
	});
	const service = new EditorDocumentService({
		root: join(root, "v1-transfers"),
		execute: (value) => runtime.executeEditor({ ...value, io }),
	});
	const actor = {
		userId: "native-user",
		narratorId: "native-narrator",
		authorize: async () => ({
			cwd: workspace,
			canonicalPath: target,
			lexicalPath: target,
			projectId: null,
			outsideRoots: false,
		}),
	};
	async function upload(text: string) {
		const doc = await service.create(actor, { path: target, origin: "legacy" });
		const value = await service.createUpload(actor, doc.docId, {
			baseHash: doc.baseHash,
			encoding: doc.encoding,
			snapshotRevision: 7,
		});
		const bytes = new TextEncoder().encode(text);
		await service.put(actor, doc.docId, value.uploadId, new Response(bytes).body, bytes.length);
		return { doc, value };
	}
	try {
		const first = await upload("after\r\n");
		await expect(
			service.commit(actor, first.doc.docId, first.value.uploadId, {}),
		).rejects.toMatchObject({ code: "WRITE_RECONCILE_REQUIRED" });
		if (!first.value.operationId) throw new Error("Missing v1 operation id");
		const intent = await native.get(first.value.operationId);
		if (!intent) throw new Error("Missing v1 intent");
		expect(intent.saved.hashKind).toBeUndefined();
		expect(intent.saved.hash).not.toBe(intent.rawDigest);
		const session = await peer();
		try {
			const input = {
				namespace: intent.namespace,
				resourceKey: intent.resourceKey,
				operationId: intent.operationId,
				requestDigest: intent.requestDigest,
			};
			const queried = await session.call<EditorQueryResult>("editor.query", input);
			if (!queried.result) throw new Error("Missing v1 durable receipt");
			runtime = makeRuntime();
			expect(queried.result.outcome).toBe("committed");
			const positive = queried.result;
			await session.withMetadata(() => native.markSettled(intent.operationId, positive));
			await session.call("editor.query", { ...input, acknowledgeTerminal: true });
		} finally {
			await session.close();
		}
		const cold = new EditorRemoteOperationStore(nativeRoot, undefined, {
			root: workspace,
			stateDir: join(root, "v1-cold-state"),
			resolveHelper: resolver,
		});
		const recovered = await cold.get(intent.operationId);
		expect(recovered?.receipt?.outcome).toBe("committed");
		expect(recovered?.saved.hash).toBe(intent.saved.hash);
		expect(applies).toBe(1);
		const second = await upload("next\r\n");
		expect((await service.commit(actor, second.doc.docId, second.value.uploadId, {})).status).toBe(
			"saved",
		);
		expect(applies).toBe(2);
		expect((await readFile(target)).toString("utf16le").replace(/^\uFEFF/, "")).toBe("next\r\n");
	} finally {
		await service.dispose();
	}
});

test("native Host metadata permission refusal is before target dispatch", async () => {
	await writeFile(target, "before");
	const inode = (await lstat(target)).ino;
	const nativeRoot = join(root, "unsafe-host-receipts");
	await mkdir(nativeRoot, { mode: 0o755 });
	const native = new EditorRemoteOperationStore(nativeRoot, undefined, {
		root: workspace,
		stateDir: state,
		resolveHelper: resolver,
	});
	const req = request(new Uint8Array([65]), hash("before"));
	req.io = createEditorFileAtomicIo({
		resourceKey: "fixture-resource",
		store: native,
		resolveHelper: resolver,
		stateDir: state,
	});
	await expect(runtime.executeEditor(req)).rejects.toThrow();
	expect(await readFile(target, "utf8")).toBe("before");
	expect((await lstat(target)).ino).toBe(inode);
});

test("production editor holds narrator revision and worktree admission through ordinary Host settlement", async () => {
	await writeFile(target, "before");
	const fixture = await nativeEditorFixture(root, workspace);
	const now = new Date().toISOString();
	appDb
		.insert(schema.users)
		.values({
			id: "native-user",
			username: "native-user",
			passwordHash: "fixture",
			role: "user",
			createdAt: now,
		})
		.run();
	appDb
		.insert(schema.narrators)
		.values({
			id: "native-narrator",
			ownerUserId: "native-user",
			cwd: workspace,
			createdAt: now,
			updatedAt: now,
		})
		.run();
	let entered!: () => void, release!: () => void;
	const ready = new Promise<void>((done) => {
			entered = done;
		}),
		held = new Promise<void>((done) => {
			release = done;
		});
	const req = request(new TextEncoder().encode("saved"), hash("before"), {
		resourceKey: "managed-resource",
		workspaceRevision: 0,
		authorizeReceipt: async () => {},
	});
	const construct = req.construct;
	req.construct = async (before) => {
		entered();
		await held;
		return construct(before);
	};
	const saving = withLocalFileChangeRuntime(runtime, () => executeAtomicEditorFileChange(req));
	try {
		await ready;
		await expect(
			workspaceContextService.switch(
				"native-narrator",
				{ expectedRevision: 0, requestId: "switch", target: { deviceId: "local", cwd: root } },
				{ origin: "http", userId: "native-user" },
			),
		).rejects.toMatchObject({ code: "WORKSPACE_CONTEXT_BUSY" });
		let retired = false;
		await expect(
			withProtectionReservation(
				[{ deviceId: "local", path: workspace }],
				"fixture worktree retirement",
				async () => {
					retired = true;
				},
			),
		).rejects.toThrow();
		expect(retired).toBe(false);
		expect(await readFile(target, "utf8")).toBe("before");
		release();
		await saving;
		expect(await readFile(target, "utf8")).toBe("saved");
		const rejected = request(new TextEncoder().encode("not-written"), hash("saved"), {
			resourceKey: "managed-resource",
			workspaceRevision: 0,
			authorizeReceipt: async () => {},
		});
		const prepare = rejected.construct;
		rejected.construct = async (before) => {
			const next = await prepare(before);
			await writeFile(target, "external");
			return next;
		};
		await expect(
			withLocalFileChangeRuntime(runtime, () => executeAtomicEditorFileChange(rejected)),
		).rejects.toThrow();
		await expect(
			lstat(
				join(
					testEnvironment.narraforkHome,
					"editor-native-operations",
					`${rejected.requestId}.json`,
				),
			),
		).rejects.toMatchObject({ code: "ENOENT" });
	} finally {
		release();
		await saving;
		await fixture.dispose();
	}
	expect(await readFile(target, "utf8")).toBe("external");
});

test("query namespace/resource/digest isolation and historical query after root deletion", async () => {
	await writeFile(target, "before");
	const req = request(new Uint8Array([65]), hash("before"));
	await runtime.executeEditor(req);
	const { record } = await query(req.requestId);
	await rm(workspace, { recursive: true });
	const session = await peer();
	try {
		const base = {
			namespace: record.namespace,
			resourceKey: record.resourceKey,
			operationId: req.requestId,
			requestDigest: record.requestDigest,
		};
		expect((await session.call<EditorQueryResult>("editor.query", base)).outcome).toBe("committed");
		expect(
			(await session.call<EditorQueryResult>("editor.query", { ...base, namespace: "other-owner" }))
				.outcome,
		).toBe("not_found");
		await expect(
			session.call("editor.query", { ...base, resourceKey: "other-resource" }),
		).rejects.toThrow();
		await expect(
			session.call("editor.query", { ...base, requestDigest: "0".repeat(64) }),
		).rejects.toThrow();
	} finally {
		await session.close();
	}
});
