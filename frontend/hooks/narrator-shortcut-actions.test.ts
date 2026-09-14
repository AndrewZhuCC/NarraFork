import { afterAll, afterEach, beforeEach, describe, expect, test } from "bun:test";
import { parseHTML } from "linkedom";
import { DEFAULT_NARRATOR_SHORTCUTS } from "../lib/narrator-shortcuts";
import type { RecentTab } from "./useRecentTabs";

const { window } = parseHTML("<html><body></body></html>");
const saved = new Map<string, PropertyDescriptor | undefined>();
const storage = new Map<string, string>();
for (const [key, value] of Object.entries({
	window,
	document: window.document,
	Element: window.Element,
	HTMLElement: window.HTMLElement,
	navigator: { platform: "MacIntel" },
	CSS: { escape: (value: string) => value },
	localStorage: {
		getItem: (key: string) => storage.get(key) ?? null,
		setItem: (key: string, value: string) => storage.set(key, value),
	},
})) {
	saved.set(key, Object.getOwnPropertyDescriptor(globalThis, key));
	Object.defineProperty(globalThis, key, { configurable: true, writable: true, value });
}
const {
	cancelPendingTabNavigation,
	handleRecentTabShortcut,
	narratorShortcutTabs,
	registerRecentTabKeyboardSource,
} = await import("./useRecentTabKeyboardNav");
const { registerReadFromStartTarget } = await import("./useReadFromStartShortcut");
const { readNarratorShortcuts, resetNarratorShortcuts, setNarratorShortcut } = await import(
	"./useNarratorShortcuts"
);
const { writeLocalShortcutPref } = await import("./useLocalPref");
let unregister: (() => void) | undefined;
let focus: Element | null = null;
Object.defineProperty(document, "activeElement", { configurable: true, get: () => focus });
const tab = (id: string, extra: Partial<RecentTab> = {}): RecentTab => ({
	type: "narrator",
	id,
	title: id,
	lastVisitedAt: 0,
	...extra,
});
function fire(
	target: Element,
	handle: (event: KeyboardEvent) => void,
	overrides: Partial<KeyboardEvent> = {},
) {
	const event = Object.assign(new window.Event("keydown", { bubbles: true, cancelable: true }), {
		code: "Digit1",
		key: "!",
		metaKey: true,
		ctrlKey: false,
		altKey: false,
		shiftKey: true,
		repeat: false,
		isComposing: false,
		...overrides,
	}) as unknown as KeyboardEvent;
	document.addEventListener("keydown", handle, { once: true });
	target.dispatchEvent(event);
	return event;
}
beforeEach(() => {
	document.body.innerHTML = "";
	focus = null;
	storage.clear();
});
afterEach(() => {
	unregister?.();
	unregister = undefined;
	cancelPendingTabNavigation();
});
afterAll(() => {
	for (const [key, descriptor] of saved) {
		if (descriptor) Object.defineProperty(globalThis, key, descriptor);
		else Reflect.deleteProperty(globalThis, key);
	}
});

describe("narrator navigation", () => {
	test("uses rendered order, excludes headers and deduplicates narrator identities", () => {
		const targets = narratorShortcutTabs([
			tab("project", { type: "project" }),
			tab("pinned", { pinned: true }),
			tab("ws", { type: "workspace" }),
			tab("child", { workspaceId: "ws" }),
			tab("bound", { type: "chapter", narratorId: "bound-narrator" }),
			tab("bound-narrator"),
			tab("chapter-without-narrator", { type: "chapter" }),
		]);
		expect(targets.map((item) => item.id)).toEqual(["pinned", "child", "bound"]);
	});
	test("a number cancels pending previous/next navigation", async () => {
		unregister = registerRecentTabKeyboardSource(() => [tab("a"), tab("b"), tab("c")]);
		const calls: string[] = [];
		const handler = (event: KeyboardEvent) =>
			handleRecentTabShortcut(
				event,
				"/narrators/a",
				({ to }) => calls.push(to),
				DEFAULT_NARRATOR_SHORTCUTS,
			);
		fire(document.body, handler, {
			code: "ArrowDown",
			key: "ArrowDown",
			ctrlKey: true,
			metaKey: false,
			shiftKey: false,
		});
		fire(document.body, handler, { code: "Digit3" });
		await new Promise((resolve) => setTimeout(resolve, 450));
		expect(calls).toEqual(["/narrators/c"]);
	});
	test("missing or collapsed targets do not consume the key", () => {
		unregister = registerRecentTabKeyboardSource(() => []);
		const handler = (event: KeyboardEvent) =>
			handleRecentTabShortcut(
				event,
				"/",
				() => {
					throw new Error("must not navigate");
				},
				DEFAULT_NARRATOR_SHORTCUTS,
			);
		expect(fire(document.body, handler).defaultPrevented).toBe(false);
	});
	test("allows modified switching in the composer but not editors, terminals or configuration", () => {
		unregister = registerRecentTabKeyboardSource(() => [tab("a")]);
		for (const attribute of [
			"",
			'class="monaco-editor"',
			'class="xterm"',
			"data-narrator-shortcut-settings",
		]) {
			document.body.innerHTML = `<div ${attribute}><textarea></textarea></div>`;
			let count = 0;
			const handler = (event: KeyboardEvent) =>
				handleRecentTabShortcut(event, "/", () => count++, DEFAULT_NARRATOR_SHORTCUTS);
			const input = document.querySelector("textarea");
			if (!input) throw new Error("missing test input");
			fire(input, handler);
			expect(count).toBe(attribute ? 0 : 1);
		}
	});
});

