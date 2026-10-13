import { afterEach, beforeEach, expect, spyOn, test } from "bun:test";
import { MD_HEADING_SLUG_ATTR } from "@frontend/lib/markdown-anchor-scroll";
import { type OpenUIError, Renderer } from "@openuidev/react-lang";
import { InlineMarkdownRenderer, ThemeProvider } from "@openuidev/react-ui";
import type { FileTarget } from "@shared/file-reference";
import { isLocalFileHref } from "@shared/markdown-file-path";
import { parseHTML } from "linkedom";
import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import {
	FileReferenceScopeProvider,
	type FileReferenceScopeValue,
} from "../composer/FileReferenceScope";
import { narratorOpenUIChatLibrary } from "./openui-library";
import { OpenUIMarkdownProvider } from "./openui-markdown";

const globals = [
	"window",
	"document",
	"navigator",
	"HTMLElement",
	"Element",
	"Node",
	"Text",
	"DOMParser",
	"MutationObserver",
	"ResizeObserver",
	"localStorage",
	"IS_REACT_ACT_ENVIRONMENT",
] as const;
const context = { deviceId: "CapturedDevice", cwd: "/repo" };
let saved: Map<string, PropertyDescriptor | undefined>;
let container: HTMLDivElement;
let root: Root;
let opened: FileTarget[];
let requests: Array<{ url: URL; options?: RequestInit }>;
let response: () => Response;
let created: ReturnType<typeof spyOn<typeof URL, "createObjectURL">>;
let revoked: ReturnType<typeof spyOn<typeof URL, "revokeObjectURL">>;
const originalFetch = globalThis.fetch;

beforeEach(() => {
	saved = new Map(globals.map((name) => [name, Object.getOwnPropertyDescriptor(globalThis, name)]));
	const { window } = parseHTML("<!doctype html><html><body></body></html>");
	for (const name of globals) {
		const value =
			name === "IS_REACT_ACT_ENVIRONMENT"
				? true
				: name === "localStorage"
					? { getItem: () => "test-token" }
					: window[name as keyof typeof window];
		Object.defineProperty(globalThis, name, { configurable: true, writable: true, value });
	}
	// Layout observation is irrelevant to these navigation/markup checks.
	globalThis.ResizeObserver = class {
		observe() {}
		unobserve() {}
		disconnect() {}
	};
	container = document.createElement("div");
	document.body.appendChild(container);
	root = createRoot(container);
	opened = [];
	requests = [];
	response = () =>
		new Response(new Uint8Array([1, 2, 3]), { headers: { "content-type": "image/png" } });
	globalThis.fetch = (async (input: string | URL | Request, options?: RequestInit) => {
		requests.push({ url: new URL(String(input), "http://test"), options });
		return response();
	}) as typeof fetch;
	created = spyOn(URL, "createObjectURL").mockImplementation(() => "blob:openui-test");
	revoked = spyOn(URL, "revokeObjectURL").mockImplementation(() => {});
});

afterEach(() => {
	act(() => root.unmount());
	container.remove();
	created.mockRestore();
	revoked.mockRestore();
	globalThis.fetch = originalFetch;
	for (const [name, descriptor] of saved) {
		if (descriptor) Object.defineProperty(globalThis, name, descriptor);
		else Reflect.deleteProperty(globalThis, name);
	}
});

async function render(
	content: string | ReactNode,
	scope: FileReferenceScopeValue = { context, openFile: (target) => opened.push(target) },
) {
	const errors: OpenUIError[] = [];
	await act(async () => {
		root.render(
			<ThemeProvider mode="light">
				<FileReferenceScopeProvider value={scope}>
					<OpenUIMarkdownProvider>
						{typeof content === "string" ? (
							<Renderer
								response={content}
								isStreaming={false}
								library={narratorOpenUIChatLibrary}
								onError={(value) => errors.push(...value)}
								publishObservability={false}
							/>
						) : (
							content
						)}
					</OpenUIMarkdownProvider>
				</FileReferenceScopeProvider>
			</ThemeProvider>,
		);
	});
	expect(errors).toEqual([]);
}

function click(anchor: Element) {
	const event = new window.Event("click", { bubbles: true, cancelable: true });
	Object.defineProperty(event, "button", { value: 0 });
	act(() => anchor.dispatchEvent(event));
	expect(event.defaultPrevented).toBe(true);
}

