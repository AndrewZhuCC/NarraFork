import { db } from "../db";
import { knowledgeService } from "./knowledge-service";
import { searchStore } from "./search/backend";
import { canUseIndex, sanitizeQuery } from "./search/query";
import {
	CONVERSATION_MESSAGE_SCOPE,
	type InheritedSearchScope,
	type MessageSearchRow,
	type MessageSearchScope,
	type SearchSort,
	type SearchStrategy,
} from "./search/types";

export { sanitizeQuery } from "./search/query";
export type { InheritedSearchScope, MessageSearchScope } from "./search/types";

/** One matching message inside a grouped conversation hit. */
export interface MessageHit {
	id: string;
	snippet: string;
	messageRole?: string;
	createdAt?: string;
	blockId?: string;
}

interface SearchResult {
	type: "chapter" | "message" | "narrator" | "knowledge";
	id: string;
	title?: string;
	snippet: string;
	chapterId?: string;
	chapterTitle?: string;
	narratorId?: string;
	narratorTitle?: string;
	projectName?: string;
	status?: string;
	role?: string;
	model?: string;
	messageRole?: string;
	/** Knowledge hits: the collection the entry lives in. */
	collectionId?: string;
	collectionName?: string;
	tags?: string[];
	createdAt?: string;
	updatedAt?: string;
	lastMessageAt?: string | null;
	matchField?: string;
	matchScore: number;
	/** Message hits: the block the match is in, when it is known. */
	blockId?: string;
	/**
	 * Message hits are grouped per narrator: the result is the narrator's best hit, `hits`
	 * holds its first few matches (best first, the best one included) and `hitCount` how many
	 * matched among the candidates read. `hitCountIsLowerBound` means the candidate window
	 * filled up, so the narrator may have more.
	 */
	hits?: MessageHit[];
	hitCount?: number;
	hitCountIsLowerBound?: boolean;
}

interface SearchOptions {
	signal?: AbortSignal;
	sort?: SearchSort;
	query: string;
	entities: string[];
	limit?: number;
	/**
	 * The searching user, so narrator and message hits are limited to what they may
	 * read. Required rather than optional: an omitted principal used to mean "see
	 * everything", and a caller that simply forgot would silently reopen the leak.
	 */
	principal: { userId: string; isAdmin: boolean };
	/** Which conversation content to search. Defaults to {@link CONVERSATION_MESSAGE_SCOPE}. */
	scope?: MessageSearchScope;
}

/** Matches shown per grouped narrator. */
export const MESSAGE_HITS_PER_GROUP = 3;
/** Message candidates read per requested group, so one long session cannot fill the page. */
const MESSAGE_CANDIDATES_PER_GROUP = 5;
/** Ceiling on message candidates per search, whatever the limit. */
const MAX_MESSAGE_CANDIDATES = 500;

/** How many message rows to read for `limit` groups. */
export function messageCandidateLimit(limit: number): number {
	return Math.min(limit * MESSAGE_CANDIDATES_PER_GROUP, MAX_MESSAGE_CANDIDATES);
}

export interface MessageHitGroup {
	/** The narrator's best hit, in the order the rows arrived. */
	best: MessageSearchRow;
	hits: MessageSearchRow[];
	hitCount: number;
}

/**
 * Group message rows by narrator, keeping the store's order.
 *
 * Rows arrive best first (by rank or by time), so a group's first row is its best hit and
 * groups come out in the order of their best hits. At most `limit` groups and
 * `hitsPerGroup` hits per group are kept; `hitCount` still counts every row of the group.
 * A row without a narrator is its own group.
 */
export function groupMessageHits(
	rows: readonly MessageSearchRow[],
	limit: number,
	hitsPerGroup = MESSAGE_HITS_PER_GROUP,
): MessageHitGroup[] {
	const groups = new Map<string, MessageHitGroup>();
	for (const row of rows) {
		const key = row.narratorId ?? `message:${row.id}`;
		const group = groups.get(key);
		if (group) {
			group.hitCount += 1;
			if (group.hits.length < hitsPerGroup) group.hits.push(row);
		} else if (groups.size < limit) {
			groups.set(key, { best: row, hits: [row], hitCount: 1 });
		}
	}
	return [...groups.values()];
}

export interface NarratorMessageSearchResult {
	messageId: string;
	seq: number;
	role: string;
	snippet: string;
	preview: string;
	createdAt: string;
	/** The block the match is in, when it is known. */
	blockId?: string;
}

/**
 * Which retrieval path a sanitized query must take.
 *
 * The threshold itself lives in `search/query.ts` (it is shared with the knowledge service).
 * The mapping to a strategy stays on this side because the score bands below are calibrated
 * to it: a backend that chose for itself would change scores with no caller aware of it.
 */
