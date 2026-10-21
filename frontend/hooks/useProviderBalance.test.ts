import { afterEach, expect, mock, spyOn, test } from "bun:test";
import type { ProviderBalanceSnapshot } from "@shared/provider-balance";
import { MutationObserver, QueryClient, QueryObserver } from "@tanstack/react-query";
import { api } from "../lib/api";
import {
	providerBalanceKey,
	providerBalanceOptions,
	providerBalancePollInterval,
	providerBalanceProbeOptions,
} from "./useProviderBalance";

const balance = (
	generation: string,
	status: ProviderBalanceSnapshot["status"] = "supported",
): ProviderBalanceSnapshot => ({
	generation,
	status,
	adapter: "new-api",
	balance: { scope: "key", unlimited: false, amounts: [{ unit: "QUOTA", remaining: 0 }] },
	fetchedAt: 1,
	attemptedAt: 1,
	error: null,
});
afterEach(() => mock.restore());

for (const status of ["pending", "probing", "supported", "unsupported", "error"] as const) {
	test(`${status} controls polling without retrying unsupported/error`, () => {
		expect(providerBalancePollInterval(balance("A", status))).toBe(
			status === "supported"
				? 60_000
				: status === "pending" || status === "probing"
					? 2_000
					: false,
		);
	});
}

test("a late A response cannot appear after switching the same provider to B", async () => {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	let resolveA!: (value: Record<string, ProviderBalanceSnapshot>) => void;
	const pending = new Promise<Record<string, ProviderBalanceSnapshot>>((resolve) => {
		resolveA = resolve;
	});
	let calls = 0;
	spyOn(api, "providerGetBalances").mockImplementation(async () =>
		++calls === 1 ? pending : { p: balance("B") },
	);
	const observer = new QueryObserver(client, providerBalanceOptions(client, "p", "A"));
	const unsubscribe = observer.subscribe(() => {});
	try {
		observer.setOptions(providerBalanceOptions(client, "p", "B"));
		expect(observer.getCurrentResult().data).toBeUndefined();
		await observer.refetch();
		resolveA({ p: balance("A") });
		await pending;
		await Promise.resolve();
		expect(observer.getCurrentResult().data?.generation).toBe("B");
	} finally {
		unsubscribe();
		client.clear();
	}
});

test("mismatched server generation is hidden and reloads settings", async () => {
	const client = new QueryClient();
	client.setQueryData(["settings"], {});
	client.setQueryData(["admin", "settings"], {});
	spyOn(api, "providerGetBalances").mockResolvedValue({ p: balance("B") });
	const observer = new QueryObserver(client, providerBalanceOptions(client, "p", "A"));
	try {
		await observer.refetch();
		expect(observer.getCurrentResult().data).toBeUndefined();
		expect(client.getQueryState(["settings"])?.isInvalidated).toBe(true);
		expect(client.getQueryState(["admin", "settings"])?.isInvalidated).toBe(true);
	} finally {
		observer.destroy();
		client.clear();
	}
});

test("terminal states stay fresh across remount; a probe snapshot restarts observation", async () => {
	const client = new QueryClient();
	const fetch = spyOn(api, "providerGetBalances").mockResolvedValue({
		p: balance("A", "unsupported"),
	});
	const options = providerBalanceOptions(client, "p", "A");
	await client.fetchQuery(options);
	const observer = new QueryObserver(client, options);
	const unsubscribe = observer.subscribe(() => {});
	try {
		expect(observer.getCurrentResult().isStale).toBe(false);
		expect(fetch).toHaveBeenCalledTimes(1);
		expect(fetch.mock.calls[0]?.[0]?.providerId).toBe("p");
		const query = observer.getCurrentQuery();
		// The same query object is updated by POST -> setQueryData.
		expect(typeof options.refetchInterval).toBe("function");
		if (typeof options.refetchInterval !== "function")
			throw new Error("Expected interval callback");
		expect(options.refetchInterval(query)).toBe(false);
		client.setQueryData(options.queryKey, balance("A", "pending"));
		expect(options.refetchInterval(query)).toBe(2_000);
		client.setQueryData(options.queryKey, balance("A", "supported"));
		expect(options.refetchInterval(query)).toBe(60_000);
		client.setQueryData(options.queryKey, balance("B", "pending"));
		expect(options.refetchInterval(query)).toBe(false);
	} finally {
		unsubscribe();
		client.clear();
	}
});

test("a failed HTTP GET recovers at 60s; a server discovery error stays terminal", async () => {
	const client = new QueryClient();
	const fetch = spyOn(api, "providerGetBalances").mockResolvedValue({ p: balance("A") });
	const options = providerBalanceOptions(client, "p", "A");
	const observer = new QueryObserver(client, options);
	try {
		await observer.refetch();
		fetch.mockRejectedValueOnce(new Error("offline"));
		await observer.refetch();
		expect(observer.getCurrentResult().isError).toBe(true);
		if (typeof options.refetchInterval !== "function")
			throw new Error("Expected interval callback");
		expect(options.refetchInterval(observer.getCurrentQuery())).toBe(60_000);
		await observer.refetch();
		expect(observer.getCurrentResult().isSuccess).toBe(true);
		expect(observer.getCurrentResult().data?.status).toBe("supported");
		fetch.mockResolvedValue({ p: balance("A", "error") });
		await observer.refetch();
		expect(options.refetchInterval(observer.getCurrentQuery())).toBe(false);
	} finally {
		observer.destroy();
		client.clear();
	}
});

