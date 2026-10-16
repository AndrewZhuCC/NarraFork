import { afterAll, beforeAll, expect, test } from "bun:test";
import { existsSync } from "node:fs";
import { basename } from "node:path";
import type { ComposerDocument } from "@shared/composer-document";
import puppeteer, { type Browser, type Page } from "puppeteer-core";
import type * as Fixture from "./NarratorComposer.browser-fixture";

type FixtureWindow = Window & { fixture: typeof Fixture };
const executablePath =
	process.env.NF_TEST_CHROMIUM_PATH ??
	"/home/fulcrum/.cache/puppeteer/chrome/linux-146.0.7680.31/chrome-linux64/chrome";
let browser: Browser | undefined;
let server: ReturnType<typeof Bun.serve> | undefined;
const errors = new WeakMap<Page, string[]>();
const emptyDraft = {
	hasDraft: false,
	text: "",
	fileReferences: [],
	revision: 1,
	updatedAt: null,
	updatedBy: null,
	sourceId: null,
};
beforeAll(async () => {
	if (!existsSync(executablePath)) throw new Error(`Chromium unavailable: ${executablePath}`);
	const bundle = await Bun.build({
		entrypoints: [new URL("./NarratorComposer.browser-fixture.tsx", import.meta.url).pathname],
		target: "browser",
		format: "esm",
		define: {
			"process.env.NODE_ENV": '"production"',
			"import.meta.env": '{"DEV":false,"PROD":true}',
		},
	});
	if (!bundle.success) throw new Error(bundle.logs.join("\n"));
	const assets = new Map(bundle.outputs.map((output) => [`/${basename(output.path)}`, output]));
	const script = [...assets.keys()].find((path) => path.endsWith(".js"));
	const styles = [...assets.keys()].filter((path) => path.endsWith(".css"));
	if (!script || !styles.length) throw new Error("Composer/CSS bundle missing");
	server = Bun.serve({
		hostname: "127.0.0.1",
		port: 0,
		async fetch(request) {
			const path = new URL(request.url).pathname;
			if (path === "/api/narrators/fixture-narrator/panel-state")
				return Response.json({
					permissions: [],
					buffer: [],
					draft: { hasDraft: false, revision: 1, updatedAt: null },
					hasMore: { permissions: false, buffer: false },
				});
			if (path === "/api/narrators/fixture-narrator/draft") {
				if (request.method === "PUT") {
					const body = await request.json();
					return Response.json({
						...emptyDraft,
						...body,
						ok: true,
						traits: [],
						revision: body.baseRevision + 1,
						hasDraft:
							!!body.text ||
							!!body.fileReferences?.length ||
							!!body.composerDocument?.assets.length,
					});
				}
				return Response.json(emptyDraft);
			}
			if (path.endsWith("/file-references/search"))
				return Response.json({
					entries: [
						{
							deviceId: "local",
							path: "/work/foo.ts",
							name: "foo.ts",
							relativePath: "foo.ts",
							isDirectory: false,
						},
						{
							deviceId: "local",
							path: "/work/food.ts",
							name: "food.ts",
							relativePath: "food.ts",
							isDirectory: false,
						},
					],
					truncated: false,
				});
			if (path.startsWith("/api/")) return Response.json([]);
			if (path === "/")
				return new Response(
					`<!doctype html><html><head><meta name="viewport" content="width=device-width,initial-scale=1">${styles.map((style) => `<link rel="stylesheet" href="${style}">`).join("")}<style>html,body{height:100%;margin:0}</style></head><body><div id="root"></div><script type="module">window.fixture=await import(${JSON.stringify(script)});</script></body></html>`,
					{ headers: { "content-type": "text/html" } },
				);
			const asset = assets.get(path);
			return asset ? new Response(asset) : new Response("Not found", { status: 404 });
		},
	});
	browser = await puppeteer.launch({
		executablePath,
		headless: true,
		args: ["--no-sandbox"],
		timeout: 15_000,
	});
}, 60_000);
afterAll(async () => {
	try {
		await browser?.close();
	} finally {
		server?.stop(true);
	}
});
async function settle(page: Page) {
	await page.evaluate(
		() =>
			new Promise<void>((resolve) =>
				requestAnimationFrame(() => requestAnimationFrame(() => resolve())),
			),
	);
}
async function open() {
	if (!browser || !server) throw new Error("Fixture unavailable");
	const page = await browser.newPage();
	page.setDefaultTimeout(5000);
	await page.setViewport({ width: 1000, height: 700 });
	const captured: string[] = [];
	errors.set(page, captured);
	page.on("pageerror", (error) => captured.push(String(error)));
	const hydrated = page.waitForResponse(
		(response) =>
			response.url().endsWith("/draft") && response.request().method() === "GET" && response.ok(),
	);
	await page.goto(server.url.origin);
	await hydrated;
	await page.waitForFunction(() => (window as unknown as FixtureWindow).fixture?.ready());
	await settle(page);
	return page;
}
async function close(page: Page) {
	await page.close();
	expect(errors.get(page)).toEqual([]);
}
async function snapshot(page: Page) {
	return page.evaluate(() => (window as unknown as FixtureWindow).fixture.snapshot());
}
async function geometry(page: Page) {
	return page.evaluate(() => {
		const row = document.getElementById("row")?.getBoundingClientRect();
		const messages = document.getElementById("messages");
		const viewport = messages?.getBoundingClientRect();
		return {
			rowTop: row?.top ?? 0,
			rowHeight: row?.height ?? 0,
			messagesHeight: viewport?.height ?? 0,
			scrollTop: messages?.scrollTop ?? 0,
			selection: window.getSelection()?.toString() ?? "",
		};
	});
}