function strategyFor(safeQuery: string): SearchStrategy {
	return canUseIndex(safeQuery) ? "index" : "substring";
}

/**
 * Relevance rank → display score, with a per-entity fallback.
 *
 * `null` means the backend had no relevance signal (the substring path), which is why the
 * caller supplies the flat band. The curve is deliberately NOT normalized inside the backend:
 * it is shared product behaviour, so two backends must not be able to disagree about which
 * hit sorts first while both "passing".
 */
function scoreFromRank(rank: number | null, fallback: number): number {
	if (rank === null) return fallback;
	return Math.max(0, Math.round(1000 - rank * 1000));
}

/**
 * Collection display names for knowledge hits, batch-loaded by id.
 *
 * One query for the whole result page rather than per row: a knowledge search can
 * return up to the hard cap, and the collection name is only used as a badge.
 */
async function collectionNameMap(collectionIds: string[]): Promise<Map<string, string>> {
	const ids = [...new Set(collectionIds.filter(Boolean))];
	if (ids.length === 0) return new Map();
	const rows = await db.query.knowledgeCollections.findMany({
		where: (c, { inArray }) => inArray(c.id, ids),
		columns: { id: true, name: true },
	});
	return new Map(rows.map((r) => [r.id, r.name]));
}

/** Shared ordering for entity results and separately resolved knowledge hits. */
export function sortSearchResults(
	results: SearchResult[],
	sort: SearchSort = "relevance",
): SearchResult[] {
	return results.sort((a, b) => {
		if (sort !== "time" && b.matchScore !== a.matchScore) return b.matchScore - a.matchScore;
		const bTime = Date.parse(b.updatedAt ?? b.createdAt ?? b.lastMessageAt ?? "") || 0;
		const aTime = Date.parse(a.updatedAt ?? a.createdAt ?? a.lastMessageAt ?? "") || 0;
		return bTime - aTime || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0);
	});
}

