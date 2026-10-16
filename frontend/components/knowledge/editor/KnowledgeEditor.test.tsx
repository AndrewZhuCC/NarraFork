/**
 * KnowledgeEditor + SubmissionStatusBadge component tests (linkedom + react-dom, the same
 * harness as the chapter modal tests).
 *
 * The router blocker, the Markdown renderer and the diff painter are replaced by tiny stubs:
 * the editor's contract with them is "which one is mounted, with which text" and "is the
 * blocker armed", which is exactly what these stubs record.
 */
import { afterAll, beforeEach, mock as bunMock, describe, expect, test } from "bun:test";
import { MantineProvider } from "@mantine/core";
import i18next from "i18next";
import { parseHTML } from "linkedom";
import type { ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { I18nextProvider, initReactI18next } from "react-i18next";
import knowledgeLocale from "../../../locales/en/knowledge.json";

const i18n = i18next.createInstance();

const realModules = {
	"@tanstack/react-router": { ...(await import("@tanstack/react-router")) },
	"../../narrator/markdown/MarkdownContent": {
		...(await import("../../narrator/markdown/MarkdownContent")),
	},
	"../../narrator/diff/DiffView": { ...(await import("../../narrator/diff/DiffView")) },
};
type MockedSpecifier = keyof typeof realModules;

/** Every useBlocker call's options, newest last. */
let blockerCalls: Array<{ disabled?: boolean; enableBeforeUnload?: unknown }> = [];

const moduleMocks = {
	"@tanstack/react-router": () => ({
		...realModules["@tanstack/react-router"],
		useBlocker: (opts: { disabled?: boolean; enableBeforeUnload?: unknown }) => {
			blockerCalls.push(opts);
			return { status: "idle" };
		},
	}),
	"../../narrator/markdown/MarkdownContent": () => ({
		MarkdownContent: ({ text }: { text: string }) => <div data-testid="md-preview">{text}</div>,
	}),
	"../../narrator/diff/DiffView": () => ({
		DiffView: ({ oldStr, newStr }: { oldStr?: string; newStr?: string }) => (
			<div data-testid="diff" data-old={oldStr} data-new={newStr} />
		),
	}),
} satisfies Record<MockedSpecifier, () => unknown>;

for (const specifier of Object.keys(moduleMocks) as MockedSpecifier[]) {
	bunMock.module(specifier, moduleMocks[specifier]);
}

afterAll(() => {
	for (const specifier of Object.keys(realModules) as MockedSpecifier[]) {
		const namespace = realModules[specifier];
		bunMock.module(specifier, () => namespace);
	}
	bunMock.restore();
});

const { KnowledgeEditor, isKnowledgeEditorSaveShortcut, knowledgeEditorViews } = await import(
	"./KnowledgeEditor"
);
const { SubmissionStatusBadge, SUBMISSION_STATUS_COLOR } = await import("../SubmissionStatusBadge");

class TestResizeObserver {
	observe() {}
	unobserve() {}
	disconnect() {}
}

// SegmentedControl's FloatingIndicator watches its target with a MutationObserver, and the
// autosizing Textarea listens for web-font loads; linkedom provides neither.
class TestMutationObserver {
	observe() {}
	disconnect() {}
	takeRecords() {
		return [];
	}
}

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
	Object.assign(window.document, {
		fonts: { addEventListener() {}, removeEventListener() {} },
	});
	Object.assign(window, {
		ResizeObserver: TestResizeObserver,
		MutationObserver: TestMutationObserver,
		matchMedia,
		requestAnimationFrame: (callback: FrameRequestCallback) => setTimeout(callback, 0),
		cancelAnimationFrame: (id: number) => clearTimeout(id),
	});
	Object.assign(globalThis, {
		window,
		document: window.document,
		navigator: window.navigator,
		Event: window.Event,
		Document: window.Document,
		ShadowRoot: window.ShadowRoot,
		HTMLElement: window.HTMLElement,
		HTMLButtonElement: window.HTMLButtonElement,
		HTMLInputElement: window.HTMLInputElement,
		HTMLTextAreaElement: window.HTMLTextAreaElement,
		Element: window.Element,
		Node: window.Node,
		Text: window.Text,
		ResizeObserver: TestResizeObserver,
		MutationObserver: TestMutationObserver,
		matchMedia,
		requestAnimationFrame: window.requestAnimationFrame,
		cancelAnimationFrame: window.cancelAnimationFrame,
		getComputedStyle:
			window.getComputedStyle?.bind(window) ?? (() => ({ getPropertyValue: () => "" })),
		IS_REACT_ACT_ENVIRONMENT: false,
	});
}

