/**
 * One page's live editing session (design §6.2–§6.6, plan §8, D33).
 *
 * WHAT IT HOLDS
 * -------------
 *   - a Yjs document with one `Y.Text` per block of the page, created from the blocks' content
 *     when the session opens (all of them at once: a text created lazily lets keystrokes race its
 *     creation, and redo then reverses typed text, as the prototype found);
 *   - a mirror of the page's structure (`mirror.ts`), equal to the blocks table between tasks;
 *   - the participants: people editing (write access) or viewing (read access) the page.
 *
 * HOW CHANGES FLOW
 * ----------------
 *   - Text: a participant's Yjs update is applied at once and broadcast to the others within
 *     {@link OUTLINE_LIVE_TIMING.broadcastMs}, merged with everything else of that window
 *     (`Y.mergeUpdates`), never echoed to its sender (who gets the transaction number back
 *     instead). Changed blocks are committed as the editor's own human commits a little later
 *     (after {@link OUTLINE_LIVE_TIMING.flushIdleMs} of quiet, at most
 *     {@link OUTLINE_LIVE_TIMING.flushMaxMs} after the first change), normalized off the main
 *     thread when large (`normalize.ts`).
 *   - Structure: a structural transaction (insert, move, delete, checkbox, plus the Yjs update
 *     of the same keystroke) is checked against the mirror, written as one commit through
 *     `writeOutline`, then broadcast in canonical form. A refused transaction is rolled back
 *     (its Yjs part included) and every later frame of that participant is dropped until it
 *     joins again; the client rebuilds its document and replays its own later changes.
 *   - Writes from outside (agents, reverts) reach the session through the barrier
 *     (`live-barrier.ts`): unflushed text is committed first, then the outside commit is read
 *     back (its block versions) and pushed in as a remote change, merged into the live text.
 *   - Every Yjs update is also appended to `outline_live_updates` within
 *     {@link OUTLINE_LIVE_TIMING.persistMs} (a snapshot first, increments after, compacted past
 *     a bound), so a crash loses nothing: a startup job replays what was not committed.
 *
 * Every method that touches the document or the database runs in the space's lane.
 */
import { generateId } from "@server/lib/id";
import { logger } from "@server/lib/logger";
import { compareOrderKeys, keyBetween, merge3 } from "@shared/outline";
import {
	encodeLiveFrame,
	isLiveTaskChar,
	LIVE_FRAME,
	LIVE_STATUS_OF_TASK,
	type LiveActor,
	type LiveBatchHead,
	type LiveCloseCode,
	type LiveCommitWire,
	type LiveJoinedHead,
	type LiveJoinHead,
	type LiveOp,
	type LivePeerWire,
	type LivePresenceDown,
	type LiveRejectCode,
	type LiveSelWire,
	type LiveTaskChar,
	OUTLINE_LIVE_LIMITS,
} from "@shared/outline-live-protocol";
import * as Y from "yjs";
import { OutlineBusyError, OutlineConflictError, OutlineWriteError } from "../errors";
import { OUTLINE_LIMITS, type OutlineTaskJson } from "../model";
import { outlineStore } from "../store";
import type { OutlineVersionRow } from "../store/port";
import {
	type OutlineCanonicalContent,
	type OutlineOp,
	type OutlineWriteResult,
	writeOutline,
} from "../write";
import { asOwnLiveWrite } from "./lane";
import { type MirrorBlock, PageMirror, taskCharOf } from "./mirror";
import { type LiveNormalized, normalizeLiveTexts } from "./normalize";

/** Timings of a session. Mutable only so tests can shorten them. */
export const OUTLINE_LIVE_TIMING: Record<
	| "broadcastMs"
	| "presenceMs"
	| "persistMs"
	| "flushIdleMs"
	| "flushMaxMs"
	| "idleCloseMs"
	| "flushRetryMs",
	number
> = {
	/** Changes are coalesced and broadcast this often (design §7.2). */
	broadcastMs: 50,
	/** Cursors are coalesced (last one wins) and broadcast this often. */
	presenceMs: 100,
	/** Yjs updates reach `outline_live_updates` at most this long after they arrive. */
	persistMs: 500,
	/** Changed text is committed after this much quiet… */
	flushIdleMs: 2_000,
	/** …and at most this long after the first change. */
	flushMaxMs: 10_000,
	/** A session nobody is in closes after this long (design §6.3, §7.2). */
	idleCloseMs: 60_000,
	/** Retry delay after a failed commit of live text. */
	flushRetryMs: 5_000,
};

export const OUTLINE_LIVE_BOUNDS = {
	/** A socket buffering more than this gets no more frames until it drains and rejoins. */
	lagBytes: 1024 * 1024,
	/** …and is told to rejoin once it is below this. */
	resumeBytes: 256 * 1024,
	/** Presence frames are skipped (kept for later) while a socket buffers more than this. */
	presenceDropBytes: 64 * 1024,
	/** Log rows or bytes of one epoch after which it is compacted into one snapshot row. */
	compactRows: 400,
	compactBytes: 1024 * 1024,
	/** Structural batches kept for resuming clients. */
	opLog: 256,
	/** Client keys whose last processed transaction is remembered. */
	clientKeys: 128,
	/** Blocks one block selection may name in presence. */
	presenceBlocks: 500,
} as const;

const SERVER_ORIGIN = { live: "server" } as const;
/** `actor_id` of the session's own log rows (snapshots, merges from outside). */
export const LIVE_LOG_ACTOR = "outline-live";
/** `actor_id` of sync rows: JSON `{ b: { blockId: content } }`, not a Yjs update. */
export const LIVE_SYNC_ACTOR = "outline-live-sync";
const CLIENT_ID_RE = /^[A-Za-z0-9_-]{8,64}$/;
const utf8 = new TextEncoder();

/** What a session needs of a socket. The WebSocket adapter provides it. */
export interface LiveSocket {
	/** Set once the socket closed. */
	readonly closed?: boolean;
	readonly userId: string;
	readonly isAdmin: boolean;
	readonly name: string;
	readonly color: string;
	/** `false` when the frame could not be queued (the socket is gone). */
	send(frame: Uint8Array): boolean;
	bufferedAmount(): number;
}

export class Participant {
	/** Highest transaction processed. */
	lastTx = 0;
	/** Highest transaction acknowledged to the client. */
	ackedTx = 0;
	/** Set after a refusal: frames are dropped until the client joins again. */
	blocked = false;
	/** Frames were held back because the socket fell behind; it is told to rejoin when it drains. */
	lagging = false;
	/** The rejoin request went out; nothing more is sent until it joins again. */
	resyncSent = false;
	sel: LiveSelWire | null = null;
	/** Where this participant's share of the current broadcast window starts. */
	windowStart = 0;
	commitStart = 0;

	constructor(
		readonly pno: number,
		readonly socket: LiveSocket,
		readonly handle: number,
		readonly key: string,
		readonly wantsEdit: boolean,
		public canWrite: boolean,
	) {}

	get userId(): string {
		return this.socket.userId;
	}

	peer(): LivePeerWire {
		return [this.pno, this.userId, this.socket.name, this.socket.color, this.canWrite ? "e" : "v"];
	}
}

interface WindowUpdate {
	from: number;
	update: Uint8Array;
}

interface PendingRow {
	actorKind: "human" | "system";
	actorId: string;
	update: Uint8Array;
}

export interface CommittedEvent {
	spaceId: string;
	commitId: string;
	seq: number;
	pageIds: string[];
	actorKind: "human" | "agent" | "system";
	actorId: string;
}

type Refusal = { refused: LiveCloseCode };

function bytesOf(text: string): number {
	return utf8.encode(text).length;
}

function sameOps(a: readonly LiveOp[], b: readonly LiveOp[]): boolean {
	return JSON.stringify(a) === JSON.stringify(b);
}

/** Random positive int31: a session instance a resuming client must name. */
function newEpoch(): number {
	const a = new Uint32Array(1);
	crypto.getRandomValues(a);
	return ((a[0] as number) % 0x7ffffffe) + 1;
}

