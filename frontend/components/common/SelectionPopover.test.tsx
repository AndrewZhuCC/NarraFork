import { afterAll, afterEach, beforeEach, describe, expect, mock, spyOn, test } from "bun:test";
import { MantineProvider } from "@mantine/core";
import { parseHTML } from "linkedom";
import { act, useRef } from "react";
import { createRoot, type Root } from "react-dom/client";
import * as clipboard from "../../lib/clipboard";
import * as domText from "../../lib/dom-text";

const realReactI18nextModule = { ...(await import("react-i18next")) };

mock.module("react-i18next", () => ({
	useTranslation: () => ({ t: (key: string) => key }),
}));

const { MAX_SELECTION_POPOVER_TEXT_CHARS, SelectionPopover } = await import("./SelectionPopover");

class TestResizeObserver {
	observe() {}
	unobserve() {}
	disconnect() {}
}

let root: Root | undefined;
let container: HTMLDivElement | undefined;

function installDom() {
	const { window } = parseHTML("<!doctype html><html><head></head><body></body></html>");
	const matchMedia = (query: string) => ({
		matches: false,
		media: query,
		onchange: null,
		addListener() {},
		removeListener() {},
		addEventListener() {},
		removeEventListener() {},
		dispatchEvent: () => false,
	});
	Object.assign(window, {
		innerWidth: 1024,
		innerHeight: 768,
		requestAnimationFrame: (cb: FrameRequestCallback) => setTimeout(cb, 0),
		cancelAnimationFrame: (id: number) => clearTimeout(id),
		matchMedia,
		getSelection: () => ({ removeAllRanges() {} }),
	});
	Object.assign(window.document, { getSelection: () => window.getSelection() });
	Object.defineProperty(window.document, "fonts", {
		configurable: true,
		value: { addEventListener() {}, removeEventListener() {} },
	});
	Object.defineProperty(window.HTMLElement.prototype, "getBoundingClientRect", {
		configurable: true,
		value: () => ({
			top: 40,
			left: 40,
			width: 80,
			height: 20,
			right: 120,
			bottom: 60,
			x: 40,
			y: 40,
			toJSON() {
				return this;
			},
		}),
	});
	Object.assign(globalThis, {
		window,
		document: window.document,
		navigator: window.navigator,
		Event: window.Event,
		MouseEvent: window.MouseEvent ?? window.Event,
		HTMLElement: window.HTMLElement,
		HTMLInputElement: window.HTMLInputElement,
		HTMLTextAreaElement: window.HTMLTextAreaElement,
		HTMLButtonElement: window.HTMLButtonElement,
		Element: window.Element,
		Node: window.Node,
		ResizeObserver: TestResizeObserver,
		MutationObserver: window.MutationObserver,
		matchMedia,
		getComputedStyle:
			window.getComputedStyle?.bind(window) ?? (() => ({ getPropertyValue: () => "" })),
		IS_REACT_ACT_ENVIRONMENT: true,
	});
}

function Harness({
	onAction,
	label,
	externalSelection = "quoted text",
	domMode,
}: {
	onAction?: (text: string) => void;
	label?: string;
	externalSelection?: string;
	domMode?: boolean;
}) {
	const ref = useRef<HTMLDivElement>(null);
	return (
		<div>
			<div ref={ref} data-selectable>
				selectable
			</div>
			<SelectionPopover
				containerRef={ref}
				externalSelection={domMode ? undefined : externalSelection}
				externalAnchor={{ x: 80, y: 60 }}
				onAction={onAction}
				label={label}
			/>
		</div>
	);
}

async function renderHarness(
	props: {
		onAction?: (text: string) => void;
		label?: string;
		externalSelection?: string;
		domMode?: boolean;
	} = {},
) {
	await act(async () => {
		root?.render(
			<MantineProvider env="test">
				<Harness {...props} />
			</MantineProvider>,
		);
	});
}

function button(label: string): HTMLElement | null {
	return document.body.querySelector(`[aria-label="${label}"]`);
}

beforeEach(() => {
	installDom();
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
	mock.restore();
});

