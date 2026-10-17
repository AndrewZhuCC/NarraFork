/**
 * The typography dock panel must never scroll horizontally.
 *
 * ## What this locks
 *
 * Mantine's `Slider` renders each mark as an absolutely positioned wrapper whose
 * width is shrink-to-fit around the dot plus the nowrap label. The wrapper's left
 * edge is the mark's own centre, so the LAST mark pushes half a label past the
 * track — into the content box of whatever scroll container holds the sliders.
 * `frontend/styles/mantine-slider-marks.css` collapses that wrapper instead.
 *
 * A unit test cannot see this: the escape only exists once a layout engine has
 * resolved shrink-to-fit widths. So the check runs against real Chromium with the
 * production panel mounted in a real Dockview surface, and asserts on the measured
 * scroll boxes rather than on the stylesheet text.
 *
 * Widening the panel does not make it disappear (the overflow is a per-mark
 * constant, not a narrow-width artifact), so several widths are covered — from a
 * comfortable desktop split down to a docked column narrower than the labels.
 */

import { afterAll, beforeAll, expect, test } from "bun:test";
import { existsSync, mkdirSync } from "node:fs";
import { basename } from "node:path";
import puppeteer, { type Browser, type Page } from "puppeteer-core";
import type * as Fixture from "./appearance-panel-overflow.browser-fixture";

// This suite owns only an ephemeral static server and an ephemeral browser: it
// never starts, restarts, or talks to NarraFork itself.
const cachedChromium =
	"/home/fulcrum/.cache/puppeteer/chrome/linux-146.0.7680.31/chrome-linux64/chrome";
const executablePath = process.env.NF_TEST_CHROMIUM_PATH ?? cachedChromium;

let browser: Browser | undefined;
let server: ReturnType<typeof Bun.serve> | undefined;
type FixtureWindow = Window & { fixture: typeof Fixture };
type Report = ReturnType<typeof Fixture.overflowReport>;

beforeAll(async () => {
	if (!existsSync(executablePath))
		throw new Error(`Chromium unavailable at ${executablePath}; set NF_TEST_CHROMIUM_PATH`);
	mkdirSync("artifacts", { recursive: true });
	const bundle = await Bun.build({
		entrypoints: [
			new URL("./appearance-panel-overflow.browser-fixture.tsx", import.meta.url).pathname,
		],
		target: "browser",
		format: "esm",
		define: { "process.env.NODE_ENV": '"production"' },
	});
	if (!bundle.success) throw new Error(bundle.logs.join("\n"));
	const assets = new Map(bundle.outputs.map((output) => [`/${basename(output.path)}`, output]));
	const script = [...assets.keys()].find((path) => path.endsWith(".js"));
	if (!script) throw new Error("Browser fixture bundle has no JavaScript output");
	const styles = [...assets.keys()].filter((path) => path.endsWith(".css"));
	server = Bun.serve({
		hostname: "127.0.0.1",
		port: 0,
		fetch(request) {
			const path = new URL(request.url).pathname;
			if (path === "/")
				return new Response(
					`<!doctype html><html><head><meta charset="utf-8">
					<meta name="viewport" content="width=device-width,initial-scale=1">
					${styles.map((style) => `<link rel="stylesheet" href="${style}">`).join("")}
					<style>html,body { margin: 0; height: 100%; }</style>
					</head><body><div id="root"></div><script type="module">
					window.fixture = await import(${JSON.stringify(script)});
					</script></body></html>`,
					{ headers: { "Content-Type": "text/html" } },
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
			new Promise<void>((resolve) => {
				requestAnimationFrame(() => requestAnimationFrame(() => resolve()));
			}),
	);
}

async function reportAt(page: Page, width: number, height: number): Promise<Report> {
	await page.setViewport({ width, height });
	await page.reload({ waitUntil: "networkidle0" });
	await page.waitForFunction(
		() => document.querySelectorAll(".mantine-Slider-markLabel").length > 0,
	);
	await settle(page);
	return page.evaluate(() => (window as unknown as FixtureWindow).fixture.overflowReport());
}

test("the typography panel never gains a horizontal scrollbar", async () => {
	if (!browser || !server) throw new Error("Browser fixture not initialized");
	const page = await browser.newPage();
	const errors: string[] = [];
	const unexpectedRequests: string[] = [];
	page.on("pageerror", (error) => errors.push(String(error)));
	page.on("request", (request) => {
		const url = new URL(request.url());
		if (url.origin !== server?.url.origin) unexpectedRequests.push(url.href);
	});
	try {
		await page.goto(`${server.url.origin}/`, { waitUntil: "networkidle0" });
		await page.waitForSelector(".dv-dockview");
		for (const width of [1200, 760, 480, 320, 240]) {
			const report = await reportAt(page, width, 820);
			// The marks must be there: "no overflow" from a panel that rendered no
			// labels would be a vacuous pass.
			expect(report.markCount).toBe(12);
			expect(report.labelCount).toBe(12);
			expect(report.panel).not.toBeNull();
			// The whole point: labels are centred on their mark, so the last one still
			// reaches ~half a label past the track, but it must stay inside the panel.
			expect(report.labelsInsidePanel).toBe(true);
			expect(report.panel?.scrollWidth).toBeLessThanOrEqual(report.panel?.clientWidth ?? 0);
			expect(report.surface.scrollWidth).toBeLessThanOrEqual(report.surface.clientWidth);
			expect(report.scrollers).toEqual([]);
		}
		expect(errors).toEqual([]);
		expect(unexpectedRequests).toEqual([]);
	} finally {
		await page.close();
	}
}, 60_000);

/**
 * The fixture above imports the stylesheet itself, so the measurement cannot see
 * whether the real app ships it. Deleting that one line would fix nothing while
 * every test stayed green — the exact silent-fuse failure the browser suite
 * exists to prevent — so pin the entry's import.
 */
test("the app entry loads the slider-marks patch", async () => {
	const entry = await Bun.file(new URL("../../main.tsx", import.meta.url)).text();
	expect(entry).toContain('import "@frontend/styles/mantine-slider-marks.css";');
});
