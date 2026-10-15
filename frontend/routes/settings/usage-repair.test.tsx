import { afterEach, beforeEach, expect, mock, test } from "bun:test";
import type {
	TokenRepairCandidate,
	TokenRepairJob,
	TokenRepairProtocolOverride,
} from "@shared/usage-token-repair";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { parseHTML } from "linkedom";
import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import locale from "../../locales/en/common.json";

let role = "admin";
let pending = true;
let probeCalls = 0;
let jobsCalls = 0;
let includeReviewed = false;
let job: TokenRepairJob | null;
let calls: string[];
let applications: { ids: string[] }[];
let starts: {
	includeReviewed: boolean;
	protocolOverrides: TokenRepairProtocolOverride[];
	sourceJobId?: string;
}[];
let fixtureRows: TokenRepairCandidate[] | null = null;
const counters = {
	inputTokens: 100,
	cachedInputTokens: 20,
	cacheCreationInputTokens: 0,
	cacheCreation5mTokens: 0,
	cacheCreation1hTokens: 0,
};
function record(
	id: string,
	classification: TokenRepairCandidate["classification"] = "repairable",
): TokenRepairCandidate {
	return {
		id,
		jobId: "job",
		requestId: `request-${id}`,
		createdAt: "2026-01-01",
		provider: "test",
		model: "model",
		classification,
		protocol: "openai",
		reason: "reason",
		evidence: "usage evidence",
		before: counters,
		after: { ...counters, inputTokens: 80 },
		appliedAt: null,
		appliedBy: null,
		totalsStatus: "unchanged",
	};
}
function makeJob(status: TokenRepairJob["status"] = "ready"): TokenRepairJob {
	return {
		id: "job",
		status,
		createdBy: "admin",
		createdAt: "2026-01-01",
		updatedAt: "2026-01-01",
		scanned: 3,
		repaired: 0,
		conflicts: 1,
		upperCreatedAt: null,
		upperId: null,
		cursorCreatedAt: null,
		cursorId: null,
		includeReviewed: false,
		error: null,
	};
}
mock.module("@frontend/hooks/useAuth", () => ({ useCurrentUser: () => ({ data: { role } }) }));
mock.module("@frontend/lib/usage-token-repair-api", () => ({
	usageTokenRepairApi: {
		probe: async () => {
			probeCalls++;
			return { hasPending: pending, job };
		},
		jobs: async () => {
			jobsCalls++;
			return { job };
		},
		job: async () => ({ job }),
		candidates: async (_id: string, cursor?: string) => {
			calls.push(`page:${cursor ?? "first"}`);
			return {
				records: cursor
					? [record("second")]
					: fixtureRows
						? fixtureRows
						: [
								record("one"),
								record("bad", "insufficient"),
								{ ...record("applied"), appliedAt: "2026-01-02" },
							],
				nextCursor: cursor ? null : "next",
			};
		},
		start: async (
			value: boolean,
			overrides?: TokenRepairProtocolOverride[],
			sourceJobId?: string,
		) => {
			includeReviewed = value;
			starts.push({
				includeReviewed: value,
				protocolOverrides: overrides ?? [],
				...(sourceJobId !== undefined ? { sourceJobId } : {}),
			});
			calls.push("start");
			job = {
				...makeJob("analyzing"),
				...(overrides ? { id: "new-job", protocolOverrides: overrides } : {}),
				...(sourceJobId !== undefined ? { sourceJobId } : {}),
			};
			return { job };
		},
		cancel: async () => {
			calls.push("cancel");
			job = { ...(job ?? makeJob()), status: "cancelled" };
			return { job };
		},
		apply: async (_id: string, ids: string[]) => {
			applications.push({ ids });
			return { applied: 1, conflicts: 2 };
		},
	},
}));
mock.module("@tanstack/react-router", () => ({
	createFileRoute: () => (options: unknown) => ({ options }),
	Link: ({ children }: { children: ReactNode }) => <span>{children}</span>,
}));
mock.module("react-i18next", () => ({
	useTranslation: () => ({
		t: (key: string, values?: Record<string, unknown>) => {
			let value: unknown = locale;
			for (const part of key.split(".")) value = (value as Record<string, unknown>)?.[part];
			return String(value ?? key).replace(/\{\{(\w+)\}\}/g, (_, name: string) =>
				String(values?.[name] ?? ""),
			);
		},
	}),
}));
mock.module("@mantine/core", () => {
	const Block = ({ children }: { children?: ReactNode }) => <div>{children}</div>;
	return {
		Alert: Block,
		Badge: Block,
		Group: Block,
		Paper: Block,
		Stack: Block,
		Text: Block,
		Title: Block,
		Select: ({
			label,
			value,
			disabled,
			data,
			onChange,
		}: {
			label: string;
			value: string;
			disabled?: boolean;
			data: { value: string; label: string; disabled?: boolean }[];
			onChange: (value: string) => void;
		}) => (
			<div>
				{label}
				{data.map((option) => (
					<button
						type="button"
						key={option.value}
						aria-label={`${label}:${option.value}`}
						disabled={disabled || option.disabled}
						data-selected={value === option.value}
						onClick={() => onChange(option.value)}
					>
						{option.label}
					</button>
				))}
			</div>
		),
		Button: ({
			children,
			disabled,
			onClick,
		}: {
			children: ReactNode;
			disabled?: boolean;
			onClick?: () => void;
		}) => (
			<button type="button" disabled={disabled} onClick={onClick}>
				{children}
			</button>
		),
		Checkbox: ({
			label,
			checked,
			disabled,
			onChange,
			"aria-label": ariaLabel,
		}: {
			label?: string;
			checked: boolean;
			disabled?: boolean;
			onChange: (event: { currentTarget: { checked: boolean } }) => void;
			"aria-label"?: string;
		}) => (
			<button
				type="button"
				aria-label={ariaLabel ?? label}
				data-checked={checked}
				disabled={disabled}
				onClick={() => onChange({ currentTarget: { checked: !checked } })}
			>
				{label}
			</button>
		),
		Modal: ({
			opened,
			children,
			title,
		}: {
			opened: boolean;
			children: ReactNode;
			title: string;
		}) =>
			opened ? (
				<div role="dialog">
					{title}
					{children}
				</div>
			) : null,
	};
});
const { UsageRepairPage } = await import("./usage-repair");
const { TokenRepairNotice } = await import("../../components/usage-history/TokenRepairNotice");
let root: Root;
let container: HTMLElement;
let client: QueryClient;
const globals = new Map<string, PropertyDescriptor | undefined>();
beforeEach(() => {
	const { window } = parseHTML("<html><body></body></html>");
	for (const [key, value] of Object.entries({
		window,
		document: window.document,
		navigator: window.navigator,
		HTMLElement: window.HTMLElement,
		Element: window.Element,
		Node: window.Node,
		Event: window.Event,
		IS_REACT_ACT_ENVIRONMENT: true,
	})) {
		globals.set(key, Object.getOwnPropertyDescriptor(globalThis, key));
		Object.defineProperty(globalThis, key, { value, configurable: true, writable: true });
	}
	role = "admin";
	pending = true;
	probeCalls = 0;
	jobsCalls = 0;
	calls = [];
	applications = [];
	starts = [];
	fixtureRows = null;
	includeReviewed = false;
	job = makeJob();
	client = new QueryClient({
		defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
	});
	container = document.createElement("div");
	document.body.appendChild(container);
	root = createRoot(container);
});
afterEach(async () => {
	await act(async () => root.unmount());
	client.clear();
	container.remove();
	for (const [key, descriptor] of globals) {
		if (descriptor) Object.defineProperty(globalThis, key, descriptor);
		else Reflect.deleteProperty(globalThis, key);
	}
	globals.clear();
});
async function settle() {
	await new Promise((resolve) => setTimeout(resolve, 20));
}
async function render(node: ReactNode) {
	await act(async () => {
		root.render(<QueryClientProvider client={client}>{node}</QueryClientProvider>);
		await settle();
	});
	await act(settle);
	await act(settle);
}
async function click(text: string) {
	const button = Array.from(container.querySelectorAll("button")).find(
		(element) =>
			element.textContent?.startsWith(text) || element.getAttribute("aria-label") === text,
	);
	if (!button) throw new Error(`Missing button: ${text}`);
	expect(button.disabled).toBe(false);
	await act(async () => {
		button.dispatchEvent(new Event("click", { bubbles: true }));
		await settle();
	});
	await act(settle);
}

