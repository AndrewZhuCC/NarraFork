/**
 * The live sessions of the process: who is in which page, the spaces' lanes, the barrier for
 * writes from outside, re-authorization, and the life cycle of sessions.
 *
 *   - Join: the page must exist and be readable by the socket's person (a page that does not
 *     exist and one they may not read answer the same `access_denied`); write access decides
 *     editing or viewing. Checked again on every access change (`narrator:access_changed`,
 *     `project:access_changed`, `auth:changed`): losing read ends the participant's session,
 *     losing write turns it into a viewer. A socket whose login session ends is closed by the
 *     WebSocket layer, which ends its participants.
 *   - A session opens in its space's lane on the first join, and closes (committing everything)
 *     {@link OUTLINE_LIVE_TIMING.idleCloseMs} after the last participant left, when the outline
 *     is switched off, and at shutdown.
 *   - Limits: {@link OUTLINE_LIVE_LIMITS} per socket, page and process.
 */
import { eventBus, type NarraForkEvent } from "@server/lib/event-bus";
import { hotOnce, hotSafe } from "@server/lib/hot-safe";
import { logger } from "@server/lib/logger";
import { subscribeSettingsChanges } from "@server/lib/settings";
import {
	encodeLiveFrame,
	LIVE_FRAME,
	type LiveCloseCode,
	type LiveFrame,
	type LiveJoinHead,
	type LivePresenceUp,
	OUTLINE_LIVE_LIMITS,
} from "@shared/outline-live-protocol";
import { canReadSpace, canWriteSpace } from "../access";
import { isOutlineEnabled } from "../feature";
import { setOutlineLiveBarrier } from "../live-barrier";
import { outlineStore } from "../store";
import type { OutlineSpaceRow } from "../store/port";
import { holdsLane, isOwnLiveWrite, SpaceLane } from "./lane";
import { replayAllLiveEpochs, replayLiveEpoch } from "./replay";
import {
	type CommittedEvent,
	LivePage,
	type LiveSocket,
	OUTLINE_LIVE_TIMING,
	type Participant,
} from "./session";

interface Binding {
	page: LivePage;
	participant: Participant;
}

type ReauthFilter = (space: OutlineSpaceRow, socket: LiveSocket) => boolean;

const state = hotSafe("narrafork.outlineLive.state", () => ({
	sessions: new Map<string, LivePage>(),
	spaces: new Map<string, OutlineSpaceRow>(),
	lanes: new Map<string, SpaceLane>(),
	pending: new Map<string, CommittedEvent[]>(),
	sockets: new Map<LiveSocket, Map<number, Binding>>(),
	/** Joins being authorized or opened, per socket (they count toward the socket's limit). */
	joining: new Map<LiveSocket, Set<number>>(),
	reauthFilters: [] as ReauthFilter[],
	reauthTimer: undefined as ReturnType<typeof setTimeout> | undefined,
	accessEpoch: 0,
	replayDone: null as Promise<unknown> | null,
}));

function sessionsOf(spaceId: string): LivePage[] {
	return [...state.sessions.values()].filter((s) => s.spaceId === spaceId);
}

function laneFor(spaceId: string): SpaceLane {
	let lane = state.lanes.get(spaceId);
	if (!lane) {
		lane = new SpaceLane(spaceId, () => drainCommits(spaceId));
		state.lanes.set(spaceId, lane);
	}
	return lane;
}

/** Drop a lane nothing uses any more. */
function retireLane(spaceId: string): void {
	setTimeout(() => {
		const lane = state.lanes.get(spaceId);
		if (lane && !lane.busy && sessionsOf(spaceId).length === 0) {
			state.lanes.delete(spaceId);
			state.pending.delete(spaceId);
		}
	}, 0).unref?.();
}

/** Push commits recorded for the space into its sessions (runs after every lane task). */
async function drainCommits(spaceId: string): Promise<void> {
	const list = state.pending.get(spaceId);
	if (!list || list.length === 0) return;
	state.pending.delete(spaceId);
	for (const session of sessionsOf(spaceId)) {
		await session.applyCommits(list);
		// An outside write merged into the live text is logged right away.
		await session.persistNow();
	}
}

function onCommitted(event: Extract<NarraForkEvent, { type: "outline:committed" }>): void {
	if (isOwnLiveWrite()) return;
	const lane = state.lanes.get(event.spaceId);
	if (!lane) return;
	const list = state.pending.get(event.spaceId);
	if (list) list.push(event);
	else state.pending.set(event.spaceId, [event]);
	// Outside a lane task (a write that started before the session existed): drain soon.
	if (!holdsLane(event.spaceId)) void lane.run(() => {});
}

// ─────────────────────────────────────────────────────────────────────────────
// The barrier (`live-barrier.ts`)
// ─────────────────────────────────────────────────────────────────────────────

