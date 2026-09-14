import { expect, test } from "bun:test";
import { MantineProvider } from "@mantine/core";
import { createInstance } from "i18next";
import { parseHTML } from "linkedom";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { I18nextProvider } from "react-i18next";
import { useNarratorShortcuts } from "../../hooks/useNarratorShortcuts";
import { formatShortcut, isMacShortcutPlatform } from "../../lib/narrator-shortcuts";
import translations from "../../locales/en/settings.json";
import { NarratorShortcutSettings } from "./NarratorShortcutSettings";

test("shortcut recorder captures keys, cancels, rejects conflicts, clears and resets", async () => {
	const { window } = parseHTML("<!doctype html><html><body></body></html>");
	const values = new Map<string, string>();
	const globals = {
		window,
		document: window.document,
		navigator: window.navigator,
		HTMLElement: window.HTMLElement,
		Element: window.Element,
		Node: window.Node,
		IS_REACT_ACT_ENVIRONMENT: true,
		localStorage: {
			getItem: (key: string) => values.get(key) ?? null,
			setItem: (key: string, value: string) => values.set(key, value),
			removeItem: (key: string) => values.delete(key),
		},
		matchMedia: () => ({ matches: false, addEventListener() {}, removeEventListener() {} }),
	};
	const previous = new Map(
		Object.keys(globals).map((key) => [key, Object.getOwnPropertyDescriptor(globalThis, key)]),
	);
	for (const [key, value] of Object.entries(globals)) {
		Object.defineProperty(globalThis, key, { configurable: true, writable: true, value });
	}
	const i18n = createInstance();
	await i18n.init({ lng: "en", resources: { en: { settings: translations } } });
	const container = document.createElement("div");
	document.body.appendChild(container);
	const root = createRoot(container);
	let api: ReturnType<typeof useNarratorShortcuts> | undefined;
	function Probe() {
		api = useNarratorShortcuts();
		return <NarratorShortcutSettings />;
	}
	const click = (element: Element) =>
		act(() => {
			element.dispatchEvent(new window.Event("click", { bubbles: true }));
		});
	const press = (
		element: Element,
		key: string,
		code: string,
		options: Partial<KeyboardEvent> = {},
	) => {
		const event = Object.assign(new window.Event("keydown", { bubbles: true, cancelable: true }), {
			key,
			code,
			ctrlKey: false,
			metaKey: false,
			shiftKey: false,
			altKey: false,
			repeat: false,
			isComposing: false,
			...options,
		});
		act(() => {
			element.dispatchEvent(event);
		});
		return event;
	};
	try {
		act(() => {
			root.render(
				<I18nextProvider i18n={i18n}>
					<MantineProvider env="test">
						<Probe />
					</MantineProvider>
				</I18nextProvider>,
			);
		});
		if (!api) throw new Error("Missing shortcut hook");
		const defaults = { ...api.bindings };
		const buttons = container.querySelectorAll("button[aria-pressed]");
		const button = buttons[0];
		if (!button) throw new Error("Missing shortcut recorder");
		expect(buttons.length).toBe(13);
		expect(api.bindings.focusComposer).toBe("Tab");
		expect(container.querySelector("input")).toBeNull();
		expect(container.textContent).not.toContain("macOS reserves");
		expect(container.textContent).not.toContain("sync across devices");
		expect(button.textContent).toBe(formatShortcut(defaults.previous, isMacShortcutPlatform()));
		press(button, "K", "KeyK", { ctrlKey: true });
		expect(api.bindings.previous).toBe(defaults.previous);

		let focusRequests = 0;
		(button as HTMLButtonElement).focus = () => {
			focusRequests += 1;
		};
		click(button);
		expect(focusRequests).toBe(1);
		expect(button.textContent).toContain("Press a shortcut");
		press(button, "Control", "ControlLeft", { ctrlKey: true });
		press(button, "K", "KeyK", { ctrlKey: true, repeat: true });
		press(button, "K", "KeyK", { ctrlKey: true, isComposing: true });
		expect(api.bindings.previous).toBe(defaults.previous);
		expect(press(button, "K", "KeyK", { ctrlKey: true, shiftKey: true }).defaultPrevented).toBe(
			true,
		);
		expect(api.bindings.previous).toBe("Control+Shift+KeyK");
		expect(button.getAttribute("aria-pressed")).toBe("false");
		expect(JSON.parse(values.get("narrafork_narrator_shortcuts") ?? "{}").previous).toBe(
			"Control+Shift+KeyK",
		);

		click(button);
		press(button, "a", "KeyA");
		expect(container.textContent).toContain("Cannot use this key");
		expect(api.bindings.previous).toBe("Control+Shift+KeyK");
		press(button, "Escape", "Escape");
		expect(button.getAttribute("aria-pressed")).toBe("false");
		expect(container.textContent).not.toContain("Cannot use this key");

		click(button);
		act(() => api?.setBinding("next", "Alt+Shift+KeyJ"));
		press(button, "J", "KeyJ", { altKey: true, shiftKey: true });
		expect(container.textContent).toContain("Already assigned");
		expect(api.bindings.previous).toBe("Control+Shift+KeyK");
		expect(button.getAttribute("aria-pressed")).toBe("true");
		press(button, "!", "Digit1", { metaKey: true, altKey: true, shiftKey: true });
		expect(api.bindings.previous).toBe("Meta+Alt+Shift+Digit1");
		expect(container.textContent).not.toContain("Already assigned");

		click(button);
		expect(press(button, "Tab", "Tab").defaultPrevented).toBe(false);
		expect(button.getAttribute("aria-pressed")).toBe("false");
		click(button);
		act(() => {
			button.dispatchEvent(new window.Event("focusout", { bubbles: true }));
		});
		expect(button.getAttribute("aria-pressed")).toBe("false");
		expect(api.bindings.previous).toBe("Meta+Alt+Shift+Digit1");

		const clear = container.querySelector(
			'button[aria-label="Clear shortcut for Previous narrator"]',
		);
		if (!clear) throw new Error("Missing clear button");
		click(clear);
		expect(api.bindings.previous).toBe("");
		expect(button.textContent).toBe("Disabled");
		click(button);
		press(button, "F6", "F6");
		expect(api.bindings.previous).toBe("F6");
		const home = buttons[11];
		click(home);
		press(home, "Home", "Home");
		expect(api.bindings.readFromStart).toBe("Home");
		const composer = buttons[12];
		expect(composer.textContent).toBe("Tab");
		click(composer);
		press(composer, "F7", "F7");
		expect(api.bindings.focusComposer).toBe("F7");
		click(composer);
		expect(press(composer, "Tab", "Tab", { shiftKey: true }).defaultPrevented).toBe(false);
		expect(api.bindings.focusComposer).toBe("F7");
		click(composer);
		expect(press(composer, "Tab", "Tab").defaultPrevented).toBe(true);
		expect(api.bindings.focusComposer).toBe("Tab");
		const clearComposer = container.querySelector(
			'button[aria-label="Clear shortcut for Focus narrator input"]',
		);
		if (!clearComposer) throw new Error("Missing composer clear button");
		click(clearComposer);
		expect(api.bindings.focusComposer).toBe("");

		values.set("narrafork_narrator_shortcuts", JSON.stringify({ ...api.bindings, previous: "F8" }));
		act(() => {
			window.dispatchEvent(
				Object.assign(new window.Event("storage"), { key: "narrafork_narrator_shortcuts" }),
			);
		});
		expect(button.textContent).toBe("F8");
		const reset = [...container.querySelectorAll("button")].find(
			(element) => element.textContent === "Restore all default shortcuts",
		);
		if (!reset) throw new Error("Missing reset button");
		click(reset);
		expect(api.bindings).toEqual(defaults);
	} finally {
		act(() => root.unmount());
		container.remove();
		for (const [key, descriptor] of previous) {
			if (descriptor) Object.defineProperty(globalThis, key, descriptor);
			else Reflect.deleteProperty(globalThis, key);
		}
	}
});
