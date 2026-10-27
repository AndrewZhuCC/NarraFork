/**
 * Read-only first (design §6.10): a page shown from its HTTP read before its live session is
 * ready, then turned live in place.
 *
 * What must hold:
 *  - the seed shows the read's blocks in its sibling order and texts, and sends nothing;
 *  - a writer may type while it connects (queued, never sent before the snapshot); structure,
 *    undo and redo wait; a viewer may not type;
 *  - the snapshot replaces the seed: what was typed is merged onto it (3-way: someone else's
 *    change to the same block meanwhile is kept), and sent once live;
 *  - nothing typed is lost without the person knowing (review round 2, probes P1–P3):
 *      - a read showing the page cannot go live (over the live limits) takes no typing;
 *      - refused when it joins (too large, no access), or no longer allowed to write when the
 *        snapshot arrives: nothing is sent, and what was typed is kept to copy (`unsaved`);
 *      - typed into a block that the snapshot no longer has (deleted or moved meanwhile): the
 *        "deleted" notice, whose restore adds the typed text back as a new block;
 *  - a session let go before it was live leaves as soon as its join is answered, never before
 *    (a leave that overtakes the join would be lost on the server); held again meanwhile, it
 *    stays; with something typed it stays for the grace period after the answer, so the merged
 *    text is sent before it leaves.
 */
import { afterEach, describe, expect, test } from "bun:test";
import {
	decodeLiveFrame,
	encodeLiveFrame,
	LIVE_FRAME,
	type LiveBlockWire,
	type LiveFrame,
	type LiveJoinedHead,
} from "@shared/outline-live-protocol";
import * as Y from "yjs";
import { LiveClient, type LiveTransport, type SeedBlock } from "./live-client";
import {
	acquireLiveSession,
	liveSessionsOn,
	releaseLiveSession,
	retainLiveSession,
	setLiveSessionGraceForTests,
} from "./live-sessions";

/** A connected socket that records what is sent and lets the test answer. */
function socket() {
	const handles = new Set<number>();
	const listeners = new Set<(frame: LiveFrame) => void>();
	const sent: LiveFrame[] = [];
	const transport: LiveTransport = {
		connected: true,
		send(bytes) {
			const frame = decodeLiveFrame(bytes);
			if (frame) sent.push(frame);
			return true;
		},
		onFrame(listener) {
			listeners.add(listener);
			return () => listeners.delete(listener);
		},
		onConnectionChange: () => () => {},
		allocateHandle() {
			for (let h = 1; h < 256; h++) {
				if (!handles.has(h)) {
					handles.add(h);
					return h;
				}
			}
			return null;
		},
		releaseHandle(handle) {
			handles.delete(handle);
		},
	};
	const deliver = (frame: { kind: number; handle: number; head?: unknown; body?: Uint8Array }) => {
		const decoded = decodeLiveFrame(encodeLiveFrame(frame));
		if (!decoded) throw new Error("bad frame");
		for (const l of [...listeners]) l(decoded);
	};
	return { transport, sent, deliver };
}

const PAGE = "page-0000001";
const A = "block-aaaaaa1";
const B = "block-bbbbbb1";
const C = "block-cccccc1";

/** The read: the page with A and B under it, C under A (the read lists B before A). */
function read(texts: Partial<Record<string, string>> = {}): SeedBlock[] {
	return [
		{
			id: PAGE,
			parentId: null,
			sid: "p1",
			content: texts[PAGE] ?? "Page",
			task: null,
			childIds: [B, A],
		},
		{ id: B, parentId: PAGE, sid: "b1", content: texts[B] ?? "Bee", task: null, childIds: [] },
		{ id: A, parentId: PAGE, sid: "a1", content: texts[A] ?? "Alpha", task: " ", childIds: [C] },
		{ id: C, parentId: A, sid: "c1", content: texts[C] ?? "Child", task: null, childIds: [] },
	];
}

