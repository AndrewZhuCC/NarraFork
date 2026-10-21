import { afterEach, beforeEach, describe, expect, test } from "bun:test";
import { MantineProvider } from "@mantine/core";
import i18next, { type i18n } from "i18next";
import { parseHTML } from "linkedom";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { I18nextProvider, initReactI18next } from "react-i18next";
import type { OpencodeUsageCache } from "../../../lib/api/types";
import narratorLocale from "../../../locales/en/narrator.json";
import { buildKimiQuotaView } from "./kimi-usage-format";
import { OpencodeQuotaIndicator } from "./OpencodeQuotaIndicator";
import { QuotaPanelIndicator } from "./QuotaPanelIndicator";

let root: Root;
let host: HTMLDivElement;
let instance: i18n;
const previousGlobals = new Map<string, PropertyDescriptor | undefined>();

beforeEach(async () => {
	const { window } = parseHTML("<!doctype html><html><head></head><body></body></html>");
	const globals = {
		window,
		document: window.document,
		navigator: window.navigator,
		HTMLElement: window.HTMLElement,
		Element: window.Element,
		Node: window.Node,
		Event: window.Event,
		getComputedStyle: () => ({ getPropertyValue: () => "", direction: "ltr" }),
		requestAnimationFrame: (callback: FrameRequestCallback) => setTimeout(() => callback(0), 0),
		cancelAnimationFrame: (id: number) => clearTimeout(id),
		matchMedia: () => ({
			matches: false,
			addEventListener() {},
			removeEventListener() {},
			addListener() {},
			removeListener() {},
		}),
		IS_REACT_ACT_ENVIRONMENT: true,
	};
	for (const [key, value] of Object.entries(globals)) {
		previousGlobals.set(key, Object.getOwnPropertyDescriptor(globalThis, key));
		Object.defineProperty(globalThis, key, { configurable: true, writable: true, value });
	}
	Object.assign(window, {
		matchMedia: globals.matchMedia,
		requestAnimationFrame: globals.requestAnimationFrame,
		cancelAnimationFrame: globals.cancelAnimationFrame,
		setInterval,
		clearInterval,
	});
	instance = i18next.createInstance();
	await instance.use(initReactI18next).init({
		lng: "en",
		resources: { en: { narrator: narratorLocale } },
		defaultNS: "narrator",
		interpolation: { escapeValue: false },
	});
	host = document.createElement("div");
	document.body.append(host);
	root = createRoot(host);
});

afterEach(async () => {
	// Unmount and drain React's scheduler while the fake `window` still exists:
	// the Popover's passive effects are scheduled as macrotasks, and one firing
	// after the globals are restored reads `window.event` and throws
	// "window is not defined" as an unhandled error in the NEXT test file.
	await act(async () => root.unmount());
	await act(async () => {
		await new Promise((resolve) => setTimeout(resolve, 0));
	});
	host.remove();
	for (const [key, descriptor] of previousGlobals) {
		if (descriptor) Object.defineProperty(globalThis, key, descriptor);
		else delete (globalThis as Record<string, unknown>)[key];
	}
	previousGlobals.clear();
});

const NOW = Date.now();

function usage(overrides: Partial<OpencodeUsageCache> = {}): OpencodeUsageCache {
	return {
		rolling: {
			status: "ok",
			percent: 37,
			resetsAt: new Date(NOW + 2 * 3_600_000).toISOString(),
		},
		weekly: { status: "ok", percent: 12, resetsAt: null },
		monthly: null,
		generation: "g1",
		fetchedAt: NOW,
		error: null,
		...overrides,
	};
}

function mount(props: Parameters<typeof OpencodeQuotaIndicator>[0]) {
	act(() => {
		root.render(
			<I18nextProvider i18n={instance}>
				<MantineProvider env="test">
					<OpencodeQuotaIndicator transitionDuration={0} {...props} />
				</MantineProvider>
			</I18nextProvider>,
		);
	});
}