export class LivePage {
	readonly doc = new Y.Doc();
	readonly texts: Y.Map<Y.Text>;
	readonly mirror: PageMirror;
	readonly epoch = newEpoch();
	readonly participants = new Map<number, Participant>();
	private nextPno = 1;
	private readonly keys = new Map<string, number>();
	private opSeq = 0;
	private readonly opLog: LiveCommitWire[] = [];
	closed = false;
	private closing: Promise<void> | null = null;

	// Broadcast window.
	private windowUpdates: WindowUpdate[] = [];
	private windowCommits: LiveCommitWire[] = [];
	private readonly windowAcks = new Map<number, Array<[number, number, LiveOp[]?]>>();
	private broadcastTimer: ReturnType<typeof setTimeout> | null = null;

	// Presence.
	private readonly presencePeers = new Set<number>();
	private readonly presenceGone = new Set<number>();
	private readonly presenceCursors = new Set<number>();
	/** Recipients that skipped presence under backpressure and need the full picture. */
	private readonly presenceBehind = new Set<number>();
	private presenceTimer: ReturnType<typeof setTimeout> | null = null;

	// Text not committed yet: block id → who changed it last (null: not a person).
	private readonly dirty = new Map<string, string | null>();
	private firstDirtyAt = 0;
	private lastDirtyAt = 0;
	private flushTimer: ReturnType<typeof setTimeout> | null = null;
	/** Blocks whose text changed in the update being applied (for structural transactions). */
	private touched: Set<string> | null = null;
	/** Live blocks a participant's update left without a text, repaired after it. */
	private readonly broken = new Set<string>();

	// Persistence.
	private pendingRows: PendingRow[] = [];
	/** Blocks whose database content changed since the last sync row (see `persistStep`). */
	private readonly syncPending = new Map<string, string>();
	private persistTimer: ReturnType<typeof setTimeout> | null = null;
	private persistChain: Promise<void> = Promise.resolve();
	private logSeq = 0;
	private snapshotWritten = false;
	private rowsSinceCompact = 0;
	private bytesSinceCompact = 0;

	private idleTimer: ReturnType<typeof setTimeout> | null = null;

	/** Measurements for diagnostics and the PR's numbers. */
	readonly stats = {
		updatesIn: 0,
		updateBytesIn: 0,
		applyMs: 0,
		framesOut: 0,
		bytesOut: 0,
		mergeMs: 0,
		txCommitted: 0,
		txRejected: 0,
		flushCommits: 0,
		rowsWritten: 0,
		rowBytes: 0,
		resyncs: 0,
	};

	private constructor(
		readonly pageId: string,
		readonly spaceId: string,
		private readonly schedule: (task: () => Promise<void>) => void,
		private readonly onEmpty: (page: LivePage) => void,
	) {
		this.texts = this.doc.getMap<Y.Text>("texts");
		this.mirror = new PageMirror(pageId);
	}

	/**
	 * Open a session: read the page (it must be small enough) and build the document. Runs in
	 * the space's lane. `schedule` queues a task on that lane; `onEmpty` is told when the last
	 * participant left and the idle period passed.
	 */
	static async open(
		pageId: string,
		spaceId: string,
		schedule: (task: () => Promise<void>) => void,
		onEmpty: (page: LivePage) => void,
	): Promise<LivePage | Refusal> {
		const { blocks, truncated } = await outlineStore.listPageBlocks(
			pageId,
			OUTLINE_LIVE_LIMITS.maxPageBlocks,
		);
		if (truncated) return { refused: "too_large" };
		const page = blocks.find((b) => b.id === pageId);
		if (!page || page.parentId !== null || page.spaceId !== spaceId)
			return { refused: "access_denied" };
		let bytes = 0;
		for (const b of blocks) bytes += bytesOf(b.content);
		if (bytes > OUTLINE_LIVE_LIMITS.maxPageBytes) return { refused: "too_large" };
		const session = new LivePage(pageId, spaceId, schedule, onEmpty);
		for (const b of blocks) {
			session.mirror.put({
				id: b.id,
				sid: b.sid,
				parentId: b.parentId,
				orderKey: b.orderKey,
				taskJson: b.taskJson,
				deleted: false,
				dbContent: b.content,
			});
		}
		const ids = blocks.map((b) => b.id).sort();
		session.doc.transact(() => {
			for (const id of ids) {
				session.texts.set(id, new Y.Text(session.mirror.get(id)?.dbContent ?? ""));
			}
		}, SERVER_ORIGIN);
		// The initial texts are what the database holds: nothing to persist or broadcast yet.
		session.windowUpdates = [];
		session.pendingRows = [];
		session.watch();
		return session;
	}

	private watch(): void {
		this.doc.on("update", (update: Uint8Array, origin: unknown) => {
			if (this.closed) return;
			const from = origin instanceof Participant ? origin.pno : 0;
			this.windowUpdates.push({ from, update });
			this.scheduleBroadcast();
			this.pendingRows.push(
				origin instanceof Participant
					? { actorKind: "human", actorId: origin.userId, update }
					: { actorKind: "system", actorId: LIVE_LOG_ACTOR, update },
			);
			this.schedulePersist();
		});
		this.texts.observeDeep((events, txn) => {
			const person = txn.origin instanceof Participant ? txn.origin : null;
			for (const ev of events) {
				if (ev.target === this.texts) {
					// Only structural transactions create texts; anything else touching the map
					// leaves a live block without its text, which is repaired from the database.
					for (const key of (ev as Y.YMapEvent<Y.Text>).keysChanged) {
						if (!person) continue;
						const t = this.texts.get(key);
						if (!(t instanceof Y.Text) && this.mirror.alive(key)) this.broken.add(key);
						this.touched?.add(key);
					}
					continue;
				}
				if (!(ev.target instanceof Y.Text)) continue;
				const id = (ev.target as unknown as { _item?: { parentSub?: string | null } })._item
					?.parentSub;
				if (!id) continue;
				this.touched?.add(id);
				if (person) this.markDirty(id, person.userId);
			}
		});
	}

	// ─────────────────────────────────────────────────────────────────────────
	// Reading
	// ─────────────────────────────────────────────────────────────────────────

	private ytext(id: string): Y.Text | null {
		const t = this.texts.get(id);
		return t instanceof Y.Text ? t : null;
	}

	/** A block's live text (its database content when it has no text in the session). */
	textOf(id: string): string {
		return this.ytext(id)?.toString() ?? this.mirror.get(id)?.dbContent ?? "";
	}

	get editors(): number {
		let n = 0;
		for (const p of this.participants.values()) if (p.canWrite) n++;
		return n;
	}

	get viewers(): number {
		return this.participants.size - this.editors;
	}

	/** Nothing waits to be broadcast (tests). */
	get quiet(): boolean {
		return (
			this.broadcastTimer === null &&
			this.windowUpdates.length === 0 &&
			this.windowCommits.length === 0
		);
	}

	/**
	 * Stop without committing or closing anything, as if the process had died here (tests of
	 * the replay). The log rows stay.
	 */
	abandon(): void {
		this.closed = true;
		for (const timer of [
			this.broadcastTimer,
			this.presenceTimer,
			this.flushTimer,
			this.persistTimer,
			this.idleTimer,
		]) {
			if (timer) clearTimeout(timer);
		}
		this.participants.clear();
		this.doc.destroy();
	}

	/** Whether any text is waiting to be committed. */
	get hasUnflushed(): boolean {
		return this.dirty.size > 0;
	}

	// ─────────────────────────────────────────────────────────────────────────
	// Participants
	// ─────────────────────────────────────────────────────────────────────────

