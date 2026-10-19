import { afterAll, expect, spyOn, test } from "bun:test";
import { EventEmitter } from "node:events";
import { hotSafe } from "../../lib/hot-safe";
import { backgroundOccupancyStore } from "../background-occupancy-store";
import type { ActiveNarrator } from "../narrator-session-state";

// Run this file in its own Bun process: seed the pre-P1 objects BEFORE importing
// the runtime, exactly as old --hot closures would hold them across re-evaluation.
const legacySessions = hotSafe<Map<string, ActiveNarrator>>(
	"narrafork.activeNarrators",
	() => new Map(),
);
const legacyAdmissions = hotSafe<Set<string>>("narrafork.narratorLoopAdmissions", () => new Set());
const legacyTools = hotSafe<Map<string, Promise<void>>>(
	"narrafork.narratorToolExecutionAdmissions",
	() => new Map(),
);

function session(id: string): ActiveNarrator {
	return {
		narratorId: id,
		conversationId: "legacy-conversation",
		cwd: "/legacy",
		model: "legacy:model",
		provider: "legacy",
		systemPrompt: "retained prompt",
		events: new EventEmitter(),
		alive: true,
		locale: "zh-CN",
		abortController: new AbortController(),
		_enabledOptionalTools: new Set(["ShareFile"]),
		_disabledTools: new Set(),
		_blockedSkills: { all: false, names: new Set() },
		_substatus: new Set(["reasoning"]),
		_lastMeterUsage: 42,
		_defaultDeviceId: "legacy-device",
	};
}
const PRIMARY = "hot-draining-primary";
const CHILD = "hot-active-child";
const REPLAY = "hot-replaying-tool";
const CACHE = "hot-idle-cache";
const primary = session(PRIMARY);
primary.alive = false;
primary._loopRunning = false; // finalizer still owns its original reservation
const cached = session(CACHE);
const replayCompletion = Promise.resolve();
legacySessions.set(PRIMARY, primary);
legacyAdmissions.add(PRIMARY);
// Every old run (whatever created its session) held the same loop reservation.
legacyAdmissions.add(CHILD);
legacyTools.set(REPLAY, replayCompletion);
legacySessions.set(CACHE, cached);

// Simulate installing occupancy after runtime ownership already used hot records.
// This session never passes through the legacy adoption or modern view setters.
const RETAINED_SESSION = "hot-retained-session-only";
const retainedSession = session(RETAINED_SESSION);
retainedSession.alive = false;
retainedSession._loopRunning = false;
hotSafe<Map<string, { session?: ActiveNarrator }>>(
	"narrafork.agentRuntime.records",
	() => new Map(),
).set(RETAINED_SESSION, { session: retainedSession });
const initialLiveness: [string, boolean][] = [];
const initialPublish = spyOn(backgroundOccupancyStore, "setRuntimeLive").mockImplementation(
	(id, live) => {
		initialLiveness.push([id, live]);
	},
);
const ownership = await import("../agent-runtime/ownership");
initialPublish.mockRestore();
const sessions = ownership.createRuntimeMapView("session");
const tools = ownership.createRuntimeMapView("toolReplayCompletion");
const extraIds = new Set<string>([RETAINED_SESSION]);
function requireOwner(id: string) {
	const owner = ownership.getExecutionOwner(id);
	if (!owner) throw new Error(`Missing execution owner: ${id}`);
	return owner;
}
function newOwner(id: string) {
	const owner = ownership.tryClaimExecution(id, "run");
	if (!owner) throw new Error(`Could not claim ${id}`);
	return owner;
}

afterAll(() => {
	legacySessions.clear();
	legacyAdmissions.clear();
	legacyTools.clear();
	for (const id of [PRIMARY, CHILD, REPLAY, CACHE, ...extraIds]) {
		ownership.getExecutionOwner(id)?.release();
		sessions.delete(id);
		tools.delete(id);
	}
});

test("first occupancy installation seeds existing session-only hot records without mutation", () => {
	expect(sessions.get(RETAINED_SESSION)).toBe(retainedSession);
	expect(ownership.getExecutionOwner(RETAINED_SESSION)).toBeUndefined();
	expect(initialLiveness).toContainEqual([RETAINED_SESSION, true]);
	expect(initialLiveness).toContainEqual([PRIMARY, true]);
	expect(initialLiveness).toContainEqual([REPLAY, true]);
});

test("adopts native containers once without losing observable objects, caches or old owners", () => {
	expect(hotSafe("narrafork.activeNarrators", () => legacySessions)).toBe(legacySessions);
	expect(hotSafe("narrafork.narratorLoopAdmissions", () => legacyAdmissions)).toBe(
		legacyAdmissions,
	);
	// Native slots are empty: old objects now forward, not maintain a second Map.
	expect([...Map.prototype.entries.call(legacySessions)]).toEqual([]);
	expect([...Set.prototype.values.call(legacyAdmissions)]).toEqual([]);
	expect(sessions.get(PRIMARY)).toBe(primary);
	expect(legacySessions.get(PRIMARY)).toBe(primary);
	expect(sessions.get(CACHE)).toBe(cached);
	expect(sessions.get(CACHE)?._enabledOptionalTools).toBe(cached._enabledOptionalTools);
	expect(sessions.get(CACHE)?._lastMeterUsage).toBe(42);
	expect(tools.get(REPLAY)).toBe(replayCompletion);
	for (const id of [PRIMARY, CHILD, REPLAY]) {
		expect(ownership.tryClaimExecution(id, "run")).toBeNull();
		expect(legacyAdmissions.has(id)).toBe(true);
	}
	expect(ownership.getExecutionOwner(CACHE)).toBeUndefined();
});

