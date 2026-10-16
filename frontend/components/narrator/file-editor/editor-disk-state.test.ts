import { afterEach, beforeEach, expect, mock, spyOn, test } from "bun:test";
import type { EditorDiskState, EditorDocumentDescriptor } from "@shared/editor-document";
import {
	EDITOR_BOUNDED_READ_CAPABILITIES,
	EDITOR_NO_CAPABILITIES,
	type EditorProviderCapabilities,
} from "@shared/editor-provider-contracts";
import { ApiError } from "../../../lib/api/client";
import { editorDocumentApi } from "../../../lib/api/editor-documents";
import { narratorWSManager } from "../../../lib/narrator-ws-manager";
import { EditorDiskStateRegistry } from "./editor-disk-state";
import { EditorDocumentSession } from "./editor-session-state";
import type { EditorWorkingCopy } from "./editor-working-copy";

function deferred<T>() {
	let resolve!: (value: T) => void;
	const promise = new Promise<T>((yes) => {
		resolve = yes;
	});
	return { promise, resolve };
}
const flush = async () => {
	for (let step = 0; step < 30; step++) await Promise.resolve();
};
const disk = (baseHash: string): EditorDiskState => ({
	exists: true,
	baseHash,
	encoding: "utf-8",
	eol: "LF",
	sourceBytes: 1,
	utf8Bytes: 1,
});
let wsListener: ((data: Record<string, unknown>) => void) | undefined;
let connection: ((connected: boolean, reconnect: boolean) => void) | undefined;
const restores: (() => void)[] = [];
beforeEach(() => {
	const subscribe = spyOn(narratorWSManager, "subscribe").mockImplementation(
		() => ({ _id: 1, _narratorIds: [], _kind: "list" }) as never,
	);
	const unsubscribe = spyOn(narratorWSManager, "unsubscribe").mockImplementation(() => {});
	const add = spyOn(narratorWSManager, "addListener").mockImplementation((_opts, cb) => {
		wsListener = cb;
		return { _id: 1 };
	});
	const remove = spyOn(narratorWSManager, "removeListener").mockImplementation(() => {
		wsListener = undefined;
	});
	const change = spyOn(narratorWSManager, "onConnectionChange").mockImplementation((cb) => {
		connection = cb;
		return () => {
			connection = undefined;
		};
	});
	restores.push(() => {
		for (const spy of [subscribe, unsubscribe, add, remove, change]) spy.mockRestore();
	});
});
const views: { detach(): void }[] = [];
afterEach(() => {
	for (const view of views.splice(0)) view.detach();
	for (const restore of restores.splice(0)) restore();
	mock.restore();
});
const track = <T extends { detach(): void }>(view: T) => {
	views.push(view);
	return view;
};

/** Real session; only the text model is replaced by a revision counter. */
async function copyWith(
	probe: (docId: string) => Promise<EditorDiskState>,
	docId = "doc-1",
	capabilities?: EditorProviderCapabilities,
) {
	let revision = 0;
	let text = "a";
	const descriptor: EditorDocumentDescriptor = {
		docId,
		target: { deviceId: "local", path: "/w/a.txt" },
		versionHandle: "v1",
		baseHash: "base-a",
		encoding: "utf-8",
		eol: "LF",
		sourceBytes: 1,
		utf8Bytes: 1,
		...(capabilities ? { capabilities } : {}),
	};
	let source = { ...descriptor };
	const api = {
		...editorDocumentApi,
		create: mock(async () => source),
		source: mock(async () => text),
		release: mock(async () => ({})),
		diskState: mock(async (_n: string, id: string) => probe(id)),
	};
	const applyContent = mock((next: string) => {
		text = next;
		revision++;
		return { revision, alternativeVersionId: revision, length: next.length };
	});
	const session = new EditorDocumentSession({
		narratorId: "narrator",
		path: "/w/a.txt",
		deviceId: "local",
		origin: () => "reference",
		api: api as unknown as typeof editorDocumentApi,
		snapshot: () => {
			throw new Error("unused");
		},
		encode: async () => new Blob([text]),
		applyContent,
		onChange: () => {},
	});
	await session.load();
	const copy = {
		session,
		options: { narratorId: "narrator", deviceId: "local" },
	} as unknown as EditorWorkingCopy;
	const edit = () => {
		revision++;
		session.change({ revision, alternativeVersionId: revision, length: 2 });
	};
	return {
		copy,
		session,
		api,
		applyContent,
		edit,
		setDisk: (next: Partial<EditorDocumentDescriptor>, body: string) => {
			source = { ...source, ...next };
			text = body;
		},
	};
}

