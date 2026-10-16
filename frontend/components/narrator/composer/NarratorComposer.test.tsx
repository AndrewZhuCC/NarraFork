import { afterEach, beforeEach, describe, expect, spyOn, test } from "bun:test";
import { ApiError, api } from "@frontend/lib/api";
import { resetSessionStoreForTest } from "@frontend/lib/session-store";
import { MantineProvider } from "@mantine/core";
import type { FileReference } from "@shared/file-reference";
import { notifyManager, QueryClient, QueryClientProvider } from "@tanstack/react-query";
import i18next, { type i18n } from "i18next";
import { parseHTML } from "linkedom";
import { act, createRef } from "react";
import { createRoot, type Root } from "react-dom/client";
import { I18nextProvider, initReactI18next } from "react-i18next";
import { joinQuotedComposerInput } from "./composer-quotes";
import { FileReferenceScopeProvider } from "./FileReferenceScope";
import { insertFileReference } from "./file-reference-input";
import {
	NarratorComposer,
	type NarratorComposerHandle,
	type NarratorComposerProps,
	type NarratorRemoteDraft,
} from "./NarratorComposer";
import { persistNarratorInputDraft, readNarratorInputDraft } from "./narrator-draft-storage";

const reference: FileReference = {
	id: "occurrence",
	deviceId: "RemoteABC",
	path: "/work/src/a.ts",
	label: "src/a.ts",
};
const initial: NarratorRemoteDraft = {
	hasDraft: false,
	text: "",
	fileReferences: [],
	revision: 1,
	sourceId: null,
	updatedAt: null,
	updatedBy: null,
};
let root: Root;
let host: HTMLElement;
let instance: i18n;
let qc: QueryClient;
let handle = createRef<NarratorComposerHandle>();
let sendingRef = { current: false };
let flags: boolean[];
let renders: number;
let sendCalls: number;
let suggestionProps: Pick<
	NarratorComposerProps,
	"isPrimary" | "composerSuggestionAvailable" | "hasAttachments"
>;
let suggestionSpy: ReturnType<typeof spyOn<typeof api, "getNarratorComposerSuggestion">>;
let testNarratorId = "n1";
let getSpy: ReturnType<typeof spyOn<typeof api, "getNarratorDraft">>;
let panelSpy: ReturnType<typeof spyOn<typeof api, "getNarratorPanelState">>;
let updateSpy: ReturnType<typeof spyOn<typeof api, "updateNarratorDraft">>;
const previousGlobals = new Map<string, PropertyDescriptor | undefined>();

function Harness() {
	renders++;
	return (
		<NarratorComposer
			ref={handle}
			narratorId={testNarratorId}
			sendingRef={sendingRef}
			permEnterActive={false}
			{...suggestionProps}
			enterMode="turn"
			ctrlEnterMode="interrupt"
			onSendWithMode={() => {
				sendCalls++;
			}}
			onTextFlagsChange={(value) => flags.push(value)}
			onPasteImages={() => {}}
		/>
	);
}

beforeEach(async () => {
	const { window } = parseHTML("<!doctype html><html><head></head><body></body></html>");
	Object.assign(window, { innerWidth: 1_024, innerHeight: 768, getSelection: () => null });
	Object.defineProperty(window.document, "fonts", {
		configurable: true,
		value: { addEventListener() {}, removeEventListener() {} },
	});
	const stored = new Map<string, string>();
	const storage = {
		get length() {
			return stored.size;
		},
		clear() {
			stored.clear();
		},
		getItem: (key: string) => stored.get(key) ?? null,
		key: (i: number) => [...stored.keys()][i] ?? null,
		setItem: (key: string, value: string) => stored.set(key, value),
		removeItem: (key: string) => {
			stored.delete(key);
		},
	};
	Object.defineProperty(window.HTMLTextAreaElement.prototype, "setSelectionRange", {
		configurable: true,
		value(this: HTMLTextAreaElement, start: number, end: number) {
			this.selectionStart = start;
			this.selectionEnd = end;
		},
	});
	for (const [key, value] of Object.entries({
		window,
		document: window.document,
		navigator: window.navigator,
		HTMLElement: window.HTMLElement,
		HTMLTextAreaElement: window.HTMLTextAreaElement,
		HTMLAnchorElement: window.HTMLAnchorElement,
		Element: window.Element,
		Node: window.Node,
		Event: window.Event,
		sessionStorage: storage,
		localStorage: storage,
		getComputedStyle: (): Partial<CSSStyleDeclaration> => ({
			getPropertyValue: () => "0px",
			direction: "ltr",
			boxSizing: "border-box",
			borderBottomWidth: "0",
			borderTopWidth: "0",
			paddingBottom: "0",
			paddingTop: "0",
		}),
		matchMedia: () => ({ matches: false, addEventListener() {}, removeEventListener() {} }),
		requestAnimationFrame: (): number => 1,
		cancelAnimationFrame: () => {},
		ResizeObserver: class {
			observe() {}
			unobserve() {}
			disconnect() {}
		},
		IS_REACT_ACT_ENVIRONMENT: true,
	})) {
		previousGlobals.set(key, Object.getOwnPropertyDescriptor(globalThis, key));
		Object.defineProperty(globalThis, key, { configurable: true, writable: true, value });
	}
	resetSessionStoreForTest();
	// Drain query observer notifications inside act; no timer may outlive the DOM fixture.
	notifyManager.setScheduler(queueMicrotask);
	instance = i18next.createInstance();
	await instance.use(initReactI18next).init({
		lng: "en",
		resources: {
			en: {
				narrator: {
					draftConflict: "draft conflict",
					draftUseServer: "use server",
					draftUseLocal: "use local",
				},
			},
		},
		react: { useSuspense: false },
	});
	qc = new QueryClient({
		defaultOptions: { queries: { retry: false, gcTime: Infinity, staleTime: Infinity } },
	});
	qc.setQueryData(["auth", "me"], { id: "u1" });
	qc.setQueryData(["named-narrators"], []);
	panelSpy = spyOn(api, "getNarratorPanelState").mockResolvedValue({
		permissions: [],
		buffer: [],
		draft: { hasDraft: false, revision: 1, updatedAt: null },
		hasMore: { permissions: false, buffer: false },
	});
	getSpy = spyOn(api, "getNarratorDraft").mockResolvedValue(initial);
	suggestionSpy = spyOn(api, "getNarratorComposerSuggestion").mockResolvedValue({
		suggestion: null,
	});
	updateSpy = spyOn(api, "updateNarratorDraft").mockImplementation(
		async (_id, text, revision, sourceId, fileReferences) => ({
			...initial,
			ok: true,
			traits: [],
			text,
			fileReferences,
			revision: revision + 1,
			sourceId: sourceId ?? null,
			hasDraft: !!text || !!fileReferences?.length,
		}),
	);
	host = document.createElement("div");
	document.body.append(host);
	root = createRoot(host);
	handle = createRef<NarratorComposerHandle>();
	sendingRef = { current: false };
	flags = [];
	renders = 0;
	sendCalls = 0;
	suggestionProps = { isPrimary: false, composerSuggestionAvailable: false, hasAttachments: false };
	testNarratorId = "n1";
});

