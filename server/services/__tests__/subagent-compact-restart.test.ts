import { describe, expect, test } from "bun:test";
import { createRuntimeRecoveryState, selectRuntimeRecovery } from "../agent-runtime/transition";
import { shouldResumeAfterCompact } from "../turn-continuation-decisions";

/** These pin the live common runtime, not the retired delegated compact planner. */
describe("shared compact restart", () => {
	test("finishes when the pass already ended naturally", () => {
		expect(
			shouldResumeAfterCompact({ completedNaturally: true, hasError: false }, true, false),
		).toBe(false);
	});
	test("restarts when the pass ended mid-work", () => {
		expect(
			shouldResumeAfterCompact({ completedNaturally: false, hasError: false }, true, false),
		).toBe(true);
	});
	test("finishes when onBeforeTurn already consumed the compact", () => {
		expect(
			shouldResumeAfterCompact({ completedNaturally: false, hasError: false }, false, false),
		).toBe(false);
	});
	test("restarts when a newer compact landed after onBeforeTurn consumed one", () => {
		expect(
			shouldResumeAfterCompact({ completedNaturally: false, hasError: false }, true, false),
		).toBe(true);
	});
	test("restarts on context overflow even when the pass reported natural completion", () => {
		const recovery = selectRuntimeRecovery(createRuntimeRecoveryState(), {
			result: {
				finalText: "",
				hasError: false,
				shouldUpdateTitle: false,
				completedNaturally: true,
				contextLengthExceeded: true,
			},
			aborted: false,
			planApproved: false,
			stateful: true,
			maxTransientRetries: 3,
		});
		expect(recovery.kind).toBe("overflow");
	});
	test("treats a missing completedNaturally flag as mid-work", () => {
		expect(shouldResumeAfterCompact({ hasError: false }, true, false)).toBe(true);
	});
});