/** The server's snapshot of the same page, with these texts. */
function joined(
	handle: number,
	texts: Partial<Record<string, string>> = {},
	head: Partial<LiveJoinedHead> = {},
) {
	const doc = new Y.Doc();
	const map = doc.getMap<Y.Text>("texts");
	const all: Record<string, string> = { [PAGE]: "Page", [B]: "Bee", [A]: "Alpha", [C]: "Child" };
	for (const [id, t] of Object.entries({ ...all, ...texts })) map.set(id, new Y.Text(t));
	const blocks: LiveBlockWire[] = [
		[PAGE, -1, "a0", "p1", ""],
		[B, 0, "a0", "b1", ""],
		[A, 0, "a1", "a1", " "],
		[C, 2, "a0", "c1", ""],
	];
	const answer: LiveJoinedHead = {
		epoch: 7,
		mode: "reset",
		w: true,
		op: 0,
		tx: 0,
		me: 1,
		blocks,
		peers: [],
		...head,
	};
	return { kind: LIVE_FRAME.joined, handle, head: answer, body: Y.encodeStateAsUpdate(doc) };
}

const type = (client: LiveClient, id: string, md: string) =>
	client.commit({
		ops: [],
		bodies: [{ id, md }],
		kind: "typing",
		blockId: id,
		selBefore: null,
		selAfter: null,
		view: null,
	});

const sentKinds = (sent: LiveFrame[]) => sent.map((f) => f.kind);

afterEach(() => setLiveSessionGraceForTests(null));