afterEach(async () => {
	await act(async () => root.unmount());
	host.remove();
	qc.clear();
	notifyManager.setScheduler((callback) => setTimeout(callback, 0));
	getSpy.mockRestore();
	suggestionSpy.mockRestore();
	panelSpy.mockRestore();
	updateSpy.mockRestore();
	resetSessionStoreForTest();
	for (const [key, descriptor] of previousGlobals) {
		if (descriptor) Object.defineProperty(globalThis, key, descriptor);
		else Reflect.deleteProperty(globalThis, key);
	}
	previousGlobals.clear();
});

async function render() {
	await act(async () =>
		root.render(
			<QueryClientProvider client={qc}>
				<MantineProvider env="test">
					<I18nextProvider i18n={instance}>
						<FileReferenceScopeProvider
							value={{ narratorId: "n1", context: { deviceId: "RemoteABC", cwd: "/work" } }}
						>
							<Harness />
						</FileReferenceScopeProvider>
					</I18nextProvider>
				</MantineProvider>
			</QueryClientProvider>,
		),
	);
}

function deferred<T>() {
	let resolve!: (value: T) => void;
	const promise = new Promise<T>((complete) => {
		resolve = complete;
	});
	return { promise, resolve };
}

describe("composer revision-only initial reconciliation", () => {
	for (const state of [
		{ text: "acknowledged text", fileReferences: [reference] },
		{ text: "", fileReferences: [reference] },
		{ text: "", fileReferences: [] },
	]) {
		test(`delayed metadata preserves the acknowledged marker for ${JSON.stringify(state)}`, async () => {
			const pending = deferred<Awaited<ReturnType<typeof api.getNarratorPanelState>>>();
			panelSpy.mockReturnValue(pending.promise);
			persistNarratorInputDraft("u1", "n1", state.text, 7, "t7", state.fileReferences, state);
			const acknowledged = readNarratorInputDraft("u1", "n1");
			await render();
			expect(handle.current?.getText()).toBe(state.text);
			expect(handle.current?.getFileReferences()).toEqual(state.fileReferences);
			expect(readNarratorInputDraft("u1", "n1")).toEqual(acknowledged);
			expect(getSpy).not.toHaveBeenCalled();
			expect(updateSpy).not.toHaveBeenCalled();
			await act(async () =>
				pending.resolve({
					permissions: [],
					buffer: [],
					draft: {
						hasDraft: !!(state.text || state.fileReferences.length),
						revision: 7,
						updatedAt: "t7",
					},
					hasMore: { permissions: false, buffer: false },
				}),
			);
			expect(readNarratorInputDraft("u1", "n1")).toEqual(acknowledged);
			expect(getSpy).not.toHaveBeenCalled();
		});
	}

	for (const remote of [
		{
			...initial,
			hasDraft: true,
			text: "new elsewhere",
			fileReferences: [{ ...reference, id: "remote" }],
			revision: 8,
		},
		{ ...initial, revision: 8 },
	]) {
		test(`a quick switch during metadata loading adopts the newer remote ${remote.hasDraft ? "edit" : "clear"}`, async () => {
			const pending = deferred<Awaited<ReturnType<typeof api.getNarratorPanelState>>>();
			panelSpy.mockReturnValueOnce(pending.promise);
			const state = { text: "old acknowledged", fileReferences: [reference] };
			persistNarratorInputDraft("u1", "n1", state.text, 7, "t7", state.fileReferences, state);
			await render();
			testNarratorId = "n2";
			await render();
			getSpy.mockClear();
			panelSpy.mockResolvedValue({
				permissions: [],
				buffer: [],
				draft: { hasDraft: remote.hasDraft, revision: 8, updatedAt: "t8" },
				hasMore: { permissions: false, buffer: false },
			});
			getSpy.mockResolvedValue({ ...remote, updatedAt: "t8" });
			testNarratorId = "n1";
			await render();
			expect(handle.current?.getText()).toBe(remote.text);
			expect(handle.current?.getFileReferences()).toEqual(remote.fileReferences);
			expect(host.textContent).not.toContain("draft conflict");
			expect(getSpy).toHaveBeenCalledTimes(1);
			expect(readNarratorInputDraft("u1", "n1").syncedRevision).toBe(8);
			expect(readNarratorInputDraft("u1", "n1").unsynced).toBeUndefined();
			await act(async () =>
				pending.resolve({
					permissions: [],
					buffer: [],
					draft: { hasDraft: true, revision: 7, updatedAt: "t7" },
					hasMore: { permissions: false, buffer: false },
				}),
			);
			expect(handle.current?.getText()).toBe(remote.text);
			expect(readNarratorInputDraft("u1", "n1").syncedRevision).toBe(8);
			expect(updateSpy).not.toHaveBeenCalled();
		});
	}

	for (const edit of ["text", "references", "clear"] as const) {
		test(`a real ${edit} edit during metadata loading remains dirty across switching`, async () => {
			const pending = deferred<Awaited<ReturnType<typeof api.getNarratorPanelState>>>();
			panelSpy.mockReturnValueOnce(pending.promise);
			const state = { text: "acknowledged", fileReferences: [reference] };
			persistNarratorInputDraft("u1", "n1", state.text, 7, "t7", state.fileReferences, state);
			await render();
			await act(async () => {
				if (edit === "text") handle.current?.setText("changed");
				else if (edit === "references")
					handle.current?.setFileReferences([{ ...reference, path: "/work/new.ts" }]);
				else handle.current?.clearTextAndDraft();
			});
			const edited = readNarratorInputDraft("u1", "n1");
			expect(edited.unsynced).toBe(true);
			expect(edited.syncedRevision).toBeUndefined();
			expect(updateSpy).not.toHaveBeenCalled();
			testNarratorId = "n2";
			await render();
			getSpy.mockResolvedValue({ ...initial, hasDraft: true, text: "remote newer", revision: 8 });
			testNarratorId = "n1";
			await render();
			expect(handle.current?.getText()).toBe(edited.text);
			expect(handle.current?.getFileReferences()).toEqual(edited.fileReferences ?? []);
			expect(readNarratorInputDraft("u1", "n1").unsynced).toBe(true);
			expect(host.textContent).toContain("draft conflict");
		});
	}

	for (const serverRevision of [1, null]) {
		test(`an unsynced clear with base ${serverRevision} stays dirty before metadata returns`, async () => {
			const pending = deferred<Awaited<ReturnType<typeof api.getNarratorPanelState>>>();
			panelSpy.mockReturnValue(pending.promise);
			persistNarratorInputDraft("u1", "n1", "", serverRevision, null, [], {
				text: "cleared but not acknowledged",
				fileReferences: [reference],
			});
			await render();
			expect(readNarratorInputDraft("u1", "n1").unsynced).toBe(true);
			expect(readNarratorInputDraft("u1", "n1").syncedRevision).toBeUndefined();
			expect(handle.current?.isTextEmpty()).toBe(true);
		});
	}

	for (const text of ["", "legacy unknown"]) {
		test(`an unknown local baseline ${JSON.stringify(text)} is never stamped acknowledged`, async () => {
			const pending = deferred<Awaited<ReturnType<typeof api.getNarratorPanelState>>>();
			panelSpy.mockReturnValue(pending.promise);
			persistNarratorInputDraft("u1", "n1", text, 1, null);
			await render();
			expect(readNarratorInputDraft("u1", "n1").syncedRevision).toBeUndefined();
			expect(handle.current?.getText()).toBe(text);
		});
	}
	for (const text of ["", "unknown restored text"]) {
		test(`editing then returning to an unknown ${JSON.stringify(text)} snapshot does not erase dirty state`, async () => {
			const pending = deferred<Awaited<ReturnType<typeof api.getNarratorPanelState>>>();
			panelSpy.mockReturnValue(pending.promise);
			persistNarratorInputDraft("u1", "n1", text, null, null);
			await render();
			await act(async () => handle.current?.setText("actually edited"));
			expect(readNarratorInputDraft("u1", "n1").unsynced).toBe(true);
			await act(async () => handle.current?.setText(text));
			expect(readNarratorInputDraft("u1", "n1").unsynced).toBe(true);
			expect(readNarratorInputDraft("u1", "n1").syncedRevision).toBeUndefined();
			getSpy.mockResolvedValue({ ...initial, hasDraft: true, text: "new remote", revision: 2 });
			await act(async () =>
				pending.resolve({
					permissions: [],
					buffer: [],
					draft: { hasDraft: true, revision: 2, updatedAt: null },
					hasMore: { permissions: false, buffer: false },
				}),
			);
			expect(handle.current?.getText()).toBe(text);
			expect(host.textContent).toContain("draft conflict");
			expect(readNarratorInputDraft("u1", "n1").unsynced).toBe(true);
		});
	}

	for (const localKind of ["acknowledged", "unsynced clear", "new empty"] as const) {
		test(`WS during delayed metadata handles ${localKind} without assuming an empty sync baseline`, async () => {
			const pending = deferred<Awaited<ReturnType<typeof api.getNarratorPanelState>>>();
			panelSpy.mockReturnValue(pending.promise);
			if (localKind === "acknowledged") {
				const state = { text: "ack", fileReferences: [reference] };
				persistNarratorInputDraft("u1", "n1", state.text, 1, null, state.fileReferences, state);
			} else if (localKind === "unsynced clear") {
				persistNarratorInputDraft("u1", "n1", "", 1, null, [], null);
			}
			await render();
			await act(async () =>
				handle.current?.handleDraftChanged({
					...initial,
					hasDraft: true,
					text: "new WS",
					fileReferences: [reference],
					revision: 2,
					sourceId: "other",
				}),
			);
			if (localKind === "unsynced clear") {
				expect(handle.current?.isTextEmpty()).toBe(true);
				expect(host.textContent).toContain("draft conflict");
				expect(readNarratorInputDraft("u1", "n1").unsynced).toBe(true);
			} else {
				expect(handle.current?.getText()).toBe("new WS");
				expect(handle.current?.getFileReferences()).toEqual([reference]);
				expect(host.textContent).not.toContain("draft conflict");
				expect(readNarratorInputDraft("u1", "n1").syncedRevision).toBe(2);
			}
			await act(async () =>
				pending.resolve({
					permissions: [],
					buffer: [],
					draft: { hasDraft: true, revision: 1, updatedAt: null },
					hasMore: { permissions: false, buffer: false },
				}),
			);
			expect(handle.current?.getText()).toBe(localKind === "unsynced clear" ? "" : "new WS");
			expect(getSpy).not.toHaveBeenCalled();
		});
	}

	test("restores an acknowledged local text/reference snapshot without fetching the body", async () => {
		const state = { text: "local acknowledged", fileReferences: [reference] };
		persistNarratorInputDraft("u1", "n1", state.text, 1, null, state.fileReferences, state);
		panelSpy.mockResolvedValue({
			permissions: [],
			buffer: [],
			draft: { hasDraft: true, revision: 1, updatedAt: null },
			hasMore: { permissions: false, buffer: false },
		});
		await render();
		expect(handle.current?.getText()).toBe(state.text);
		expect(handle.current?.getFileReferences()).toEqual(state.fileReferences);
		expect(getSpy).not.toHaveBeenCalled();
		expect(panelSpy).toHaveBeenCalledTimes(1);
		expect(updateSpy).not.toHaveBeenCalled();
	});

	test("a dirty local CAS baseline is not mistaken for a synchronized revision", async () => {
		persistNarratorInputDraft("u1", "n1", "dirty", 1, null, [reference], {
			text: "old",
			fileReferences: [],
		});
		getSpy.mockResolvedValue({ ...initial, hasDraft: true, text: "old" });
		await render();
		expect(getSpy).toHaveBeenCalledTimes(1);
		expect(handle.current?.getText()).toBe("dirty");
		expect(handle.current?.getFileReferences()).toEqual([reference]);
		expect(readNarratorInputDraft("u1", "n1").unsynced).toBe(true);
	});

	test("metadata arriving after typing preserves both new text and attachments", async () => {
		const pending = deferred<Awaited<ReturnType<typeof api.getNarratorPanelState>>>();
		panelSpy.mockReturnValue(pending.promise);
		const state = { text: "synced", fileReferences: [reference] };
		persistNarratorInputDraft("u1", "n1", state.text, 1, null, state.fileReferences, state);
		await render();
		expect(handle.current?.getText()).toBe("synced");
		const newerRefs = [{ ...reference, id: "new", path: "/work/new.ts" }];
		await act(async () => handle.current?.restoreInput("typed during metadata", newerRefs));
		await act(async () =>
			pending.resolve({
				permissions: [],
				buffer: [],
				draft: { hasDraft: true, revision: 1, updatedAt: "t1" },
				hasMore: { permissions: false, buffer: false },
			}),
		);
		expect(handle.current?.getText()).toBe("typed during metadata");
		expect(handle.current?.getFileReferences()).toEqual(newerRefs);
		expect(getSpy).not.toHaveBeenCalled();
		expect(readNarratorInputDraft("u1", "n1").unsynced).toBe(true);
	});

	test("a newer server body conflicts rather than dropping unsynced stored references", async () => {
		persistNarratorInputDraft("u1", "n1", "dirty", 1, null, [reference], {
			text: "old",
			fileReferences: [],
		});
		getSpy.mockResolvedValue({
			...initial,
			hasDraft: true,
			text: "other",
			fileReferences: [{ ...reference, deviceId: "other" }],
			revision: 2,
		});
		await render();
		expect(host.textContent).toContain("draft conflict");
		expect(handle.current?.getText()).toBe("dirty");
		expect(handle.current?.getFileReferences()).toEqual([reference]);
	});

	test("a late body cannot erase text and reference edits made while it loaded", async () => {
		const pending = deferred<NarratorRemoteDraft>();
		getSpy.mockReturnValue(pending.promise);
		await render();
		await act(async () => handle.current?.restoreInput("new local", [reference]));
		await act(async () =>
			pending.resolve({ ...initial, hasDraft: true, text: "remote", revision: 2 }),
		);
		expect(handle.current?.getText()).toBe("new local");
		expect(handle.current?.getFileReferences()).toEqual([reference]);
		expect(host.textContent).toContain("draft conflict");
	});

	test("a WS revision invalidates an older hydration response", async () => {
		const pending = deferred<Awaited<ReturnType<typeof api.getNarratorPanelState>>>();
		panelSpy.mockReturnValue(pending.promise);
		persistNarratorInputDraft("u1", "n1", "", 1, null, [], { text: "", fileReferences: [] });
		await render();
		await act(async () =>
			handle.current?.handleDraftChanged({
				...initial,
				hasDraft: true,
				text: "new WS",
				revision: 2,
				fileReferences: [reference],
				sourceId: "other",
			}),
		);
		await act(async () =>
			pending.resolve({
				permissions: [],
				buffer: [],
				draft: { hasDraft: false, revision: 1, updatedAt: null },
				hasMore: { permissions: false, buffer: false },
			}),
		);
		expect(handle.current?.getText()).toBe("new WS");
		expect(handle.current?.getFileReferences()).toEqual([reference]);
		expect(getSpy).not.toHaveBeenCalled();
	});

	for (const error of [new ApiError("old server", 404), new TypeError("network failure")]) {
		test(`falls back to the old body endpoint for ${error.message}`, async () => {
			panelSpy.mockRejectedValue(error);
			getSpy.mockResolvedValue({
				...initial,
				hasDraft: true,
				text: "legacy",
				fileReferences: [reference],
			});
			await render();
			expect(getSpy).toHaveBeenCalledTimes(1);
			expect(handle.current?.getText()).toBe("legacy");
			expect(handle.current?.getFileReferences()).toEqual([reference]);
		});
	}

	test("authorization failure is not bypassed through legacy endpoints", async () => {
		panelSpy.mockRejectedValue(new ApiError("forbidden", 403));
		await render();
		expect(getSpy).not.toHaveBeenCalled();
	});

	for (const switchKind of ["account", "narrator"] as const) {
		test(`does not persist the previous pair's text or attachments under a new ${switchKind}`, async () => {
			const state = { text: "previous pair private text", fileReferences: [reference] };
			persistNarratorInputDraft("u1", "n1", state.text, 1, null, state.fileReferences, state);
			panelSpy.mockResolvedValue({
				permissions: [],
				buffer: [],
				draft: { hasDraft: true, revision: 1, updatedAt: null },
				hasMore: { permissions: false, buffer: false },
			});
			await render();
			expect(handle.current?.getText()).toBe(state.text);
			if (switchKind === "account")
				await act(async () => qc.setQueryData(["auth", "me"], { id: "u2" }));
			else testNarratorId = "n2";
			await render();
			expect(handle.current?.getText()).toBe("");
			expect(handle.current?.getFileReferences()).toEqual([]);
			const next = readNarratorInputDraft(switchKind === "account" ? "u2" : "u1", testNarratorId);
			expect(next.text).toBe("");
			expect(next.fileReferences).toBeUndefined();
			expect(readNarratorInputDraft("u1", "n1").text).toBe(state.text);
		});
		test(`ignores the old pair's body after switching ${switchKind}`, async () => {
			const pending = deferred<NarratorRemoteDraft>();
			getSpy.mockReturnValueOnce(pending.promise);
			await render();
			if (switchKind === "account")
				await act(async () => qc.setQueryData(["auth", "me"], { id: "u2" }));
			else {
				testNarratorId = "n2";
				await render();
			}
			await act(async () =>
				pending.resolve({
					...initial,
					hasDraft: true,
					text: "old pair secret",
					fileReferences: [reference],
				}),
			);
			expect(handle.current?.getText()).toBe("");
			expect(handle.current?.getFileReferences()).toEqual([]);
			expect(
				readNarratorInputDraft(switchKind === "account" ? "u2" : "u1", testNarratorId).text,
			).toBe("");
		});
	}
});

