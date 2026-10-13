/**
 * Estimate token count for a string using character-based heuristics.
 * - ASCII / Latin characters: ~0.3 tokens per character
 * - CJK / wide characters: ~0.6 tokens per character
 *
 * This is intentionally conservative (over-estimates) so we stay within budget.
 */
export function tokenTenthsForCodeUnit(code: number): number {
	return (code >= 0x2e80 && code <= 0x9fff) ||
		(code >= 0xac00 && code <= 0xd7af) ||
		(code >= 0xf900 && code <= 0xfaff) ||
		(code >= 0xff00 && code <= 0xffef)
		? 6
		: 3;
}

export function estimateTokens(text: string): number {
	let tenths = 0;
	for (let i = 0; i < text.length; i++) tenths += tokenTenthsForCodeUnit(text.charCodeAt(i));
	// Integer accumulation prevents rounding drift and rounds the complete input only once.
	return Math.ceil(tenths / 10);
}
