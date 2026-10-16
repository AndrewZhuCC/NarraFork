/**
 * Pure routing decisions for the knowledge review UX.
 *
 * The review center and the notification bell both need to land a reviewer on the
 * verdict controls in one click, and an author on the findings for a linked draft.
 * Those decisions stay here so they can be tested without rendering the pages.
 */

export const KNOWLEDGE_ENTRY_TABS = [
	"content",
	"draft",
	"history",
	"submissions",
	"links",
] as const;

export type KnowledgeEntryTab = (typeof KNOWLEDGE_ENTRY_TABS)[number];

/** Submission ids are nanoids and may start with "-" (URL-safe alphabet). */
const SUBMISSION_ID = /^[A-Za-z0-9_-]{1,64}$/;

export interface KnowledgeEntryHashTarget {
	tab: KnowledgeEntryTab;
	/** Set when the hash names a specific submission to open in the review panel. */
	submissionId: string | null;
}

/**
 * Read `/knowledge/$entryId` hash into a tab plus an optional submission to select.
 *
 * Accepted forms (with or without a leading `#`):
 *  - empty / `content` → Content
 *  - `draft` | `history` | `links` | `submissions`
 *  - `submissions/<id>` → Submissions tab with that submission's review panel open
 */
export function parseKnowledgeEntryHash(hash: string | undefined | null): KnowledgeEntryHashTarget {
	const raw = (hash ?? "").replace(/^#/, "").trim();
	if (!raw || raw === "content") return { tab: "content", submissionId: null };
	if (raw.startsWith("submissions/")) {
		const id = decodeURIComponent(raw.slice("submissions/".length));
		return {
			tab: "submissions",
			submissionId: SUBMISSION_ID.test(id) ? id : null,
		};
	}
	if ((KNOWLEDGE_ENTRY_TABS as readonly string[]).includes(raw)) {
		return { tab: raw as KnowledgeEntryTab, submissionId: null };
	}
	return { tab: "content", submissionId: null };
}

/** Hash (no leading `#`) that opens one submission's verdict / findings panel. */
export function knowledgeSubmissionHash(submissionId: string): string {
	return `submissions/${encodeURIComponent(submissionId)}`;
}

export type KnowledgeReviewDestination =
	| { to: "entry"; entryId: string; hash: string }
	| { to: "review-center"; submissionId: string }
	| { to: "personal"; personalEntryId: string };

/**
 * One-click target for a submission.
 *
 * Linked submissions (entryId set) open the shared entry on the Submissions tab with
 * that row selected — standalone new entries have no entry page, so reviewers open
 * the Review center modal and authors stay on the personal entry (where findings render).
 */
export function knowledgeReviewDestination(input: {
	audience: "reviewer" | "author";
	entryId: string | null;
	draftId: string | null;
	submissionId: string;
}): KnowledgeReviewDestination {
	if (input.entryId) {
		return {
			to: "entry",
			entryId: input.entryId,
			hash: knowledgeSubmissionHash(input.submissionId),
		};
	}
	if (input.audience === "reviewer") {
		return { to: "review-center", submissionId: input.submissionId };
	}
	if (input.draftId) return { to: "personal", personalEntryId: input.draftId };
	return { to: "review-center", submissionId: input.submissionId };
}

/** Prefer an open review (pending / conflict); otherwise the first row. */
export function primaryReviewSubmission<T extends { status: string }>(
	subs: readonly T[],
): T | undefined {
	return subs.find((s) => s.status === "pending" || s.status === "conflict") ?? subs[0];
}

/**
 * Which explainer the entry Submissions tab should show.
 *
 * The review-scope notice answers "why can I review these?". It is misleading on an
 * author's own publish request ("you hold no review grants") — that visitor needs the
 * author hint, which points at findings, instead.
 */
export function submissionTabNotice(input: {
	isAdmin: boolean;
	reviewableCount: number;
	ownCount: number;
}): "reviewer" | "author" {
	if (input.isAdmin || input.reviewableCount > 0) return "reviewer";
	if (input.ownCount > 0) return "author";
	return "reviewer";
}

/**
 * Where a linked personal entry should land.
 *
 * Editing lives on the shared entry's Draft tab. An in-flight or already-reviewed
 * request lands on that submission so findings are not dropped by the redirect.
 */
export function linkedPersonalRedirectHash(
	rows: readonly { id: string; status: string; findingsJson?: readonly unknown[] | null }[],
): string {
	const withFindings = rows.find(
		(s) =>
			(s.findingsJson?.length ?? 0) > 0 &&
			(s.status === "changes_requested" || s.status === "rejected" || s.status === "approved"),
	);
	const open = rows.find(
		(s) => s.status === "changes_requested" || s.status === "pending" || s.status === "conflict",
	);
	const focus = withFindings ?? open;
	return focus ? knowledgeSubmissionHash(focus.id) : "draft";
}

/**
 * Submission to show in the review panel.
 *
 * An explicit hash id wins. Otherwise, a single open request is selected so a bare
 * `#submissions` link still reaches the verdict controls.
 */
export function initialSubmissionSelection(input: {
	focusSubmissionId: string | null;
	current: string | null;
	rows: readonly { id: string; status: string }[];
}): string | null {
	if (input.focusSubmissionId) return input.focusSubmissionId;
	if (input.current) return input.current;
	const actionable = input.rows.filter((s) => s.status === "pending" || s.status === "conflict");
	return actionable.length === 1 ? actionable[0].id : null;
}
