import { afterEach, beforeEach, describe, expect, test } from "bun:test";

let restore: (() => void) | undefined;

beforeEach(async () => {
	const { parseHTML } = await import("linkedom");
	const previous = new Map<string, PropertyDescriptor | undefined>();
	for (const key of ["window", "document", "localStorage", "IS_REACT_ACT_ENVIRONMENT"] as const) {
		previous.set(key, Object.getOwnPropertyDescriptor(globalThis, key));
	}
	const { window } = parseHTML("<!doctype html><html><body /></html>");
	const storage = new Map<string, string>();
	const values = {
		IS_REACT_ACT_ENVIRONMENT: true,
		window,
		document: window.document,
		localStorage: {
			getItem: (key: string) => storage.get(key) ?? null,
			setItem: (key: string, value: string) => storage.set(key, value),
			removeItem: (key: string) => storage.delete(key),
		},
	};
	for (const [key, value] of Object.entries(values)) {
		Object.defineProperty(globalThis, key, { value, configurable: true, writable: true });
	}
	restore = () => {
		for (const key of ["window", "document", "localStorage", "IS_REACT_ACT_ENVIRONMENT"] as const) {
			const descriptor = previous.get(key);
			if (descriptor) Object.defineProperty(globalThis, key, descriptor);
			else Reflect.deleteProperty(globalThis, key);
		}
	};
});

afterEach(() => {
	restore?.();
	restore = undefined;
});

function fire(type: string, clientY: number, pointerId = 1) {
	const event = document.createEvent("Event");
	event.initEvent(type, true, true);
	Object.defineProperty(event, "clientY", { value: clientY, configurable: true });
	Object.defineProperty(event, "pointerId", { value: pointerId, configurable: true });
	window.dispatchEvent(event);
}

