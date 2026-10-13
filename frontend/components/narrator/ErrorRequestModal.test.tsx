import { afterEach, beforeEach, describe, expect, mock, spyOn, test } from "bun:test";
import { MantineProvider } from "@mantine/core";
import type { RequestRecordingDetail } from "@shared/request-recording";
import i18next from "i18next";
import { parseHTML } from "linkedom";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { I18nextProvider } from "react-i18next";
import { ApiError, api } from "../../lib/api";
import en from "../../locales/en/narrator.json";
import zh from "../../locales/zh-CN/narrator.json";
import { ErrorRequestModal, type ErrorRequestTarget } from "./ErrorRequestModal";

const i18n = i18next.createInstance();
await i18n.init({ lng: "en", resources: { en: { narrator: en } }, react: { useSuspense: false } });
const detail: RequestRecordingDetail = {
	requestId: "recording-request-one",
	narratorId: "narrator-one",
	apiRequestId: "usage-one",
	provider: "provider-one",
	model: "model-one",
	startedAt: "2026-10-07T00:00:00Z",
	endedAt: "2026-10-07T00:00:01Z",
	status: "failed",
	error: "Recorded failure",
	partial: true,
	truncated: true,
	requestPreview: '{"input":"bounded request preview"}',
	responsePreview: "bounded response preview",
	previewTruncated: { request: true, response: true },
	apiRequest: {
		id: "usage-one",
		createdAt: "2026-10-07T00:00:00Z",
		durationMs: 1000,
		ttftMs: 0,
		errorMessage: "Usage failure",
	},
};
const target: ErrorRequestTarget = {
	requestId: detail.requestId,
	errorText: "Original failure",
	diagnostics: {
		schema: "narrafork.error-diagnostics.v1",
		apiRequestId: "usage-from-diagnostic",
		providerRequestId: "upstream-one",
		statusCode: 429,
		code: "rate_limit",
		provider: "diagnostic-provider",
		model: "diagnostic-model",
		source: "gateway",
		phase: "response",
		reason: "capacity",
		errorType: "overloaded",
		message: "Diagnostic failure",
		cause: "Channel failure",
		responseHeaders: { "retry-after": "10", "x-request-id": "upstream-one" },
		responseSnippet: "Diagnostic response body",
	},
};
let root: Root;
let fetchDetail: ReturnType<typeof spyOn<typeof api, "getRequestRecordingDetail">>;
let copied: ReturnType<typeof mock>;
let onDownload: ReturnType<typeof mock>;
let onClose: ReturnType<typeof mock>;
const globals = new Map<string, PropertyDescriptor | undefined>();

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
	fetchDetail = spyOn(api, "getRequestRecordingDetail").mockResolvedValue(detail);
	onDownload = mock(() => {});
	onClose = mock(() => {});
});

afterEach(async () => {
	await act(async () => root.unmount());
	fetchDetail.mockRestore();
	for (const [key, descriptor] of globals) {
		if (descriptor) Object.defineProperty(globalThis, key, descriptor);
		else Reflect.deleteProperty(globalThis, key);
	}
	globals.clear();
});

async function flush() {
	await act(async () => {
		for (let i = 0; i < 4; i++) await new Promise((resolve) => setTimeout(resolve, 0));
	});
}

