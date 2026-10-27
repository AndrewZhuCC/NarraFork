import { collectSelectionTextPreview } from "@frontend/lib/dom-text";
import {
	type ComposerQuote,
	fileQuoteName,
	MAX_COMPOSER_QUOTE_CHARS,
	normalizeComposerQuote,
	quotePreviewLabel,
} from "./composer-quotes";
import {
	QUOTE_FILE_ATTR,
	QUOTE_PART_ATTR,
	QUOTE_ROLE_ATTR,
	QUOTE_TOOL_ATTR,
	QUOTE_TOOL_ID_ATTR,
	QUOTE_TOOL_SUMMARY_ATTR,
} from "./quote-source-attrs";

/** Result text of these tools is a file's contents, so the quote names the file. */
const RESULT_FILE_TOOLS = new Set(["Read", "StructView", "StructSed"]);
/** Input text of these tools is the file being written, so the quote names the file. */
const INPUT_FILE_TOOLS = new Set(["Write", "Edit", "StructSed"]);

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

interface ToolQuoteContext {
	tool: string;
	part: string;
	file: string;
	toolId?: string;
}

function toolContext(element: Element | null): ToolQuoteContext | null {
	if (!element) return null;
	const toolEl = element.closest(`[${QUOTE_TOOL_ATTR}]`);
	if (!toolEl) return null;
	return {
		tool: toolEl.getAttribute(QUOTE_TOOL_ATTR)?.trim() ?? "",
		part: element.closest(`[${QUOTE_PART_ATTR}]`)?.getAttribute(QUOTE_PART_ATTR) ?? "",
		file: toolEl.getAttribute(QUOTE_FILE_ATTR)?.trim() ?? "",
		toolId: toolEl.getAttribute(QUOTE_TOOL_ID_ATTR)?.trim() || undefined,
	};
}

/** File name when this endpoint is a Read/StructView result or a Write/Edit/StructSed input. */
function quotedFileName(info: ToolQuoteContext | null): string | undefined {
	if (!info?.file) return undefined;
	const named =
		(info.part === "result" && RESULT_FILE_TOOLS.has(info.tool)) ||
		(info.part === "input" && INPUT_FILE_TOOLS.has(info.tool));
	return named ? fileQuoteName(info.file) : undefined;
}

function quoteAnchor(element: Element | null): string | undefined {
	if (!element) return undefined;
	const toolId = element
		.closest(`[${QUOTE_TOOL_ID_ATTR}]`)
		?.getAttribute(QUOTE_TOOL_ID_ATTR)
		?.trim();
	if (toolId) return `t:${toolId}`;
	const messageId = element.closest("[data-message-id]")?.getAttribute("data-message-id")?.trim();
	return messageId ? `m:${messageId}` : undefined;
}

/** Resolve a document selection inside `panel`'s message list into a quote candidate. */
export function readPanelQuoteSelection(
	panel: Element,
	selection: Selection | null,
): ComposerQuote | null {
	if (!selection || selection.isCollapsed || selection.rangeCount === 0) return null;
	const anchorElement = elementOf(selection.anchorNode);
	const focusElement = elementOf(selection.focusNode);
	// File previews own their selection and must keep file identity and source lines.
	if (
		anchorElement?.closest("[data-rendered-file-quote]") ||
		focusElement?.closest("[data-rendered-file-quote]")
	)
		return null;
	const anchor = sourceBlock(anchorElement, panel);
	const focus = sourceBlock(focusElement, panel);
	if (!anchor || !focus || !anchorElement || !focusElement) return null;
	// The selection anchor is where the drag started, which may be the later
	// message. The quoted text and its highlight start at the document-order
	// boundary, so the jump has to open that block.
	const startElement = elementOf(selection.getRangeAt(0).startContainer);
	if (!startElement || !sourceBlock(startElement, panel)) return null;
	const text = collectSelectionTextPreview(selection, MAX_COMPOSER_QUOTE_CHARS + 1);
	if (!text.trim()) return null;
	const fileName = quotedFileName(toolContext(startElement));
	const quoted = fileName && !text.startsWith(fileName) ? `${fileName}\n${text}` : text;
	return normalizeComposerQuote(
		quotePreviewLabel(text, fileName),
		quoted,
		quoteAnchor(startElement),
	);
}