describe("composer Enter focus", () => {
	test("mounted composer registers Tab focus without swallowing its own Tab or completion", async () => {
		await render();
		const textarea = host.querySelector("textarea");
		if (!textarea) throw new Error("Missing composer textarea");
		Object.defineProperty(textarea, "getClientRects", { value: () => [{}] });
		Object.defineProperty(textarea, "getBoundingClientRect", {
			value: () => ({ top: 10, bottom: 100, left: 0, right: 300 }),
		});
		Object.assign(window, { innerHeight: 600, innerWidth: 800 });
		const focus = spyOn(textarea, "focus").mockImplementation(() => {});
		const press = (target: Element) => {
			const event = new Event("keydown", { bubbles: true, cancelable: true });
			Object.assign(event, {
				key: "Tab",
				code: "Tab",
				ctrlKey: false,
				metaKey: false,
				shiftKey: false,
				altKey: false,
				isComposing: false,
				repeat: false,
			});
			target.dispatchEvent(event);
			return event;
		};
		expect(press(document.body).defaultPrevented).toBe(true);
		expect(focus).toHaveBeenCalledTimes(1);
		// linkedom needs the same focus/value-watcher setup as FileReferencePopover tests.
		Object.assign(textarea, { attachEvent() {}, detachEvent() {} });
		act(() => {
			textarea.dispatchEvent(new Event("focusin", { bubbles: true }));
		});
		expect(press(textarea).defaultPrevented).toBe(false);
		const complete = (event: Event) => event.preventDefault();
		textarea.addEventListener("keydown", complete, true);
		expect(press(textarea).defaultPrevented).toBe(true);
		textarea.removeEventListener("keydown", complete, true);
		expect(focus).toHaveBeenCalledTimes(1);
		focus.mockRestore();
	});
	test("focuses from the page without sending; leaves controls and modified Enter alone", async () => {
		await render();
		const textarea = host.querySelector("textarea");
		if (!textarea) throw new Error("Missing composer textarea");
		Object.defineProperty(textarea, "getClientRects", { value: () => [{}] });
		const focus = spyOn(textarea, "focus").mockImplementation(() => {});
		const press = (target: Element, extra = {}) => {
			const event = new Event("keydown", { bubbles: true, cancelable: true });
			Object.assign(event, { key: "Enter", ...extra });
			target.dispatchEvent(event);
			return event;
		};
		expect(press(document.body).defaultPrevented).toBe(true);
		expect(focus).toHaveBeenCalledTimes(1);
		for (const extra of [
			{ shiftKey: true },
			{ ctrlKey: true },
			{ altKey: true },
			{ metaKey: true },
			{ isComposing: true },
			{ repeat: true },
		]) {
			expect(press(document.body, extra).defaultPrevented).toBe(false);
		}
		for (const tag of ["input", "button", "select"]) {
			const control = document.createElement(tag);
			host.append(control);
			expect(press(control).defaultPrevented).toBe(false);
		}
		const modal = document.createElement("div");
		modal.setAttribute("aria-modal", "true");
		host.append(modal);
		expect(press(document.body).defaultPrevented).toBe(false);
		expect(focus).toHaveBeenCalledTimes(1);
		focus.mockRestore();
	});
});

