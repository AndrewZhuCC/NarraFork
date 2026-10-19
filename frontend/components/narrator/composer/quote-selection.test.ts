import { describe, expect, test } from "bun:test";
import { NodeFilter, parseHTML } from "linkedom";
import {
	describeQuoteEndpoint,
	type QuoteSourceLabels,
	readPanelQuoteSelection,
} from "./quote-selection";
import { editorSelectionCandidate } from "./use-quote-candidate";

const labels: QuoteSourceLabels = {
	assistant: "Assistant",
	user: "User",
	message: "Message",
	merged: (count) => `${count} blocks`,
};

function dom() {
	const { document } = parseHTML(`<!doctype html><html><body>
		<div data-narrator-panel id="a">
			<div data-message-id="m1" data-quote-role="assistant">
				<div data-content-block id="text"><p id="para">Hello</p></div>
				<div data-content-block data-quote-tool="Bash" data-quote-tool-summary="  ls   -la
					${"x".repeat(80)}"><pre id="tool">out</pre></div>
			</div>
			<div data-message-id="m2" data-quote-role="user"><span id="user">Hi</span></div>
			<textarea id="input"></textarea>
		</div>
		<div data-narrator-panel id="b">
			<div data-message-id="m3"><span id="other">Elsewhere</span></div>
		</div>
	</body></html>`);
	return document;
}

function fakeSelection(anchor: Node | null, focus: Node | null, collapsed = false): Selection {
	return {
		anchorNode: anchor,
		focusNode: focus,
		isCollapsed: collapsed,
		rangeCount: 1,
		getRangeAt: () => {
			throw new Error("range should not be read for rejected selections");
		},
	} as unknown as Selection;
}

describe("describeQuoteEndpoint", () => {
	test("labels message roles", () => {
		const document = dom();
		expect(describeQuoteEndpoint(document.getElementById("para")!, labels)).toBe("Assistant");
		expect(describeQuoteEndpoint(document.getElementById("user")!, labels)).toBe("User");
		expect(describeQuoteEndpoint(document.getElementById("other")!, labels)).toBe("Message");
	});

	test("labels tool output with a whitespace-collapsed, clipped summary", () => {
		const label = describeQuoteEndpoint(dom().getElementById("tool")!, labels);
		expect(label.startsWith("Bash · ls -la x")).toBe(true);
		expect(label.endsWith("…")).toBe(true);
		expect(label.length).toBe("Bash · ".length + 48);
	});
});