test("non-admin sends no probe or jobs requests and sees no cached data", async () => {
	role = "user";
	client.setQueryData(["usage-token-repair", "probe"], { hasPending: true, job });
	client.setQueryData(["usage-token-repair", "jobs"], { job });
	await render(
		<>
			<TokenRepairNotice />
			<UsageRepairPage />
		</>,
	);
	expect(probeCalls).toBe(0);
	expect(jobsCalls).toBe(0);
	expect(container.textContent).toContain(locale.tokenRepair.adminOnly);
	expect(container.textContent).not.toContain("request-one");
	expect(container.textContent).not.toContain(locale.tokenRepair.open);
});
test("administrator entry stays visible and uses cached probe results", async () => {
	await render(<TokenRepairNotice />);
	expect(container.textContent).toContain(locale.tokenRepair.notice);
	await render(<TokenRepairNotice />);
	expect(probeCalls).toBe(1);
});
test("entry remains available without pending records, with or without a prior job", async () => {
	pending = false;
	await render(<TokenRepairNotice />);
	expect(container.textContent).toContain(locale.tokenRepair.continue);
	job = null;
	client.removeQueries({ queryKey: ["usage-token-repair", "probe"] });
	await render(<TokenRepairNotice />);
	expect(container.textContent).toContain(locale.tokenRepair.open);
});
test("preview requires second confirmation and clearly limits corrections to request details", async () => {
	await render(<UsageRepairPage />);
	await click("request-one");
	await click("Preview selected");
	expect(applications).toEqual([]);
	expect(container.querySelector('[role="dialog"]')?.textContent).toContain("Ordinary input: -20");
	await click(locale.tokenRepair.confirmApply);
	expect(applications).toEqual([{ ids: ["one"] }]);
	expect(container.textContent).toContain("Applied 1 · Conflicts 2");
	expect(container.textContent).toContain(
		"Historical costs, lifetime totals, and shared messages remain uncorrected.",
	);
});
test("unrepairable and already applied candidates cannot be selected", async () => {
	await render(<UsageRepairPage />);
	for (const id of ["bad", "applied"])
		expect(
			(container.querySelector(`[aria-label="request-${id}"]`) as HTMLButtonElement).disabled,
		).toBe(true);
	expect(
		Array.from(container.querySelectorAll("button")).find((button) =>
			button.textContent?.startsWith("Preview selected"),
		)?.disabled,
	).toBe(true);
});
test("pagination clears selection and supports previous page", async () => {
	await render(<UsageRepairPage />);
	await click("request-one");
	await click(locale.tokenRepair.next);
	expect(calls).toContain("page:next");
	expect(container.textContent).toContain("request-second");
	expect(container.textContent).toContain("Preview selected (0)");
	await click(locale.tokenRepair.previous);
	expect(container.textContent).toContain("request-one");
});
test("manual start includes reviewed option and ending the analysis is explicit", async () => {
	job = null;
	await render(<UsageRepairPage />);
	expect(calls).not.toContain("start");
	await click(locale.tokenRepair.includeReviewed);
	await click(locale.tokenRepair.start);
	expect(includeReviewed).toBe(true);
	await click(locale.tokenRepair.finish);
	expect(calls).toContain("cancel");
});
test.each([
	"interrupted",
	"failed",
] as const)("%s jobs must end before starting a new analysis", async (status) => {
	job = makeJob(status);
	await render(<UsageRepairPage />);
	const startButton = () =>
		Array.from(container.querySelectorAll("button")).find(
			(button) => button.textContent === locale.tokenRepair.start,
		);
	expect(startButton()?.disabled).toBe(true);
	await click(locale.tokenRepair.finish);
	expect(startButton()?.disabled).toBe(false);
	await click(locale.tokenRepair.start);
	expect(calls).toContain("start");
});

