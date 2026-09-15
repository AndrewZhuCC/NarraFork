import { afterEach, beforeEach, expect, test } from "bun:test";
import { MantineProvider, Modal } from "@mantine/core";
import { GENERIC_REASONING_EFFORT_TIERS } from "@shared/reasoning-effort-support";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
	createMemoryHistory,
	createRootRoute,
	createRouter,
	RouterContextProvider,
} from "@tanstack/react-router";
import i18next from "i18next";
import { parseHTML } from "linkedom";
import { act, type ComponentType } from "react";
import { createRoot, type Root } from "react-dom/client";
import { I18nextProvider, initReactI18next } from "react-i18next";
import type { ScheduledTask, ScheduledTaskInput } from "../../lib/api/scheduled-tasks";
import common from "../../locales/en/common.json";
import narrator from "../../locales/en/narrator.json";
import scheduledTasks from "../../locales/en/scheduledTasks.json";
import { Route } from "./index";

let root: Root;
let queryClient: QueryClient;
let originalFetch: typeof fetch;
let requests: Array<{ method: string; body: ScheduledTaskInput }>;
let makeEvent: (type: string) => Event;
const globals = new Map<string, PropertyDescriptor | undefined>();
const task: ScheduledTask = {
	id: "task-1",
	name: "Existing task",
	prompt: "Do work",
	enabled: true,
	cronExpr: "0 9 * * *",
	timezone: null,
	systemPrompt: null,
	model: null,
	reasoningEffort: "high",
	permissionMode: "readOnly",
	locale: "en",
	runContext: "standalone",
	cwd: null,
	projectId: null,
	chapterId: null,
	narratorMode: "reuse",
	reuseNarratorId: "narrator-1",
	createdBy: null,
	lastRunAt: null,
	nextRunAt: null,
	lastNarratorId: null,
	lastStatus: null,
	lastError: null,
	createdAt: "2026-01-01",
	updatedAt: "2026-01-01",
};

beforeEach(() => {
	const { window } = parseHTML("<!doctype html><html><body></body></html>");
	makeEvent = (type) => new window.Event(type, { bubbles: true });
	Object.defineProperty(window.document, "fonts", {
		value: {
			addEventListener() {},
			removeEventListener() {},
		},
	});
	for (const proto of [window.HTMLInputElement.prototype, window.HTMLTextAreaElement.prototype]) {
		Object.defineProperties(proto, {
			attachEvent: { value: () => {}, configurable: true },
			detachEvent: { value: () => {}, configurable: true },
			select: { value: () => {}, configurable: true },
			setSelectionRange: { value: () => {}, configurable: true },
		});
	}
	Object.defineProperty(window.HTMLElement.prototype, "scrollIntoView", {
		value: () => {},
		configurable: true,
	});
	const computedStyle = () =>
		new Proxy(
			{},
			{
				get: (_target, key) => (key === "getPropertyValue" ? () => "" : ""),
			},
		);
	const matchMedia = () => ({ matches: false, addEventListener() {}, removeEventListener() {} });
	const values: Record<string, unknown> = {
		window,
		document: window.document,
		navigator: window.navigator,
		HTMLElement: window.HTMLElement,
		HTMLInputElement: window.HTMLInputElement,
		HTMLTextAreaElement: window.HTMLTextAreaElement,
		Element: window.Element,
		Node: window.Node,
		Document: window.Document,
		ShadowRoot: class {},
		getComputedStyle: computedStyle,
		matchMedia,
		MutationObserver: window.MutationObserver,
		ResizeObserver: class {
			observe() {}
			unobserve() {}
			disconnect() {}
		},
		requestAnimationFrame: (cb: FrameRequestCallback) => setTimeout(() => cb(Date.now()), 0),
		cancelAnimationFrame: (id: Timer) => clearTimeout(id),
		localStorage: { getItem: () => null, setItem() {}, removeItem() {} },
		IS_REACT_ACT_ENVIRONMENT: true,
	};
	for (const [key, value] of Object.entries(values)) {
		globals.set(key, Object.getOwnPropertyDescriptor(globalThis, key));
		Object.defineProperty(globalThis, key, { value, configurable: true, writable: true });
	}
	Object.assign(window, { getComputedStyle: computedStyle, matchMedia });
	queryClient = new QueryClient({
		defaultOptions: {
			queries: { retry: false, staleTime: Infinity },
			mutations: { retry: false },
		},
	});
	requests = [];
	originalFetch = globalThis.fetch;
	globalThis.fetch = (async (_url, init) => {
		if (init?.method === "POST" || init?.method === "PUT") {
			requests.push({ method: init.method, body: JSON.parse(String(init.body)) });
			return Response.json(task);
		}
		return Response.json([]);
	}) as typeof fetch;
	root = createRoot(document.body.appendChild(document.createElement("div")));
});

afterEach(async () => {
	await act(async () => root.unmount());
	queryClient.clear();
	globalThis.fetch = originalFetch;
	for (const [key, descriptor] of globals) {
		if (descriptor) Object.defineProperty(globalThis, key, descriptor);
		else Reflect.deleteProperty(globalThis, key);
	}
	globals.clear();
});

