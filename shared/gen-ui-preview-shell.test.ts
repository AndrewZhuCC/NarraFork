import { expect, test } from "bun:test";
import { createContext, runInContext } from "node:vm";
import {
	createPreviewShell,
	MAX_PREVIEW_HEIGHT,
	type PreviewShellEvent,
	readPreviewContentHeight,
} from "./gen-ui-preview-shell";

const theme = { colorScheme: "light" as const, tokenCss: "" };

test("inspection enforces offline CSP even for a project kit", () => {
	const source = { runtimeJs: "", runtimeCss: "" };
	const offline = createPreviewShell(source, theme, "failed", { kit: true, inspection: true });
	expect(offline).toContain("connect-src 'none'");
	expect(offline).toContain("worker-src 'none'");
	expect(offline).toContain("font-src 'none'");
	expect(offline).not.toContain("connect-src *");
	const normal = createPreviewShell(source, theme, "failed", { kit: true });
	expect(normal).toContain("connect-src *");
	expect(normal).toContain("worker-src blob:");
});

type ShellMessage =
	| PreviewShellEvent
	| { type: "narrafork:preview-height"; height: number }
	| { type: "narrafork:preview-focus-composer" };

/** Exercise the emitted bootstrap, holding commits, geometry and animation frames explicitly. */
function harness(inspection = false, markup = false, autoHeight = false) {
	const messages: ShellMessage[] = [];
	const listeners = new Map<string, ((event: Record<string, unknown>) => void)[]>();
	const frames: (() => void)[] = [];
	const effects: (() => void)[] = [];
	const components = new Map<string, () => unknown>();
	const states: unknown[] = [];
	let cursor = 0;
	const resized: (() => void)[] = [];
	const mutated: (() => void)[] = [];
	const geometry = {
		height: 80,
		popup: null as { top: number; bottom: number; height: number } | null,
	};
	const content = {
		get scrollHeight() {
			return geometry.height;
		},
		getBoundingClientRect: () => ({ top: 0, bottom: geometry.height }),
	};
	const popup = {
		get offsetHeight() {
			return geometry.popup?.height ?? 0;
		},
		getBoundingClientRect: () => geometry.popup,
		getClientRects: () => [{}],
	};
	class Element {
		constructor(private readonly protectedTarget = false) {}
		closest() {
			return this.protectedTarget ? this : null;
		}
	}
	const parent = { postMessage: (event: ShellMessage) => messages.push(event) };
	const listen = (name: string, fn: (event: Record<string, unknown>) => void) => {
		listeners.set(name, [...(listeners.get(name) ?? []), fn]);
	};
	const context = createContext({
		parent,
		TextEncoder,
		Element,
		innerWidth: 390,
		innerHeight: 640,
		scrollY: 0,
		ResizeObserver: class {
			constructor(fn: () => void) {
				resized.push(fn);
			}
			observe() {}
		},
		MutationObserver: class {
			constructor(fn: () => void) {
				mutated.push(fn);
			}
			observe() {}
		},
		performance: { now: () => 100 },
		requestAnimationFrame: (fn: () => void) => frames.push(fn),
		document: {
			getElementById: (id: string) =>
				id === "root" ? content : id === "failure" ? { hidden: true } : { closest: () => popup },
			querySelectorAll: () =>
				geometry.popup
					? [{ getAttribute: () => "popup", getBoundingClientRect: () => ({ bottom: 80 }) }]
					: [],
			createElement: () => ({ remove() {} }),
			head: { appendChild() {} },
			body: {},
			fonts: { ready: Promise.resolve() },
			documentElement: { dataset: {}, style: { overflowY: "" } },
			addEventListener: listen,
		},
		addEventListener: listen,
		__nfGenUiApp: () => null,
		__nfKit: {
			React: {
				Component: class {},
				createElement(type: string | (() => unknown), ...args: unknown[]) {
					if (typeof type === "function") components.set(type.name, type);
					return { type, args };
				},
				useEffect: (fn: () => void) => effects.push(fn),
				useState(initial: unknown) {
					const index = cursor++;
					if (!(index in states)) states[index] = initial;
					return [
						states[index],
						(next: unknown) => {
							states[index] = typeof next === "function" ? next(states[index]) : next;
						},
					];
				},
			},
			ReactDOMClient: { createRoot: () => ({ render() {} }) },
			components: {},
		},
	});
	context.window = context;
	const html = createPreviewShell({ runtimeJs: "", runtimeCss: "" }, theme, "failed", {
		kit: true,
		inspection,
		autoHeight,
		mode: markup ? "markup" : "app",
	});
	const script = html.match(/<script>([\s\S]*)<\/script>/)?.[1];
	if (!script) throw new Error("Missing shell bootstrap");
	runInContext(script, context);
	return {
		messages,
		context,
		states,
		geometry,
		element: (protectedTarget = false) => new Element(protectedTarget),
		resize: () => {
			for (const fn of resized) fn();
		},
		mutate: () => {
			for (const fn of mutated) fn();
		},
		emit(name: string, event: Record<string, unknown>) {
			for (const listener of listeners.get(name) ?? []) listener(event);
		},
		tree(nodes: unknown[]) {
			for (const listener of listeners.get("message") ?? [])
				listener({ source: parent, data: { type: "narrafork:ui-tree", nodes } });
		},
		commit() {
			cursor = 0;
			components.get(markup ? "Markup" : "AppCommitted")?.();
			for (const effect of effects.splice(0)) effect();
		},
		frame() {
			for (const fn of frames.splice(0)) fn();
		},
	};
}

