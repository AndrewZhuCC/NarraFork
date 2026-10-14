import type { FileReference } from "@shared/file-reference";
import type { FileReferenceInput } from "./file-reference-input";

/**
 * A quoted excerpt of conversation content (message text or tool output).
 *
 * Quotes are NOT a separate draft/message field: they live at the very start of
 * the composer text as canonical blockquotes, and the composer only presents
 * that leading prefix as chips. Draft sync, the local draft mirror, input
 * history, queued-message edits, failed-send restore and the model all see the
 * same plain text, so no transport needs to know quotes exist. User bubbles
 * render plain pre-wrap text, which is why the header is readable as-is.
 *
 *   > [quote] Selecting text in a message @@m:msgId
 *   > Selecting text in a message
 *   >
 *   > shows a chip
 *
 *   body text typed by the user
 */
export interface ComposerQuote {
	label: string;
	text: string;
	/**
	 * Where the excerpt came from, so a click can scroll back to it after send.
	 * `m:<messageId>` or `t:<toolUseId>`. Absent when the source has no stable id.
	 */
	anchor?: string;
}

/** Chip text: the start of the selection, with the file name in front when the source is a file tool. */
export const QUOTE_PREVIEW_CHARS = 36;

export function quotePreviewLabel(text: string, fileName?: string): string {
	const flat = text.replace(/\s+/gu, " ").trim();
	const clipped =
		flat.length > QUOTE_PREVIEW_CHARS
			? `${flat.slice(0, QUOTE_PREVIEW_CHARS - 1)}${TRUNCATION_MARK}`
			: flat;
	if (!fileName) return clipped;
	const withFile = `${fileName} · ${clipped}`;
	return withFile.length > MAX_COMPOSER_QUOTE_LABEL_CHARS
		? `${withFile.slice(0, MAX_COMPOSER_QUOTE_LABEL_CHARS - 1)}${TRUNCATION_MARK}`
		: withFile;
}

/** `path/to/App.tsx:12` → `App.tsx`. The line suffix is the card's own decoration. */
export function fileQuoteName(raw: string): string {
	const trimmed = raw.trim().replace(/:\d+$/u, "");
	const base = trimmed.split(/[\\/]/u).pop() || trimmed;
	return base;
}

export const MAX_COMPOSER_QUOTES = 8;
export const MAX_COMPOSER_QUOTE_CHARS = 8000;
export const MAX_COMPOSER_QUOTE_LABEL_CHARS = 80;

const HEADER_PREFIX = "> [quote] ";
const HEADER = /^> \[quote\] (.+)$/;
/** Trailing jump target. The id is at least 4 characters so ordinary prose cannot collide. */
const ANCHOR_SUFFIX = / @@([mt]:[A-Za-z0-9_-]{4,})$/;
// A content line that would read as a header is escaped with one extra backslash.
const ESCAPABLE = /^\\*\[quote\]/;
const ESCAPED = /^\\+\[quote\]/;
const TRUNCATION_MARK = "…";

function clipLabel(label: string): string {
	const single = label.replace(/\s+/gu, " ").trim();
	return single.length > MAX_COMPOSER_QUOTE_LABEL_CHARS
		? `${single.slice(0, MAX_COMPOSER_QUOTE_LABEL_CHARS - 1)}${TRUNCATION_MARK}`
		: single;
}

export function splitQuoteHeader(headerBody: string): { label: string; anchor?: string } {
	const trimmed = headerBody.trim();
	const match = ANCHOR_SUFFIX.exec(trimmed);
	if (!match) return { label: trimmed };
	const label = trimmed.slice(0, match.index).trim();
	return label ? { label, anchor: match[1] } : { label: trimmed };
}

/** Null when nothing quotable remains. Over-long text is clipped with a visible mark. */
export function normalizeComposerQuote(
	label: string,
	text: string,
	anchor?: string,
): ComposerQuote | null {
	const cleanLabel = clipLabel(label);
	const lines = text
		.replace(/\r\n?/gu, "\n")
		.split("\n")
		.map((line) => line.replace(/\s+$/u, ""));
	while (lines.length && !lines[0].trim()) lines.shift();
	while (lines.length && !lines[lines.length - 1].trim()) lines.pop();
	let body = lines.join("\n");
	if (!cleanLabel || !body) return null;
	if (body.length > MAX_COMPOSER_QUOTE_CHARS) {
		body = `${body.slice(0, MAX_COMPOSER_QUOTE_CHARS - 1).replace(/\s+$/u, "")}${TRUNCATION_MARK}`;
	}
	const quote: ComposerQuote = { label: cleanLabel, text: body };
	if (anchor && ANCHOR_SUFFIX.test(` @@${anchor}`)) quote.anchor = anchor;
	return quote;
}

export function sameComposerQuote(a: ComposerQuote, b: ComposerQuote): boolean {
	return a.label === b.label && a.text === b.text && a.anchor === b.anchor;
}

function serializeQuote(quote: ComposerQuote): string {
	const content = quote.text
		.split("\n")
		.map((line) => {
			const escaped = ESCAPABLE.test(line) ? `\\${line}` : line;
			return escaped ? `> ${escaped}` : ">";
		})
		.join("\n");
	const suffix = quote.anchor ? ` @@${quote.anchor}` : "";
	return `${HEADER_PREFIX}${clipLabel(quote.label)}${suffix}\n${content}\n\n`;
}

