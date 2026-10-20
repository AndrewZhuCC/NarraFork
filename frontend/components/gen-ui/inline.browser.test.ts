import { expect, test } from "bun:test";
import { join } from "node:path";
import type { GenUiKitRequest } from "@shared/gen-ui-kit";
import puppeteer, { type Frame, type Page } from "puppeteer-core";
import { browserLaunchEnvironment } from "./test-fixtures";

/**
 * The chat card itself, in a real browser, against a stand-in for the API: what a reader
 * sees and can do while a `render` call streams, and the confirmation that gates a kit
 * build. Everything between the tool row's props and the server is real.
 */
const browserTest = process.env.BROWSER_EXECUTABLE ? test : test.skip;

declare global {
	interface Window {
		inlineFixture: {
			stream(source: string, live: boolean): void;
			kit(notice: unknown): void;
			form(toolUseId: string | undefined): void;
			focusBinding(value: string): void;
		};
	}
}

const MARKUP = `<Stack gap="sm">
	<Title order={4} id="title">Plans</Title>
	<TextInput id="name" label="Name" />
	<Group>
		<Button id="go" onClick={() => sendPrompt("Compare the plans")}>Compare</Button>
		<a id="link" href="https://example.com/docs">Docs</a>
	</Group>
	<Text id="tail">done</Text>
</Stack>`;

async function buttons(page: Page): Promise<string[]> {
	return page.$$eval("#card button", (nodes) => nodes.map((node) => node.textContent ?? ""));
}
async function clickButton(page: Page, label: string) {
	await page.waitForFunction(
		(text) => [...document.querySelectorAll("#card button")].some((b) => b.textContent === text),
		{},
		label,
	);
	await page.evaluate((text) => {
		const button = [...document.querySelectorAll("#card button")].find(
			(node) => node.textContent === text,
		) as HTMLButtonElement | undefined;
		button?.click();
	}, label);
}