test("ready review can finish without applying selections and then start another batch", async () => {
	await render(<UsageRepairPage />);
	const startButton = () =>
		Array.from(container.querySelectorAll("button")).find(
			(button) => button.textContent === locale.tokenRepair.start,
		);
	expect(startButton()?.disabled).toBe(true);
	expect(container.textContent).toContain(locale.tokenRepair.finishHelp);
	const candidateQuery = client
		.getQueryCache()
		.find({ queryKey: ["usage-token-repair", "candidates", "job", undefined] });
	expect(
		(candidateQuery?.options as { refetchInterval?: number | false } | undefined)?.refetchInterval,
	).toBe(false);
	await click("request-one");
	await click(locale.tokenRepair.finish);
	expect(calls).toContain("cancel");
	expect(applications).toEqual([]);
	expect(startButton()?.disabled).toBe(false);
	await click(locale.tokenRepair.start);
	expect(calls).toContain("start");
	expect(container.textContent).toContain(locale.tokenRepair.status.analyzing);
});

test("manual choice ends ready job before reanalysis, isolates providers and invalidates old preview", async () => {
	fixtureRows = [
		record("one"),
		{ ...record("uncertain", "ambiguous"), protocol: "unknown", provider: "alpha", model: "same" },
		{ ...record("other", "ambiguous"), protocol: "unknown", provider: "beta", model: "same" },
		{ ...record("missing", "ambiguous"), protocol: "unknown", model: null },
	];
	await render(<UsageRepairPage />);
	await click("request-one");
	await click("Preview selected");
	expect(container.querySelector('[role="dialog"]')).not.toBeNull();
	await click("alpha / same:openai");
	expect(container.querySelector('[role="dialog"]')).toBeNull();
	expect(container.textContent).toContain(locale.tokenRepair.protocol.draftWarning);
	expect(
		(container.querySelector('[aria-label="request-one"]') as HTMLButtonElement).disabled,
	).toBe(true);
	expect(
		container.querySelector('[aria-label="beta / same:auto"]')?.getAttribute("data-selected"),
	).toBe("true");
	expect(container.querySelector('[aria-label="test / null:openai"]')).toBeNull();
	await click(locale.tokenRepair.protocol.reanalyze);
	expect(calls.indexOf("cancel")).toBeLessThan(calls.indexOf("start"));
	expect(starts).toEqual([
		{
			includeReviewed: true,
			protocolOverrides: [{ provider: "alpha", model: "same", protocol: "openai" }],
			sourceJobId: "job",
		},
	]);
	expect(job?.id).toBe("new-job");
	expect(container.textContent).toContain("Reanalyzing only candidates from source batch job");
	expect(applications).toEqual([]);
	expect(container.textContent).not.toContain(locale.tokenRepair.protocol.draftWarning);
});

