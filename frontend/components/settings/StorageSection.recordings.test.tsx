import { afterAll, afterEach, beforeEach, expect, mock, spyOn, test } from "bun:test";
import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createInstance } from "i18next";
import { parseHTML } from "linkedom";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { I18nextProvider } from "react-i18next";
import { api, type StorageScanResult } from "../../lib/api";
import en from "../../locales/en/settings.json";
import zh from "../../locales/zh-CN/settings.json";

mock.module("../../hooks/usePlatform", () => ({
	useDatabaseCapability: () => ({ supported: false }),
	useStorageCapability: () => ({
		scanSupported: false,
		cachedSupported: true,
		vacuumSupported: false,
		healthState: "ready",
		healthReady: true,
		healthFetching: false,
		refetchHealth: async () => {},
	}),
	useStorageCleanupOperationCapabilities: () =>
		Object.fromEntries(
			["uploads", "chatAttachments", "shares", "worktrees", "containers"].map((key) => [
				key,
				{ supported: true },
			]),
		),
	useStorageDatabaseCleanupCapabilities: () =>
		Object.fromEntries(
			["archivedSessions", "staleSessions", "apiRequestDumps", "toolCallPayloads"].map((key) => [
				key,
				{ supported: false },
			]),
		),
	useStorageDatabasePreviewCapability: () => ({ supported: false }),
}));
mock.module("../common/confirm-dialog-context", () => ({
	useConfirmDialog: () => async () => true,
}));
mock.module("./WorkspaceBarriersCard", () => ({ WorkspaceBarriersCard: () => null }));
const { StorageSection } = await import("./StorageSection");

const result: StorageScanResult = {
	totalBytes: 6144,
	scannedAt: 1_760_000_000_000,
	categories: [
		{ key: "requestRecordings", sizeBytes: 1024, details: { fileCount: 3 } },
		{ key: "savedRequestRecordings", sizeBytes: 2048, details: { fileCount: 7 } },
		{ key: "requestDumps", sizeBytes: 3072, details: { fileCount: 11 } },
	],
};
const globals = new Map<string, PropertyDescriptor | undefined>();
let root: Root;
let client: QueryClient;
let cached: ReturnType<typeof spyOn<typeof api, "getCachedStorage">>;
let status: ReturnType<typeof spyOn<typeof api, "getStorageScanStatus">>;
let cleanup: ReturnType<typeof spyOn<typeof api, "cleanupStorage">>;

class TestObserver {
	observe() {}
	unobserve() {}
	disconnect() {}
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
	Object.assign(window, { matchMedia });
	for (const [key, value] of Object.entries({
		window,
		document: window.document,
		navigator: window.navigator,
		HTMLElement: window.HTMLElement,
		Element: window.Element,
		Node: window.Node,
		IS_REACT_ACT_ENVIRONMENT: true,
		ResizeObserver: TestObserver,
		requestAnimationFrame: (callback: FrameRequestCallback) => setTimeout(callback, 0),
		cancelAnimationFrame: (id: number) => clearTimeout(id),
		matchMedia,
		localStorage: { getItem: () => null },
	})) {
		globals.set(key, Object.getOwnPropertyDescriptor(globalThis, key));
		Object.defineProperty(globalThis, key, { configurable: true, writable: true, value });
	}
	root = createRoot(document.body.appendChild(document.createElement("div")));
	client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
	client.setQueryData(["settings"], {});
	cached = spyOn(api, "getCachedStorage").mockResolvedValue({ cached: true, data: result });
	status = spyOn(api, "getStorageScanStatus").mockResolvedValue({
		state: {
			status: "idle",
			progressMessage: null,
			progressDetail: null,
			categories: [],
			result: null,
			error: null,
			startedAt: null,
			finishedAt: null,
		},
	});
	cleanup = spyOn(api, "cleanupStorage");
});
afterEach(async () => {
	await act(async () => root.unmount());
	client.clear();
	for (const spy of [cached, status, cleanup]) spy.mockRestore();
	for (const [key, descriptor] of globals) {
		if (descriptor) Object.defineProperty(globalThis, key, descriptor);
		else Reflect.deleteProperty(globalThis, key);
	}
	globals.clear();
});
afterAll(() => mock.restore());

test.each([
	"en",
	"zh-CN",
])("actual storage cards show service recording categories and never expose generic cleanup in %s", async (language) => {
	const labels = language === "en" ? en : zh;
	const i18n = createInstance();
	await i18n.init({
		lng: language,
		resources: { en: { settings: en }, "zh-CN": { settings: zh } },
		react: { useSuspense: false },
	});
	await act(async () => {
		root.render(
			<I18nextProvider i18n={i18n}>
				<QueryClientProvider client={client}>
					<MantineProvider env="test">
						<StorageSection />
					</MantineProvider>
				</QueryClientProvider>
			</I18nextProvider>,
		);
		for (let i = 0; i < 4; i++) await new Promise((resolve) => setTimeout(resolve, 0));
	});
	expect(cached).toHaveBeenCalledTimes(1);
	expect(document.querySelectorAll("[data-storage-category]")).toHaveLength(3);
	const checks = [
		[
			"requestRecordings",
			labels.storageRequestRecordings,
			labels.storageRequestRecordingsDesc,
			"1.0 KB",
			3,
		],
		[
			"savedRequestRecordings",
			labels.storageSavedRequestRecordings,
			labels.storageSavedRequestRecordingsDesc,
			"2.0 KB",
			7,
		],
		["requestDumps", labels.storageRequestDumps, labels.storageRequestDumpsDesc, "3.0 KB", 11],
	] as const;
	for (const [key, label, description, size, count] of checks) {
		const card = document.querySelector(`[data-storage-category="${key}"]`);
		expect(card).not.toBeNull();
		expect(card?.textContent).toContain(label);
		expect(card?.textContent).toContain(description);
		expect(card?.textContent).toContain(size);
		expect(card?.textContent).toContain(
			i18n.t("storageRequestDumpFileCount", { ns: "settings", count }),
		);
		expect(card?.querySelector("svg")).not.toBeNull();
		expect(card?.querySelector("button")).toBeNull();
	}
	expect(document.querySelector('[data-storage-category="savedRequestDumps"]')).toBeNull();
	expect(document.body.textContent).not.toContain("storageSavedRequestRecordings");
	expect(cleanup).not.toHaveBeenCalled();
});
