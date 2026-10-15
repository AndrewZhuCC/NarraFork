import { afterEach, beforeEach, expect, spyOn, test } from "bun:test";
import type { EditorDocumentDescriptor } from "@shared/editor-document";
import { ApiError } from "../../../lib/api/client";
import { editorDocumentApi } from "../../../lib/api/editor-documents";
import {
	type EditorDocumentLease,
	EditorDocumentRegistry,
	type EditorRecoveryLease,
} from "./editor-document-registry";
import * as worker from "./editor-worker-client";
import * as workingCopies from "./editor-working-copy";
import { TestEditorModel } from "./file-editor-test-model";

const options = {
	narratorId: "source",
	path: "/repo/a",
	deviceId: "local",
	origin: () => "reference" as const,
};
const descriptor = (id: number, resourceKey = "authorized-scope"): EditorDocumentDescriptor => ({
	docId: `doc-${id}`,
	resourceKey,
	target: { deviceId: "local", path: "/repo/a" },
	versionHandle: "v1",
	baseHash: "base",
	encoding: "utf-8",
	eol: "LF",
	sourceBytes: 5,
	utf8Bytes: 5,
});
let registry: EditorDocumentRegistry;
let leases: EditorDocumentLease[];
let recoveryLeases: EditorRecoveryLease[];
let models: TestEditorModel[];
let restores: (() => void)[];
let create: ReturnType<typeof spyOn<typeof editorDocumentApi, "create">>;
let source: ReturnType<typeof spyOn<typeof editorDocumentApi, "source">>;
let release: ReturnType<typeof spyOn<typeof editorDocumentApi, "release">>;
function deferred<T>() {
	let resolve!: (value: T) => void;
	const promise = new Promise<T>((done) => {
		resolve = done;
	});
	return { promise, resolve };
}
const tick = async () => {
	await Promise.resolve();
	await Promise.resolve();
};
async function acquire(signal = new AbortController().signal) {
	const lease = await registry.acquire(options, signal);
	leases.push(lease);
	return lease;
}
beforeEach(() => {
	registry = new EditorDocumentRegistry();
	leases = [];
	recoveryLeases = [];
	models = [];
	restores = [];
	let next = 0;
	create = spyOn(editorDocumentApi, "create").mockImplementation(async () => descriptor(++next));
	source = spyOn(editorDocumentApi, "source").mockResolvedValue("hello");
	release = spyOn(editorDocumentApi, "release").mockResolvedValue(undefined);
	const factory = spyOn(workingCopies, "createWorkingCopyModel").mockImplementation(
		async (filePath, documentKey) => {
			const model = new TestEditorModel({ filePath, documentKey, initialValue: "" });
			models.push(model);
			return model.getModel();
		},
	);
	restores.push(
		() => create.mockRestore(),
		() => source.mockRestore(),
		() => release.mockRestore(),
		() => factory.mockRestore(),
	);
});
afterEach(async () => {
	for (const lease of recoveryLeases) lease.release();
	for (const lease of leases) {
		lease.release();
		lease.copy.dispose();
	}
	await tick();
	for (const restore of restores.reverse()) restore();
});

test("two independently authorized consumers share one working copy, model, baseline, undo and save coordinator", async () => {
	const [a, b] = await Promise.all([acquire(), acquire()]);
	expect(a.copy).toBe(b.copy);
	expect(a.copy.session).toBe(b.copy.session);
	await Promise.all([a.copy.ensureLoaded(), b.copy.ensureLoaded()]);
	expect(create).toHaveBeenCalledTimes(2);
	expect(source).toHaveBeenCalledTimes(1);
	expect(models).toHaveLength(1);
	expect(release).toHaveBeenCalledWith("source", "doc-2");
	models[0].edit("shared draft");
	expect(b.copy.getSnapshot().version?.length).toBe(12);
	a.release();
	a.release();
	await tick();
	expect(models[0].disposed).toBe(false);
	models[0].undo();
	expect(models[0].text).toBe("hello");
	expect(b.copy.protected).toBe(false);
	b.release();
	await tick();
	expect(models[0].disposed).toBe(true);
	expect(registry.size).toBe(0);
	expect(release).toHaveBeenCalledWith("source", "doc-1");
});