	/**
	 * Add a participant and answer its join: a snapshot (reset), or the difference to what it
	 * has when it resumes this same session.
	 */
	join(
		socket: LiveSocket,
		handle: number,
		head: LiveJoinHead,
		stateVector: Uint8Array,
		canWrite: boolean,
	): Participant | Refusal {
		if (this.closed) return { refused: "ended" };
		if (
			canWrite
				? this.editors >= OUTLINE_LIVE_LIMITS.editorsPerPage
				: this.viewers >= OUTLINE_LIVE_LIMITS.viewersPerPage
		)
			return { refused: "unavailable" };
		this.cancelIdle();
		const p = new Participant(
			this.nextPno++,
			socket,
			handle,
			head.key,
			head.mode === "edit",
			canWrite,
		);
		const resume = head.resume;
		let joined: LiveJoinedHead;
		let body: Uint8Array;
		const base = {
			epoch: this.epoch,
			w: canWrite,
			op: this.opSeq,
			me: p.pno,
			peers: [...this.participants.values()].map((x) => x.peer()),
			cur: [...this.participants.values()]
				.filter((x) => x.sel !== null)
				.map((x) => [x.pno, x.sel] as [number, LiveSelWire | null]),
		};
		let diff: Uint8Array | null = null;
		if (resume && resume.epoch === this.epoch && stateVector.length > 0) {
			try {
				diff = Y.encodeStateAsUpdate(this.doc, stateVector);
			} catch {
				diff = null;
			}
		}
		if (resume && diff) {
			const covered =
				resume.op === this.opSeq ||
				(this.opLog.length > 0 && (this.opLog[0] as LiveCommitWire).o <= resume.op + 1);
			joined = {
				...base,
				mode: "resume",
				tx: this.keys.get(head.key) ?? 0,
				...(covered
					? { ops: this.opLog.filter((c) => c.o > resume.op) }
					: { blocks: this.mirror.snapshot() }),
			};
			body = diff;
		} else {
			joined = { ...base, mode: "reset", tx: 0, blocks: this.mirror.snapshot() };
			body = Y.encodeStateAsUpdate(this.doc);
		}
		p.lastTx = joined.tx;
		p.ackedTx = joined.tx;
		p.windowStart = this.windowUpdates.length;
		p.commitStart = this.windowCommits.length;
		this.participants.set(p.pno, p);
		this.sendTo(
			p,
			encodeLiveFrame({ kind: LIVE_FRAME.joined, handle, head: joined, body }),
			"control",
		);
		this.presencePeers.add(p.pno);
		this.schedulePresence();
		return p;
	}

	leave(p: Participant): void {
		if (this.participants.get(p.pno) !== p) return;
		this.participants.delete(p.pno);
		this.windowAcks.delete(p.pno);
		this.presencePeers.delete(p.pno);
		this.presenceCursors.delete(p.pno);
		this.presenceBehind.delete(p.pno);
		this.presenceGone.add(p.pno);
		this.schedulePresence();
		if (this.participants.size === 0) this.armIdle();
	}

	/** Change a participant's write access (an access change re-checked it). */
	setWrite(p: Participant, canWrite: boolean): void {
		if (p.canWrite === canWrite) return;
		p.canWrite = canWrite;
		this.sendTo(
			p,
			encodeLiveFrame({ kind: LIVE_FRAME.mode, handle: p.handle, head: { w: canWrite } }),
			"control",
		);
		this.presencePeers.add(p.pno);
		this.schedulePresence();
	}

	/** Tell a participant the session ended for it (it is removed). */
	dismiss(p: Participant, code: LiveCloseCode): void {
		this.sendTo(
			p,
			encodeLiveFrame({ kind: LIVE_FRAME.closed, handle: p.handle, head: { code } }),
			"control",
		);
		this.leave(p);
	}

	private cancelIdle(): void {
		if (this.idleTimer) {
			clearTimeout(this.idleTimer);
			this.idleTimer = null;
		}
	}

	private armIdle(): void {
		this.cancelIdle();
		this.idleTimer = setTimeout(() => {
			this.idleTimer = null;
			if (this.participants.size === 0 && !this.closed) this.onEmpty(this);
		}, OUTLINE_LIVE_TIMING.idleCloseMs);
		this.idleTimer.unref?.();
	}

	/**
	 * Called when a participant's socket drained: one that fell behind is told to rejoin, and
	 * gets exactly what it lacks from its state vector. Its own frames are still taken meanwhile.
	 */
	onDrain(p: Participant): void {
		if (!p.lagging || p.resyncSent || this.participants.get(p.pno) !== p) return;
		if (p.socket.bufferedAmount() > OUTLINE_LIVE_BOUNDS.resumeBytes) return;
		p.resyncSent = true;
		this.stats.resyncs++;
		p.socket.send(encodeLiveFrame({ kind: LIVE_FRAME.resync, handle: p.handle }));
	}

	// ─────────────────────────────────────────────────────────────────────────
	// Sending
	// ─────────────────────────────────────────────────────────────────────────

	/**
	 * Send one frame. `presence` frames are skipped under mild backpressure (kept to be sent
	 * later); any frame is held back from a socket buffering past {@link OUTLINE_LIVE_BOUNDS.
	 * lagBytes}, which is then told to rejoin once it drains: dropping a text update silently
	 * would leave that client diverged.
	 */
	private sendTo(
		p: Participant,
		frame: Uint8Array,
		kind: "data" | "presence" | "control",
	): boolean {
		if (p.lagging) return false;
		const buffered = p.socket.bufferedAmount();
		if (kind === "presence" && buffered > OUTLINE_LIVE_BOUNDS.presenceDropBytes) return false;
		if (kind === "data" && buffered > OUTLINE_LIVE_BOUNDS.lagBytes) {
			p.lagging = true;
			return false;
		}
		if (!p.socket.send(frame)) {
			p.lagging = true;
			return false;
		}
		this.stats.framesOut++;
		this.stats.bytesOut += frame.length;
		return true;
	}

	private scheduleBroadcast(): void {
		if (this.broadcastTimer || this.closed) return;
		this.broadcastTimer = setTimeout(() => {
			this.broadcastTimer = null;
			this.flushBroadcast();
		}, OUTLINE_LIVE_TIMING.broadcastMs);
		this.broadcastTimer.unref?.();
	}

	/** Send everyone what the window collected for them (see the module header). */
	flushBroadcast(): void {
		if (this.broadcastTimer) {
			clearTimeout(this.broadcastTimer);
			this.broadcastTimer = null;
		}
		const updates = this.windowUpdates;
		const commits = this.windowCommits;
		this.windowUpdates = [];
		this.windowCommits = [];
		const t0 = performance.now();
		const merged = new Map<string, Uint8Array>();
		const mergedFor = (p: Participant): Uint8Array | null => {
			const share = updates.slice(p.windowStart).filter((u) => u.from !== p.pno);
			if (share.length === 0) return null;
			if (share.length === 1) return (share[0] as WindowUpdate).update;
			// Recipients that sent nothing in the window share one merge.
			const key = p.windowStart === 0 && share.length === updates.length ? "all" : `${p.pno}`;
			let out = merged.get(key);
			if (!out) {
				out = Y.mergeUpdates(share.map((u) => u.update));
				merged.set(key, out);
			}
			return out;
		};
		for (const p of this.participants.values()) {
			const body = mergedFor(p);
			const own = commits.slice(p.commitStart).filter((c) => c.by !== p.pno);
			const acks = this.windowAcks.get(p.pno);
			const ack = p.lastTx > p.ackedTx ? p.lastTx : 0;
			p.windowStart = 0;
			p.commitStart = 0;
			if (!body && own.length === 0 && !acks && ack === 0) continue;
			const head: LiveBatchHead = {};
			if (own.length > 0) head.c = own;
			if (acks) head.a = acks;
			const sent = this.sendTo(
				p,
				encodeLiveFrame({
					kind: LIVE_FRAME.batch,
					handle: p.handle,
					n: ack,
					...(head.c || head.a ? { head } : {}),
					...(body ? { body } : {}),
				}),
				"data",
			);
			if (sent) {
				p.ackedTx = p.lastTx;
				this.windowAcks.delete(p.pno);
			}
		}
		this.windowAcks.clear();
		this.stats.mergeMs += performance.now() - t0;
	}

	// ─────────────────────────────────────────────────────────────────────────
	// Presence (ephemeral: never persisted, coalesced last-wins, dropped first)
	// ─────────────────────────────────────────────────────────────────────────

	setSelection(p: Participant, sel: LiveSelWire | null): void {
		if (this.participants.get(p.pno) !== p) return;
		p.sel = validSelection(sel);
		this.presenceCursors.add(p.pno);
		this.schedulePresence();
	}

