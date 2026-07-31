import { parseModelId } from "@shared/model-id";

/**
 * Resolve user-facing model names to Kiro API model IDs.
 *
 * Strips the "provider:" prefix if present and returns the bare model name as-is.
 * Falls back to "claude-sonnet-4.5" when no model is specified.
 */
export function resolveModel(model?: string): string {
	if (!model) return "claude-sonnet-4.5";

	// Strip provider prefix if present (e.g. "kiro:claude-sonnet-4.6" → "claude-sonnet-4.6")
	const { model: bare } = parseModelId(model);
	if (!bare) return "claude-sonnet-4.5";

	return bare;
}
