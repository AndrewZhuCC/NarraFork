import { afterEach, beforeEach, describe, expect, spyOn, test } from "bun:test";
import { MantineProvider } from "@mantine/core";
import i18next, { type i18n } from "i18next";
import { parseHTML } from "linkedom";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { I18nextProvider, initReactI18next } from "react-i18next";
import type { ProviderBalanceSnapshot } from "../../../../shared/provider-balance";
import en from "../../../locales/en/narrator.json";
import zh from "../../../locales/zh-CN/narrator.json";
import { Sub2ApiSubscriptionQuotaIndicator } from "./Sub2ApiSubscriptionQuotaIndicator";

let root: Root;
let host: HTMLDivElement;
let instance: i18n;
const previousGlobals = new Map<string, PropertyDescriptor | undefined>();
const NOW = Date.parse("2026-10-21T12:00:00Z");
let now = NOW;
let dateNow: ReturnType<typeof spyOn<typeof Date, "now">>;
let nextIntervalId = 0;
let parentRenders = 0;
const intervals = new Map<number, { callback: () => void; delay: number }>();

beforeEach(async () => {
	now = NOW;
	parentRenders = 0;
	intervals.clear();
	dateNow = spyOn(Date, "now").mockImplementation(() => now);
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
		setInterval: (callback: () => void, delay: number) => {
			const id = ++nextIntervalId;
			intervals.set(id, { callback, delay });
			return id;
		},
		clearInterval: (id: number) => intervals.delete(id),
	});
	instance = i18next.createInstance();
	await instance.use(initReactI18next).init({
		lng: "en",
		resources: { en: { narrator: en }, "zh-CN": { narrator: zh } },
		defaultNS: "narrator",
		interpolation: { escapeValue: false },
	});
	host = document.createElement("div");
	document.body.append(host);
	root = createRoot(host);
});

afterEach(async () => {
	await act(async () => root.unmount());
	// Drain Popover effects before restoring the fake browser globals.
	await act(async () => {
		await new Promise((resolve) => setTimeout(resolve, 0));
	});
	host.remove();
	for (const [key, descriptor] of previousGlobals) {
		if (descriptor) Object.defineProperty(globalThis, key, descriptor);
		else delete (globalThis as Record<string, unknown>)[key];
	}
	previousGlobals.clear();
	dateNow.mockRestore();
	intervals.clear();
});

function snapshot(): ProviderBalanceSnapshot {
	return {
		adapter: "sub2api",
		status: "supported",
		generation: "g1",
		balance: {
			scope: "subscription",
			unlimited: false,
			planName: "<img src=x onerror=alert(1)>",
			expiresAt: "2026-11-01T00:00:00Z",
			amounts: [
				{ unit: "USD", window: "daily", used: 2.5, total: 10, remaining: 7.5 },
				{
					unit: "USD",
					window: "weekly",
					used: 90,
					total: 100,
					remaining: 10,
					windowStart: "2026-10-20T00:00:00Z",
				},
				{ unit: "USD", window: "monthly", used: 200, total: 1000, remaining: 800 },
			],
		},
		fetchedAt: NOW,
		attemptedAt: NOW,
		error: null,
	};
}

function Parent({
	data,
	queryFailed,
}: {
	data: ProviderBalanceSnapshot | null | undefined;
	queryFailed: boolean;
}) {
	parentRenders++;
	return (
		<I18nextProvider i18n={instance}>
			<MantineProvider env="test">
				<Sub2ApiSubscriptionQuotaIndicator
					snapshot={data}
					queryFailed={queryFailed}
					transitionDuration={0}
				/>
			</MantineProvider>
		</I18nextProvider>
	);
}
function renderQuotaPanelIndicator(data = snapshot(), queryFailed = false) {
	act(() => root.render(<Parent data={data} queryFailed={queryFailed} />));
}
function tick(milliseconds: number) {
	now += milliseconds;
	act(() => {
		for (const { callback } of intervals.values()) callback();
	});
}
function trigger(): HTMLElement {
	const button = host.querySelector("button");
	if (!button) throw new Error("Expected indicator button");
	return button;
}
async function openPanel(event = "click") {
	await act(async () => {
		trigger().dispatchEvent(new window.Event(event, { bubbles: true }));
	});
	return document.body.textContent ?? "";
}

