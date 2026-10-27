import { describe, expect, test } from "bun:test";
import { MantineProvider } from "@mantine/core";
import { parseHTML } from "linkedom";
import { renderToStaticMarkup } from "react-dom/server";
import { MarkdownContent } from "../markdown/MarkdownContent";
import { readRenderedFileSelection } from "./rendered-file-selection";

function documentFor(source: string) {
	const html = renderToStaticMarkup(
		<MantineProvider>
			<div id="preview">
				<MarkdownContent text={source} sourceLines />
			</div>
		</MantineProvider>,
	);
	const { document } = parseHTML(`<html><body>${html}<p id="outside">outside</p></body></html>`);
	return {
		document,
		root: document.getElementById("preview") as HTMLDivElement,
		lines: source.split(/\r\n|\r|\n/),
	};
}

function leaf(element: Node, end = false): Node {
	let node = element;
	while (end ? node.lastChild : node.firstChild)
		node = (end ? node.lastChild : node.firstChild) as Node;
	return node;
}

function select(
	start: Node,
	text: string,
	end = start,
	startOffset = 0,
	endOffset = end.textContent?.length ?? 0,
): Selection {
	return {
		isCollapsed: false,
		rangeCount: 1,
		getRangeAt: () => ({
			startContainer: start,
			startOffset,
			endContainer: end,
			endOffset,
			collapsed: false,
			toString: () => text,
		}),
	} as unknown as Selection;
}

function whole(element: Element): Selection {
	return select(leaf(element), element.textContent ?? "", leaf(element, true));
}

function lineRange(lines: string[], start: number, end = start) {
	return {
		startLineNumber: start,
		startColumn: 1,
		endLineNumber: end,
		endColumn: lines[end - 1].length + 1,
	};
}

describe("rendered Markdown file selection", () => {
	test("real Markdown positions preserve leading blank lines and CRLF", () => {
		const { root, lines } = documentFor(
			"\r\n\r\n# 标题😀\r\n\r\n中文 **加粗** and [link](guide.md)\r\n",
		);
		const heading = root.querySelector("h1")!;
		expect(heading.getAttribute("data-line")).toBe("2");
		expect(readRenderedFileSelection(root, whole(heading), lines)).toEqual(lineRange(lines, 3));
		expect(readRenderedFileSelection(root, whole(root.querySelector("p")!), lines)).toEqual(
			lineRange(lines, 5),
		);
	});

	test("identical paragraphs resolve by DOM source anchors, never the first text occurrence", () => {
		const { root, lines } = documentFor("same sentence\n\nsame sentence");
		const paragraphs = root.querySelectorAll("p");
		expect(readRenderedFileSelection(root, whole(paragraphs[1]), lines)).toEqual(
			lineRange(lines, 3),
		);
	});

	test("unique literal text refines a soft-wrapped paragraph to its actual source line", () => {
		const { root, lines } = documentFor("first soft line\nsecond soft line\nthird soft line");
		const node = root.querySelector("p")!.firstChild!;
		const start = node.textContent!.indexOf("second");
		expect(
			readRenderedFileSelection(
				root,
				select(node, "second soft line", node, start, start + 16),
				lines,
			),
		).toEqual(lineRange(lines, 2));
	});

	test("list items and table cells carry their own source ranges", () => {
		const { root, lines } = documentFor(
			"- first\n- **second**\n\n| h | value |\n| - | - |\n| x | y |\n| a | b |",
		);
		expect(readRenderedFileSelection(root, whole(root.querySelectorAll("li")[1]), lines)).toEqual(
			lineRange(lines, 2),
		);
		expect(
			readRenderedFileSelection(root, whole(root.querySelectorAll("tbody td")[3]), lines),
		).toEqual(lineRange(lines, 7));
	});

	test("fenced code refines to selected source lines without quoting the language/copy toolbar", () => {
		const { root, lines } = documentFor("# head\n\n```ts\nconst alpha = 1;\nconst beta = 2;\n```");
		const code = root.querySelector("[data-file-quote-code] pre")!;
		const node = leaf(code);
		const text = "const beta = 2;";
		const at = node.textContent!.indexOf(text);
		expect(
			readRenderedFileSelection(root, select(node, text, node, at, at + text.length), lines),
		).toEqual(lineRange(lines, 5));
		const language = root.querySelector("[data-file-quote-ignore]")!;
		expect(readRenderedFileSelection(root, whole(language), lines)).toBeNull();
	});

	test("cross-paragraph selections include the proven source interval", () => {
		const { root, lines } = documentFor("**first** paragraph\n\nlast [paragraph](guide.md)");
		const paragraphs = root.querySelectorAll("p");
		const selection = select(
			leaf(paragraphs[0]),
			"first paragraph\n\nlast paragraph",
			leaf(paragraphs[1], true),
		);
		expect(readRenderedFileSelection(root, selection, lines)).toEqual(lineRange(lines, 1, 3));
	});

	test("an exclusive end at the next paragraph does not quote that paragraph", () => {
		const { root, lines } = documentFor("first\n\nsecond");
		const paragraphs = root.querySelectorAll("p");
		expect(
			readRenderedFileSelection(
				root,
				select(leaf(paragraphs[0]), "first", leaf(paragraphs[1]), 0, 0),
				lines,
			),
		).toEqual(lineRange(lines, 1));
	});

	test("cross-preview, collapsed, synthetic and invalid source selections are rejected", () => {
		const { document, root, lines } = documentFor("paragraph");
		const paragraph = root.querySelector("p")!;
		const node = leaf(paragraph);
		expect(readRenderedFileSelection(root, null, lines)).toBeNull();
		expect(
			readRenderedFileSelection(root, { isCollapsed: true, rangeCount: 0 } as Selection, lines),
		).toBeNull();
		expect(
			readRenderedFileSelection(
				root,
				select(node, "paragraph outside", leaf(document.getElementById("outside")!)),
				lines,
			),
		).toBeNull();
		const formula = document.createElement("span");
		formula.className = "katex";
		formula.textContent = "synthetic";
		paragraph.append(formula);
		expect(readRenderedFileSelection(root, whole(formula), lines)).toBeNull();
		paragraph.dataset.lineEnd = "10000";
		expect(readRenderedFileSelection(root, select(node, "paragraph"), lines)).toBeNull();
	});
});
