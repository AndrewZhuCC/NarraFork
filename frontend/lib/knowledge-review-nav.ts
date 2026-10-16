/**
 * Pure routing decisions for the knowledge review UX (docs/KNOWLEDGE_UI_REDESIGN.md §4).
 *
 * Every publish request — linked or standalone, for its author or a reviewer — has one page,
 * `/knowledge/requests/$submissionId`. The legacy URLs (review tab, entry `#submissions` hash)
 * redirect there; the parsing lives here so each redirect row is unit-tested.
 */

export const KNOWLEDGE_ENTRY_TABS = ["content", "draft", "history", "links"] as const;

export type KnowledgeEntryTab = (typeof KNOWLEDGE_ENTRY_TABS)[number];

/** Submission ids are nanoids and may start with "-" (URL-safe alphabet). */
const SUBMISSION_ID = /^[A-Za-z0-9_-]{1,64}$/;

export function isKnowledgeSubmissionId(id: unknown): id is string {
	return typeof id === "string" && SUBMISSION_ID.test(id);
}

/**
 * Read the `/knowledge/$entryId` hash into a tab. Empty, `content` or anything unknown →
 * Content. (`#submissions…` never reaches the page: {@link legacyEntryHashRedirect} sends it to
 * the review inbox / request page first.)
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
	| { to: "/knowledge/requests/$submissionId"; params: { submissionId: string } };

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
 * Legacy `/knowledge?tab=review[&submission=<id>]` (§4.2):
 *  - `tab=review&submission=<id>` → `/knowledge/requests/<id>`
 *  - `tab=review` (or a malformed id) → `/knowledge/review`
 * Anything else stays on the browse page (null).
 */
export function legacyKnowledgeIndexRedirect(
	search: Record<string, unknown>,
): KnowledgeLegacyRedirect | null {
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
 * review inbox, "my library" and personal-entry histories. One page for every audience.
 */
export function knowledgeRequestDestination(submissionId: string): {
	to: "/knowledge/requests/$submissionId";
	params: { submissionId: string };
} {
	return { to: "/knowledge/requests/$submissionId", params: { submissionId } };
}

/**
 * Where a LINKED personal entry's legacy page (`/knowledge/personal/<id>`) should land.
 *
 * Editing lives on the shared entry's Draft tab. An in-flight or already-reviewed request lands
 * on that request's page so its findings are not dropped by the redirect.
 */
export function linkedPersonalRedirect(
	rows: readonly { id: string; status: string; findingsJson?: readonly unknown[] | null }[],
): { to: "request"; submissionId: string } | { to: "draft" } {
	const withFindings = rows.find(
		(s) =>
			(s.findingsJson?.length ?? 0) > 0 &&
			(s.status === "changes_requested" || s.status === "rejected" || s.status === "approved"),
	);
	const open = rows.find(
		(s) => s.status === "changes_requested" || s.status === "pending" || s.status === "conflict",
	);
	const focus = withFindings ?? open;
	return focus ? { to: "request", submissionId: focus.id } : { to: "draft" };
}
