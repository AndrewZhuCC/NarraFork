import { afterAll, beforeAll, expect, test } from "bun:test";
import { existsSync } from "node:fs";
import { basename } from "node:path";
import puppeteer, { type Browser } from "puppeteer-core";

const executablePath =
	process.env.NF_TEST_CHROMIUM_PATH ??
	"/home/fulcrum/.cache/puppeteer/chrome/linux-146.0.7680.31/chrome-linux64/chrome";
let browser: Browser | undefined;
let server: ReturnType<typeof Bun.serve> | undefined;

beforeAll(async () => {
	if (!existsSync(executablePath)) throw new Error(`Chromium unavailable: ${executablePath}`);
	const bundle = await Bun.build({
		entrypoints: ["version-banner-fixture"],
		target: "browser",
		format: "esm",
		define: { "process.env.NODE_ENV": '"production"' },
		plugins: [
			{
				name: "version-banner-fixture",
				setup(builder) {
					builder.onResolve({ filter: /^version-banner-fixture$/ }, () => ({
						path: "fixture",
						namespace: "fixture",
					}));
					builder.onLoad({ filter: /.*/, namespace: "fixture" }, () => ({
						loader: "tsx",
						resolveDir: import.meta.dir,
						contents: `import '@mantine/core/styles.css';
import { MantineProvider } from '@mantine/core';
import { createRoot } from 'react-dom/client';
import { VersionUpdateBanner } from ${JSON.stringify(new URL("./VersionUpdateBanner.tsx", import.meta.url).pathname)};
createRoot(document.getElementById('root')).render(<MantineProvider><VersionUpdateBanner /></MantineProvider>);`,
					}));
					builder.onResolve({ filter: /\/useVersionCheck$/ }, () => ({
						path: "hook",
						namespace: "hook",
					}));
					builder.onLoad({ filter: /.*/, namespace: "hook" }, () => ({
						loader: "js",
						contents: `export const useVersionCheck=()=>({updateAvailable:true,serverVersion:'0.9.0+fe6657f5a48086b931639c1a03bebdc61c87e123',refresh:()=>window.refreshCalls++,dismiss:()=>window.dismissCalls++});`,
					}));
					builder.onResolve({ filter: /^react-i18next$/ }, () => ({
						path: "i18n",
						namespace: "i18n",
					}));
					builder.onLoad({ filter: /.*/, namespace: "i18n" }, () => ({
						loader: "js",
						resolveDir: import.meta.dir,
						contents: `import en from ${JSON.stringify(new URL("../locales/en/common.json", import.meta.url).pathname)};
import zh from ${JSON.stringify(new URL("../locales/zh-CN/common.json", import.meta.url).pathname)};
export const useTranslation=()=>({t:(key,params)=> (new URLSearchParams(location.search).get('lang')==='en'?en:zh)[key].replace('{{version}}',params?.version??'')});`,
					}));
				},
			},
		],
	});
	if (!bundle.success) throw new Error(bundle.logs.join("\n"));
	const assets = new Map(bundle.outputs.map((output) => [`/${basename(output.path)}`, output]));
	const script = [...assets.keys()].find((path) => path.endsWith(".js"));
	const styles = [...assets.keys()].filter((path) => path.endsWith(".css"));
	if (!script || !styles.length) throw new Error("Banner/CSS bundle missing");
	server = Bun.serve({
		hostname: "127.0.0.1",
		port: 0,
		fetch(request) {
			const path = new URL(request.url).pathname;
			if (path === "/")
				return new Response(
					`<!doctype html><html><head><meta name="viewport" content="width=device-width,initial-scale=1">${styles.map((style) => `<link rel="stylesheet" href="${style}">`).join("")}</head><body><div id="root"></div><script>window.refreshCalls=0;window.dismissCalls=0;</script><script type="module" src="${script}"></script></body></html>`,
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

for (const width of [320, 390, 1440]) {
	for (const language of ["en", "zh-CN"]) {
		test(`${width}px ${language}: long hash keeps both actions visible and clickable`, async () => {
			if (!browser || !server) throw new Error("Fixture unavailable");
			const page = await browser.newPage();
			try {
				await page.setViewport({ width, height: 844 });
				await page.goto(`${server.url.origin}?lang=${language}`, { waitUntil: "networkidle0" });
				await page.waitForSelector(".nf-top-banner button");
				const geometry = await page.evaluate(() => {
					const banner = document.querySelector<HTMLElement>(".nf-top-banner");
					if (!banner) throw new Error("Missing banner");
					const bounds = banner.getBoundingClientRect();
					// Alert renders [body, CloseButton] inside its wrapper, so the close
					// button is the last button in document order. (`:last-of-type` is
					// per-parent and would select the refresh button instead.)
					const buttons = [...banner.querySelectorAll<HTMLElement>("button")];
					const closeRect = buttons[buttons.length - 1]?.getBoundingClientRect();
					if (!closeRect) throw new Error("Missing close button");
					return {
						overflow: banner.scrollWidth > banner.clientWidth,
						bannerCentreOffset: bounds.left + bounds.width / 2 - innerWidth / 2,
						buttonCount: buttons.length,
						// The close button is a flex item of the Alert's wrapper, so it
						// silently top-aligns unless it opts into cross-axis centring.
						closeCentreOffset:
							closeRect.top + closeRect.height / 2 - (bounds.top + bounds.height / 2),
						buttons: buttons.map((button) => {
							const rect = button.getBoundingClientRect();
							return (
								rect.width > 0 &&
								rect.left >= bounds.left &&
								rect.right <= bounds.right &&
								rect.left >= 0 &&
								rect.right <= innerWidth &&
								rect.top >= bounds.top &&
								rect.bottom <= bounds.bottom &&
								button.contains(
									document.elementFromPoint(rect.x + rect.width / 2, rect.y + rect.height / 2),
								)
							);
						}),
					};
				});
				expect(geometry.overflow).toBe(false);
				expect(Math.abs(geometry.bannerCentreOffset)).toBeLessThanOrEqual(1);
				expect(geometry.buttonCount).toBe(2);
				expect(Math.abs(geometry.closeCentreOffset)).toBeLessThanOrEqual(1);
				expect(geometry.buttons).toEqual([true, true]);
				const calls = () =>
					page.evaluate(() => ({
						refresh: Reflect.get(window, "refreshCalls"),
						dismiss: Reflect.get(window, "dismissCalls"),
					}));
				const [refreshButton, closeButton] = await page.$$(".nf-top-banner button");
				// Pins which button is which, so the centring assertion above cannot
				// drift onto the refresh button.
				await closeButton?.click();
				expect(await calls()).toEqual({ refresh: 0, dismiss: 1 });
				await refreshButton?.click();
				expect(await calls()).toEqual({ refresh: 1, dismiss: 1 });
			} finally {
				await page.close();
			}
		}, 15_000);
	}
}