async function render(tasks: ScheduledTask[] = []) {
	queryClient.setQueryData(["scheduled-tasks"], tasks);
	queryClient.setQueryData(["settings"], {
		agent: { defaultModel: "openai:test-model", defaultReasoningEffort: "max" },
		openaiModels: ["test-model", "deepseek-v4", "claude-sonnet-4-5", "card-model"].map((id) => ({
			id,
		})),
		codexAvailable: true,
	});
	queryClient.setQueryData(["narrators", "narrator-1"], {
		id: "narrator-1",
		model: "openai:test-model",
		status: "idle",
	});
	queryClient.setQueryData(["model-cards"], { cards: [] });
	const i18n = i18next.createInstance();
	await i18n.use(initReactI18next).init({
		lng: "en",
		resources: { en: { common, narrator, scheduledTasks } },
		interpolation: { escapeValue: false },
		react: { useSuspense: false },
	});
	const router = createRouter({ routeTree: createRootRoute(), history: createMemoryHistory() });
	const Page = Route.options.component as ComponentType;
	await act(async () =>
		root.render(
			<I18nextProvider i18n={i18n}>
				<QueryClientProvider client={queryClient}>
					<MantineProvider
						env="test"
						theme={{
							components: {
								Modal: Modal.extend({ defaultProps: { transitionProps: { duration: 0 } } }),
							},
						}}
					>
						<RouterContextProvider router={router}>
							<Page />
						</RouterContextProvider>
					</MantineProvider>
				</QueryClientProvider>
			</I18nextProvider>,
		),
	);
}

async function click(element: Element | null | undefined) {
	if (!element) throw new Error("Missing click target");
	await act(async () => {
		element.dispatchEvent(makeEvent("click"));
	});
}
function button(text: string) {
	return [...document.querySelectorAll("button")].find((el) => el.textContent === text);
}
function input(label: string) {
	const el = [...document.querySelectorAll("label")].find((el) =>
		el.textContent?.startsWith(label),
	);
	const target = document.getElementById(el?.getAttribute("for") ?? "");
	if (!target) throw new Error(`Missing input: ${label}`);
	return target as HTMLInputElement;
}
async function fill(label: string, value: string) {
	const target = input(label);
	// Linkedom does not implement the browser's input event/value tracker bridge.
	const key = Object.keys(target).find((key) => key.startsWith("__reactProps$"));
	if (!key) throw new Error("Missing React input handler");
	const props = (target as unknown as Record<string, { onChange: (event: unknown) => void }>)[key];
	await act(async () => props.onChange({ currentTarget: { value } }));
}
async function choose(label: string, text: string) {
	await click(input(label));
	await click(
		[...document.querySelectorAll('[role="option"]')].findLast((el) => el.textContent === text),
	);
}

test("creation defaults to null and saves every explicit tier, retaining effort on model changes", async () => {
	await render();
	await click(button("New Task"));
	expect(input("Reasoning effort").value).toBe("Follow default");
	await fill("Name", "New task");
	await fill("Prompt", "Do work");
	await click(button("Save"));
	expect(requests.at(-1)).toMatchObject({ method: "POST", body: { reasoningEffort: null } });
	for (const effort of GENERIC_REASONING_EFFORT_TIERS) {
		await click(button("New Task"));
		await fill("Name", "New task");
		await fill("Prompt", "Do work");
		await choose("Reasoning effort", narrator[`reasoning_${effort}`]);
		await choose("Model", "test-model");
		expect(input("Reasoning effort").value).toBe(narrator[`reasoning_${effort}`]);
		await click(button("Save"));
		expect(requests.at(-1)).toMatchObject({
			method: "POST",
			body: { reasoningEffort: effort, model: "openai:test-model" },
		});
	}
});

test("model switching clamps display without losing desired low, and unsupported models preserve it", async () => {
	await render([{ ...task, narratorMode: "new", reasoningEffort: "low" }]);
	await click(document.querySelector('[aria-label="Edit"]'));
	await choose("Model", "deepseek-v4");
	expect(input("Reasoning effort").value).toBe("High");
	await click(input("Reasoning effort"));
	expect(
		[...document.querySelectorAll('[role="option"]')].map((el) => el.textContent),
	).not.toContain("Low");
	await click(input("Reasoning effort"));
	await choose("Model", "claude-sonnet-4-5");
	expect(input("Reasoning effort").disabled).toBe(true);
	expect(input("Reasoning effort").value).toBe("Low");
	await choose("Model", "test-model");
	expect(input("Reasoning effort").value).toBe("Low");
	await click(button("Save"));
	expect(requests.at(-1)?.body.reasoningEffort).toBe("low");
});

