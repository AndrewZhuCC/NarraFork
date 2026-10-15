import { afterEach, beforeEach, expect, mock, test } from "bun:test";
import type {
	TokenRepairJob,
	TokenRepairProtocolOverride,
	TokenRepairSummary,
} from "@shared/usage-token-repair";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { parseHTML } from "linkedom";
import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import locale from "../../locales/en/common.json";

let role = "admin";
let job: TokenRepairJob | null;
let calls: string[];
let drafts: TokenRepairProtocolOverride[][];
let versions: string[];
let emptyModels = false;
let rejectApply = false;
let detailReply: (() => Promise<{ job: TokenRepairJob | null }>) | undefined;
const totals = {
	inputTokens: "900719925474099312345",
	cachedInputTokens: "20",
	cacheCreationInputTokens: "30",
	cacheCreation5mTokens: "10",
	cacheCreation1hTokens: "20",
};
const summary: TokenRepairSummary = {
	previewVersion: "immutable-v1",
	requestCount: 300000,
	repairable: 100000,
	correct: 100000,
	unresolved: 50000,
	skipped: 49999,
	conflicts: 1,
	modelCount: 2,
	reasons: { missing_dump: 49999 },
	before: totals,
	after: { ...totals, inputTokens: "900719925474099312325" },
	delta: { ...totals, inputTokens: "-20" },
	repairBefore: totals,
	repairAfter: totals,
	appliedDelta: { ...totals, inputTokens: "-7" },
};
function makeJob(phase: TokenRepairJob["phase"] = "models"): TokenRepairJob {
	return {
		id: "job",
		status: "ready",
		workflowVersion: 2,
		phase,
		createdBy: "admin",
		createdAt: "2026-01-01",
		updatedAt: "2026-01-01",
		scanned: 300000,
		repaired: 0,
		conflicts: 1,
		upperCreatedAt: null,
		upperId: null,
		cursorCreatedAt: null,
		cursorId: null,
		includeReviewed: true,
		error: null,
		...(phase === "preview" ? { summary } : {}),
	};
}
mock.module("@frontend/hooks/useAuth", () => ({ useCurrentUser: () => ({ data: { role } }) }));
mock.module("@frontend/lib/usage-token-repair-api", () => ({
	usageTokenRepairApi: {
		probe: async () => {
			calls.push("probe");
			return { hasPending: true, job };
		},
		jobs: async () => {
			calls.push("jobs");
			return { job };
		},
		job: async () => (detailReply ? detailReply() : { job }),
		models: async (_id: string, cursor?: string) => {
			calls.push(`models:${cursor ?? "first"}`);
			return {
				models: emptyModels
					? []
					: [
							{
								provider: cursor ? "other" : null,
								model: "same",
								requestCount: 50000,
								protocol:
									job?.protocolOverrides?.find(
										(entry) =>
											entry.provider === (cursor ? "other" : null) && entry.model === "same",
									)?.protocol ?? null,
							},
						],
				nextCursor: cursor || emptyModels ? null : "next",
			};
		},
		start: async (includeReviewed: boolean) => {
			calls.push(`start:${includeReviewed}`);
			job = { ...makeJob("scanning"), status: "analyzing" };
			return { job };
		},
		cancel: async () => {
			calls.push("cancel");
			job = { ...(job ?? makeJob()), status: "cancelled" };
			return { job };
		},
		dryRun: async (_id: string, entries: TokenRepairProtocolOverride[]) => {
			drafts.push(entries);
			job = {
				...makeJob("dry-running"),
				status: "analyzing",
				protocolOverrides: entries,
			};
			return { job };
		},
		apply: async (_id: string, version: string) => {
			versions.push(version);
			if (rejectApply) throw new Error("conflict");
			job = { ...makeJob("applying"), status: "applying", summary };
			return { job };
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
			data: { value: string; label: string }[];
			onChange: (value: string) => void;
		}) => (
			<div>
				{label}
				{data.map((option) => (
					<button
						type="button"
						key={option.value}
						aria-label={`${label}:${option.value}`}
						disabled={disabled}
						data-selected={value === option.value}
						onClick={() => onChange(option.value)}
					>
						{option.label}
					</button>
				))}
			</div>
		),
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
	calls = [];
	drafts = [];
	versions = [];
	emptyModels = false;
	rejectApply = false;
	detailReply = undefined;
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
async function render(node: ReactNode = <UsageRepairPage />) {
	await act(async () => {
		root.render(<QueryClientProvider client={client}>{node}</QueryClientProvider>);
		await settle();
	});
	await act(settle);
	await act(settle);
}
function button(text: string) {
	const result = Array.from(container.querySelectorAll("button")).find(
		(element) => element.textContent === text || element.getAttribute("aria-label") === text,
	);
	if (!result) throw new Error(`Missing button: ${text}`);
	return result;
}
async function click(text: string) {
	const target = button(text);
	expect(target.disabled).toBe(false);
	await act(async () => {
		target.dispatchEvent(new Event("click", { bubbles: true }));
		await settle();
	});
	await act(settle);
}
async function publish(updated: TokenRepairJob) {
	job = updated;
	await act(async () => {
		client.setQueryData(["usage-token-repair", "job", updated.id], { job });
		await settle();
	});
	await act(settle);
}

test("non-admin sends no queries, even with cached administrator data", async () => {
	role = "user";
	client.setQueryData(["usage-token-repair", "jobs"], { job });
	await render(
		<>
			<TokenRepairNotice />
			<UsageRepairPage />
		</>,
	);
	expect(calls).toEqual([]);
	expect(container.textContent).toContain(locale.tokenRepair.adminOnly);
});
test("admin entry remains available and probe is cached", async () => {
	await render(<TokenRepairNotice />);
	await render(<TokenRepairNotice />);
	expect(calls).toEqual(["probe"]);
	expect(container.textContent).toContain(locale.tokenRepair.notice);
});
test("scan shows bounded progress only, not models or candidate requests", async () => {
	job = { ...makeJob("scanning"), status: "analyzing", rowsPerSecond: 1234 };
	await render();
	expect(calls).not.toContain("models:first");
	expect(container.textContent).toContain("300000");
	expect(container.textContent).toContain("1234 rows/second");
	expect(container.textContent).not.toContain(locale.tokenRepair.protocol.title);
	expect(container.querySelector('input[type="checkbox"]')).toBeNull();
	await publish(makeJob());
	expect(calls).toContain("models:first");
});
test("start always includes reviewed records and cancellation preserves a restart path", async () => {
	job = null;
	await render();
	await click(locale.tokenRepair.start);
	expect(calls).toContain("start:true");
	expect(calls).not.toContain("models:first");
	await click(locale.tokenRepair.finish);
	expect(button(locale.tokenRepair.start).disabled).toBe(false);
});
test("model paging preserves exact provider+model drafts and dry-run is asynchronous", async () => {
	await render();
	await click("No provider / same:openai");
	await click(locale.tokenRepair.next);
	await click("other / same:gemini");
	await click(locale.tokenRepair.previous);
	expect(button("No provider / same:openai").getAttribute("data-selected")).toBe("true");
	await click(locale.tokenRepair.dryRun);
	expect(drafts).toEqual([
		[
			{ provider: null, model: "same", protocol: "openai" },
			{ provider: "other", model: "same", protocol: "gemini" },
		],
	]);
	expect(container.textContent).toContain(locale.tokenRepair.phase["dry-running"]);
	expect(versions).toEqual([]);
	await publish({ ...makeJob("preview"), protocolOverrides: drafts[0] });
	expect(container.textContent).toContain(totals.inputTokens);
	expect(container.textContent).toContain("Overall before");
	expect(container.textContent).toContain("Repairable subset before");
	await click(locale.tokenRepair.confirm);
	expect(versions).toEqual(["immutable-v1"]);
	expect(container.textContent).toContain(locale.tokenRepair.phase.applying);
	await publish({ ...makeJob("done"), status: "completed", repaired: 99999, summary });
	expect(container.textContent).toContain("Actually applied 99999");
	expect(container.textContent).toContain("-7");
});
test("no association is required for dry-run; unresolved records can remain skipped", async () => {
	emptyModels = true;
	await render();
	await click(locale.tokenRepair.dryRun);
	expect(drafts).toEqual([[]]);
	await publish(makeJob("preview"));
	expect(container.textContent).toContain("Unresolved 50000");
	expect(container.textContent).toContain("missing_dump: 49999");
});
test("editing then reverting a rule immediately invalidates old immutable preview", async () => {
	job = makeJob("preview");
	await render();
	expect(button(locale.tokenRepair.confirm).disabled).toBe(false);
	await click("No provider / same:anthropic");
	await click("No provider / same:auto");
	expect(button(locale.tokenRepair.confirm).disabled).toBe(true);
	expect(container.textContent).not.toContain(locale.tokenRepair.previewTitle);
	expect(versions).toEqual([]);
	await click(locale.tokenRepair.dryRun);
	await publish(makeJob("preview"));
	expect(button(locale.tokenRepair.confirm).disabled).toBe(false);
});
test("clearing a persisted association displays automatic and submits no stale rule", async () => {
	job = {
		...makeJob("preview"),
		protocolOverrides: [{ provider: null, model: "same", protocol: "openai" }],
	};
	await render();
	expect(button("No provider / same:openai").getAttribute("data-selected")).toBe("true");
	await click("No provider / same:auto");
	expect(button("No provider / same:auto").getAttribute("data-selected")).toBe("true");
	expect(button(locale.tokenRepair.confirm).disabled).toBe(true);
	await click(locale.tokenRepair.dryRun);
	expect(drafts).toEqual([[]]);
});

async function refetchJob(updated: TokenRepairJob) {
	job = updated;
	await act(async () => {
		await client.refetchQueries({ queryKey: ["usage-token-repair", "job", updated.id] });
		await settle();
	});
	await act(settle);
}

test("same-job external dry-run refetch preserves unsent edits and blocks the new preview", async () => {
	job = makeJob("preview");
	await render();
	await click("No provider / same:anthropic");
	await refetchJob({
		...makeJob("preview"),
		protocolOverrides: [{ provider: null, model: "same", protocol: "gemini" }],
		summary: { ...summary, previewVersion: "other-tab-v2" },
	});
	expect(button("No provider / same:anthropic").getAttribute("data-selected")).toBe("true");
	expect(button(locale.tokenRepair.confirm).disabled).toBe(true);
	expect(container.textContent).not.toContain(locale.tokenRepair.previewTitle);
	expect(versions).toEqual([]);
	await click(locale.tokenRepair.dryRun);
	expect(drafts[0]).toEqual([{ provider: null, model: "same", protocol: "anthropic" }]);
	await refetchJob({
		...makeJob("preview"),
		protocolOverrides: drafts[0],
		summary: { ...summary, previewVersion: "local-v3" },
	});
	await click(locale.tokenRepair.confirm);
	expect(versions).toEqual(["local-v3"]);
});

test("same-job refetch synchronizes an unedited draft before confirming its new preview", async () => {
	job = makeJob("preview");
	await render();
	await refetchJob({
		...makeJob("preview"),
		protocolOverrides: [{ provider: null, model: "same", protocol: "gemini" }],
		summary: { ...summary, previewVersion: "other-tab-v2" },
	});
	expect(button("No provider / same:gemini").getAttribute("data-selected")).toBe("true");
	expect(container.textContent).toContain(locale.tokenRepair.previewTitle);
	await click(locale.tokenRepair.confirm);
	expect(versions).toEqual(["other-tab-v2"]);
});

test("late same-job detail replaces the unedited rules initially loaded from latest", async () => {
	job = makeJob("preview");
	let resolveDetail!: (reply: { job: TokenRepairJob }) => void;
	const pending = new Promise<{ job: TokenRepairJob }>((resolve) => {
		resolveDetail = resolve;
	});
	detailReply = () => pending;
	await render();
	expect(button("No provider / same:auto").getAttribute("data-selected")).toBe("true");
	await act(async () => {
		resolveDetail({
			job: {
				...makeJob("preview"),
				protocolOverrides: [{ provider: null, model: "same", protocol: "openai" }],
				summary: { ...summary, previewVersion: "detail-v2" },
			},
		});
		await settle();
	});
	await act(settle);
	expect(button("No provider / same:openai").getAttribute("data-selected")).toBe("true");
	expect(button(locale.tokenRepair.confirm).disabled).toBe(false);
});

test("external rules matching unsent edits do not revalidate a locally invalid preview", async () => {
	job = makeJob("preview");
	await render();
	await click("No provider / same:anthropic");
	await refetchJob({
		...makeJob("preview"),
		protocolOverrides: [{ provider: null, model: "same", protocol: "anthropic" }],
		summary: { ...summary, previewVersion: "other-tab-v2" },
	});
	expect(button(locale.tokenRepair.confirm).disabled).toBe(true);
	expect(container.textContent).not.toContain(locale.tokenRepair.previewTitle);
});

test("reordered rules preserve exact provider+model matching and preview validity", async () => {
	const rules: TokenRepairProtocolOverride[] = [
		{ provider: null, model: "same", protocol: "openai" },
		{ provider: "other", model: "same", protocol: "gemini" },
	];
	job = { ...makeJob("preview"), protocolOverrides: rules };
	await render();
	await refetchJob({ ...makeJob("preview"), protocolOverrides: [...rules].reverse() });
	expect(button("No provider / same:openai").getAttribute("data-selected")).toBe("true");
	await click(locale.tokenRepair.next);
	expect(button("other / same:gemini").getAttribute("data-selected")).toBe("true");
	expect(button(locale.tokenRepair.confirm).disabled).toBe(false);
});

test("excess association rules are not silently truncated or submitted", async () => {
	job = {
		...makeJob(),
		protocolOverrides: Array.from({ length: 33 }, (_, index) => ({
			provider: "provider",
			model: `model-${index}`,
			protocol: "openai" as const,
		})),
	};
	await render();
	expect(button(locale.tokenRepair.dryRun).disabled).toBe(true);
	expect(container.textContent).toContain(locale.tokenRepair.protocol.limit);
	expect(drafts).toEqual([]);
});

test("legacy ready jobs cannot fetch models or confirm their summary; they can end", async () => {
	job = { ...makeJob("preview"), workflowVersion: undefined };
	await render();
	expect(container.textContent).toContain(locale.tokenRepair.legacy);
	expect(calls).not.toContain("models:first");
	expect(container.textContent).not.toContain(locale.tokenRepair.previewTitle);
	expect(button(locale.tokenRepair.start).disabled).toBe(true);
	await click(locale.tokenRepair.finish);
	expect(button(locale.tokenRepair.start).disabled).toBe(false);
});
test("failed apply displays failure, never a successful result", async () => {
	job = makeJob("preview");
	rejectApply = true;
	await render();
	await click(locale.tokenRepair.confirm);
	expect(container.textContent).toContain(locale.tokenRepair.failed);
	expect(container.textContent).not.toContain("Actually applied");
});
test("interrupted partial application displays committed delta, not predicted delta", async () => {
	job = { ...makeJob("applying"), status: "interrupted", repaired: 2, summary };
	await render();
	expect(container.textContent).toContain("Actually applied 2");
	expect(container.textContent).toContain("-7");
	expect(container.textContent).not.toContain("-20");
	expect(container.textContent).toContain(locale.tokenRepair.unchanged);
});
