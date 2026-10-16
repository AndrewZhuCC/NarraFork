import { expect } from "bun:test";
import { readdir } from "node:fs/promises";
import type { Page } from "puppeteer-core";
import type {} from "../../scripts/smoke-editor-cache-fixture";
import type { EditorDocumentService } from "../services/editor-document-service";

export async function editorCacheBrowserAcceptance(
	page: Page,
	documents: EditorDocumentService,
	transferRoot: string,
	requests: () => {
		documentCreates: number;
		contentResponses: number;
		recoveryScopes: number;
		metadataRequests: number;
	},
) {
	const state = () => page.evaluate(() => window.__editorCache.snapshot());
	const wait = (predicate: Parameters<Page["waitForFunction"]>[0]) =>
		page.waitForFunction(predicate, { timeout: 15_000, polling: 50 });
	const docCount = () => (documents as unknown as { sessions: Map<string, unknown> }).sessions.size;
	const files = async (directory: string, depth = 0): Promise<number> => {
		if (depth > 4) throw new Error("Fixture transfer tree too deep");
		let total = 0;
		const entries = await readdir(directory, { withFileTypes: true }).catch((error) => {
			if ((error as NodeJS.ErrnoException).code === "ENOENT") return [];
			throw error;
		});
		if (entries.length > 256) throw new Error("Fixture transfer tree too large");
		for (const entry of entries)
			total += entry.isDirectory() ? await files(`${directory}/${entry.name}`, depth + 1) : 1;
		return total;
	};
	await wait(
		() =>
			window.__editorCache?.snapshot().views === 2 &&
			window.__editorCache.snapshot().registry.loads === 0 &&
			window.__editorCache.snapshot().drafts.metadataReads === 2,
	);
	const initial = await state();
	expect(initial.tabs).toBe(1000);
	expect(initial.registry.tabs).toBe(1000);
	expect(initial.registry.workingCopies).toBe(1);
	expect(initial.monacoModels).toBe(1);
	expect(initial.registry.docs).toBe(1);
	expect(docCount()).toBe(1);
	expect(requests()).toEqual({
		documentCreates: 2,
		contentResponses: 1,
		recoveryScopes: 2, // one fresh owner proof per visible host; none for 998 hidden tabs
		metadataRequests: 2,
	});
	expect(initial.drafts.metadataReads).toBe(2);
	expect(initial.drafts.bodyReads).toBe(0);
	expect(initial.panes.filter((pane) => pane.visible)).toHaveLength(2);
	expect(initial.panes.some((pane) => pane.visible && !pane.active)).toBe(true);
	const fdBaseline = (await readdir("/proc/self/fd")).length;
	const transferBaseline = await files(transferRoot);
	await page.evaluate(() => {
		window.__editorCache.position("file-0", 3, 80);
		window.__editorCache.position("file-1", 45, 700);
		window.__editorCache.edit("file-0", "/* HIDDEN_DIRTY */");
	});
	const independent = await state();
	const second = independent.panes.find((pane) => pane.id === "file-1");
	expect(second?.position?.lineNumber).toBe(45);
	expect(second?.scroll).toBeGreaterThan(0);
	// One side hides through real group selection; the unfocused split still owns a view.
	await page.evaluate(() => window.__editorCache.show("blank-left"));
	await wait(() => window.__editorCache.snapshot().views === 1);
	const oneSide = await state();
	expect(oneSide.monacoModels).toBe(1);
	expect(oneSide.panes[0]?.id).toBe("file-1");
	expect(oneSide.panes[0]?.firstLine).toContain("HIDDEN_DIRTY");
	// Rapid group hide/show must cancel delayed teardown (the real view stays allocated).
	await page.evaluate(async () => {
		window.__editorCache.show("blank-right");
		await new Promise<void>((resolve) => setTimeout(resolve, 250));
		window.__editorCache.show("file-1");
	});
	await page.evaluate(() => new Promise<void>((resolve) => setTimeout(resolve, 1150)));
	expect((await state()).views).toBe(1);
	expect((await state()).panes[0]?.lifetime).toBe(oneSide.panes[0]?.lifetime);
	await page.evaluate(() => window.__editorCache.show("blank-right"));
	await wait(() => window.__editorCache.snapshot().views === 0);
	const hidden = await state();
	expect(hidden.monacoModels).toBe(1);
	expect(hidden.registry.pinReasons.dirty).toBe(1);
	expect(hidden.registry.pinnedBytes).toBeGreaterThan(0);
	expect(
		await page.evaluate(
			() => !window.dispatchEvent(new Event("beforeunload", { cancelable: true })),
		),
	).toBe(true);
	expect(hidden.workers.active).toHaveLength(0);
	expect(hidden.workers.pool.slots.filter((slot) => slot.docId)).toHaveLength(0);
	expect(docCount()).toBe(1);
	await page.evaluate(() => window.__editorCache.show("file-1"));
	await wait(() => window.__editorCache.snapshot().views === 1);
	const restored = await state();
	expect(restored.panes[0]?.position ?? null).toEqual(second?.position ?? null);
	expect(restored.panes[0]?.scroll).toBeCloseTo(second?.scroll ?? 0, 0);
	expect(restored.panes[0]?.firstLine).toContain("HIDDEN_DIRTY");
	expect(restored.panes[0]?.lifetime).not.toBe(second?.lifetime);
	expect(requests()).toEqual({
		documentCreates: 2,
		contentResponses: 1,
		recoveryScopes: 4, // rapid reveal and later reattach each reauthorize discovery
		metadataRequests: 2,
	});
	await page.evaluate(() => window.__editorCache.undo("file-1"));
	await wait(() => !window.__editorCache.snapshot().registry.pinReasons.dirty);
	expect((await state()).panes[0]?.firstLine).not.toContain("HIDDEN_DIRTY");
	await page.evaluate(() => window.__editorCache.redo("file-1"));
	await wait(() => !!window.__editorCache.snapshot().registry.pinReasons.dirty);
	// Workspace hidden is independent of dock focus/selected tabs.
	await page.evaluate(() => window.__editorCache.workspaceVisible(false));
	await wait(() => window.__editorCache.snapshot().views === 0);
	expect((await state()).registry.pinReasons.dirty).toBe(1);
	await page.evaluate(() => window.__editorCache.workspaceVisible(true));
	await wait(() => window.__editorCache.snapshot().views === 1);
	// Browser-window visibility is not panel focus. Exercise actual visibilitychange.
	const otherPage = await page.browser().newPage();
	try {
		await otherPage.bringToFront();
		await wait(
			() => document.visibilityState === "hidden" && window.__editorCache.snapshot().views === 0,
		);
		expect((await state()).registry.pinReasons.dirty).toBe(1);
		await page.bringToFront();
		await wait(
			() => document.visibilityState === "visible" && window.__editorCache.snapshot().views === 1,
		);
	} finally {
		await otherPage.close();
	}
	// Populate a real search worker mirror, then verify hidden teardown releases it.
	await page.click('[data-cache-pane="file-1"] button[aria-label="Find / replace"]');
	await page.waitForSelector('[data-cache-pane="file-1"] input[name="search"]');
	await page.type('[data-cache-pane="file-1"] input[name="search"]', "actual");
	await wait(() =>
		window.__editorCache.snapshot().workers.pool.slots.some((slot) => !!slot.docId && !slot.busy),
	);
	await page.evaluate(() => window.__editorCache.show("blank-right"));
	await wait(
		() =>
			window.__editorCache.snapshot().views === 0 &&
			window.__editorCache.snapshot().workers.pool.slots.every((slot) => !slot.docId),
	);
	expect(await page.$("[data-editor-search-panel]")).toBeNull();
	for (let index = 2; index <= 14; index++) {
		await page.evaluate((id) => window.__editorCache.show(id), `file-${index}`);
		await page.waitForFunction(
			(id) =>
				window.__editorCache
					.snapshot()
					.panes.some(
						(pane) => pane.id === id && pane.firstLine?.startsWith(`cache file ${id.slice(5)}`),
					),
			{ timeout: 10_000 },
			`file-${index}`,
		);
	}
	await page.evaluate(() => {
		window.__editorCache.show("blank-left");
		window.__editorCache.show("blank-right");
	});
	await wait(
		() =>
			window.__editorCache.snapshot().views === 0 &&
			window.__editorCache.snapshot().monacoModels <= 8,
	);
	// Release requests are async; observe the actual service rather than assuming client counters.
	for (let attempt = 0; docCount() > 8 && attempt < 50; attempt++)
		await new Promise((resolve) => setTimeout(resolve, 10));
	const cached = await state();
	expect(cached.tabs).toBe(1000);
	expect(cached.monacoModels).toBe(8);
	expect(cached.registry.workingCopies).toBe(8);
	expect(cached.registry.pinReasons).toEqual({ dirty: 1 });
	expect(docCount()).toBe(8);
	expect(requests()).toEqual({
		documentCreates: 15,
		contentResponses: 14,
		recoveryScopes: 19,
		metadataRequests: 15,
	});
	expect(cached.workers.active).toHaveLength(0);
	expect(cached.workers.pool.slots.filter((slot) => slot.docId)).toHaveLength(0);
	const fdCached = (await readdir("/proc/self/fd")).length;
	expect(fdCached).toBeLessThanOrEqual(fdBaseline + 8);
	const transferCached = await files(transferRoot);
	// Cached immutable v1 source snapshots are released together with the server documents.
	expect(transferCached).toBeLessThanOrEqual(transferBaseline + 16);
	const cachedRequests = requests();
	// Keep split orientation stable when the search toolbar consumes height, so
	// this regression isolates navigation rather than a legitimate preview reflow.
	await page.setViewport({ width: 1800, height: 700 });
	await page.evaluate(() => window.__editorCache.show("file-16"));
	await wait(() =>
		window.__editorCache
			.snapshot()
			.panes.some((pane) => pane.id === "file-16" && pane.firstLine === "# Real preview fixture"),
	);
	await page.evaluate(() => {
		window.__editorCache.position("file-16", 20, 100);
		window.__editorCache.mode("file-16", "preview");
	});
	await wait(
		() =>
			window.__editorCache.snapshot().views === 0 &&
			!!document.querySelector('[data-cache-pane="file-16"] [data-file-editor-preview] h1'),
	);
	expect((await state()).registry.visibleConsumers).toBe(1);
	expect((await state()).registry.models).toBeLessThanOrEqual(8);
	const previewSelector = '[data-cache-pane="file-16"] [data-file-editor-preview]';
	const previewPosition = () =>
		page.$eval(previewSelector, (node) => {
			const element = node as HTMLElement;
			return {
				top: element.scrollTop,
				span: element.scrollHeight - element.clientHeight,
				fraction: element.scrollTop / (element.scrollHeight - element.clientHeight),
			};
		});
	await page.$eval(previewSelector, (node) => {
		const element = node as HTMLElement;
		element.scrollTop = (element.scrollHeight - element.clientHeight) * 0.45;
	});
	await page.evaluate(
		() =>
			new Promise<void>((resolve) =>
				requestAnimationFrame(() => requestAnimationFrame(() => resolve())),
			),
	);
	const previewBeforeHide = await previewPosition();
	expect(previewBeforeHide.span).toBeGreaterThan(1000);
	expect(previewBeforeHide.fraction).toBeCloseTo(0.45, 2);
	await page.evaluate(() => window.__editorCache.show("blank-left"));
	await wait(
		() =>
			!document.querySelector('[data-cache-pane="file-16"] [data-file-editor-preview] h1') &&
			window.__editorCache.snapshot().registry.visibleConsumers === 0,
	);
	await page.evaluate(() => window.__editorCache.show("file-16"));
	await wait(
		() => !!document.querySelector('[data-cache-pane="file-16"] [data-file-editor-preview] h1'),
	);
	await page.evaluate(
		() =>
			new Promise<void>((resolve) =>
				requestAnimationFrame(() => requestAnimationFrame(() => resolve())),
			),
	);
	const previewAfterHide = await previewPosition();
	expect(previewAfterHide.fraction).toBeCloseTo(previewBeforeHide.fraction, 4);
	expect(previewAfterHide.top).toBe(previewBeforeHide.top);
	await page.evaluate(async () => {
		window.__editorCache.show("blank-left");
		await new Promise<void>((resolve) => setTimeout(resolve, 250));
		window.__editorCache.show("file-16");
	});
	await wait(
		() => !!document.querySelector('[data-cache-pane="file-16"] [data-file-editor-preview] h1'),
	);
	await page.evaluate(
		() =>
			new Promise<void>((resolve) =>
				requestAnimationFrame(() => requestAnimationFrame(() => resolve())),
			),
	);
	const previewAfterRapid = await previewPosition();
	expect(previewAfterRapid.fraction).toBeCloseTo(previewBeforeHide.fraction, 4);
	expect(previewAfterRapid.top).toBe(previewBeforeHide.top);
	await page.evaluate(() => window.__editorCache.mode("file-16", "split"));
	await wait(() => window.__editorCache.snapshot().views === 1);
	expect((await state()).panes[0]?.position?.lineNumber).toBe(20);
	await page.evaluate(() => window.__editorCache.edit("file-16", "LIVE_SPLIT "));
	await wait(
		() =>
			!!document
				.querySelector('[data-cache-pane="file-16"] [data-file-editor-preview]')
				?.textContent?.includes("LIVE_SPLIT"),
	);
	// The live preview's 100ms anchor-refresh and resulting scroll followers must
	// settle before this independent user scroll, rather than compete with it.
	await page.evaluate(() => new Promise<void>((resolve) => setTimeout(resolve, 500)));
	await page.$eval(previewSelector, (node) => {
		const element = node as HTMLElement;
		element.scrollTop = (element.scrollHeight - element.clientHeight) * 0.37;
	});
	// Let the real bidirectional followers settle before saving the split position.
	await page.evaluate(() => new Promise<void>((resolve) => setTimeout(resolve, 500)));
	const splitBeforeHide = await previewPosition();
	expect(splitBeforeHide.fraction).toBeCloseTo(0.37, 2);
	await page.evaluate(() => window.__editorCache.workspaceVisible(false));
	await wait(() => window.__editorCache.snapshot().views === 0);
	expect(await page.$('[data-cache-pane="file-16"] [data-file-editor-preview] h1')).toBeNull();
	expect((await state()).registry.pinReasons.dirty).toBe(2);
	await page.evaluate(() => window.__editorCache.workspaceVisible(true));
	await wait(
		() =>
			window.__editorCache.snapshot().views === 1 &&
			!!document
				.querySelector('[data-cache-pane="file-16"] [data-file-editor-preview]')
				?.textContent?.includes("LIVE_SPLIT"),
	);
	await page.evaluate(() => new Promise<void>((resolve) => setTimeout(resolve, 500)));
	const splitAfterHide = await previewPosition();
	expect(splitAfterHide.fraction).toBeCloseTo(splitBeforeHide.fraction, 4);
	const beforeRapidLifetime = (await state()).panes[0]?.lifetime;
	await page.evaluate(async () => {
		window.__editorCache.show("blank-left");
		await new Promise<void>((resolve) => setTimeout(resolve, 250));
		window.__editorCache.show("file-16");
	});
	await wait(
		() =>
			window.__editorCache.snapshot().views === 1 &&
			!!document
				.querySelector('[data-cache-pane="file-16"] [data-file-editor-preview]')
				?.textContent?.includes("LIVE_SPLIT"),
	);
	await page.evaluate(() => new Promise<void>((resolve) => setTimeout(resolve, 500)));
	const splitAfterRapid = await previewPosition();
	expect(splitAfterRapid.fraction).toBeCloseTo(splitBeforeHide.fraction, 4);
	expect((await state()).panes[0]?.lifetime).toBe(beforeRapidLifetime);
	// Toolbar/search fields are outside both scroll panes: opening and automatic
	// match decoration must preserve the restored position, but explicit Enter/Next
	// navigation must relinquish resume protection before revealing a distant match.
	await page.click('[data-cache-pane="file-16"] button[aria-label="Find / replace"]');
	await page.waitForSelector('[data-cache-pane="file-16"] input[name="search"]');
	await page.type('[data-cache-pane="file-16"] input[name="search"]', "Preview section 140");
	await wait(() =>
		window.__editorCache.snapshot().workers.pool.slots.some((slot) => !!slot.docId && !slot.busy),
	);
	const splitAfterOpeningSearch = await previewPosition();
	expect(splitAfterOpeningSearch.top).toBe(splitAfterRapid.top);
	await page.keyboard.press("Enter");
	await wait(() => (window.__editorCache.snapshot().panes[0]?.position?.lineNumber ?? 0) > 500);
	await page.evaluate(() => new Promise<void>((resolve) => setTimeout(resolve, 500)));
	const splitAfterSearchReveal = await previewPosition();
	expect(splitAfterSearchReveal.fraction).toBeGreaterThan(0.8);
	expect((await state()).panes[0]?.scroll).toBeGreaterThan(8000);
	await page.keyboard.press("Escape");
	await page.evaluate(() => window.__editorCache.workspaceVisible(false));
	await wait(() => window.__editorCache.snapshot().views === 0);
	await page.evaluate(() => window.__editorCache.workspaceVisible(true));
	await wait(
		() =>
			window.__editorCache.snapshot().views === 1 &&
			!!document
				.querySelector('[data-cache-pane="file-16"] [data-file-editor-preview]')
				?.textContent?.includes("LIVE_SPLIT"),
	);
	await page.evaluate(() => new Promise<void>((resolve) => setTimeout(resolve, 500)));
	const beforeNextSearch = await previewPosition();
	await page.click('[data-cache-pane="file-16"] button[aria-label="Find / replace"]');
	await page.type('[data-cache-pane="file-16"] input[name="search"]', "Preview section 10");
	await wait(() =>
		window.__editorCache.snapshot().workers.pool.slots.some((slot) => !!slot.docId && !slot.busy),
	);
	expect((await previewPosition()).top).toBe(beforeNextSearch.top);
	await page.click(
		'[data-cache-pane="file-16"] [data-editor-search-panel] button[aria-label="next"]',
	);
	await wait(() => (window.__editorCache.snapshot().panes[0]?.position?.lineNumber ?? 1000) < 80);
	await page.evaluate(() => new Promise<void>((resolve) => setTimeout(resolve, 500)));
	const splitAfterNextReveal = await previewPosition();
	expect(splitAfterNextReveal.fraction).toBeLessThan(0.15);
	await page.keyboard.press("Escape");
	// Ctrl+F opens the same non-navigating UI without prematurely dropping restore
	// protection on its modifier/key events inside the source pane.
	await page.evaluate(() => window.__editorCache.workspaceVisible(false));
	await wait(() => window.__editorCache.snapshot().views === 0);
	await page.evaluate(() => window.__editorCache.workspaceVisible(true));
	await wait(
		() =>
			window.__editorCache.snapshot().views === 1 &&
			!!document
				.querySelector('[data-cache-pane="file-16"] [data-file-editor-preview]')
				?.textContent?.includes("LIVE_SPLIT"),
	);
	await page.evaluate(() => new Promise<void>((resolve) => setTimeout(resolve, 500)));
	const beforeShortcutOpen = await previewPosition();
	await page.evaluate(() => window.__editorCache.focus("file-16"));
	await page.keyboard.down("Control");
	await page.keyboard.press("f");
	await page.keyboard.up("Control");
	await page.waitForSelector('[data-cache-pane="file-16"] input[name="search"]');
	await page.evaluate(() => new Promise<void>((resolve) => setTimeout(resolve, 250)));
	expect((await previewPosition()).top).toBe(beforeShortcutOpen.top);
	await page.keyboard.press("Escape");
	// A real source key gesture relinquishes preview-resume authority; split sync
	// must continue following intentional editor navigation, not stay pinned forever.
	await page.evaluate(() => window.__editorCache.focus("file-16"));
	await page.keyboard.down("Control");
	await page.keyboard.press("Home");
	await page.keyboard.up("Control");
	await wait(() => {
		const preview = document.querySelector<HTMLElement>(
			'[data-cache-pane="file-16"] [data-file-editor-preview]',
		);
		return (
			!!preview &&
			preview.scrollTop < 100 &&
			window.__editorCache.snapshot().panes[0]?.position?.lineNumber === 1
		);
	});
	await page.evaluate(() => window.__editorCache.workspaceVisible(false));
	await wait(() => window.__editorCache.snapshot().views === 0);
	await page.evaluate(() => window.__editorCache.workspaceVisible(true));
	await wait(() => window.__editorCache.snapshot().views === 1);
	const externalNavigationLifetime = (await state()).panes[0]?.lifetime;
	await page.evaluate(() => window.__editorCache.navigate("file-16", 550));
	await wait(
		() =>
			window.__editorCache.snapshot().panes[0]?.position?.lineNumber === 550 &&
			!document.querySelector('[data-cache-pane="file-16"] [data-file-editor-preview]'),
	);
	await page.evaluate(() => new Promise<void>((resolve) => setTimeout(resolve, 250)));
	expect((await state()).panes[0]?.scroll).toBeGreaterThan(8000);
	expect((await state()).panes[0]?.lifetime).toBe(externalNavigationLifetime);
	await page.evaluate(() => window.__editorCache.navigate("file-16", 42));
	await wait(() => window.__editorCache.snapshot().panes[0]?.position?.lineNumber === 42);
	await page.evaluate(() => new Promise<void>((resolve) => setTimeout(resolve, 250)));
	expect((await state()).panes[0]?.scroll).toBeLessThan(2000);
	expect((await state()).panes[0]?.lifetime).toBe(externalNavigationLifetime);
	console.info(
		"EDITOR_PREVIEW_SCROLL_CHROMIUM",
		JSON.stringify({
			previewBeforeHide,
			previewAfterHide,
			previewAfterRapid,
			splitBeforeHide,
			splitAfterHide,
			splitAfterRapid,
			splitAfterOpeningSearch,
			splitAfterSearchReveal,
			splitAfterNextReveal,
			externalNavigationLines: [550, 42],
		}),
	);
	await page.evaluate(() => window.__editorCache.dispose());
	for (let attempt = 0; docCount() && attempt < 100; attempt++)
		await new Promise((resolve) => setTimeout(resolve, 10));
	expect(docCount()).toBe(0);
	expect(await files(transferRoot)).toBe(0);
	console.info(
		"EDITOR_CACHE_CHROMIUM",
		JSON.stringify({
			initial: {
				tabs: initial.tabs,
				workingCopies: initial.registry.workingCopies,
				models: initial.monacoModels,
				views: initial.views,
				docs: 1,
				creates: 2,
				contents: 1,
			},
			hidden: {
				models: hidden.monacoModels,
				views: hidden.views,
				dirtyPins: hidden.registry.pinReasons.dirty,
				workerMirrors: 0,
			},
			cached: {
				tabs: cached.tabs,
				workingCopies: cached.registry.workingCopies,
				models: cached.monacoModels,
				views: cached.views,
				docs: 8,
				estimatedBytes: cached.registry.estimatedBytes,
				pinnedBytes: cached.registry.pinnedBytes,
				requests: cachedRequests,
				fdBaseline,
				fdCached,
				transferBaseline,
				transferCached,
			},
			teardown: { docs: docCount(), transfers: await files(transferRoot) },
		}),
	);
}
