/**
 * Request page action sets per viewer (§9: author / reviewer / admin / unrelated reader).
 *
 * The caps fixtures are what `GET /submissions/:id` returns for each viewer — the server side
 * of that mapping is pinned by server/services/__tests__/knowledge-submission-viewer-caps.test.ts.
 * This pins the client side: the page offers exactly what the caps allow, nothing inferred.
 */
import { describe, expect, test } from "bun:test";
import type { KnowledgeSubmissionViewerCaps } from "../../lib/api";
import { requestActions } from "./request-actions";

const none: KnowledgeSubmissionViewerCaps = {
	isSubmitter: false,
	canReview: false,
	canApprove: false,
	canResolveConflict: false,
	canWithdraw: false,
	canResubmit: false,
};

describe("requestActions", () => {
	test("pending request", () => {
		// Author: withdraw + edit their version; never a verdict on their own request.
		expect(requestActions({ ...none, isSubmitter: true, canWithdraw: true })).toEqual({
			verdicts: [],
			resolveConflict: false,
			withdraw: true,
			resubmit: false,
			editVersion: true,
			readOnly: false,
		});
		// Reviewer: the full verdict set, no author actions.
		expect(requestActions({ ...none, canReview: true, canApprove: true })).toEqual({
			verdicts: ["approve", "request_changes", "reject", "comment_only"],
			resolveConflict: false,
			withdraw: false,
			resubmit: false,
			editVersion: false,
			readOnly: false,
		});
		// Admin (not the submitter): reviews and may also withdraw on the author's behalf.
		const admin = requestActions({ ...none, canReview: true, canApprove: true, canWithdraw: true });
		expect(admin.verdicts).toContain("approve");
		expect(admin.withdraw).toBe(true);
		expect(admin.editVersion).toBe(false);
		// Anyone else who can open it: read-only.
		expect(requestActions(none).readOnly).toBe(true);
	});

	test("conflict: reviewers resolve instead of approving", () => {
		const reviewer = requestActions({ ...none, canReview: true, canResolveConflict: true });
		expect(reviewer.verdicts).toEqual(["request_changes", "reject", "comment_only"]);
		expect(reviewer.resolveConflict).toBe(true);
	});

	test("changes requested: the author re-submits or withdraws", () => {
		const author = requestActions({
			...none,
			isSubmitter: true,
			canWithdraw: true,
			canResubmit: true,
		});
		expect(author).toMatchObject({ verdicts: [], withdraw: true, resubmit: true, readOnly: false });
	});

	test("closed request: the author can still open their version, a reviewer only reads", () => {
		expect(requestActions({ ...none, isSubmitter: true })).toMatchObject({
			editVersion: true,
			readOnly: false,
		});
		expect(requestActions(none).readOnly).toBe(true);
	});
});
