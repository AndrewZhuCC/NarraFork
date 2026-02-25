import { parseModelId } from "../settings";

/**
 * Resolve user-facing model names to Kiro API model IDs.
 * This is the single source of truth for model name → Kiro ID mapping.
 *
 * Supports "provider:model" prefix format — the prefix is stripped before resolution.
 *
 * Reverse-engineered from kiro CLI chat model resolution:
 * - Any model containing "sonnet" → "claude-sonnet-4.5"
 * - Opus 4.5/4-5 variants → "claude-opus-4.5"
 * - Other opus variants → "claude-opus-4.6"
 * - Any model containing "haiku" → "claude-haiku-4.5"
 *
 * See also: kiro/types.ts (KiroModel type), kiro-adapter.ts (inlined mirror type)
 */
export function resolveModel(model?: string): string {
	if (!model) return "claude-sonnet-4.5";

	// Strip provider prefix if present (e.g. "kiro:claude-sonnet" → "claude-sonnet")
	const { model: bare } = parseModelId(model);
	if (!bare) return "claude-sonnet-4.5";

	const lower = bare.toLowerCase();

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
	return bare;
}
