import { afterEach, beforeEach, describe, expect, spyOn, test } from "bun:test";
import { MantineProvider } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import type { RequestRecordingDetail, SavedRecording } from "@shared/request-recording";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import i18next from "i18next";
import { parseHTML } from "linkedom";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { I18nextProvider } from "react-i18next";
import { ApiError, api } from "../../lib/api";
import * as downloads from "../../lib/file-download";
import en from "../../locales/en/narrator.json";
import { useErrorRequestDump } from "./useErrorRequestDump";

const i18n = i18next.createInstance();
await i18n.init({ lng: "en", resources: { en: { narrator: en } }, react: { useSuspense: false } });
const globals = new Map<string, PropertyDescriptor | undefined>();
let root: Root;
let qc: QueryClient;
let hook: ReturnType<typeof useErrorRequestDump>;
let get: ReturnType<typeof spyOn<typeof api, "getRequestRecordingDetail">>;
let save: ReturnType<typeof spyOn<typeof api, "saveSpecificRequestRecording">>;
let download: ReturnType<typeof spyOn<typeof api, "downloadRequestRecording">>;
let browserDownload: ReturnType<typeof spyOn<typeof downloads, "saveBlobAsFile">>;
let notify: ReturnType<typeof spyOn<typeof notifications, "show">>;
let invalidate: ReturnType<typeof spyOn<QueryClient, "invalidateQueries">>;
type Stage = "detail" | "save" | "download";
type Settlement = "resolve" | "reject";
const stages: Stage[] = ["detail", "save", "download"];

function recording(narratorId = "narrator-one", requestId = "request-one"): SavedRecording {
	return {
		id: `save-${narratorId}-${requestId}`,
		narratorId,
		createdAt: "2026-10-08T00:00:00Z",
		requestIds: [requestId],
		apiRequestIds: [],
		requestCount: 1,
		bytes: 32,
		partial: false,
		truncated: false,
	};
}