describe("read-only first: a page seeded from its HTTP read", () => {
	test("the seed shows the read in its order and texts and sends nothing", () => {
		const { transport, sent } = socket();
		const client = new LiveClient(transport, PAGE, "edit");
		expect(client.editable).toBe(false);
		expect(client.seed(read())).toBe(true);
		expect(client.provisional).toBe(true);
		expect(client.visible.children(PAGE)).toEqual([B, A]);
		expect(client.visible.children(A)).toEqual([C]);
		expect(client.visible.get(A)?.task).toBe(" ");
		expect(client.textOf(B)).toBe("Bee");
		// Only the join went out.
		expect(sentKinds(sent)).toEqual([LIVE_FRAME.join]);
		expect(client.editable).toBe(true);
		const viewer = new LiveClient(socket().transport, PAGE, "view");
		viewer.seed(read());
		expect(viewer.editable).toBe(false);
	});

	test("typing is queued, structure, undo and redo wait; a later read refreshes untyped texts", async () => {
		const { transport, sent } = socket();
		const client = new LiveClient(transport, PAGE, "edit");
		client.seed(read());
		type(client, B, "Bee typed");
		client.commit({
			ops: [{ t: "d", id: C }],
			bodies: [],
			kind: "struct",
			blockId: null,
			selBefore: null,
			selAfter: null,
			view: null,
		});
		expect(client.visible.alive(C)).toBeDefined();
		expect(client.undo()).toBe(false);
		expect(client.redo()).toBe(false);
		await new Promise((r) => setTimeout(r, 120));
		expect(sentKinds(sent)).toEqual([LIVE_FRAME.join]);
		// A fresher read: the untyped text follows it, the typed one is kept.
		client.seed(read({ [A]: "Alpha v2", [B]: "Bee v2" }));
		expect(client.textOf(A)).toBe("Alpha v2");
		expect(client.textOf(B)).toBe("Bee typed");
	});

	test("the snapshot replaces the seed; what was typed is merged onto it and sent", () => {
		const { transport, sent, deliver } = socket();
		const client = new LiveClient(transport, PAGE, "edit");
		client.seed(read());
		type(client, B, "Bee and more");
		const handle = sent[0]?.handle as number;
		// Someone else changed the start of the same block meanwhile.
		const answer = joined(handle, { [B]: "Busy Bee" });
		deliver(answer);
		expect(client.ready).toBe(true);
		expect(client.provisional).toBe(false);
		expect(client.textOf(B)).toBe("Busy Bee and more");
		expect(client.textOf(A)).toBe("Alpha");
		expect(client.visible.children(PAGE)).toEqual([B, A]);
		expect(sentKinds(sent)).toEqual([LIVE_FRAME.join, LIVE_FRAME.update]);
		// What is sent is a change of the server's document, not of the seed's.
		const server = new Y.Doc();
		Y.applyUpdate(server, answer.body);
		Y.applyUpdate(server, sent[1]?.body as Uint8Array);
		expect(server.getMap<Y.Text>("texts").get(B)?.toString()).toBe("Busy Bee and more");
	});

	test("no longer allowed to write when the snapshot arrives: nothing is sent, the typing is kept to copy", () => {
		const { transport, sent, deliver } = socket();
		const client = new LiveClient(transport, PAGE, "edit");
		const events: string[] = [];
		client.on((e) => events.push(e));
		client.seed(read());
		type(client, B, "Bee typed");
		deliver(joined(sent[0]?.handle as number, {}, { w: false }));
		expect(client.ready).toBe(true);
		expect(client.editable).toBe(false);
		expect(client.textOf(B)).toBe("Bee");
		expect(sentKinds(sent)).toEqual([LIVE_FRAME.join]);
		expect(client.unsaved).toEqual({
			reason: "read_only",
			texts: [{ blockId: B, text: "Bee typed" }],
		});
		expect(events).toContain("unsaved");
		client.dismissUnsaved();
		expect(client.unsaved).toBeNull();
	});

	test("a read showing the page cannot go live takes no typing (probe P2)", () => {
		const { transport, sent } = socket();
		const client = new LiveClient(transport, PAGE, "edit");
		client.seed(read(), { typing: false });
		expect(client.provisional).toBe(true);
		expect(client.editable).toBe(false);
		type(client, B, "Bee typed");
		expect(client.textOf(B)).toBe("Bee");
		expect(client.pending).toEqual([]);
		// A later read that looks fine does not allow it again.
		client.seed(read());
		expect(client.editable).toBe(false);
		expect(sentKinds(sent)).toEqual([LIVE_FRAME.join]);
	});

	test("refused when it joins (too large) after typing: nothing is sent, the typing is kept to copy (probe P2)", () => {
		const { transport, sent, deliver } = socket();
		const shared = acquireLiveSession(transport, PAGE, "edit");
		if (!shared) throw new Error("no session");
		retainLiveSession(shared);
		const client = shared.client;
		client.seed(read());
		type(client, B, "Bee typed then refused");
		type(client, A, "Alpha too");
		deliver({
			kind: LIVE_FRAME.closed,
			handle: sent[0]?.handle as number,
			head: { code: "too_large" },
		});
		expect(client.status).toBe("closed");
		expect(client.closeCode).toBe("too_large");
		expect(client.unsaved).toEqual({
			reason: "refused",
			texts: [
				{ blockId: B, text: "Bee typed then refused" },
				{ blockId: A, text: "Alpha too" },
			],
		});
		// The view keeps showing it (the editor stays to offer the copy).
		expect(client.textOf(B)).toBe("Bee typed then refused");
		expect(sentKinds(sent)).toEqual([LIVE_FRAME.join]);
		releaseLiveSession(shared);
	});

	test("typed into a block the snapshot no longer has: the deleted notice restores the text as a new block (probe P3)", () => {
		const { transport, sent, deliver } = socket();
		const client = new LiveClient(transport, PAGE, "edit");
		client.seed(read());
		type(client, C, "Child with my words");
		const handle = sent[0]?.handle as number;
		// Someone deleted C (under A) before the snapshot.
		const doc = new Y.Doc();
		const map = doc.getMap<Y.Text>("texts");
		for (const [id, t] of Object.entries({ [PAGE]: "Page", [B]: "Bee", [A]: "Alpha" }))
			map.set(id, new Y.Text(t));
		const blocks: LiveBlockWire[] = [
			[PAGE, -1, "a0", "p1", ""],
			[B, 0, "a0", "b1", ""],
			[A, 0, "a1", "a1", " "],
		];
		deliver({
			kind: LIVE_FRAME.joined,
			handle,
			head: { epoch: 7, mode: "reset", w: true, op: 0, tx: 0, me: 1, blocks, peers: [] },
			body: Y.encodeStateAsUpdate(doc),
		});
		expect(client.ready).toBe(true);
		expect(client.visible.alive(C)).toBeUndefined();
		expect(client.deleted).toMatchObject({ blockId: C, whileConnecting: true, by: "" });
		const op = client.deleted?.restore[0];
		expect(op).toMatchObject({ t: "i", p: A, content: "Child with my words", task: null });
		expect(op && op.t === "i" ? op.id : C).not.toBe(C);
		// Nothing sent until the person restores.
		expect(sentKinds(sent)).toEqual([LIVE_FRAME.join]);
		client.restoreDeleted();
		const restored = client.visible.children(A);
		expect(restored).toHaveLength(1);
		expect(client.textOf(restored[0] as string)).toBe("Child with my words");
		expect(sentKinds(sent)).toEqual([LIVE_FRAME.join, LIVE_FRAME.tx]);
	});

	test("its parent gone too: the text goes back under the nearest block still there", () => {
		const { transport, sent, deliver } = socket();
		const client = new LiveClient(transport, PAGE, "edit");
		client.seed(read());
		type(client, C, "Child with my words");
		const doc = new Y.Doc();
		const map = doc.getMap<Y.Text>("texts");
		for (const [id, t] of Object.entries({ [PAGE]: "Page", [B]: "Bee" }))
			map.set(id, new Y.Text(t));
		deliver({
			kind: LIVE_FRAME.joined,
			handle: sent[0]?.handle as number,
			head: {
				epoch: 7,
				mode: "reset",
				w: true,
				op: 0,
				tx: 0,
				me: 1,
				blocks: [
					[PAGE, -1, "a0", "p1", ""],
					[B, 0, "a0", "b1", ""],
				],
				peers: [],
			},
			body: Y.encodeStateAsUpdate(doc),
		});
		expect(client.deleted?.restore[0]).toMatchObject({ t: "i", p: PAGE });
		client.restoreDeleted();
		const kids = client.visible.children(PAGE);
		expect(kids[0]).toBe(B);
		expect(client.textOf(kids[1] as string)).toBe("Child with my words");
	});
});

