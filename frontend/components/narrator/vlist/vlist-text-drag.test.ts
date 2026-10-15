/**
 * Selecting vlist text and then dragging that selection starts a native HTML5
 * drag. Chrome replaces pointerup with pointercancel and can finish without
 * dragend once the row under the pointer is swapped out, which leaves the page
 * unable to take clicks or keys until reload. The guard cancels that dragstart.
 */
import { afterEach, beforeEach, describe, expect, it } from "bun:test";
import { readFileSync } from "node:fs";
import { parseHTML } from "linkedom";
import { createPointerDragTracker } from "./vlist-pointer-drag";
import { installVListTextDragGuard, shouldCancelTextSelectionDrag } from "./vlist-text-drag";

let restore: (() => void) | undefined;

function installDom(html: string): Document {
	const previous = Object.getOwnPropertyDescriptor(globalThis, "document");
	const { window } = parseHTML(`<!doctype html><html><body>${html}</body></html>`);
	Object.defineProperty(globalThis, "document", {
		value: window.document,
		configurable: true,
		writable: true,
	});
	restore = () => {
		if (previous) Object.defineProperty(globalThis, "document", previous);
		else Reflect.deleteProperty(globalThis as object, "document");
	};
	return window.document;
}

function select(doc: Document, node: Node | null, collapsed = false): void {
	Object.defineProperty(doc, "getSelection", {
		configurable: true,
		value: () =>
			node
				? {
						isCollapsed: collapsed,
						rangeCount: collapsed ? 0 : 1,
						anchorNode: node,
						focusNode: node,
					}
				: null,
	});
}

function dragstart(target: Node): Event {
	const event = document.createEvent("Event");
	event.initEvent("dragstart", true, true);
	target.dispatchEvent(event);
	return event;
}

beforeEach(() => {
	installDom(
		`<div id="list"><p id="msg">hello session text</p><span id="handle" draggable="true">move</span><input id="field" value="edit" /></div><div id="outside">other</div>`,
	);
});

afterEach(() => {
	restore?.();
	restore = undefined;
});

describe("shouldCancelTextSelectionDrag", () => {
	it("cancels only a selection drag that starts inside the list", () => {
		expect(
			shouldCancelTextSelectionDrag({
				defaultPrevented: false,
				targetInRoot: true,
				explicitDragSource: false,
				selectionInRoot: true,
			}),
		).toBe(true);
		expect(
			shouldCancelTextSelectionDrag({
				defaultPrevented: true,
				targetInRoot: true,
				explicitDragSource: false,
				selectionInRoot: true,
			}),
		).toBe(false);
		expect(
			shouldCancelTextSelectionDrag({
				defaultPrevented: false,
				targetInRoot: false,
				explicitDragSource: false,
				selectionInRoot: true,
			}),
		).toBe(false);
		expect(
			shouldCancelTextSelectionDrag({
				defaultPrevented: false,
				targetInRoot: true,
				explicitDragSource: true,
				selectionInRoot: true,
			}),
		).toBe(false);
		expect(
			shouldCancelTextSelectionDrag({
				defaultPrevented: false,
				targetInRoot: true,
				explicitDragSource: false,
				selectionInRoot: false,
			}),
		).toBe(false);
	});
});

describe("installVListTextDragGuard", () => {
	it("cancels a drag of selected message text and does not latch the pointer tracker", async () => {
		const list = document.getElementById("list") as HTMLElement;
		const text = document.querySelector("#msg")?.firstChild;
		if (!text) throw new Error("missing text");
		select(document, text);
		const stop = installVListTextDragGuard(list);
		let releases = 0;
		const tracker = createPointerDragTracker(() => releases++);
		try {
			const event = dragstart(text);
			expect(event.defaultPrevented).toBe(true);
			await Promise.resolve();
			expect(tracker.isDown()).toBe(false);
			expect(releases).toBe(0);
		} finally {
			tracker.dispose();
			stop();
		}
	});

	it("leaves an explicit drag source and text outside the list alone", () => {
		const list = document.getElementById("list") as HTMLElement;
		const text = document.querySelector("#msg")?.firstChild;
		if (!text) throw new Error("missing text");
		select(document, text);
		const stop = installVListTextDragGuard(list);
		try {
			const handle = dragstart(document.getElementById("handle") as Node);
			const field = dragstart(document.getElementById("field") as Node);
			const outside = dragstart(document.getElementById("outside") as Node);
			expect(handle.defaultPrevented).toBe(false);
			expect(field.defaultPrevented).toBe(false);
			expect(outside.defaultPrevented).toBe(false);
		} finally {
			stop();
		}
	});

	it("does not cancel a press that has no text selection", () => {
		const list = document.getElementById("list") as HTMLElement;
		const text = document.querySelector("#msg")?.firstChild;
		if (!text) throw new Error("missing text");
		select(document, text, true);
		const stop = installVListTextDragGuard(list);
		try {
			expect(dragstart(text).defaultPrevented).toBe(false);
		} finally {
			stop();
		}
	});

	it("is installed before the pointer tracker so a cancelled drag cannot latch first", () => {
		const source = readFileSync(new URL("./PretextExactMessageList.tsx", import.meta.url), "utf8");
		const guard = source.indexOf("installVListTextDragGuard(node)");
		const tracker = source.indexOf("createPointerDragTracker(");
		expect(guard).toBeGreaterThan(0);
		expect(tracker).toBeGreaterThan(guard);
	});
});
