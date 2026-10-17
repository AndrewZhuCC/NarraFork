import { expect, test } from "bun:test";
import { mkdir, mkdtemp, realpath, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { LocalBackend } from "@server/lib/agent/execution/local-backend";
import { compilePreview } from "@server/services/gen-ui/compiler";
import { collectSources } from "@server/services/gen-ui/files";
import puppeteer, { type Frame, type Page } from "puppeteer-core";
import { browserLaunchEnvironment } from "./test-fixtures";

/**
 * A shown app runs in its chat card: the newest one starts by itself, an older one waits
 * for Run, and whatever the app sends to the conversation waits for the reader's OK.
 */
const browserTest = process.env.BROWSER_EXECUTABLE ? test : test.skip;

const APP = `import { Button } from "@mantine/core";
export default function App() {
	return <div>
		<p id="ready">app</p>
		<Button id="ask" onClick={() => genUI.sendPrompt("Ship it")}>Ask</Button>
		<Button id="submit" onClick={() => genUI.submit({ size: "L", count: 2 }, "Chosen")}>Submit</Button>
	</div>;
}`;

async function appFrame(page: Page): Promise<Frame> {
	const element = await page.waitForSelector('iframe[title="Demo preview"]');
	const frame = await element?.contentFrame();
	if (!frame) throw new Error("Missing app frame");
	await frame.waitForSelector("#ask", { timeout: 8000 });
	return frame;
}

browserTest(
	"a shown app runs in its card, and what it sends waits for the reader",
	async () => {
		const [fixture, runtime] = await Promise.all([
			Bun.build({
				entrypoints: [join(import.meta.dir, "preview.browser.fixture.tsx")],
				target: "browser",
				define: { "process.env.NODE_ENV": '"production"' },
			}),
			Bun.build({
				entrypoints: [join(import.meta.dir, "../../plugin-runtime/vendor.ts")],
				target: "browser",
				format: "iife",
				minify: true,
				define: { "process.env.NODE_ENV": '"production"' },
			}),
		]);
		if (!fixture.success || !runtime.success)
			throw new AggregateError([...fixture.logs, ...runtime.logs], "Browser fixture build failed");
		const text = async (build: typeof fixture, extension: string) => {
			const output = build.outputs.find((file) => file.path.endsWith(extension));
			if (!output) throw new Error(`Missing ${extension} output`);
			return output.text();
		};
		const [script, css, runtimeJs, runtimeCss] = await Promise.all([
			text(fixture, ".js"),
			text(fixture, ".css"),
			text(runtime, ".js"),
			text(runtime, ".css"),
		]);
		const root = await realpath(await mkdtemp(join(tmpdir(), "nf-app-ui-browser-")));
		const directory = join(root, ".narrafork", "gen_ui", "demo-123");
		await mkdir(directory, { recursive: true });
		await writeFile(join(directory, "App.tsx"), APP);
		const backend = new LocalBackend();
		let builds = 0;
		const messages: unknown[] = [];
		const server = Bun.serve({
			hostname: "127.0.0.1",
			port: 0,
			async fetch(request) {
				const path = new URL(request.url).pathname;
				if (path.endsWith("/messages")) {
					messages.push(await request.json());
					return Response.json({ ok: true });
				}
				if (path.includes("/gen-ui/preview/")) {
					builds++;
					const sources = await collectSources(backend, directory, async (p) => p, request.signal);
					const bundle = await compilePreview(sources, request.signal);
					return Response.json({
						...bundle,
						directory,
						entry: "App.tsx",
						files: sources.map((source) => source.path),
					});
				}
				const assets: Record<string, [string, string]> = {
					"/nf/fixture.js": [script, "text/javascript"],
					"/nf/fixture.css": [css, "text/css"],
					"/nf/plugin-runtime/vendor.js": [runtimeJs, "text/javascript"],
					"/nf/plugin-runtime/vendor.css": [runtimeCss, "text/css"],
				};
				if (assets[path])
					return new Response(assets[path][0], { headers: { "Content-Type": assets[path][1] } });
				return new Response(
					'<!doctype html><html><head><base href="/nf/"><link rel="stylesheet" href="fixture.css"></head><body><div id="root"></div><script type="module" src="fixture.js"></script></body></html>',
					{ headers: { "Content-Type": "text/html" } },
				);
			},
		});
		let browser: Awaited<ReturnType<typeof puppeteer.launch>> | undefined;
		try {
			browser = await puppeteer.launch({
				executablePath: process.env.BROWSER_EXECUTABLE,
				headless: true,
				args: ["--no-sandbox", "--no-first-run"],
				env: browserLaunchEnvironment(),
				timeout: 15000,
			});
			const page = await browser.newPage();
			page.setDefaultTimeout(8000);
			await page.setViewport({ width: 1100, height: 900 });
			await page.goto(`http://127.0.0.1:${server.port}/nf/`, { waitUntil: "networkidle0" });

			// Shown an hour ago: it waits for Run.
			await page.evaluate(() => {
				window.previewFixture.mount(false);
				window.previewFixture.show(new Date(Date.now() - 3_600_000).toISOString());
				window.previewFixture.mount(true);
			});
			await page.waitForFunction(() => document.body.textContent?.includes("Not running"));
			expect(await page.$("iframe")).toBeNull();
			expect(builds).toBe(0);
			await page.evaluate(() =>
				[...document.querySelectorAll("button")].find((b) => b.textContent === "Run")?.click(),
			);
			await appFrame(page);
			expect(builds).toBe(1);

			// Just shown: it starts by itself.
			await page.evaluate(() => {
				window.previewFixture.mount(false);
				window.previewFixture.show(new Date().toISOString());
			});
			await page.evaluate(() => window.previewFixture.mount(true));
			const frame = await appFrame(page);
			expect(builds).toBe(2);

			// The app asks to send: nothing goes out until the reader confirms.
			await frame.click("#ask");
			await page.waitForFunction(() =>
				document.body.textContent?.includes("Send this to the chat? Ship it"),
			);
			expect(messages).toEqual([]);
			await page.evaluate(() =>
				[...document.querySelectorAll("button")].find((b) => b.textContent === "Send")?.click(),
			);
			for (let waited = 0; messages.length === 0 && waited < 40; waited++)
				await new Promise((done) => setTimeout(done, 50));
			expect(messages).toEqual([{ message: "Ship it" }]);

			await frame.click("#submit");
			await page.waitForFunction(() => document.body.textContent?.includes("size: L"));
			await page.evaluate(() =>
				[...document.querySelectorAll("button")].find((b) => b.textContent === "Cancel")?.click(),
			);
			await new Promise((done) => setTimeout(done, 200));
			expect(messages).toHaveLength(1);
			await frame.click("#submit");
			await page.waitForFunction(() => document.body.textContent?.includes("size: L"));
			await page.evaluate(() =>
				[...document.querySelectorAll("button")].find((b) => b.textContent === "Send")?.click(),
			);
			for (let waited = 0; messages.length < 2 && waited < 40; waited++)
				await new Promise((done) => setTimeout(done, 50));
			expect(messages[1]).toEqual({ message: "Chosen\nsize: L\ncount: 2" });
		} finally {
			await browser?.close();
			await server.stop(true);
			await rm(root, { recursive: true, force: true });
		}
	},
	120_000,
);
