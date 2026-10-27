import { type FileSelection, MAX_FILE_REFERENCE_POSITION } from "@shared/file-reference";

const SOURCE = "[data-line][data-line-end]";
const SYNTHETIC = "button, input, textarea, select, svg, canvas, .katex, [data-file-quote-ignore]";

function adjacentLeaf(node: Node, root: HTMLElement, backward: boolean): Node | null {
	let current: Node | null = node;
	while (current && current !== root) {
		const sibling = backward ? current.previousSibling : current.nextSibling;
		if (sibling) {
			let leaf: Node = sibling;
			let child = backward ? leaf.lastChild : leaf.firstChild;
			while (child) {
				leaf = child;
				child = backward ? leaf.lastChild : leaf.firstChild;
			}
			return leaf;
		}
		current = current.parentNode;
	}
	return null;
}

function endpoint(
	root: HTMLElement,
	container: Node,
	offset: number,
	end: boolean,
): HTMLElement | null {
	let node: Node | null = container;
	if (container.nodeType === 3) {
		if ((end && offset === 0) || (!end && offset === container.textContent?.length)) {
			node = adjacentLeaf(container, root, end);
		}
	} else {
		node = container.childNodes[end ? offset - 1 : offset] ?? adjacentLeaf(container, root, end);
		while (node && (end ? node.lastChild : node.firstChild)) {
			node = end ? node.lastChild : node.firstChild;
		}
	}
	// react-markdown inserts unanchored newline text nodes between block elements.
	while (node?.nodeType === 3 && !node.textContent?.trim()) node = adjacentLeaf(node, root, end);
	const element = node?.nodeType === 1 ? (node as HTMLElement) : node?.parentElement;
	if (!element || !root.contains(element) || element.closest(SYNTHETIC)) return null;
	const source = element.closest<HTMLElement>(SOURCE);
	return source && root.contains(source) ? source : null;
}

function lineSpan(element: HTMLElement, count: number): [number, number] | null {
	const start = Number(element.dataset.line);
	const end = Number(element.dataset.lineEnd);
	return Number.isSafeInteger(start) &&
		Number.isSafeInteger(end) &&
		start >= 0 &&
		end >= start &&
		end < count
		? [start, end]
		: null;
}

/** DOM positions only identify this preview's source blocks; never search a different file/paragraph. */
export function readRenderedFileSelection(
	root: HTMLElement,
	selection: Selection | null,
	lines: readonly string[],
): FileSelection | null {
	if (!selection || selection.rangeCount !== 1 || selection.isCollapsed) return null;
	const range = selection.getRangeAt(0);
	if (range.collapsed || !root.contains(range.startContainer) || !root.contains(range.endContainer))
		return null;
	const text = range.toString().replace(/\r\n?/g, "\n");
	if (!text.trim()) return null;
	const first = endpoint(root, range.startContainer, range.startOffset, false);
	const last = endpoint(root, range.endContainer, range.endOffset, true);
	if (!first || !last) return null;
	const startSpan = lineSpan(first, lines.length);
	const endSpan = lineSpan(last, lines.length);
	if (!startSpan || !endSpan) return null;
	let start = startSpan[0];
	let end = endSpan[1];
	if (end < start) return null;
	const source = lines.slice(start, end + 1).join("\n");
	const match = source.indexOf(text);
	// ponytail: formatting is not reversibly mapped to columns. A unique literal match
	// refines the anchored lines; otherwise quote their proven source range, not a guessed occurrence.
	if (match >= 0 && source.indexOf(text, match + 1) < 0) {
		const lineAt = (offset: number) => source.slice(0, offset).split("\n").length - 1;
		end = start + lineAt(match + text.length - 1);
		start += lineAt(match);
	} else if (first.closest("[data-file-quote-code]") || last.closest("[data-file-quote-code]")) {
		return null;
	}
	const endColumn = lines[end].length + 1;
	if (end + 1 > MAX_FILE_REFERENCE_POSITION || endColumn > MAX_FILE_REFERENCE_POSITION) return null;
	return { startLineNumber: start + 1, startColumn: 1, endLineNumber: end + 1, endColumn };
}
