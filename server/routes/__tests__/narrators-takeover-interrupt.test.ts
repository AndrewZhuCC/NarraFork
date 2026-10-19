/**
 * `POST /narrators/:id/interrupt` against a session whose run the user holds (takeover).
 *
 * Holding the Stop button during a takeover is how a user makes a queued message run
 * next. Stop there must end ONLY the current turn: the run (its owner, destination and
 * deadline) stays, the hold stays, and the stopped turn returns to the hold where the
 * queued input is consumed. Ending the run would discard that input and hand the
 * caller a result it was never supposed to get yet.
 *
 * Two signals are observable and both are pinned: the session's turn controller (what
 * Stop aborts) and the run's own cancellation (what must stay untouched while held).
 * A session that is not held is stopped the ordinary way: its turn ends and so does
 * the run. Every session takes this same route; none is special because it was
 * created by delegation.
 */
import { afterAll, afterEach, beforeAll, describe, expect, test } from "bun:test";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { Hono } from "hono";
import { AppError } from "../../lib/errors";

const previousHome = process.env.NARRAFORK_HOME;
const testHome = mkdtempSync(join(tmpdir(), "narrafork-takeover-interrupt-"));
process.env.NARRAFORK_HOME = testHome;
process.env.NARRAFORK_ALLOW_MULTIPLE = "1";

// Imported after NARRAFORK_HOME is redirected: these modules initialise the DB at
// import time and must not touch the developer's real instance.
const { narratorRoutes } = await import("../narrators");
const { db } = await import("../../db");
const { narrators, users } = await import("../../db/schema");
const { eq } = await import("drizzle-orm");
const { generateId } = await import("../../lib/id");
const { getExecutionOwner, getRunHold, setRunControl, setRunHold, tryClaimExecution } =
	await import("../../services/agent-runtime/ownership");
type RunControl = import("../../services/agent-runtime/ownership").RunControl;
const isTakenOver = (id: string) => !!getRunHold(id);
const markTakenOver = (id: string) => setRunHold(id, { released: false });
const { activeNarrators } = await import("../../services/narrator-session-state");
type ActiveNarrator = import("../../services/narrator-session-state").ActiveNarrator;

afterAll(() => {
	if (previousHome === undefined) delete process.env.NARRAFORK_HOME;
	else process.env.NARRAFORK_HOME = previousHome;
	rmSync(testHome, { recursive: true, force: true });
});

const OWNER = "takeover-interrupt-owner";
let parentId: string;
let subagentId: string;

function app() {
	const instance = new Hono();
	instance.use("*", async (c, next) => {
		c.set("user", { sub: OWNER, role: "user", iat: 0, exp: Number.MAX_SAFE_INTEGER });
		await next();
	});
	instance.onError((error) => {
		if (error instanceof AppError) {
			return new Response(JSON.stringify({ error: error.message, code: error.code }), {
				status: error.statusCode,
				headers: { "content-type": "application/json" },
			});
		}
		return new Response(JSON.stringify({ error: String(error) }), { status: 500 });
	});
	instance.route("/narrators", narratorRoutes);
	return instance;
}

function interrupt(id: string) {
	return app().request(`http://localhost/narrators/${id}/interrupt`, { method: "POST" });
}

beforeAll(async () => {
	const now = new Date().toISOString();
	await db.insert(users).values({
		id: OWNER,
		username: `${OWNER}-${Date.now()}`,
		passwordHash: "x",
		role: "user",
		createdAt: now,
	});

	parentId = generateId();
	subagentId = generateId();
	await db.insert(narrators).values([
		{
			id: parentId,
			title: "parent",
			ownerUserId: OWNER,
			visibility: "private",
			status: "working",
			createdAt: now,
			updatedAt: now,
		},
		{
			id: subagentId,
			title: "taken-over subagent",
			ownerUserId: OWNER,
			visibility: "private",
			variant: "subagent:general",
			type: "subagent",
			parentNarratorId: parentId,
			// A subagent holds no access state of its own: the route gate judges it by
			// this root. Omitting it makes every request a 404 before the handler runs.
			aclRootNarratorId: parentId,
			status: "working",
			createdAt: now,
			updatedAt: now,
		},
	]);
});

/**
 * Stand in for a run of this session in flight the way the run service has it: the
 * owner + run control in the registry and the session's live turn controller. A run
 * parked by its hold has no turn in flight, only a wake.
 */