test("old primary cleanup releases only after both session and reservation drain", () => {
	const owner = requireOwner(PRIMARY);
	legacySessions.delete(PRIMARY);
	expect(sessions.has(PRIMARY)).toBe(false);
	expect(owner.isCurrent()).toBe(true);
	legacyAdmissions.delete(PRIMARY);
	expect(owner.isCurrent()).toBe(false);
	const next = newOwner(PRIMARY);
	const replacement = session(PRIMARY);
	sessions.set(PRIMARY, replacement);
	legacyAdmissions.delete(PRIMARY); // repeated stale finally
	legacySessions.delete(PRIMARY);
	expect(requireOwner(PRIMARY)).toBe(next);
	expect(sessions.get(PRIMARY)).toBe(replacement);
	expect(legacySessions.get(PRIMARY)).toBe(replacement);
});

test("an old run reservation protects the ownership gap until its own terminal", () => {
	const owner = requireOwner(CHILD);
	expect(owner.isCurrent()).toBe(true);
	legacyAdmissions.delete(CHILD);
	expect(owner.isCurrent()).toBe(false);
});

test("old loop add/delete binds its epoch, never releases a modern successor", () => {
	const id = "hot-late-old-finally";
	extraIds.add(id);
	const previous = session(id);
	legacySessions.set(id, previous);
	legacyAdmissions.add(id);
	const old = requireOwner(id);
	old.release(); // simulate ownership superseded before late old callbacks
	const next = newOwner(id);
	const replacement = session(id);
	sessions.set(id, replacement);
	legacyAdmissions.delete(id);
	legacySessions.delete(id);
	expect(requireOwner(id)).toBe(next);
	expect(sessions.get(id)).toBe(replacement);
	expect(() => legacyAdmissions.add(id)).toThrow("execution owner");
	expect(requireOwner(id)).toBe(next);
});

test("old tool completion release cannot remove the replacement completion or owner", () => {
	const old = requireOwner(REPLAY);
	old.release();
	const next = newOwner(REPLAY);
	const replacement = Promise.resolve();
	tools.set(REPLAY, replacement);
	legacyTools.delete(REPLAY);
	expect(tools.get(REPLAY)).toBe(replacement);
	expect(requireOwner(REPLAY)).toBe(next);
});

test("outer legacy terminal releases ownership even when inner cache cleanup failed", () => {
	for (const kind of ["delegated", "undelegated"] as const) {
		const id = `hot-failed-inner-cleanup-${kind}`;
		extraIds.add(id);
		legacyAdmissions.add(id);
		legacySessions.set(id, session(id));
		legacyAdmissions.delete(id); // old inner finally threw before session.delete
		expect(ownership.getExecutionOwner(id)).toBeUndefined();
		expect(sessions.has(id)).toBe(false);
		const next = newOwner(id);
		sessions.set(id, session(id));
		legacySessions.delete(id);
		expect(requireOwner(id)).toBe(next);
		expect(sessions.has(id)).toBe(true);
	}
});

test("a second module evaluation retains forwarding identity and does not re-adopt projections", async () => {
	const path = `../agent-runtime/ownership.ts?hot-contract=${Date.now()}`;
	const before = requireOwner(PRIMARY);
	const get = legacySessions.get;
	const remove = legacyAdmissions.delete;
	const publish = spyOn(backgroundOccupancyStore, "setRuntimeLive").mockImplementation(() => {});
	let reloaded: typeof ownership;
	try {
		reloaded = await import(path);
		expect(publish).toHaveBeenCalledWith(RETAINED_SESSION, true);
		expect(publish).toHaveBeenCalledWith(PRIMARY, true);
		expect(publish).toHaveBeenCalledWith(CACHE, true);
		expect(publish).toHaveBeenCalledWith(REPLAY, true);
	} finally {
		publish.mockRestore();
	}
	expect(reloaded.getExecutionOwner(PRIMARY)).toBe(before);
	expect(legacySessions.get).toBe(get);
	expect(legacyAdmissions.delete).toBe(remove);
	expect(reloaded.createRuntimeMapView("session").get(PRIMARY)).toBe(sessions.get(PRIMARY));
	legacyAdmissions.delete(PRIMARY);
	expect(reloaded.getExecutionOwner(PRIMARY)).toBe(before);
	const id = "hot-new-after-second-load";
	extraIds.add(id);
	const value = session(id);
	legacySessions.set(id, value);
	legacyAdmissions.add(id);
	expect(reloaded.createRuntimeMapView("session").get(id)).toBe(value);
	expect(reloaded.tryClaimExecution(id, "run")).toBeNull();
	legacySessions.delete(id);
	legacyAdmissions.delete(id);
	expect(reloaded.getExecutionOwner(id)).toBeUndefined();
});
