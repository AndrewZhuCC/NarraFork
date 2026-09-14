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
const { handleReadFromStartShortcut } = await import("./useReadFromStartShortcut");
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
	function host() {
		const node = document.createElement("div");
		node.setAttribute("data-shortcut-reading-host", "");
		Object.defineProperty(node, "getClientRects", { value: () => [{}] });
		document.body.append(node);
		return node;
	}
	const home = { code: "Home", key: "Home", metaKey: false, shiftKey: false };
	test("only the hovered target runs and a handled key cannot run twice", () => {
		const a = host(),
			b = host();
		const calls: string[] = [];
		const event = fire(
			document.body,
			(event) => {
				handleReadFromStartShortcut(event, a, false, () => calls.push("a"), "Home");
				handleReadFromStartShortcut(event, b, true, () => calls.push("b"), "Home");
				handleReadFromStartShortcut(event, a, true, () => calls.push("again"), "Home");
			},
			home,
		);
		expect(calls).toEqual(["b"]);
		expect(event.defaultPrevented).toBe(true);
	});
	test("focus wins over hovering a different content block", () => {
		const a = host(),
			b = host();
		focus = a;
		const calls: string[] = [];
		fire(
			a,
			(event) => {
				handleReadFromStartShortcut(event, b, true, () => calls.push("b"), "Home");
				handleReadFromStartShortcut(event, a, false, () => calls.push("a"), "Home");
			},
			home,
		);
		expect(calls).toEqual(["a"]);
	});
	test.each([
		"textarea",
		'div contenteditable="true"',
		'div class="monaco-editor"',
		'div class="xterm"',
	])("preserves Home in %s", (markup) => {
		const a = host();
		a.innerHTML = `<${markup}></${markup.split(" ")[0]}>`;
		const input = a.firstElementChild;
		if (!input) throw new Error("missing test input");
		const event = fire(
			input,
			(event) =>
				handleReadFromStartShortcut(
					event,
					a,
					true,
					() => {
						throw new Error("must not read");
					},
					"Home",
				),
			home,
		);
		expect(event.defaultPrevented).toBe(false);
	});
	test("disabled and targetless shortcuts leave browser behavior alone", () => {
		const a = host();
		for (const [hovered, shortcut] of [
			[false, "Home"],
			[true, ""],
		] as const) {
			const event = fire(
				document.body,
				(event) =>
					handleReadFromStartShortcut(
						event,
						a,
						hovered,
						() => {
							throw new Error("must not read");
						},
						shortcut,
					),
				home,
			);
			expect(event.defaultPrevented).toBe(false);
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