function trigger(): HTMLElement {
	const button = host.querySelector("button");
	if (!button) throw new Error("indicator button not rendered");
	return button as HTMLElement;
}

async function openPanel(): Promise<string> {
	await act(async () => {
		trigger().dispatchEvent(new window.Event("click", { bubbles: true }));
	});
	return document.body.textContent ?? "";
}

describe("OpencodeQuotaIndicator", () => {
	test("renders nothing for a cold cache", () => {
		mount({ usage: undefined });
		expect(host.querySelector("button")).toBeNull();
	});

	test("badge names the limiting window; the panel lists only reported windows", async () => {
		mount({ usage: usage() });
		// One vertical bar per reported window; the limiting one leads the label.
		const bars = trigger().querySelectorAll("[data-quota-bar]");
		expect([...bars].map((bar) => bar.getAttribute("data-quota-bar"))).toEqual([
			"fiveHour",
			"weekly",
		]);
		// Rolling has 63% left, weekly 88%: the rolling window bounds the account.
		expect(trigger().getAttribute("aria-label")).toContain("5h 63%");
		const panel = await openPanel();
		expect(panel).toContain("5-hour limit");
		expect(panel).toContain("Weekly limit");
		expect(panel).not.toContain("Monthly limit");
		expect(panel).toContain("63% left");
		// Weekly has no upstream reset, so it is counted down to next Monday.
		expect(panel).toContain("Resets Monday, in");
	});

	test("an OpenCode quota wait turns the bars red and shows the wait in the panel", async () => {
		mount({ usage: usage(), waitingForQuota: true, waitingText: "Waiting for quota · soon" });
		const levels = [...trigger().querySelectorAll("[data-quota-bar]")].map((bar) =>
			bar.getAttribute("data-level"),
		);
		expect(levels).toEqual(["critical", "critical"]);
		expect(await openPanel()).toContain("Waiting for quota · soon");
	});

	test("a failed refresh with no numbers shows the unavailable placeholder", () => {
		mount({
			usage: usage({ rolling: null, weekly: null, error: undefined, hasError: true }),
		});
		expect(trigger().textContent).toBe("OpenCode Go usage unavailable");
	});
});

describe("QuotaPanelIndicator with a Kimi allowance", () => {
	function mountKimi(waitingForQuota = false) {
		const view = buildKimiQuotaView(
			{
				fiveHour: {
					used: 92,
					limit: 100,
					remaining: 8,
					resetTime: new Date(NOW + 3_600_000).toISOString(),
				},
				weekly: { used: 20, limit: 100, remaining: 80, resetTime: null },
				monthly: null,
				extraWindows: [],
				fetchedAt: NOW,
				error: null,
			},
			instance.t.bind(instance),
		);
		act(() => {
			root.render(
				<I18nextProvider i18n={instance}>
					<MantineProvider env="test">
						<QuotaPanelIndicator
							view={view}
							waitingForQuota={waitingForQuota}
							waitingText="Waiting for quota"
							transitionDuration={0}
						/>
					</MantineProvider>
				</I18nextProvider>,
			);
		});
	}

	test("trigger draws a bar per window; panel has bars, amounts and countdown", async () => {
		mountKimi();
		const levels = [...trigger().querySelectorAll("[data-quota-bar]")].map((bar) =>
			bar.getAttribute("data-level"),
		);
		expect(levels).toEqual(["critical", "ok"]);
		expect(trigger().getAttribute("aria-label")).toContain("5h 8%");
		const panel = await openPanel();
		expect(panel).toContain("Kimi usage");
		expect(panel).toContain("5-hour limit");
		expect(panel).toContain("Weekly limit");
		expect(panel).toContain("Used 92 / 100");
		expect(panel).toContain("Resets in");
		expect(document.body.querySelectorAll(".mantine-Progress-root").length).toBe(2);
	});

	test("a Kimi quota wait shows the wait text in the panel", async () => {
		mountKimi(true);
		expect(await openPanel()).toContain("Waiting for quota");
	});
});
