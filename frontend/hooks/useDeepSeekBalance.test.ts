import { afterEach, expect, mock, spyOn, test } from "bun:test";
import { QueryClient, QueryObserver } from "@tanstack/react-query";
import { api } from "../lib/api";
import type { DeepSeekBalanceCache } from "../lib/api/types";
import { deepSeekBalanceOptions } from "./useDeepSeekBalance";

const balance = (generation: string): DeepSeekBalanceCache => ({
	generation,
	isAvailable: true,
	balanceInfos: [
		{
			currency: "CNY",
			totalBalance: generation === "A" ? "100" : "2",
			grantedBalance: "0",
			toppedUpBalance: "0",
		},
	],
	fetchedAt: 1,
	error: null,
});
afterEach(() => mock.restore());

for (const honorCancellation of [true, false]) {
	test(`same provider A to B excludes a late A response (transport cancellation ${honorCancellation})`, async () => {
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		let resolveA!: (value: Record<string, DeepSeekBalanceCache>) => void;
		const pendingA = new Promise<Record<string, DeepSeekBalanceCache>>((resolve) => {
			resolveA = resolve;
		});
		let signalA: AbortSignal | undefined;
		let calls = 0;
		spyOn(api, "deepseekGetBalances").mockImplementation(async (options) => {
			if (++calls === 1) {
				if (honorCancellation) signalA = (options as { signal: AbortSignal }).signal;
				return pendingA;
			}
			return { sameId: balance("B") };
		});
		const observer = new QueryObserver(client, deepSeekBalanceOptions(client, "sameId", "A"));
		const unsubscribe = observer.subscribe(() => {});
		try {
			observer.setOptions(deepSeekBalanceOptions(client, "sameId", "B"));
			expect(observer.getCurrentResult().data).toBeUndefined();
			await observer.refetch();
			expect(observer.getCurrentResult().data?.generation).toBe("B");
			resolveA({ sameId: balance("A") });
			await pendingA;
			await Promise.resolve();
			expect(observer.getCurrentResult().data?.balanceInfos[0]?.totalBalance).toBe("2");
			if (honorCancellation) expect(signalA?.aborted).toBe(true);
		} finally {
			unsubscribe();
			client.clear();
		}
	});
}

test("another browser's credential change hides mismatched balance and invalidates both settings projections", async () => {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	client.setQueryData(["settings"], { deepseekBalanceGenerations: { sameId: "A" } });
	client.setQueryData(["admin", "settings"], {});
	spyOn(api, "deepseekGetBalances").mockResolvedValue({ sameId: balance("B") });
	const observer = new QueryObserver(client, deepSeekBalanceOptions(client, "sameId", "A"));
	const unsubscribe = observer.subscribe(() => {});
	try {
		await observer.refetch();
		expect(observer.getCurrentResult().data).toBeUndefined();
		expect(client.getQueryState(["settings"])?.isInvalidated).toBe(true);
		expect(client.getQueryState(["admin", "settings"])?.isInvalidated).toBe(true);
		observer.setOptions(deepSeekBalanceOptions(client, "sameId", "B"));
		await observer.refetch();
		expect(observer.getCurrentResult().data?.generation).toBe("B");
	} finally {
		unsubscribe();
		client.clear();
	}
});

test("switching generations drops an already cached A balance immediately", async () => {
	const client = new QueryClient();
	client.setQueryData(["deepseek", "balances", "sameId", "A"], balance("A"));
	const observer = new QueryObserver(client, deepSeekBalanceOptions(client, "sameId", "A", false));
	expect(observer.getCurrentResult().data?.generation).toBe("A");
	observer.setOptions(deepSeekBalanceOptions(client, "sameId", "B", false));
	expect(observer.getCurrentResult().data).toBeUndefined();
	client.clear();
});
