import { describe, expect, test } from "bun:test";
import {
	DEFAULT_NARRATOR_SHORTCUTS,
	findShortcutConflict,
	formatShortcut,
	matchesShortcut,
	normalizeShortcut,
	parseShortcutBindings,
} from "./narrator-shortcuts";

const event = (overrides: Partial<KeyboardEvent> = {}) =>
	({
		code: "Digit1",
		key: "!",
		ctrlKey: false,
		metaKey: true,
		altKey: false,
		shiftKey: true,
		repeat: false,
		isComposing: false,
		defaultPrevented: false,
		...overrides,
	}) as KeyboardEvent;

describe("narrator shortcuts", () => {
	test("keeps requested defaults and can disable a binding", () => {
		expect(DEFAULT_NARRATOR_SHORTCUTS.readFromStart).toBe("Home");
		expect(DEFAULT_NARRATOR_SHORTCUTS.narrator9).toBe("Mod+Shift+Digit9");
		expect(parseShortcutBindings('{"next":""}').next).toBe("");
		expect(matchesShortcut(event(), "", true)).toBe(false);
	});
	test.each([
		["Cmd + Shift + 1", "Meta+Shift+Digit1"],
		["control+option+a", "Control+Alt+KeyA"],
		["Home", "Home"],
		["Tab", "Tab"],
		["mod + shift + Digit9", "Mod+Shift+Digit9"],
		[" ", ""],
	])("normalizes %s", (input, expected) => expect(normalizeShortcut(input)).toBe(expected));
	test.each([
		"Shift",
		"Cmd+",
		"Ctrl+Ctrl+1",
		"Home+Ctrl",
		"a",
		"Shift+1",
		"Enter",
		"Shift+Tab",
		"F25",
		"x".repeat(101),
	])("rejects invalid/typing chord %s", (input) => expect(normalizeShortcut(input)).toBeNull());
	test.each([
		["Backquote", "`"],
		["Minus", "-"],
		["Equal", "="],
		["BracketLeft", "["],
		["BracketRight", "]"],
		["Backslash", "\\"],
		["Semicolon", ";"],
		["Quote", "'"],
		["Comma", ","],
		["Period", "."],
		["Slash", "/"],
		["NumpadAdd", "Numpad +"],
		["NumpadSubtract", "Numpad -"],
		["NumpadMultiply", "Numpad *"],
		["NumpadDivide", "Numpad /"],
		["NumpadDecimal", "Numpad ."],
		["NumpadEqual", "Numpad ="],
		["NumpadComma", "Numpad ,"],
	])("records, persists and matches modified %s without hijacking typing", (code, label) => {
		const binding = `Control+${code}`;
		expect(normalizeShortcut(`ctrl+${code.toLowerCase()}`)).toBe(binding);
		expect(formatShortcut(binding, true)).toBe(`Ctrl + ${label}`);
		expect(parseShortcutBindings(JSON.stringify({ previous: binding })).previous).toBe(binding);
		expect(
			matchesShortcut(
				event({
					code,
					key: label.replace("Numpad ", ""),
					ctrlKey: true,
					metaKey: false,
					shiftKey: false,
				}),
				binding,
				true,
			),
		).toBe(true);
		expect(normalizeShortcut(code)).toBeNull();
		expect(normalizeShortcut(`Shift+${code}`)).toBeNull();
	});
	test("plus on the main keyboard retains Shift and stays distinct from numpad add", () => {
		const plus = event({ code: "Equal", key: "+", ctrlKey: true, metaKey: false });
		const binding = "Control+Shift+Equal";
		expect(normalizeShortcut("Ctrl+Shift+Equal")).toBe(binding);
		expect(formatShortcut(binding, true)).toBe("Ctrl + Shift + =");
		expect(matchesShortcut(plus, binding, true)).toBe(true);
		expect(matchesShortcut({ ...plus, shiftKey: false }, binding, true)).toBe(false);
		expect(matchesShortcut(plus, "Control+NumpadAdd", true)).toBe(false);
	});
	test("symbol shortcuts still detect modifier alias conflicts", () => {
		const bindings = { ...DEFAULT_NARRATOR_SHORTCUTS, previous: "Control+BracketLeft" };
		expect(findShortcutConflict(bindings, "next", "Ctrl+BracketLeft", true)).toBe("previous");
		expect(findShortcutConflict(bindings, "next", "Mod+BracketLeft", false)).toBe("previous");
		expect(findShortcutConflict(bindings, "next", "Mod+BracketLeft", true)).toBeUndefined();
	});
	test("matches physical digits even when Shift changes the printable character", () => {
		expect(matchesShortcut(event(), DEFAULT_NARRATOR_SHORTCUTS.narrator1, true)).toBe(true);
		expect(matchesShortcut(event(), DEFAULT_NARRATOR_SHORTCUTS.narrator1, false)).toBe(false);
		expect(
			matchesShortcut(
				event({ metaKey: false, ctrlKey: true }),
				DEFAULT_NARRATOR_SHORTCUTS.narrator1,
				false,
			),
		).toBe(true);
	});
	test.each([
		{ altKey: true },
		{ repeat: true },
		{ isComposing: true },
		{ defaultPrevented: true },
	])("does not intercept extra modifiers or already handled input %j", (override) => {
		expect(matchesShortcut(event(override), "Mod+Shift+Digit1", true)).toBe(false);
	});
	test("detects alias conflicts on the current platform", () => {
		expect(findShortcutConflict(DEFAULT_NARRATOR_SHORTCUTS, "next", "Cmd+Shift+1", true)).toBe(
			"narrator1",
		);
		expect(findShortcutConflict(DEFAULT_NARRATOR_SHORTCUTS, "next", "Ctrl+Shift+1", false)).toBe(
			"narrator1",
		);
		expect(findShortcutConflict(DEFAULT_NARRATOR_SHORTCUTS, "next", "", true)).toBeUndefined();
		expect(
			findShortcutConflict(DEFAULT_NARRATOR_SHORTCUTS, "narrator1", "Cmd+Shift+1", true),
		).toBeUndefined();
	});
	test("formats readable platform-specific combinations", () => {
		expect(formatShortcut("Mod+Alt+Digit1", true)).toBe("Cmd + Option + 1");
		expect(formatShortcut("Mod+Alt+Digit1", false)).toBe("Ctrl + Alt + 1");
	});
	test.each([
		"bad",
		"null",
		"[]",
		"x".repeat(4097),
		'{"next":42}',
	])("stored garbage falls back safely: %s", (raw) => {
		expect(parseShortcutBindings(raw)).toEqual(DEFAULT_NARRATOR_SHORTCUTS);
	});
});
