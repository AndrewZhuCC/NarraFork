/**
 * Pure routing decisions for the knowledge UX (docs/KNOWLEDGE_UI_REDESIGN.md §4).
 *
 * Every publish request — linked or standalone, for its author or a reviewer — has one page,
 * `/knowledge/requests/$submissionId`; every personal version — linked or standalone — has one
 * page, `/knowledge/mine/$personalEntryId`. The legacy URLs (review / my-library tabs, entry
 * `#submissions` and `#draft` hashes, `/knowledge/personal/<id>`) redirect there; the parsing
 * lives here so each redirect row is unit-tested.
 */

export const KNOWLEDGE_ENTRY_TABS = ["content", "history", "links"] as const;

export type KnowledgeEntryTab = (typeof KNOWLEDGE_ENTRY_TABS)[number];

/** Submission ids are nanoids and may start with "-" (URL-safe alphabet). */
const SUBMISSION_ID = /^[A-Za-z0-9_-]{1,64}$/;

export function isKnowledgeSubmissionId(id: unknown): id is string {
	return typeof id === "string" && SUBMISSION_ID.test(id);
}

/**
 * Read the `/knowledge/$entryId` hash into a tab. Empty, `content` or anything unknown →
 * Content. (`#submissions…` and `#draft` never reach the page: {@link legacyEntryHashRedirect}
 * and {@link legacyDraftHashRedirect} send them to the request / personal-version pages first.)
 */