describe("read from start", () => {
	const cleanups: (() => void)[] = [];
	const calls: string[] = [];
	const home = { code: "Home", key: "Home", metaKey: false, shiftKey: false };
	const press = (target = document.body, overrides: Partial<KeyboardEvent> = {}) =>
		fire(target, () => {}, { ...home, ...overrides });
	function rect(node: HTMLElement, top = 0, bottom = 600) {
		Object.defineProperty(node, "getClientRects", { configurable: true, value: () => [{}] });
		Object.defineProperty(node, "getBoundingClientRect", {
			configurable: true,
			value: () => ({ top, bottom, left: 0, right: 300 }),
		});
	}
	function panel() {
		const node = document.createElement("div");
		node.setAttribute("data-narrator-panel", "");
		node.style.overflow = "hidden";
		rect(node, 100, 500);
		document.body.append(node);
		return node;
	}
	function host(id: string, parent = panel(), options = { hovered: false, active: true }) {
		const node = document.createElement("div");
		node.setAttribute("data-shortcut-reading-host", "");
		rect(node, 120, 450);
		parent.append(node);
		cleanups.push(
			registerReadFromStartTarget(() => ({ host: node, ...options, read: () => calls.push(id) })),
		);
		return node;
	}
	beforeEach(() => {
		calls.length = 0;
		Object.assign(window, {
			innerWidth: 320,
			innerHeight: 600,
			getComputedStyle: (node: HTMLElement) => node.style,
		});
		resetNarratorShortcuts();
	});
	afterEach(() => {
		for (const cleanup of cleanups.splice(0)) cleanup();
	});
	function composer(id: string, parent = panel(), active = true) {
		const node = document.createElement("textarea");
		node.setAttribute("data-narrator-composer", "");
		rect(node, 400, 490);
		parent.append(node);
		cleanups.push(
			registerReadFromStartTarget(() => ({
				host: node,
				active,
				hovered: false,
				composer: true,
				read: () => {
					calls.push(id);
					focus = node;
				},
			})),
		);
		return node;
	}
	const pressTab = (target = document.body, overrides: Partial<KeyboardEvent> = {}) =>
		press(target, { code: "Tab", key: "Tab", ...overrides });
	test("Tab focuses a single composer without hover and allows leaving then returning from buttons", () => {
		const input = composer("only");
		expect(pressTab().defaultPrevented).toBe(true);
		expect(focus).toBe(input);
		expect(pressTab(input).defaultPrevented).toBe(false);
		const button = document.createElement("button");
		document.body.append(button);
		focus = button;
		expect(pressTab(button).defaultPrevented).toBe(true);
		expect(calls).toEqual(["only", "only"]);
	});
	test("composer shares active, reading hover, last panel and focus arbitration with Home", () => {
		const a = panel(),
			b = panel();
		const body = host("read-a", a, { hovered: false, active: false });
		const options = { hovered: false, active: true };
		host("read-b", b, options);
		composer("a", a, false);
		composer("b", b);
		pressTab();
		focus = null;
		body.dispatchEvent(new window.Event("wheel", { bubbles: true }));
		pressTab();
		focus = null;
		options.hovered = true;
		press();
		pressTab();
		focus = body;
		// Reading focus wins even while another body's hover remains active.
		pressTab();
		expect(calls).toEqual(["b", "a", "read-b", "b", "a"]);
	});
	test.each([
		"display",
		"visibility",
		"hidden",
		"aria-hidden",
		"inert",
		"detached",
		"disabled",
	])("Tab excludes %s composers", (mode) => {
		const parent = panel();
		const input = composer("hidden", parent);
		if (mode === "display") parent.style.display = "none";
		if (mode === "visibility") parent.style.visibility = "hidden";
		if (mode === "hidden") parent.hidden = true;
		if (mode === "aria-hidden") parent.setAttribute("aria-hidden", "true");
		if (mode === "inert") parent.inert = true;
		if (mode === "detached") parent.remove();
		if (mode === "disabled") input.setAttribute("disabled", "");
		composer("visible", panel(), false);
		pressTab();
		expect(calls).toEqual(["visible"]);
	});
	test.each([
		"input",
		"textarea",
		'div contenteditable="true"',
		'div class="monaco-editor"',
		'div class="xterm"',
		"div data-narrator-shortcut-settings",
		'div role="dialog"',
		'div role="alertdialog"',
		'div role="menu"',
		'div role="listbox"',
	])("Tab leaves %s alone", (markup) => {
		composer("a");
		const node = document.createElement("div");
		node.innerHTML = `<${markup}></${markup.split(" ")[0]}>`;
		document.body.append(node);
		expect(pressTab(node.firstElementChild as HTMLElement).defaultPrevented).toBe(false);
		expect(calls).toEqual([]);
	});
	test.each([
		"dialog",
		"alertdialog",
		"menu",
		"listbox",
	])("an open %s blocks Tab even outside its portal", (role) => {
		composer("a");
		const overlay = document.createElement("div");
		overlay.setAttribute("role", role);
		rect(overlay);
		document.body.append(overlay);
		expect(pressTab().defaultPrevented).toBe(false);
		overlay.hidden = true;
		expect(pressTab().defaultPrevented).toBe(true);
	});
	test("focus binding honors an explicitly configured modified Tab", () => {
		composer("a");
		setNarratorShortcut("focusComposer", "Control+Tab");
		expect(pressTab().defaultPrevented).toBe(false);
		expect(pressTab(document.body, { ctrlKey: true }).defaultPrevented).toBe(true);
		expect(calls).toEqual(["a"]);
	});
	test("focus binding is live, disableable and preserves unconfigured chords, IME and repeats", () => {
		composer("a");
		setNarratorShortcut("focusComposer", "");
		expect(pressTab().defaultPrevented).toBe(false);
		setNarratorShortcut("focusComposer", "F6");
		expect(pressTab().defaultPrevented).toBe(false);
		expect(press(document.body, { code: "F6", key: "F6" }).defaultPrevented).toBe(true);
		focus = null;
		resetNarratorShortcuts();
		for (const overrides of [
			{ shiftKey: true },
			{ ctrlKey: true },
			{ metaKey: true },
			{ altKey: true },
			{ isComposing: true },
			{ keyCode: 229 },
			{ repeat: true },
		]) {
			expect(pressTab(document.body, overrides).defaultPrevented).toBe(false);
		}
		const prevent = (event: Event) => event.preventDefault();
		document.body.addEventListener("keydown", prevent);
		expect(pressTab().defaultPrevented).toBe(true);
		document.body.removeEventListener("keydown", prevent);
		expect(calls).toEqual(["a"]);
		Object.defineProperty(document, "visibilityState", { configurable: true, value: "hidden" });
		try {
			expect(pressTab().defaultPrevented).toBe(false);
		} finally {
			Reflect.deleteProperty(document, "visibilityState");
		}
	});
	test("composer-only registration cleans listeners on last unmount and does not consume Home or Enter", () => {
		composer("a");
		expect(press().defaultPrevented).toBe(false);
		expect(press(document.body, { code: "Enter", key: "Enter" }).defaultPrevented).toBe(false);
		for (const cleanup of cleanups.splice(0)) cleanup();
		expect(pressTab().defaultPrevented).toBe(false);
		composer("b");
		pressTab();
		expect(calls).toEqual(["b"]);
	});
	test("single foreground narrator needs no hover or focus, even at mobile width", () => {
		host("only");
		expect(press().defaultPrevented).toBe(true);
		expect(calls).toEqual(["only"]);
	});
	test("ignores keyboard events while the page is hidden", () => {
		host("only");
		const descriptor = Object.getOwnPropertyDescriptor(document, "visibilityState");
		Object.defineProperty(document, "visibilityState", { configurable: true, value: "hidden" });
		try {
			expect(press().defaultPrevented).toBe(false);
			expect(calls).toEqual([]);
		} finally {
			if (descriptor) Object.defineProperty(document, "visibilityState", descriptor);
			else Reflect.deleteProperty(document, "visibilityState");
		}
	});
	test("selects the body at the reading edge, not an offscreen historical DOM row", () => {
		const parent = panel();
		const old = host("history", parent);
		rect(old, -300, 90);
		const current = host("current", parent);
		rect(current, -30, 250);
		const next = host("next", parent);
		rect(next, 270, 480);
		focus = old;
		press();
		expect(calls).toEqual(["current"]);
	});
	test.each([
		"display",
		"visibility",
		"hidden",
		"aria-hidden",
		"detached",
	])("excludes %s panels even when focused or hovered", (mode) => {
		const parent = panel();
		const hidden = host("background", parent, { hovered: true, active: true });
		focus = hidden;
		if (mode === "display") parent.style.display = "none";
		if (mode === "visibility") parent.style.visibility = "hidden";
		if (mode === "hidden") parent.hidden = true;
		if (mode === "aria-hidden") parent.setAttribute("aria-hidden", "true");
		if (mode === "detached") parent.remove();
		host("foreground", panel(), { hovered: false, active: false });
		press();
		expect(calls).toEqual(["foreground"]);
	});
	test("multiple panels choose active, then last reading region, then hover and focus", () => {
		const a = host("a", panel(), { hovered: false, active: false });
		const options = { hovered: false, active: true };
		const b = host("b", panel(), options);
		press();
		a.dispatchEvent(new window.Event("pointerdown", { bubbles: true }));
		press();
		options.hovered = true;
		press();
		focus = a;
		press();
		expect(calls).toEqual(["b", "a", "b", "a"]);
		focus = null;
		options.hovered = false;
		b.dispatchEvent(new window.Event("wheel", { bubbles: true }));
		press();
		expect(calls.at(-1)).toBe("b");
	});
	test.each([
		"input",
		"textarea",
		"select",
		'div contenteditable="true"',
		'div class="monaco-editor"',
		'div class="xterm"',
		"div data-narrator-shortcut-settings",
		'div role="dialog"',
	])("preserves Home in %s", (markup) => {
		const a = host("a");
		a.innerHTML = `<${markup}></${markup.split(" ")[0]}>`;
		expect(press(a.firstElementChild as HTMLElement).defaultPrevented).toBe(false);
		expect(calls).toEqual([]);
	});
	test("uses live configurable bindings and respects disable, composition and handled keys", () => {
		host("a");
		setNarratorShortcut("readFromStart", "");
		expect(press().defaultPrevented).toBe(false);
		setNarratorShortcut("readFromStart", "F6");
		expect(press().defaultPrevented).toBe(false);
		expect(press(document.body, { code: "F6", key: "F6" }).defaultPrevented).toBe(true);
		resetNarratorShortcuts();
		expect(press(document.body, { isComposing: true }).defaultPrevented).toBe(false);
		expect(press(document.body, { repeat: true }).defaultPrevented).toBe(false);
		expect(calls).toEqual(["a"]);
	});
	test("many bodies share one listener and the final unmount removes it", () => {
		const add = document.addEventListener.bind(document);
		const remove = document.removeEventListener.bind(document);
		let added = 0,
			removed = 0;
		document.addEventListener = (...args: Parameters<Document["addEventListener"]>) => {
			if (args[0] === "keydown") added++;
			return add(...args);
		};
		document.removeEventListener = (...args: Parameters<Document["removeEventListener"]>) => {
			if (args[0] === "keydown") removed++;
			return remove(...args);
		};
		try {
			const parent = panel();
			for (let i = 0; i < 100; i++) host(String(i), parent);
			expect(added).toBe(1);
			cleanups.shift()?.();
			expect(removed).toBe(0);
			press();
			expect(calls).toHaveLength(1);
			for (const cleanup of cleanups.splice(0)) cleanup();
			expect(removed).toBe(1);
			expect(press().defaultPrevented).toBe(false);
			host("remounted");
			press();
			expect(calls.at(-1)).toBe("remounted");
		} finally {
			document.addEventListener = add;
			document.removeEventListener = remove;
		}
	});
});