function composerTextarea(): HTMLTextAreaElement {
	const textarea = host.querySelector("textarea");
	if (!textarea) throw new Error("Missing composer textarea");
	Object.assign(textarea, { attachEvent() {}, detachEvent() {} });
	return textarea;
}

function focusTextarea(textarea: HTMLTextAreaElement) {
	Object.assign(textarea, {
		attachEvent() {},
		detachEvent() {},
		selectionStart: 0,
		selectionEnd: 0,
	});
	Object.defineProperty(document, "activeElement", { configurable: true, value: textarea });
	textarea.dispatchEvent(new Event("focusin", { bubbles: true }));
}

function keyDown(textarea: HTMLTextAreaElement, extra = {}): Event {
	const event = new Event("keydown", { bubbles: true, cancelable: true });
	Object.assign(event, {
		key: "Tab",
		code: "Tab",
		ctrlKey: false,
		metaKey: false,
		shiftKey: false,
		altKey: false,
		isComposing: false,
		repeat: false,
		...extra,
	});
	textarea.dispatchEvent(event);
	return event;
}

const nextSuggestion = "Run the focused tests and report the result.";
function enableSuggestions() {
	suggestionProps = { isPrimary: true, composerSuggestionAvailable: true, hasAttachments: false };
	suggestionSpy.mockResolvedValue({ suggestion: nextSuggestion });
}