test("production Row footer stays inside the editor and typing does not rerender its parent boundary", async () => {
	const page = await open();
	try {
		expect(
			await page.evaluate(() => {
				const shell = document.querySelector("[data-inline-composer]");
				const attach = document.querySelector('button[aria-label="Attach file"]');
				const send = [...document.querySelectorAll("button")].find(
					(button) => button.textContent?.trim() === "Send",
				);
				return !!shell && !!attach && !!send && shell.contains(attach) && shell.contains(send);
			}),
		).toBe(true);
		await page.click('[role="textbox"]');
		await page.keyboard.type("hello");
		await settle(page);
		const before = await snapshot(page);
		await page.keyboard.type(" world repeated typing");
		await settle(page);
		const after = await snapshot(page);
		expect(after.text).toBe("hello world repeated typing");
		expect(after.parentRenders).toBe(before.parentRenders);
		expect(after.sends).toBe(0);
	} finally {
		await close(page);
	}
}, 15_000);

test("20 real message selections keep main composer geometry, message scroll and source selection unchanged", async () => {
	const page = await open();
	try {
		await page.evaluate(() => {
			const messages = document.getElementById("messages");
			if (messages) messages.scrollTop = 12;
		});
		const initial = await geometry(page);
		expect(initial.scrollTop).toBeGreaterThan(0);
		for (let i = 0; i < 20; i++) {
			await page.evaluate(() => (window as unknown as FixtureWindow).fixture.selectSource());
			await page.waitForSelector("[data-inline-quote-candidate]", { visible: true });
			const selected = await geometry(page);
			expect(Math.abs(selected.rowTop - initial.rowTop)).toBeLessThanOrEqual(1);
			expect(Math.abs(selected.rowHeight - initial.rowHeight)).toBeLessThanOrEqual(1);
			expect(Math.abs(selected.messagesHeight - initial.messagesHeight)).toBeLessThanOrEqual(1);
			expect(selected.scrollTop).toBe(initial.scrollTop);
			expect(selected.selection).toContain("Select this source text");
			await page.evaluate(() => (window as unknown as FixtureWindow).fixture.clearSelection());
			await page.waitForSelector("[data-inline-quote-candidate]", { hidden: true });
		}
		await page.evaluate(() => (window as unknown as FixtureWindow).fixture.selectSource());
		await page.waitForSelector("[data-inline-quote-candidate]", { visible: true });
		await page.click("[data-inline-quote-candidate] button");
		await settle(page);
		expect(
			(await snapshot(page)).document?.paragraphs[0].children.some((node) => node.type === "quote"),
		).toBe(true);
	} finally {
		await close(page);
	}
}, 20_000);

