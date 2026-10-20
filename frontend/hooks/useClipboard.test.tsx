import { afterEach, beforeEach, describe, expect, spyOn, test } from "bun:test";
import { parseHTML } from "linkedom";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { useClipboard } from "./useClipboard";

let root: Root | undefined;
let container: HTMLDivElement | undefined;

function installDom(execCommand: () => boolean) {
	const { window } = parseHTML("<!doctype html><html><body></body></html>");
	Object.defineProperty(window, "isSecureContext", { value: false, configurable: true });
	Object.defineProperties(window.HTMLInputElement.prototype, {
		select: { value: () => {}, configurable: true },
		setSelectionRange: { value: () => {}, configurable: true },
	});
	Object.defineProperties(window.HTMLTextAreaElement.prototype, {
		select: { value: () => {}, configurable: true },
		setSelectionRange: { value: () => {}, configurable: true },
	});
	Object.defineProperty(window.document, "execCommand", {
		value: execCommand,
		configurable: true,
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
		Element: window.Element,
		Node: window.Node,
		IS_REACT_ACT_ENVIRONMENT: true,
	});
}

function ClipboardHarness({ timeout = 1000 }: { timeout?: number }) {
	const clipboard = useClipboard({ timeout });
	return (
		<div>
			<button type="button" onClick={() => clipboard.copy("copy me")}>
				copy
			</button>
			<span data-testid="copied">{clipboard.copied ? "yes" : "no"}</span>
			<span data-testid="error">{clipboard.error?.message ?? ""}</span>
		</div>
	);
}

async function renderHarness(timeout?: number) {
	container = document.createElement("div");
	document.body.appendChild(container);
	root = createRoot(container);
	await act(async () => root?.render(<ClipboardHarness timeout={timeout} />));
	const button = container.querySelector("button");
	await act(async () => button?.dispatchEvent(new MouseEvent("click", { bubbles: true })));
}

beforeEach(() => {
	root = undefined;
	container = undefined;
});

afterEach(async () => {
	if (root) await act(async () => root?.unmount());
	container?.remove();
	root = undefined;
	container = undefined;
});

describe("useClipboard", () => {
	test("sets and resets copied state after a successful fallback", async () => {
		installDom(() => true);
		// linkedom forwards window timers to globalThis, so intercept that real boundary.
		const schedule = globalThis.setTimeout;
		const cancel = globalThis.clearTimeout;
		let resetCopied: (() => void) | undefined;
		// Control only this hook's 10ms timer; React's scheduling stays real.
		const timerId = -1;
		const timer = spyOn(globalThis, "setTimeout").mockImplementation(((
			callback: () => void,
			ms?: number,
		) => {
			if (ms !== 10) return schedule(callback, ms);
			resetCopied = callback;
			return timerId;
		}) as typeof window.setTimeout);
		const clear = spyOn(globalThis, "clearTimeout").mockImplementation(((
			id: Parameters<typeof window.clearTimeout>[0],
		) => {
			if (id === timerId) resetCopied = undefined;
			else cancel(id);
		}) as typeof window.clearTimeout);
		try {
			await renderHarness(10);
			expect(timer.mock.calls.filter(([, ms]) => ms === 10)).toHaveLength(1);
			expect(resetCopied).toBeFunction();
			expect(container?.querySelector('[data-testid="copied"]')?.textContent).toBe("yes");
			await act(async () => resetCopied?.());
			expect(container?.querySelector('[data-testid="copied"]')?.textContent).toBe("no");
			await act(async () => root?.unmount());
			root = undefined;
			expect(clear).toHaveBeenCalledWith(timerId);
			expect(resetCopied).toBeUndefined();
		} finally {
			try {
				if (root) await act(async () => root?.unmount());
				root = undefined;
			} finally {
				timer.mockRestore();
				clear.mockRestore();
			}
		}
	});

	test("exposes an error when both clipboard paths fail", async () => {
		installDom(() => false);
		await renderHarness();
		expect(container?.querySelector('[data-testid="copied"]')?.textContent).toBe("no");
		expect(container?.querySelector('[data-testid="error"]')?.textContent).toContain(
			"Clipboard copy command failed",
		);
	});
});
