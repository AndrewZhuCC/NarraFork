import { afterEach, beforeEach, expect, test } from "bun:test";
import { parseHTML } from "linkedom";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { useFirstScreenActivity } from "./useFirstScreenActivity";

let root: Root;
let host: HTMLElement;
let enabled = false;
const originals = new Map<string, PropertyDescriptor | undefined>();
let nextTimer = 0;
const timers = new Map<number, { callback: () => void; delay: number }>();

beforeEach(() => {
	const { window } = parseHTML("<html><body></body></html>");
	for (const [key, value] of Object.entries({
		window,
		document: window.document,
		navigator: window.navigator,
		IS_REACT_ACT_ENVIRONMENT: true,
	})) {
		originals.set(key, Object.getOwnPropertyDescriptor(globalThis, key));
		Object.defineProperty(globalThis, key, { configurable: true, writable: true, value });
	}
	for (const key of ["setTimeout", "clearTimeout"])
		originals.set(key, Object.getOwnPropertyDescriptor(globalThis, key));
	Object.defineProperty(globalThis, "setTimeout", {
		configurable: true,
		value: (callback: () => void, delay: number) => {
			const id = ++nextTimer;
			timers.set(id, { callback, delay });
			return id;
		},
	});
	Object.defineProperty(globalThis, "clearTimeout", {
		configurable: true,
		value: (id: number) => {
			timers.delete(id);
		},
	});
	host = document.createElement("div");
	document.body.append(host);
	root = createRoot(host);
	enabled = false;
	timers.clear();
});

afterEach(async () => {
	await act(async () => root.unmount());
	host.remove();
	for (const [key, value] of originals) {
		if (value) Object.defineProperty(globalThis, key, value);
		else Reflect.deleteProperty(globalThis, key);
	}
	originals.clear();
	timers.clear();
});

function Harness({ id, tail, delay }: { id: string; tail: boolean; delay?: number }) {
	enabled = useFirstScreenActivity(id, tail, delay);
	return null;
}
const render = (id: string, tail: boolean, delay?: number) =>
	act(async () => root.render(<Harness id={id} tail={tail} delay={delay} />));

test("noncritical work waits for tail adoption, with a bounded fallback", async () => {
	await render("n1", false);
	expect(enabled).toBe(false);
	expect([...timers.values()].map((timer) => timer.delay)).toEqual([1500]);
	await render("n1", true);
	expect(enabled).toBe(true);
	expect(timers.size).toBe(0);
});

test("fallback releases a slow first screen and narrator switching revokes the previous release", async () => {
	await render("n1", false, 60_000);
	const timer = [...timers.values()][0];
	expect(timer.delay).toBe(5000);
	await act(async () => timer.callback());
	expect(enabled).toBe(true);
	await render("n2", false);
	expect(enabled).toBe(false);
	await render("n2", true);
	expect(enabled).toBe(true);
});

test("rapidly returning to a previously released narrator does not reuse its old release", async () => {
	await render("n1", true);
	expect(enabled).toBe(true);
	await render("n2", false);
	expect(enabled).toBe(false);
	await render("n1", false);
	expect(enabled).toBe(false);
});

test("switch and unmount cancel outstanding old-screen timers", async () => {
	await render("n1", false);
	const previous = [...timers.keys()][0];
	await render("n2", false);
	expect(timers.has(previous)).toBe(false);
	expect(enabled).toBe(false);
	await render("", false);
	expect(enabled).toBe(false);
	expect(timers.size).toBe(0);
});