describe("local shortcut preferences", () => {
	test("updates, disables and resets without overwriting other bindings", () => {
		setNarratorShortcut("previous", "Cmd+Option+ArrowUp");
		setNarratorShortcut("readFromStart", "");
		expect(readNarratorShortcuts().previous).toBe("Meta+Alt+ArrowUp");
		expect(readNarratorShortcuts().readFromStart).toBe("");
		setNarratorShortcut("next", "Cmd+Option+ArrowUp");
		expect(readNarratorShortcuts().next).toBe("Control+ArrowDown");
		resetNarratorShortcuts();
		expect(readNarratorShortcuts()).toEqual(DEFAULT_NARRATOR_SHORTCUTS);
	});
	test("failed writes still update this tab even if stale storage remains readable", () => {
		const setItem = localStorage.setItem;
		try {
			localStorage.setItem = () => {
				throw new Error("quota");
			};
			setNarratorShortcut("readFromStart", "F6");
			expect(readNarratorShortcuts().readFromStart).toBe("F6");
		} finally {
			localStorage.setItem = setItem;
			resetNarratorShortcuts();
		}
	});
	test("corrupt stored data falls back", () => {
		writeLocalShortcutPref("bad");
		expect(readNarratorShortcuts()).toEqual(DEFAULT_NARRATOR_SHORTCUTS);
	});
});
