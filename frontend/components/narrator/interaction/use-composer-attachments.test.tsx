import { afterEach, beforeEach, expect, mock, spyOn, test } from "bun:test";
import { notifications } from "@mantine/notifications";
import { parseHTML } from "linkedom";
import { act, createElement } from "react";
import { createRoot, type Root } from "react-dom/client";
import * as storage from "../composer/draft-image-attachments";
import { useComposerAttachments } from "./use-composer-attachments";

let root: Root;
let host: HTMLElement;
let hook: ReturnType<typeof useComposerAttachments>;
let narratorId: string;
let currentUserId: string;
const sendingRef = { current: false };
const t = (key: string) => key;
const previousGlobals = new Map<string, PropertyDescriptor | undefined>();
type Draft = storage.LoadedDraftAttachments;
let loads: { userId: string; narratorId: string; resolve: (draft: Draft) => void }[];
let saved: Map<string, Draft>;
let save: ReturnType<typeof spyOn<typeof storage, "saveDraftImageAttachments">>;
const empty = (): Draft => ({ images: [], textFiles: [], droppedCount: 0 });
function image(id: string) {
	const file = new File([id], `${id}.png`, { type: "image/png" });
	storage.setComposerAssetId(file, id);
	return file;
}
function text(id: string) {
	const file = new File([id], `${id}.txt`, { type: "text/plain" });
	storage.setComposerAssetId(file, id);
	return file;
}
function Harness() {
	hook = useComposerAttachments({ narratorId, currentUserId, sendingRef, t });
	return null;
}
async function render() {
	await act(async () => root.render(createElement(Harness)));
}
async function hydrate(draft: Draft, request = loads.length - 1) {
	await act(async () => loads[request].resolve(draft));
}
function persisted(user = currentUserId, narrator = narratorId) {
	return saved.get(storage.getDraftImageAttachmentKey(user, narrator));
}
beforeEach(async () => {
	const { window } = parseHTML("<!doctype html><html><body></body></html>");
	for (const [key, value] of Object.entries({
		window,
		document: window.document,
		navigator: window.navigator,
		HTMLElement: window.HTMLElement,
		Element: window.Element,
		Node: window.Node,
		IS_REACT_ACT_ENVIRONMENT: true,
	})) {
		previousGlobals.set(key, Object.getOwnPropertyDescriptor(globalThis, key));
		Object.defineProperty(globalThis, key, { configurable: true, writable: true, value });
	}
	narratorId = "narrator-a";
	currentUserId = "user-a";
	sendingRef.current = false;
	loads = [];
	saved = new Map();
	spyOn(storage, "loadDraftImageAttachments").mockImplementation(
		(userId, narrator) =>
			new Promise((resolve) => loads.push({ userId, narratorId: narrator, resolve })),
	);
	save = spyOn(storage, "saveDraftImageAttachments").mockImplementation(
		async (userId, narrator, images, textFiles = []) => {
			saved.set(storage.getDraftImageAttachmentKey(userId, narrator), {
				images: [...images],
				textFiles: [...textFiles],
				droppedCount: 0,
			});
		},
	);
	spyOn(notifications, "show").mockImplementation(() => "notification");
	host = document.createElement("div");
	document.body.append(host);
	root = createRoot(host);
	await render();
});
afterEach(async () => {
	await act(async () => root.unmount());
	host.remove();
	mock.restore();
	for (const [key, descriptor] of previousGlobals) {
		if (descriptor) Object.defineProperty(globalThis, key, descriptor);
		else Reflect.deleteProperty(globalThis, key);
	}
	previousGlobals.clear();
});

test("manifest arriving before IndexedDB filters deleted files but preserves referenced blobs", async () => {
	const keep = image("keep");
	const removed = image("removed");
	const keepText = text("keep-text");
	const removedText = text("removed-text");
	await act(async () => hook.reconcileDocumentAssets(["keep", "keep-text"]));
	expect(save).not.toHaveBeenCalled();
	await hydrate({ images: [keep, removed], textFiles: [keepText, removedText], droppedCount: 0 });
	expect(hook.attachedImages).toEqual([keep]);
	expect(hook.attachedTextFiles).toEqual([keepText]);
	expect(hook.composerAssets.map((asset) => asset.id)).toEqual(["keep", "keep-text"]);
	expect(persisted()).toEqual({ images: [keep], textFiles: [keepText], droppedCount: 0 });
});

