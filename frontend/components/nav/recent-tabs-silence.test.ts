/**
 * `isBackgroundWorkSilencedStatus` — the RecentTabs sound/PWA gate for the
 * done-transition that fires while background work whose completion wakes the
 * narrator (bash tasks / subagents — never transfers or services) is still
 * running.
 *
 * Only that exact combination stays silent: permission waits (`waiting`) must
 * always notify, a missing status cannot be confirmed as a done-transition, and
 * an unflagged done (the wake-up turn after the last background task completes)
 * notifies exactly as before.
 */

import { describe, expect, it } from "bun:test";
import { isBackgroundWorkSilencedStatus } from "./recent-tabs-logic";

describe("isBackgroundWorkSilencedStatus", () => {
	it("silences the done-transition flagged by the server", () => {
		expect(isBackgroundWorkSilencedStatus({ status: "idle", backgroundWorkActive: true })).toBe(
			true,
		);
	});

	it("never silences a permission wait, even if flagged", () => {
		// The server only sets the flag on done, but the gate must not depend on
		// that discipline: a blocked agent needs the user regardless of background work.
		expect(isBackgroundWorkSilencedStatus({ status: "waiting", backgroundWorkActive: true })).toBe(
			false,
		);
	});

	it("does not silence an unflagged done", () => {
		expect(isBackgroundWorkSilencedStatus({ status: "idle" })).toBe(false);
		expect(isBackgroundWorkSilencedStatus({ status: "idle", backgroundWorkActive: false })).toBe(
			false,
		);
	});

	it("does not silence a flagged event whose status is missing", () => {
		// The flag only means "done with background work" on an idle transition; a
		// status-less (substatus-only) update carrying it proves nothing, and a
		// flagged non-idle status (e.g. a mis-tagged error) must stay audible.
		expect(isBackgroundWorkSilencedStatus({ backgroundWorkActive: true })).toBe(false);
		expect(isBackgroundWorkSilencedStatus({ status: "working", backgroundWorkActive: true })).toBe(
			false,
		);
	});
});
