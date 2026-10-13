import { afterAll, beforeAll, expect, test } from "bun:test";
import { existsSync } from "node:fs";
import { basename } from "node:path";
import puppeteer, { type Browser } from "puppeteer-core";
import type * as Fixture from "./idle-action-width.browser-fixture";

// Actual row/buttons, production translations and CSS, real Chromium autosize.
// Composer persistence/providers are stubbed; the full application smoke owns them.
const executablePath =
	process.env.NF_TEST_CHROMIUM_PATH ??
	"/home/fulcrum/.cache/puppeteer/chrome/linux-146.0.7680.31/chrome-linux64/chrome";
let browser: Browser | undefined;
let server: ReturnType<typeof Bun.serve> | undefined;
type FixtureWindow = Window & { fixture: typeof Fixture };
beforeAll(async () => {
	if (!existsSync(executablePath)) throw new Error(`Chromium unavailable: ${executablePath}`);
	const bundle = await Bun.build({
		entrypoints: [new URL("./idle-action-width.browser-fixture.tsx", import.meta.url).pathname],
		target: "browser",
		format: "esm",
		define: { "process.env.NODE_ENV": '"production"' },
		plugins: [
			{
				name: "no-persistence-or-providers",
				setup(builder) {
					builder.onResolve({ filter: /\/NarratorComposer$/ }, (args) => ({
						path: args.path,
						namespace: "composer-fixture",
					}));
					builder.onResolve({ filter: /\/useInputMenu$/ }, (args) => ({
						path: args.path,
						namespace: "menu-fixture",
					}));
					builder.onLoad({ filter: /.*/, namespace: "menu-fixture" }, () => ({
						loader: "js",
						contents: `export const useInputMenu=()=>({menuProps:{},targetProps:{}});`,
					}));
					builder.onLoad({ filter: /.*/, namespace: "composer-fixture" }, () => ({
						loader: "jsx",
						contents: `import {Textarea} from '@mantine/core'; import {useState} from 'react'; export const NarratorComposer=props=>{const [value,setValue]=useState('');return <div style={{flex:1,minWidth:0}}><Textarea autosize minRows={1} maxRows={12} value={value} placeholder="Send a message... Shift+Enter for a new line" onChange={event=>{setValue(event.currentTarget.value);props.onTextFlagsChange(event.currentTarget.value.length>0);}}/></div>};`,
					}));
				},
			},
		],
	});
	if (!bundle.success) throw new Error(bundle.logs.join("\n"));
	const assets = new Map(bundle.outputs.map((output) => [`/${basename(output.path)}`, output]));
	const script = [...assets.keys()].find((path) => path.endsWith(".js"));
	const styles = [...assets.keys()].filter((path) => path.endsWith(".css"));
	if (!script || !styles.length) throw new Error("Production row/CSS bundle missing");
	server = Bun.serve({
		hostname: "127.0.0.1",
		port: 0,
		fetch(request) {
			const path = new URL(request.url).pathname;
			if (path === "/")
				return new Response(
					`<!doctype html><html><head><meta name="viewport" content="width=device-width,initial-scale=1">${styles.map((style) => `<link rel="stylesheet" href="${style}">`).join("")}<style>html,body{height:100%;margin:0}</style></head><body><div id="root"></div><script type="module">window.fixture=await import(${JSON.stringify(script)});</script></body></html>`,
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
for (const width of [1440, 390]) {
	for (const language of ["en", "zh-CN"]) {
		test(`${width}px ${language}: Send/Retry/Continue preserve intrinsic action and input widths`, async () => {
			if (!browser || !server) throw new Error("Fixture unavailable");
			const page = await browser.newPage();
			const errors: string[] = [];
			page.on("pageerror", (error) => errors.push(String(error)));
			try {
				await page.setViewport({ width, height: 844 });
				await page.goto(server.url.origin, { waitUntil: "networkidle0" });
				await page.waitForFunction(() => !!(window as unknown as FixtureWindow).fixture);
				await page.evaluate(
					(locale) => (window as unknown as FixtureWindow).fixture.start(locale),
					language,
				);
				const settle = () =>
					page.evaluate(
						() =>
							new Promise<void>((resolve) =>
								requestAnimationFrame(() => requestAnimationFrame(() => resolve())),
							),
					);
				await settle();
				const before = await page.evaluate(() =>
					(window as unknown as FixtureWindow).fixture.snapshot(),
				);
				expect(before.disabled).toBe(true);
				expect(before.sizers).toHaveLength(3);
				for (const sizer of before.sizers) {
					expect(sizer.hidden).toBe("true");
					expect(sizer.visibility).toBe("hidden");
					expect(sizer.text).toBe("");
				}
				for (const action of ["retry", "continue"] as const) {
					await page.evaluate(
						(value) => (window as unknown as FixtureWindow).fixture.setAction(value),
						action,
					);
					await settle();
					const next = await page.evaluate(() =>
						(window as unknown as FixtureWindow).fixture.snapshot(),
					);
					expect(next.disabled).toBe(false);
					expect(next.buttonText).not.toBe(before.buttonText);
					expect(next.buttonWidth).toBe(before.buttonWidth);
					expect(next.inputWidth).toBe(before.inputWidth);
					expect(next.inputHeight).toBe(before.inputHeight);
					expect(next.viewportHeight).toBe(before.viewportHeight);
					expect(next.labelWidth ?? 0).toBeGreaterThanOrEqual(next.actualWidth ?? 0);
					await page.click("button:has([data-stable-action-label])");
					expect(
						await page.evaluate(
							(key) => (window as unknown as FixtureWindow).fixture.snapshot().calls[key],
							action,
						),
					).toBe(1);
				}
				await page.evaluate(() => {
					const input = document.querySelector("textarea");
					if (!input) throw new Error("Missing textarea");
					const setter = Object.getOwnPropertyDescriptor(
						HTMLTextAreaElement.prototype,
						"value",
					)?.set;
					setter?.call(input, "one\ntwo\nthree\nfour");
					input.dispatchEvent(new Event("input", { bubbles: true }));
				});
				await settle();
				const grown = await page.evaluate(() =>
					(window as unknown as FixtureWindow).fixture.snapshot(),
				);
				expect(grown.inputHeight ?? 0).toBeGreaterThan(before.inputHeight ?? 0);
				expect(grown.inputWidth).toBe(before.inputWidth);
				expect(grown.calls.send).toBe(0);
				await page.evaluate(() => {
					const input = document.querySelector("textarea");
					if (!input) throw new Error("Missing textarea");
					Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")?.set?.call(
						input,
						"",
					);
					input.dispatchEvent(new Event("input", { bubbles: true }));
				});
				await settle();
				const cleared = await page.evaluate(() =>
					(window as unknown as FixtureWindow).fixture.snapshot(),
				);
				expect(cleared.inputHeight).toBe(before.inputHeight);
				expect(cleared.inputWidth).toBe(before.inputWidth);
				await page.evaluate(() => (window as unknown as FixtureWindow).fixture.setAction("queue"));
				await settle();
				expect(await page.$("[data-stable-action-label]")).toBeNull();
				expect(errors).toEqual([]);
			} finally {
				await page.close();
			}
		}, 15_000);
	}
}