describe("SelectionPopover actions", () => {
	test.each([
		"style",
		"class",
		"hidden",
		"inert",
	])("hiding a dock ancestor via %s dismisses an open popover", async (attribute) => {
		await renderHarness();
		const target = container?.querySelector("[data-selectable]") as HTMLElement;
		const ancestor = target.parentElement;
		if (!ancestor) throw new Error("missing dock ancestor");
		Object.defineProperty(target, "checkVisibility", {
			configurable: true,
			value: () => !ancestor.hasAttribute(attribute),
		});
		expect(button("copy")).toBeTruthy();
		await act(async () => {
			ancestor.setAttribute(attribute, attribute === "style" ? "display:none" : "hidden-panel");
		});
		expect(button("copy")).toBeNull();
		await act(async () => {
			ancestor.removeAttribute(attribute);
		});
		expect(button("copy")).toBeNull();
	});
	test.each([
		"touchend",
		"keyup",
	])("DOM selection completes on %s and dismisses when cleared", async (event) => {
		const preview = spyOn(domText, "collectSelectionTextPreview").mockReturnValue("selected");
		try {
			await renderHarness({ domMode: true });
			const target = container?.querySelector("[data-selectable]");
			if (!target) throw new Error("missing selection container");
			let collapsed = false;
			Object.assign(window, {
				getSelection: () => ({
					isCollapsed: collapsed,
					rangeCount: collapsed ? 0 : 1,
					removeAllRanges: () => {
						collapsed = true;
					},
					getRangeAt: () => ({
						commonAncestorContainer: target,
						startContainer: target,
						getBoundingClientRect: () => target.getBoundingClientRect(),
					}),
				}),
			});
			await act(async () => {
				target.dispatchEvent(new Event(event, { bubbles: true }));
				await new Promise((resolve) => setTimeout(resolve, 20));
			});
			expect(preview).toHaveBeenCalledWith(expect.anything(), 200_000);
			expect(button("copy")).toBeTruthy();
			const press = new MouseEvent("mousedown", { bubbles: true, cancelable: true });
			await act(async () => button("copy")?.dispatchEvent(press));
			expect(press.defaultPrevented).toBe(true);
			await act(async () => document.dispatchEvent(new Event("selectionchange")));
			expect(button("copy")).toBeTruthy();
			// Alt+Q clears the selection and focuses the comment editor outside the message list.
			// There is no subsequent mouse/key event in the selection container.
			await act(async () => {
				window.getSelection()?.removeAllRanges();
				document.dispatchEvent(new Event("selectionchange"));
			});
			expect(button("copy") === null).toBe(true);
		} finally {
			preview.mockRestore();
		}
	});
	test("external selection survives DOM mouseup and selectionchange", async () => {
		const sent: string[] = [];
		await renderHarness({ onAction: (text) => sent.push(text) });
		await act(async () => {
			container
				?.querySelector("[data-selectable]")
				?.dispatchEvent(new MouseEvent("mouseup", { bubbles: true }));
			document.dispatchEvent(new Event("selectionchange"));
			await new Promise((resolve) => setTimeout(resolve, 20));
		});
		expect(button("copy")).toBeTruthy();
		await act(async () =>
			button("send")?.dispatchEvent(new MouseEvent("click", { bubbles: true })),
		);
		expect(sent).toEqual(["quoted text"]);
	});

	test("shows copy without requiring a send callback", async () => {
		await renderHarness();
		expect(button("copy")).toBeTruthy();
		expect(button("send")).toBeNull();
		expect(button("Send to terminal")).toBeNull();
	});

	test("copy uses the clipboard helper and bounds external selection text", async () => {
		const copy = spyOn(clipboard, "copyTextToClipboard").mockResolvedValue(undefined);
		try {
			const bounded = "x".repeat(MAX_SELECTION_POPOVER_TEXT_CHARS);
			await renderHarness({ externalSelection: `${bounded}overflow` });
			await act(async () =>
				button("copy")?.dispatchEvent(new MouseEvent("click", { bubbles: true })),
			);
			expect(copy).toHaveBeenCalledTimes(1);
			expect(copy).toHaveBeenCalledWith(bounded);
			expect(button("send")).toBeNull();
		} finally {
			copy.mockRestore();
		}
	});

	test("shows send only when a send callback is provided", async () => {
		await renderHarness({ onAction: () => {}, label: "Send to terminal" });
		expect(button("copy")).toBeTruthy();
		expect(button("Send to terminal")).toBeTruthy();
	});

	test("send callback receives the selected text", async () => {
		const sent: string[] = [];
		await renderHarness({
			onAction: (text) => sent.push(text),
			label: "Send to terminal",
		});
		await act(async () =>
			button("Send to terminal")?.dispatchEvent(new MouseEvent("click", { bubbles: true })),
		);
		expect(sent).toEqual(["quoted text"]);
	});
});
