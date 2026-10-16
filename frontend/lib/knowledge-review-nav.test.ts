import { describe, expect, test } from "bun:test";
import {
	initialSubmissionSelection,
	knowledgeReviewDestination,
	knowledgeSubmissionHash,
	linkedPersonalRedirectHash,
	parseKnowledgeEntryHash,
	primaryReviewSubmission,
	submissionTabNotice,
} from "./knowledge-review-nav";

describe("parseKnowledgeEntryHash", () => {
	test("defaults to the content tab", () => {
		expect(parseKnowledgeEntryHash("")).toEqual({ tab: "content", submissionId: null });
		expect(parseKnowledgeEntryHash(null)).toEqual({ tab: "content", submissionId: null });
		expect(parseKnowledgeEntryHash("#content")).toEqual({ tab: "content", submissionId: null });
		expect(parseKnowledgeEntryHash("not-a-tab")).toEqual({ tab: "content", submissionId: null });
	});

	test("selects a named tab", () => {
		expect(parseKnowledgeEntryHash("submissions")).toEqual({
			tab: "submissions",
			submissionId: null,
		});
		expect(parseKnowledgeEntryHash("#draft")).toEqual({ tab: "draft", submissionId: null });
		expect(parseKnowledgeEntryHash("history")).toEqual({ tab: "history", submissionId: null });
		expect(parseKnowledgeEntryHash("links")).toEqual({ tab: "links", submissionId: null });
	});

	test("opens a specific submission, including ids that start with a hyphen", () => {
		expect(parseKnowledgeEntryHash("submissions/-T3FW1KB")).toEqual({
			tab: "submissions",
			submissionId: "-T3FW1KB",
		});
		expect(parseKnowledgeEntryHash("#submissions/Wl5v48sFqX9hn_NC7Y5ja")).toEqual({
			tab: "submissions",
			submissionId: "Wl5v48sFqX9hn_NC7Y5ja",
		});
		expect(parseKnowledgeEntryHash(knowledgeSubmissionHash("-T3FW1KB"))).toEqual({
			tab: "submissions",
			submissionId: "-T3FW1KB",
		});
	});

	test("ignores an empty or oversized submission id but still opens the tab", () => {
		expect(parseKnowledgeEntryHash("submissions/")).toEqual({
			tab: "submissions",
			submissionId: null,
		});
		expect(parseKnowledgeEntryHash(`submissions/${"a".repeat(65)}`)).toEqual({
			tab: "submissions",
			submissionId: null,
		});
	});
});

describe("knowledgeReviewDestination", () => {
	test("linked submissions open the shared entry review panel for both audiences", () => {
		const reviewer = knowledgeReviewDestination({
			audience: "reviewer",
			entryId: "entry-1",
			draftId: "draft-1",
			submissionId: "-T3FW1KB",
		});
		const author = knowledgeReviewDestination({
			audience: "author",
			entryId: "entry-1",
			draftId: "draft-1",
			submissionId: "-T3FW1KB",
		});
		expect(reviewer).toEqual({
			to: "entry",
			entryId: "entry-1",
			hash: "submissions/-T3FW1KB",
		});
		expect(author).toEqual(reviewer);
	});

	test("standalone submissions stay in the review center for reviewers and on the personal entry for authors", () => {
		expect(
			knowledgeReviewDestination({
				audience: "reviewer",
				entryId: null,
				draftId: "draft-1",
				submissionId: "sub-1",
			}),
		).toEqual({ to: "review-center", submissionId: "sub-1" });
		expect(
			knowledgeReviewDestination({
				audience: "author",
				entryId: null,
				draftId: "draft-1",
				submissionId: "sub-1",
			}),
		).toEqual({ to: "personal", personalEntryId: "draft-1" });
	});
});

describe("primaryReviewSubmission", () => {
	test("prefers an open pending or conflict row over a newer closed one", () => {
		const rows = [
			{ id: "closed", status: "approved" },
			{ id: "open", status: "pending" },
		];
		expect(primaryReviewSubmission(rows)?.id).toBe("open");
		expect(primaryReviewSubmission([{ id: "conflict", status: "conflict" }])?.id).toBe("conflict");
		expect(primaryReviewSubmission([])).toBeUndefined();
	});
});

describe("submissionTabNotice", () => {
	test("authors looking at their own requests do not get the reviewer-scope explainer", () => {
		expect(submissionTabNotice({ isAdmin: false, reviewableCount: 0, ownCount: 1 })).toBe("author");
		expect(submissionTabNotice({ isAdmin: false, reviewableCount: 2, ownCount: 1 })).toBe(
			"reviewer",
		);
		expect(submissionTabNotice({ isAdmin: true, reviewableCount: 0, ownCount: 1 })).toBe(
			"reviewer",
		);
		expect(submissionTabNotice({ isAdmin: false, reviewableCount: 0, ownCount: 0 })).toBe(
			"reviewer",
		);
	});
});

describe("linkedPersonalRedirectHash", () => {
	test("lands on the submission that carries findings", () => {
		expect(
			linkedPersonalRedirectHash([
				{ id: "pending", status: "pending", findingsJson: [] },
				{
					id: "bounced",
					status: "changes_requested",
					findingsJson: [{ message: "spell out the grant" }],
				},
			]),
		).toBe("submissions/bounced");
	});

	test("lands on an open request when nothing has findings yet", () => {
		expect(linkedPersonalRedirectHash([{ id: "open", status: "pending" }])).toBe(
			"submissions/open",
		);
	});

	test("lands on the draft tab when there is nothing to review", () => {
		expect(linkedPersonalRedirectHash([])).toBe("draft");
		expect(linkedPersonalRedirectHash([{ id: "old", status: "withdrawn", findingsJson: [] }])).toBe(
			"draft",
		);
	});
});

describe("initialSubmissionSelection", () => {
	test("hash id wins, otherwise the sole open request is selected", () => {
		const rows = [
			{ id: "a", status: "pending" },
			{ id: "b", status: "approved" },
		];
		expect(initialSubmissionSelection({ focusSubmissionId: "b", current: null, rows })).toBe("b");
		expect(initialSubmissionSelection({ focusSubmissionId: null, current: null, rows })).toBe("a");
		expect(
			initialSubmissionSelection({
				focusSubmissionId: null,
				current: null,
				rows: [
					{ id: "a", status: "pending" },
					{ id: "c", status: "conflict" },
				],
			}),
		).toBeNull();
		expect(initialSubmissionSelection({ focusSubmissionId: null, current: "a", rows })).toBe("a");
	});
});
