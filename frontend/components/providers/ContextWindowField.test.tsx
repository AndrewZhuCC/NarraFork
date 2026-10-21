import { afterAll, afterEach, beforeEach, describe, expect, mock, test } from "bun:test";
import { MantineProvider } from "@mantine/core";
import type { ModelWindowInfo } from "@shared/model-catalog/schema/api";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { parseHTML } from "linkedom";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";

/**
 * A provider row's window shows the value requests use and its source, and the editor
 * writes either this provider entry's own value or the model-wide one.
 */

const windowCalls: string[][] = [];
const setCalls: unknown[] = [];
const infos = new Map<string, ModelWindowInfo>();

const realReactI18nextModule = { ...(await import("react-i18next")) };
const realModelCatalogApi = { ...(await import("../../lib/api/model-catalog")) };

mock.module("react-i18next", () => ({
	useTranslation: () => ({ t: (key: string) => key }),
}));
mock.module("../../lib/api/model-catalog", () => ({
	...realModelCatalogApi,
	modelCatalogApi: {
		...realModelCatalogApi.modelCatalogApi,
		windows: (models: string[]) => {
			windowCalls.push(models);
			return Promise.resolve({
				revision: 5,
				windows: models.flatMap((model) => (infos.has(model) ? [infos.get(model)] : [])),
			});
		},
		setWindow: (input: unknown) => {
			setCalls.push(input);
			return Promise.resolve({ schemaVersion: 2, catalog: {}, local: { revision: 6 } });
		},
	},
}));

const { ContextWindowField } = await import("./ContextWindowField");

let root: Root | undefined;
let container: HTMLDivElement | undefined;

function installDom() {
	const { window } = parseHTML("<!doctype html><html><head></head><body></body></html>");
	if (!window.document.fonts)
		Object.defineProperty(window.document, "fonts", {
			configurable: true,
			value: { addEventListener() {}, removeEventListener() {} },
		});
	Object.assign(globalThis, {
		window,
		document: window.document,
		navigator: window.navigator,
		Event: window.Event,
		MouseEvent: window.MouseEvent ?? window.Event,
		Document: window.Document,
		ShadowRoot: window.ShadowRoot ?? class ShadowRoot {},
		HTMLElement: window.HTMLElement,
		HTMLButtonElement: window.HTMLButtonElement,
		HTMLInputElement: window.HTMLInputElement,
		HTMLTextAreaElement: window.HTMLTextAreaElement,
		Element: window.Element,
		Node: window.Node,
		Text: window.Text,
		ResizeObserver: class {
			observe() {}
			unobserve() {}
			disconnect() {}
		},
		MutationObserver: class {
			observe() {}
			disconnect() {}
			takeRecords() {
				return [];
			}
		},
		matchMedia: (query: string) => ({
			matches: false,
			media: query,
			onchange: null,
			addListener() {},
			removeListener() {},
			addEventListener() {},
			removeEventListener() {},
			dispatchEvent: () => false,
		}),
		requestAnimationFrame: (callback: FrameRequestCallback) => setTimeout(callback, 0),
		cancelAnimationFrame: (id: number) => clearTimeout(id),
		getComputedStyle: window.getComputedStyle?.bind(window) ?? (() => ({})),
		IS_REACT_ACT_ENVIRONMENT: true,
	});
}

const wait = (ms = 20) =>
	act(async () => {
		await new Promise((resolve) => setTimeout(resolve, ms));
	});

async function render(models: string[]) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	await act(async () => {
		root?.render(
			<MantineProvider>
				<QueryClientProvider client={client}>
					{models.map((model) => (
						<ContextWindowField key={model} modelValue={model} />
					))}
				</QueryClientProvider>
			</MantineProvider>,
		);
	});
	await wait();
}
function buttonWithText(text: string) {
	const found = [...document.querySelectorAll("button")].find((b) => b.textContent?.includes(text));
	if (!found) throw new Error(`Missing button ${text}`);
	return found as HTMLButtonElement;
}
async function click(element: Element) {
	await act(async () => {
		element.dispatchEvent(new window.MouseEvent("click", { bubbles: true }));
	});
	await wait();
}

beforeEach(() => {
	installDom();
	windowCalls.length = 0;
	setCalls.length = 0;
	infos.clear();
	infos.set("codex:gpt-6-astra", {
		model: "codex:gpt-6-astra",
		contextWindow: 272_000,
		source: "own",
		own: 272_000,
		inherited: 400_000,
		inheritedSource: "model",
		modelId: "gpt-6-astra",
		modelWide: 400_000,
		others: [],
	});
	infos.set("openai:gpt-6-astra", {
		model: "openai:gpt-6-astra",
		contextWindow: 400_000,
		source: "model",
		inherited: 400_000,
		inheritedSource: "model",
		modelId: "gpt-6-astra",
		modelWide: 400_000,
		others: [{ model: "codex:gpt-6-astra", contextWindow: 272_000 }],
	});
	container = document.createElement("div");
	document.body.appendChild(container);
	root = createRoot(container);
});
afterEach(async () => {
	if (root) await act(async () => root?.unmount());
	container?.remove();
	root = undefined;
	container = undefined;
});
afterAll(() => {
	mock.module("react-i18next", () => realReactI18nextModule);
	mock.module("../../lib/api/model-catalog", () => realModelCatalogApi);
	mock.restore();
});

describe("ContextWindowField", () => {
	test("rows load in one batch and show the value in use with its source", async () => {
		await render(["codex:gpt-6-astra", "openai:gpt-6-astra"]);
		expect(windowCalls).toEqual([["codex:gpt-6-astra", "openai:gpt-6-astra"]]);
		const text = container?.textContent ?? "";
		expect(text).toContain("272k");
		expect(text).toContain("contextWindowSource.own");
		expect(text).toContain("400k");
		expect(text).toContain("contextWindowSource.model");
	});

	test("clearing a provider entry's own value writes only that entry", async () => {
		await render(["codex:gpt-6-astra"]);
		await click(buttonWithText("272k"));
		await click(buttonWithText("contextWindowClearOwn"));
		expect(setCalls).toEqual([
			{ baseRevision: 5, scope: "connection", target: "codex:gpt-6-astra", contextWindow: null },
		]);
	});

	test("the all-providers scope writes the catalog model and names entries that keep their own", async () => {
		await render(["openai:gpt-6-astra"]);
		await click(buttonWithText("400k"));
		const modelScope = [...document.querySelectorAll("input")].find(
			(input) => input.getAttribute("value") === "model",
		);
		if (!modelScope) throw new Error("Missing scope control");
		await click(modelScope);
		expect(document.body.textContent).toContain("contextWindowOthersShadowModel");
		expect(document.body.textContent).toContain("codex:gpt-6-astra · 272k");
		await click(buttonWithText("contextWindowSave"));
		expect(setCalls).toEqual([
			{ baseRevision: 5, scope: "model", target: "gpt-6-astra", contextWindow: 400_000 },
		]);
	});
});
