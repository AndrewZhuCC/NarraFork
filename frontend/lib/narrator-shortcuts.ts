export const SHORTCUT_ACTIONS = [
	"previous",
	"next",
	"narrator1",
	"narrator2",
	"narrator3",
	"narrator4",
	"narrator5",
	"narrator6",
	"narrator7",
	"narrator8",
	"narrator9",
	"readFromStart",
] as const;
export type ShortcutAction = (typeof SHORTCUT_ACTIONS)[number];
export type ShortcutBindings = Record<ShortcutAction, string>;
export const DEFAULT_NARRATOR_SHORTCUTS: ShortcutBindings = {
	previous: "Control+ArrowUp",
	next: "Control+ArrowDown",
	narrator1: "Mod+Shift+Digit1",
	narrator2: "Mod+Shift+Digit2",
	narrator3: "Mod+Shift+Digit3",
	narrator4: "Mod+Shift+Digit4",
	narrator5: "Mod+Shift+Digit5",
	narrator6: "Mod+Shift+Digit6",
	narrator7: "Mod+Shift+Digit7",
	narrator8: "Mod+Shift+Digit8",
	narrator9: "Mod+Shift+Digit9",
	readFromStart: "Home",
};
const MODIFIERS = ["Mod", "Control", "Meta", "Alt", "Shift"] as const;
const NAMED_KEYS = [
	"Home",
	"End",
	"PageUp",
	"PageDown",
	"ArrowUp",
	"ArrowDown",
	"ArrowLeft",
	"ArrowRight",
	"Escape",
	"Enter",
	"Tab",
	"Backspace",
	"Delete",
	"Space",
];
const MODIFIER_ALIASES: Record<string, string> = {
	mod: "Mod",
	ctrl: "Control",
	control: "Control",
	cmd: "Meta",
	command: "Meta",
	meta: "Meta",
	alt: "Alt",
	option: "Alt",
	shift: "Shift",
};
export function isMacShortcutPlatform(): boolean {
	return typeof navigator !== "undefined" && /Mac|iPhone|iPad/.test(navigator.platform);
}
/** Empty means disabled; null is invalid. Values are bounded before parsing stored input. */
export function normalizeShortcut(value: string): string | null {
	if (value.length > 100) return null;
	if (!value.trim()) return "";
	const parts = value.split("+").map((part) => part.trim());
	const rawKey = parts.pop() ?? "";
	let key = NAMED_KEYS.find((name) => name.toLowerCase() === rawKey.toLowerCase());
	if (/^(?:Key)?[a-z]$/i.test(rawKey)) key = `Key${rawKey.slice(-1).toUpperCase()}`;
	if (/^(?:Digit)?[0-9]$/i.test(rawKey)) key = `Digit${rawKey.slice(-1)}`;
	if (/^F(?:[1-9]|1\d|2[0-4])$/i.test(rawKey)) key = rawKey.toUpperCase();
	if (!key) return null;
	const modifiers = parts.map((part) => MODIFIER_ALIASES[part.toLowerCase()]);
	if (modifiers.some((modifier) => !modifier) || new Set(modifiers).size !== modifiers.length)
		return null;
	// Never turn ordinary typing / activation keys into unmodified global shortcuts.
	if (
		!modifiers.some((modifier) => modifier !== "Shift") &&
		!/^(Home|End|PageUp|PageDown|Arrow\w+|F\d+)$/.test(key)
	)
		return null;
	return [...MODIFIERS.filter((modifier) => modifiers.includes(modifier)), key].join("+");
}
function platformShortcut(value: string, isMac: boolean): string {
	const parts = value.split("+");
	const key = parts.pop();
	const modifiers = new Set(
		parts.map((part) => (part === "Mod" ? (isMac ? "Meta" : "Control") : part)),
	);
	return [...MODIFIERS.filter((part) => modifiers.has(part)), key].join("+");
}
export function findShortcutConflict(
	bindings: ShortcutBindings,
	action: ShortcutAction,
	value: string,
	isMac: boolean,
): ShortcutAction | undefined {
	const normalized = normalizeShortcut(value);
	if (!normalized) return undefined;
	return SHORTCUT_ACTIONS.find(
		(other) =>
			other !== action &&
			bindings[other] &&
			platformShortcut(bindings[other], isMac) === platformShortcut(normalized, isMac),
	);
}
export function formatShortcut(value: string, isMac: boolean): string {
	if (!value) return "";
	return value
		.split("+")
		.map((part) => {
			if (part === "Mod") return isMac ? "Cmd" : "Ctrl";
			if (part === "Meta") return isMac ? "Cmd" : "Meta";
			if (part === "Control") return "Ctrl";
			if (part === "Alt") return isMac ? "Option" : "Alt";
			return part.replace(/^(Key|Digit)/, "");
		})
		.join(" + ");
}
export function parseShortcutBindings(raw: string): ShortcutBindings {
	const bindings = { ...DEFAULT_NARRATOR_SHORTCUTS };
	if (raw.length > 4096) return bindings;
	try {
		const value = JSON.parse(raw);
		if (!value || typeof value !== "object" || Array.isArray(value)) return bindings;
		for (const action of SHORTCUT_ACTIONS) {
			if (typeof value[action] !== "string") continue;
			const normalized = normalizeShortcut(value[action]);
			if (normalized !== null) bindings[action] = normalized;
		}
	} catch {
		/* Invalid local preferences use defaults. */
	}
	return bindings;
}
export function matchesShortcut(
	event: KeyboardEvent,
	value: string,
	isMac = isMacShortcutPlatform(),
): boolean {
	if (!value || event.isComposing || event.repeat || event.defaultPrevented) return false;
	const parts = platformShortcut(value, isMac).split("+");
	const key = parts.pop();
	return (
		(event.code || event.key) === key &&
		event.ctrlKey === parts.includes("Control") &&
		event.metaKey === parts.includes("Meta") &&
		event.altKey === parts.includes("Alt") &&
		event.shiftKey === parts.includes("Shift")
	);
}
export function shortcutTargetBlocked(event: KeyboardEvent, reading = false): boolean {
	return event.composedPath().some((node) => {
		if (!(node instanceof Element)) return false;
		if (node.closest('[data-narrator-shortcut-settings], .monaco-editor, .xterm, [role="dialog"]'))
			return true;
		return (
			reading &&
			!!node.closest(
				'input, textarea, select, [contenteditable]:not([contenteditable="false"]), [role="textbox"]',
			)
		);
	});
}