	private schedulePresence(): void {
		if (this.presenceTimer || this.closed) return;
		this.presenceTimer = setTimeout(() => {
			this.presenceTimer = null;
			this.flushPresence();
		}, OUTLINE_LIVE_TIMING.presenceMs);
		this.presenceTimer.unref?.();
	}

	private flushPresence(): void {
		const peers = [...this.presencePeers].map((n) => this.participants.get(n)).filter((p) => !!p);
		const gone = [...this.presenceGone];
		const cursors = [...this.presenceCursors]
			.map((n) => this.participants.get(n))
			.filter((p): p is Participant => !!p);
		this.presencePeers.clear();
		this.presenceGone.clear();
		this.presenceCursors.clear();
		for (const r of this.participants.values()) {
			const behind = this.presenceBehind.has(r.pno);
			const others = [...this.participants.values()].filter((p) => p !== r);
			const head: LivePresenceDown = {};
			const peerList = behind ? others : peers.filter((p) => p !== r);
			if (peerList.length) head.peers = peerList.map((p) => p.peer());
			if (gone.length && !behind) head.gone = gone;
			const cur = (behind ? others : cursors.filter((p) => p !== r)).filter(
				(p) => !behind || p.sel !== null,
			);
			if (cur.length) head.cur = cur.map((p) => [p.pno, p.sel]);
			if (!head.peers && !head.gone && !head.cur) continue;
			const sent = this.sendTo(
				r,
				encodeLiveFrame({ kind: LIVE_FRAME.presence, handle: r.handle, head }),
				"presence",
			);
			// Skipped under backpressure: send the whole picture once it can take it.
			if (sent) this.presenceBehind.delete(r.pno);
			else this.presenceBehind.add(r.pno);
		}
		if (this.presenceBehind.size > 0) this.schedulePresence();
	}

	// ─────────────────────────────────────────────────────────────────────────
	// Text updates
	// ─────────────────────────────────────────────────────────────────────────

	private reject(p: Participant, n: number, code: LiveRejectCode): void {
		this.stats.txRejected++;
		p.blocked = true;
		// Everything up to the refused frame is acknowledged first, in order.
		this.flushBroadcast();
		this.sendTo(
			p,
			encodeLiveFrame({ kind: LIVE_FRAME.reject, handle: p.handle, n, head: { code } }),
			"control",
		);
	}

	/** Apply a participant's update, reporting whether it integrated cleanly. */
	private applyUpdate(p: Participant, update: Uint8Array): boolean {
		const store = this.doc.store as unknown as {
			pendingStructs: unknown;
			pendingDs: unknown;
		};
		const t0 = performance.now();
		try {
			Y.applyUpdate(this.doc, update, p);
		} catch {
			return false;
		} finally {
			this.stats.applyMs += performance.now() - t0;
		}
		this.stats.updatesIn++;
		this.stats.updateBytesIn += update.length;
		if (store.pendingStructs || store.pendingDs) {
			// The update depends on changes this session never had (a client out of step): it
			// would sit in the document forever. Drop it and have the client rebuild.
			store.pendingStructs = null;
			store.pendingDs = null;
			return false;
		}
		this.repairBroken();
		return true;
	}

	/** Live blocks whose text a participant's update removed get it back from the database. */
	private repairBroken(): void {
		if (this.broken.size === 0) return;
		const ids = [...this.broken];
		this.broken.clear();
		this.doc.transact(() => {
			for (const id of ids) {
				const b = this.mirror.alive(id);
				if (b && !(this.texts.get(id) instanceof Y.Text))
					this.texts.set(id, new Y.Text(b.dbContent));
			}
		}, SERVER_ORIGIN);
	}

	/** A text-only update (`update` frame). */
	handleUpdate(p: Participant, n: number, update: Uint8Array): void {
		if (this.participants.get(p.pno) !== p || p.blocked || this.closed) return;
		if (n <= p.lastTx) return;
		if (!p.canWrite) {
			this.reject(p, n, "read_only");
			return;
		}
		if (!this.applyUpdate(p, update)) {
			this.reject(p, n, "invalid");
			return;
		}
		this.processed(p, n);
	}

	private processed(p: Participant, n: number): void {
		p.lastTx = n;
		this.keys.delete(p.key);
		this.keys.set(p.key, n);
		while (this.keys.size > OUTLINE_LIVE_BOUNDS.clientKeys) {
			const oldest = this.keys.keys().next().value;
			if (oldest === undefined) break;
			this.keys.delete(oldest);
		}
		this.scheduleBroadcast();
	}

	private markDirty(id: string, userId: string | null): void {
		this.dirty.set(id, userId);
		const now = Date.now();
		if (this.firstDirtyAt === 0) this.firstDirtyAt = now;
		this.lastDirtyAt = now;
		this.scheduleFlush();
	}

	private scheduleFlush(delayMs?: number): void {
		if (this.closed) return;
		if (this.flushTimer) clearTimeout(this.flushTimer);
		const due =
			delayMs !== undefined
				? Date.now() + delayMs
				: Math.min(
						this.lastDirtyAt + OUTLINE_LIVE_TIMING.flushIdleMs,
						this.firstDirtyAt + OUTLINE_LIVE_TIMING.flushMaxMs,
					);
		this.flushTimer = setTimeout(
			() => {
				this.flushTimer = null;
				this.schedule(async () => {
					await this.flushContent();
				});
			},
			Math.max(0, due - Date.now()),
		);
		this.flushTimer.unref?.();
	}

	// ─────────────────────────────────────────────────────────────────────────
	// Committing text
	// ─────────────────────────────────────────────────────────────────────────

	/**
	 * The live texts of `ids`, cut to the per-block size limit and normalized (off the main
	 * thread when large). Part 0 is the block's own content; further parts become blocks after it.
	 */
	private async planContents(ids: readonly string[]): Promise<ContentPlan[]> {
		const cut = ids.map((id) => ({ id, text: this.textOf(id), parts: fitBody(this.textOf(id)) }));
		const normalized = await normalizeLiveTexts(cut.flatMap((c) => c.parts));
		let at = 0;
		return cut.map((c) => {
			const parts = normalized.slice(at, at + c.parts.length);
			at += c.parts.length;
			return { id: c.id, text: c.text, parts };
		});
	}

	/** Commit the texts of `ids` as `userId`'s commit(s). Returns false when that failed. */
	private async commitTexts(ids: string[], userId: string | null): Promise<boolean> {
		const live = ids.filter((id) => this.mirror.alive(id));
		if (live.length === 0) return true;
		let plans: ContentPlan[];
		try {
			plans = await this.planContents(live);
		} catch (error) {
			logger.warn("Outline live text could not be normalized", { error: String(error) });
			return false;
		}
		const ops: OutlineOp[] = [];
		const canonical: Array<OutlineCanonicalContent | undefined> = [];
		for (const plan of plans) {
			const b = this.mirror.get(plan.id) as MirrorBlock;
			contentOps(plan, b.dbContent, b.parentId, ops, canonical);
		}
		if (ops.length > 0) {
			const actor = userId
				? { kind: "human" as const, id: userId }
				: { kind: "system" as const, id: "outline-live" };
			let results: OutlineWriteResult[];
			try {
				results = await this.writeChunks(ops, canonical, actor, userId, "live edit");
			} catch {
				return false;
			}
			this.stats.flushCommits += results.filter((r) => r.commit).length;
			this.settleContents(plans);
			if (plans.some(reshapes)) await this.applyOwnResults(results);
		}
		return true;
	}

	/** After committing contents: the mirror holds them, the live texts follow normalization. */
	private settleContents(plans: readonly ContentPlan[]): void {
		for (const plan of plans) {
			const first = plan.parts[0] as LiveNormalized;
			this.setDbContent(plan.id, first.content);
			// What was committed differs from the session's text (normalized, or cut at the size
			// limit): bring the text to it, keeping anything typed since.
			if (first.content !== plan.text) this.mergeText(plan.id, plan.text, first.content);
		}
	}

	/** Own commits that created blocks the session did not ask for (split content). */
	private async applyOwnResults(results: readonly OutlineWriteResult[]): Promise<void> {
		for (const r of results) {
			if (!r.commit) continue;
			await this.applyCommits([
				{
					spaceId: this.spaceId,
					commitId: r.commit.id,
					seq: r.commit.seq,
					pageIds: r.pageIds,
					actorKind: "system",
					actorId: "outline-live",
				},
			]);
		}
	}