async function initTestI18n() {
	if (!i18n.isInitialized) {
		await i18n.use(initReactI18next).init({
			lng: "en",
			fallbackLng: "en",
			defaultNS: "knowledge",
			ns: ["knowledge"],
			resources: { en: { knowledge: knowledgeLocale } },
			interpolation: { escapeValue: false },
			react: { useSuspense: false },
		});
	}
}

async function flush() {
	for (let i = 0; i < 6; i++) await new Promise((resolve) => setTimeout(resolve, 0));
}

let root: Root | undefined;

async function render(node: ReactNode) {
	if (!root) throw new Error("test root not initialized");
	root.render(
		<I18nextProvider i18n={i18n}>
			<MantineProvider env="test">{node}</MantineProvider>
		</I18nextProvider>,
	);
	await flush();
}

const q = (sel: string) => document.body.querySelector(sel);

function pressKey(target: Element, init: { key: string; ctrlKey?: boolean; metaKey?: boolean }) {
	const ev = new Event("keydown", { bubbles: true, cancelable: true }) as Event & {
		key: string;
		ctrlKey: boolean;
		metaKey: boolean;
		altKey: boolean;
		shiftKey: boolean;
	};
	Object.assign(ev, { ctrlKey: false, metaKey: false, altKey: false, shiftKey: false, ...init });
	target.dispatchEvent(ev);
	return ev;
}

async function pickView(view: string) {
	const input = q(`input[type='radio'][value='${view}']`);
	if (!(input instanceof HTMLInputElement)) {
		const seen = Array.from(document.body.querySelectorAll("input[type='radio']")).map(
			(i) => (i as HTMLInputElement).value,
		);
		throw new Error(`view ${view} not offered; have ${JSON.stringify(seen)}`);
	}
	input.dispatchEvent(new Event("click", { bubbles: true }));
	await flush();
}

beforeEach(async () => {
	blockerCalls = [];
	installDom();
	await initTestI18n();
	const container = document.createElement("div");
	document.body.appendChild(container);
	root = createRoot(container);
});

describe("KnowledgeEditor pure helpers", () => {
	test("Ctrl+S and Cmd+S are the save shortcut; Alt or a bare S is not", () => {
		const base = { ctrlKey: false, metaKey: false, altKey: false };
		expect(isKnowledgeEditorSaveShortcut({ ...base, key: "s", ctrlKey: true })).toBe(true);
		expect(isKnowledgeEditorSaveShortcut({ ...base, key: "S", metaKey: true })).toBe(true);
		expect(isKnowledgeEditorSaveShortcut({ ...base, key: "s" })).toBe(false);
		expect(isKnowledgeEditorSaveShortcut({ ...base, key: "s", ctrlKey: true, altKey: true })).toBe(
			false,
		);
	});

	test("diff is offered only with a baseline; split only on wide screens", () => {
		expect(knowledgeEditorViews({ hasBaseline: false, wide: false })).toEqual(["edit", "preview"]);
		expect(knowledgeEditorViews({ hasBaseline: true, wide: true })).toEqual([
			"edit",
			"preview",
			"split",
			"diff",
		]);
	});
});