describe("a session let go before it was live", () => {
	test("leaves as soon as its join is answered, not before", () => {
		const { transport, sent, deliver } = socket();
		const shared = acquireLiveSession(transport, PAGE, "edit");
		if (!shared) throw new Error("no session");
		retainLiveSession(shared);
		shared.client.seed(read());
		releaseLiveSession(shared);
		// The join is still on its way: no leave yet (it could overtake the join).
		expect(sentKinds(sent)).toEqual([LIVE_FRAME.join]);
		expect(liveSessionsOn(transport)).toHaveLength(1);
		deliver(joined(sent[0]?.handle as number));
		expect(sentKinds(sent)).toEqual([LIVE_FRAME.join, LIVE_FRAME.leave]);
		expect(liveSessionsOn(transport)).toEqual([]);
	});

	test("held again before the answer: it stays", () => {
		const { transport, sent, deliver } = socket();
		const shared = acquireLiveSession(transport, PAGE, "edit");
		if (!shared) throw new Error("no session");
		retainLiveSession(shared);
		releaseLiveSession(shared);
		retainLiveSession(shared);
		deliver(joined(sent[0]?.handle as number));
		expect(sentKinds(sent)).toEqual([LIVE_FRAME.join]);
		expect(liveSessionsOn(transport)).toEqual([
			{ pageId: PAGE, mode: "edit", refs: 1, status: "live" },
		]);
	});

	test("typed on the read, let go before the snapshot: the merged text is sent, then it leaves (probe P1)", async () => {
		setLiveSessionGraceForTests(40);
		const { transport, sent, deliver } = socket();
		const shared = acquireLiveSession(transport, PAGE, "edit");
		if (!shared) throw new Error("no session");
		retainLiveSession(shared);
		shared.client.seed(read());
		type(shared.client, B, "Bee typed fast");
		// The person jumped elsewhere (or hid the panel) before the snapshot.
		releaseLiveSession(shared);
		const answer = joined(sent[0]?.handle as number);
		deliver(answer);
		expect(sentKinds(sent)).toEqual([LIVE_FRAME.join, LIVE_FRAME.update]);
		expect(liveSessionsOn(transport)).toHaveLength(1);
		await new Promise((r) => setTimeout(r, 80));
		expect(sentKinds(sent)).toEqual([LIVE_FRAME.join, LIVE_FRAME.update, LIVE_FRAME.leave]);
		expect(liveSessionsOn(transport)).toEqual([]);
		// What went out is the typed text on the server's document.
		const server = new Y.Doc();
		Y.applyUpdate(server, answer.body);
		Y.applyUpdate(server, sent[1]?.body as Uint8Array);
		expect(server.getMap<Y.Text>("texts").get(B)?.toString()).toBe("Bee typed fast");
	});

	test("a live session still gets the grace period", async () => {
		setLiveSessionGraceForTests(40);
		const { transport, sent, deliver } = socket();
		const shared = acquireLiveSession(transport, PAGE, "edit");
		if (!shared) throw new Error("no session");
		retainLiveSession(shared);
		deliver(joined(sent[0]?.handle as number));
		releaseLiveSession(shared);
		expect(liveSessionsOn(transport)).toHaveLength(1);
		await new Promise((r) => setTimeout(r, 80));
		expect(liveSessionsOn(transport)).toEqual([]);
		expect(sentKinds(sent)).toEqual([LIVE_FRAME.join, LIVE_FRAME.leave]);
	});
});