	/**
	 * Write operations as one or more commits (the write path's per-transaction limits), as the
	 * session's own writes. Throws the write path's error; earlier chunks stay committed.
	 */
	private async writeChunks(
		ops: OutlineOp[],
		canonical: Array<OutlineCanonicalContent | undefined>,
		actor: { kind: "human" | "system"; id: string },
		responsible: string | null,
		summary: string,
	): Promise<OutlineWriteResult[]> {
		const out: OutlineWriteResult[] = [];
		let start = 0;
		while (start < ops.length) {
			let end = start;
			let bytes = 0;
			while (end < ops.length && end - start < OUTLINE_LIMITS.maxOps) {
				const op = ops[end] as OutlineOp;
				const size = "content" in op && typeof op.content === "string" ? bytesOf(op.content) : 0;
				// A chain of temp ids (split content) stays in one transaction.
				if (end > start && bytes + size > OUTLINE_LIMITS.maxTransactionBodyBytes && !namesTemp(op))
					break;
				bytes += size;
				end++;
			}
			const chunk = ops.slice(start, end);
			try {
				out.push(
					await asOwnLiveWrite(() =>
						writeOutline(
							{
								spaceId: this.spaceId,
								ops: chunk,
								actor,
								responsibleUserId: responsible,
								...(summary ? { summary } : {}),
							},
							{ canonical: canonical.slice(start, end) },
						),
					),
				);
			} catch (error) {
				logger.warn("Outline live commit failed", {
					pageId: this.pageId,
					error: String(error),
					code: (error as { code?: string }).code,
				});
				throw error;
			}
			start = end;
		}
		return out;
	}

	/** Record what the database holds for a block now (logged for replay, see `persistStep`). */
	private setDbContent(id: string, content: string): void {
		const b = this.mirror.get(id);
		if (!b || b.dbContent === content) return;
		this.mirror.update(id, { dbContent: content });
		this.syncPending.set(id, content);
		this.schedulePersist();
	}

	/** Bring a block's live text from `from` to `to`, keeping changes made since `from`. */
	private mergeText(id: string, from: string, to: string): void {
		const t = this.ytext(id);
		if (!t) return;
		const current = t.toString();
		const r = merge3(from, to, current, { lenient: true });
		if (!r.ok || r.result === current) return;
		this.doc.transact(() => {
			for (const h of [...r.patch].sort((a, b) => b.start - a.start)) {
				if (h.end > h.start) t.delete(h.start, h.end - h.start);
				if (h.text) t.insert(h.start, h.text);
			}
		}, SERVER_ORIGIN);
	}

	/**
	 * Commit every text not committed yet (each person's blocks as their own commit). Runs in
	 * the lane: on the timer, before a write from outside, and when the session closes.
	 */
	async flushContent(): Promise<boolean> {
		if (this.flushTimer) {
			clearTimeout(this.flushTimer);
			this.flushTimer = null;
		}
		if (this.dirty.size === 0) return true;
		const byUser = new Map<string | null, string[]>();
		for (const [id, user] of this.dirty) {
			const list = byUser.get(user);
			if (list) list.push(id);
			else byUser.set(user, [id]);
		}
		this.dirty.clear();
		this.firstDirtyAt = 0;
		let ok = true;
		for (const [user, ids] of byUser) {
			if (await this.commitTexts(ids, user)) continue;
			ok = false;
			for (const id of ids) if (!this.dirty.has(id)) this.dirty.set(id, user);
		}
		if (!ok) {
			this.firstDirtyAt = Date.now();
			this.scheduleFlush(OUTLINE_LIVE_TIMING.flushRetryMs);
		}
		return ok;
	}

	// ─────────────────────────────────────────────────────────────────────────
	// Structural transactions
	// ─────────────────────────────────────────────────────────────────────────

	/** A structural transaction (`tx` frame): check, apply, commit, broadcast or refuse. */
	async handleTx(p: Participant, n: number, rawHead: unknown, update: Uint8Array): Promise<void> {
		if (this.participants.get(p.pno) !== p || p.blocked || this.closed) return;
		if (n <= p.lastTx) return;
		if (!p.canWrite) {
			this.reject(p, n, "read_only");
			return;
		}
		const ops = parseOps(rawHead);
		if (!ops) {
			this.reject(p, n, "invalid");
			return;
		}
		const plan = this.planTx(ops);
		if (!plan) {
			this.reject(p, n, "conflict");
			return;
		}
		// Text of blocks this transaction deletes is committed first, so their history keeps it.
		const doomed = plan.deleted.filter((id) => this.dirty.has(id));
		if (doomed.length > 0) {
			const byUser = new Map<string | null, string[]>();
			for (const id of doomed) {
				const user = this.dirty.get(id) ?? null;
				this.dirty.delete(id);
				byUser.set(user, [...(byUser.get(user) ?? []), id]);
			}
			for (const [user, ids] of byUser) await this.commitTexts(ids, user);
		}

		// The update goes in first (it creates the new blocks' texts) and is undone when the
		// commit fails, so a refused transaction leaves the session as it was.
		const undo = new Y.UndoManager(this.texts, {
			trackedOrigins: new Set([p]),
			captureTimeout: 0,
		});
		const refuse = (code: LiveRejectCode) => {
			undo.undo();
			undo.destroy();
			this.reject(p, n, code);
		};
		this.touched = new Set();
		let touched: Set<string>;
		try {
			const integrated = update.length === 0 || this.applyUpdate(p, update);
			touched = this.touched;
			if (!integrated) {
				refuse("invalid");
				return;
			}
		} finally {
			this.touched = null;
		}
		const inserted = new Map<string, Extract<LiveOp, { t: "i" }>>();
		for (const op of plan.ops) if (op.t === "i") inserted.set(op.id, op);
		const deleted = new Set(plan.deleted);
		const updated = [...touched].filter(
			(id) => !inserted.has(id) && !deleted.has(id) && this.mirror.alive(id),
		);
		let contents: ContentPlan[];
		try {
			contents = await this.planContents([...inserted.keys(), ...updated]);
		} catch {
			refuse("failed");
			return;
		}
		const byId = new Map(contents.map((c) => [c.id, c]));
		const { ops: writeOps, canonical, opOf } = this.writeOpsOf(plan.ops, byId, inserted);
		let results: OutlineWriteResult[] = [];
		if (writeOps.length > 0) {
			try {
				results = await this.writeChunks(
					writeOps,
					canonical,
					{ kind: "human", id: p.userId },
					p.userId,
					"",
				);
			} catch (error) {
				refuse(isConflict(error) ? "conflict" : "failed");
				return;
			}
		}
		undo.destroy();
		this.stats.txCommitted++;
		// The texts this commit wrote are settled, whoever typed them.
		for (const c of contents) this.dirty.delete(c.id);

		// The mirror follows what was written; the broadcast carries the canonical form.
		const written = results.flatMap((r) => r.results);
		const canonicalOps: LiveOp[] = [];
		for (const op of plan.ops) {
			const at = opOf.get(op);
			const r = at === undefined ? undefined : written[at];
			const moved = r?.adjustments?.find((x) => x.kind === "order-key-moved");
			const key =
				moved?.kind === "order-key-moved" ? moved.got : op.t === "i" || op.t === "m" ? op.k : "";
			switch (op.t) {
				case "i": {
					const prior = this.mirror.get(op.id);
					const taskJson: OutlineTaskJson | null =
						op.task === undefined
							? prior?.deleted
								? prior.taskJson
								: null
							: op.task
								? { status: LIVE_STATUS_OF_TASK[op.task], assignee: null }
								: null;
					this.mirror.put({
						id: op.id,
						sid: r?.sid ?? prior?.sid ?? "",
						parentId: op.p,
						orderKey: key,
						taskJson,
						deleted: false,
						dbContent: prior?.dbContent ?? "",
					});
					canonicalOps.push({ ...op, k: key });
					break;
				}
				case "m":
					this.mirror.update(op.id, { parentId: op.p, orderKey: key });
					canonicalOps.push({ ...op, k: key });
					break;
				case "d":
					for (const id of this.mirror.subtree(op.id)) {
						this.mirror.update(id, { deleted: true });
						canonicalOps.push({ t: "d", id });
					}
					break;
				case "k": {
					const b = this.mirror.get(op.id);
					this.mirror.update(op.id, {
						taskJson: op.task
							? { ...(b?.taskJson ?? { assignee: null }), status: LIVE_STATUS_OF_TASK[op.task] }
							: null,
					});
					canonicalOps.push(op);
					break;
				}
			}
		}
		this.settleContents(contents);
		this.processed(p, n);
		if (canonicalOps.length > 0) {
			const commit: LiveCommitWire = { o: ++this.opSeq, by: p.pno, ops: canonicalOps };
			this.recordCommit(commit);
			const acks = this.windowAcks.get(p.pno) ?? [];
			acks.push(sameOps(canonicalOps, ops) ? [n, commit.o] : [n, commit.o, canonicalOps]);
			this.windowAcks.set(p.pno, acks);
		}
		if (contents.some(reshapes)) await this.applyOwnResults(results);
		this.scheduleBroadcast();
	}

