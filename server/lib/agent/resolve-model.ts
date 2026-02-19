/**
 * Resolve model names to Kiro model IDs.
 *
 * Reverse-engineered from kiro CLI chat model resolution:
 * - Any model containing "sonnet" → "claude-sonnet-4.5"
 * - Opus 4.5/4-5 variants → "claude-opus-4.5"
 * - Other opus variants → "claude-opus-4.6"
 * - Any model containing "haiku" → "claude-haiku-4.5"
 */
export function resolveModel(model?: string): string {
	if (!model) return "claude-sonnet-4.5";

	const lower = model.toLowerCase();

	if (lower.includes("sonnet")) {
		return "claude-sonnet-4.5";
	}
	if (lower.includes("opus")) {
		if (lower.includes("4-5") || lower.includes("4.5")) {
			return "claude-opus-4.5";
		}
		return "claude-opus-4.6";
	}
	if (lower.includes("haiku")) {
		return "claude-haiku-4.5";
	}

	// Already a valid Kiro model ID or unknown — pass through
	return model;
}