async function render(
	selected: ErrorRequestTarget | null = target,
	options: {
		narratorId?: string;
		downloading?: boolean;
		downloadBlocked?: boolean;
		mounted?: boolean;
	} = {},
) {
	await act(async () =>
		root.render(
			<I18nextProvider i18n={i18n}>
				<MantineProvider env="test">
					{options.mounted !== false && (
						<ErrorRequestModal
							narratorId={options.narratorId ?? "narrator-one"}
							target={selected}
							onClose={onClose}
							onDownload={onDownload}
							downloading={options.downloading ?? false}
							downloadBlocked={options.downloadBlocked ?? false}
						/>
					)}
				</MantineProvider>
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

async function close() {
	const button = document.querySelector<HTMLButtonElement>(".mantine-Modal-close");
	expect(button).not.toBeNull();
	await act(async () => button?.click());
	await flush();
}

describe("ErrorRequestModal", () => {
	test("fetches only on demand with the exact recording ID, not the upstream or usage ID", async () => {
		await render(null);
		expect(fetchDetail).not.toHaveBeenCalled();
		await render();
		expect(fetchDetail).toHaveBeenCalledTimes(1);
		expect(fetchDetail).toHaveBeenCalledWith(
			"narrator-one",
			"recording-request-one",
			expect.any(AbortSignal),
		);
		await render({ ...target, errorText: "Updated error" });
		expect(fetchDetail).toHaveBeenCalledTimes(1);
		expect(document.body.textContent).toContain("Updated error");
	});

	test("displays bounded previews, partial/truncated/privacy warnings and diagnostics", async () => {
		await render();
		const text = document.body.textContent;
		for (const value of [
			detail.requestId,
			"usage-one",
			"upstream-one",
			detail.provider,
			detail.model,
			detail.startedAt,
			detail.endedAt,
			"1000 ms",
			"0 ms",
			detail.requestPreview,
			detail.responsePreview,
			"429",
			"rate_limit",
			"retry-after",
			"Diagnostic failure",
			"Original failure",
			"Recorded failure",
			"Usage failure",
			"Diagnostic response body",
			"Channel failure",
			"gateway",
			"response",
			"capacity",
			"overloaded",
			en.saveDump.partial,
			en.saveDump.truncated,
			en.saveDump.privacy,
			en.errorRequest.previewTruncated,
		]) {
			expect(value).toBeDefined();
			if (value !== undefined) expect(text).toContain(value);
		}
	});

	test("caps unexpected oversized preview payloads before rendering", async () => {
		fetchDetail.mockResolvedValue({
			...detail,
			requestPreview: `${"x".repeat(32 * 1024)}hidden-request-tail`,
			responsePreview: `${"y".repeat(32 * 1024)}hidden-response-tail`,
			previewTruncated: { request: false, response: false },
		});
		await render();
		expect(document.body.textContent).not.toContain("hidden-request-tail");
		expect(document.body.textContent).not.toContain("hidden-response-tail");
		expect(
			Array.from(document.querySelectorAll("pre")).every(
				(element) => element.textContent.length <= 32 * 1024,
			),
		).toBe(true);
		expect(document.body.textContent).toContain(en.errorRequest.previewTruncated);
	});

	test("404 keeps diagnostics and download available and can retry a later retained record", async () => {
		fetchDetail.mockRejectedValueOnce(new ApiError("Not found", 404));
		await render();
		expect(document.body.textContent).toContain(en.errorRequest.unavailable);
		expect(document.body.textContent).toContain("Diagnostic failure");
		expect(document.body.textContent).toContain("usage-from-diagnostic");
		expect(document.body.textContent).toContain("diagnostic-provider");
		expect(document.body.textContent).toContain("429");
		await click(en.errorRequest.download);
		expect(onDownload).toHaveBeenCalledTimes(1);
		await click(en.errorRequest.retry);
		expect(fetchDetail).toHaveBeenCalledTimes(2);
		expect(document.body.textContent).toContain(detail.requestPreview);
		expect(document.body.textContent).not.toContain(en.errorRequest.unavailable);
	});

	test("other failures display an actionable error and retry", async () => {
		fetchDetail.mockRejectedValueOnce(new ApiError("Detail service unavailable", 503));
		await render();
		expect(document.body.textContent).toContain(en.errorRequest.failed);
		expect(document.body.textContent).toContain("Detail service unavailable");
		expect(document.body.textContent).toContain("Diagnostic failure");
		await click(en.errorRequest.retry);
		expect(fetchDetail).toHaveBeenCalledTimes(2);
		expect(document.body.textContent).toContain(detail.responsePreview);
	});

	test("downloads through its callback only and reflects externally owned pending state", async () => {
		await render();
		await click(en.errorRequest.download);
		expect(onDownload).toHaveBeenCalledTimes(1);
		expect(fetchDetail).toHaveBeenCalledTimes(1);
		await render(target, { downloading: true });
		const button = Array.from(document.querySelectorAll("button")).find(
			(element) => element.textContent === en.errorRequest.download,
		);
		expect(button?.hasAttribute("data-loading")).toBe(true);
	});
	test("another request's download disables this modal without claiming this request is loading", async () => {
		await render(target, { downloadBlocked: true });
		const button = Array.from(document.querySelectorAll("button")).find(
			(element) => element.textContent === en.errorRequest.download,
		);
		expect(button?.disabled).toBe(true);
		expect(button?.hasAttribute("data-loading")).toBe(false);
		expect(document.body.textContent).toContain(en.errorRequest.otherDownload);
		await click(en.errorRequest.download);
		expect(onDownload).not.toHaveBeenCalled();
		await render();
		await click(en.errorRequest.download);
		expect(onDownload).toHaveBeenCalledTimes(1);
		expect(document.body.textContent).not.toContain(en.errorRequest.otherDownload);
	});

	test("copies the narrator, recording, usage and upstream IDs", async () => {
		await render();
		await click(en.errorRequest.copy);
		expect(copied).toHaveBeenCalledWith(
			"narratorId: narrator-one\nrequestId: recording-request-one\napiRequestId: usage-one\nproviderRequestId: upstream-one",
		);
		expect(document.body.textContent).toContain(en.errorRequest.copied);
	});

	test("shows a loader while pending and aborts immediately on close before parent rerender", async () => {
		const pending = Promise.withResolvers<RequestRecordingDetail>();
		fetchDetail.mockReturnValue(pending.promise);
		onClose.mockImplementation(() =>
			pending.resolve({ ...detail, responsePreview: "late-close-preview" }),
		);
		await render();
		expect(document.querySelector(`[aria-label="${en.errorRequest.loading}"]`)).not.toBeNull();
		const signal = fetchDetail.mock.calls[0]?.[2];
		await close();
		expect(onClose).toHaveBeenCalledTimes(1);
		expect(signal?.aborted).toBe(true);
		expect(document.body.textContent).not.toContain("late-close-preview");
	});

	test.each([
		false,
		true,
	])("closing aborts pending detail and reopening rejects stale data (unmount=%s)", async (unmount) => {
		const old = Promise.withResolvers<RequestRecordingDetail>();
		const latest = Promise.withResolvers<RequestRecordingDetail>();
		fetchDetail.mockReturnValueOnce(old.promise).mockReturnValueOnce(latest.promise);
		await render();
		const oldSignal = fetchDetail.mock.calls[0]?.[2];
		await render(null, { mounted: !unmount });
		expect(oldSignal?.aborted).toBe(true);
		await render();
		await act(async () => latest.resolve({ ...detail, responsePreview: "latest-preview" }));
		await flush();
		await act(async () => old.resolve({ ...detail, responsePreview: "old-preview" }));
		await flush();
		expect(document.body.textContent).toContain("latest-preview");
		expect(document.body.textContent).not.toContain("old-preview");
	});

	test.each([
		"resolve",
		"reject",
	])("switching requests aborts old detail and ignores stale %s", async (settle) => {
		const old = Promise.withResolvers<RequestRecordingDetail>();
		fetchDetail.mockReturnValueOnce(old.promise).mockResolvedValueOnce({
			...detail,
			requestId: "recording-request-two",
			responsePreview: "second-preview",
		});
		await render();
		const oldSignal = fetchDetail.mock.calls[0]?.[2];
		await render({ requestId: "recording-request-two", errorText: "Second error" });
		expect(oldSignal?.aborted).toBe(true);
		expect(fetchDetail.mock.calls[1]?.[1]).toBe("recording-request-two");
		await act(async () => {
			if (settle === "resolve") old.resolve({ ...detail, responsePreview: "old-preview" });
			else old.reject(new Error("Old detail failed"));
		});
		await flush();
		expect(document.body.textContent).toContain("second-preview");
		expect(document.body.textContent).not.toContain("old-preview");
		expect(document.body.textContent).not.toContain("Old detail failed");
		expect(document.body.textContent).not.toContain("Diagnostic failure");
	});

	test("switching narrators refetches even with the same request ID", async () => {
		const old = Promise.withResolvers<RequestRecordingDetail>();
		fetchDetail.mockReturnValueOnce(old.promise);
		await render();
		const oldSignal = fetchDetail.mock.calls[0]?.[2];
		await render(target, { narratorId: "narrator-two" });
		expect(oldSignal?.aborted).toBe(true);
		expect(fetchDetail.mock.calls[1]?.[0]).toBe("narrator-two");
		await act(async () => old.reject(new Error("Old narrator failure")));
		await flush();
		expect(document.body.textContent).not.toContain("Old narrator failure");
	});

	test("both locales have matching detail keys", () => {
		expect(Object.keys(en.errorRequest).sort()).toEqual(Object.keys(zh.errorRequest).sort());
	});
});