test("content heights are finite, positive and bounded before reaching the host", () => {
	expect(readPreviewContentHeight(80.2)).toBe(81);
	expect(readPreviewContentHeight(MAX_PREVIEW_HEIGHT)).toBe(MAX_PREVIEW_HEIGHT);
	for (const value of [
		null,
		"80",
		Number.NaN,
		Number.POSITIVE_INFINITY,
		0,
		-1,
		MAX_PREVIEW_HEIGHT + 1,
	])
		expect(readPreviewContentHeight(value)).toBeNull();
});

test("auto height restores after a Select popup closes and follows mobile width changes", () => {
	const shell = harness(false, true, true);
	const heights = () =>
		shell.messages
			.filter((event) => event.type === "narrafork:preview-height")
			.map((event) => event.height);
	shell.frame();
	expect(heights()).toEqual([80]);
	expect(shell.context.document.documentElement.style.overflowY).toBe("hidden");
	shell.geometry.popup = { top: -20, bottom: 120, height: 140 };
	shell.mutate();
	shell.frame();
	expect(heights()).toEqual([80, 228]);
	shell.geometry.popup = null;
	shell.mutate();
	shell.frame();
	expect(heights()).toEqual([80, 228, 80]);
	shell.context.innerWidth = 320;
	shell.geometry.height = 120;
	shell.emit("resize", {});
	shell.frame();
	expect(heights()).toEqual([80, 228, 80, 120]);
	shell.geometry.height = MAX_PREVIEW_HEIGHT + 1;
	shell.mutate();
	shell.frame();
	expect(heights().at(-1)).toBe(MAX_PREVIEW_HEIGHT);
	expect(shell.context.document.documentElement.style.overflowY).toBe("auto");
});

test("auto height ignores feedback from a viewport-sized layout", () => {
	const shell = harness(false, true, true);
	shell.geometry.height = 640;
	shell.frame();
	expect(shell.messages).toEqual([{ type: "narrafork:preview-height", height: 640 }]);
	shell.context.innerHeight = 700;
	shell.geometry.height = 700;
	shell.resize();
	shell.frame();
	expect(shell.messages).toEqual([{ type: "narrafork:preview-height", height: 640 }]);
});

test("Tab focus bridge preserves editable targets, modified keys and inspection mode", () => {
	for (const inspection of [false, true]) {
		const shell = harness(inspection);
		const tab = { isTrusted: true, key: "Tab", target: shell.element() };
		for (const override of [
			{ isTrusted: false },
			{ defaultPrevented: true },
			{ key: "Enter" },
			{ repeat: true },
			{ isComposing: true },
			{ keyCode: 229 },
			{ ctrlKey: true },
			{ metaKey: true },
			{ altKey: true },
			{ shiftKey: true },
			{ target: shell.element(true) },
		])
			shell.emit("keydown", { ...tab, ...override });
		expect(shell.messages).toEqual([]);
		shell.emit("keydown", tab);
		expect(shell.messages).toEqual(
			inspection ? [] : [{ type: "narrafork:preview-focus-composer" }],
		);
	}
});