test("hidden views never probe; a hidden copy is not polled", async () => {
	const registry = new EditorDiskStateRegistry();
	const probe = mock(async () => disk("base-a"));
	spyOn(editorDocumentApi, "diskState").mockImplementation(probe);
	const { copy, api } = await copyWith(probe);
	const view = track(registry.attach(copy, false));
	await flush();
	expect(api.diskState).not.toHaveBeenCalled();
	view.detach();
	expect(registry.size).toBe(0);
});

test("two views of one working copy share one entry and one probe", async () => {
	const registry = new EditorDiskStateRegistry();
	const pending = deferred<EditorDiskState>();
	const { copy, api } = await copyWith(() => pending.promise);
	spyOn(editorDocumentApi, "diskState").mockImplementation(api.diskState);
	const left = track(registry.attach(copy, true));
	const right = track(registry.attach(copy, true));
	await flush();
	expect(registry.size).toBe(1);
	expect(api.diskState).toHaveBeenCalledTimes(1);
	pending.resolve(disk("base-a"));
	left.detach();
	right.detach();
	expect(registry.size).toBe(0);
});

test("no change keeps the model untouched (no rebuild, undo preserved)", async () => {
	const registry = new EditorDiskStateRegistry();
	const { copy, api, applyContent } = await copyWith(async () => disk("base-a"));
	spyOn(editorDocumentApi, "diskState").mockImplementation(api.diskState);
	track(registry.attach(copy, true));
	await flush();
	expect(api.diskState).toHaveBeenCalledTimes(1);
	expect(applyContent).toHaveBeenCalledTimes(1);
	expect(registry.diagnostics.reloads).toBe(0);
});

test("clean idle copy whose disk changed reinstalls through the ordinary load", async () => {
	const registry = new EditorDiskStateRegistry();
	const ctx = await copyWith(async () => disk("base-b"));
	spyOn(editorDocumentApi, "diskState").mockImplementation(ctx.api.diskState);
	ctx.setDisk({ docId: "doc-2", baseHash: "base-b" }, "b");
	track(registry.attach(ctx.copy, true));
	await flush();
	expect(ctx.applyContent).toHaveBeenCalledTimes(2);
	expect(ctx.session.state.baseHash).toBe("base-b");
	expect(ctx.session.state.loading).toBe(false);
	expect(ctx.session.state.externalDisk ?? null).toBeNull();
});

test("dirty copy only receives an advisory flag; text, base and undo are untouched", async () => {
	const registry = new EditorDiskStateRegistry();
	const ctx = await copyWith(async () => disk("base-b"));
	spyOn(editorDocumentApi, "diskState").mockImplementation(ctx.api.diskState);
	ctx.edit();
	track(registry.attach(ctx.copy, true));
	await flush();
	expect(ctx.applyContent).toHaveBeenCalledTimes(1);
	expect(ctx.session.state.baseHash).toBe("base-a");
	expect(ctx.session.state.externalDisk).toBe("modified");
});

test("deletion is reported without clearing the buffer or baseline", async () => {
	const registry = new EditorDiskStateRegistry();
	const ctx = await copyWith(async () => ({ exists: false }));
	spyOn(editorDocumentApi, "diskState").mockImplementation(ctx.api.diskState);
	track(registry.attach(ctx.copy, true));
	await flush();
	expect(ctx.applyContent).toHaveBeenCalledTimes(1);
	expect(ctx.session.state.baseHash).toBe("base-a");
	expect(ctx.session.state.externalDisk).toBe("deleted");
});

test("typing while the probe is in flight cancels the clean refresh", async () => {
	const registry = new EditorDiskStateRegistry();
	const pending = deferred<EditorDiskState>();
	const ctx = await copyWith(() => pending.promise);
	spyOn(editorDocumentApi, "diskState").mockImplementation(ctx.api.diskState);
	track(registry.attach(ctx.copy, true));
	await flush();
	ctx.edit();
	pending.resolve(disk("base-b"));
	await flush();
	expect(ctx.applyContent).toHaveBeenCalledTimes(1);
	expect(ctx.session.state.externalDisk).toBe("modified");
});

test("typing while the background source loads keeps the draft and its undo", async () => {
	const registry = new EditorDiskStateRegistry();
	const ctx = await copyWith(async () => disk("base-b"));
	spyOn(editorDocumentApi, "diskState").mockImplementation(ctx.api.diskState);
	const body = deferred<string>();
	ctx.api.source.mockImplementationOnce(() => body.promise);
	ctx.setDisk({ docId: "doc-2", baseHash: "base-b" }, "b");
	track(registry.attach(ctx.copy, true));
	await flush();
	expect(ctx.session.state.loading).toBe(false);
	ctx.edit();
	body.resolve("b");
	await flush();
	expect(ctx.applyContent).toHaveBeenCalledTimes(1);
	expect(ctx.session.state.baseHash).toBe("base-a");
	expect(ctx.session.state.error).toBeNull();
	expect(ctx.session.state.externalDisk).toBe("modified");
});

