export const FAST_MODE_OVERRIDE_VALUES = ["inherit", "on", "off", "ultrafast"] as const;
export type FastModeOverride = (typeof FAST_MODE_OVERRIDE_VALUES)[number];

export function isFastModeOverride(value: unknown): value is FastModeOverride {
	return FAST_MODE_OVERRIDE_VALUES.some((mode) => mode === value);
}

export function normalizeFastModeOverride(value: unknown): FastModeOverride {
	return isFastModeOverride(value) ? value : "inherit";
}

/** Match the concrete connection and model, never an auto: route or a bare model name. */
export function supportsCodexUltrafast(
	model: string,
	allowedModels: readonly string[] | undefined,
): boolean {
	return (
		model.indexOf(":") > 0 &&
		!model.startsWith("auto:") &&
		Array.isArray(allowedModels) &&
		allowedModels.includes(model)
	);
}