export function parseKnowledgeEntryHash(hash: string | undefined | null): KnowledgeEntryTab {
	const raw = (hash ?? "").replace(/^#/, "").trim();
	return (KNOWLEDGE_ENTRY_TABS as readonly string[]).includes(raw)
		? (raw as KnowledgeEntryTab)
		: "content";
}

/** A redirect target for a legacy knowledge URL. */
export type KnowledgeLegacyRedirect =
	| { to: "/knowledge/review"; search: KnowledgeReviewSearch }
	| { to: "/knowledge/requests/$submissionId"; params: { submissionId: string } }
	| { to: "/knowledge/mine" }
	| KnowledgePersonalEntryDestination;

/** Search params of the review inbox. */
export interface KnowledgeReviewSearch {
	status?: KnowledgeReviewStatusFilter;
	collection?: string;
	entry?: string;
}

/**
 * `open` (default) = pending + conflict — everything awaiting a reviewer, the same set the nav
 * badge counts. `all` includes closed requests (the old Review Center "All").
 */
export const KNOWLEDGE_REVIEW_STATUS_FILTERS = ["open", "pending", "conflict", "all"] as const;
export type KnowledgeReviewStatusFilter = (typeof KNOWLEDGE_REVIEW_STATUS_FILTERS)[number];

/** Entry / collection ids in search params are nanoids too. */
const KNOWLEDGE_ID = SUBMISSION_ID;

/**
 * `validateSearch` for `/knowledge/review`. The default filter (open) is left out of the URL;
 * malformed ids are dropped rather than sent to the server.
 */
export function parseKnowledgeReviewSearch(search: Record<string, unknown>): KnowledgeReviewSearch {
	const status = (KNOWLEDGE_REVIEW_STATUS_FILTERS as readonly unknown[]).includes(search.status)
		? (search.status as KnowledgeReviewStatusFilter)
		: undefined;
	const collection =
		typeof search.collection === "string" && KNOWLEDGE_ID.test(search.collection)
			? search.collection
			: undefined;
	const entry =
		typeof search.entry === "string" && KNOWLEDGE_ID.test(search.entry) ? search.entry : undefined;
	return {
		...(status && status !== "open" ? { status } : {}),
		...(collection ? { collection } : {}),
		...(entry ? { entry } : {}),
	};
}

/**
 * Legacy `/knowledge?tab=…` (§4.2):
 *  - `tab=review&submission=<id>` → `/knowledge/requests/<id>`
 *  - `tab=review` (or a malformed id) → `/knowledge/review`
 *  - `tab=mylibrary` → `/knowledge/mine`
 * Anything else stays on the browse page (null).
 */
export function legacyKnowledgeIndexRedirect(
	search: Record<string, unknown>,
): KnowledgeLegacyRedirect | null {
	if (search.tab === "mylibrary") return { to: "/knowledge/mine" };
	if (search.tab !== "review") return null;
	if (isKnowledgeSubmissionId(search.submission)) {
		return {
			to: "/knowledge/requests/$submissionId",
			params: { submissionId: search.submission },
		};
	}
	return { to: "/knowledge/review", search: {} };
}

/**
 * Legacy `/knowledge/$entryId#submissions[/<id>]` (§4.2):
 *  - `#submissions/<id>` → `/knowledge/requests/<id>`
 *  - `#submissions` (or a malformed id) → `/knowledge/review?entry=<entryId>&status=all`
 *    (the entry's requests the caller may review, any status — what the old tab listed)
 * Other hashes are entry tabs (null).
 */
export function legacyEntryHashRedirect(
	entryId: string,
	hash: string | undefined | null,
): KnowledgeLegacyRedirect | null {
	const raw = (hash ?? "").replace(/^#/, "").trim();
	if (raw !== "submissions" && !raw.startsWith("submissions/")) return null;
	if (raw.startsWith("submissions/")) {
		let id: string | null = null;
		try {
			id = decodeURIComponent(raw.slice("submissions/".length));
		} catch {
			id = null;
		}
		if (isKnowledgeSubmissionId(id)) {
			return { to: "/knowledge/requests/$submissionId", params: { submissionId: id } };
		}
	}
	return {
		to: "/knowledge/review",
		search: parseKnowledgeReviewSearch({ entry: entryId, status: "all" }),
	};
}

/**
 * Where a submission row opens — the bell (reviewer inbox rows and author verdicts alike), the
 * review inbox, "My" and personal-version histories. One page for every audience.
 */
export function knowledgeRequestDestination(submissionId: string): {
	to: "/knowledge/requests/$submissionId";
	params: { submissionId: string };
} {
	return { to: "/knowledge/requests/$submissionId", params: { submissionId } };
}

/** The one page of a personal version (linked or standalone). */
export interface KnowledgePersonalEntryDestination {
	to: "/knowledge/mine/$personalEntryId";
	params: { personalEntryId: string };
}

/**
 * Where a personal version opens — "My" rows, the entry page's "my version" button, the request
 * page's "edit my version", the narrator dock and knowledge hints. Also the legacy
 * `/knowledge/personal/<id>` redirect (§4.2), for linked and standalone entries alike.
 */
export function knowledgePersonalEntryDestination(
	personalEntryId: string,
): KnowledgePersonalEntryDestination {
	return { to: "/knowledge/mine/$personalEntryId", params: { personalEntryId } };
}

/** Is this `/knowledge/$entryId` hash the legacy "My draft" tab? */
export function isLegacyDraftHash(hash: string | undefined | null): boolean {
	return (hash ?? "").replace(/^#/, "").trim() === "draft";
}

/** Search params of the entry page (the `#draft` fallback below is the only one). */
export interface KnowledgeEntrySearch {
	/** `create` → open the "create personal version" confirmation on arrival. */
	personal?: "create";
}

export function parseKnowledgeEntrySearch(search: Record<string, unknown>): KnowledgeEntrySearch {
	return search.personal === "create" ? { personal: "create" } : {};
}

/**
 * Legacy `/knowledge/$entryId#draft` (§4.2), once the caller's personal version id is known
 * (`viewerCaps.myPersonalEntryId`):
 *  - a personal version exists → `/knowledge/mine/<id>`
 *  - none → the entry page itself with `?personal=create`, which opens the create confirmation
 *    (creating is a write, so it is never done by a redirect)
 * Other hashes → null.
 */
export function legacyDraftHashRedirect(
	entryId: string,
	hash: string | undefined | null,
	myPersonalEntryId: string | null | undefined,
):
	| KnowledgePersonalEntryDestination
	| { to: "/knowledge/$entryId"; params: { entryId: string }; search: KnowledgeEntrySearch }
	| null {
	if (!isLegacyDraftHash(hash)) return null;
	if (myPersonalEntryId) return knowledgePersonalEntryDestination(myPersonalEntryId);
	return { to: "/knowledge/$entryId", params: { entryId }, search: { personal: "create" } };
}