test("simultaneous saves from two leases encode, upload and commit one immutable attempt", async () => {
	const pending = deferred<Blob>();
	const encode = spyOn(worker, "encodeEditorSnapshot").mockReturnValue(pending.promise);
	const begin = spyOn(editorDocumentApi, "createUpload").mockResolvedValue({
		uploadId: "upload",
		state: "uploading",
	});
	const put = spyOn(editorDocumentApi, "upload").mockResolvedValue({
		uploadId: "upload",
		state: "sealed",
	});
	const commit = spyOn(editorDocumentApi, "commit").mockImplementation(async () => ({
		status: "saved",
		operationId: "op",
		hash: "saved",
		snapshotRevision: models[0].revision,
		bytes: 5,
	}));
	restores.push(
		() => encode.mockRestore(),
		() => begin.mockRestore(),
		() => put.mockRestore(),
		() => commit.mockRestore(),
	);
	const [a, b] = await Promise.all([acquire(), acquire()]);
	await a.copy.ensureLoaded();
	models[0].edit("draft");
	const saving = a.copy.session.save();
	const duplicate = b.copy.session.save();
	expect(encode).toHaveBeenCalledTimes(1);
	a.release();
	await tick();
	expect(models[0].disposed).toBe(false);
	pending.resolve(new Blob(["draft"]));
	await Promise.all([saving, duplicate]);
	expect(begin).toHaveBeenCalledTimes(1);
	expect(put).toHaveBeenCalledTimes(1);
	expect(commit).toHaveBeenCalledTimes(1);
	expect(b.copy.protected).toBe(false);
});

test("different server bindings never merge even with identical UI path/narrator", async () => {
	create.mockImplementation(async () =>
		descriptor(create.mock.calls.length, `scope-${create.mock.calls.length}`),
	);
	const [a, b] = await Promise.all([acquire(), acquire()]);
	expect(a.copy).not.toBe(b.copy);
	await Promise.all([a.copy.ensureLoaded(), b.copy.ensureLoaded()]);
	expect(models).toHaveLength(2);
});

for (const authorized of [true, false])
	test(`reopening a changed binding ${authorized ? "exposes" : "never exposes"} the retained draft under its verified recovery principal`, async () => {
		create.mockResolvedValue({ ...descriptor(1), recoveryKey: "verified-principal" });
		const old = await acquire();
		await old.copy.ensureLoaded();
		models[0].edit("retained draft");
		registry.invalidateNarrator("source", "scope changed");
		expect(registry.discardRetained(old.copy)).toBe(false);
		old.release();
		await tick();
		create.mockResolvedValue({
			...descriptor(2, "new-context"),
			recoveryKey: authorized ? "verified-principal" : "another-principal",
		});
		const current = await acquire();
		await current.copy.ensureLoaded();
		expect(current.copy).not.toBe(old.copy);
		const proof = spyOn(editorDocumentApi, "recoveryScope").mockResolvedValue({
			recoveryKey: authorized ? "verified-principal" : "another-principal",
		});
		restores.push(() => proof.mockRestore());
		const recovered = await registry.discoverRetained(
			options,
			new AbortController().signal,
			Promise.resolve(current.copy),
		);
		recoveryLeases.push(recovered);
		expect(recovered.retainedCopies).toEqual(authorized ? [old.copy] : []);
		expect(models[0].text).toBe("retained draft");
		expect(models[0].disposed).toBe(false);
		expect(models[1].text).toBe("hello");
		expect(current.copy.getSnapshot().invalidated).not.toBe(true);
		if (authorized) {
			expect(registry.discardRetained(old.copy)).toBe(true);
			expect(models[0].disposed).toBe(true);
			expect(models[1].disposed).toBe(false);
			expect(old.copy.getSnapshot().discarded).toBe(true);
			expect(registry.size).toBe(1);
		}
	});

for (const access of ["owner", "different-user", "revoked"] as const)
	test(`independent retained discovery ${access} never depends on current create or a client path as authorization`, async () => {
		create.mockResolvedValue({ ...descriptor(1), recoveryKey: "original-owner" });
		const old = await acquire();
		await old.copy.ensureLoaded();
		models[0].edit("private draft");
		old.release();
		await tick();
		const proof = spyOn(editorDocumentApi, "recoveryScope");
		restores.push(() => proof.mockRestore());
		if (access === "revoked") proof.mockRejectedValue(new ApiError("Forbidden", 403));
		else
			proof.mockResolvedValue({
				recoveryKey: access === "owner" ? "original-owner" : "other-owner",
			});
		create.mockRejectedValue(new ApiError("Current path unavailable", 403));
		if (access === "revoked")
			await expect(
				registry.discoverRetained(options, new AbortController().signal),
			).rejects.toThrow("Forbidden");
		else {
			const recovered = await registry.discoverRetained(options, new AbortController().signal);
			recoveryLeases.push(recovered);
			expect(recovered.retainedCopies).toEqual(access === "owner" ? [old.copy] : []);
			if (access === "owner") expect(old.copy.getSnapshot().invalidated).toBe(true);
		}
		expect(proof).toHaveBeenCalledWith(
			"source",
			{ deviceId: "local", origin: "reference" },
			expect.any(AbortSignal),
		);
		expect(create).toHaveBeenCalledTimes(1);
		expect(source).toHaveBeenCalledTimes(1);
		expect(models[0].text).toBe("private draft");
		expect(models[0].disposed).toBe(false);
	});