function suggestionButton(scope: ParentNode = host) {
	return [...scope.querySelectorAll("button")].find(
		(button) =>
			button.textContent === "composerAcceptSuggestion" ||
			button.getAttribute("aria-label") === "composerAcceptSuggestion",
	);
}

describe("Codex Composer cached suggestions", () => {
	test("subagents never query or display suggestions, even when idle or taken over", async () => {
		suggestionProps.composerSuggestionAvailable = true;
		qc.setQueryData(["narrators", "n1", "composer-suggestion"], { suggestion: nextSuggestion });
		await render();
		expect(suggestionSpy).not.toHaveBeenCalled();
		expect(composerTextarea().placeholder).toBe("sendPlaceholder");
		expect(suggestionButton()).toBeUndefined();
	});

	test("empty input displays only a placeholder; adoption uses the draft path without sending", async () => {
		enableSuggestions();
		await render();
		expect(composerTextarea().placeholder).toBe(nextSuggestion);
		expect(handle.current?.getText()).toBe("");
		expect(readNarratorInputDraft("u1", "n1").text).toBe("");
		expect(updateSpy).not.toHaveBeenCalled();
		expect(suggestionSpy).toHaveBeenCalledTimes(1);
		expect(suggestionButton()).toBeDefined();
		const controls = suggestionButton()?.closest(".mantine-Textarea-section");
		expect(controls).not.toBeNull();
		expect(controls?.querySelectorAll("button").length).toBe(2);
		await act(async () => suggestionButton()?.click());
		expect(handle.current?.getText()).toBe(nextSuggestion);
		expect(readNarratorInputDraft("u1", "n1").text).toBe(nextSuggestion);
		expect(suggestionButton()).toBeUndefined();
		expect(sendCalls).toBe(0);
	});

	test("a hydrated draft and even whitespace win over a cached suggestion", async () => {
		enableSuggestions();
		getSpy.mockResolvedValue({ ...initial, hasDraft: true, text: "my real draft" });
		await render();
		expect(handle.current?.getText()).toBe("my real draft");
		expect(composerTextarea().placeholder).toBe("sendPlaceholder");
		expect(suggestionButton()).toBeUndefined();
		await act(async () => handle.current?.setText(" "));
		expect(suggestionButton()).toBeUndefined();
		await act(async () => handle.current?.setText(""));
		expect(composerTextarea().placeholder).toBe(nextSuggestion);
	});

	test("late cache reads never overwrite text typed while the GET was in flight", async () => {
		enableSuggestions();
		let resolve!: (value: { suggestion: string }) => void;
		suggestionSpy.mockImplementationOnce(
			() =>
				new Promise((done) => {
					resolve = done;
				}),
		);
		await render();
		await act(async () => handle.current?.setText("typed before the response"));
		await act(async () => {
			resolve({ suggestion: nextSuggestion });
			await new Promise((done) => setTimeout(done, 0));
		});
		expect(handle.current?.getText()).toBe("typed before the response");
		expect(readNarratorInputDraft("u1", "n1").text).toBe("typed before the response");
		expect(suggestionButton()).toBeUndefined();
		expect(sendCalls).toBe(0);
	});

	test("working, attachments and references hide it; clearing reveals the current cache, not subagents", async () => {
		enableSuggestions();
		await render();
		suggestionProps.composerSuggestionAvailable = false;
		await render();
		expect(suggestionButton()).toBeUndefined();
		suggestionProps.composerSuggestionAvailable = true;
		suggestionProps.hasAttachments = true;
		await render();
		expect(suggestionButton()).toBeUndefined();
		suggestionProps.hasAttachments = false;
		await render();
		await act(async () => handle.current?.restoreInput("", [reference]));
		expect(suggestionButton()).toBeUndefined();
		await act(async () => handle.current?.restoreInput("", []));
		expect(composerTextarea().placeholder).toBe(nextSuggestion);
		suggestionProps.isPrimary = false;
		await render();
		expect(suggestionButton()).toBeUndefined();
		expect(suggestionSpy).toHaveBeenCalledTimes(1);
	});

	test("quote-only input hides inline and fullscreen suggestions without replacing the quote", async () => {
		enableSuggestions();
		await render();
		const staleAccept = suggestionButton();
		const quoted = joinQuotedComposerInput(
			[{ label: "Assistant message", text: "quoted content", anchor: "m:message1" }],
			{ text: "", fileReferences: [] },
		);
		await act(async () => {
			handle.current?.restoreInput(quoted.text, quoted.fileReferences);
			staleAccept?.click();
		});
		const textarea = composerTextarea();
		expect(textarea.value).toBe("");
		expect(textarea.placeholder).toBe("quote.placeholder");
		expect(suggestionButton()).toBeUndefined();
		act(() => focusTextarea(textarea));
		act(() => expect(keyDown(textarea).defaultPrevented).toBe(false));
		expect(handle.current?.getText()).toBe(quoted.text);
		expect(readNarratorInputDraft("u1", "n1").text).toBe(quoted.text);
		const expand = [
			...host.querySelectorAll<HTMLButtonElement>(".mantine-Textarea-section button"),
		].at(-1);
		if (!expand) throw new Error("Missing expand button");
		await act(async () => expand.click());
		const dialog = document.querySelector('[role="dialog"]');
		if (!dialog) throw new Error("Missing fullscreen dialog");
		expect(dialog.querySelector("textarea")?.value).toBe("");
		expect(dialog.querySelector("textarea")?.placeholder).not.toBe(nextSuggestion);
		expect(suggestionButton(dialog)).toBeUndefined();
		expect(handle.current?.getText()).toBe(quoted.text);
		expect(sendCalls).toBe(0);
	});

	test("Tab requires focused desktop input and preserves modifiers, IME and completion ownership", async () => {
		enableSuggestions();
		await render();
		const textarea = composerTextarea();
		act(() => focusTextarea(textarea));
		Object.defineProperty(document, "activeElement", { configurable: true, value: document.body });
		act(() => expect(keyDown(textarea).defaultPrevented).toBe(false));
		act(() => focusTextarea(textarea));
		for (const extra of [
			{ shiftKey: true },
			{ ctrlKey: true },
			{ metaKey: true },
			{ altKey: true },
			{ isComposing: true },
		]) {
			act(() => expect(keyDown(textarea, extra).defaultPrevented).toBe(false));
			expect(handle.current?.getText()).toBe("");
		}
		const completion = (event: Event) => event.preventDefault();
		textarea.addEventListener("keydown", completion, true);
		act(() => keyDown(textarea));
		expect(handle.current?.getText()).toBe("");
		textarea.removeEventListener("keydown", completion, true);
		Object.assign(window, { innerWidth: 360 });
		act(() => expect(keyDown(textarea).defaultPrevented).toBe(false));
		Object.assign(window, { innerWidth: 1_024 });
		act(() => expect(keyDown(textarea).defaultPrevented).toBe(true));
		expect(handle.current?.getText()).toBe(nextSuggestion);
		await act(async () => handle.current?.setText(""));
		// 229 starts React's legacy IME fallback in linkedom; exercise it last.
		act(() => expect(keyDown(textarea, { keyCode: 229 }).defaultPrevented).toBe(false));
		act(() => expect(keyDown(textarea).defaultPrevented).toBe(false));
		expect(handle.current?.getText()).toBe("");
		expect(sendCalls).toBe(0);
	});

	test("fullscreen adoption stays local; Escape cancels and confirmation alone copies to the draft", async () => {
		enableSuggestions();
		await render();
		const expand = [
			...host.querySelectorAll<HTMLButtonElement>(".mantine-Textarea-section button"),
		].at(-1);
		if (!expand) throw new Error("Missing expand button");
		await act(async () => expand.click());
		const dialog = document.querySelector('[role="dialog"]');
		if (!dialog) throw new Error("Missing fullscreen dialog");
		const fullscreenInput = dialog.querySelector("textarea");
		if (!fullscreenInput) throw new Error("Missing fullscreen input");
		expect(fullscreenInput.placeholder).toBe(nextSuggestion);
		await act(async () => suggestionButton(dialog)?.click());
		expect(fullscreenInput.value).toBe(nextSuggestion);
		expect(handle.current?.getText()).toBe("");
		expect(readNarratorInputDraft("u1", "n1").text).toBe("");
		act(() => focusTextarea(fullscreenInput));
		act(() => keyDown(fullscreenInput, { keyCode: 229 }));
		await act(async () => keyDown(fullscreenInput, { key: "Escape", code: "Escape" }));
		expect(handle.current?.getText()).toBe("");
		await act(async () => expand.click());
		const reopened = document.querySelector('[role="dialog"]');
		if (!reopened) throw new Error("Missing reopened dialog");
		expect(reopened.querySelector("textarea")?.value).toBe("");
		const reopenedInput = reopened.querySelector("textarea");
		if (!reopenedInput) throw new Error("Missing reopened textarea");
		act(() => focusTextarea(reopenedInput));
		act(() => expect(keyDown(reopenedInput).defaultPrevented).toBe(true));
		expect(handle.current?.getText()).toBe("");
		const confirm = [...reopened.querySelectorAll("button")].find(
			(button) => button.textContent === "confirm",
		);
		await act(async () => confirm?.click());
		expect(handle.current?.getText()).toBe(nextSuggestion);
		expect(readNarratorInputDraft("u1", "n1").text).toBe(nextSuggestion);
		expect(sendCalls).toBe(0);
	});
});

