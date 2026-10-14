import { collectSelectionTextPreview } from "@frontend/lib/dom-text";
import {
	type ComposerQuote,
	MAX_COMPOSER_QUOTE_CHARS,
	normalizeComposerQuote,
} from "./composer-quotes";
import { QUOTE_ROLE_ATTR, QUOTE_TOOL_ATTR, QUOTE_TOOL_SUMMARY_ATTR } from "./quote-source-attrs";

export const NARRATOR_PANEL_SELECTOR = "[data-narrator-panel]";
const CONTENT_BLOCK_SELECTOR = "[data-content-block]";
const SOURCE_SELECTOR = `${CONTENT_BLOCK_SELECTOR}, [data-message-id]`;
const MAX_TOOL_SUMMARY_LABEL_CHARS = 48;

export interface QuoteSourceLabels {
	assistant: string;
	user: string;
	message: string;
	merged: (count: number) => string;
}

function elementOf(node: Node | null): Element | null {
	if (!node) return null;
	return node.nodeType === 1 ? (node as Element) : node.parentElement;
}

/** The message-list block an endpoint belongs to, only when it is this panel's own list. */
function sourceBlock(element: Element | null, panel: Element): Element | null {
	if (!element || element.closest(NARRATOR_PANEL_SELECTOR) !== panel) return null;
	if (element.closest("textarea, input, [contenteditable]:not([contenteditable='false'])"))
		return null;
	return element.closest(SOURCE_SELECTOR);
}

export function describeQuoteEndpoint(element: Element, labels: QuoteSourceLabels): string {
	const tool = element.closest(`[${QUOTE_TOOL_ATTR}]`);
	if (tool) {
		const name = tool.getAttribute(QUOTE_TOOL_ATTR)?.trim() || labels.message;
		const summary = tool.getAttribute(QUOTE_TOOL_SUMMARY_ATTR)?.replace(/\s+/gu, " ").trim();
		if (!summary) return name;
		const clipped =
			summary.length > MAX_TOOL_SUMMARY_LABEL_CHARS
				? `${summary.slice(0, MAX_TOOL_SUMMARY_LABEL_CHARS - 1)}…`
				: summary;
		return `${name} · ${clipped}`;
	}
	const role = element.closest(`[${QUOTE_ROLE_ATTR}]`)?.getAttribute(QUOTE_ROLE_ATTR);
	if (role === "assistant") return labels.assistant;
	if (role === "user") return labels.user;
	return labels.message;
}

function isTopLevelBlock(block: Element): boolean {
	return !block.parentElement?.closest(CONTENT_BLOCK_SELECTOR);
}

/** Selections spanning several blocks become ONE merged quote labelled with the count. */
function countSelectedBlocks(panel: Element, range: Range, anchor: Element, focus: Element) {
	if (anchor === focus) return 1;
	let count = 0;
	for (const block of panel.querySelectorAll(CONTENT_BLOCK_SELECTOR)) {
		if (isTopLevelBlock(block) && range.intersectsNode(block)) count++;
	}
	return Math.max(2, count);
}

/** Resolve a document selection inside `panel`'s message list into a quote candidate. */
export function readPanelQuoteSelection(
	panel: Element,
	selection: Selection | null,
	labels: QuoteSourceLabels,
): ComposerQuote | null {
	if (!selection || selection.isCollapsed || selection.rangeCount === 0) return null;
	const anchorElement = elementOf(selection.anchorNode);
	const focusElement = elementOf(selection.focusNode);
	const anchor = sourceBlock(anchorElement, panel);
	const focus = sourceBlock(focusElement, panel);
	if (!anchor || !focus || !anchorElement) return null;
	const text = collectSelectionTextPreview(selection, MAX_COMPOSER_QUOTE_CHARS + 1);
	if (!text.trim()) return null;
	const count = countSelectedBlocks(panel, selection.getRangeAt(0), anchor, focus);
	const label = count > 1 ? labels.merged(count) : describeQuoteEndpoint(anchorElement, labels);
	return normalizeComposerQuote(label, text);
}