for (const fence of ["abort", "generation"] as const)
	test(`late independent recovery proof cannot publish after ${fence}`, async () => {
		create.mockResolvedValue({ ...descriptor(1), recoveryKey: "owner" });
		const old = await acquire();
		await old.copy.ensureLoaded();
		models[0].edit("private draft");
		old.release();
		await tick();
		const pending = deferred<{ recoveryKey: string }>();
		const proof = spyOn(editorDocumentApi, "recoveryScope").mockReturnValue(pending.promise);
		restores.push(() => proof.mockRestore());
		const controller = new AbortController();
		const recovering = registry.discoverRetained(options, controller.signal);
		if (fence === "abort") controller.abort();
		else registry.invalidateNarrator("source", "new context");
		pending.resolve({ recoveryKey: "owner" });
		await expect(recovering).rejects.toThrow();
		expect(models[0].text).toBe("private draft");
		expect(models[0].disposed).toBe(false);
		expect(
			[...(Reflect.get(registry, "entries") as Set<{ references: number }>)][0].references,
		).toBe(0);
	});

test("a discarded draft is not resurrected by a delayed independent recovery response", async () => {
	create.mockResolvedValue({ ...descriptor(1), recoveryKey: "owner" });
	const old = await acquire();
	await old.copy.ensureLoaded();
	models[0].edit("old draft");
	registry.invalidateNarrator("source", "old scope");
	old.release();
	await tick();
	const pending = deferred<{ recoveryKey: string }>();
	const proof = spyOn(editorDocumentApi, "recoveryScope").mockReturnValue(pending.promise);
	restores.push(() => proof.mockRestore());
	const recovering = registry.discoverRetained(options, new AbortController().signal);
	expect(registry.discardRetained(old.copy)).toBe(true);
	pending.resolve({ recoveryKey: "owner" });
	const recovered = await recovering;
	recoveryLeases.push(recovered);
	expect(recovered.retainedCopies).toEqual([]);
	expect(registry.size).toBe(0);
	expect(models[0].disposed).toBe(true);
});

test("independent discovery never invalidates the ordinary dirty working copy reacquired by the current view", async () => {
	create.mockImplementation(async () => ({
		...descriptor(create.mock.calls.length),
		recoveryKey: "owner",
	}));
	const old = await acquire();
	await old.copy.ensureLoaded();
	models[0].edit("same draft and undo");
	old.release();
	await tick();
	const proof = spyOn(editorDocumentApi, "recoveryScope").mockResolvedValue({
		recoveryKey: "owner",
	});
	restores.push(() => proof.mockRestore());
	const viewing = acquire();
	const recovered = await registry.discoverRetained(
		options,
		new AbortController().signal,
		viewing.then((lease) => lease.copy),
	);
	recoveryLeases.push(recovered);
	const current = await viewing;
	expect(current.copy).toBe(old.copy);
	expect(recovered.retainedCopies).toEqual([]);
	expect(current.copy.getSnapshot().invalidated).not.toBe(true);
	expect(models).toHaveLength(1);
	expect(models[0].text).toBe("same draft and undo");
	expect(source).toHaveBeenCalledTimes(1);
});

test("cancellation stops independent discovery even while an uncooperative current create remains pending", async () => {
	create.mockResolvedValue({ ...descriptor(1), recoveryKey: "owner" });
	const old = await acquire();
	await old.copy.ensureLoaded();
	models[0].edit("private draft");
	old.release();
	await tick();
	const proof = spyOn(editorDocumentApi, "recoveryScope").mockResolvedValue({
		recoveryKey: "owner",
	});
	restores.push(() => proof.mockRestore());
	const excluding = deferred<null>();
	const controller = new AbortController();
	const recovering = registry.discoverRetained(options, controller.signal, excluding.promise);
	await tick();
	controller.abort();
	await expect(recovering).rejects.toThrow();
	excluding.resolve(null);
	await tick();
	expect([...(Reflect.get(registry, "entries") as Set<{ references: number }>)][0].references).toBe(
		0,
	);
	expect(models[0].text).toBe("private draft");
	expect(models[0].disposed).toBe(false);
});