for (const component of ["TextContent", "MarkDownRenderer", "Steps"] as const) {
	test(`${component} opens file links with captured scope, explicit devices and selections`, async () => {
		const text = [
			"[relative](src/a.ts#L2-L3)",
			"[absolute](/tmp/b.ts#L4)",
			"[remote](nf-file://open?device=OtherDevice&path=%2Fapp%2Fc.ts#L5)",
			"[windows](C:/repo/d.ts#L6)",
			"[file](file:///tmp/e.ts#L7)",
			"[web](https://example.com/docs)",
		].join("\n\n");
		const body =
			component === "Steps"
				? `Steps([StepsItem("Inspect", ${JSON.stringify(text)})])`
				: `${component}(${JSON.stringify(text)})`;
		await render(`root = Card([${body}])`);
		const links = Array.from(container.querySelectorAll<HTMLAnchorElement>("a[href]"));
		const files = links.filter((link) => isLocalFileHref(link.getAttribute("href") ?? undefined));
		expect(files).toHaveLength(5);
		for (const link of files) {
			expect(link.getAttribute("target")).toBeNull();
			click(link);
		}
		expect(opened.map(({ deviceId, path }) => ({ deviceId, path }))).toEqual([
			{ deviceId: "CapturedDevice", path: "/repo/src/a.ts" },
			{ deviceId: "CapturedDevice", path: "/tmp/b.ts" },
			{ deviceId: "OtherDevice", path: "/app/c.ts" },
			{ deviceId: "CapturedDevice", path: "C:/repo/d.ts" },
			{ deviceId: "CapturedDevice", path: "/tmp/e.ts" },
		]);
		expect(opened.map(({ selection }) => selection)).toEqual(
			[
				[2, 4],
				[4, 5],
				[5, 6],
				[6, 7],
				[7, 8],
			].map(([start, end]) => ({
				startLineNumber: start,
				startColumn: 1,
				endLineNumber: end,
				endColumn: 1,
			})),
		);
		const web = links.find((link) => link.getAttribute("href") === "https://example.com/docs");
		expect(web?.getAttribute("target")).toBe("_blank");
		expect(web?.getAttribute("rel")).toBe("noopener noreferrer");
	});
}

test("missing historical context disables inferred file links without guessing a web route", async () => {
	await render('root = Card([TextContent("[file](src/a.ts#L3)")])', {
		context: null,
		openFile: (target) => opened.push(target),
	});
	expect(container.querySelector("a")).toBeNull();
	expect(container.textContent).toContain("file");
	expect(opened).toEqual([]);
});

test("Markdown images use authenticated scoped previews and never fall back after denial", async () => {
	const text = "![local](images/中文%20图.png)\n\n![external](https://example.com/image.png)";
	await render(`root = Card([TextContent(${JSON.stringify(text)})])`, {
		narratorId: "n/1",
		context,
	});
	expect(requests).toHaveLength(1);
	expect(requests[0].url.pathname).toBe("/api/narrators/n%2F1/file-references/image-preview");
	expect(requests[0].url.searchParams.get("deviceId")).toBe("CapturedDevice");
	expect(requests[0].url.searchParams.get("path")).toBe("/repo/images/中文 图.png");
	expect(new Headers(requests[0].options?.headers).get("authorization")).toBe("Bearer test-token");
	expect(
		Array.from(container.querySelectorAll("img"), (image) => image.getAttribute("src")),
	).toEqual(["blob:openui-test", "https://example.com/image.png"]);
	response = () => Response.json({ error: "denied" }, { status: 403 });
	await render('root = Card([TextContent("![denied](images/denied.png)")])', {
		narratorId: "n/1",
		context,
	});
	expect(container.querySelector("img")).toBeNull();
	expect(container.textContent).toContain("denied");
	expect(requests).toHaveLength(2);
	expect(revoked).toHaveBeenCalledWith("blob:openui-test");
});

test("anchors resolve across text components in this block, not another message", async () => {
	const other = document.createElement("h2");
	other.setAttribute(MD_HEADING_SLUG_ATTR, "details");
	document.body.prepend(other);
	let focused = "";
	Object.defineProperty(other, "focus", {
		value: () => {
			focused = "other";
		},
	});
	await render('root = Card([TextContent("[Jump](#details)"), TextContent("## Details")])');
	const heading = container.querySelector(`[${MD_HEADING_SLUG_ATTR}="details"]`);
	expect(heading).not.toBeNull();
	Object.defineProperty(heading, "focus", {
		value: () => {
			focused = "current";
		},
	});
	const anchor = container.querySelector("a[href='#details']");
	expect(anchor).not.toBeNull();
	expect(anchor?.getAttribute("target")).toBeNull();
	click(anchor as Element);
	expect(focused).toBe("current");
	other.remove();
});

test("inline Markdown keeps flattened headings while sharing file navigation", async () => {
	await render(<InlineMarkdownRenderer content="## **See** [file](src/a.ts#L2)" />);
	expect(container.querySelector("h2")).toBeNull();
	expect(container.querySelector("strong")?.textContent).toBe("See");
	const link = container.querySelector("a");
	expect(link).not.toBeNull();
	click(link as Element);
	expect(opened[0]).toMatchObject({ deviceId: "CapturedDevice", path: "/repo/src/a.ts" });
});

test("the SDK citation renderer and GFM table formatting remain available", async () => {
	const text = "See [1].\n\n| A | B |\n| --- | --- |\n| one | two |";
	const sources = [{ title: "Reference", sourceName: "Docs", url: "https://example.com/docs" }];
	await render(`root = Card([TextContent(${JSON.stringify(text)})], ${JSON.stringify(sources)})`);
	expect(container.querySelector(".openui-citation")).not.toBeNull();
	expect(container.querySelector("table")?.textContent).toContain("one");
});
