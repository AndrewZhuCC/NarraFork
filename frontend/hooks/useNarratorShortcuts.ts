import { useMemo } from "react";
import {
	findShortcutConflict,
	isMacShortcutPlatform,
	normalizeShortcut,
	parseShortcutBindings,
	type ShortcutAction,
} from "../lib/narrator-shortcuts";
import {
	readLocalShortcutPref,
	useLocalShortcutPref,
	writeLocalShortcutPref,
} from "./useLocalPref";

export function readNarratorShortcuts() {
	return parseShortcutBindings(readLocalShortcutPref());
}
export function setNarratorShortcut(action: ShortcutAction, value: string): void {
	const normalized = normalizeShortcut(value);
	const bindings = readNarratorShortcuts();
	if (
		normalized === null ||
		findShortcutConflict(bindings, action, normalized, isMacShortcutPlatform())
	)
		return;
	writeLocalShortcutPref(JSON.stringify({ ...bindings, [action]: normalized }));
}
export function resetNarratorShortcuts(): void {
	writeLocalShortcutPref("{}");
}
export function useNarratorShortcuts() {
	const raw = useLocalShortcutPref();
	const bindings = useMemo(() => parseShortcutBindings(raw), [raw]);
	return { bindings, setBinding: setNarratorShortcut, reset: resetNarratorShortcuts };
}