test("real File previews survive failed sends, then successful commit releases their resource bindings", async () => {
	const page = await open();
	try {
		const doc = await page.evaluate(() =>
			(window as unknown as FixtureWindow).fixture.stageMixed(),
		);
		await settle(page);
		expect((await snapshot(page)).missing).toBe(false);
		await page.waitForSelector("[data-inline-composer] img");
		await page.waitForFunction(() =>
			[...document.querySelectorAll<HTMLImageElement>("[data-inline-composer] img")].every(
				(image) => image.complete && image.naturalWidth > 0,
			),
		);
		if (process.env.NF_RICH_COMPOSER_CAPTURE === "1")
			await page.screenshot({ path: ".narrafork/rich-composer-preview.png", fullPage: true });
		await page.evaluate(() => (window as unknown as FixtureWindow).fixture.hide());
		await settle(page);
		await page.evaluate(
			(value) => (window as unknown as FixtureWindow).fixture.restore(value),
			doc,
		);
		await settle(page);
		expect((await snapshot(page)).missing).toBe(false);
		await page.evaluate(() => {
			const fixture = (window as unknown as FixtureWindow).fixture;
			fixture.hide();
			fixture.commit();
		});
		await settle(page);
		await page.evaluate(
			(value) => (window as unknown as FixtureWindow).fixture.restore(value),
			doc,
		);
		await settle(page);
		expect((await snapshot(page)).missing).toBe(true);
	} finally {
		await close(page);
	}
}, 20_000);