function registerRun(id: string, parked = false) {
	const owner = tryClaimExecution(id, "run");
	if (!owner) throw new Error("expected a free execution slot");
	const run: RunControl = {
		startedAt: Date.now(),
		cancel: new AbortController(),
		timedOut: false,
		terminal: new Promise(() => {}),
		deliverTo: { narratorId: parentId, toolUseId: "origin-tool" },
		...(parked ? { wake: () => {} } : {}),
	};
	setRunControl(owner, run);
	const turn = new AbortController();
	if (!parked)
		activeNarrators.set(id, {
			narratorId: id,
			alive: true,
			abortController: turn,
		} as unknown as ActiveNarrator);
	return { run, turn };
}

afterEach(async () => {
	setRunHold(subagentId, undefined);
	getExecutionOwner(subagentId)?.release();
	activeNarrators.delete(subagentId);
	// Restore the running-turn state the other tests assume, so a case that parks the
	// subagent in idle cannot make its neighbours pass for the wrong reason.
	await db
		.update(narrators)
		.set({ status: "working", substatus: "[]" })
		.where(eq(narrators.id, subagentId));
});

describe("interrupting a held run", () => {
	test("stops only the current turn; the run and the hold survive", async () => {
		markTakenOver(subagentId);
		const { run, turn } = registerRun(subagentId);

		const res = await interrupt(subagentId);

		expect(res.status).toBe(200);
		expect(await res.json()).toEqual({ interrupted: true });
		// The turn must still be stopped — a takeover does not make Stop a no-op.
		expect(turn.signal.aborted).toBe(true);
		// But the run is not cancelled: the stopped turn returns to the hold.
		expect(run.cancel.signal.aborted).toBe(false);
		expect(isTakenOver(subagentId)).toBe(true);
		const after = await db.select().from(narrators).where(eq(narrators.id, subagentId));
		expect(after[0]?.substatus).not.toContain("interrupted");
	});

	test("a parked held run has no turn in flight: nothing is stopped or settled", async () => {
		/*
		 * Parked in `idle[taken_over]` waiting for the user's next input: nothing is in
		 * flight to stop. Ending the hold is `POST /:id/stop-takeover`'s job, and the
		 * zombie fallback is inert because a held run is never a zombie.
		 */
		markTakenOver(subagentId);
		const { run } = registerRun(subagentId, true);
		await db
			.update(narrators)
			.set({ status: "idle", substatus: JSON.stringify(["taken_over"]) })
			.where(eq(narrators.id, subagentId));

		const res = await interrupt(subagentId);

		expect(res.status).toBe(200);
		expect(await res.json()).toEqual({ interrupted: false });
		expect(run.cancel.signal.aborted).toBe(false);
		expect(isTakenOver(subagentId)).toBe(true);
		const after = await db.select().from(narrators).where(eq(narrators.id, subagentId));
		expect(after[0]?.status).toBe("idle");
		expect(after[0]?.substatus).toBe(JSON.stringify(["taken_over"]));
	});

	test("a held run whose turn just ended is never treated as a zombie", async () => {
		markTakenOver(subagentId);
		// working in the DB, no live turn (between passes) — still held, not a zombie.
		const res = await interrupt(subagentId);

		expect(await res.json()).toEqual({ interrupted: false });
		const after = await db.select().from(narrators).where(eq(narrators.id, subagentId));
		expect(after[0]?.status).toBe("working");
		expect(isTakenOver(subagentId)).toBe(true);
	});
});

describe("interrupting a run that is not held", () => {
	test("stops the session's turn the ordinary way (the run then ends)", async () => {
		const { turn } = registerRun(subagentId);

		const res = await interrupt(subagentId);

		expect(await res.json()).toEqual({ interrupted: true });
		// The turn ends; the run's loop then reaches its single finalizer, which
		// delivers the interrupted result once to the registered destination.
		expect(turn.signal.aborted).toBe(true);
	});

	test("an undelegated session takes the very same path", async () => {
		const owner = tryClaimExecution(parentId, "run");
		const turn = new AbortController();
		activeNarrators.set(parentId, {
			narratorId: parentId,
			alive: true,
			abortController: turn,
		} as unknown as ActiveNarrator);
		try {
			const res = await interrupt(parentId);
			expect(await res.json()).toEqual({ interrupted: true });
			expect(turn.signal.aborted).toBe(true);
		} finally {
			owner?.release();
			activeNarrators.delete(parentId);
		}
	});

	test("a zombie row (working, nothing running) is reset to interrupted", async () => {
		const res = await interrupt(subagentId);

		expect(await res.json()).toEqual({ interrupted: true });
		const after = await db.select().from(narrators).where(eq(narrators.id, subagentId));
		expect(after[0]?.status).toBe("idle");
		expect(after[0]?.substatus).toContain("interrupted");
	});
});
