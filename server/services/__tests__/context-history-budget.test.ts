import { describe, expect, spyOn, test } from "bun:test";
import * as runtimeHistory from "../agent-runtime/history";
import { narratorService } from "../narrator-service";
import { buildContextManagementHooks } from "../narrator-session";

function prepared(): runtimeHistory.PreparedRuntimeHistory {
	return {
		history: [],
		trailingToolResults: [],
		sourceMessages: [],
		modelMessages: [],
		replayedInjectionMessageIds: [],
		currentText: "",
		currentInputText: "",
		currentInputPersisted: false,
		endsWithToolResults: false,
		endsWithInput: false,
	};
}
function hooks(signal: AbortSignal) {
	return buildContextManagementHooks({
		narratorId: "budget-rebuild",
		locale: "en",
		getModel: () => "model",
		getProvider: () => "anthropic",
		attachmentSignal: signal,
		rebuildSystemPrompt: async () => "system",
	});
}

describe("compact/plan-toggle/forced context rebuild budget", () => {
	test("forwards active cancellation to the source loader and does not build after abort", async () => {
		const controller = new AbortController();
		let passedDeadline: number | undefined;
		const load = spyOn(narratorService, "getModelHistorySinceLastCompact").mockImplementation(
			async (_id, _limit, options) => {
				expect(options?.signal).toBe(controller.signal);
				passedDeadline = options?.deadline;
				return new Promise((_, reject) =>
					controller.signal.addEventListener("abort", () => reject(new Error("source cancelled")), {
						once: true,
					}),
				);
			},
		);
		const build = spyOn(runtimeHistory, "buildRuntimeHistory").mockImplementation(async () =>
			prepared(),
		);
		try {
			const rebuilding = hooks(controller.signal).onBeforeTurn(1, { force: true });
			controller.abort();
			await expect(rebuilding).rejects.toThrow("source cancelled");
			expect(passedDeadline).toBeGreaterThan(Date.now());
			expect(build).not.toHaveBeenCalled();
		} finally {
			load.mockRestore();
			build.mockRestore();
		}
	});
	test("source and provider projection share one deadline instead of receiving two 10s budgets", async () => {
		const controller = new AbortController();
		let now = 100_000;
		let sharedDeadline: number | undefined;
		const clock = spyOn(Date, "now").mockImplementation(() => now);
		const load = spyOn(narratorService, "getModelHistorySinceLastCompact").mockImplementation(
			async (_id, _limit, options) => {
				sharedDeadline = options?.deadline;
				now += 8000;
				return [];
			},
		);
		const build = spyOn(runtimeHistory, "buildRuntimeHistory").mockImplementation(
			async (options) => {
				expect(options.deadline).toBe(sharedDeadline);
				expect(options.attachmentSignal).toBe(controller.signal);
				expect((options.deadline ?? 0) - Date.now()).toBe(2000);
				now += 3000;
				return prepared();
			},
		);
		try {
			await expect(hooks(controller.signal).onBeforeTurn(1, { force: true })).rejects.toThrow(
				"10s budget",
			);
		} finally {
			load.mockRestore();
			build.mockRestore();
			clock.mockRestore();
		}
	});
});