function textDocument(text: string): ComposerDocument {
	return { version: 1, paragraphs: [{ children: [{ type: "text", text }] }], assets: [] };
}
async function loadDocument(page: Page, document: ComposerDocument) {
	await page.evaluate((doc) => (window as unknown as FixtureWindow).fixture.load(doc), document);
	await settle(page);
}
for (const key of ["Tab", "Enter"] as const) {
	test(`file completion ${key} is handled before the composer send shortcut`, async () => {
		const page = await open();
		try {
			await loadDocument(page, textDocument("#fo"));
			await page.evaluate(() => (window as unknown as FixtureWindow).fixture.nativeSelection(4));
			await page.waitForSelector("[data-file-reference-item]");
			await page.keyboard.press(key === "Tab" ? "ArrowDown" : "ArrowUp");
			await settle(page);
			await page.keyboard.press(key);
			await settle(page);
			const state = await snapshot(page);
			expect(
				state.document?.paragraphs[0].children.find((node) => node.type === "fileReference"),
			).toMatchObject({ reference: { label: "food.ts" } });
			expect(state.sends).toBe(0);
			expect(await page.$("[data-file-reference-item]")).toBeNull();
		} finally {
			await close(page);
		}
	});
}
test("file completion Escape closes the menu without clearing the draft", async () => {
	const page = await open();
	try {
		await loadDocument(page, textDocument("#fo"));
		await page.evaluate(() => (window as unknown as FixtureWindow).fixture.nativeSelection(4));
		await page.waitForSelector("[data-file-reference-item]");
		await page.keyboard.press("Escape");
		await settle(page);
		expect(await page.$("[data-file-reference-item]")).toBeNull();
		expect((await snapshot(page)).text).toBe("#fo");
	} finally {
		await close(page);
	}
});
test("file and mention queries stop at inline atoms instead of deleting them", async () => {
	const page = await open();
	const quote = {
		type: "quote",
		id: "q-boundary",
		label: "source",
		text: "keep this quote",
	} as const;
	try {
		await loadDocument(page, {
			version: 1,
			assets: [],
			paragraphs: [
				{ children: [{ type: "text", text: "#fo" }, quote, { type: "text", text: "o" }] },
			],
		});
		await page.evaluate(() => (window as unknown as FixtureWindow).fixture.nativeSelection(6));
		await settle(page);
		expect(await page.$("[data-file-reference-item]")).toBeNull();
		expect((await snapshot(page)).document?.paragraphs[0].children).toContainEqual(quote);
		await page.evaluate(() =>
			(window as unknown as FixtureWindow).fixture.namedCandidates([
				{ id: "named", handle: "foo", title: "Foo" },
			]),
		);
		await loadDocument(page, {
			version: 1,
			assets: [],
			paragraphs: [
				{ children: [{ type: "text", text: "@fo" }, quote, { type: "text", text: "o" }] },
			],
		});
		await page.evaluate(() => (window as unknown as FixtureWindow).fixture.nativeSelection(6));
		await settle(page);
		expect(await page.$("[data-mention-item]")).toBeNull();
		await loadDocument(page, {
			version: 1,
			assets: [],
			paragraphs: [
				{ children: [{ type: "text", text: "before" }, quote, { type: "text", text: "#fo" }] },
			],
		});
		await page.evaluate(() => (window as unknown as FixtureWindow).fixture.nativeSelection(11));
		await page.waitForSelector("[data-file-reference-item]");
		await page.click("[data-file-reference-item]");
		await settle(page);
		expect((await snapshot(page)).document?.paragraphs[0].children).toContainEqual(quote);
		await loadDocument(page, {
			version: 1,
			assets: [],
			paragraphs: [
				{ children: [{ type: "text", text: "before" }, quote, { type: "text", text: "@fo" }] },
			],
		});
		await page.evaluate(() => (window as unknown as FixtureWindow).fixture.nativeSelection(11));
		await page.waitForSelector("[data-mention-item]");
		await page.click("[data-mention-item]");
		await settle(page);
		const state = await snapshot(page);
		expect(state.document?.paragraphs[0].children).toContainEqual(quote);
		expect(state.text).toContain("@foo ");
	} finally {
		await close(page);
	}
});
test("asynchronous real image ingestion restores its mapped insertion bookmark, not a later selection", async () => {
	const page = await open();
	try {
		await loadDocument(page, textDocument("alpha beta gamma"));
		await page.evaluate(() => {
			const fixture = (window as unknown as FixtureWindow).fixture;
			fixture.nativeSelection(7);
			fixture.ingestImage();
			fixture.nativeSelection(12, 17);
		});
		await page.evaluate(() => (window as unknown as FixtureWindow).fixture.awaitFiles());
		await settle(page);
		const state = await snapshot(page);
		expect(state.document?.paragraphs[0].children.map((node) => node.type)).toEqual([
			"text",
			"attachment",
			"text",
		]);
		expect(state.document?.paragraphs[0].children[0]).toEqual({ type: "text", text: "alpha " });
		expect(state.document?.paragraphs[0].children[2]).toEqual({ type: "text", text: "beta gamma" });
		expect(state.files).toEqual(["async.png"]);
	} finally {
		await close(page);
	}
});
test("switching the draft while a real image is prepared cancels insertion atomically", async () => {
	const page = await open();
	try {
		await loadDocument(page, textDocument("old draft"));
		await page.evaluate(() => {
			const fixture = (window as unknown as FixtureWindow).fixture;
			fixture.nativeSelection(4);
			fixture.ingestImage();
			fixture.load({
				version: 1,
				paragraphs: [{ children: [{ type: "text", text: "replacement" }] }],
				assets: [],
			});
		});
		await page.evaluate(() => (window as unknown as FixtureWindow).fixture.awaitFiles());
		await settle(page);
		const state = await snapshot(page);
		expect(state.text).toBe("replacement");
		expect(state.document?.assets).toHaveLength(0);
		expect(state.files).toEqual([]);
	} finally {
		await close(page);
	}
});
test("adopting a remote deletion cannot resurrect the old attachments on the next upload", async () => {
	const page = await open();
	try {
		const synced = page.waitForResponse(
			(response) =>
				response.url().endsWith("/draft") && response.request().method() === "PUT" && response.ok(),
		);
		await page.evaluate(() => (window as unknown as FixtureWindow).fixture.stageMixed());
		await synced;
		await settle(page);
		await page.evaluate(() =>
			(window as unknown as FixtureWindow).fixture.remote({
				version: 1,
				paragraphs: [{ children: [{ type: "text", text: "remote draft" }] }],
				assets: [],
			}),
		);
		await page.waitForFunction(() => {
			const state = (window as unknown as FixtureWindow).fixture.snapshot();
			return state.text === "remote draft" && state.files.length === 0;
		});
		await page.evaluate(() => (window as unknown as FixtureWindow).fixture.ingestText("G.txt"));
		await page.evaluate(() => (window as unknown as FixtureWindow).fixture.awaitFiles());
		await settle(page);
		const state = await snapshot(page);
		expect(state.document?.assets.map((asset) => asset.name)).toEqual(["G.txt"]);
		expect(state.files).toEqual(["G.txt"]);
	} finally {
		await close(page);
	}
}, 15000);
test("Down exits input history even when the recalled message has missing attachments", async () => {
	const page = await open();
	try {
		await page.evaluate(() => {
			const fixture = (window as unknown as FixtureWindow).fixture;
			const doc = fixture.stageMixed();
			fixture.remember(doc);
			fixture.hide();
			fixture.commit();
			fixture.restore({
				version: 1,
				paragraphs: [{ children: [{ type: "text", text: "my unsent draft" }] }],
				assets: [],
			});
		});
		await settle(page);
		await page.evaluate(() => (window as unknown as FixtureWindow).fixture.nativeSelection(1));
		await settle(page); // Tiptap applies DOM focus on the next animation frame.
		await page.keyboard.press("ArrowUp");
		await page.waitForFunction(
			() =>
				((window as unknown as FixtureWindow).fixture.snapshot().document?.assets.length ?? 0) > 0,
		);
		expect((await snapshot(page)).missing).toBe(true);
		await page.keyboard.press("ArrowDown");
		await page.waitForFunction(
			() => (window as unknown as FixtureWindow).fixture.snapshot().text === "my unsent draft",
		);
		expect((await snapshot(page)).document?.assets ?? []).toHaveLength(0);
	} finally {
		await close(page);
	}
}, 15000);
test("the production fullscreen scroll layer fills the actual composer dialog", async () => {
	const page = await open();
	try {
		await loadDocument(
			page,
			textDocument(Array.from({ length: 40 }, (_, index) => `line ${index}`).join("\n")),
		);
		await page.click("button:has(svg.tabler-icon-arrows-maximize)");
		await settle(page);
		const fullscreen = await page.evaluate(() => {
			const element = document.querySelector<HTMLElement>("[data-composer-scroll]");
			return { height: element?.clientHeight ?? 0, scroll: element?.scrollHeight ?? 0 };
		});
		expect(fullscreen.height).toBeGreaterThan(350);
		expect(fullscreen.scroll).toBeGreaterThan(fullscreen.height);
		await page.keyboard.press("Escape");
		await settle(page);
		expect(
			await page.evaluate(
				() => document.querySelector<HTMLElement>("[data-composer-scroll]")?.clientHeight ?? 0,
			),
		).toBeLessThanOrEqual(200);
	} finally {
		await close(page);
	}
});

