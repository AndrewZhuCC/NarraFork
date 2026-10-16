/**
 * Pure routing decisions for the knowledge UX (docs/KNOWLEDGE_UI_REDESIGN.md §4).
 *
 * Every publish request — linked or standalone, for its author or a reviewer — has one page,
 * `/knowledge/requests/$submissionId`; every personal version — linked or standalone — has one
 * page, `/knowledge/mine/$personalEntryId`; an entry's history / edit / settings are sub-pages
 * of `/knowledge/$entryId`. The legacy URLs (review / my-library tabs, entry `#submissions`,
 * `#draft`, `#content`, `#history` and `#links` hashes, `/knowledge/personal/<id>`, the admin
 * tab) redirect
 * there; the parsing lives here so each redirect row is unit-tested.
 */

/** Submission ids are nanoids and may start with "-" (URL-safe alphabet). */
const SUBMISSION_ID = /^[A-Za-z0-9_-]{1,64}$/;

export function isKnowledgeSubmissionId(id: unknown): id is string {
	return typeof id === "string" && SUBMISSION_ID.test(id);
}

/** Anchor of the read page's "Related entries" section (target of the legacy `#links`). */
export const KNOWLEDGE_RELATED_ANCHOR = "related";

/** The read page, optionally scrolled to an anchor. */
export interface KnowledgeEntryReadDestination {
	to: "/knowledge/$entryId";
	params: { entryId: string };
	hash?: string;
}

/**
 * Legacy entry tab hashes (§4.2) — the tabs became pages:
 *  - `#content` → `/knowledge/$entryId` (the read page, no hash)
 *  - `#history` → `/knowledge/$entryId/history`
 *  - `#links` → `/knowledge/$entryId#related` (the read page, scrolled to "Related entries")
 * Anything else (incl. `#related` itself, `#draft`, `#submissions…`) → null.
 */
export function legacyEntryTabRedirect(
	entryId: string,
	hash: string | undefined | null,
):
	| KnowledgeEntryReadDestination
	| { to: "/knowledge/$entryId/history"; params: { entryId: string } }
	| null {
	const raw = (hash ?? "").replace(/^#/, "").trim();
	if (raw === "content") return { to: "/knowledge/$entryId", params: { entryId } };
	if (raw === "history") return { to: "/knowledge/$entryId/history", params: { entryId } };
	if (raw === "links") {
		return { to: "/knowledge/$entryId", params: { entryId }, hash: KNOWLEDGE_RELATED_ANCHOR };
	}
	return null;
}

/**
 * `/knowledge/$entryId/edit` is only for callers who may write main directly
 * (`viewerCaps.canWriteMain`); everyone else — and an entry that could not be read
 * (`canWriteMain` unknown) — lands on the read page instead.
 */
export function entryEditRedirect(
	entryId: string,
	canWriteMain: boolean | null | undefined,
): KnowledgeEntryReadDestination | null {
	return canWriteMain ? null : { to: "/knowledge/$entryId", params: { entryId } };
}

/** A redirect target for a legacy knowledge URL. */
export type KnowledgeLegacyRedirect =
	| { to: "/knowledge/review"; search: KnowledgeReviewSearch }
	| { to: "/knowledge/requests/$submissionId"; params: { submissionId: string } }
	| { to: "/knowledge/mine" }
	| { to: "/knowledge/admin" }
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
 *  - `tab=admin` → `/knowledge/admin` (whose layout sends non-admins back to `/knowledge`)
 * Anything else stays on the browse page (null).
 */
export function legacyKnowledgeIndexRedirect(
	search: Record<string, unknown>,
): KnowledgeLegacyRedirect | null {
	if (search.tab === "mylibrary") return { to: "/knowledge/mine" };
	if (search.tab === "admin") return { to: "/knowledge/admin" };
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
 * Other hashes are not this function's business (null).
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