export const searchService = {
	async search(options: SearchOptions): Promise<SearchResult[]> {
		const {
			query,
			entities,
			limit = 50,
			principal,
			sort = "relevance",
			scope = CONVERSATION_MESSAGE_SCOPE,
		} = options;
		const results: SearchResult[] = [];

		// Sanitize: remove FTS5 special chars to prevent injection
		const safeQuery = sanitizeQuery(query);
		if (!safeQuery) return results;

		const strategy = strategyFor(safeQuery);
		const safeQueryLower = safeQuery.toLowerCase();
		// One query object for all three entity searches: the viewer travels with it, so a
		// branch cannot accidentally run ungated.
		const base = {
			text: safeQuery,
			strategy,
			limit,
			sort,
			viewer: principal,
			signal: options.signal,
			scope,
		};

		if (entities.includes("chapters")) {
			// A chapter hit exposes its title, description snippet and project name, so the
			// backend gates it by the owning project inside the query.
			for (const row of await searchStore.searchChapters(base)) {
				results.push({
					type: "chapter",
					id: row.id,
					title: row.title ?? undefined,
					projectName: row.projectName ?? undefined,
					status: row.status ?? undefined,
					role: row.role ?? undefined,
					createdAt: row.createdAt ?? undefined,
					updatedAt: row.updatedAt ?? undefined,
					matchField: row.title?.toLowerCase().includes(safeQueryLower) ? "title" : "description",
					matchScore: scoreFromRank(row.rank, strategy === "index" ? 760 : 520),
					snippet: row.snippet || row.description || "",
				});
			}
		}

		if (entities.includes("messages")) {
			const candidateLimit = messageCandidateLimit(limit);
			const rows = await searchStore.searchMessages({ ...base, limit: candidateLimit });
			const fallbackScore = strategy === "index" ? 700 : 500;
			for (const group of groupMessageHits(rows, limit)) {
				const row = group.best;
				results.push({
					type: "message",
					id: row.id,
					narratorId: row.narratorId ?? undefined,
					narratorTitle: row.narratorTitle ?? undefined,
					chapterId: row.chapterId ?? undefined,
					chapterTitle: row.chapterTitle ?? undefined,
					projectName: row.projectName ?? undefined,
					model: row.model ?? undefined,
					messageRole: row.messageRole ?? undefined,
					createdAt: row.createdAt ?? undefined,
					matchField: "message",
					matchScore: scoreFromRank(row.rank, fallbackScore),
					snippet: row.snippet || row.preview || "",
					...(row.blockId ? { blockId: row.blockId } : {}),
					hits: group.hits.map((hit) => ({
						id: hit.id,
						snippet: hit.snippet || hit.preview || "",
						...(hit.messageRole ? { messageRole: hit.messageRole } : {}),
						...(hit.createdAt ? { createdAt: hit.createdAt } : {}),
						...(hit.blockId ? { blockId: hit.blockId } : {}),
					})),
					hitCount: group.hitCount,
					hitCountIsLowerBound: rows.length >= candidateLimit,
				});
			}
		}

		if (entities.includes("narrators")) {
			for (const row of await searchStore.searchNarrators(base)) {
				results.push({
					type: "narrator",
					id: row.id,
					title: row.title ?? undefined,
					chapterId: row.chapterId ?? undefined,
					chapterTitle: row.chapterTitle ?? undefined,
					projectName: row.projectName ?? undefined,
					status: row.status ?? undefined,
					model: row.model ?? undefined,
					createdAt: row.createdAt ?? undefined,
					updatedAt: row.updatedAt ?? undefined,
					lastMessageAt: row.lastMessageAt,
					matchField: "title",
					matchScore: scoreFromRank(row.rank, strategy === "index" ? 820 : 620),
					snippet: row.snippet || row.title || "",
				});
			}
		}

		return sortSearchResults(results, sort);
	},

	/**
	 * Knowledge-base entries as global-search results.
	 *
	 * Kept separate from `search()` because its visibility gate differs in kind: knowledge
	 * visibility is a dual-axis (clearance + controlled tag) check that reads grants, so it
	 * cannot be expressed as a SQL fragment the way the narrator/project gates are. The
	 * matched rows are post-filtered by `knowledgeService.filterReadable`, which
	 * fails closed — the same gate the knowledge routes use.
	 *
	 * Results carry `updatedAt`, so the "newest" sort in the UI orders them
	 * alongside the other entity types without special-casing.
	 */
	async searchKnowledge(options: {
		query: string;
		limit?: number;
		signal?: AbortSignal;
		sort?: "time" | "relevance";
		principal: { userId: string; isAdmin: boolean };
	}): Promise<SearchResult[]> {
		const { query, limit = 50, principal } = options;
		const safeQuery = sanitizeQuery(query);
		if (!safeQuery) return [];

		// The caller's own personal versions shadow the committed one, mirroring what
		// they see in the knowledge UI: a hit on text they only have locally must not
		// be reported against the main body it replaced.
		const rows = await knowledgeService.search({
			q: safeQuery,
			limit,
			signal: options.signal,
			sort: options.sort,
			draftUserId: principal.userId || undefined,
		});
		if (rows.length === 0) return [];

		const readable = await knowledgeService.filterReadable(
			principal.userId
				? { userId: principal.userId, role: principal.isAdmin ? "admin" : "user" }
				: undefined,
			rows,
		);
		if (readable.length === 0) return [];

		const collectionNames = await collectionNameMap(
			readable.flatMap((r) => (r.collectionId ? [r.collectionId] : [])),
		);
		const safeQueryLower = safeQuery.toLowerCase();
		return readable.map((row) => ({
			type: "knowledge" as const,
			id: row.id,
			title: row.title,
			collectionId: row.collectionId ?? undefined,
			collectionName: row.collectionId ? collectionNames.get(row.collectionId) : undefined,
			tags: row.tags,
			status: row.status,
			createdAt: row.createdAt,
			updatedAt: row.updatedAt,
			matchField: row.title?.toLowerCase().includes(safeQueryLower) ? "title" : "content",
			// Knowledge search returns rank-ordered rows without exposing the raw rank,
			// so score by position within a band that keeps titles above body matches.
			matchScore: strategyFor(safeQuery) === "index" ? 780 : 540,
			snippet: row.snippet || row.title || "",
		}));
	},

	/**
	 * Full-text search within a single narrator's own conversation history.
	 * Returns messages on this narrator's timeline (newest first) with the `seq`
	 * needed to jump to each result. Bounded by `limit` (hard cap 100).
	 *
	 * `inheritedScopes` widens the search across a lazy fork's ancestry: a fork
	 * only materializes refs after its parent's last compact, so older history is
	 * still owned by an ancestor. Each scope carries the seq bound that ancestor
	 * was inherited up to, which keeps out messages the ancestor produced *after*
	 * the fork diverged. Callers obtain these from `resolveLazyLineage`; passing
	 * none reproduces the single-narrator behaviour exactly.
	 */
	async searchNarratorMessages(
		narratorId: string,
		query: string,
		limit = 60,
		inheritedScopes: InheritedSearchScope[] = [],
	): Promise<NarratorMessageSearchResult[]> {
		const safeQuery = sanitizeQuery(query);
		if (!safeQuery) return [];

		return searchStore.searchTimeline({
			narratorId,
			text: safeQuery,
			strategy: strategyFor(safeQuery),
			limit: Math.min(Math.max(1, limit), 100),
			inheritedScopes,
		});
	},
};
