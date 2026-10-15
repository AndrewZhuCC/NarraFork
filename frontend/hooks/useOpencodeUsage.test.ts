import { afterEach, expect, mock, spyOn, test } from "bun:test";
import { QueryClient, QueryObserver } from "@tanstack/react-query";
import { api } from "../lib/api";
import type { OpencodeUsageCache } from "../lib/api/types";
import { opencodeUsageOptions } from "./useOpencodeUsage";

const usage = (generation: string): OpencodeUsageCache => ({
	rolling: { status: "ok", percent: generation === "A" ? 10 : 80, resetsAt: null },
	weekly: null,
	monthly: null,
	generation,
	fetchedAt: 1,
	error: null,
});
afterEach(() => mock.restore());

for (const honorCancellation of [true, false]) {
	test(`same provider A to B excludes a late A response (transport cancellation ${honorCancellation})`, async () => {
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		let resolveA!: (value: Record<string, OpencodeUsageCache>) => void;
		const pendingA = new Promise<Record<string, OpencodeUsageCache>>((resolve) => {
			resolveA = resolve;
		});
		let signalA: AbortSignal | undefined;
		let calls = 0;
		spyOn(api, "opencodeGetUsages").mockImplementation(async (options) => {
			if (++calls === 1) {
				if (honorCancellation) signalA = (options as { signal: AbortSignal }).signal;
				return pendingA;
			}
			return { sameId: usage("B") };
		});
		const observer = new QueryObserver(client, opencodeUsageOptions(client, "sameId", "A"));
		const unsubscribe = observer.subscribe(() => {});
		try {
			observer.setOptions(opencodeUsageOptions(client, "sameId", "B"));
			expect(observer.getCurrentResult().data).toBeUndefined();
			await observer.refetch();
			expect(observer.getCurrentResult().data?.generation).toBe("B");
			resolveA({ sameId: usage("A") });
			await pendingA;
			await Promise.resolve();
			// The account switched mid-flight: A's allowance must not land on B.
			expect(observer.getCurrentResult().data?.rolling?.percent).toBe(80);
			if (honorCancellation) expect(signalA?.aborted).toBe(true);
		} finally {
			unsubscribe();
			client.clear();
		}
	});
}

test("another browser's credential change hides the mismatched allowance and invalidates both settings projections", async () => {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	client.setQueryData(["settings"], { opencodeUsageGenerations: { sameId: "A" } });
	client.setQueryData(["admin", "settings"], {});
	spyOn(api, "opencodeGetUsages").mockResolvedValue({ sameId: usage("B") });
	const observer = new QueryObserver(client, opencodeUsageOptions(client, "sameId", "A"));
	const unsubscribe = observer.subscribe(() => {});
	try {
		await observer.refetch();
		expect(observer.getCurrentResult().data).toBeUndefined();
		expect(client.getQueryState(["settings"])?.isInvalidated).toBe(true);
		expect(client.getQueryState(["admin", "settings"])?.isInvalidated).toBe(true);
		observer.setOptions(opencodeUsageOptions(client, "sameId", "B"));
		await observer.refetch();
		expect(observer.getCurrentResult().data?.generation).toBe("B");
	} finally {
		unsubscribe();
		client.clear();
	}
});

test("switching generations drops an already cached A allowance immediately", async () => {
	const client = new QueryClient();
	client.setQueryData(["opencode", "usages", "sameId", "A"], usage("A"));
	const observer = new QueryObserver(client, opencodeUsageOptions(client, "sameId", "A", false));
	expect(observer.getCurrentResult().data?.generation).toBe("A");
	observer.setOptions(opencodeUsageOptions(client, "sameId", "B", false));
	expect(observer.getCurrentResult().data).toBeUndefined();
	client.clear();
});

test("stays disabled without a provider, a generation, or an active panel", async () => {
	const client = new QueryClient();
	const spy = spyOn(api, "opencodeGetUsages").mockResolvedValue({});
	for (const options of [
		opencodeUsageOptions(client, null, "A"),
		opencodeUsageOptions(client, "sameId", undefined),
		opencodeUsageOptions(client, "sameId", "A", false),
	]) {
		const observer = new QueryObserver(client, options);
		const unsubscribe = observer.subscribe(() => {});
		expect(observer.getCurrentResult().fetchStatus).toBe("idle");
		unsubscribe();
	}
	expect(spy).not.toHaveBeenCalled();
	client.clear();
});