async function runBehindBarrier<T>(spaceId: string, fn: () => Promise<T>): Promise<T> {
	const lane = state.lanes.get(spaceId);
	if (!lane) return await fn();
	return await lane.run(async () => {
		for (const session of sessionsOf(spaceId)) await session.flushContent();
		return await fn();
	});
}

setOutlineLiveBarrier({ run: runBehindBarrier, holds: holdsLane });

// ─────────────────────────────────────────────────────────────────────────────
// Joining and leaving
// ─────────────────────────────────────────────────────────────────────────────

function sendClosed(socket: LiveSocket, handle: number, code: LiveCloseCode): void {
	socket.send(encodeLiveFrame({ kind: LIVE_FRAME.closed, handle, head: { code } }));
}

async function authorize(
	socket: LiveSocket,
	pageId: string,
): Promise<{ space: OutlineSpaceRow; canWrite: boolean } | null> {
	if (!socket.userId) return null;
	const page = await outlineStore.getBlock(pageId);
	if (!page || page.deleted || page.parentId !== null || page.pageId !== page.id) return null;
	const space = await outlineStore.getSpace(page.spaceId);
	if (!space) return null;
	const principal = { userId: socket.userId, isAdmin: socket.isAdmin };
	if (!(await canReadSpace(principal, space))) return null;
	return { space, canWrite: await canWriteSpace(principal, space) };
}

function validJoinHead(head: unknown): LiveJoinHead | null {
	const h = head as Partial<LiveJoinHead> | null;
	if (!h || typeof h.page !== "string" || h.page.length === 0 || h.page.length > 128) return null;
	if (h.mode !== "edit" && h.mode !== "view") return null;
	if (typeof h.key !== "string" || !/^[A-Za-z0-9_-]{8,64}$/.test(h.key)) return null;
	const r = h.resume;
	if (
		r !== undefined &&
		(!r ||
			!Number.isSafeInteger(r.epoch) ||
			!Number.isSafeInteger(r.op) ||
			!Number.isSafeInteger(r.tx))
	)
		return null;
	return h as LiveJoinHead;
}

/** Open the session of a page, replaying any log a previous session left. In the lane. */
async function openSession(
	pageId: string,
	space: OutlineSpaceRow,
): Promise<LivePage | { refused: LiveCloseCode }> {
	const existing = state.sessions.get(pageId);
	if (existing && !existing.closed) return existing;
	if (state.sessions.size >= OUTLINE_LIVE_LIMITS.livePages) return { refused: "unavailable" };
	// What an earlier session of this page left uncommitted goes in first.
	for (const e of await outlineStore.listLiveEpochs({
		after: { pageBlockId: pageId, epoch: 0 },
		limit: 8,
	})) {
		if (e.pageBlockId !== pageId) break;
		if ((await replayLiveEpoch(pageId, e.epoch)) === "kept") return { refused: "unavailable" };
	}
	const lane = laneFor(space.id);
	const opened = await LivePage.open(
		pageId,
		space.id,
		(task) => void lane.run(task),
		(page) => void lane.run(() => closeSession(page, "ended", true)),
	);
	if ("refused" in opened) return opened;
	state.sessions.set(pageId, opened);
	state.spaces.set(space.id, space);
	return opened;
}

/** Close one session (commit everything, end its participants). In its lane. */
async function closeSession(page: LivePage, code: LiveCloseCode, idle = false): Promise<void> {
	// Someone joined during the idle period.
	if (idle && page.participants.size > 0) return;
	for (const bindings of state.sockets.values()) {
		for (const [handle, binding] of [...bindings])
			if (binding.page === page) bindings.delete(handle);
	}
	await page.close(code);
	if (state.sessions.get(page.pageId) === page) state.sessions.delete(page.pageId);
	retireLane(page.spaceId);
}

