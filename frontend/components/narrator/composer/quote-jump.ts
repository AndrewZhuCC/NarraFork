import type { FileReference } from "@shared/file-reference";
import type { NarratorDockContextValue } from "../dock/NarratorDockContext";
import { nextHighlightRequestId } from "../panels/panel-kind";
import { type ComposerQuote, quoteHighlightNeedle } from "./composer-quotes";

const HIGHLIGHT_NAME = "nf-quote-jump";
const HIGHLIGHT_ATTEMPTS = 20;
const HIGHLIGHT_INTERVAL_MS = 50;

type HighlightList = {
	set: (name: string, highlight: { delete?: () => void } | Highlight) => void;
	delete: (name: string) => void;
};

function highlightApi(): HighlightList | null {
	const lists = (globalThis as { CSS?: { highlights?: HighlightList } }).CSS?.highlights;
	return lists && typeof Highlight !== "undefined" ? lists : null;
}

function ensureHighlightStyle(doc: Document) {
	if (doc.getElementById("nf-quote-jump-style")) return;
	const style = doc.createElement("style");
	style.id = "nf-quote-jump-style";
	style.textContent = `::highlight(${HIGHLIGHT_NAME}){background-color:rgba(250,176,5,.55)}`;
	doc.head.append(style);
}

function rangeCovering(
	doc: Document,
	nodes: readonly Text[],
	start: number,
	length: number,
): Range | null {
	let offset = 0;
	let startNode: Text | null = null;
	let startOffset = 0;
	const endAt = start + length;
	let endNode: Text | null = null;
	let endOffset = 0;
	for (const node of nodes) {
		const next = offset + node.data.length;
		if (!startNode && start < next) {
			startNode = node;
			startOffset = start - offset;
		}
		if (startNode && endAt <= next) {
			endNode = node;
			endOffset = endAt - offset;
			break;
		}
		offset = next;
	}
	if (!startNode || !endNode) return null;
	const range = doc.createRange();
	range.setStart(startNode, startOffset);
	range.setEnd(endNode, endOffset);
	return range;
}

/** First range inside `root` whose text contains `needle`. Layout-neutral: a custom highlight, not a DOM edit. */
function rangeForNeedle(root: Element, needle: string): Range | null {
	const probe = needle.slice(0, 48);
	if (probe.length < 2) return null;
	const doc = root.ownerDocument;
	const walker = doc.createTreeWalker(root, NodeFilter.SHOW_TEXT);
	const nodes: Text[] = [];
	let combined = "";
	for (let current = walker.nextNode(); current; current = walker.nextNode()) {
		const text = current as Text;
		if (text.parentElement?.closest("textarea, input")) continue;
		nodes.push(text);
		combined += text.data;
		const at = combined.indexOf(probe);
		if (at < 0) continue;
		return rangeCovering(doc, nodes, at, probe.length);
	}
	return null;
}

function paintHighlight(roots: readonly Element[], needle: string): boolean {
	for (const root of roots) {
		const range = rangeForNeedle(root, needle);
		if (!range) continue;
		const api = highlightApi();
		if (!api) return true;
		ensureHighlightStyle(root.ownerDocument);
		const highlight = new Highlight(range);
		api.set(HIGHLIGHT_NAME, highlight);
		window.setTimeout(() => api.delete(HIGHLIGHT_NAME), 1600);
		return true;
	}
	return false;
}

function quoteRoots(panel: Element, anchor: string): Element[] {
	const id = anchor.slice(2);
	if (anchor.startsWith("t:")) {
		const row = panel.querySelector(`[data-nf-row-key="${CSS.escape(`tool-${id}`)}"]`);
		return row ? [row] : [];
	}
	if (!anchor.startsWith("m:")) return [];
	return [...panel.querySelectorAll(`[data-message-id="${CSS.escape(id)}"]`)];
}

let highlightTimer: ReturnType<typeof setTimeout> | undefined;

/** Scroll the source into view, expand a folded tool card once, and highlight the quoted text. */
export function revealComposerQuote(dock: NarratorDockContextValue | null, quote: ComposerQuote) {
	const anchor = quote.anchor;
	if (!anchor) return;
	dock?.scrollToMessage?.(anchor.slice(2));
	const panel = document.querySelector("[data-narrator-panel]");
	if (!panel) return;
	clearTimeout(highlightTimer);
	let expanded = false;
	let attempt = 0;
	const tick = () => {
		attempt += 1;
		const roots = quoteRoots(panel, anchor);
		if (!expanded) {
			for (const root of roots) {
				const header = root.querySelector('[data-nf-card-header][aria-expanded="false"]');
				if (header instanceof HTMLElement) {
					expanded = true;
					header.click();
					break;
				}
			}
		}
		if (paintHighlight(roots, quoteHighlightNeedle(quote.text)) || attempt >= HIGHLIGHT_ATTEMPTS)
			return;
		highlightTimer = setTimeout(tick, HIGHLIGHT_INTERVAL_MS);
	};
	highlightTimer = setTimeout(tick, HIGHLIGHT_INTERVAL_MS);
}

/** Open the file a file-quote chip points at, on the quoted lines. */
export function revealQuotedFile(dock: NarratorDockContextValue | null, reference: FileReference) {
	dock?.openFilePanel?.(reference.path, undefined, {
		deviceId: reference.deviceId,
		selection: reference.selection,
		highlightRequestId: nextHighlightRequestId(),
	});
}