	private recordCommit(commit: LiveCommitWire): void {
		this.windowCommits.push(commit);
		this.opLog.push(commit);
		while (this.opLog.length > OUTLINE_LIVE_BOUNDS.opLog) this.opLog.shift();
		this.scheduleBroadcast();
	}

	/**
	 * Check a transaction against the mirror, as if applied in order. Returns the operations to
	 * write (deletes of blocks inside a block deleted by the same transaction dropped) and the
	 * blocks it deletes, or null when it does not fit the current structure.
	 */
	private planTx(ops: LiveOp[]): { ops: LiveOp[]; deleted: string[] } | null {
		const overlay = new Map<string, { parentId: string | null; deleted: boolean }>();
		const parentOf = (id: string): string | null | undefined =>
			overlay.has(id) ? overlay.get(id)?.parentId : this.mirror.get(id)?.parentId;
		const known = (id: string): boolean => overlay.has(id) || this.mirror.get(id) !== undefined;
		const alive = (id: string): boolean => {
			let cur: string | null | undefined = id;
			for (let guard = 0; cur && guard < 10_000; guard++) {
				const deleted = overlay.has(cur)
					? overlay.get(cur)?.deleted
					: this.mirror.get(cur)?.deleted;
				if (deleted !== false) return false;
				if (cur === this.pageId) return true;
				cur = parentOf(cur);
			}
			return false;
		};
		const isWithin = (candidate: string, root: string): boolean => {
			let cur: string | null | undefined = candidate;
			for (let guard = 0; cur && guard < 10_000; guard++) {
				if (cur === root) return true;
				cur = parentOf(cur);
			}
			return false;
		};
		const out: LiveOp[] = [];
		const deleted: string[] = [];
		for (const op of ops) {
			switch (op.t) {
				case "i": {
					if (!CLIENT_ID_RE.test(op.id) || !alive(op.p)) return null;
					if (known(op.id) && alive(op.id)) return null;
					overlay.set(op.id, { parentId: op.p, deleted: false });
					out.push(op);
					break;
				}
				case "m": {
					if (op.id === this.pageId || !alive(op.id) || !alive(op.p)) return null;
					if (isWithin(op.p, op.id)) return null;
					overlay.set(op.id, { parentId: op.p, deleted: false });
					out.push(op);
					break;
				}
				case "d": {
					if (op.id === this.pageId) return null;
					if (!known(op.id)) return null;
					// Inside a block this transaction already deleted: gone with it.
					if (!alive(op.id)) {
						if (deleted.some((d) => isWithin(op.id, d))) break;
						return null;
					}
					overlay.set(op.id, { parentId: parentOf(op.id) ?? null, deleted: true });
					deleted.push(op.id);
					out.push(op);
					break;
				}
				case "k": {
					if (!alive(op.id) || op.id === this.pageId) return null;
					out.push(op);
					break;
				}
			}
		}
		// Every block a delete takes, for the text committed beforehand.
		const all = new Set<string>();
		for (const d of deleted) {
			if (this.mirror.get(d)) for (const id of this.mirror.subtree(d)) all.add(id);
			else all.add(d);
		}
		return { ops: out, deleted: [...all] };
	}

	/** The write path's operations for a checked transaction, and where each of its ops went. */
	private writeOpsOf(
		ops: readonly LiveOp[],
		contents: ReadonlyMap<string, ContentPlan>,
		inserted: ReadonlyMap<string, Extract<LiveOp, { t: "i" }>>,
	): {
		ops: OutlineOp[];
		canonical: Array<OutlineCanonicalContent | undefined>;
		opOf: Map<LiveOp, number>;
	} {
		const out: OutlineOp[] = [];
		const canonical: Array<OutlineCanonicalContent | undefined> = [];
		const opOf = new Map<LiveOp, number>();
		for (const op of ops) {
			opOf.set(op, out.length);
			switch (op.t) {
				case "i": {
					const first = contents.get(op.id)?.parts[0] as LiveNormalized;
					const task = op.task ? { status: LIVE_STATUS_OF_TASK[op.task] } : null;
					if (this.mirror.get(op.id)?.deleted) {
						out.push({
							op: "restore",
							block: { id: op.id },
							parent: { id: op.p },
							position: { orderKey: op.k },
							content: first.content,
							...(op.task !== undefined ? { task } : {}),
						});
					} else {
						out.push({
							op: "insert",
							id: op.id,
							parent: { id: op.p },
							position: { orderKey: op.k },
							content: first.content,
							task,
						});
					}
					canonical.push({ content: first.content, links: first.links });
					break;
				}
				case "m":
					out.push({
						op: "move",
						block: { id: op.id },
						parent: { id: op.p },
						position: { orderKey: op.k },
					});
					canonical.push(undefined);
					break;
				case "d":
					out.push({ op: "delete", block: { id: op.id } });
					canonical.push(undefined);
					break;
				case "k":
					out.push({
						op: "setTask",
						block: { id: op.id },
						task: op.task ? { status: LIVE_STATUS_OF_TASK[op.task] } : null,
					});
					canonical.push(undefined);
					break;
			}
		}
		// Text the same keystroke changed in existing blocks (the first half of a split block),
		// split-off structure and parts past the size limit.
		for (const plan of contents.values()) {
			const insert = inserted.get(plan.id);
			if (insert) contentOps(plan, null, insert.p, out, canonical);
			else {
				const b = this.mirror.get(plan.id) as MirrorBlock;
				contentOps(plan, b.dbContent, b.parentId, out, canonical);
			}
		}
		return { ops: out, canonical, opOf };
	}

	// ─────────────────────────────────────────────────────────────────────────
	// Commits from outside the session
	// ─────────────────────────────────────────────────────────────────────────

	/**
	 * Push commits made outside the session (an agent's, a revert, split blocks) into it: read
	 * the block versions each commit wrote under the pages it touched, update the mirror,
	 * merge changed content into the live texts, and broadcast the structural part.
	 */
	async applyCommits(commits: readonly CommittedEvent[]): Promise<void> {
		for (const commit of commits) {
			if (this.closed || !commit.pageIds.includes(this.pageId)) continue;
			const versions: OutlineVersionRow[] = [];
			for (const pageId of commit.pageIds) {
				const rows = await outlineStore.listPageVersions(pageId, {
					afterSeq: commit.seq - 1,
					limit: OUTLINE_LIMITS.maxRowsTouched + 1,
				});
				for (const v of rows) if (v.fromSeq === commit.seq) versions.push(v);
			}
			await this.applyVersions(versions, actorOf(commit));
		}
	}