/** A `join` frame. */
export async function joinLive(socket: LiveSocket, frame: LiveFrame): Promise<void> {
	const handle = frame.handle;
	const head = validJoinHead(frame.head);
	if (!head || handle === 0) return sendClosed(socket, handle, "unavailable");
	if (!isOutlineEnabled()) return sendClosed(socket, handle, "unavailable");
	let bindings = state.sockets.get(socket);
	// Joining again on a handle (a resync, a resume) replaces what was there.
	const previous = bindings?.get(handle);
	if (previous) {
		bindings?.delete(handle);
		const prior = previous;
		void laneFor(prior.page.spaceId).run(() => prior.page.leave(prior.participant));
	}
	let joining = state.joining.get(socket);
	if (!joining) {
		joining = new Set();
		state.joining.set(socket, joining);
	}
	if (
		(bindings?.size ?? 0) + joining.size >= OUTLINE_LIVE_LIMITS.sessionsPerSocket ||
		joining.has(handle)
	)
		return sendClosed(socket, handle, "unavailable");
	joining.add(handle);
	try {
		let verdict: Awaited<ReturnType<typeof authorize>> = null;
		for (;;) {
			const epoch = state.accessEpoch;
			verdict = await authorize(socket, head.page);
			if (epoch === state.accessEpoch) break;
		}
		if (!verdict) return sendClosed(socket, handle, "access_denied");
		const { space, canWrite } = verdict;
		const result = await laneFor(space.id).run(async () => {
			if (!isOutlineEnabled()) return { refused: "unavailable" as LiveCloseCode };
			const page = await openSession(head.page, space);
			if ("refused" in page) return page;
			const joined = page.join(socket, handle, head, frame.body, head.mode === "edit" && canWrite);
			if ("refused" in joined) return joined;
			return { page, participant: joined };
		});
		if ("refused" in result) return sendClosed(socket, handle, result.refused);
		if (socket.closed) {
			// The socket closed while the join ran.
			void laneFor(space.id).run(() => result.page.leave(result.participant));
			return;
		}
		bindings = state.sockets.get(socket);
		if (!bindings) {
			bindings = new Map();
			state.sockets.set(socket, bindings);
		}
		bindings.set(handle, result);
	} catch (error) {
		logger.warn("Outline live join failed", { error: String(error) });
		sendClosed(socket, handle, "unavailable");
	} finally {
		joining.delete(handle);
		if (joining.size === 0) state.joining.delete(socket);
	}
}

function bindingOf(socket: LiveSocket, handle: number): Binding | undefined {
	return state.sockets.get(socket)?.get(handle);
}

/** Every frame of a socket after the join. */
export function handleLiveFrame(socket: LiveSocket, frame: LiveFrame): void {
	if (frame.kind === LIVE_FRAME.join) {
		void joinLive(socket, frame);
		return;
	}
	const binding = bindingOf(socket, frame.handle);
	if (!binding) return;
	const { page, participant } = binding;
	switch (frame.kind) {
		case LIVE_FRAME.leave:
			state.sockets.get(socket)?.delete(frame.handle);
			void laneFor(page.spaceId).run(() => page.leave(participant));
			break;
		case LIVE_FRAME.update: {
			// The socket's buffer may be reused once this returns.
			const body = frame.body.slice();
			void laneFor(page.spaceId).run(() => page.handleUpdate(participant, frame.n, body));
			break;
		}
		case LIVE_FRAME.tx: {
			const body = frame.body.slice();
			const head = frame.head;
			void laneFor(page.spaceId).run(() => page.handleTx(participant, frame.n, head, body));
			break;
		}
		case LIVE_FRAME.selection:
			page.setSelection(participant, (frame.head as LivePresenceUp | null)?.s ?? null);
			break;
		default:
			break;
	}
}

/** The socket closed: its participants leave. */
export function releaseLiveSocket(socket: LiveSocket): void {
	const bindings = state.sockets.get(socket);
	state.sockets.delete(socket);
	state.joining.delete(socket);
	if (!bindings) return;
	for (const { page, participant } of bindings.values()) {
		void laneFor(page.spaceId).run(() => page.leave(participant));
	}
}

