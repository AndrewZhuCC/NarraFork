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
 *   > [quote] Assistant message
 *   > first quoted line
 *   >
 *   > third quoted line
 *
 *   body text typed by the user
 */
export interface ComposerQuote {
	label: string;
	text: string;
}

export const MAX_COMPOSER_QUOTES = 8;
export const MAX_COMPOSER_QUOTE_CHARS = 8000;
export const MAX_COMPOSER_QUOTE_LABEL_CHARS = 80;

const HEADER_PREFIX = "> [quote] ";
const HEADER = /^> \[quote\] (.+)$/;
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

/** Null when nothing quotable remains. Over-long text is clipped with a visible mark. */
export function normalizeComposerQuote(label: string, text: string): ComposerQuote | null {
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
	return { label: cleanLabel, text: body };
}

export function sameComposerQuote(a: ComposerQuote, b: ComposerQuote): boolean {
	return a.label === b.label && a.text === b.text;
}

function serializeQuote(quote: ComposerQuote): string {
	const content = quote.text
		.split("\n")
		.map((line) => {
			const escaped = ESCAPABLE.test(line) ? `\\${line}` : line;
			return escaped ? `> ${escaped}` : ">";
		})
		.join("\n");
	return `${HEADER_PREFIX}${clipLabel(quote.label)}\n${content}\n\n`;
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
		const label = header[1].trim();
		const quote = { label, text: lines.join("\n") };
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
