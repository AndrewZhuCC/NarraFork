import { describe, expect, test } from "bun:test";
import { nugAvailabilityPoller } from "../nug-availability-poller";
import { setNugCachedModels } from "../nug-model-cache";

describe("nugAvailabilityPoller", () => {
	test("fast path: resolves immediately when the model is already available in cache", async () => {
		const providerId = "prov-avail-1";
		setNugCachedModels(providerId, [
			{
				id: "kiro:claude-sonnet-4.5",
				channel: "kiro",
				model: "claude-sonnet-4.5",
				available: true,
			},
		]);
		const controller = new AbortController();
		const outcome = await nugAvailabilityPoller.waitForModelAvailable({
			providerId,
			nugModelId: "kiro:claude-sonnet-4.5",
			signal: controller.signal,
		});
		expect(outcome).toBe("available");
		expect(nugAvailabilityPoller.waiterCount()).toBe(0);
	});

	test("fast path: resolves 'aborted' immediately when the signal is already aborted", async () => {
		const providerId = "prov-avail-2";
		setNugCachedModels(providerId, [
			{
				id: "kiro:claude-sonnet-4.5",
				channel: "kiro",
				model: "claude-sonnet-4.5",
				available: false,
			},
		]);
		const controller = new AbortController();
		controller.abort();
		const outcome = await nugAvailabilityPoller.waitForModelAvailable({
			providerId,
			nugModelId: "kiro:claude-sonnet-4.5",
			signal: controller.signal,
		});
		expect(outcome).toBe("aborted");
		expect(nugAvailabilityPoller.waiterCount()).toBe(0);
	});

	test("registers a waiter for an unavailable model and resolves 'aborted' on signal abort", async () => {
		const providerId = "prov-avail-3";
		// No matching provider config in settings → poll can't recover it; we only
		// exercise the waiter lifecycle + abort here.
		setNugCachedModels(providerId, [
			{ id: "kiro:claude-opus-4.6", channel: "kiro", model: "claude-opus-4.6", available: false },
		]);
		const controller = new AbortController();
		const promise = nugAvailabilityPoller.waitForModelAvailable({
			providerId,
			nugModelId: "kiro:claude-opus-4.6",
			signal: controller.signal,
		});
		// The waiter is registered while it waits.
		expect(nugAvailabilityPoller.waiterCount()).toBe(1);
		controller.abort();
		const outcome = await promise;
		expect(outcome).toBe("aborted");
		expect(nugAvailabilityPoller.waiterCount()).toBe(0);
	});

	test("treats a model missing the `available` flag as available (legacy gateway)", async () => {
		const providerId = "prov-avail-4";
		setNugCachedModels(providerId, [
			// No `available` field at all.
			{ id: "openai:gpt-5.5", channel: "openai", model: "gpt-5.5" },
		]);
		const controller = new AbortController();
		const outcome = await nugAvailabilityPoller.waitForModelAvailable({
			providerId,
			nugModelId: "openai:gpt-5.5",
			signal: controller.signal,
		});
		expect(outcome).toBe("available");
	});
});