test("fullscreen Escape dismisses completion before closing the editor", async () => {
	const page = await open();
	try {
		await loadDocument(page, textDocument("#fo"));
		await page.evaluate(() => (window as unknown as FixtureWindow).fixture.nativeSelection(4));
		await page.waitForSelector("[data-file-reference-item]");
		await page.click("button:has(svg.tabler-icon-arrows-maximize)");
		await settle(page);
		await page.evaluate(() => (window as unknown as FixtureWindow).fixture.nativeSelection(4));
		await settle(page);
		await page.keyboard.press("Escape");
		await settle(page);
		expect(await page.$("[data-file-reference-item]")).toBeNull();
		expect(await page.$('[role="dialog"]')).not.toBeNull();
		await page.keyboard.press("Escape");
		await settle(page);
		expect(await page.$('[role="dialog"]')).toBeNull();
	} finally {
		await close(page);
	}
});

function nodeCount(doc: ComposerDocument | undefined, type: string) {
	return (
		doc?.paragraphs.flatMap((paragraph) => paragraph.children).filter((node) => node.type === type)
			.length ?? 0
	);
}
async function selectQuotableSource(page: Page) {
	await page.evaluate(() => (window as unknown as FixtureWindow).fixture.selectSource());
	await page.waitForSelector("[data-inline-quote-candidate]", { visible: true });
}