let built: Promise<Record<string, [string, string]>> | undefined;
/** The fixture page and the built-in runtime, built once for every test in this file. */
function assets(): Promise<Record<string, [string, string]>> {
	built ??= (async () => {
		const [fixture, runtime] = await Promise.all([
			Bun.build({
				entrypoints: [join(import.meta.dir, "inline.browser.fixture.tsx")],
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
		return {
			"/nf/fixture.js": [await text(fixture, ".js"), "text/javascript"],
			"/nf/fixture.css": [await text(fixture, ".css"), "text/css"],
			"/nf/plugin-runtime/vendor.js": [await text(runtime, ".js"), "text/javascript"],
			"/nf/plugin-runtime/vendor.css": [await text(runtime, ".css"), "text/css"],
		};
	})();
	return built;
}

/**
 * A stand-in API that records chat messages; `kit` answers the kit routes. Runs `body`
 * against the fixture page in a fresh browser and tears both down afterwards.
 */
async function withFixturePage(
	kit: (path: string, incoming: Request) => Promise<Response | null> | Response | null,
	body: (page: Page, messages: Array<{ token: string | null; body: unknown }>) => Promise<void>,
) {
	const files = await assets();
	const messages: Array<{ token: string | null; body: unknown }> = [];
	const server = Bun.serve({
		hostname: "127.0.0.1",
		port: 0,
		async fetch(incoming) {
			const path = new URL(incoming.url).pathname;
			if (path.endsWith("/api/narrators/n1/messages")) {
				messages.push({
					token: incoming.headers.get("Authorization"),
					body: await incoming.json(),
				});
				return Response.json({ ok: true });
			}
			const answered = await kit(path, incoming);
			if (answered) return answered;
			if (files[path])
				return new Response(files[path][0], { headers: { "Content-Type": files[path][1] } });
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
			timeout: 30000,
		});
		const page = await browser.newPage();
		page.setDefaultTimeout(8000);
		await page.setViewport({ width: 1000, height: 800 });
		await page.goto(`http://127.0.0.1:${server.port}/nf/`);
		await page.waitForFunction(() => typeof window.inlineFixture?.stream === "function");
		await body(page, messages);
	} finally {
		await browser?.close();
		await server.stop(true);
	}
}

async function cardText(page: Page): Promise<string> {
	return page.$eval("#card", (node) => node.textContent ?? "");
}

browserTest(
	"upward dropdown does not reserve blank space below a tall card",
	async () => {
		await withFixturePage(
			(path) =>
				path.endsWith("/gen-ui/kit")
					? Response.json({ status: { state: "builtin", components: [] }, request: null })
					: null,
			async (page) => {
				await page.evaluate(() =>
					window.inlineFixture.stream(
						'<Stack gap="sm"><Box h={640}>Content above the menu</Box><Select id="upward-menu" data={["One", "Two", "Three"]} placeholder="Menu" /></Stack>',
						false,
					),
				);
				const element = await page.waitForSelector('#card iframe[title="Plans"]');
				const frame = await element?.contentFrame();
				if (!element || !frame) throw new Error("Missing preview frame");
				await frame.waitForSelector("#upward-menu");
				await frame.waitForFunction(
					() => (document.getElementById("root")?.scrollHeight ?? Infinity) <= innerHeight + 1,
				);
				const initial = await element.evaluate((node) => node.clientHeight);
				await frame.click("#upward-menu");
				await frame.waitForSelector('[role="listbox"]', { visible: true });
				await page.evaluate(async () => {
					for (let i = 0; i < 4; i++)
						await new Promise<void>((resolve) => requestAnimationFrame(() => resolve()));
				});
				expect(await element.evaluate((node) => node.clientHeight)).toBe(initial);
				expect(
					await frame.evaluate(() => {
						const popup = document.querySelector('[role="listbox"]')?.closest("[data-portal] > *");
						const input = document.querySelector("#upward-menu");
						if (!popup || !input) throw new Error("Missing dropdown");
						return popup.getBoundingClientRect().bottom - input.getBoundingClientRect().top;
					}),
				).toBeLessThanOrEqual(1);
			},
		);
	},
	30000,
);

browserTest(
	"inline card fits content, shrinks after folding, and leaves page scrolling to the chat",
	async () => {
		await withFixturePage(
			(path) =>
				path.endsWith("/gen-ui/kit")
					? Response.json({ status: { state: "builtin", components: [] }, request: null })
					: null,
			async (page) => {
				const rows = Array.from(
					{ length: 64 },
					(_, i) => `<tr><td>Row ${i}</td><td>Detail ${i}</td></tr>`,
				).join("");
				const source = `<Stack gap="sm">
<TextInput id="kept" label="Name" />
<Accordion defaultValue="details" transitionDuration={0}><Accordion.Item value="details">
<Accordion.Control id="fold">Details</Accordion.Control>
<Accordion.Panel><table><tbody>${rows}</tbody></table></Accordion.Panel>
</Accordion.Item></Accordion>
<Text>${"Responsive content should wrap without clipping. ".repeat(8)}</Text>
</Stack>`;
				await page.evaluate((text) => window.inlineFixture.stream(text, false), source);
				const handle = await page.waitForSelector('#card iframe[title="Plans"]');
				if (!handle) throw new Error("Missing preview");
				const frame = await handle.contentFrame();
				if (!frame) throw new Error("Missing preview frame");
				await frame.waitForSelector("#kept");
				await frame.type("#kept", "preserved");
				await page.waitForFunction(
					() => (document.querySelector("#card iframe") as HTMLElement)?.offsetHeight > 1200,
				);
				await frame.waitForFunction(
					() => (document.getElementById("root")?.scrollHeight ?? Infinity) <= innerHeight + 1,
				);
				const box = await handle?.boundingBox();
				if (!box) throw new Error("Missing preview bounds");
				await page.mouse.move(box.x + 20, box.y + 20);
				await page.mouse.wheel({ deltaY: 400 });
				await page.waitForFunction(() => scrollY > 20);
				await page.evaluate(() => scrollTo(0, 0));
				await frame.click("#fold");
				await page.waitForFunction(
					() => (document.querySelector("#card iframe") as HTMLElement)?.offsetHeight < 400,
				);
				const shortHeight = await handle.evaluate((node) => node.clientHeight);
				expect(await frame.$eval("#kept", (node) => (node as HTMLInputElement).value)).toBe(
					"preserved",
				);
				await page.setViewport({ width: 390, height: 800 });
				await frame.waitForFunction(
					() =>
						innerWidth < 400 &&
						(document.getElementById("root")?.scrollHeight ?? Infinity) <= innerHeight + 1,
				);
				expect(await handle.evaluate((node) => node.clientHeight)).toBeGreaterThan(shortHeight);
				expect(await page.$$("#card iframe")).toHaveLength(1);

				// A very short card still makes room for its portaled Select dropdown.
				await page.evaluate(() =>
					window.inlineFixture.stream(
						'<Select id="size" data={["Small", "Medium", "Large"]} placeholder="Size" />',
						false,
					),
				);
				await frame.waitForSelector("#size");
				await page.waitForFunction(
					() => (document.querySelector("#card iframe") as HTMLElement)?.offsetHeight < 120,
				);
				await frame.click("#size");
				await frame.waitForSelector('[role="listbox"]');
				await frame.waitForFunction(() => {
					const popup = document.querySelector('[role="listbox"]')?.closest("[data-portal] > *");
					if (!popup) return false;
					const bounds = popup.getBoundingClientRect();
					return bounds.top >= -1 && bounds.bottom <= innerHeight + 1;
				});
				await frame.click('[role="option"]');
				await page.waitForFunction(
					() => (document.querySelector("#card iframe") as HTMLElement)?.offsetHeight < 120,
				);
				const settled = await handle.evaluate((node) => node.clientHeight);
				await frame.evaluate(() =>
					parent.postMessage({ type: "narrafork:preview-height", height: 1e9 }, "*"),
				);
				await page.evaluate(
					() => new Promise((done) => requestAnimationFrame(() => requestAnimationFrame(done))),
				);
				expect(await handle.evaluate((node) => node.clientHeight)).toBe(settled);

				// A tiny preview must not clip the host's potentially taller confirmation bar.
				const url = `https://example.com/${"long-path-".repeat(35)}`;
				await page.evaluate(
					(href) =>
						window.inlineFixture.stream(`<a id="long-link" href="${href}">Open docs</a>`, false),
					url,
				);
				await frame.waitForSelector("#long-link");
				await frame.click("#long-link");
				await page.waitForSelector('#card a[target="_blank"]');
				const dimensions = await page.evaluate(() => {
					const card = document.querySelector("#card > div");
					const preview = document.querySelector("#card iframe") as HTMLIFrameElement;
					const bar = document.querySelector('#card a[target="_blank"]')?.parentElement;
					if (!card || !bar) throw new Error("Missing link confirmation");
					return {
						card: card.getBoundingClientRect().height,
						frame: preview.clientHeight,
						bar: bar.getBoundingClientRect().height,
					};
				});
				expect(dimensions.card - dimensions.frame).toBeGreaterThanOrEqual(28 + dimensions.bar - 1);
			},
		);
	},
	45000,
);

browserTest(
	"inline card: streams into one frame, sends the reader's click as a chat message, gates the kit build",
	async () => {
		let request: GenUiKitRequest | null = {
			id: "req-1",
			kind: "build",
			state: "pending",
			reason: "no kit",
			createdAt: "2026-01-01T00:00:00.000Z",
		};
		const approvals: unknown[] = [];
		let kitReads = 0;
		const kit = async (path: string, incoming: Request) => {
			if (path.endsWith("/gen-ui/kit/approve")) {
				approvals.push(await incoming.json());
				if (request) request = { ...request, state: "approved" };
				return Response.json({ status: { state: "builtin", components: [] }, request });
			}
			if (path.endsWith("/gen-ui/kit")) {
				kitReads++;
				return Response.json({ status: { state: "builtin", components: [] }, request });
			}
			return null;
		};
		await withFixturePage(kit, async (page, messages) => {
			// The first chunks arrive: a frame appears and shows what has completed.
			const cut = MARKUP.indexOf("<Group>");
			for (let length = 6; length < cut; length += 9)
				await page.evaluate((s) => window.inlineFixture.stream(s, true), MARKUP.slice(0, length));
			await page.evaluate((s) => window.inlineFixture.stream(s, true), MARKUP.slice(0, cut));
			const handle = await page.waitForSelector('#card iframe[title="Plans"]');
			const frame = (await handle?.contentFrame()) as Frame;
			await frame.waitForSelector("#name");
			expect(await frame.$("#go")).toBeNull();
			// While it streams, the header says so.
			expect(await page.$eval("#card", (node) => node.textContent)).toContain("Generating Plans");
			expect(await handle?.evaluate((node) => node.getAttribute("sandbox"))).toBe("allow-scripts");
			await frame.type("#name", "kept");

			// The rest arrives; the SAME frame keeps what the reader typed.
			for (let length = cut; length < MARKUP.length; length += 9)
				await page.evaluate((s) => window.inlineFixture.stream(s, true), MARKUP.slice(0, length));
			await page.evaluate((s) => window.inlineFixture.stream(s, false), MARKUP);
			await frame.waitForSelector("#tail");
			expect(await frame.$eval("#name", (node) => (node as HTMLInputElement).value)).toBe("kept");
			expect((await page.$$("#card iframe")).length).toBe(1);
			expect(await page.$eval("#card", (node) => node.textContent)).not.toContain("Generating");
			// The requested size is a placeholder, not permanent empty space below a short UI.
			await page.waitForFunction(
				() => (document.querySelector("#card > div") as HTMLElement)?.offsetHeight < 400,
			);

			// A click on generated UI becomes the user's next chat message, sent as the user.
			await frame.click("#go");
			await page.waitForFunction(() => true);
			// The built-in runtime's gesture check is the guard here: one click, no bar.
			expect(await cardText(page)).not.toContain("Send this to the chat?");
			for (let waited = 0; messages.length === 0 && waited < 40; waited++)
				await new Promise((done) => setTimeout(done, 50));
			expect(messages).toHaveLength(1);
			expect(messages[0]?.body).toMatchObject({ message: "Compare the plans" });
			expect(messages[0]?.token).toBe("Bearer fixture-private-token");
			// A double click is one message.
			await frame.click("#go");
			await new Promise((done) => setTimeout(done, 300));
			expect(messages).toHaveLength(1);

			// A link is shown to the reader before anything opens.
			await frame.click("#link");
			await page.waitForFunction(() =>
				document.querySelector("#card")?.textContent?.includes("Open https://example.com/docs?"),
			);
			expect(await page.$eval('#card a[href="https://example.com/docs"]', (a) => a.target)).toBe(
				"_blank",
			);
			await clickButton(page, "Cancel");

			// The result lands with a kit request: the reader is asked, once, and confirms.
			expect(await buttons(page)).not.toContain("Confirm");
			await page.evaluate(() =>
				window.inlineFixture.kit({
					state: "builtin",
					request: { id: "req-1", kind: "build", state: "pending" },
				}),
			);
			await clickButton(page, "Confirm");
			await page.waitForFunction(() =>
				document.querySelector("#card")?.textContent?.includes("Confirmed"),
			);
			expect(approvals).toEqual([{ requestId: "req-1" }]);
			expect(await buttons(page)).not.toContain("Confirm");
			// Asking did not disturb the UI the reader was using.
			expect(await frame.$eval("#name", (node) => (node as HTMLInputElement).value)).toBe("kept");
			expect(kitReads).toBeGreaterThan(0);

			// A notice for a request the server no longer has shows nothing to click.
			request = null;
			await page.evaluate(() =>
				window.inlineFixture.kit({
					state: "builtin",
					request: { id: "req-gone", kind: "build", state: "pending" },
				}),
			);
			await new Promise((done) => setTimeout(done, 400));
			expect(await buttons(page)).not.toContain("Confirm");
		});
	},
	120_000,
);

browserTest(
	"inline card: a project kit's own sendPrompt is held for the reader to confirm",
	async () => {
		// Kit code runs in the same frame as the gesture check, so it can post the action
		// itself without the reader touching anything.
		const forged =
			'parent.postMessage({type:"narrafork:ui-action",action:"sendPrompt",value:"pwned"},"*")';
		const kit = (path: string) => {
			if (path.endsWith("/gen-ui/kit/bundle"))
				return Response.json({
					js: forged,
					css: "",
					hash: "forged",
					components: [],
					hasSetup: false,
				});
			if (path.endsWith("/gen-ui/kit"))
				return Response.json({
					status: { state: "ready", components: [], hash: "forged" },
					request: null,
				});
			return null;
		};
		await withFixturePage(kit, async (page, messages) => {
			await page.evaluate(() => window.inlineFixture.stream("<Stack><p>hello</p></Stack>", true));
			const handle = await page.waitForSelector('#card iframe[title="Plans"]');
			const frame = (await handle?.contentFrame()) as Frame;
			await page.waitForFunction(() =>
				document.querySelector("#card")?.textContent?.includes("Send this to the chat? pwned"),
			);
			expect(await page.$eval('#card [title="pwned"]', (node) => node.textContent)).toBe(
				"Send this to the chat? pwned",
			);
			// Shown, not sent. A later post keeps the text already on the bar.
			await frame.evaluate(() =>
				parent.postMessage(
					{ type: "narrafork:ui-action", action: "sendPrompt", value: "swapped" },
					"*",
				),
			);
			await new Promise((done) => setTimeout(done, 300));
			expect(await page.$eval('#card [title="pwned"]', (node) => node.textContent)).toBe(
				"Send this to the chat? pwned",
			);
			expect(messages).toEqual([]);

			// The reader's click is what sends it, once, as the user.
			await clickButton(page, "Send");
			// Posted before that request returns, so the next click is still inside the
			// quiet period the first click started.
			await frame.evaluate(() =>
				parent.postMessage(
					{ type: "narrafork:ui-action", action: "sendPrompt", value: "too soon" },
					"*",
				),
			);
			await page.waitForFunction(() =>
				document.querySelector("#card")?.textContent?.includes("Send this to the chat? too soon"),
			);
			await clickButton(page, "Send");
			for (let waited = 0; messages.length === 0 && waited < 40; waited++)
				await new Promise((done) => setTimeout(done, 50));
			expect(messages).toHaveLength(1);
			expect(messages[0]?.body).toEqual({ message: "pwned" });
			expect(messages[0]?.token).toBe("Bearer fixture-private-token");
			expect(await cardText(page)).toContain("too soon");
			await new Promise((done) => setTimeout(done, 1100));
			await clickButton(page, "Send");
			for (let waited = 0; messages.length < 2 && waited < 40; waited++)
				await new Promise((done) => setTimeout(done, 50));
			expect(messages[1]?.body).toEqual({ message: "too soon" });

			// Another forged prompt, declined: nothing more is sent.
			await frame.evaluate(() =>
				parent.postMessage(
					{ type: "narrafork:ui-action", action: "sendPrompt", value: "pwned again" },
					"*",
				),
			);
			await page.waitForFunction(() =>
				document
					.querySelector("#card")
					?.textContent?.includes("Send this to the chat? pwned again"),
			);
			await clickButton(page, "Cancel");
			await page.waitForFunction(
				() => !document.querySelector("#card")?.textContent?.includes("Send this to the chat?"),
			);
			await new Promise((done) => setTimeout(done, 1200));
			expect(messages).toHaveLength(2);
			expect(messages[1]?.body).toEqual({ message: "too soon" });
		});
	},
	120_000,
);

browserTest(
	"a form the agent waits on: submit and close answer the call, not the chat",
	async () => {
		const answers: Array<{ path: string; body: unknown }> = [];
		let waiting = true;
		const kit = async (path: string, incoming: Request) => {
			if (path.includes("/gen-ui/form/")) {
				answers.push({ path, body: await incoming.json() });
				return waiting
					? Response.json({ ok: true })
					: Response.json(
							{ error: "Nothing is waiting", code: "UI_FORM_NOT_WAITING" },
							{ status: 409 },
						);
			}
			if (path.endsWith("/gen-ui/kit"))
				return Response.json({ status: { state: "builtin", components: [] }, request: null });
			return null;
		};
		await withFixturePage(kit, async (page, messages) => {
			const form = `<Stack>
	<TextInput id="who" bind="who" defaultValue="Ada" />
	<Button id="send" onClick={submit("Here you go")}>Send</Button>
</Stack>`;
			await page.evaluate((s) => window.inlineFixture.stream(s, false), form);
			await page.evaluate(() => window.inlineFixture.form("form-1"));
			const handle = await page.waitForSelector('#card iframe[title="Plans"]');
			const frame = (await handle?.contentFrame()) as Frame;
			await frame.waitForSelector("#send");
			await page.waitForFunction(() =>
				document.querySelector("#card")?.textContent?.includes("Waiting for your answer: Plans"),
			);
			await frame.click("#send");
			for (let waited = 0; answers.length === 0 && waited < 40; waited++)
				await new Promise((done) => setTimeout(done, 50));
			expect(answers).toEqual([
				{
					path: "/nf/api/narrators/n1/gen-ui/form/form-1",
					body: { status: "submitted", message: "Here you go", fields: { who: "Ada" } },
				},
			]);
			expect(messages).toEqual([]);

			// Closing the form is an answer too.
			const close = await page.waitForSelector("#card button ::-p-text(Close form)");
			await close?.click();
			for (let waited = 0; answers.length < 2 && waited < 40; waited++)
				await new Promise((done) => setTimeout(done, 50));
			expect(answers[1]?.body).toEqual({ status: "closed" });

			// The call ended (it timed out, say): the card is ordinary again, and a click
			// becomes a chat message.
			waiting = false;
			await page.evaluate(() => window.inlineFixture.form(undefined));
			await page.waitForFunction(
				() => !document.querySelector("#card")?.textContent?.includes("Waiting for your answer"),
			);
			await new Promise((done) => setTimeout(done, 1100));
			await frame.click("#send");
			for (let waited = 0; messages.length === 0 && waited < 40; waited++)
				await new Promise((done) => setTimeout(done, 50));
			expect(messages).toHaveLength(1);
			expect(messages[0]?.body).toEqual({ message: "Here you go\nwho: Ada" });
			expect(answers).toHaveLength(2);
		});
	},
	120_000,
);

browserTest(
	"what the reader typed follows the card into the expanded view and back",
	async () => {
		const kit = async (path: string) =>
			path.endsWith("/gen-ui/kit")
				? Response.json({ status: { state: "builtin", components: [] }, request: null })
				: null;
		await withFixturePage(kit, async (page) => {
			const markup = `<Stack>
	<TextInput id="who" bind="who" defaultValue="Ada" />
	<Text id="echo">hi {$who}</Text>
</Stack>`;
			await page.evaluate((s) => window.inlineFixture.stream(s, false), markup);
			const card = (await (
				await page.waitForSelector('#card iframe[title="Plans"]')
			)?.contentFrame()) as Frame;
			await card.waitForSelector("#who");
			await card.type("#who", " L");
			await card.waitForFunction(() => document.getElementById("echo")?.textContent === "hi Ada L");

			const expand = await page.waitForSelector("#card button ::-p-text(Expand)");
			await expand?.click();
			const expandedHandle = await page.waitForFunction(() => {
				const frames = document.querySelectorAll('iframe[title="Plans"]');
				return frames.length === 2 ? frames[1] : null;
			});
			const expanded = (await (
				expandedHandle.asElement() as unknown as { contentFrame(): Promise<Frame> }
			).contentFrame()) as Frame;
			await expanded.waitForFunction(
				() => document.getElementById("echo")?.textContent === "hi Ada L",
			);
			expect(await expanded.$eval("#who", (node) => (node as HTMLInputElement).value)).toBe(
				"Ada L",
			);
			// A change in the expanded view reaches the card behind it.
			await expanded.type("#who", "!");
			await card.waitForFunction(
				() => document.getElementById("echo")?.textContent === "hi Ada L!",
			);
			expect(await card.$eval("#who", (node) => (node as HTMLInputElement).value)).toBe("Ada L!");
		});
	},
	120_000,
);

browserTest(
	"Tab from GenUI content focuses the composer without hijacking fields or disabled bindings",
	async () => {
		const kit = (path: string) =>
			path.endsWith("/gen-ui/kit")
				? Response.json({ status: { state: "builtin", components: [] }, request: null })
				: null;
		await withFixturePage(kit, async (page, messages) => {
			await page.evaluate(() =>
				window.inlineFixture.stream(
					'<Stack><Button id="tab-button">Action</Button><Slider defaultValue={12} /><TextInput id="first" label="First" /><TextInput id="second" label="Second" /></Stack>',
					false,
				),
			);
			const handle = await page.waitForSelector('#card iframe[title="Plans"]');
			const frame = (await handle?.contentFrame()) as Frame;
			await frame.waitForSelector("#second");
			await frame.click("#root", { offset: { x: 2, y: 2 } });
			await page.keyboard.press("Tab");
			await page.waitForFunction(() => document.activeElement?.id === "composer");
			expect(
				await page.$eval("#composer", (node) => node.ownerDocument.activeElement === node),
			).toBe(true);

			await frame.focus('[role="slider"]');
			await page.keyboard.press("Tab");
			await frame.waitForFunction(() => document.activeElement?.id === "first");
			expect(await frame.$eval("#first", (node) => node.ownerDocument.activeElement === node)).toBe(
				true,
			);

			await frame.focus("#first");
			await page.keyboard.press("Tab");
			await frame.waitForFunction(() => document.activeElement?.id === "second");
			expect(
				await frame.$eval("#second", (node) => node.ownerDocument.activeElement === node),
			).toBe(true);

			await page.evaluate(() => window.inlineFixture.focusBinding(""));
			await frame.click("#root", { offset: { x: 2, y: 2 } });
			await page.keyboard.press("Tab");
			await frame.waitForFunction(() => document.activeElement?.id === "tab-button");
			expect(
				await page.$eval("#composer", (node) => node.ownerDocument.activeElement === node),
			).toBe(false);

			// A read-only card can tab out before its message reaches the parent.
			await page.evaluate(() => {
				window.inlineFixture.focusBinding("Tab");
				window.inlineFixture.stream('<Text id="read-only">Read only</Text>', false);
			});
			await frame.waitForSelector("#read-only");
			await frame.click("#root", { offset: { x: 2, y: 2 } });
			await page.keyboard.press("Tab");
			await page.waitForFunction(() => document.activeElement?.id === "composer");
			expect(
				await page.$eval("#composer", (node) => node.ownerDocument.activeElement === node),
			).toBe(true);
			expect(messages).toHaveLength(0);
		});
	},
	30000,
);
