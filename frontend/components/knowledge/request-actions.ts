/**
 * Which actions the request page offers, derived ONLY from the server's `viewerCaps`
 * (docs/KNOWLEDGE_UI_REDESIGN.md §5.7). Pure so the per-role action sets are unit-tested.
 */
import type { KnowledgeSubmissionViewerCaps, KnowledgeVerdict } from "../../lib/api";

export interface RequestActionSet {
	/** Verdicts the review form offers (approve only while the server accepts it). */
	verdicts: KnowledgeVerdict[];
	resolveConflict: boolean;
	withdraw: boolean;
	resubmit: boolean;
	/** "Edit my version" — only the submitter owns the personal version. */
	editVersion: boolean;
	/** Nothing to do: the page is read-only for this viewer. */
	readOnly: boolean;
}

export function requestActions(caps: KnowledgeSubmissionViewerCaps): RequestActionSet {
	const verdicts: KnowledgeVerdict[] = caps.canReview
		? [
				...(caps.canApprove ? (["approve"] as const) : []),
				"request_changes",
				"reject",
				"comment_only",
			]
		: [];
	const set = {
		verdicts,
		resolveConflict: caps.canResolveConflict,
		withdraw: caps.canWithdraw,
		resubmit: caps.canResubmit,
		editVersion: caps.isSubmitter,
	};
	return {
		...set,
		readOnly:
			verdicts.length === 0 &&
			!set.resolveConflict &&
			!set.withdraw &&
			!set.resubmit &&
			!set.editVersion,
	};
}