test("a live selected message is quoted once with Tab, not Alt+Q or modified Tab", async () => {
	const page = await open();
	try {
		await selectQuotableSource(page);
		expect(await page.$eval("[data-inline-quote-candidate] kbd", (node) => node.textContent)).toBe(
			"Tab",
		);
		const consumed = await page.evaluate(() =>
			[
				{ key: "q", code: "KeyQ", altKey: true },
				{ key: "Tab", code: "Tab", shiftKey: true },
				{ key: "Tab", code: "Tab", ctrlKey: true },
				{ key: "Tab", code: "Tab", metaKey: true },
				{ key: "Tab", code: "Tab", altKey: true },
				{ key: "Tab", code: "Tab", isComposing: true },
			].map((init) => {
				const event = new KeyboardEvent("keydown", { ...init, bubbles: true, cancelable: true });
				document.body.dispatchEvent(event);
				return event.defaultPrevented;
			}),
		);
		expect(consumed).toEqual([false, false, false, false, false, false]);
		expect(nodeCount((await snapshot(page)).document, "quote")).toBe(0);
		await page.keyboard.press("Tab");
		await settle(page);
		const state = await snapshot(page);
		expect(nodeCount(state.document, "quote")).toBe(1);
		expect(state.document?.paragraphs[0].children[0]).toMatchObject({
			type: "quote",
			anchor: "m:message-source",
		});
		expect(await page.$("[data-inline-quote-candidate]")).toBeNull();
		expect(await page.evaluate(() => document.activeElement?.getAttribute("role"))).toBe("textbox");
		await page.keyboard.press("Tab");
		await settle(page);
		expect(nodeCount((await snapshot(page)).document, "quote")).toBe(1);
	} finally {
		await close(page);
	}
});

test("normal input Tab is not captured by a stale message quote candidate", async () => {
	const page = await open();
	try {
		await selectQuotableSource(page);
		await loadDocument(page, textDocument("hello"));
		await page.evaluate(() => (window as unknown as FixtureWindow).fixture.nativeSelection(3));
		await settle(page);
		const prevented = await page.evaluate(() => {
			const event = new KeyboardEvent("keydown", {
				key: "Tab",
				code: "Tab",
				bubbles: true,
				cancelable: true,
			});
			document.querySelector('[role="textbox"]')?.dispatchEvent(event);
			return event.defaultPrevented;
		});
		expect(prevented).toBe(false);
		expect((await snapshot(page)).text).toBe("hello");
		expect(nodeCount((await snapshot(page)).document, "quote")).toBe(0);
	} finally {
		await close(page);
	}
});