describe("readPanelQuoteSelection", () => {
	test("names a quote from its text and a file tool from the file", () => {
		const { document: doc, window } = parseHTML(`<!doctype html><html><body>
			<div data-narrator-panel id="a">
				<div data-message-id="m9abcd" data-quote-role="assistant">
					<div data-content-block><span id="para">Hello there friend</span></div>
				</div>
				<div data-message-id="m1" data-quote-tool="Read" data-quote-tool-id="tool1234" data-quote-file="/work/biome.json">
					<div data-content-block data-quote-part="result"><span id="out">"formatter": true</span></div>
				</div>
				<div data-message-id="m2" data-quote-tool="Write" data-quote-tool-id="tool5678" data-quote-file="src/app.ts:4">
					<div data-content-block data-quote-part="input"><span id="inp">const answer = 1</span></div>
				</div>
			</div>
		</body></html>`);
		const globals = globalThis as {
			window?: unknown;
			Node?: unknown;
			NodeFilter?: unknown;
		};
		const previous = {
			document: globalThis.document,
			window: globals.window,
			Node: globals.Node,
			NodeFilter: globals.NodeFilter,
		};
		window.getComputedStyle = () =>
			({ display: "block", visibility: "visible" }) as unknown as CSSStyleDeclaration;
		Object.defineProperty(globalThis, "document", { configurable: true, value: doc });
		Object.defineProperty(globalThis, "window", { configurable: true, value: window });
		Object.defineProperty(globalThis, "Node", { configurable: true, value: window.Node });
		Object.defineProperty(globalThis, "NodeFilter", { configurable: true, value: NodeFilter });
		try {
			const select = (id: string) => {
				const node = doc.getElementById(id)?.firstChild;
				if (!node) throw new Error(id);
				const range = doc.createRange();
				range.selectNodeContents(node);
				Object.defineProperty(range, "startContainer", { value: node });
				Object.defineProperty(range, "endContainer", { value: node });
				Object.defineProperty(range, "startOffset", { value: 0 });
				Object.defineProperty(range, "endOffset", { value: node.textContent?.length ?? 0 });
				return {
					anchorNode: node,
					focusNode: node,
					isCollapsed: false,
					rangeCount: 1,
					getRangeAt: () => range,
				} as unknown as Selection;
			};
			const panel = doc.getElementById("a");
			if (!panel) throw new Error("panel");
			const message = readPanelQuoteSelection(panel, select("para"));
			expect(message?.label).toBe("Hello there friend");
			expect(message?.anchor).toBe("m:m9abcd");
			expect(message?.text).toBe("Hello there friend");
			const result = readPanelQuoteSelection(panel, select("out"));
			expect(result?.label.startsWith("biome.json · ")).toBe(true);
			expect(result?.text.startsWith('biome.json\n"formatter"')).toBe(true);
			expect(result?.anchor).toBe("t:tool1234");
			const input = readPanelQuoteSelection(panel, select("inp"));
			expect(input?.label.startsWith("app.ts · ")).toBe(true);
			expect(input?.anchor).toBe("t:tool5678");
		} finally {
			Object.defineProperty(globalThis, "document", {
				configurable: true,
				value: previous.document,
			});
			if (previous.window)
				Object.defineProperty(globalThis, "window", { configurable: true, value: previous.window });
			else Reflect.deleteProperty(globalThis, "window");
			if (previous.Node)
				Object.defineProperty(globalThis, "Node", { configurable: true, value: previous.Node });
			else Reflect.deleteProperty(globalThis, "Node");
			if (previous.NodeFilter)
				Object.defineProperty(globalThis, "NodeFilter", {
					configurable: true,
					value: previous.NodeFilter,
				});
			else Reflect.deleteProperty(globalThis, "NodeFilter");
		}
	});

	test("anchors a backwards selection at the document-order start", () => {
		const { document: doc, window } = parseHTML(`<!doctype html><html><body>
			<div data-narrator-panel id="panel">
				<div data-message-id="msg-early"><span id="early">alpha sentence starts</span></div>
				<div data-message-id="msg-later" data-quote-tool="Read" data-quote-tool-id="tool9999" data-quote-file="/work/biome.json">
					<div data-quote-part="result"><span id="later">omega sentence ends</span></div>
				</div>
			</div>
			<div data-narrator-panel id="file-first">
				<div data-message-id="msg-tool" data-quote-tool="Read" data-quote-tool-id="tool9999" data-quote-file="/work/biome.json">
					<div data-quote-part="result"><span id="file">"formatter": true</span></div>
				</div>
				<div data-message-id="msg-after"><span id="after">after the file</span></div>
			</div>
		</body></html>`);
		const globals = globalThis as { window?: unknown; Node?: unknown; NodeFilter?: unknown };
		const previous = {
			document: globalThis.document,
			window: globals.window,
			Node: globals.Node,
			NodeFilter: globals.NodeFilter,
		};
		window.getComputedStyle = () =>
			({ display: "block", visibility: "visible" }) as unknown as CSSStyleDeclaration;
		Object.defineProperty(globalThis, "document", { configurable: true, value: doc });
		Object.defineProperty(globalThis, "window", { configurable: true, value: window });
		Object.defineProperty(globalThis, "Node", { configurable: true, value: window.Node });
		Object.defineProperty(globalThis, "NodeFilter", { configurable: true, value: NodeFilter });
		try {
			const across = (rootId: string, startId: string, endId: string) => {
				const start = doc.getElementById(startId)?.firstChild;
				const end = doc.getElementById(endId)?.firstChild;
				if (!start || !end) throw new Error(`${startId}/${endId}`);
				const range = {
					startContainer: start,
					endContainer: end,
					startOffset: 0,
					endOffset: end.textContent?.length ?? 0,
					commonAncestorContainer: doc.getElementById(rootId),
					intersectsNode: () => true,
				};
				return {
					anchorNode: end,
					focusNode: start,
					isCollapsed: false,
					rangeCount: 1,
					getRangeAt: () => range,
				} as unknown as Selection;
			};
			const panel = doc.getElementById("panel");
			const filePanel = doc.getElementById("file-first");
			if (!panel || !filePanel) throw new Error("panel");
			const messageFirst = readPanelQuoteSelection(panel, across("panel", "early", "later"));
			expect(messageFirst?.anchor).toBe("m:msg-early");
			expect(messageFirst?.label.includes("biome.json")).toBe(false);
			expect(messageFirst?.text.includes("alpha sentence starts")).toBe(true);
			const fileFirst = readPanelQuoteSelection(filePanel, across("file-first", "file", "after"));
			expect(fileFirst?.anchor).toBe("t:tool9999");
			expect(fileFirst?.label.startsWith("biome.json · ")).toBe(true);
		} finally {
			Object.defineProperty(globalThis, "document", {
				configurable: true,
				value: previous.document,
			});
			if (previous.window)
				Object.defineProperty(globalThis, "window", { configurable: true, value: previous.window });
			else Reflect.deleteProperty(globalThis, "window");
			if (previous.Node)
				Object.defineProperty(globalThis, "Node", { configurable: true, value: previous.Node });
			else Reflect.deleteProperty(globalThis, "Node");
			if (previous.NodeFilter)
				Object.defineProperty(globalThis, "NodeFilter", {
					configurable: true,
					value: previous.NodeFilter,
				});
			else Reflect.deleteProperty(globalThis, "NodeFilter");
		}
	});

	test("rejects collapsed, foreign-panel and editable selections", () => {
		const document = dom();
		const panel = document.getElementById("a")!;
		const para = document.getElementById("para")!.firstChild;
		const other = document.getElementById("other")!.firstChild;
		const input = document.getElementById("input");
		expect(readPanelQuoteSelection(panel, null)).toBeNull();
		expect(readPanelQuoteSelection(panel, fakeSelection(para, para, true))).toBeNull();
		expect(readPanelQuoteSelection(panel, fakeSelection(other, other))).toBeNull();
		expect(readPanelQuoteSelection(panel, fakeSelection(para, other))).toBeNull();
		expect(readPanelQuoteSelection(panel, fakeSelection(input, input))).toBeNull();
	});
});

