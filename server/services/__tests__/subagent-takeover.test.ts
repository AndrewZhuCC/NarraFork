import { afterEach, describe, expect, test } from "bun:test";

/*
 * The user's hold on a run's result (takeover) is one generic piece of run state:
 * it does not depend on how the session was created or whether its run is detached.
 */
const { getRunHold, isRunHeldForDisplay, listRunHolds, preserveHeldSubstatus, setRunHold } =
	await import("../agent-runtime/ownership");

const SUBAGENT_ID = "subagent-takeover-test";
const hold = () => setRunHold(SUBAGENT_ID, { released: false });

afterEach(() => {
	setRunHold(SUBAGENT_ID, undefined);
});

describe("hold state basics", () => {
	test("a hold is recorded and listed", () => {
		hold();
		expect(getRunHold(SUBAGENT_ID)).toEqual({ released: false });
		expect(listRunHolds()).toContain(SUBAGENT_ID);
	});

	test("taking over does not invent a pending release", () => {
		hold();
		expect(getRunHold(SUBAGENT_ID)?.released).toBe(false);
	});

	test("clearing removes the hold entirely", () => {
		hold();
		setRunHold(SUBAGENT_ID, undefined);
		expect(getRunHold(SUBAGENT_ID)).toBeUndefined();
		expect(listRunHolds()).not.toContain(SUBAGENT_ID);
	});
});

/*
 * A stopped turn of a held run returns to the hold, so every status written while held
 * keeps the durable `taken_over` mirror (which is also what restart recovery restores
 * the hold from). Repeated Stops therefore keep the takeover visible every time.
 */
describe("status writes while held keep the takeover mirror", () => {
	test("every write during a hold keeps taken_over", () => {
		hold();
		expect(preserveHeldSubstatus(SUBAGENT_ID, ["interrupted"])).toEqual([
			"interrupted",
			"taken_over",
		]);
		expect(preserveHeldSubstatus(SUBAGENT_ID, ["interrupted"])).toContain("taken_over");
		expect(preserveHeldSubstatus(SUBAGENT_ID, ["taken_over"])).toEqual(["taken_over"]);
		// Deciding the substatus never consumes the hold.
		expect(getRunHold(SUBAGENT_ID)).toBeDefined();
	});

	test("outside any hold a status write is unchanged", () => {
		expect(preserveHeldSubstatus(SUBAGENT_ID, ["interrupted"])).toEqual(["interrupted"]);
		expect(getRunHold(SUBAGENT_ID)).toBeUndefined();
	});
});

/*
 * Control flow and display diverge in one window: after the user lets go while a
 * turn is still running, the release is deferred to that run's terminal (the hold
 * stays so the run delivers exactly once), but the badge must not keep claiming the
 * user is driving.
 */
describe("display state vs control-flow state after a deferred release", () => {
	test("a deferred release stops the badge while keeping the hold", () => {
		setRunHold(SUBAGENT_ID, { released: true });
		expect(getRunHold(SUBAGENT_ID)).toBeDefined();
		expect(isRunHeldForDisplay(SUBAGENT_ID)).toBe(false);
		expect(listRunHolds()).toContain(SUBAGENT_ID);
	});

	test("an ACTIVE hold is displayed", () => {
		hold();
		expect(isRunHeldForDisplay(SUBAGENT_ID)).toBe(true);
	});

	test("a session that was never held is not displayed", () => {
		expect(isRunHeldForDisplay(SUBAGENT_ID)).toBe(false);
	});
});