test("old v1 descriptors without opaque bindings remain exclusive", async () => {
	create.mockImplementation(async () => ({
		...descriptor(create.mock.calls.length),
		resourceKey: undefined,
	}));
	const [a, b] = await Promise.all([acquire(), acquire()]);
	expect(a.copy).not.toBe(b.copy);
});

test("last dirty reference is pinned; reacquire restores the same draft and undo", async () => {
	const a = await acquire();
	await a.copy.ensureLoaded();
	models[0].edit("unsaved");
	a.release();
	await tick();
	expect(registry.size).toBe(1);
	expect(models[0].disposed).toBe(false);
	const b = await acquire();
	expect(b.copy).toBe(a.copy);
	await b.copy.ensureLoaded();
	expect(source).toHaveBeenCalledTimes(1);
	models[0].undo();
	expect(models[0].text).toBe("hello");
});

test("cancellation after an uncooperative create releases the extra descriptor without installing it", async () => {
	const pending = deferred<EditorDocumentDescriptor>();
	create.mockReturnValue(pending.promise);
	const controller = new AbortController();
	const result = acquire(controller.signal);
	controller.abort();
	pending.resolve(descriptor(99));
	await expect(result).rejects.toThrow();
	expect(release).toHaveBeenCalledWith("source", "doc-99");
	expect(registry.size).toBe(0);
	expect(source).not.toHaveBeenCalled();
});

test("context invalidation fences pending authorization and retains old dirty model without rebinding", async () => {
	const a = await acquire();
	await a.copy.ensureLoaded();
	models[0].edit("old draft");
	const pending = deferred<EditorDocumentDescriptor>();
	create.mockReturnValue(pending.promise);
	const late = acquire();
	registry.invalidateNarrator("source", "context changed");
	pending.resolve(descriptor(99));
	await expect(late).rejects.toThrow("authorization changed");
	expect(a.copy.getSnapshot().invalidated).toBe(true);
	expect(models[0].text).toBe("old draft");
	await a.copy.session.load();
	await a.copy.session.save();
	expect(models[0].text).toBe("old draft");
	expect(source).toHaveBeenCalledTimes(1);
	expect(release).toHaveBeenCalledWith("source", "doc-99");
});

test("late model candidate after release is disposed and never starts a source load", async () => {
	const pending = deferred<ReturnType<TestEditorModel["getModel"]>>();
	const factory = spyOn(workingCopies, "createWorkingCopyModel").mockReturnValue(pending.promise);
	const a = await acquire();
	const loading = a.copy.ensureLoaded();
	a.release();
	await tick();
	const model = new TestEditorModel({ filePath: "/repo/a", documentKey: "late", initialValue: "" });
	pending.resolve(model.getModel());
	await loading;
	expect(model.disposed).toBe(true);
	expect(source).not.toHaveBeenCalled();
	factory.mockRestore();
});

test("initial source retry cannot change the registry's authorized resource binding", async () => {
	const a = await acquire();
	source.mockRejectedValueOnce(new ApiError("changed", 409, { code: "EDITOR_SOURCE_CHANGED" }));
	create.mockResolvedValue(descriptor(99, "new-workspace"));
	await a.copy.ensureLoaded();
	expect(a.copy.getSnapshot().invalidated).toBe(true);
	expect(a.copy.getSnapshot().loaded).toBe(false);
	expect(models[0].text).toBe("");
	expect(source).toHaveBeenCalledTimes(1);
	expect(release).toHaveBeenCalledWith("source", "doc-99");
});

test("source failure retry reuses its existing model without leaking another candidate", async () => {
	source.mockRejectedValueOnce(new Error("offline"));
	const a = await acquire();
	await a.copy.ensureLoaded();
	expect(a.copy.getSnapshot().loaded).toBe(false);
	await a.copy.ensureLoaded();
	expect(a.copy.getSnapshot().loaded).toBe(true);
	expect(models).toHaveLength(1);
});

test("late source results cannot install after binding invalidation", async () => {
	const pending = deferred<string>();
	source.mockReturnValue(pending.promise);
	const a = await acquire();
	const loading = a.copy.ensureLoaded();
	await tick();
	registry.invalidateNarrator("source", "context changed");
	pending.resolve("late new text");
	await loading;
	expect(models[0].text).toBe("");
	expect(a.copy.getSnapshot().loaded).toBe(false);
	expect(a.copy.getSnapshot().invalidated).toBe(true);
});