/** The socket drained: participants that fell behind are told to rejoin. */
export function drainLiveSocket(socket: LiveSocket): void {
	for (const { page, participant } of state.sockets.get(socket)?.values() ?? []) {
		page.onDrain(participant);
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Re-authorization, the switch, shutdown
// ─────────────────────────────────────────────────────────────────────────────

async function reauthorize(): Promise<void> {
	state.reauthTimer = undefined;
	const filters = state.reauthFilters;
	state.reauthFilters = [];
	const verdicts = new Map<string, Promise<{ read: boolean; write: boolean }>>();
	for (const [socket, bindings] of [...state.sockets]) {
		for (const [handle, binding] of [...bindings]) {
			const space = state.spaces.get(binding.page.spaceId);
			if (!space || !filters.some((matches) => matches(space, socket))) continue;
			const key = `${socket.userId}\u0000${socket.isAdmin}\u0000${space.id}`;
			let verdict = verdicts.get(key);
			if (!verdict) {
				const principal = { userId: socket.userId, isAdmin: socket.isAdmin };
				verdict = (async () => {
					const fresh = (await outlineStore.getSpace(space.id)) ?? space;
					const read = await canReadSpace(principal, fresh);
					return { read, write: read && (await canWriteSpace(principal, fresh)) };
				})().catch((error) => {
					logger.warn("Outline live re-authorization failed", { error: String(error) });
					return { read: false, write: false };
				});
				verdicts.set(key, verdict);
			}
			const { read, write } = await verdict;
			if (bindings.get(handle) !== binding) continue;
			const { page, participant } = binding;
			if (!read) {
				bindings.delete(handle);
				void laneFor(page.spaceId).run(() => page.dismiss(participant, "access_denied"));
			} else {
				void laneFor(page.spaceId).run(() =>
					page.setWrite(participant, participant.wantsEdit && write),
				);
			}
		}
	}
}

function scheduleReauth(filter: ReauthFilter): void {
	state.accessEpoch++;
	state.reauthFilters.push(filter);
	if (!state.reauthTimer) {
		state.reauthTimer = setTimeout(() => void reauthorize(), OUTLINE_LIVE_TIMING.broadcastMs);
		state.reauthTimer.unref?.();
	}
}

/** Re-check the participants an access change may affect (exported for tests). */
export function onOutlineLiveAccessEvent(event: NarraForkEvent): void {
	switch (event.type) {
		case "narrator:access_changed":
			scheduleReauth((space) => space.narratorId === event.narratorId);
			break;
		case "project:access_changed":
			// A session's readers pass its project's gate, so session spaces may change too.
			scheduleReauth((space) => space.projectId === event.projectId || space.kind === "session");
			break;
		case "auth:changed":
			scheduleReauth((_space, socket) =>
				event.userId === undefined ? true : socket.userId === event.userId,
			);
			break;
		default:
			break;
	}
}

/** Close every session (commit everything first). The switch went off, or shutdown. */
export async function closeAllLiveSessions(code: LiveCloseCode): Promise<void> {
	const sessions = [...state.sessions.values()];
	await Promise.all(
		sessions.map((page) => laneFor(page.spaceId).run(() => closeSession(page, code))),
	);
}

/** Write every session's pending log rows now (shutdown, before the database closes). */
export async function persistAllLiveSessions(): Promise<void> {
	await Promise.all([...state.sessions.values()].map((page) => page.persistNow()));
}

/**
 * Replay what earlier sessions left in the log (the startup job). One run per process; later
 * calls return the same promise.
 */
export function startLiveReplay(): Promise<unknown> {
	state.replayDone ??= replayAllLiveEpochs(
		(pageId, epoch) => state.sessions.get(pageId)?.epoch === epoch,
		async (pageId, epoch) => {
			const page = await outlineStore.getBlock(pageId);
			const run = () => replayLiveEpoch(pageId, epoch);
			// Behind the space's lane when it has one, so it cannot race a session's commits.
			const lane = page ? state.lanes.get(page.spaceId) : undefined;
			if (!lane) return await run();
			return await lane.run(async () =>
				state.sessions.get(pageId) ? ("empty" as const) : await run(),
			);
		},
	)
		.then((result) => {
			if (result.committed || result.kept) logger.info("Outline live log replayed", result);
			return result;
		})
		.catch((error) => logger.warn("Outline live log replay failed", { error: String(error) }));
	return state.replayDone;
}

/** Counts for tests and diagnostics. */
export function outlineLiveStats(): {
	sessions: number;
	lanes: number;
	sockets: number;
	participants: number;
} {
	let participants = 0;
	for (const s of state.sessions.values()) participants += s.participants.size;
	return {
		sessions: state.sessions.size,
		lanes: state.lanes.size,
		sockets: state.sockets.size,
		participants,
	};
}

/** Whether nothing is queued in any lane or waiting to be broadcast (tests). */
export function outlineLiveQuiet(): boolean {
	for (const lane of state.lanes.values()) if (lane.busy) return false;
	for (const s of state.sessions.values()) if (!s.quiet) return false;
	return true;
}

/** Drop a page's session as if the process had died (tests of the replay). */
export function abandonLiveSessionForTests(pageId: string): void {
	const session = state.sessions.get(pageId);
	if (!session) return;
	session.abandon();
	state.sessions.delete(pageId);
	for (const bindings of state.sockets.values()) {
		for (const [handle, binding] of [...bindings])
			if (binding.page === session) bindings.delete(handle);
	}
	retireLane(session.spaceId);
}

/** The session of a page, if one is open (tests and diagnostics). */
export function liveSessionOf(pageId: string): LivePage | undefined {
	return state.sessions.get(pageId);
}

/** Run `fn` in a space's lane (tests). */
export function runInLane<T>(spaceId: string, fn: () => Promise<T> | T): Promise<T> {
	return laneFor(spaceId).run(fn);
}

if (hotOnce("narrafork.outlineLive.listenersRegistered")) {
	eventBus.on("outline:committed", onCommitted);
	eventBus.on("narrator:access_changed", onOutlineLiveAccessEvent);
	eventBus.on("project:access_changed", onOutlineLiveAccessEvent);
	eventBus.on("auth:changed", onOutlineLiveAccessEvent);
	subscribeSettingsChanges(() => {
		if (!isOutlineEnabled() && state.sessions.size > 0) void closeAllLiveSessions("unavailable");
	});
}