function detail(narratorId = "narrator-one", requestId = "request-one"): RequestRecordingDetail {
	return {
		requestId,
		narratorId,
		provider: "provider",
		model: "model",
		startedAt: "2026-10-08T00:00:00Z",
		partial: false,
		truncated: false,
		requestPreview: "request",
		responsePreview: "response",
		previewTruncated: { request: false, response: false },
	};
}

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
	for (const [key, value] of Object.entries({
		window,
		document: window.document,
		HTMLElement: window.HTMLElement,
		Element: window.Element,
		Node: window.Node,
		Event: window.Event,
		Document: window.Document,
		ShadowRoot: window.ShadowRoot,
		navigator: { clipboard: { writeText: async () => {} } },
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
	get = spyOn(api, "getRequestRecordingDetail").mockImplementation(async (narratorId, requestId) =>
		detail(narratorId, requestId),
	);
	save = spyOn(api, "saveSpecificRequestRecording").mockImplementation(
		async (narratorId, requestId) => recording(narratorId, requestId),
	);
	download = spyOn(api, "downloadRequestRecording").mockResolvedValue({
		blob: new Blob(["ZIP"]),
		fileName: "dump.zip",
	});
	browserDownload = spyOn(downloads, "saveBlobAsFile").mockImplementation(() => {});
	notify = spyOn(notifications, "show").mockReturnValue("notice");
	invalidate = spyOn(qc, "invalidateQueries");
});

afterEach(async () => {
	await act(async () => root.unmount());
	qc.clear();
	for (const spy of [get, save, download, browserDownload, notify, invalidate]) spy.mockRestore();
	for (const [key, descriptor] of globals) {
		if (descriptor) Object.defineProperty(globalThis, key, descriptor);
		else Reflect.deleteProperty(globalThis, key);
	}
	globals.clear();
});

function Probe({ narratorId }: { narratorId: string }) {
	hook = useErrorRequestDump(narratorId);
	return <output data-testid="busy">{hook.downloadingRequestId ?? "idle"}</output>;
}

async function flush() {
	await act(async () => {
		for (let i = 0; i < 4; i++) await new Promise((resolve) => setTimeout(resolve, 0));
	});
}

async function render(narratorId = "narrator-one", mounted = true) {
	await act(async () =>
		root.render(
			<I18nextProvider i18n={i18n}>
				<QueryClientProvider client={qc}>
					<MantineProvider env="test">
						{mounted && <Probe narratorId={narratorId} />}
					</MantineProvider>
				</QueryClientProvider>
			</I18nextProvider>,
		),
	);
	await flush();
}

function busy() {
	return document.querySelector('[data-testid="busy"]')?.textContent;
}

async function start(requestId = "request-one") {
	await act(async () => {
		void hook.download(requestId);
	});
	await flush();
}

function deferStage(stage: Stage) {
	if (stage === "detail") {
		const pending = Promise.withResolvers<RequestRecordingDetail>();
		get.mockReturnValueOnce(pending.promise);
		return (settlement: Settlement) => {
			if (settlement === "resolve") pending.resolve(detail());
			else pending.reject(new Error("Stale detail failure"));
		};
	}
	if (stage === "save") {
		const pending = Promise.withResolvers<SavedRecording>();
		save.mockReturnValueOnce(pending.promise);
		return (settlement: Settlement) => {
			if (settlement === "resolve") pending.resolve(recording());
			else pending.reject(new Error("Stale save failure"));
		};
	}
	const pending = Promise.withResolvers<{ blob: Blob; fileName: string }>();
	download.mockReturnValueOnce(pending.promise);
	return (settlement: Settlement) => {
		if (settlement === "resolve")
			pending.resolve({ blob: new Blob(["OLD ZIP"]), fileName: "old.zip" });
		else pending.reject(new Error("Stale download failure"));
	};
}

describe("useErrorRequestDump mounted lifecycle", () => {
	test("same-turn duplicate clicks only save and download once", async () => {
		const settle = deferStage("detail");
		await render();
		expect(get).not.toHaveBeenCalled();
		await act(async () => {
			void hook.download("request-one");
			void hook.download("request-one");
			void hook.download("request-other");
		});
		await flush();
		expect(get).toHaveBeenCalledTimes(1);
		expect(busy()).toBe("request-one");
		await act(async () => settle("resolve"));
		await flush();
		expect(save).toHaveBeenCalledTimes(1);
		expect(save).toHaveBeenCalledWith("narrator-one", "request-one", expect.any(AbortSignal));
		expect(download).toHaveBeenCalledTimes(1);
		expect(browserDownload).toHaveBeenCalledTimes(1);
		expect(browserDownload).toHaveBeenCalledWith(expect.any(Blob), "dump.zip");
		expect(invalidate).toHaveBeenCalledWith({ queryKey: ["request-recordings", "narrator-one"] });
		expect(busy()).toBe("idle");
		expect(notify).not.toHaveBeenCalled();
	});

	test.each(
		stages,
	)("cancel aborts immediately while %s is pending and ignores a late resolution", async (stage) => {
		const settle = deferStage(stage);
		await render();
		await start();
		const signal = get.mock.calls[0]?.[2];
		expect(signal).toBeInstanceOf(AbortSignal);
		expect(busy()).toBe("request-one");
		await act(async () => {
			hook.cancel();
			expect(signal?.aborted).toBe(true);
		});
		await flush();
		expect(busy()).toBe("idle");
		const saves = save.mock.calls.length;
		const downloads = download.mock.calls.length;
		await act(async () => settle("resolve"));
		await flush();
		expect(save).toHaveBeenCalledTimes(saves);
		expect(download).toHaveBeenCalledTimes(downloads);
		expect(browserDownload).not.toHaveBeenCalled();
		expect(notify).not.toHaveBeenCalled();
		expect(invalidate).not.toHaveBeenCalled();
	});

	for (const transition of ["switch", "unmount"] as const) {
		for (const settlement of ["resolve", "reject"] as const) {
			test.each(
				stages,
			)(`${transition} during %s prevents stale ${settlement} effects and preserves a new download`, async (stage) => {
				const settleOld = deferStage(stage);
				await render();
				await start();
				const oldSignal = get.mock.calls[0]?.[2];
				if (transition === "unmount") await render("narrator-one", false);
				await render("narrator-two");
				expect(oldSignal?.aborted).toBe(true);
				expect(busy()).toBe("idle");
				const latest = Promise.withResolvers<{ blob: Blob; fileName: string }>();
				download.mockReturnValueOnce(latest.promise);
				await start("request-two");
				expect(get).toHaveBeenLastCalledWith(
					"narrator-two",
					"request-two",
					expect.any(AbortSignal),
				);
				expect(save).toHaveBeenLastCalledWith(
					"narrator-two",
					"request-two",
					expect.any(AbortSignal),
				);
				expect(download).toHaveBeenLastCalledWith(
					"narrator-two",
					"save-narrator-two-request-two",
					expect.any(AbortSignal),
				);
				const latestSignal = get.mock.calls[1]?.[2];
				expect(busy()).toBe("request-two");
				const saves = save.mock.calls.length;
				const downloads = download.mock.calls.length;
				await act(async () => settleOld(settlement));
				await flush();
				expect(browserDownload).not.toHaveBeenCalled();
				expect(notify).not.toHaveBeenCalled();
				expect(invalidate).not.toHaveBeenCalled();
				expect(save).toHaveBeenCalledTimes(saves);
				expect(download).toHaveBeenCalledTimes(downloads);
				expect(latestSignal?.aborted).toBe(false);
				expect(busy()).toBe("request-two");
				await act(async () => latest.resolve({ blob: new Blob(["NEW ZIP"]), fileName: "new.zip" }));
				await flush();
				expect(browserDownload).toHaveBeenCalledTimes(1);
				expect(browserDownload).toHaveBeenCalledWith(expect.any(Blob), "new.zip");
				expect(invalidate).toHaveBeenCalledTimes(1);
				expect(invalidate).toHaveBeenCalledWith({
					queryKey: ["request-recordings", "narrator-two"],
				});
				expect(busy()).toBe("idle");
				expect(notify).not.toHaveBeenCalled();
			});
		}
	}

	test("cancel and restart in the same narrator does not let an old finally clear the new busy state", async () => {
		const settleOld = deferStage("download");
		await render();
		await start();
		const latest = Promise.withResolvers<{ blob: Blob; fileName: string }>();
		download.mockReturnValueOnce(latest.promise);
		await act(async () => {
			hook.cancel();
			void hook.download("request-two");
		});
		await flush();
		expect(busy()).toBe("request-two");
		await act(async () => settleOld("reject"));
		await flush();
		expect(busy()).toBe("request-two");
		expect(notify).not.toHaveBeenCalled();
		expect(browserDownload).not.toHaveBeenCalled();
		await act(async () => latest.resolve({ blob: new Blob(["NEW ZIP"]), fileName: "retry.zip" }));
		await flush();
		expect(browserDownload).toHaveBeenCalledTimes(1);
		expect(browserDownload).toHaveBeenCalledWith(expect.any(Blob), "retry.zip");
		expect(busy()).toBe("idle");
	});

	test("a retained callback from an old narrator cannot start a download in the new narrator", async () => {
		await render();
		const staleDownload = hook.download;
		await render("narrator-two");
		await act(async () => {
			void staleDownload("old-request");
		});
		await flush();
		expect(get).not.toHaveBeenCalled();
		expect(busy()).toBe("idle");
		await start("request-two");
		expect(get).toHaveBeenCalledTimes(1);
		expect(get).toHaveBeenCalledWith("narrator-two", "request-two", expect.any(AbortSignal));
		expect(browserDownload).toHaveBeenCalledTimes(1);
	});

	test.each([
		404, 503,
	])("an active %s failure notifies once and clears busy so a retry can run", async (status) => {
		get.mockRejectedValueOnce(new ApiError("Active request failed", status));
		await render();
		await start();
		expect(notify).toHaveBeenCalledTimes(1);
		expect(notify).toHaveBeenCalledWith({
			title: en.errorRequest.failed,
			message: status === 404 ? en.errorRequest.unavailable : "Active request failed",
			color: "red",
		});
		expect(browserDownload).not.toHaveBeenCalled();
		expect(busy()).toBe("idle");
		await start();
		expect(browserDownload).toHaveBeenCalledTimes(1);
		expect(notify).toHaveBeenCalledTimes(1);
		expect(busy()).toBe("idle");
	});
});