test("markup buffers the latest pre-commit tree and reports paint after commit", () => {
	const shell = harness(true, true);
	shell.tree([{ $node: "text", text: "Earlier" }]);
	const latest = [{ $node: "text", text: "Latest" }];
	shell.tree(latest);
	expect(shell.messages).toEqual([]);
	shell.commit();
	expect(shell.states[0]).toEqual({ nodes: latest, revision: 1 });
	expect(shell.messages).toEqual([{ type: "narrafork:ui-ready" }]);
	shell.frame();
	expect(shell.messages).toHaveLength(1);
	shell.frame();
	expect(shell.messages.at(-1)?.type).toBe("narrafork:preview-painted");
});

test("app painted waits for commit and two frames, never root.render return", () => {
	const shell = harness();
	expect(shell.messages).toEqual([]);
	shell.commit();
	expect(shell.messages).toEqual([{ type: "narrafork:ui-ready" }]);
	shell.frame();
	expect(shell.messages).toHaveLength(1);
	shell.frame();
	expect(shell.messages.at(-1)?.type).toBe("narrafork:preview-painted");
});

test("markup handshake precedes host tree, initial empty commit is not painted", () => {
	const shell = harness(true, true);
	shell.commit();
	shell.frame();
	shell.frame();
	expect(shell.messages).toEqual([{ type: "narrafork:ui-ready" }]);
	shell.tree([]);
	shell.commit();
	shell.frame();
	shell.frame();
	expect(shell.messages.at(-1)?.type).toBe("narrafork:preview-painted");
});

test("runtime errors suppress successful paint even after offline warnings", () => {
	const shell = harness(true);
	shell.commit();
	shell.emit("securitypolicyviolation", {
		effectiveDirective: "connect-src",
		blockedURI: "https://invalid",
	});
	for (let index = 0; index < 30; index++)
		shell.emit("error", { error: new Error("x".repeat(4000)) });
	shell.frame();
	shell.frame();
	const diagnostics = shell.messages.filter(
		(event) => event.type === "narrafork:preview-diagnostic",
	);
	expect(diagnostics.length).toBeLessThanOrEqual(20);
	expect(diagnostics[0].kind).toBe("csp");
	expect(diagnostics[0].severity).toBe("warning");
	expect(diagnostics[1].severity).toBe("error");
	expect(diagnostics.every((event) => Buffer.byteLength(event.message) <= 2048)).toBe(true);
	expect(
		diagnostics.reduce((sum, event) => sum + Buffer.byteLength(event.message), 0),
	).toBeLessThanOrEqual(16384);
	expect(shell.messages.some((event) => event.type === "narrafork:preview-painted")).toBe(false);
});

test("offline resource warnings preserve painted success and reserve error reporting capacity", () => {
	const shell = harness(true);
	shell.commit();
	for (let index = 0; index < 100; index++)
		shell.emit("securitypolicyviolation", {
			effectiveDirective: "img-src",
			blockedURI: "错误😀".repeat(1000),
		});
	shell.frame();
	shell.frame();
	expect(shell.messages.some((event) => event.type === "narrafork:preview-painted")).toBe(true);
	shell.emit("unhandledrejection", { reason: new Error("actual runtime failure") });
	const diagnostics = shell.messages.filter(
		(event) => event.type === "narrafork:preview-diagnostic",
	);
	expect(diagnostics.at(-1)).toMatchObject({
		severity: "error",
		message: "actual runtime failure",
	});
	expect(diagnostics.length).toBeLessThanOrEqual(20);
	expect(diagnostics.every((event) => Buffer.byteLength(event.message) <= 2048)).toBe(true);
	expect(
		diagnostics.reduce((sum, event) => sum + Buffer.byteLength(event.message), 0),
	).toBeLessThanOrEqual(16384);
});

test("inspection never forwards actions even following a trusted gesture", () => {
	for (const inspection of [false, true]) {
		const shell = harness(inspection);
		shell.emit("click", { isTrusted: true });
		runInContext('genUI.sendPrompt("hello")', shell.context);
		expect(
			shell.messages.some((event) => (event as { type: string }).type === "narrafork:ui-action"),
		).toBe(!inspection);
	}
});
