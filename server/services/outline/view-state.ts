/**
 * One person's view state of one page (design §6.10, §7.1 `outline_view_state`): which blocks
 * they folded and which way their mind map grows. Personal: never part of the block model, never
 * a commit, invisible to everyone else, and the same on every device they use.
 *
 * Reading and writing need read access to the page's space (PR5's `canReadSpace`): folding a
 * page one may only read is still one's own view of it. A page one may not read answers 404 the
 * same way as a page that does not exist, as every other outline route does.
 *
 * Bounded: one row per person and page, read and written by its primary key; at most
 * `OUTLINE_VIEW_STATE_LIMITS.maxCollapsed` folded ids. Ids are not checked against the page's
 * blocks (that would read the page); the client ignores ids it does not know, and a later write
 * replaces the list.
 */
import {
	OUTLINE_VIEW_STATE_LIMITS,
	type OutlineMapLayout,
	type OutlineViewStateResponse,
} from "@shared/outline-api";
import { canReadSpace, type OutlinePrincipal } from "./access";
import { outlineStore } from "./store";
import type { OutlineStore } from "./store/port";

/** The page, if it is a live page of a space the person may read; null otherwise. */
async function readablePage(
	principal: OutlinePrincipal,
	pageId: string,
	store: OutlineStore,
): Promise<{ id: string } | null> {
	const block = await store.getBlock(pageId);
	if (!block || block.deleted || block.parentId !== null || block.pageId !== block.id) return null;
	const space = await store.getSpace(block.spaceId);
	if (!space || !(await canReadSpace(principal, space))) return null;
	return { id: block.id };
}

/** Folded ids without duplicates, in their order, within the limits. */
export function normalizeCollapsed(ids: readonly string[]): string[] {
	const out: string[] = [];
	const seen = new Set<string>();
	for (const id of ids) {
		if (out.length >= OUTLINE_VIEW_STATE_LIMITS.maxCollapsed) break;
		if (!id || id.length > OUTLINE_VIEW_STATE_LIMITS.maxIdChars || seen.has(id)) continue;
		seen.add(id);
		out.push(id);
	}
	return out;
}

/** The person's view state of a page (the defaults when never written); null: no such page. */
export async function readViewState(
	principal: OutlinePrincipal,
	pageId: string,
	store: OutlineStore = outlineStore,
): Promise<OutlineViewStateResponse | null> {
	const page = await readablePage(principal, pageId, store);
	if (!page) return null;
	const row = await store.getViewState(principal.userId, page.id);
	return {
		pageId: page.id,
		collapsed: row ? normalizeCollapsed(row.collapsed) : [],
		layout: row?.layout ?? "right",
		updatedAt: row?.updatedAt ?? null,
	};
}

/** Replace the person's view state of a page; null: no such page (nothing written). */
export async function writeViewState(
	principal: OutlinePrincipal,
	pageId: string,
	input: { collapsed: readonly string[]; layout: OutlineMapLayout },
	options: { store?: OutlineStore; now?: string } = {},
): Promise<OutlineViewStateResponse | null> {
	const store = options.store ?? outlineStore;
	const page = await readablePage(principal, pageId, store);
	if (!page) return null;
	const updatedAt = options.now ?? new Date().toISOString();
	const collapsed = normalizeCollapsed(input.collapsed);
	await store.putViewState({
		userId: principal.userId,
		pageBlockId: page.id,
		collapsed,
		layout: input.layout,
		updatedAt,
	});
	return { pageId: page.id, collapsed, layout: input.layout, updatedAt };
}