describe("bottom spacing drag store", () => {
	test("default-off ignores drag even with legacy saved spacing", async () => {
		const { resetBottomSpacingStoreForTest, startBottomSpacingResize } = await import(
			"./useResizableBottomSpacing"
		);
		localStorage.setItem("narrafork_narrator_bottom_spacing", "80");
		resetBottomSpacingStoreForTest(80);
		let prevented = false;
		startBottomSpacingResize({
			clientY: 100,
			pointerId: 1,
			preventDefault: () => {
				prevented = true;
			},
		});
		fire("pointermove", 0);
		fire("pointerup", 0);
		expect(prevented).toBe(false);
		expect(document.body.style.cursor).not.toBe("ns-resize");
		expect(localStorage.getItem("narrafork_narrator_bottom_spacing")).toBe("80");
	});

	test("disabling during a drag cancels subsequent movement", async () => {
		const { resetBottomSpacingStoreForTest, startBottomSpacingResize } = await import(
			"./useResizableBottomSpacing"
		);
		resetBottomSpacingStoreForTest(30);
		localStorage.setItem("narrafork_narrator_bottom_spacing_resize", "true");
		startBottomSpacingResize({ clientY: 100, pointerId: 1, preventDefault: () => {} });
		localStorage.setItem("narrafork_narrator_bottom_spacing_resize", "false");
		fire("pointermove", 0);
		expect(document.body.style.cursor).toBe("");
		expect(document.body.style.userSelect).toBe("");
		expect(localStorage.getItem("narrafork_narrator_bottom_spacing")).toBe("30");
	});

	test("hook fixes spacing at zero and reacts immediately to the settings toggle", async () => {
		const { act, createElement } = await import("react");
		const { createRoot } = await import("react-dom/client");
		const { useLocalPref } = await import("./useLocalPref");
		const { useBottomSpacing, resetBottomSpacingStoreForTest, startBottomSpacingResize } =
			await import("./useResizableBottomSpacing");
		resetBottomSpacingStoreForTest(80);
		localStorage.setItem("narrafork_narrator_bottom_spacing", "80");
		let toggle: (value: boolean) => void = () => {};
		function Probe() {
			const [, setEnabled] = useLocalPref("narrafork_narrator_bottom_spacing_resize");
			toggle = setEnabled;
			return createElement("span", null, useBottomSpacing());
		}
		const container = document.createElement("div");
		document.body.appendChild(container);
		const root = createRoot(container);
		try {
			await act(async () => root.render(createElement(Probe)));
			expect(container.textContent).toBe("0");
			await act(async () => toggle(true));
			expect(container.textContent).toBe("80");
			await act(async () => {
				startBottomSpacingResize({ clientY: 100, pointerId: 1, preventDefault: () => {} });
			});
			await act(async () => toggle(false));
			expect(container.textContent).toBe("0");
			expect(document.body.style.cursor).toBe("");
			expect(localStorage.getItem("narrafork_narrator_bottom_spacing")).toBe("80");
			await act(async () => toggle(true));
			expect(container.textContent).toBe("80");
		} finally {
			await act(async () => root.unmount());
			container.remove();
		}
	});
	test("starts at zero and permits a zero-height spacer", async () => {
		const { BOTTOM_SPACING_CONSTANTS, resetBottomSpacingStoreForTest } = await import(
			"./useResizableBottomSpacing"
		);
		resetBottomSpacingStoreForTest();
		expect(BOTTOM_SPACING_CONSTANTS.DEFAULT_SPACING).toBe(0);
		expect(BOTTOM_SPACING_CONSTANTS.MIN_SPACING).toBe(0);
	});

	test("increases when dragged upward and clamps to bounds", async () => {
		const { BOTTOM_SPACING_CONSTANTS, resetBottomSpacingStoreForTest, startBottomSpacingResize } =
			await import("./useResizableBottomSpacing");
		resetBottomSpacingStoreForTest(BOTTOM_SPACING_CONSTANTS.DEFAULT_SPACING);
		localStorage.setItem("narrafork_narrator_bottom_spacing_resize", "true");
		startBottomSpacingResize({ clientY: 100, pointerId: 1, preventDefault: () => {} });
		fire("pointermove", -200);
		fire("pointerup", -200);
		expect(localStorage.getItem("narrafork_narrator_bottom_spacing")).toBe(
			String(BOTTOM_SPACING_CONSTANTS.MAX_SPACING),
		);
	});

	test("decreases when dragged downward and clamps to the minimum", async () => {
		const { BOTTOM_SPACING_CONSTANTS, resetBottomSpacingStoreForTest, startBottomSpacingResize } =
			await import("./useResizableBottomSpacing");
		resetBottomSpacingStoreForTest(BOTTOM_SPACING_CONSTANTS.DEFAULT_SPACING);
		localStorage.setItem("narrafork_narrator_bottom_spacing_resize", "true");
		startBottomSpacingResize({ clientY: 100, pointerId: 1, preventDefault: () => {} });
		fire("pointermove", 400);
		fire("pointerup", 400);
		expect(localStorage.getItem("narrafork_narrator_bottom_spacing")).toBe(
			String(BOTTOM_SPACING_CONSTANTS.MIN_SPACING),
		);
	});

	test("reset clears an active drag's listeners and body styles", async () => {
		const { resetBottomSpacingStoreForTest, startBottomSpacingResize } = await import(
			"./useResizableBottomSpacing"
		);
		resetBottomSpacingStoreForTest(20);
		localStorage.setItem("narrafork_narrator_bottom_spacing_resize", "true");
		startBottomSpacingResize({ clientY: 100, pointerId: 7, preventDefault: () => {} });
		expect(document.body.style.cursor).toBe("ns-resize");
		expect(document.body.style.userSelect).toBe("none");

		resetBottomSpacingStoreForTest(20);

		expect(document.body.style.cursor).toBe("");
		expect(document.body.style.userSelect).toBe("");
		// A late event after reset must not persist a new drag position.
		fire("pointermove", 0, 7);
		expect(localStorage.getItem("narrafork_narrator_bottom_spacing")).toBeNull();
	});
});
