import { describe, expect, test } from "bun:test";
import { parseHTML } from "linkedom";
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
	test("rejects collapsed, foreign-panel and editable selections", () => {
		const document = dom();
		const panel = document.getElementById("a")!;
		const para = document.getElementById("para")!.firstChild;
		const other = document.getElementById("other")!.firstChild;
		const input = document.getElementById("input");
		expect(readPanelQuoteSelection(panel, null, labels)).toBeNull();
		expect(readPanelQuoteSelection(panel, fakeSelection(para, para, true), labels)).toBeNull();
		expect(readPanelQuoteSelection(panel, fakeSelection(other, other), labels)).toBeNull();
		expect(readPanelQuoteSelection(panel, fakeSelection(para, other), labels)).toBeNull();
		expect(readPanelQuoteSelection(panel, fakeSelection(input, input), labels)).toBeNull();
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