test("automatic analysis stays compatible and frozen manual choices can be explicitly cleared", async () => {
	job = null;
	await render(<UsageRepairPage />);
	await click(locale.tokenRepair.start);
	expect(starts).toEqual([{ includeReviewed: false, protocolOverrides: [] }]);
});

test("job choices initialize drafts, preserve audit provenance, and automatic reanalysis submits no rule", async () => {
	job = {
		...makeJob(),
		protocolOverrides: [{ provider: null, model: "same", protocol: "gemini" }],
	};
	fixtureRows = [
		{ ...record("manual", "ambiguous"), provider: null, model: "same", manualProtocol: "gemini" },
	];
	await render(<UsageRepairPage />);
	expect(container.textContent).toContain("Audit source: administrator-selected Gemini");
	expect(container.textContent).not.toContain(locale.tokenRepair.protocol.draftWarning);
	await click("No provider / same:auto");
	await click(locale.tokenRepair.protocol.reanalyze);
	expect(starts).toEqual([{ includeReviewed: true, protocolOverrides: [], sourceJobId: "job" }]);
});

test("a new batch preserves unchanged frozen choices rather than silently switching to automatic", async () => {
	const overrides: TokenRepairProtocolOverride[] = [
		{ provider: "p", model: "same", protocol: "anthropic" },
	];
	job = { ...makeJob("cancelled"), protocolOverrides: overrides };
	await render(<UsageRepairPage />);
	await click(locale.tokenRepair.start);
	expect(starts).toEqual([{ includeReviewed: false, protocolOverrides: overrides }]);
});

test("active analysis freezes selectors and requires stopping first", async () => {
	job = makeJob("analyzing");
	fixtureRows = [{ ...record("uncertain", "ambiguous"), protocol: "unknown" }];
	await render(<UsageRepairPage />);
	expect(container.textContent).toContain(locale.tokenRepair.protocol.stopFirst);
	expect(
		(container.querySelector('[aria-label="test / model:openai"]') as HTMLButtonElement).disabled,
	).toBe(true);
	await click(locale.tokenRepair.finish);
	expect(
		(container.querySelector('[aria-label="test / model:openai"]') as HTMLButtonElement).disabled,
	).toBe(false);
});

test("draft choices survive pagination and enforce the 32-rule limit", async () => {
	job = {
		...makeJob(),
		protocolOverrides: Array.from({ length: 32 }, (_, index) => ({
			provider: "p",
			model: `model-${index}`,
			protocol: "openai" as const,
		})),
	};
	fixtureRows = [
		{ ...record("uncertain", "ambiguous"), protocol: "unknown", provider: "p", model: "extra" },
	];
	await render(<UsageRepairPage />);
	expect(
		(container.querySelector('[aria-label="p / extra:gemini"]') as HTMLButtonElement).disabled,
	).toBe(true);
	await click("p / model-0:auto");
	await click("p / extra:gemini");
	await click(locale.tokenRepair.next);
	await click(locale.tokenRepair.previous);
	expect(
		container.querySelector('[aria-label="p / extra:gemini"]')?.getAttribute("data-selected"),
	).toBe("true");
	await click(locale.tokenRepair.protocol.reanalyze);
	expect(starts[0]?.protocolOverrides).toHaveLength(32);
	expect(starts[0]?.protocolOverrides).toContainEqual({
		provider: "p",
		model: "extra",
		protocol: "gemini",
	});
});
