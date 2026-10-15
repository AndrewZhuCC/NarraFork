export const MIN_MODEL_CONTEXT_WINDOW = 256;
export const MAX_MODEL_CONTEXT_WINDOW = 10_000_000;

/** Bounds for the legacy per-model context-window editor and its auto-fillers. */
export function isValidModelContextWindow(value: unknown): value is number {
	return (
		typeof value === "number" &&
		Number.isInteger(value) &&
		value >= MIN_MODEL_CONTEXT_WINDOW &&
		value <= MAX_MODEL_CONTEXT_WINDOW
	);
}

/** Repair user/legacy overrides instead of blocking unrelated provider edits. */
export function normalizeModelContextWindowOverrides(value: unknown): Record<string, number> {
	if (!value || typeof value !== "object" || Array.isArray(value)) return {};
	return Object.fromEntries(
		Object.entries(value).flatMap(([model, raw]) => {
			const number = typeof raw === "string" && raw.trim() ? Number(raw) : raw;
			if (typeof number !== "number" || !Number.isFinite(number) || number <= 0) return [];
			return [
				[
					model,
					Math.max(
						MIN_MODEL_CONTEXT_WINDOW,
						Math.min(MAX_MODEL_CONTEXT_WINDOW, Math.floor(number)),
					),
				],
			];
		}),
	);
}