	private async applyVersions(versions: OutlineVersionRow[], by: LiveActor): Promise<void> {
		const inserts: LiveOp[] = [];
		const moves: LiveOp[] = [];
		const tasks: LiveOp[] = [];
		const deletes: LiveOp[] = [];
		const created: OutlineVersionRow[] = [];
		for (const v of versions) {
			const m = this.mirror.get(v.blockId);
			const inPage = !v.deleted && v.pageId === this.pageId;
			if (inPage && (!m || m.deleted)) {
				created.push(v);
				continue;
			}
			if (!m || m.deleted) continue;
			if (!inPage) {
				this.mirror.update(v.blockId, { deleted: true });
				deletes.push({ t: "d", id: v.blockId });
				continue;
			}
			if (m.parentId !== v.parentId || m.orderKey !== v.orderKey) {
				this.mirror.update(v.blockId, { parentId: v.parentId, orderKey: v.orderKey });
				if (v.parentId !== null)
					moves.push({ t: "m", id: v.blockId, p: v.parentId, k: v.orderKey });
			}
			const task = taskCharOf(v.taskJson);
			if (task !== taskCharOf(m.taskJson)) tasks.push({ t: "k", id: v.blockId, task });
			if (JSON.stringify(v.taskJson) !== JSON.stringify(m.taskJson))
				this.mirror.update(v.blockId, { taskJson: v.taskJson });
			if (v.content !== null && v.content !== m.dbContent) {
				this.mergeText(v.blockId, m.dbContent, v.content);
				this.setDbContent(v.blockId, v.content);
			}
		}
		if (created.length > 0) {
			const sids = new Map(
				(
					await outlineStore.getBlockMetas(this.spaceId, { ids: created.map((v) => v.blockId) })
				).map((m) => [m.id, m.sid]),
			);
			this.doc.transact(() => {
				for (const v of created) {
					const prior = this.mirror.get(v.blockId);
					const content = v.content ?? "";
					this.mirror.put({
						id: v.blockId,
						sid: sids.get(v.blockId) ?? prior?.sid ?? "",
						parentId: v.parentId,
						orderKey: v.orderKey,
						taskJson: v.taskJson,
						deleted: false,
						dbContent: content,
					});
					this.syncPending.set(v.blockId, content);
					const t = this.ytext(v.blockId);
					if (!t) this.texts.set(v.blockId, new Y.Text(content));
					else if (t.toString() !== content) {
						t.delete(0, t.length);
						t.insert(0, content);
					}
				}
			}, SERVER_ORIGIN);
			// Parents before children.
			const ordered = [...created].sort(
				(a, b) =>
					this.mirror.depth(a.blockId) - this.mirror.depth(b.blockId) ||
					compareOrderKeys(a.orderKey, b.orderKey),
			);
			for (const v of ordered) {
				if (v.parentId === null) continue;
				const task = taskCharOf(v.taskJson);
				inserts.push({
					t: "i",
					id: v.blockId,
					p: v.parentId,
					k: v.orderKey,
					...(task ? { task } : {}),
				});
			}
		}
		// Blocks still in the page whose parent went: they went with it.
		const ops = [...inserts, ...moves, ...tasks, ...deletes];
		if (ops.length > 0) this.recordCommit({ o: ++this.opSeq, by, ops });
	}

	// ─────────────────────────────────────────────────────────────────────────
	// Persistence
	// ─────────────────────────────────────────────────────────────────────────

	private schedulePersist(): void {
		if (this.persistTimer || this.closed) return;
		this.persistTimer = setTimeout(() => {
			this.persistTimer = null;
			void this.persistNow();
		}, OUTLINE_LIVE_TIMING.persistMs);
		this.persistTimer.unref?.();
	}

	/** Append what arrived since the last append (a snapshot the first time). */
	persistNow(): Promise<void> {
		if (this.persistTimer) {
			clearTimeout(this.persistTimer);
			this.persistTimer = null;
		}
		this.persistChain = this.persistChain
			.then(() => this.persistStep())
			.catch((error) => {
				logger.warn("Outline live log append failed", {
					pageId: this.pageId,
					error: String(error),
				});
			});
		return this.persistChain;
	}

	/**
	 * One append. The log of an epoch is: a snapshot of the document, then increments, and
	 * "sync" rows saying what the database held for blocks whose content it changed (a commit of
	 * typed text, a write from outside). Replay merges three ways: from the last synced content,
	 * to the final text, onto what the database holds then, so a write from outside that the
	 * session had not logged yet is kept, not overwritten (`replay.ts`).
	 */
	private async persistStep(): Promise<void> {
		const pending = this.pendingRows;
		if (pending.length === 0 && this.syncPending.size === 0) return;
		this.pendingRows = [];
		const now = new Date().toISOString();
		const row = (data: Uint8Array, actorKind: "human" | "system", actorId: string) => ({
			pageBlockId: this.pageId,
			epoch: this.epoch,
			seq: this.logSeq++,
			updateB64: Buffer.from(data).toString("base64"),
			actorKind,
			actorId,
			createdAt: now,
		});
		const rows = [];
		const sync = new Map(this.syncPending);
		if (!this.snapshotWritten) {
			// The whole document first: later increments build on the items it holds. Blocks
			// whose text is already ahead of the database get their database content synced.
			rows.push(row(Y.encodeStateAsUpdate(this.doc), "system", LIVE_LOG_ACTOR));
			for (const b of this.mirror.all()) {
				if (!b.deleted && this.textOf(b.id) !== b.dbContent) sync.set(b.id, b.dbContent);
			}
		} else {
			const byActor = new Map<string, PendingRow[]>();
			for (const r of pending) {
				const key = `${r.actorKind}\u0000${r.actorId}`;
				const list = byActor.get(key);
				if (list) list.push(r);
				else byActor.set(key, [r]);
			}
			for (const list of byActor.values()) {
				const first = list[0] as PendingRow;
				rows.push(
					row(
						list.length === 1 ? first.update : Y.mergeUpdates(list.map((r) => r.update)),
						first.actorKind,
						first.actorId,
					),
				);
			}
		}
		if (sync.size > 0) rows.push(row(encodeSyncRow(sync), "system", LIVE_SYNC_ACTOR));
		try {
			await outlineStore.appendLiveUpdates(rows);
		} catch (error) {
			// Kept for the next attempt (the seq numbers drawn stay unused).
			this.pendingRows = [...pending, ...this.pendingRows];
			this.schedulePersist();
			throw error;
		}
		for (const [id, content] of sync)
			if (this.syncPending.get(id) === content) this.syncPending.delete(id);
		this.snapshotWritten = true;
		const bytes = rows.reduce((n, r) => n + r.updateB64.length, 0);
		this.stats.rowsWritten += rows.length;
		this.stats.rowBytes += bytes;
		this.rowsSinceCompact += rows.length;
		this.bytesSinceCompact += bytes;
		if (
			this.rowsSinceCompact > OUTLINE_LIVE_BOUNDS.compactRows ||
			this.bytesSinceCompact > OUTLINE_LIVE_BOUNDS.compactBytes
		) {
			const ahead = new Map<string, string>();
			for (const b of this.mirror.all()) {
				if (!b.deleted && this.textOf(b.id) !== b.dbContent) ahead.set(b.id, b.dbContent);
			}
			const compacted = [row(Y.encodeStateAsUpdate(this.doc), "system", LIVE_LOG_ACTOR)];
			if (ahead.size > 0) compacted.push(row(encodeSyncRow(ahead), "system", LIVE_SYNC_ACTOR));
			await outlineStore.replaceLiveUpdates(this.pageId, this.epoch, compacted);
			this.rowsSinceCompact = compacted.length;
			this.bytesSinceCompact = compacted.reduce((n, r) => n + r.updateB64.length, 0);
		}
	}

	// ─────────────────────────────────────────────────────────────────────────
	// Closing
	// ─────────────────────────────────────────────────────────────────────────

	/**
	 * Commit everything, drop the log once nothing is left uncommitted, end the participants'
	 * session with `code`. Runs in the lane. Idempotent.
	 */
	close(code: LiveCloseCode = "ended"): Promise<void> {
		this.closing ??= this.doClose(code);
		return this.closing;
	}

