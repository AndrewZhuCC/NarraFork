/**
 * Replaying a live session's log after it ended without committing everything (a crash, a
 * failed final commit): the text people typed is committed, then the log is dropped.
 *
 * An epoch's log is a document snapshot, increments, and sync rows (what the database held for
 * a block when the session changed it, see `LivePage.persistStep`). Replay rebuilds the final
 * document and, for every live block of the page, merges three ways: from the last synced
 * content (or the snapshot's text), to the final text, onto what the database holds now. So a
 * commit the session never logged (an outside write in the last moments) is kept, and only
 * what people typed and nobody committed is written, as their own commits.
 *
 * Runs at startup as a background job over every epoch left in `outline_live_updates`, and
 * before a session opens on a page that still has one. Bounded: at most
 * {@link OUTLINE_LIVE_REPLAY.maxRows} rows and {@link OUTLINE_LIVE_REPLAY.maxBytes} of log per
 * epoch are read (a session compacts long before that); an epoch over the bound is left for an
 * operator and logged, never truncated.
 */
import { logger } from "@server/lib/logger";
import { merge3 } from "@shared/outline";
import * as Y from "yjs";
import { OUTLINE_LIMITS } from "../model";
import { outlineStore } from "../store";
import type { OutlineLiveUpdateRow } from "../store/port";
import { type OutlineCanonicalContent, type OutlineOp, writeOutline } from "../write";
import { asOwnLiveWrite } from "./lane";
import { normalizeLiveTexts } from "./normalize";
import { type ContentPlan, contentOps, fitBody, LIVE_SYNC_ACTOR } from "./session";

export const OUTLINE_LIVE_REPLAY = {
	/** Rows read per query. */
	pageRows: 200,
	/** Rows of one epoch replayed at most. */
	maxRows: 5_000,
	/** Base64 bytes of one epoch replayed at most. */
	maxBytes: 64 * 1024 * 1024,
	/** Epochs listed per query by the startup job. */
	epochsPerPage: 50,
	/** Pause between two epochs of the startup job, so it yields to requests. */
	pauseMs: 20,
} as const;

export type ReplayOutcome = "committed" | "empty" | "kept";

const decoder = new TextDecoder();

async function readEpoch(pageId: string, epoch: number): Promise<OutlineLiveUpdateRow[] | null> {
	const rows: OutlineLiveUpdateRow[] = [];
	let bytes = 0;
	let afterSeq: number | undefined;
	for (;;) {
		const page = await outlineStore.listLiveUpdates(pageId, epoch, {
			...(afterSeq !== undefined ? { afterSeq } : {}),
			limit: OUTLINE_LIVE_REPLAY.pageRows,
		});
		for (const row of page) {
			rows.push(row);
			bytes += row.updateB64.length;
		}
		if (rows.length > OUTLINE_LIVE_REPLAY.maxRows || bytes > OUTLINE_LIVE_REPLAY.maxBytes)
			return null;
		if (page.length < OUTLINE_LIVE_REPLAY.pageRows) return rows;
		afterSeq = (page[page.length - 1] as OutlineLiveUpdateRow).seq;
	}
}

/**
 * Replay one epoch: commit what is not committed, then drop the rows. `kept` when the log could
 * not be replayed (too large, unreadable, the commit failed): the rows stay.
 */
export async function replayLiveEpoch(pageId: string, epoch: number): Promise<ReplayOutcome> {
	const rows = await readEpoch(pageId, epoch);
	if (!rows) {
		logger.warn("Outline live log too large to replay; kept", { pageId, epoch });
		return "kept";
	}
	if (rows.length === 0) return "empty";
	const doc = new Y.Doc();
	const texts = doc.getMap<Y.Text>("texts");
	const base = new Map<string, string>();
	const lastActor = new Map<string, string>();
	texts.observeDeep((events, txn) => {
		const actor = txn.origin as OutlineLiveUpdateRow | null;
		if (!actor || actor.actorKind !== "human") return;
		for (const ev of events) {
			const target = ev.target as unknown as { _item?: { parentSub?: string | null } };
			const id = ev.target === texts ? null : target._item?.parentSub;
			if (id) lastActor.set(id, actor.actorId);
			if (ev.target === texts)
				for (const key of (ev as Y.YMapEvent<Y.Text>).keysChanged)
					lastActor.set(key, actor.actorId);
		}
	});
	let first = true;
	try {
		for (const row of rows) {
			const data = Buffer.from(row.updateB64, "base64");
			if (row.actorId === LIVE_SYNC_ACTOR) {
				const parsed = JSON.parse(decoder.decode(data)) as { b?: Record<string, string> };
				for (const [id, content] of Object.entries(parsed.b ?? {})) base.set(id, content);
				continue;
			}
			Y.applyUpdate(doc, data, row);
			if (first) {
				// The snapshot: its texts are the base wherever no sync row says otherwise.
				for (const [id, t] of texts)
					if (t instanceof Y.Text && !base.has(id)) base.set(id, t.toString());
				first = false;
			}
		}
	} catch (error) {
		logger.warn("Outline live log unreadable; kept", { pageId, epoch, error: String(error) });
		doc.destroy();
		return "kept";
	}

	const page = await outlineStore.getBlock(pageId);
	if (!page || page.deleted) {
		doc.destroy();
		await outlineStore.deleteLiveUpdates(pageId, epoch);
		return "empty";
	}
	const { blocks } = await outlineStore.listPageBlocks(pageId, OUTLINE_LIMITS.maxPageBlocks);
	const changes: Array<{ id: string; text: string; actor: string | null }> = [];
	for (const b of blocks) {
		const t = texts.get(b.id);
		if (!(t instanceof Y.Text)) continue;
		const final = t.toString();
		const from = base.get(b.id) ?? b.content;
		if (final === from) continue;
		const merged = merge3(from, final, b.content, { lenient: true });
		const text = merged.ok ? merged.result : final;
		if (text !== b.content) changes.push({ id: b.id, text, actor: lastActor.get(b.id) ?? null });
	}
	doc.destroy();
	if (changes.length > 0) {
		const committed = await commitChanges(page.spaceId, changes, blocks);
		if (!committed) return "kept";
	}
	await outlineStore.deleteLiveUpdates(pageId, epoch);
	return changes.length > 0 ? "committed" : "empty";
}