test("touch and pen selections hide shortcut hints; mouse on the same touch-capable device shows Tab", async () => {
	const page = await open();
	try {
		await page.setViewport({ width: 390, height: 844, hasTouch: true, isMobile: true });
		// Switching touch/mobile emulation reloads the page; await the new editor instance.
		await page.waitForFunction(() => (window as unknown as FixtureWindow).fixture?.ready());
		await settle(page);
		const source = await page.$eval("#quote-source", (element) => {
			const rect = element.getBoundingClientRect();
			return { x: rect.x + 20, y: rect.y + 12 };
		});
		await page.touchscreen.tap(source.x, source.y);
		await selectQuotableSource(page);
		expect(await page.$("[data-inline-quote-candidate] kbd")).toBeNull();
		expect(
			await page.$eval("[data-inline-quote-candidate]", (element) =>
				element.getAttribute("aria-label"),
			),
		).not.toContain("Tab");
		await page.mouse.click(source.x, source.y);
		await selectQuotableSource(page);
		await page.waitForSelector("[data-inline-quote-candidate] kbd");
		expect(
			await page.$eval("[data-inline-quote-candidate] kbd", (element) => element.textContent),
		).toBe("Tab");
		await page.evaluate(() =>
			document
				.getElementById("quote-source")
				?.dispatchEvent(new PointerEvent("pointerdown", { pointerType: "pen", bubbles: true })),
		);
		await settle(page);
		expect(await page.$("[data-inline-quote-candidate] kbd")).toBeNull();
		await page.evaluate(() =>
			document.body.dispatchEvent(
				new KeyboardEvent("keydown", { key: "Process", isComposing: true, bubbles: true }),
			),
		);
		await settle(page);
		expect(await page.$("[data-inline-quote-candidate] kbd")).toBeNull();
		const button = await page.$eval("[data-inline-quote-candidate] button", (element) => {
			const rect = element.getBoundingClientRect();
			return { x: rect.x + 20, y: rect.y + rect.height / 2 };
		});
		await page.touchscreen.tap(button.x, button.y);
		await settle(page);
		expect(nodeCount((await snapshot(page)).document, "quote")).toBe(1);
	} finally {
		await close(page);
	}
}, 15000);

test("a Monaco selection has no global quote popup and its Tab is never consumed", async () => {
	const page = await open();
	try {
		await page.evaluate(() => (window as unknown as FixtureWindow).fixture.publishFileSelection());
		await settle(page);
		expect(await page.$("[data-inline-quote-candidate]")).toBeNull();
		await page.evaluate(() => {
			const source = document.getElementById("file-selection-source");
			source?.addEventListener("keydown", (event) => {
				if ((event as KeyboardEvent).key === "Tab")
					source.setAttribute("data-tab-native", String(!event.defaultPrevented));
			});
		});
		await page.keyboard.press("Tab");
		await settle(page);
		expect(
			await page.$eval("#file-selection-source", (element) =>
				element.getAttribute("data-tab-native"),
			),
		).toBe("true");
		expect(nodeCount((await snapshot(page)).document, "fileReference")).toBe(0);
	} finally {
		await close(page);
	}
});

test("a retained selection in A cannot intercept a collapsed foreign Monaco B Tab", async () => {
	const page = await open();
	try {
		await page.evaluate(() => {
			(window as unknown as FixtureWindow).fixture.publishFileSelection();
			const pane = document.createElement("section");
			pane.setAttribute("data-narrator-panel", "other-narrator");
			const monaco = document.createElement("div");
			monaco.className = "monaco-editor";
			const source = document.createElement("textarea");
			source.id = "foreign-source";
			source.value = "other file";
			source.addEventListener("keydown", (event) => {
				if (event.key === "Tab") source.dataset.tabNative = String(!event.defaultPrevented);
			});
			monaco.append(source);
			pane.append(monaco);
			document.body.append(pane);
			source.focus();
			source.setSelectionRange(0, 0);
		});
		await settle(page);
		await page.keyboard.press("Tab");
		await settle(page);
		expect(
			await page.$eval("#foreign-source", (element) => element.getAttribute("data-tab-native")),
		).toBe("true");
		expect(nodeCount((await snapshot(page)).document, "fileReference")).toBe(0);
		expect((await snapshot(page)).text).toBe("");
	} finally {
		await close(page);
	}
});

