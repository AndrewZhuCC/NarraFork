import { normalizeModelContextWindowOverrides } from "@shared/model-context-window";
import type { NarraForkSettings } from "./types";

export function normalizeModelContextWindows(settings: NarraForkSettings): boolean {
	const windows = settings.agent.modelContextWindows;
	const normalized = normalizeModelContextWindowOverrides(windows);
	if (JSON.stringify(windows) === JSON.stringify(normalized)) return false;
	settings.agent.modelContextWindows = normalized;
	return true;
}