test("a reply observed against a superseded base (own save receipt) is discarded", async () => {
	const registry = new EditorDiskStateRegistry();
	const pending = deferred<EditorDiskState>();
	const ctx = await copyWith(() => pending.promise);
	spyOn(editorDocumentApi, "diskState").mockImplementation(ctx.api.diskState);
	track(registry.attach(ctx.copy, true));
	await flush();
	// Our own receipt moved the base while the probe was in flight.
	ctx.session.state = { ...ctx.session.state, baseHash: "saved-by-me" };
	pending.resolve(disk("saved-by-me"));
	await flush();
	expect(ctx.applyContent).toHaveBeenCalledTimes(1);
	expect(registry.diagnostics.discarded).toBe(1);
	expect(ctx.session.state.externalDisk ?? null).toBeNull();
});

test("global concurrency is capped at two in-flight probes", async () => {
	const registry = new EditorDiskStateRegistry();
	let running = 0;
	let peak = 0;
	const release = deferred<void>();
	const probe = async () => {
		peak = Math.max(peak, ++running);
		await release.promise;
		running--;
		return disk("base-a");
	};
	const copies = await Promise.all(
		Array.from({ length: 5 }, (_, index) => copyWith(probe, `doc-${index}`)),
	);
	spyOn(editorDocumentApi, "diskState").mockImplementation(probe);
	for (const ctx of copies) track(registry.attach(ctx.copy, true));
	await flush();
	expect(peak).toBe(2);
	release.resolve();
	await flush();
	expect(peak).toBe(2);
});

test("WS hints and reconnects coalesce into one probe per copy", async () => {
	const registry = new EditorDiskStateRegistry();
	const pending = deferred<EditorDiskState>();
	const ctx = await copyWith(() => pending.promise);
	const probe = spyOn(editorDocumentApi, "diskState").mockImplementation(ctx.api.diskState);
	track(registry.attach(ctx.copy, true));
	await flush();
	for (let index = 0; index < 20; index++) wsListener?.({ type: "workspace_paths_changed" });
	connection?.(true, true);
	await new Promise((resolve) => setTimeout(resolve, 300));
	expect(probe).toHaveBeenCalledTimes(1);
	pending.resolve(disk("base-a"));
	await flush();
	// Exactly one queued follow-up for all wake-ups that arrived while in flight.
	expect(probe).toHaveBeenCalledTimes(2);
});

test("a read-only resource with boundedRead still verifies disk identity and only hints when dirty", async () => {
	const registry = new EditorDiskStateRegistry();
	const ctx = await copyWith(
		async () => disk("base-b"),
		"doc-ro",
		EDITOR_BOUNDED_READ_CAPABILITIES,
	);
	spyOn(editorDocumentApi, "diskState").mockImplementation(ctx.api.diskState);
	expect(ctx.session.state.capabilities).toEqual(EDITOR_BOUNDED_READ_CAPABILITIES);
	ctx.edit();
	track(registry.attach(ctx.copy, true));
	await flush();
	expect(ctx.api.diskState).toHaveBeenCalledTimes(1);
	expect(ctx.session.state.externalDisk).toBe("modified");
	expect(ctx.applyContent).toHaveBeenCalledTimes(1);
});

test("a declared provider without boundedRead is never probed", async () => {
	const registry = new EditorDiskStateRegistry();
	const ctx = await copyWith(async () => disk("base-b"), "doc-none", EDITOR_NO_CAPABILITIES);
	spyOn(editorDocumentApi, "diskState").mockImplementation(ctx.api.diskState);
	track(registry.attach(ctx.copy, true));
	await flush();
	expect(ctx.api.diskState).not.toHaveBeenCalled();
});

test("an expired server document reauthorizes without touching the draft", async () => {
	const registry = new EditorDiskStateRegistry();
	let calls = 0;
	const ctx = await copyWith(async () => {
		if (++calls === 1) throw new ApiError("Expired", 410);
		return disk("base-a");
	});
	spyOn(editorDocumentApi, "diskState").mockImplementation(ctx.api.diskState);
	ctx.edit();
	track(registry.attach(ctx.copy, true));
	await flush();
	expect(ctx.api.create).toHaveBeenCalledTimes(2);
	expect(ctx.session.state.baseHash).toBe("base-a");
	expect(ctx.applyContent).toHaveBeenCalledTimes(1);
});