test("latest manifest wins during hydrate, including an empty remote document", async () => {
	await act(async () => {
		hook.reconcileDocumentAssets(["f"]);
		hook.reconcileDocumentAssets([]);
	});
	await hydrate({ images: [image("f")], textFiles: [], droppedCount: 0 });
	expect(hook.attachedImages).toEqual([]);
	expect(persisted()).toEqual(empty());
});

test("manifest after hydrate removes F from arrays and persistence before adding G", async () => {
	const f = image("f");
	const oldText = text("old-text");
	const g = image("g");
	await hydrate({ images: [f], textFiles: [oldText], droppedCount: 0 });
	await act(async () => {
		hook.reconcileDocumentAssets([]);
		// Exercise a same-event functional append: its previous array must already
		// be filtered, not the render's stale pre-reconciliation state.
		hook.updateAttachedImages((previous) => [...previous, g]);
	});
	expect(hook.attachedImages).toEqual([g]);
	expect(hook.attachedTextFiles).toEqual([]);
	expect(persisted()).toEqual({ images: [g], textFiles: [], droppedCount: 0 });
	await act(async () => hook.reconcileDocumentAssets(["g"]));
	expect(hook.attachedImagesRef.current).toEqual([g]);
	const stored = persisted();
	expect(stored).toBeDefined();
	await act(async () => {
		root.unmount();
		root = createRoot(host);
		root.render(createElement(Harness));
	});
	await act(async () => hook.reconcileDocumentAssets(["g"]));
	await hydrate(stored as Draft);
	expect(hook.attachedImages.map(storage.getComposerAssetId)).toEqual(["g"]);
});

test("new local G wins over a late hydrate and is allowed before document adoption", async () => {
	const g = image("g");
	const newText = text("new-text");
	await act(async () => {
		hook.reconcileDocumentAssets([]);
		hook.updateAttachedImages((previous) => [...previous, g]);
		hook.updateAttachedTextFiles((previous) => [...previous, newText]);
	});
	await hydrate({ images: [image("f")], textFiles: [text("old-text")], droppedCount: 0 });
	expect(hook.attachedImages).toEqual([g]);
	expect(hook.attachedTextFiles).toEqual([newText]);
	expect(persisted()).toEqual({ images: [g], textFiles: [newText], droppedCount: 0 });
	await act(async () => hook.reconcileDocumentAssets(["g", "new-text"]));
	expect(hook.composerAssets.map((asset) => asset.id)).toEqual(["g", "new-text"]);
});

test("document declarations alone do not suppress valid F hydration", async () => {
	const f = image("f");
	await act(async () => hook.reconcileDocumentAssets(["f"]));
	await act(async () => hook.reconcileDocumentAssets(["f"]));
	await hydrate({ images: [f], textFiles: [], droppedCount: 0 });
	expect(hook.attachedImages).toEqual([f]);
	expect(persisted()?.images).toEqual([f]);
});

for (const switchScope of ["narrator", "user"] as const) {
	test(`${switchScope} scope switch resets allowed ids and ignores the old late hydrate`, async () => {
		await act(async () => hook.reconcileDocumentAssets([]));
		if (switchScope === "narrator") narratorId = "narrator-b";
		else currentUserId = "user-b";
		await render();
		const fresh = image("fresh");
		await hydrate({ images: [fresh], textFiles: [], droppedCount: 0 }, 1);
		await hydrate({ images: [image("stale")], textFiles: [], droppedCount: 0 }, 0);
		expect(hook.attachedImages).toEqual([fresh]);
		expect(persisted()?.images).toEqual([fresh]);
	});
}

test("narrator A to B to A does not reuse the first A ownership declaration or hydrate", async () => {
	await act(async () => hook.reconcileDocumentAssets([]));
	narratorId = "narrator-b";
	await render();
	narratorId = "narrator-a";
	await render();
	const current = image("current");
	await hydrate({ images: [current], textFiles: [], droppedCount: 0 }, 2);
	await hydrate({ images: [image("stale")], textFiles: [], droppedCount: 0 }, 0);
	expect(hook.attachedImages).toEqual([current]);
	expect(persisted()?.images).toEqual([current]);
});

test("local clearing during hydrate still takes priority over a document manifest", async () => {
	await act(async () => {
		hook.reconcileDocumentAssets(["f"]);
		hook.hideAttachedFilesForSend();
	});
	await hydrate({ images: [image("f")], textFiles: [], droppedCount: 0 });
	expect(hook.attachedImages).toEqual([]);
	expect(persisted()).toEqual(empty());
});