test("manual probe cancels same-generation GET even if transport ignores abort", async () => {
	const client = new QueryClient();
	client.setQueryData(providerBalanceKey("p", "A"), balance("A", "unsupported"));
	let resolveOld!: (value: Record<string, ProviderBalanceSnapshot>) => void;
	const oldResponse = new Promise<Record<string, ProviderBalanceSnapshot>>((resolve) => {
		resolveOld = resolve;
	});
	let oldSignal: AbortSignal | undefined;
	const fetch = spyOn(api, "providerGetBalances").mockImplementation(async (options) => {
		oldSignal = options?.signal;
		return oldResponse;
	});
	const probe = spyOn(api, "providerProbeBalance").mockResolvedValue(balance("A", "probing"));
	const observer = new QueryObserver(client, providerBalanceOptions(client, "p", "A"));
	const unsubscribe = observer.subscribe(() => {});
	const oldFetch = observer.refetch();
	const mutation = new MutationObserver(
		client,
		providerBalanceProbeOptions(client, "p", "A", true),
	);
	try {
		await Promise.all([mutation.mutate(), mutation.mutate()]);
		expect(probe).toHaveBeenCalledTimes(1);
		expect(oldSignal?.aborted).toBe(true);
		expect(observer.getCurrentResult().data?.status).toBe("probing");
		resolveOld({ p: balance("A", "unsupported") });
		await oldResponse;
		await oldFetch;
		await Promise.resolve();
		expect(observer.getCurrentResult().data?.status).toBe("probing");
		fetch.mockResolvedValue({ p: balance("A", "supported") });
		await observer.refetch();
		expect(observer.getCurrentResult().data?.status).toBe("supported");
	} finally {
		unsubscribe();
		client.clear();
	}
});

test("a GET started during pending POST cannot overwrite its newer probing result", async () => {
	const client = new QueryClient();
	client.setQueryData(providerBalanceKey("p", "A"), balance("A", "unsupported"));
	let resolvePost!: (snapshot: ProviderBalanceSnapshot) => void;
	let postStarted!: () => void;
	const started = new Promise<void>((resolve) => {
		postStarted = resolve;
	});
	const pendingPost = new Promise<ProviderBalanceSnapshot>((resolve) => {
		resolvePost = resolve;
	});
	spyOn(api, "providerProbeBalance").mockImplementation(() => {
		postStarted();
		return pendingPost;
	});
	let resolveGet!: (snapshots: Record<string, ProviderBalanceSnapshot>) => void;
	const pendingGet = new Promise<Record<string, ProviderBalanceSnapshot>>((resolve) => {
		resolveGet = resolve;
	});
	let getSignal: AbortSignal | undefined;
	spyOn(api, "providerGetBalances").mockImplementation(async (options) => {
		getSignal = options?.signal;
		return pendingGet;
	});
	const observer = new QueryObserver(client, providerBalanceOptions(client, "p", "A"));
	const unsubscribe = observer.subscribe(() => {});
	const mutation = new MutationObserver(
		client,
		providerBalanceProbeOptions(client, "p", "A", true),
	);
	try {
		const manualProbe = mutation.mutate();
		await started;
		// Simulate a poll/remount after pre-POST cancellation but before POST returns.
		const lateGet = observer.refetch();
		resolvePost(balance("A", "probing"));
		await manualProbe;
		expect(getSignal?.aborted).toBe(true);
		expect(observer.getCurrentResult().data?.status).toBe("probing");
		resolveGet({ p: balance("A", "error") });
		await pendingGet;
		await lateGet;
		await Promise.resolve();
		expect(observer.getCurrentResult().data?.status).toBe("probing");
	} finally {
		unsubscribe();
		client.clear();
	}
});

test("failed pre-probe cancellation releases the single-flight lock", async () => {
	const client = new QueryClient();
	const cancel = spyOn(client, "cancelQueries").mockRejectedValueOnce(new Error("cancel failed"));
	const probe = spyOn(api, "providerProbeBalance").mockResolvedValue(balance("A", "probing"));
	const mutation = new MutationObserver(
		client,
		providerBalanceProbeOptions(client, "p", "A", true),
	);
	try {
		await expect(mutation.mutate()).rejects.toThrow("cancel failed");
		expect(probe).not.toHaveBeenCalled();
		cancel.mockRestore();
		await mutation.mutate();
		expect(probe).toHaveBeenCalledTimes(1);
		expect(client.getQueryData<ProviderBalanceSnapshot>(providerBalanceKey("p", "A"))?.status).toBe(
			"probing",
		);
	} finally {
		client.clear();
	}
});

test("removing identity hides cached balances and disabling activity prevents fetch", () => {
	const client = new QueryClient();
	client.setQueryData(providerBalanceKey("p", "A"), balance("A"));
	const observer = new QueryObserver(client, providerBalanceOptions(client, "p", "A", false));
	expect(observer.getCurrentResult().data?.generation).toBe("A");
	observer.setOptions(providerBalanceOptions(client, "p", undefined, false));
	expect(observer.getCurrentResult().data).toBeUndefined();
	observer.setOptions(providerBalanceOptions(client, null, "A", false));
	expect(observer.getCurrentResult().data).toBeUndefined();
	expect(providerBalanceOptions(client, "p", "A", false).enabled).toBe(false);
	expect(providerBalanceOptions(client, "p", undefined).enabled).toBe(false);
	observer.destroy();
	client.clear();
});
