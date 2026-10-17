/**
 * HighlightedCode must not replace its text nodes on an ordinary re-render.
 *
 * React assigns `innerHTML` whenever the `dangerouslySetInnerHTML` object is a
 * new identity, even if the HTML string is unchanged. The tool-call inspector
 * re-renders that block on hover and whenever its host row updates, which
 * cancelled an in-progress selection. The payload is memoized on the HTML string
 * so a style-only re-render keeps the same nodes.
 */

import { afterEach, beforeEach, describe, expect, spyOn, test } from "bun:test";
import { MantineProvider } from "@mantine/core";
import { parseHTML } from "linkedom";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import * as shikiLoader from "../../../lib/shiki-loader";
import { HighlightedCode } from "./HighlightedCode";
import { clearHighlightCache } from "./highlight-cache";

let root: Root;
let container: HTMLDivElement;
let loaderSpy: ReturnType<typeof spyOn<typeof shikiLoader, "loadShiki">>;
const originalGlobals = new Map<string, PropertyDescriptor | undefined>();
const globalKeys = [
	"window",
	"document",
	"navigator",
	"HTMLElement",
	"Element",
	"Node",
	"matchMedia",
	"IS_REACT_ACT_ENVIRONMENT",
] as const;

beforeEach(() => {
	const { window } = parseHTML("<!doctype html><html><body></body></html>");
	for (const key of globalKeys)
		originalGlobals.set(key, Object.getOwnPropertyDescriptor(globalThis, key));
	const overrides = {
		window,
		document: window.document,
		navigator: window.navigator,
		HTMLElement: window.HTMLElement,
		Element: window.Element,
		Node: window.Node,
		matchMedia: () => ({ matches: false, addEventListener() {}, removeEventListener() {} }),
		IS_REACT_ACT_ENVIRONMENT: true,
	};
	for (const [key, value] of Object.entries(overrides)) {
		Object.defineProperty(globalThis, key, { configurable: true, writable: true, value });
	}
	container = document.createElement("div");
	document.body.appendChild(container);
	root = createRoot(container);
	clearHighlightCache();
	loaderSpy = spyOn(shikiLoader, "loadShiki").mockResolvedValue({
		bundledLanguages: { json: true },
		codeToHtml: async (code: string) => `<span data-hl-token="1">${code}</span>`,
		codeToTokens: async () => ({ tokens: [] }),
	});
});

afterEach(async () => {
	try {
		await act(async () => root?.unmount());
		container?.remove();
	} finally {
		loaderSpy?.mockRestore();
		clearHighlightCache();
		for (const [key, descriptor] of originalGlobals) {
			if (descriptor) Object.defineProperty(globalThis, key, descriptor);
			else Reflect.deleteProperty(globalThis, key);
		}
		originalGlobals.clear();
	}
});

async function render(code: string, fontSize: number) {
	await act(async () => {
		root.render(
			<MantineProvider forceColorScheme="dark">
				<HighlightedCode code={code} lang="json" style={{ fontSize }} />
			</MantineProvider>,
		);
	});
}

describe("HighlightedCode selection stability", () => {
	test("a style-only re-render keeps the highlighted text node", async () => {
		await render('{"a":1}', 12);
		const token = container.querySelector("[data-hl-token]");
		expect(token).not.toBeNull();
		expect(token?.textContent).toBe('{"a":1}');
		token?.setAttribute("data-alive", "yes");

		await render('{"a":1}', 13);

		expect(container.querySelector("[data-hl-token]")).toBe(token);
		expect(token?.isConnected).toBe(true);
		expect(token?.getAttribute("data-alive")).toBe("yes");
	});

	test("a new source still replaces the highlighted text", async () => {
		await render('{"a":1}', 12);
		const token = container.querySelector("[data-hl-token]");
		await render('{"a":2}', 12);
		expect(container.querySelector("[data-hl-token]")?.textContent).toBe('{"a":2}');
		expect(token?.isConnected).toBe(false);
	});
});
