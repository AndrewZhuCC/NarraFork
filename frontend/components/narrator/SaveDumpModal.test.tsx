import { afterEach, beforeEach, describe, expect, mock, spyOn, test } from "bun:test";
import { MantineProvider } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import type { SavedRecording } from "@shared/request-recording";
import { type InfiniteData, QueryClient, QueryClientProvider } from "@tanstack/react-query";
import i18next from "i18next";
import { parseHTML } from "linkedom";
import { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { I18nextProvider } from "react-i18next";
import { api } from "../../lib/api";
import * as downloads from "../../lib/file-download";
import en from "../../locales/en/narrator.json";
import { SaveDumpModal } from "./SaveDumpModal";

const i18n = i18next.createInstance();
await i18n.init({ lng: "en", resources: { en: { narrator: en } }, react: { useSuspense: false } });
const recording: SavedRecording = {
	id: "s1",
	narratorId: "n",
	createdAt: "2026-10-07T00:00:00Z",
	requestIds: ["request-one"],
	apiRequestIds: ["upstream-one"],
	requestCount: 1,
	bytes: 1024 * 1024,
	partial: true,
	truncated: true,
};
let root: Root;
let qc: QueryClient;
const globals = new Map<string, PropertyDescriptor | undefined>();
let list: ReturnType<typeof spyOn<typeof api, "listRequestRecordings">>;
let save: ReturnType<typeof spyOn<typeof api, "saveRequestRecording">>;
let remove: ReturnType<typeof spyOn<typeof api, "deleteRequestRecording">>;
let download: ReturnType<typeof spyOn<typeof api, "downloadRequestRecording">>;
let blobDownload: ReturnType<typeof spyOn<typeof downloads, "saveBlobAsFile">>;
let notify: ReturnType<typeof spyOn<typeof notifications, "show">>;
let copied: ReturnType<typeof mock>;
let onDeleted: ReturnType<typeof mock>;
let onSaved: ReturnType<typeof mock>;
let onClose: ReturnType<typeof mock>;
let updateSaved: (recording: SavedRecording) => void;

class TestObserver {
	observe() {}
	unobserve() {}
	disconnect() {}
	takeRecords() {
		return [];
	}
}

beforeEach(() => {
	const { window } = parseHTML("<!doctype html><html><head></head><body></body></html>");
	const matchMedia = () => ({
		matches: false,
		addEventListener() {},
		removeEventListener() {},
		addListener() {},
		removeListener() {},
	});
	Object.assign(window, {
		matchMedia,
		ResizeObserver: TestObserver,
		MutationObserver: TestObserver,
	});
	copied = mock(async () => {});
	for (const [key, value] of Object.entries({
		window,
		document: window.document,
		HTMLElement: window.HTMLElement,
		Element: window.Element,
		Node: window.Node,
		Event: window.Event,
		Document: window.Document,
		ShadowRoot: window.ShadowRoot,
		navigator: { clipboard: { writeText: copied } },
		getComputedStyle: () => ({ getPropertyValue: () => "" }),
		ResizeObserver: TestObserver,
		MutationObserver: TestObserver,
		matchMedia,
		requestAnimationFrame: (callback: FrameRequestCallback) => setTimeout(callback, 0),
		cancelAnimationFrame: (id: number) => clearTimeout(id),
		IS_REACT_ACT_ENVIRONMENT: true,
		localStorage: { getItem: () => null, setItem() {}, removeItem() {} },
	})) {
		globals.set(key, Object.getOwnPropertyDescriptor(globalThis, key));
		Object.defineProperty(globalThis, key, { value, writable: true, configurable: true });
	}
	root = createRoot(document.body.appendChild(document.createElement("div")));
	qc = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
	list = spyOn(api, "listRequestRecordings").mockResolvedValue({
		items: [recording],
		nextCursor: null,
	});
	save = spyOn(api, "saveRequestRecording").mockResolvedValue({ ...recording, id: "s2" });
	remove = spyOn(api, "deleteRequestRecording").mockResolvedValue({ ok: true });
	download = spyOn(api, "downloadRequestRecording").mockResolvedValue({
		blob: new Blob(["zip"]),
		fileName: "evidence.zip",
	});
	blobDownload = spyOn(downloads, "saveBlobAsFile").mockImplementation(() => {});
	notify = spyOn(notifications, "show").mockReturnValue("notice");
	onDeleted = mock(() => {});
	onSaved = mock(() => {});
	onClose = mock(() => {});
});
afterEach(async () => {
	await act(async () => root.unmount());
	qc.clear();
	for (const spy of [list, save, remove, download, blobDownload, notify]) spy.mockRestore();
	for (const [key, descriptor] of globals) {
		if (descriptor) Object.defineProperty(globalThis, key, descriptor);
		else Reflect.deleteProperty(globalThis, key);
	}
	globals.clear();
});
function Probe({ opened, narratorId = "n" }: { opened: boolean; narratorId?: string }) {
	const [saved, setSaved] = useState<SavedRecording | null>(recording);
	updateSaved = setSaved;
	return (
		<SaveDumpModal
			narratorId={narratorId}
			opened={opened}
			onClose={onClose}
			saved={saved}
			onSaved={(value) => {
				setSaved(value);
				onSaved(value);
			}}
			onDeleted={(id) => {
				setSaved((value) => (value?.id === id ? null : value));
				onDeleted(id);
			}}
		/>
	);
}
async function flush() {
	await act(async () => {
		for (let i = 0; i < 4; i++) await new Promise((resolve) => setTimeout(resolve, 0));
	});
}
async function render(opened = true, mounted = true, narratorId = "n") {
	await act(async () =>
		root.render(
			<I18nextProvider i18n={i18n}>
				<QueryClientProvider client={qc}>
					<MantineProvider env="test">
						{mounted && <Probe opened={opened} narratorId={narratorId} />}
					</MantineProvider>
				</QueryClientProvider>
			</I18nextProvider>,
		),
	);
	await flush();
}
async function click(text: string) {
	const button = Array.from(document.querySelectorAll("button")).find(
		(element) => element.textContent === text,
	);
	expect(button).toBeDefined();
	await act(async () => button?.click());
	await flush();
}

type RecordingPage = { items: SavedRecording[]; nextCursor: string | null };
function cachedPages(narratorId = "n") {
	return qc.getQueryData<InfiniteData<RecordingPage, string | undefined>>([
		"request-recordings",
		narratorId,
	]);
}
function longHistory() {
	const items = Array.from({ length: 250 }, (_, index) => ({
		...recording,
		id: `bundle-${String(index).padStart(4, "0")}`,
		requestIds: [`evidence-${String(index).padStart(4, "0")}-request`],
	}));
	const page = (cursor?: string): RecordingPage => {
		const index = cursor ? Number(cursor.slice("page-".length)) : 0;
		return {
			items: items.slice(index * 50, (index + 1) * 50),
			nextCursor: (index + 1) * 50 < items.length ? `page-${index + 1}` : null,
		};
	};
	list.mockImplementation(async (_id, cursor) => page(cursor));
	return { items, page };
}
async function readFivePages() {
	await render();
	for (let index = 0; index < 4; index++) await click(en.saveDump.more);
	expect(cachedPages()?.pages).toHaveLength(4);
	expect(cachedPages()?.pages.flatMap((page) => page.items)).toHaveLength(200);
	expect(cachedPages()?.pageParams).toEqual(["page-1", "page-2", "page-3", "page-4"]);
	expect(document.body.textContent).not.toContain("evidence-0000-request");
	expect(document.body.textContent).toContain("evidence-0249-request");
}
function expectLatest() {
	expect(cachedPages()?.pageParams).toEqual([undefined]);
	expect(cachedPages()?.pages).toHaveLength(1);
	expect(cachedPages()?.pages[0].items).toHaveLength(50);
	expect(document.body.textContent).toContain("evidence-0000-request");
	expect(document.body.textContent).not.toContain("evidence-0249-request");
	expect(list.mock.calls.at(-1)?.[1]).toBeUndefined();
}

describe("SaveDumpModal bounded history", () => {
	test("five pages retain at most 200 entries and Back to latest resets the evicted first cursor", async () => {
		longHistory();
		await readFivePages();
		await click(en.saveDump.latest);
		expectLatest();
		await click(en.saveDump.more);
		expect(cachedPages()?.pageParams).toEqual([undefined, "page-1"]);
	});
	test.each([
		false,
		true,
	])("reopening starts at latest after five pages even with retained cache (unmount=%s)", async (unmount) => {
		longHistory();
		qc.setDefaultOptions({ queries: { retry: false, gcTime: Infinity } });
		await readFivePages();
		await render(false, !unmount);
		const calls = list.mock.calls.length;
		await render();
		expect(list.mock.calls.slice(calls).map((call) => call[1])).toEqual([undefined]);
		expectLatest();
	});
	test("saving on the fifth page fetches the newest first page, not the retained first cursor", async () => {
		const history = longHistory();
		await readFivePages();
		const fresh = { ...recording, id: "new-save", requestIds: ["new-save-evidence"] };
		save.mockImplementation(async () => {
			history.items.unshift(fresh);
			return fresh;
		});
		const calls = list.mock.calls.length;
		await click(en.saveDump.save);
		expect(onSaved).toHaveBeenCalledWith(fresh);
		expect(list.mock.calls.slice(calls).map((call) => call[1])).toEqual([undefined]);
		expectLatest();
		expect(document.body.textContent).toContain("new-save-evidence");
	});
	test("an externally published save resets fifth-page history and invalidates an older modal save", async () => {
		const history = longHistory();
		await readFivePages();
		const pending = Promise.withResolvers<SavedRecording>();
		save.mockReturnValue(pending.promise);
		await click(en.saveDump.save);
		const fresh = { ...recording, id: "command-save", requestIds: ["command-evidence"] };
		history.items.unshift(fresh);
		await act(async () => updateSaved(fresh));
		await flush();
		expectLatest();
		await click(en.saveDump.more);
		const calls = list.mock.calls.length;
		await act(async () => pending.resolve({ ...recording, id: "old-modal-save" }));
		await flush();
		expect(onSaved).not.toHaveBeenCalled();
		expect(list).toHaveBeenCalledTimes(calls);
		expect(cachedPages()?.pageParams).toEqual([undefined, "page-1"]);
		expect(document.body.textContent).toContain("command-evidence");
		expect(document.body.textContent).not.toContain("old-modal-save");
	});
	test.each([
		"resolve",
		"reject",
	])("reset cancels an in-flight older page and ignores its late %s without reverting", async (outcome) => {
		const history = longHistory();
		await render();
		for (let index = 0; index < 3; index++) await click(en.saveDump.more);
		const pending = Promise.withResolvers<RecordingPage>();
		list.mockImplementation((_id, cursor) =>
			cursor === "page-4" ? pending.promise : Promise.resolve(history.page(cursor)),
		);
		await click(en.saveDump.more);
		const oldSignal = list.mock.calls.at(-1)?.[2];
		await click(en.saveDump.latest);
		expect(oldSignal?.aborted).toBeTrue();
		expectLatest();
		await act(async () =>
			outcome === "resolve"
				? pending.resolve(history.page("page-4"))
				: pending.reject(new Error("old page failure")),
		);
		await flush();
		expectLatest();
		expect(notify).not.toHaveBeenCalled();
		expect(document.body.textContent).not.toContain("old page failure");
	});
	test("closing during the latest reset cannot let its old completion overwrite a reopened query", async () => {
		const history = longHistory();
		await readFivePages();
		const old = Promise.withResolvers<RecordingPage>();
		list.mockReturnValueOnce(old.promise);
		await click(en.saveDump.latest);
		const oldSignal = list.mock.calls.at(-1)?.[2];
		await render(false);
		expect(oldSignal?.aborted).toBeTrue();
		history.items.unshift({ ...recording, id: "reopened", requestIds: ["reopened-evidence"] });
		await render();
		await act(async () =>
			old.resolve({
				items: [{ ...recording, id: "obsolete", requestIds: ["obsolete-evidence"] }],
				nextCursor: null,
			}),
		);
		await flush();
		expectLatest();
		expect(document.body.textContent).toContain("reopened-evidence");
		expect(document.body.textContent).not.toContain("obsolete-evidence");
	});
	test.each([
		"resolve",
		"reject",
	])("changing narrator cancels an old pending list and cannot show its late %s in the new owner", async (outcome) => {
		const pending = Promise.withResolvers<RecordingPage>();
		const next = {
			...recording,
			narratorId: "next",
			id: "next-list",
			requestIds: ["next-list-evidence"],
		};
		list.mockImplementation((owner) =>
			owner === "n" ? pending.promise : Promise.resolve({ items: [next], nextCursor: null }),
		);
		await render();
		const oldSignal = list.mock.calls[0]?.[2];
		await render(true, true, "next");
		expect(oldSignal?.aborted).toBeTrue();
		expect(document.body.textContent).toContain("next-list-evidence");
		await act(async () => {
			if (outcome === "resolve") pending.resolve({ items: [recording], nextCursor: "old-cursor" });
			else pending.reject(new Error("old-list-error"));
		});
		await flush();
		expect(cachedPages("next")?.pageParams).toEqual([undefined]);
		expect(cachedPages("next")?.pages[0].items).toEqual([next]);
		expect(document.body.textContent).not.toContain("request-one");
		expect(document.body.textContent).not.toContain("old-list-error");
		expect(notify).not.toHaveBeenCalled();
	});
	test.each([
		"resolve",
		"reject",
	])("changing narrator while open cancels old download and ignores old save %s", async (outcome) => {
		const oldSave = Promise.withResolvers<SavedRecording>();
		const oldDownload = Promise.withResolvers<{ blob: Blob; fileName: string }>();
		save.mockReturnValueOnce(oldSave.promise);
		download.mockReturnValueOnce(oldDownload.promise);
		await render();
		await click(en.saveDump.save);
		await click(en.saveDump.download);
		const oldSignal = download.mock.calls[0]?.[2];
		const next = {
			...recording,
			narratorId: "next",
			id: "next-save",
			requestIds: ["next-evidence"],
		};
		list.mockResolvedValue({ items: [next], nextCursor: null });
		save.mockResolvedValue(next);
		await render(true, true, "next");
		expect(oldSignal?.aborted).toBeTrue();
		expect(document.body.textContent).not.toContain("request-one");
		await click(en.saveDump.save);
		expect(onSaved).toHaveBeenCalledTimes(1);
		expect(onSaved).toHaveBeenCalledWith(next);
		const calls = list.mock.calls.length;
		await act(async () => {
			if (outcome === "resolve") oldSave.resolve({ ...recording, id: "old-owner-save" });
			else oldSave.reject(new Error("old owner failure"));
			oldDownload.resolve({ blob: new Blob(["old"]), fileName: "old-owner.zip" });
		});
		await flush();
		expect(onSaved).toHaveBeenCalledTimes(1);
		expect(list).toHaveBeenCalledTimes(calls);
		expect(blobDownload).not.toHaveBeenCalled();
		expect(notify).not.toHaveBeenCalled();
		expect(document.body.textContent).toContain("next-evidence");
		expect(document.body.textContent).not.toContain("old-owner-save");
	});
});

describe("SaveDumpModal", () => {
	test("only loads while open and displays recording IDs and partial/truncated warnings", async () => {
		await render(false);
		expect(list).not.toHaveBeenCalled();
		await render();
		expect(list).toHaveBeenCalledTimes(1);
		expect(document.body.textContent).toContain("request-one");
		expect(document.body.textContent).toContain("upstream-one");
		expect(document.body.textContent).toContain(en.saveDump.partial);
		expect(document.body.textContent).toContain(en.saveDump.truncated);
		expect(document.querySelectorAll("button").length).toBeGreaterThan(0);
	});
	test("copies both ID sets and downloads ZIP through authenticated API helper", async () => {
		await render();
		await click(en.saveDump.copy);
		expect(copied).toHaveBeenCalledWith(
			"saveId: s1\nnarratorId: n\nRequest IDs:\nrequest-one\nUsage record IDs:\nupstream-one",
		);
		await click(en.saveDump.download);
		expect(download).toHaveBeenCalledWith("n", "s1", expect.any(AbortSignal));
		expect(blobDownload).toHaveBeenCalledWith(expect.any(Blob), "evidence.zip");
	});
	test("requires delete confirmation, then removes the saved item", async () => {
		await render();
		await click(en.saveDump.delete);
		expect(remove).not.toHaveBeenCalled();
		list.mockResolvedValue({ items: [], nextCursor: null });
		await click(en.saveDump.confirm);
		expect(remove).toHaveBeenCalledWith("n", "s1");
		expect(onDeleted).toHaveBeenCalledWith("s1");
		expect(document.body.textContent).not.toContain("request-one");
	});
	test.each([
		false,
		true,
	])("late save after close (unmount=%s) updates cache without delivering a UI result", async (unmount) => {
		const pending = Promise.withResolvers<SavedRecording>();
		save.mockReturnValue(pending.promise);
		const invalidate = spyOn(qc, "invalidateQueries");
		try {
			await render();
			await click(en.saveDump.save);
			await render(false, !unmount);
			await act(async () => pending.resolve({ ...recording, id: "late" }));
			await flush();
			expect(onSaved).not.toHaveBeenCalled();
			expect(notify).not.toHaveBeenCalled();
			expect(invalidate).toHaveBeenCalledWith({
				queryKey: ["request-recordings", "n"],
				refetchType: "none",
			});
			expect(document.body.textContent).not.toContain("late");
		} finally {
			invalidate.mockRestore();
		}
	});
	test.each([
		false,
		true,
	])("old save cannot replace a new result after reopening (unmount=%s)", async (unmount) => {
		const oldSave = Promise.withResolvers<SavedRecording>();
		const newSave = Promise.withResolvers<SavedRecording>();
		save.mockReturnValueOnce(oldSave.promise).mockReturnValueOnce(newSave.promise);
		await render();
		await click(en.saveDump.save);
		await render(false, !unmount);
		await render();
		await click(en.saveDump.save);
		expect(save).toHaveBeenCalledTimes(2);
		await act(async () => newSave.resolve({ ...recording, id: "new-result" }));
		await flush();
		expect(onSaved).toHaveBeenCalledTimes(1);
		expect(document.body.textContent).toContain("new-result");
		await act(async () => oldSave.resolve({ ...recording, id: "old-result" }));
		await flush();
		expect(onSaved).toHaveBeenCalledTimes(1);
		expect(document.body.textContent).toContain("new-result");
		expect(document.body.textContent).not.toContain("old-result");
	});
	test("close button invalidates a save even before the parent commits closed state", async () => {
		const pending = Promise.withResolvers<SavedRecording>();
		save.mockReturnValue(pending.promise);
		onClose.mockImplementation(() => pending.resolve({ ...recording, id: "close-result" }));
		await render();
		await click(en.saveDump.save);
		const closeButton = document.querySelector<HTMLButtonElement>(".mantine-Modal-close");
		expect(closeButton).not.toBeNull();
		await act(async () => closeButton?.click());
		await flush();
		expect(onClose).toHaveBeenCalledTimes(1);
		expect(onSaved).not.toHaveBeenCalled();
	});
	test("a newer externally published result wins over a pending in-modal save", async () => {
		const pending = Promise.withResolvers<SavedRecording>();
		save.mockReturnValue(pending.promise);
		await render();
		await click(en.saveDump.save);
		await act(async () => updateSaved({ ...recording, id: "command-result" }));
		await act(async () => pending.resolve({ ...recording, id: "older-modal-result" }));
		await flush();
		expect(onSaved).not.toHaveBeenCalled();
		expect(document.body.textContent).toContain("command-result");
		expect(document.body.textContent).not.toContain("older-modal-result");
	});
	test("an aborted old download cannot deliver a file or clear a newer download", async () => {
		const oldDownload = Promise.withResolvers<{ blob: Blob; fileName: string }>();
		const newDownload = Promise.withResolvers<{ blob: Blob; fileName: string }>();
		download.mockReturnValueOnce(oldDownload.promise).mockReturnValueOnce(newDownload.promise);
		await render();
		await click(en.saveDump.download);
		const oldSignal = download.mock.calls[0]?.[2];
		await render(false);
		expect(oldSignal?.aborted).toBe(true);
		await render();
		await click(en.saveDump.download);
		expect(download).toHaveBeenCalledTimes(2);
		await act(async () => oldDownload.resolve({ blob: new Blob(["old"]), fileName: "old.zip" }));
		await flush();
		expect(blobDownload).not.toHaveBeenCalled();
		await act(async () => newDownload.resolve({ blob: new Blob(["new"]), fileName: "new.zip" }));
		await flush();
		expect(blobDownload).toHaveBeenCalledTimes(1);
		expect(blobDownload).toHaveBeenCalledWith(expect.any(Blob), "new.zip");
	});
	test("late deletion reconciles its ID without clearing a newer saved result", async () => {
		const pending = Promise.withResolvers<{ ok: true }>();
		remove.mockReturnValue(pending.promise);
		await render();
		await click(en.saveDump.delete);
		await click(en.saveDump.confirm);
		await render(false);
		await render();
		await act(async () => updateSaved({ ...recording, id: "new-save" }));
		await act(async () => pending.resolve({ ok: true }));
		await flush();
		expect(onDeleted).toHaveBeenCalledWith("s1");
		expect(document.body.textContent).toContain("new-save");
		expect(notify).not.toHaveBeenCalled();
	});
	test("late save failure after closing does not display an unrelated toast", async () => {
		const pending = Promise.withResolvers<SavedRecording>();
		save.mockReturnValue(pending.promise);
		await render();
		await click(en.saveDump.save);
		await render(false, false);
		await act(async () => pending.reject(new Error("old failure")));
		await flush();
		expect(onSaved).not.toHaveBeenCalled();
		expect(notify).not.toHaveBeenCalled();
	});
	test("saves immediately from the modal and loads explicit next cursor pages", async () => {
		list.mockImplementation(async (_id, cursor) =>
			cursor
				? { items: [{ ...recording, id: "old", requestIds: ["older-request"] }], nextCursor: null }
				: { items: [recording], nextCursor: "older-page" },
		);
		await render();
		await click(en.saveDump.more);
		expect(list.mock.calls[1]?.[1]).toBe("older-page");
		expect(document.body.textContent).toContain("older-request");
		await click(en.saveDump.save);
		expect(save).toHaveBeenCalledWith("n", expect.any(AbortSignal));
		expect(onSaved).toHaveBeenCalledWith(expect.objectContaining({ id: "s2" }));
	});
});
