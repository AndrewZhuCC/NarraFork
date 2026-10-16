/**
 * Search params of the browse page `/knowledge` (docs/KNOWLEDGE_UI_REDESIGN.md §4.1, §5.1):
 * `collection`, `q`, `tag`, `level`, `status`, `sort`. Every view state is in the URL, so a
 * filtered list can be linked; defaults are left out. The legacy `tab=…` values are read by the
 * route's `beforeLoad` redirect (`legacyKnowledgeIndexRedirect`), not kept here.
 */
import type { KnowledgeEntrySort, KnowledgeEntryStatus } from "./api";

export interface KnowledgeBrowseSearch {
	collection?: string;
	q?: string;
	tag?: string;
	level?: string;
	status?: KnowledgeEntryStatus;
	sort?: KnowledgeEntrySort;
}

/** Entry / collection ids are nanoids (URL-safe alphabet). */
const ID = /^[A-Za-z0-9_-]{1,64}$/;
const SORTS: readonly KnowledgeEntrySort[] = ["updated", "title", "relevance"];
const STATUSES: readonly KnowledgeEntryStatus[] = ["active", "archived"];

function text(v: unknown, max: number): string | undefined {
	if (typeof v !== "string") return undefined;
	const s = v.trim();
	return s && s.length <= max ? s : undefined;
}

/** `validateSearch` for `/knowledge/`: unknown or malformed values are dropped. */
export function parseKnowledgeBrowseSearch(search: Record<string, unknown>): KnowledgeBrowseSearch {
	const collection =
		typeof search.collection === "string" && ID.test(search.collection)
			? search.collection
			: undefined;
	const q = text(search.q, 500);
	const tag = text(search.tag, 64);
	const level = text(search.level, 64);
	const status = STATUSES.includes(search.status as KnowledgeEntryStatus)
		? (search.status as KnowledgeEntryStatus)
		: undefined;
	const rawSort = SORTS.includes(search.sort as KnowledgeEntrySort)
		? (search.sort as KnowledgeEntrySort)
		: undefined;
	// The default order is implied: relevance with a query, most recently updated without.
	const sort = rawSort && rawSort !== defaultBrowseSort(q) ? rawSort : undefined;
	return {
		...(collection ? { collection } : {}),
		...(q ? { q } : {}),
		...(tag ? { tag } : {}),
		...(level ? { level } : {}),
		...(status ? { status } : {}),
		...(sort ? { sort } : {}),
	};
}

function defaultBrowseSort(q: string | undefined): KnowledgeEntrySort {
	return q ? "relevance" : "updated";
}

/**
 * The order the list is actually requested in: the explicit `sort`, else the default for the
 * presence of `q`. `relevance` without a query has nothing to rank, so it falls back to updated.
 */
export function effectiveBrowseSort(search: KnowledgeBrowseSearch): KnowledgeEntrySort {
	const sort = search.sort ?? defaultBrowseSort(search.q);
	return sort === "relevance" && !search.q ? "updated" : sort;
}

/** Which empty state an empty list shows (§5.1). */
export type KnowledgeBrowseEmptyState = "noCollections" | "noResults" | "emptyCollection" | "empty";

export function browseEmptyState(
	search: KnowledgeBrowseSearch,
	collectionCount: number,
): KnowledgeBrowseEmptyState {
	if (collectionCount === 0) return "noCollections";
	if (search.q || search.tag || search.level || search.status) return "noResults";
	if (search.collection) return "emptyCollection";
	return "empty";
}
