import { afterEach, describe, expect, test } from "bun:test";
import { readLocalPref } from "./useLocalPref";

const CONTEXT_MENU_KEY = "narrafork_git_identity_context_menu" as const;
const CREATE_MODAL_KEY = "narrafork_git_identity_create_modal" as const;

describe("git identity visibility prefs", () => {
	const previous = Object.getOwnPropertyDescriptor(globalThis, "localStorage");

	afterEach(() => {
		if (previous) Object.defineProperty(globalThis, "localStorage", previous);
		else Reflect.deleteProperty(globalThis, "localStorage");
	});

	function install(store: Map<string, string>) {
		Object.defineProperty(globalThis, "localStorage", {
			configurable: true,
			value: {
				getItem: (key: string) => store.get(key) ?? null,
				setItem: (key: string, value: string) => {
					store.set(key, value);
				},
				removeItem: (key: string) => {
					store.delete(key);
				},
			},
		});
	}

	test("缺失、损坏和非 true 的值都按关闭处理", () => {
		const store = new Map<string, string>();
		install(store);
		expect(readLocalPref(CONTEXT_MENU_KEY)).toBe(false);
		expect(readLocalPref(CREATE_MODAL_KEY)).toBe(false);

		store.set(CONTEXT_MENU_KEY, "");
		store.set(CREATE_MODAL_KEY, "{");
		expect(readLocalPref(CONTEXT_MENU_KEY)).toBe(false);
		expect(readLocalPref(CREATE_MODAL_KEY)).toBe(false);

		store.set(CONTEXT_MENU_KEY, "yes");
		store.set(CREATE_MODAL_KEY, "1");
		expect(readLocalPref(CONTEXT_MENU_KEY)).toBe(false);
		expect(readLocalPref(CREATE_MODAL_KEY)).toBe(false);
	});

	test("两个开关互相独立，可以同时开启", () => {
		const store = new Map<string, string>();
		install(store);
		store.set(CONTEXT_MENU_KEY, "true");
		expect(readLocalPref(CONTEXT_MENU_KEY)).toBe(true);
		expect(readLocalPref(CREATE_MODAL_KEY)).toBe(false);

		store.set(CREATE_MODAL_KEY, "true");
		expect(readLocalPref(CONTEXT_MENU_KEY)).toBe(true);
		expect(readLocalPref(CREATE_MODAL_KEY)).toBe(true);
	});

	test("存储不可用时按关闭处理", () => {
		Object.defineProperty(globalThis, "localStorage", {
			configurable: true,
			value: {
				getItem() {
					throw new Error("blocked");
				},
			},
		});
		expect(readLocalPref(CONTEXT_MENU_KEY)).toBe(false);
		expect(readLocalPref(CREATE_MODAL_KEY)).toBe(false);
	});
});