test("file selection metadata still supports explicit #selection completion without a quote popup", async () => {
	const page = await open();
	try {
		await page.evaluate(() => (window as unknown as FixtureWindow).fixture.publishFileSelection());
		await settle(page);
		expect(await page.$("[data-inline-quote-candidate]")).toBeNull();
		await loadDocument(page, textDocument("#selection"));
		await page.evaluate(() => (window as unknown as FixtureWindow).fixture.nativeSelection(11));
		await page.waitForSelector("[data-file-reference-item]");
		await page.keyboard.press("Tab");
		await settle(page);
		expect(nodeCount((await snapshot(page)).document, "fileReference")).toBe(1);
		expect((await snapshot(page)).text).toContain("#file:src.ts:1-1");
	} finally {
		await close(page);
	}
});

for (const target of ["search", "monaco"] as const) {
	test(`blurred mention completion cannot steal ${target} Tab and reopens only for its own editor`, async () => {
		const page = await open();
		try {
			await page.evaluate(() =>
				(window as unknown as FixtureWindow).fixture.namedCandidates([
					{ id: "named", handle: "foo" },
				]),
			);
			await loadDocument(page, textDocument("@fo"));
			await page.evaluate(() => (window as unknown as FixtureWindow).fixture.nativeSelection(4));
			await page.waitForSelector("[data-mention-item]");
			await page.evaluate((kind) => {
				const parent = document.createElement("div");
				if (kind === "monaco") parent.className = "monaco-editor";
				const input = document.createElement("input");
				input.id = "other-input";
				input.type = "search";
				input.addEventListener("keydown", (event) => {
					if (event.key === "Tab") input.dataset.tabNative = String(!event.defaultPrevented);
				});
				parent.append(input);
				document.body.append(parent);
				input.focus();
			}, target);
			await settle(page);
			expect(await page.$("[data-mention-item]")).toBeNull();
			await page.keyboard.press("Tab");
			await settle(page);
			expect(
				await page.$eval("#other-input", (element) => element.getAttribute("data-tab-native")),
			).toBe("true");
			expect((await snapshot(page)).text).toBe("@fo");
			await page.evaluate(() => (window as unknown as FixtureWindow).fixture.nativeSelection(4));
			await page.waitForSelector("[data-mention-item]");
			await page.keyboard.press("Tab");
			await settle(page);
			expect((await snapshot(page)).text).toBe("@foo ");
		} finally {
			await close(page);
		}
	});
}

test("touch-to-mouse dismiss does not move the close target during pointerdown", async () => {
	const page = await open();
	try {
		await page.setViewport({ width: 1000, height: 800, hasTouch: true });
		await page.waitForFunction(() => (window as unknown as FixtureWindow).fixture?.ready());
		await settle(page);
		const source = await page.$eval("#quote-source", (element) => {
			const rect = element.getBoundingClientRect();
			return { x: rect.x + 20, y: rect.y + 12 };
		});
		await page.touchscreen.tap(source.x, source.y);
		await selectQuotableSource(page);
		expect(await page.$("[data-inline-quote-candidate] kbd")).toBeNull();
		const closePoint = () =>
			page.$eval("[data-inline-quote-candidate] > button:last-child", (element) => {
				const rect = element.getBoundingClientRect();
				return { x: rect.x + rect.width / 2, y: rect.y + rect.height / 2 };
			});
		const before = await closePoint();
		await page.mouse.move(before.x, before.y);
		await page.mouse.down();
		await settle(page);
		expect(await closePoint()).toEqual(before);
		await page.mouse.up();
		await settle(page);
		expect(await page.$("[data-inline-quote-candidate]")).toBeNull();
		expect(nodeCount((await snapshot(page)).document, "quote")).toBe(0);
	} finally {
		await close(page);
	}
}, 15000);