export function serializeComposerQuotes(quotes: readonly ComposerQuote[]): string {
	return quotes.map(serializeQuote).join("");
}

function unescapeContentLine(raw: string): string {
	const line = raw.startsWith("> ") ? raw.slice(2) : raw.slice(1);
	return ESCAPED.test(line) ? line.slice(1) : line;
}

function lineEnd(text: string, from: number): number {
	const end = text.indexOf("\n", from);
	return end < 0 ? text.length : end;
}

/**
 * Parse the leading quote blocks. A block must be a header, at least one `>`
 * content line, then a blank line, another header, or the end of the text; a
 * block that is not terminated that way is left in the body untouched.
 */
export function parseComposerQuotePrefix(text: string): {
	quotes: ComposerQuote[];
	prefixLength: number;
} {
	const quotes: ComposerQuote[] = [];
	let prefixLength = 0;
	let pos = 0;
	while (pos < text.length && quotes.length < MAX_COMPOSER_QUOTES) {
		const headerEnd = lineEnd(text, pos);
		const header = HEADER.exec(text.slice(pos, headerEnd));
		if (!header || headerEnd >= text.length) break;
		let cursor = headerEnd + 1;
		const lines: string[] = [];
		while (cursor < text.length && text[cursor] === ">") {
			const end = lineEnd(text, cursor);
			const raw = text.slice(cursor, end);
			if (HEADER.test(raw)) break;
			lines.push(unescapeContentLine(raw));
			cursor = end < text.length ? end + 1 : text.length;
		}
		if (!lines.length) break;
		const { label, anchor } = splitQuoteHeader(header[1]);
		if (!label) break;
		const quote: ComposerQuote = { label, text: lines.join("\n") };
		if (anchor) quote.anchor = anchor;
		if (cursor >= text.length) {
			quotes.push(quote);
			prefixLength = text.length;
			break;
		}
		if (text[cursor] === "\n") {
			quotes.push(quote);
			prefixLength = cursor + 1;
			pos = prefixLength;
			continue;
		}
		if (text.startsWith(HEADER_PREFIX, cursor)) {
			quotes.push(quote);
			prefixLength = cursor;
			pos = cursor;
			continue;
		}
		break;
	}
	return { quotes, prefixLength };
}

function shiftReferences(
	references: readonly FileReference[],
	offset: number,
	minStart: number,
): FileReference[] {
	return references.map((reference) => {
		if (!reference.inputRange) return reference;
		const [start, end] = reference.inputRange;
		if (start < minStart) {
			const { inputRange: _dropped, ...detached } = reference;
			return detached;
		}
		return { ...reference, inputRange: [start + offset, end + offset] as [number, number] };
	});
}

export interface QuotedComposerInput {
	quotes: ComposerQuote[];
	/** Text after the quote prefix, with file-token offsets relative to it. */
	body: FileReferenceInput;
}

export function splitQuotedComposerInput(input: FileReferenceInput): QuotedComposerInput {
	const { quotes, prefixLength } = parseComposerQuotePrefix(input.text);
	if (!prefixLength) return { quotes, body: input };
	return {
		quotes,
		body: {
			text: input.text.slice(prefixLength),
			fileReferences: shiftReferences(input.fileReferences, -prefixLength, prefixLength),
		},
	};
}

/** One clickable quote block in a sent user message, after the jump id is hidden. */
export interface QuoteDisplayJump {
	/** Displayed header line, `> [quote] <label>`, with no anchor suffix. */
	header: string;
	anchor: string;
	/** Text to look for at the source. The leading file-name line is not part of it. */
	highlight: string;
}

/**
 * A single source line to highlight. A leading line with no spaces is the file
 * name prepended for the model, not part of what the reader selected.
 */
export function quoteHighlightNeedle(text: string): string {
	const lines = text
		.split("\n")
		.map((line) => line.trim())
		.filter(Boolean);
	const body = lines.length > 1 && lines[0] && !/\s/u.test(lines[0]) ? lines[1] : lines[0];
	const line = body ?? "";
	return line.length > 80 ? line.slice(0, 80) : line;
}

/**
 * The text a user bubble measures and paints. Anchors stay in the stored message
 * (so a click still knows where to go) but not on screen.
 */
export function quoteDisplayText(text: string): { text: string; jumps: QuoteDisplayJump[] } {
	const { quotes, prefixLength } = parseComposerQuotePrefix(text);
	if (!prefixLength) return { text, jumps: [] };
	const jumps: QuoteDisplayJump[] = [];
	let display = "";
	for (const quote of quotes) {
		const header = `${HEADER_PREFIX}${quote.label}`;
		display += serializeQuote({ label: quote.label, text: quote.text });
		if (quote.anchor) {
			jumps.push({ header, anchor: quote.anchor, highlight: quoteHighlightNeedle(quote.text) });
		}
	}
	return { text: display + text.slice(prefixLength), jumps };
}

export function joinQuotedComposerInput(
	quotes: readonly ComposerQuote[],
	body: FileReferenceInput,
): FileReferenceInput {
	const prefix = serializeComposerQuotes(quotes);
	if (!prefix) return body;
	return {
		text: prefix + body.text,
		fileReferences: shiftReferences(body.fileReferences, prefix.length, 0),
	};
}