test("model card tiers clamp low upward but never to none; explicit none remains available", async () => {
	await render([{ ...task, narratorMode: "new", reasoningEffort: "low" }]);
	await act(async () => {
		queryClient.setQueryData(["model-cards"], {
			cards: [{ modelKey: "card-model", effortLevels: ["medium", "high"] }],
		});
	});
	await click(document.querySelector('[aria-label="Edit"]'));
	await choose("Model", "card-model");
	expect(input("Reasoning effort").value).toBe("Medium");
	await choose("Reasoning effort", narrator.reasoning_none);
	await click(button("Save"));
	expect(requests.at(-1)?.body.reasoningEffort).toBe("none");
});

test("following default reacts to default model changes while preserving null", async () => {
	await render([{ ...task, narratorMode: "new", reasoningEffort: null }]);
	await click(document.querySelector('[aria-label="Edit"]'));
	await act(async () => {
		queryClient.setQueryData(["settings"], (old: Record<string, unknown>) => ({
			...old,
			agent: { defaultModel: "openai:deepseek-v4" },
		}));
	});
	expect(input("Reasoning effort").value).toBe("Follow default");
	await click(input("Reasoning effort"));
	const labels = [...document.querySelectorAll('[role="option"]')].map((el) => el.textContent);
	expect(labels).toContain("High");
	expect(labels).not.toContain("Low");
	await click(button("Save"));
	expect(requests.at(-1)?.body.reasoningEffort).toBeNull();
});

test("reuse uses session model instead of task model and missing references stay intact", async () => {
	await render([{ ...task, model: "deleted:model", reasoningEffort: "low" }]);
	await act(async () => {
		queryClient.setQueryData(["narrators", "narrator-1"], {
			model: "openai:test-model",
			effectiveModel: "openai:deepseek-v4",
			status: "idle",
		});
	});
	await click(document.querySelector('[aria-label="Edit"]'));
	expect(input("Reasoning effort").value).toBe("High");
	await choose("Narrator mode", scheduledTasks.modeNew);
	expect(input("Reasoning effort").disabled).toBe(true);
	await click(button("Save"));
	expect(requests.at(-1)?.body).toMatchObject({ model: "deleted:model", reasoningEffort: "low" });
});

test("catalog channel metadata selects Codex and Anthropic tiers, and cards use the bare model", async () => {
	await render([{ ...task, narratorMode: "new", reasoningEffort: "max" }]);
	await act(async () => {
		queryClient.setQueryData(["settings"], (old: Record<string, unknown>) => ({
			...old,
			nugProviders: [{ id: "gateway", prefix: "relay" }],
			nugModelsGrouped: [
				{
					providerId: "gateway",
					models: [
						{ id: "route:opaque", channel: "route", channelType: "codex", model: "unknown-codex" },
						{
							id: "route:claude",
							channel: "route",
							channelType: "anthropic",
							model: "claude-opus-4-7",
						},
						{ id: "route:card", channel: "route", channelType: "codex", model: "gpt-6-astra" },
					],
				},
			],
		}));
		queryClient.setQueryData(["model-cards"], {
			cards: [{ modelKey: "gpt-6-astra", effortLevels: ["medium", "high"] }],
		});
	});
	await click(document.querySelector('[aria-label="Edit"]'));
	await choose("Model", "route · unknown-codex");
	expect(input("Reasoning effort").value).toBe("High");
	await choose("Model", "route · claude-opus-4-7");
	expect(input("Reasoning effort").value).toBe("Max");
	await choose("Reasoning effort", narrator.reasoning_xhigh);
	await choose("Model", "route · gpt-6-astra");
	expect(input("Reasoning effort").value).toBe("High");
	await click(input("Reasoning effort"));
	const labels = [...document.querySelectorAll('[role="option"]')].map((el) => el.textContent);
	expect(labels).toContain("Medium");
	expect(labels).not.toContain(narrator.reasoning_none);
	expect(labels).not.toContain("Max");
	await click(button("Save"));
	expect(requests.at(-1)?.body.reasoningEffort).toBe("xhigh");
});

test("editing saves the selected override", async () => {
	await render([task]);
	await click(document.querySelector('[aria-label="Edit"]'));
	expect(input("Reasoning effort").value).toBe("High");
	await choose("Reasoning effort", "Max");
	await click(button("Save"));
	expect(requests.at(-1)).toMatchObject({ method: "PUT", body: { reasoningEffort: "max" } });
});

test("editing refills effort and explicitly clearing it sends null", async () => {
	await render([task]);
	await click(document.querySelector('[aria-label="Edit"]'));
	expect(input("Reasoning effort").value).toBe("High");
	await click(document.querySelector('[aria-label="Follow default"]'));
	expect(input("Reasoning effort").value).toBe("Follow default");
	await click(button("Save"));
	expect(requests.at(-1)).toMatchObject({ method: "PUT", body: { reasoningEffort: null } });
});

test("legacy tasks without the field edit and save as default, not none", async () => {
	const { reasoningEffort: _effort, ...legacy } = task;
	await render([legacy as ScheduledTask]);
	await click(document.querySelector('[aria-label="Edit"]'));
	expect(input("Reasoning effort").value).toBe("Follow default");
	await click(button("Save"));
	expect(requests.at(-1)).toMatchObject({ method: "PUT", body: { reasoningEffort: null } });
});