async function commitChanges(
	spaceId: string,
	changes: Array<{ id: string; text: string; actor: string | null }>,
	blocks: ReadonlyArray<{ id: string; content: string; parentId: string | null }>,
): Promise<boolean> {
	const current = new Map(blocks.map((b) => [b.id, b]));
	// A text past the per-block limit keeps what fits; the log is the only other copy of the
	// rest, so it becomes the block's next siblings rather than being dropped.
	const cut = changes.map((c) => fitBody(c.text));
	let normalized: Awaited<ReturnType<typeof normalizeLiveTexts>>;
	try {
		normalized = await normalizeLiveTexts(cut.flat());
	} catch (error) {
		logger.warn("Outline live replay could not normalize", { error: String(error) });
		return false;
	}
	const plans: ContentPlan[] = [];
	let at = 0;
	changes.forEach((c, i) => {
		const parts = normalized.slice(at, at + (cut[i] as string[]).length);
		at += (cut[i] as string[]).length;
		plans.push({ id: c.id, text: c.text, parts });
	});
	const byActor = new Map<string | null, ContentPlan[]>();
	changes.forEach((c, i) => {
		const list = byActor.get(c.actor);
		if (list) list.push(plans[i] as ContentPlan);
		else byActor.set(c.actor, [plans[i] as ContentPlan]);
	});
	for (const [actor, list] of byActor) {
		const ops: OutlineOp[] = [];
		const canonical: Array<OutlineCanonicalContent | undefined> = [];
		for (const plan of list) {
			const b = current.get(plan.id);
			if (b) contentOps(plan, b.content, b.parentId, ops, canonical);
		}
		if (ops.length === 0) continue;
		try {
			// Split chains are short and written with their block; plain updates chunk freely.
			for (let start = 0; start < ops.length; start += OUTLINE_LIMITS.maxOps) {
				await asOwnLiveWrite(() =>
					writeOutline(
						{
							spaceId,
							ops: ops.slice(start, start + OUTLINE_LIMITS.maxOps),
							actor: actor ? { kind: "human", id: actor } : { kind: "system", id: "outline-live" },
							responsibleUserId: actor,
							summary: "live edit (recovered)",
						},
						{ canonical: canonical.slice(start, start + OUTLINE_LIMITS.maxOps) },
					),
				);
			}
		} catch (error) {
			logger.warn("Outline live replay commit failed; log kept", { spaceId, error: String(error) });
			return false;
		}
	}
	return true;
}

/**
 * The startup job: replay every epoch left in the log, one at a time with a pause between, skipping
 * epochs of sessions open in this process (`isLive`). Resolves when the log was walked once.
 */
export async function replayAllLiveEpochs(
	isLive: (pageId: string, epoch: number) => boolean,
	replay: (pageId: string, epoch: number) => Promise<ReplayOutcome>,
): Promise<{ committed: number; kept: number }> {
	let after: { pageBlockId: string; epoch: number } | undefined;
	let committed = 0;
	let kept = 0;
	for (;;) {
		const epochs = await outlineStore.listLiveEpochs({
			...(after ? { after } : {}),
			limit: OUTLINE_LIVE_REPLAY.epochsPerPage,
		});
		for (const e of epochs) {
			if (isLive(e.pageBlockId, e.epoch)) continue;
			try {
				const outcome = await replay(e.pageBlockId, e.epoch);
				if (outcome === "committed") committed++;
				if (outcome === "kept") kept++;
			} catch (error) {
				kept++;
				logger.warn("Outline live replay failed", { pageId: e.pageBlockId, error: String(error) });
			}
			await new Promise((resolve) => setTimeout(resolve, OUTLINE_LIVE_REPLAY.pauseMs));
		}
		if (epochs.length < OUTLINE_LIVE_REPLAY.epochsPerPage) break;
		after = epochs[epochs.length - 1];
	}
	return { committed, kept };
}
