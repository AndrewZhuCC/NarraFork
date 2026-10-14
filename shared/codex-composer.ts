/** User-supplied Composer configuration snapshot, prompt version 5. */
export const DEFAULT_CODEX_COMPOSER_PROMPT = `This is an app-generated request for a suggested next user message. The user did not write this message.

Your suggestion will appear in the user's empty message composer. They can accept it, edit it, or ignore it; it will not be sent automatically.

Predict the single message this user is most likely to type next from the visible conversation. Use the whole conversation to understand their current intent and the latest exchange. Choose the action and specific object that best continue that intent, rather than a generally useful follow-up. Express its core naturally in the user's voice, keeping the requirements needed to retain its meaning. Choose the strongest grounded move even if several continuations are possible; uncertainty about exact wording or incidental details is not a reason to return null. Use null only when the conversation offers no grounded next move at all.

Set suggestion to exactly one natural user message, one line, at most 240 characters, or null. No alternatives or explanation.`;

export const CODEX_COMPOSER_MAX_PROMPT_CHARS = 16_000;
export const CODEX_COMPOSER_MAX_SUGGESTION_CHARS = 240;

/** Kept in the appended prompt, not text.format, to preserve the ordinary request prefix. */
export const CODEX_COMPOSER_SCHEMA = {
	$schema: "https://json-schema.org/draft/2020-12/schema",
	type: "object",
	properties: { suggestion: { anyOf: [{ type: "string" }, { type: "null" }] } },
	required: ["suggestion"],
	additionalProperties: false,
} as const;

export interface CodexComposerSuggestion {
	suggestion: string | null;
}

export function parseCodexComposerSuggestion(text: string): CodexComposerSuggestion {
	const value: unknown = JSON.parse(text);
	if (
		!value ||
		typeof value !== "object" ||
		Array.isArray(value) ||
		Object.keys(value).length !== 1 ||
		!Object.hasOwn(value, "suggestion") ||
		!("suggestion" in value)
	) {
		throw new Error("Invalid Composer suggestion object");
	}
	if (value.suggestion === null) return { suggestion: null };
	if (
		typeof value.suggestion !== "string" ||
		value.suggestion.length > CODEX_COMPOSER_MAX_SUGGESTION_CHARS * 2 ||
		/[\r\n\u2028\u2029]/u.test(value.suggestion) ||
		Array.from(value.suggestion).length > CODEX_COMPOSER_MAX_SUGGESTION_CHARS
	) {
		throw new Error("Invalid Composer suggestion text");
	}
	return { suggestion: value.suggestion.trim() || null };
}
