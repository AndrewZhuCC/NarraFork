import { parseModelId } from "./model-id";

export const FAST_MODE_OVERRIDE_VALUES = ["inherit", "on", "off", "ultrafast"] as const;
export type FastModeOverride = (typeof FAST_MODE_OVERRIDE_VALUES)[number];

export function isFastModeOverride(value: unknown): value is FastModeOverride {
	return FAST_MODE_OVERRIDE_VALUES.some((mode) => mode === value);
}

export function normalizeFastModeOverride(value: unknown): FastModeOverride {
	return isFastModeOverride(value) ? value : "inherit";
}

/** Match the named model across providers; an unpinned aggregation names no model. */
export function codexUltrafastModelId(reference: unknown): string {
	const { provider, model } = parseModelId(typeof reference === "string" ? reference.trim() : "");
	return provider === "__agg__" ? "" : model;
}

export function supportsCodexUltrafast(
	model: string,
	allowedModels: readonly string[] | undefined,
): boolean {
	const id = codexUltrafastModelId(model);
	return (
		!!id &&
		Array.isArray(allowedModels) &&
		allowedModels.some((allowed) => allowed === id || codexUltrafastModelId(allowed) === id)
	);
}
