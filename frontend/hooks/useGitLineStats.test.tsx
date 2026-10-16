import { afterEach, beforeEach, expect, mock, spyOn, test } from "bun:test";
import { parseHTML } from "linkedom";
import type { Root } from "react-dom/client";
import type { GitLineStats } from "../../shared/git-line-stats";
import type { GitTarget } from "../lib/api/git";

const { window } = parseHTML("<!doctype html><html><body></body></html>");
Object.assign(globalThis, {
	window,
	document: window.document,
	navigator: window.navigator,
	IS_REACT_ACT_ENVIRONMENT: false,
});
mock.module("./useNarrator", () => ({ useNarrator: () => ({ data: undefined }) }));
mock.module("../lib/narrator-ws-manager", () => ({
	narratorWSManager: {
		addListener: () => ({}),
		removeListener: () => {},
		onConnectionChange: () => () => {},
	},
}));
const { QueryClient, QueryClientProvider } = await import("@tanstack/react-query");
const { createRoot } = await import("react-dom/client");
const { api } = await import("../lib/api");
const {
	useGitLineStats,
	useGitStatus,
	invalidateWorkspaceQueries,
	invalidateGitStatusPushQueries,
} = await import("./useGit");

let root: Root;
let client: InstanceType<typeof QueryClient>;
let container: HTMLElement;
const target = { narratorId: "n1", workspaceKey: "workspace", canWrite: true };
const stats = (added: number): GitLineStats => ({
	files: [],
	linesAdded: added,
	linesRemoved: 2,
	complete: true,
});
function deferred<T>() {
	let resolve!: (value: T) => void;
	let reject!: (error: Error) => void;
	const promise = new Promise<T>((yes, no) => {
		resolve = yes;
		reject = no;
	});
	return { promise, resolve, reject };
}
function Probe({ target, name }: { target: GitTarget; name: string }) {
	const status = useGitStatus(target);
	const lines = useGitLineStats(target);
	return (
		<div data-probe={name} data-fetching={lines.isFetching} data-error={lines.error?.message}>
			{status.data?.branch}:{lines.isFetching || lines.error ? "unknown" : lines.data?.linesAdded}
		</div>
	);
}
async function flush() {
	for (let i = 0; i < 8; i++) await new Promise((resolve) => setTimeout(resolve, 0));
}
async function render(value = target) {
	root.render(
		<QueryClientProvider client={client}>
			<Probe target={value} name="bar" />
			<Probe target={{ ...value, narratorId: "n2" }} name="panel" />
		</QueryClientProvider>,
	);
	await flush();
}
beforeEach(() => {
	client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: Infinity } } });
	container = document.createElement("div");
	document.body.appendChild(container);
	root = createRoot(container);
	spyOn(api, "getGitStatus").mockResolvedValue({ branch: "main", files: [], totalFiles: 1 });
});
afterEach(async () => {
	root.unmount();
	client.clear();
	await flush();
	container.remove();
	mock.restore();
});

test("two mounted consumers share one cancellable stats query and never wait to show status", async () => {
	const pending = deferred<GitLineStats>();
	const get = spyOn(api, "getGitLineStats").mockReturnValue(pending.promise);
	await render();
	expect(get).toHaveBeenCalledTimes(1);
	expect(get.mock.calls[0]?.[1]).toBeInstanceOf(AbortSignal);
	expect(container.textContent).toBe("main:unknownmain:unknown");
	pending.resolve(stats(17));
	await flush();
	expect(container.textContent).toBe("main:17main:17");
});

test.each([
	503, 501,
])("stats failure %s keeps ready workspace and status without retry or probe", async (status) => {
	client.setQueryData(["gitWorkspace", "n1"], { state: "ready", workspaceKey: "workspace" });
	const reset = spyOn(client, "resetQueries");
	const get = spyOn(api, "getGitLineStats").mockRejectedValue(
		Object.assign(new Error("stats unavailable"), {
			status,
			data: status === 501 ? { code: "GIT_LINE_STATS_UNSUPPORTED" } : undefined,
		}),
	);
	await render();
	expect(get).toHaveBeenCalledTimes(1);
	expect(reset).not.toHaveBeenCalled();
	expect(client.getQueryData(["gitWorkspace", "n1"])).toEqual({
		state: "ready",
		workspaceKey: "workspace",
	});
	expect(container.textContent).toBe("main:unknownmain:unknown");
	expect(container.querySelector('[data-probe="bar"]')?.getAttribute("data-error")).toBe(
		"stats unavailable",
	);
});

test("push, manual refresh and writes invalidate the shared query and update both consumers", async () => {
	client.setQueryData(["gitWorkspace", "n1"], { state: "ready", workspaceKey: "workspace" });
	const get = spyOn(api, "getGitLineStats").mockResolvedValue(stats(1));
	await render();
	expect(container.textContent).toBe("main:1main:1");
	const pending = deferred<GitLineStats>();
	get.mockReturnValue(pending.promise);
	invalidateGitStatusPushQueries(client, "n1", null);
	await flush();
	expect(container.textContent).toBe("main:unknownmain:unknown");
	pending.resolve(stats(8));
	await flush();
	expect(container.textContent).toBe("main:8main:8");
	get.mockResolvedValue(stats(21));
	invalidateWorkspaceQueries(client, target);
	await flush();
	expect(container.textContent).toBe("main:21main:21");
	expect(get).toHaveBeenCalledTimes(3);
});

test("switching workspace does not display old totals and cancels the abandoned read", async () => {
	const old = deferred<GitLineStats>();
	const next = deferred<GitLineStats>();
	const get = spyOn(api, "getGitLineStats")
		.mockReturnValueOnce(old.promise)
		.mockReturnValue(next.promise);
	await render();
	const signal = get.mock.calls[0]?.[1];
	await render({ ...target, workspaceKey: "other" });
	expect(signal?.aborted).toBe(true);
	old.resolve(stats(999));
	await flush();
	expect(container.textContent).not.toContain("999");
	next.resolve(stats(4));
	await flush();
	expect(container.textContent).toBe("main:4main:4");
});