describe("composer atomic reference contract", () => {
	test.each([
		"",
		"next unsent message",
	])("confirmed sends mirror the remaining draft %j before a delayed clear response", async (nextText) => {
		await render();
		await act(async () => handle.current?.restoreInput("already sent", [reference]));
		expect(readNarratorInputDraft("u1", "n1").unsynced).toBe(true);
		const pending = deferred<Awaited<ReturnType<typeof api.updateNarratorDraft>>>();
		updateSpy.mockReturnValueOnce(pending.promise);
		sendingRef.current = true;
		await act(async () => handle.current?.hideTextForSend());
		// Preserve the original draft until sending succeeds, in case sending fails.
		expect(readNarratorInputDraft("u1", "n1").text).toBe("already sent");
		await act(async () => {
			if (nextText) handle.current?.restoreInput(nextText, []);
			handle.current?.commitDraftAfterSend();
		});
		expect(updateSpy).toHaveBeenCalledWith("n1", "", 1, expect.any(String), []);
		expect(readNarratorInputDraft("u1", "n1").text).toBe(nextText);
		expect(readNarratorInputDraft("u1", "n1").fileReferences ?? []).toEqual([]);

		// Switch away before the clear response arrives: its callbacks become stale.
		testNarratorId = "n2";
		await render();
		sendingRef.current = false;
		const cleared = { ...initial, revision: 2 };
		await act(async () => pending.resolve({ ...cleared, ok: true, traits: [] }));
		getSpy.mockResolvedValue(cleared);
		panelSpy.mockResolvedValue({
			permissions: [],
			buffer: [],
			draft: { hasDraft: false, revision: 2, updatedAt: null },
			hasMore: { permissions: false, buffer: false },
		});
		testNarratorId = "n1";
		await render();
		expect(handle.current?.getText()).toBe(nextText);
		expect(handle.current?.getFileReferences()).toEqual([]);
		if (!nextText) expect(host.textContent).not.toContain("draft conflict");
	});

	test("same-tick restore and add expose detached text/reference state without rerendering the parent", async () => {
		await render();
		const before = renders;
		await act(async () => {
			handle.current?.addFileReference(reference);
			expect(handle.current?.getText()).toBe("#file:src/a.ts ");
			expect(handle.current?.getFileReferences()[0]).toMatchObject(reference);
			const copy = handle.current?.getFileReferences() ?? [];
			copy[0].path = "/mutated";
			expect(handle.current?.getFileReferences()[0].path).toBe(reference.path);
		});
		await act(async () => handle.current?.appendText("more"));
		expect(renders).toBe(before);
		expect(flags).toEqual([false, true]);
		expect(handle.current?.getFileReferences()).toHaveLength(1);
	});
	test("hide for send keeps persisted draft, failure restore brings back its refs, clear syncs []", async () => {
		await render();
		const state = insertFileReference({ text: "read ", fileReferences: [] }, reference);
		await act(async () => handle.current?.restoreInput(state.text, state.fileReferences));
		expect(readNarratorInputDraft("u1", "n1").fileReferences).toEqual(state.fileReferences);
		sendingRef.current = true;
		await act(async () => handle.current?.hideTextForSend());
		expect(handle.current?.getText()).toBe("");
		expect(handle.current?.getFileReferences()).toEqual([]);
		expect(readNarratorInputDraft("u1", "n1").fileReferences).toEqual(state.fileReferences);
		sendingRef.current = false;
		await act(async () => handle.current?.restoreInput(state.text, state.fileReferences));
		expect(handle.current?.getFileReferences()).toEqual(state.fileReferences);
		await act(async () => handle.current?.clearTextAndDraft());
		expect(updateSpy.mock.calls[0]?.[4]).toEqual([]);
		expect(handle.current?.getFileReferences()).toEqual([]);
		expect(readNarratorInputDraft("u1", "n1").fileReferences).toBeUndefined();
	});
	test("quote prefix renders as chips, keeps the textarea to the body, and persists with the draft", async () => {
		await render();
		const prefix =
			"> [quote] Assistant message\n> hello\n> world\n| [comment]\n| explain this quote\n\n";
		const body = insertFileReference({ text: "explain ", fileReferences: [] }, reference);
		const full = joinQuotedComposerInput(
			[{ label: "Assistant message", text: "hello\nworld", comment: "explain this quote" }],
			body,
		);
		expect(full.text).toBe(`${prefix}${body.text}`);
		await act(async () => handle.current?.restoreInput(full.text, full.fileReferences));
		const textarea = host.querySelector("textarea");
		expect(textarea?.value).toBe(body.text);
		expect(host.textContent).toContain("Assistant message");
		expect(handle.current?.getText()).toBe(`${prefix}${body.text}`);
		expect(handle.current?.getFileReferences()[0].inputRange?.[0]).toBe(
			prefix.length + "explain ".length,
		);
		expect(readNarratorInputDraft("u1", "n1").text).toBe(`${prefix}${body.text}`);
		await act(async () => handle.current?.appendText("more"));
		expect(handle.current?.getText().startsWith(prefix)).toBe(true);
		expect(textarea?.value).toBe(`${body.text}\nmore`);
		const expand = [
			...host.querySelectorAll<HTMLButtonElement>(".mantine-Textarea-section button"),
		].at(-1);
		if (!expand) throw new Error("Missing expand button");
		await act(async () => expand.click());
		const dialog = document.querySelector('[role="dialog"]');
		if (!dialog) throw new Error("Missing fullscreen dialog");
		expect(dialog.querySelector("textarea")?.value).toBe(`${body.text}\nmore`);
		const confirm = [...dialog.querySelectorAll("button")].find(
			(button) => button.textContent === "confirm",
		);
		if (!confirm) throw new Error("Missing fullscreen confirm button");
		await act(async () => confirm.click());
		expect(handle.current?.getText()).toBe(`${prefix}${body.text}\nmore`);
		expect(handle.current?.getFileReferences()).toEqual(full.fileReferences);
		expect(readNarratorInputDraft("u1", "n1").text).toBe(`${prefix}${body.text}\nmore`);
	});
	test("metadata-only input is sendable and conflicts with a different remote target", async () => {
		await render();
		await act(async () => handle.current?.restoreInput("", [reference]));
		expect(handle.current?.isTextEmpty()).toBe(false);
		expect(flags).toEqual([false, true]);
		expect(host.textContent).toContain("RemoteABC");
		const remote = {
			...initial,
			hasDraft: true,
			fileReferences: [{ ...reference, deviceId: "local" }],
			revision: 2,
			sourceId: "another-editor",
		};
		await act(async () => handle.current?.handleDraftChanged(remote));
		expect(host.textContent).toContain("draft conflict");
		expect(handle.current?.getFileReferences()[0].deviceId).toBe("RemoteABC");
		const serverButton = [...host.querySelectorAll("button")].find(
			(button) => button.textContent === "use server",
		);
		await act(async () => serverButton?.click());
		expect(handle.current?.getFileReferences()[0].deviceId).toBe("local");
	});
	test("synced remote clear removes text and refs while a same-source echo preserves new local refs", async () => {
		getSpy.mockResolvedValue({
			...initial,
			hasDraft: true,
			text: "same",
			fileReferences: [reference],
		});
		await render();
		await act(async () =>
			handle.current?.handleDraftChanged({ ...initial, revision: 2, sourceId: "other" }),
		);
		expect(handle.current?.isTextEmpty()).toBe(true);
		expect(handle.current?.getFileReferences()).toEqual([]);
		await act(async () => handle.current?.restoreInput("same", [reference]));
		await act(async () => {
			await new Promise((resolve) => setTimeout(resolve, 820));
		});
		const sourceId = updateSpy.mock.calls.at(-1)?.[3];
		expect(sourceId).toBeDefined();
		await act(async () => handle.current?.setFileReferences([{ ...reference, id: "newer-local" }]));
		await act(async () =>
			handle.current?.handleDraftChanged({
				...initial,
				hasDraft: true,
				text: "same",
				fileReferences: [reference],
				revision: 4,
				sourceId: sourceId ?? null,
			}),
		);
		expect(handle.current?.getFileReferences()[0].id).toBe("newer-local");
	});
	test("same-editor CAS retry keeps the exact reference payload on the newer revision", async () => {
		updateSpy.mockImplementationOnce(async (_id, text, _revision, sourceId) => {
			throw new ApiError("revision conflict", 409, {
				current: { ...initial, text, revision: 2, sourceId, fileReferences: [] },
			});
		});
		await render();
		await act(async () => handle.current?.restoreInput("same", [reference]));
		await act(async () => {
			await new Promise((resolve) => setTimeout(resolve, 820));
		});
		expect(updateSpy).toHaveBeenCalledTimes(2);
		expect(updateSpy.mock.calls.map((call) => [call[2], call[4]])).toEqual([
			[1, [reference]],
			[2, [reference]],
		]);
		expect(handle.current?.getFileReferences()).toEqual([reference]);
		expect(host.textContent).not.toContain("draft conflict");
	});
});