	private async doClose(code: LiveCloseCode): Promise<void> {
		this.flushBroadcast();
		const committed = await this.flushContent();
		await this.persistNow();
		this.closed = true;
		for (const timer of [
			this.broadcastTimer,
			this.presenceTimer,
			this.flushTimer,
			this.persistTimer,
			this.idleTimer,
		]) {
			if (timer) clearTimeout(timer);
		}
		for (const p of [...this.participants.values()]) {
			p.socket.send(encodeLiveFrame({ kind: LIVE_FRAME.closed, handle: p.handle, head: { code } }));
		}
		this.participants.clear();
		if (committed && this.dirty.size === 0) {
			try {
				await outlineStore.deleteLiveUpdates(this.pageId, this.epoch);
			} catch (error) {
				logger.warn("Outline live log could not be dropped", {
					pageId: this.pageId,
					error: String(error),
				});
			}
		} else {
			logger.warn("Outline live session closed with uncommitted text; the log is kept for replay", {
				pageId: this.pageId,
				epoch: this.epoch,
			});
		}
		this.doc.destroy();
	}
}

function encodeSyncRow(contents: ReadonlyMap<string, string>): Uint8Array {
	return utf8.encode(JSON.stringify({ b: Object.fromEntries(contents) }));
}

function actorOf(commit: CommittedEvent): LiveActor {
	switch (commit.actorKind) {
		case "agent":
			return "a";
		case "human":
			return "h";
		default:
			return "s";
	}
}

/** A block's live text prepared for a commit (see `planContents`). */
export interface ContentPlan {
	id: string;
	/** The live text when planned. */
	text: string;
	/** Normalized parts: the block's own content, then blocks to add after it. */
	parts: LiveNormalized[];
}

/** Whether committing a plan adds blocks (split structure, or parts past the size limit). */
function reshapes(plan: ContentPlan): boolean {
	return plan.parts.length > 1 || plan.parts.some((part) => part.split.length > 0);
}

/** Whether an operation names a block by temp id (part of a split chain). */
function namesTemp(op: OutlineOp): boolean {
	const refs: unknown[] = [];
	if ("parent" in op) refs.push(op.parent);
	if ("position" in op && op.position && typeof op.position === "object") {
		const pos = op.position as Record<string, unknown>;
		refs.push(pos.after, pos.before);
	}
	return refs.some((r) => !!r && typeof r === "object" && "tempId" in (r as object));
}

/**
 * Operations committing a content plan: the block's content (when `dbContent` is given and it
 * changed; an insert carries it already), its split-off children, and further parts as blocks
 * right after it under `parentId`.
 */
export function contentOps(
	plan: ContentPlan,
	dbContent: string | null,
	parentId: string | null,
	ops: OutlineOp[],
	canonical: Array<OutlineCanonicalContent | undefined>,
): void {
	const [first, ...rest] = plan.parts as [LiveNormalized, ...LiveNormalized[]];
	if (dbContent !== null && first.content !== dbContent) {
		ops.push({ op: "update", block: { id: plan.id }, content: first.content });
		canonical.push({ content: first.content, links: first.links });
	}
	addSplit(ops, canonical, { id: plan.id }, first.split);
	let after: { id: string } | { tempId: string } = { id: plan.id };
	for (const part of rest) {
		const tempId = `p${ops.length}`;
		ops.push({
			op: "insert",
			tempId,
			id: generateId(),
			parent: parentId === null ? null : { id: parentId },
			position: { after },
			content: part.content,
		});
		canonical.push({ content: part.content, links: part.links });
		addSplit(ops, canonical, { tempId }, part.split);
		after = { tempId };
	}
}

/** Split-off blocks of normalized content become its first children. */
function addSplit(
	ops: OutlineOp[],
	canonical: Array<OutlineCanonicalContent | undefined>,
	parent: { id: string } | { tempId: string },
	split: LiveNormalized["split"],
): void {
	let previous: string | null = null;
	for (const node of split) {
		const tempId = `s${ops.length}`;
		ops.push({
			op: "insert",
			tempId,
			id: generateId(),
			parent,
			position: previous ? { after: { tempId: previous } } : { first: true },
			content: node.content,
			task: node.task ? { status: node.task } : null,
		});
		canonical.push({ content: node.content, links: node.links });
		addSplit(ops, canonical, { tempId }, node.children);
		previous = tempId;
	}
}

/**
 * A text over the per-block limit, cut into parts that fit (at line breaks where possible): the
 * first stays in the block, the rest become blocks after it. Never drops text.
 */
export function fitBody(text: string): string[] {
	const limit = OUTLINE_LIMITS.maxBodyBytes;
	if (bytesOf(text) <= limit) return [text];
	const parts: string[] = [];
	let rest = text;
	while (bytesOf(rest) > limit) {
		// Largest prefix under the limit, by halving the character count.
		let lo = 1;
		let hi = rest.length;
		while (lo < hi) {
			const mid = Math.ceil((lo + hi) / 2);
			if (bytesOf(rest.slice(0, mid)) <= limit) lo = mid;
			else hi = mid - 1;
		}
		let cut = lo;
		const nl = rest.lastIndexOf("\n", cut);
		if (nl > cut / 2) cut = nl;
		// Never split a surrogate pair.
		const code = rest.charCodeAt(cut - 1);
		if (code >= 0xd800 && code <= 0xdbff) cut--;
		parts.push(rest.slice(0, cut));
		rest = rest.slice(cut).replace(/^\n/, "");
	}
	parts.push(rest);
	return parts;
}

function parseOps(raw: unknown): LiveOp[] | null {
	const list = (raw as { ops?: unknown } | null)?.ops;
	if (!Array.isArray(list) || list.length === 0 || list.length > OUTLINE_LIVE_LIMITS.maxTxOps)
		return null;
	const out: LiveOp[] = [];
	const str = (v: unknown) => typeof v === "string" && v.length > 0 && v.length <= 128;
	for (const item of list as Array<Record<string, unknown>>) {
		if (!item || typeof item !== "object" || !str(item.id)) return null;
		const id = item.id as string;
		switch (item.t) {
			case "i":
				if (!str(item.p) || !str(item.k)) return null;
				if (item.task !== undefined && item.task !== null && !isLiveTaskChar(item.task))
					return null;
				if (!isKey(item.k as string)) return null;
				out.push({
					t: "i",
					id,
					p: item.p as string,
					k: item.k as string,
					...(item.task !== undefined ? { task: item.task as LiveTaskChar | null } : {}),
				});
				break;
			case "m":
				if (!str(item.p) || !str(item.k) || !isKey(item.k as string)) return null;
				out.push({ t: "m", id, p: item.p as string, k: item.k as string });
				break;
			case "d":
				out.push({ t: "d", id });
				break;
			case "k":
				if (item.task !== null && !isLiveTaskChar(item.task)) return null;
				out.push({ t: "k", id, task: item.task as LiveTaskChar | null });
				break;
			default:
				return null;
		}
	}
	return out;
}

function isKey(k: string): boolean {
	try {
		keyBetween(k, null);
		return true;
	} catch {
		return false;
	}
}

function validPos(pos: unknown): boolean {
	if (!Array.isArray(pos) || pos.length !== 2 || typeof pos[0] !== "string" || pos[0].length > 128)
		return false;
	const rel = pos[1];
	return (
		rel === null ||
		(Array.isArray(rel) && rel.length === 3 && rel.every((x) => Number.isSafeInteger(x)))
	);
}

function validSelection(sel: unknown): LiveSelWire | null {
	if (!sel || typeof sel !== "object") return null;
	const s = sel as Record<string, unknown>;
	if (Array.isArray(s.b)) {
		const ids = (s.b as unknown[])
			.filter((x): x is string => typeof x === "string" && x.length <= 128)
			.slice(0, OUTLINE_LIVE_BOUNDS.presenceBlocks);
		return { b: ids };
	}
	if (validPos(s.a) && validPos(s.h)) return { a: s.a as never, h: s.h as never };
	return null;
}

/** Errors of the write path that mean "the structure moved under this transaction". */
export function isConflict(error: unknown): boolean {
	return (
		error instanceof OutlineConflictError ||
		error instanceof OutlineBusyError ||
		(error instanceof OutlineWriteError &&
			[
				"OUTLINE_BLOCK_NOT_FOUND",
				"OUTLINE_BLOCK_DELETED",
				"OUTLINE_CYCLE",
				"OUTLINE_ID_TAKEN",
			].includes(error.code))
	);
}
