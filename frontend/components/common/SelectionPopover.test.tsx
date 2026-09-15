import { afterAll, afterEach, beforeEach, describe, expect, mock, spyOn, test } from "bun:test";
import { MantineProvider } from "@mantine/core";
import { parseHTML } from "linkedom";
import { act, useRef } from "react";
import { createRoot, type Root } from "react-dom/client";
import * as domText from "../../lib/dom-text";

const realReactI18nextModule = { ...(await import("react-i18next")) };

mock.module("react-i18next", () => ({
	useTranslation: () => ({ t: (key: string) => key }),
}));

const { SelectionPopover } = await import("./SelectionPopover");

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
	onComment,
	label,
	resetKey,
	hideComment,
	externalSelection = "quoted text",
	domMode,
}: {
	onAction?: (text: string) => void;
	onComment?: (payload: { excerpt: string; comment: string; anchorNode: Node | null }) => void;
	label?: string;
	resetKey?: string;
	hideComment?: boolean;
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
				resetKey={resetKey}
				hideComment={hideComment}
				externalAnchor={{ x: 80, y: 60 }}
				onAction={onAction}
				onComment={onComment}
				label={label}
			/>
		</div>
	);
}

async function renderHarness(
	props: {
		onAction?: (text: string) => void;
		onComment?: (payload: { excerpt: string; comment: string; anchorNode: Node | null }) => void;
		label?: string;
		resetKey?: string;
		hideComment?: boolean;
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
		expect(button("addComment")).toBeTruthy();
		await act(async () => {
			ancestor.setAttribute(attribute, attribute === "style" ? "display:none" : "hidden-panel");
		});
		expect(button("addComment")).toBeNull();
		await act(async () => {
			ancestor.removeAttribute(attribute);
		});
		expect(button("addComment")).toBeNull();
	});
	test.each([
		"touchend",
		"keyup",
	])("DOM selection completes on %s with the shared budget", async (event) => {
		const preview = spyOn(domText, "collectSelectionTextPreview").mockReturnValue("selected");
		try {
			await renderHarness({ domMode: true });
			const target = container?.querySelector("[data-selectable]");
			if (!target) throw new Error("missing selection container");
			Object.assign(window, {
				getSelection: () => ({
					rangeCount: 1,
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
			expect(button("addComment")).toBeTruthy();
		} finally {
			preview.mockRestore();
		}
	});
	test("external selection survives DOM mouseup and comment input focus", async () => {
		await renderHarness();
		await act(async () => {
			container
				?.querySelector("[data-selectable]")
				?.dispatchEvent(new MouseEvent("mouseup", { bubbles: true }));
			await new Promise((resolve) => setTimeout(resolve, 20));
		});
		expect(button("addComment")).toBeTruthy();
		await act(async () =>
			button("addComment")?.dispatchEvent(new MouseEvent("click", { bubbles: true })),
		);
		await act(async () =>
			document.body
				.querySelector("textarea")
				?.dispatchEvent(new MouseEvent("mousedown", { bubbles: true })),
		);
		expect(document.body.querySelector("textarea")).toBeTruthy();
	});

	test("reset target dismisses old selection even if the external text is unchanged", async () => {
		await renderHarness({ resetKey: "a" });
		expect(button("addComment")).toBeTruthy();
		await renderHarness({
			resetKey: "b",
			onComment: () => {
				throw new Error("stale comment");
			},
		});
		expect(button("addComment")).toBeNull();
		await renderHarness({ resetKey: "b", externalSelection: "new quote" });
		expect(button("addComment")).toBeTruthy();
	});

	test("hideComment preserves copying without a recipient", async () => {
		await renderHarness({ hideComment: true });
		expect(button("copy")).toBeTruthy();
		expect(button("addComment")).toBeNull();
	});
	test("always shows copy and comment; send only when onAction is provided", async () => {
		await renderHarness();
		expect(button("copy")).toBeTruthy();
		expect(button("addComment")).toBeTruthy();
		expect(button("send")).toBeNull();
		expect(button("Send to terminal")).toBeNull();
	});

	test("shows comment without requiring an onComment handler", async () => {
		await renderHarness();
		expect(button("copy")).toBeTruthy();
		expect(button("addComment")).toBeTruthy();
		expect(button("send")).toBeNull();
	});

	test("shows send only when a send callback is provided", async () => {
		await renderHarness({ onAction: () => {}, label: "Send to terminal" });
		expect(button("copy")).toBeTruthy();
		expect(button("addComment")).toBeTruthy();
		expect(button("Send to terminal")).toBeTruthy();
	});

	test("opens the comment panel and confirms into onComment", async () => {
		const comments: Array<{ excerpt: string; comment: string }> = [];
		await renderHarness({
			onComment: (payload) => {
				comments.push({ excerpt: payload.excerpt, comment: payload.comment });
			},
		});

		await act(async () =>
			button("addComment")?.dispatchEvent(new MouseEvent("click", { bubbles: true })),
		);
		const textarea = document.body.querySelector("textarea");
		expect(textarea).toBeTruthy();

		const setter = Object.getOwnPropertyDescriptor(
			window.HTMLTextAreaElement.prototype,
			"value",
		)?.set;
		if (!textarea || !setter) throw new Error("missing textarea value setter");
		await act(async () => {
			setter.call(textarea, "please rephrase");
			textarea.dispatchEvent(new Event("input", { bubbles: true }));
			textarea.dispatchEvent(new Event("change", { bubbles: true }));
		});

		const confirm = Array.from(document.body.querySelectorAll("button")).find(
			(el) => el.textContent === "confirm",
		);
		expect(confirm).toBeTruthy();
		await act(async () => confirm?.dispatchEvent(new MouseEvent("click", { bubbles: true })));

		expect(comments).toEqual([{ excerpt: "quoted text", comment: "please rephrase" }]);
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
