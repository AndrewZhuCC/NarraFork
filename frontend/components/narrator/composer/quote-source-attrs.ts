/**
 * Height-neutral markers that let the composer label a text selection made in
 * the message list. Renderers only stamp them; `quote-selection.ts` reads them.
 */
export const QUOTE_ROLE_ATTR = "data-quote-role";
export const QUOTE_TOOL_ATTR = "data-quote-tool";
export const QUOTE_TOOL_SUMMARY_ATTR = "data-quote-tool-summary";
/** File path shown on the tool card (Read/Write/Edit/StructView/StructSed). */
export const QUOTE_FILE_ATTR = "data-quote-file";
/** `input` or `result`: which region of the tool card the selection sits in. */
export const QUOTE_PART_ATTR = "data-quote-part";
/** Tool use id, so a quote can scroll back to this card after it is sent. */
export const QUOTE_TOOL_ID_ATTR = "data-quote-tool-id";

/** Section keys from the tool detail classifier: `input.*` is the call, `output.*` the result. */
export function quoteSectionPart(key: string): "input" | "result" | undefined {
	if (key.startsWith("input.")) return "input";
	if (key.startsWith("output.")) return "result";
	return undefined;
}