describe("editorSelectionCandidate", () => {
	const target = { deviceId: "local", path: "/w/src/a.ts" };
	const range = { startLineNumber: 3, startColumn: 1, endLineNumber: 5, endColumn: 4 };

	test("ignores empty or caret-only selections", () => {
		expect(editorSelectionCandidate(null)).toBeNull();
		expect(
			editorSelectionCandidate({
				target: {
					...target,
					selection: { startLineNumber: 2, startColumn: 3, endLineNumber: 2, endColumn: 3 },
				},
				label: "src/a.ts",
				expectedHash: "h1",
				dirty: false,
			}),
		).toBeNull();
	});

	test("labels saved selections with path and lines, and withholds dirty ones", () => {
		const saved = editorSelectionCandidate({
			target: { ...target, selection: range },
			label: "src/a.ts",
			expectedHash: "h1",
			dirty: false,
		});
		expect(saved?.label).toContain("src/a.ts");
		expect(saved?.label).toContain("3");
		expect(saved?.label).toContain("5");
		expect(saved?.reference?.path).toBe("/w/src/a.ts");
		const dirty = editorSelectionCandidate({
			target: { ...target, selection: range },
			label: "src/a.ts",
			expectedHash: "h1",
			dirty: true,
		});
		expect(dirty?.reference).toBeNull();
		expect(dirty?.key).toBe(saved?.key ?? "");
	});
});
