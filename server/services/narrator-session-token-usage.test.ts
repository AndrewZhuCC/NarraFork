import { describe, expect, test } from "bun:test";
import type { TokenUsageSnapshot } from "./narrator-event-handler";
import { accountRequestTokenUsage, accountTokenUsageForTurn } from "./narrator-session";
import type { ActiveNarrator } from "./narrator-session-state";

function activeWith(usage?: TokenUsageSnapshot): ActiveNarrator {
	const active = { _lastTokenUsage: usage } as ActiveNarrator;
	accountRequestTokenUsage(active, usage);
	return active;
}

describe("normalized round token accounting", () => {
	test("ordinary input is already uncached, even when smaller than cache reads", () => {
		const active = activeWith({ inputTokens: 200, cachedInputTokens: 800, completionTokens: 100 });
		expect(accountTokenUsageForTurn(active)).toBe(300);
		expect(accountTokenUsageForTurn(active)).toBe(0);
	});

	test("full cache hits retain zero ordinary input", () => {
		expect(
			accountTokenUsageForTurn(
				activeWith({
					promptTokens: 1000,
					inputTokens: 0,
					cachedInputTokens: 1000,
					completionTokens: 100,
				}),
			),
		).toBe(100);
	});

	test("legacy prompt-only snapshots subtract cache reads and writes once", () => {
		expect(
			accountTokenUsageForTurn(
				activeWith({
					promptTokens: 1000,
					cachedInputTokens: 700,
					cacheCreationInputTokens: 100,
					completionTokens: 100,
				}),
			),
		).toBe(300);
	});

	test("cache-write TTL details are not added twice", () => {
		expect(
			accountTokenUsageForTurn(
				activeWith({
					promptTokens: 1000,
					cachedInputTokens: 700,
					cacheCreationInputTokens: 100,
					cacheCreation5mTokens: 60,
					cacheCreation1hTokens: 40,
					completionTokens: 100,
				}),
			),
		).toBe(300);
	});

	test("multiple requests in one pass and smaller snapshots in later passes all count", () => {
		const active = activeWith({ inputTokens: 1000, completionTokens: 100 });
		accountRequestTokenUsage(active, { inputTokens: 1100, completionTokens: 100 });
		expect(accountTokenUsageForTurn(active)).toBe(2300);
		expect(accountTokenUsageForTurn(active)).toBe(0);
		accountRequestTokenUsage(active, { inputTokens: 100, completionTokens: 20 });
		expect(accountTokenUsageForTurn(active)).toBe(120);
		expect(active._tokenUsageBaseline).toBe(2420);
	});

	test("context snapshots are not charges and zero/missing usage cannot reuse old usage", () => {
		const active = activeWith({ inputTokens: 1000, completionTokens: 100 });
		expect(accountTokenUsageForTurn(active)).toBe(1100);
		accountRequestTokenUsage(active);
		accountRequestTokenUsage(active, { inputTokens: 0, completionTokens: 0 });
		expect(accountTokenUsageForTurn(active)).toBe(0);
		expect(active._lastTokenUsage).toEqual({ inputTokens: 1000, completionTokens: 100 });
	});

	test("reasoning is not added again and parent/child counters are independent", () => {
		const parent = activeWith({ inputTokens: 100, completionTokens: 20, reasoningTokens: 10 });
		const child = activeWith({ inputTokens: 500, completionTokens: 50 });
		expect(accountTokenUsageForTurn(parent)).toBe(120);
		expect(accountTokenUsageForTurn(child)).toBe(550);
	});

	test("missing snapshots contribute no tokens", () => {
		expect(accountTokenUsageForTurn(activeWith())).toBe(0);
	});
});