describe("production Sub2ApiSubscriptionQuotaIndicator", () => {
	test.each(["en", "zh-CN"])("small USD allowances never show used 0 / 0 (%s)", async (locale) => {
		await instance.changeLanguage(locale);
		const data = snapshot();
		if (!data.balance) throw new Error("Missing balance");
		data.balance.amounts = [
			{ unit: "USD", window: "daily", used: 0.001, total: 0.004, remaining: 0.003 },
		];
		renderQuotaPanelIndicator(data);
		const panel = await openPanel();
		expect(panel).toContain(
			instance.t("quotaPanel.usedOfLimit", { used: "0.001", limit: "0.004" }),
		);
		expect(panel).not.toContain(instance.t("quotaPanel.usedOfLimit", { used: "0", limit: "0" }));
	});
	test("one 30-second clock marks aged data stale without rerendering its parent", async () => {
		renderQuotaPanelIndicator();
		expect([...intervals.values()].map(({ delay }) => delay)).toEqual([30_000]);
		const renders = parentRenders;
		tick(120_000);
		expect(trigger().getAttribute("aria-label")).not.toContain(instance.t("quotaPanel.staleHint"));
		tick(30_000);
		expect(parentRenders).toBe(renders);
		expect(intervals.size).toBe(1);
		expect(trigger().querySelector("[data-quota-bars]")?.getAttribute("style")).toContain(
			"opacity:0.55",
		);
		const panel = await openPanel();
		expect(panel).toContain(instance.t("quotaPanel.staleHint"));
		expect(panel).not.toContain(instance.t("sub2apiSubscription.refreshFailed"));
	});

	test("the wrapper clock transitions an unlimited subscription into expired", async () => {
		const data = snapshot();
		if (!data.balance) throw new Error("Missing balance");
		data.balance.unlimited = true;
		data.balance.expiresAt = new Date(NOW + 30_000).toISOString();
		renderQuotaPanelIndicator(data);
		expect(trigger().textContent).toBe(instance.t("sub2apiSubscription.unlimited"));
		const renders = parentRenders;
		tick(30_000);
		expect(parentRenders).toBe(renders);
		expect(trigger().textContent).toBe(instance.t("sub2apiSubscription.expired"));
		expect(trigger().querySelector("[data-quota-bar]")).toBeNull();
		expect(await openPanel()).toContain(instance.t("sub2apiSubscription.expiredDetail"));
	});

	test("wallet/key/pending hide the widget and a new generation replaces all old metadata", async () => {
		const old = snapshot();
		renderQuotaPanelIndicator(old);
		await openPanel();
		if (!old.balance) throw new Error("Missing balance");
		for (const data of [
			{ ...old, balance: { ...old.balance, scope: "wallet" as const } },
			{ ...old, balance: { ...old.balance, scope: "key" as const } },
			{ ...old, status: "pending" as const },
		]) {
			renderQuotaPanelIndicator(data);
			expect(host.querySelector("button")).toBeNull();
			expect(document.body.textContent).not.toContain(old.balance.planName);
			expect(intervals.size).toBe(0);
		}
		const next = snapshot();
		next.generation = "g2";
		if (!next.balance) throw new Error("Missing balance");
		next.balance.planName = "New plan";
		next.balance.expiresAt = undefined;
		next.balance.amounts = [{ unit: "USD", window: "daily", used: 1, total: 2, remaining: 1 }];
		renderQuotaPanelIndicator(next);
		expect(intervals.size).toBe(1);
		if (trigger().getAttribute("aria-expanded") !== "true") await openPanel();
		const panel = document.body.textContent ?? "";
		expect(panel).toContain("New plan");
		expect(panel).toContain("Used 1 / 2");
		expect(panel).not.toContain(old.balance.planName);
		expect(panel).not.toContain("Weekly window start");
		expect(panel).not.toContain("Expires at");
		expect(trigger().querySelectorAll("[data-quota-bar]")).toHaveLength(1);
	});

	test.each(["en", "zh-CN"])("explicit unlimited has no percentage bars (%s)", async (locale) => {
		await instance.changeLanguage(locale);
		const data = snapshot();
		if (!data.balance) throw new Error("Missing balance");
		data.balance.unlimited = true;
		data.balance.amounts = [];
		renderQuotaPanelIndicator(data);
		expect(trigger().textContent).toBe(instance.t("sub2apiSubscription.unlimited"));
		expect(trigger().querySelector("[data-quota-bar]")).toBeNull();
		const panel = await openPanel();
		expect(panel).toContain(instance.t("sub2apiSubscription.unlimited"));
		expect(document.body.querySelector(".mantine-Progress-root")).toBeNull();
		expect(panel).not.toContain("%");
	});
	test("click opens daily/weekly/monthly USD amounts with metadata but no invented resets", async () => {
		renderQuotaPanelIndicator();
		expect(
			[...trigger().querySelectorAll("[data-quota-bar]")].map((bar) =>
				bar.getAttribute("data-quota-bar"),
			),
		).toEqual(["daily", "weekly", "monthly"]);
		expect(trigger().getAttribute("aria-label")).toContain("Day 75%");
		expect(trigger().getAttribute("aria-expanded")).toBe("false");
		const panel = await openPanel();
		expect(trigger().getAttribute("aria-expanded")).toBe("true");
		expect(panel).toContain("sub2api subscription quota");
		expect(panel).toContain("Daily limit (USD)");
		expect(panel).toContain("Weekly limit (USD)");
		expect(panel).toContain("Monthly limit (USD)");
		expect(panel).toContain("Used 2.5 / 10");
		expect(panel).toContain("Used 90 / 100");
		expect(panel).toContain("Weekly window start");
		expect(panel).toContain("Expires at");
		expect(panel).toContain("The API does not provide reset times.");
		expect(panel).toContain("<img src=x onerror=alert(1)>");
		expect(document.body.querySelector("img")).toBeNull();
		expect(panel).not.toContain("Resets in");
		expect(panel).not.toContain("Monday");
		expect(document.body.querySelectorAll(".mantine-Progress-root")).toHaveLength(3);
		await openPanel();
		expect(trigger().getAttribute("aria-expanded")).toBe("false");
	});

	test("pointer hover opens the same translated Chinese details", async () => {
		await instance.changeLanguage("zh-CN");
		renderQuotaPanelIndicator();
		expect(trigger().getAttribute("aria-label")).toContain("日 75%");
		const panel = await openPanel("pointerover");
		expect(trigger().getAttribute("aria-expanded")).toBe("true");
		expect(panel).toContain("sub2api 订阅额度");
		expect(panel).toContain("日额度（USD）");
		expect(panel).toContain("已用 2.5 / 10");
		expect(panel).toContain("套餐名称");
		expect(panel).toContain("到期时间");
		expect(panel).toContain("周窗口起点");
		expect(panel).toContain("接口未提供重置时间。");
		expect(panel).not.toContain("后重置");
		expect(panel).not.toContain("下周一");
	});

	test.each([
		"en",
		"zh-CN",
	])("unknown allowance remains clickable without fake bars (%s)", async (locale) => {
		await instance.changeLanguage(locale);
		const data = snapshot();
		if (!data.balance) throw new Error("Missing balance");
		data.balance.amounts = [{ unit: "USD", window: "daily", total: 0, used: 0, remaining: 0 }];
		renderQuotaPanelIndicator(data);
		expect(trigger().textContent).toBe(instance.t("sub2apiSubscription.unknown"));
		expect(trigger().querySelector("[data-quota-bar]")).toBeNull();
		const panel = await openPanel();
		expect(panel).toContain(instance.t("sub2apiSubscription.resetUnavailable"));
		expect(document.body.querySelector(".mantine-Progress-root")).toBeNull();
		expect(panel).not.toContain("0%");
	});

	test.each([
		"en",
		"zh-CN",
	])("expired allowance is unavailable instead of a green remaining bar (%s)", async (locale) => {
		await instance.changeLanguage(locale);
		const data = snapshot();
		if (!data.balance) throw new Error("Missing balance");
		data.balance.expiresAt = new Date(NOW).toISOString();
		renderQuotaPanelIndicator(data);
		expect(trigger().textContent).toBe(instance.t("sub2apiSubscription.expired"));
		expect(trigger().querySelector("[data-quota-bar]")).toBeNull();
		const panel = await openPanel();
		expect(panel).toContain(instance.t("sub2apiSubscription.expiredDetail"));
		expect(panel).not.toContain(instance.t("quotaPanel.staleHint"));
	});

	test.each([
		"en",
		"zh-CN",
	])("stale data retains USD figures, dims bars and never exposes error codes (%s)", async (locale) => {
		await instance.changeLanguage(locale);
		const data = snapshot();
		data.error = "request_failed";
		renderQuotaPanelIndicator(data, true);
		expect(trigger().querySelector("[data-quota-bars]")?.getAttribute("style")).toContain(
			"opacity:0.55",
		);
		const panel = await openPanel();
		expect(panel).toContain("USD");
		expect(panel).toContain("2.5 / 10");
		expect(panel).toContain(instance.t("quotaPanel.staleHint"));
		expect(panel).toContain(instance.t("sub2apiSubscription.refreshFailed"));
		expect(panel).not.toContain("request_failed");
	});
});