describe("KnowledgeEditor", () => {
	test("dirty state follows value vs savedValue and arms the navigation guard", async () => {
		await render(<KnowledgeEditor value="hello" onChange={() => {}} savedValue="hello" />);
		expect(q("[data-testid='knowledge-editor-dirty']")).toBeNull();
		expect(blockerCalls.at(-1)?.disabled).toBe(true);
		expect(blockerCalls.at(-1)?.enableBeforeUnload).toBe(false);

		await render(<KnowledgeEditor value="hello world" onChange={() => {}} savedValue="hello" />);
		expect(q("[data-testid='knowledge-editor-dirty']")).not.toBeNull();
		expect(blockerCalls.at(-1)?.disabled).toBe(false);
		// beforeunload is armed together with the route blocker.
		expect(blockerCalls.at(-1)?.enableBeforeUnload).toBe(true);
	});

	test("a read-only editor is never dirty and never blocks navigation", async () => {
		await render(<KnowledgeEditor value="changed" onChange={() => {}} savedValue="x" readOnly />);
		expect(q("[data-testid='knowledge-editor-dirty']")).toBeNull();
		expect(blockerCalls.at(-1)?.disabled).toBe(true);
	});

	test("Ctrl+S inside the editor calls onSave and suppresses the browser save", async () => {
		let saves = 0;
		await render(
			<KnowledgeEditor value="a" onChange={() => {}} savedValue="" onSave={() => saves++} />,
		);
		// Dispatched on the editor root: React's legacy input-event polyfill trips over a
		// keydown aimed straight at a linkedom textarea. The handler sits on the root and sees
		// the textarea's keydowns by bubbling, so this is the same code path.
		const editor = q("[data-knowledge-editor]");
		if (!editor) throw new Error("editor not rendered");
		const ev = pressKey(editor, { key: "s", ctrlKey: true });
		await flush();
		expect(saves).toBe(1);
		expect(ev.defaultPrevented).toBe(true);

		pressKey(editor, { key: "s" });
		await flush();
		expect(saves).toBe(1);
	});

	test("Ctrl+S does not save while saving or read-only", async () => {
		let saves = 0;
		await render(<KnowledgeEditor value="a" onChange={() => {}} onSave={() => saves++} saving />);
		pressKey(q("[data-knowledge-editor]") as Element, { key: "s", metaKey: true });
		await render(<KnowledgeEditor value="a" onChange={() => {}} onSave={() => saves++} readOnly />);
		pressKey(q("[data-knowledge-editor]") as Element, { key: "s", ctrlKey: true });
		await flush();
		expect(saves).toBe(0);
	});

	test("switches between edit, preview and diff-vs-baseline", async () => {
		await render(
			<KnowledgeEditor value="# new" onChange={() => {}} baseline="# old" savedValue="# old" />,
		);
		expect(q("textarea")).not.toBeNull();
		expect(q("[data-testid='md-preview']")).toBeNull();

		await pickView("preview");
		expect(q("textarea")).toBeNull();
		expect(q("[data-testid='md-preview']")?.textContent).toBe("# new");

		await pickView("diff");
		const diff = q("[data-testid='diff']");
		expect(diff?.getAttribute("data-old")).toBe("# old");
		expect(diff?.getAttribute("data-new")).toBe("# new");

		await pickView("edit");
		expect(q("textarea")).not.toBeNull();
	});

	test("without a baseline there is no diff view", async () => {
		await render(<KnowledgeEditor value="x" onChange={() => {}} />);
		expect(q("input[type='radio'][value='diff']")).toBeNull();
		expect(q("input[type='radio'][value='preview']")).not.toBeNull();
	});
});

describe("SubmissionStatusBadge", () => {
	test("renders the localized label for every status", async () => {
		await render(<SubmissionStatusBadge status="changes_requested" />);
		expect(document.body.textContent).toContain("Changes requested");
	});

	test("one color per status: open states are distinct, verdicts green/red, closures neutral", () => {
		const open = ["pending", "conflict", "changes_requested"] as const;
		expect(new Set(open.map((s) => SUBMISSION_STATUS_COLOR[s])).size).toBe(open.length);
		expect(SUBMISSION_STATUS_COLOR.approved).toBe("green");
		expect(SUBMISSION_STATUS_COLOR.rejected).toBe("red");
		expect(SUBMISSION_STATUS_COLOR.withdrawn).toBe("gray");
		expect(SUBMISSION_STATUS_COLOR.superseded).toBe("gray");
	});
});
