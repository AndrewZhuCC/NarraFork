import { describe, expect, test } from "bun:test";
import {
	isLegacyDraftHash,
	knowledgePersonalEntryDestination,
	knowledgeRequestDestination,
	legacyDraftHashRedirect,
	legacyEntryHashRedirect,
	legacyKnowledgeIndexRedirect,
	parseKnowledgeEntryHash,
	parseKnowledgeEntrySearch,
	parseKnowledgeReviewSearch,
} from "./knowledge-review-nav";

describe("parseKnowledgeEntryHash", () => {
	test("defaults to the content tab", () => {
		expect(parseKnowledgeEntryHash("")).toBe("content");
		expect(parseKnowledgeEntryHash(null)).toBe("content");
		expect(parseKnowledgeEntryHash("#content")).toBe("content");
		expect(parseKnowledgeEntryHash("not-a-tab")).toBe("content");
	});

	test("selects a named tab; the Submissions and My-draft tabs are gone", () => {
		expect(parseKnowledgeEntryHash("history")).toBe("history");
		expect(parseKnowledgeEntryHash("links")).toBe("links");
		expect(parseKnowledgeEntryHash("submissions")).toBe("content");
		expect(parseKnowledgeEntryHash("#draft")).toBe("content");
	});
});

// One test per §4.2 row owned by P2a.
describe("legacy URL redirects (§4.2, P2a rows)", () => {
	test("/knowledge?tab=review → /knowledge/review", () => {
		expect(legacyKnowledgeIndexRedirect({ tab: "review" })).toEqual({
			to: "/knowledge/review",
			search: {},
		});
	});

	test("/knowledge?tab=review&submission=<id> → /knowledge/requests/<id>", () => {
		expect(legacyKnowledgeIndexRedirect({ tab: "review", submission: "-T3FW1KB" })).toEqual({
			to: "/knowledge/requests/$submissionId",
			params: { submissionId: "-T3FW1KB" },
		});
		// A malformed id degrades to the inbox instead of a broken request page.
		expect(
			legacyKnowledgeIndexRedirect({ tab: "review", submission: "a/../b" as unknown }),
		).toEqual({ to: "/knowledge/review", search: {} });
	});

	test("/knowledge/$entryId#submissions/<id> → /knowledge/requests/<id>", () => {
		expect(legacyEntryHashRedirect("entry-1", "#submissions/Wl5v48sFqX9hn_NC7Y5ja")).toEqual({
			to: "/knowledge/requests/$submissionId",
			params: { submissionId: "Wl5v48sFqX9hn_NC7Y5ja" },
		});
		expect(legacyEntryHashRedirect("entry-1", "submissions/-T3FW1KB")).toEqual({
			to: "/knowledge/requests/$submissionId",
			params: { submissionId: "-T3FW1KB" },
		});
	});

	test("/knowledge/$entryId#submissions → /knowledge/review?entry=$entryId (all statuses)", () => {
		const inbox = {
			to: "/knowledge/review",
			search: { status: "all", entry: "entry-1" },
		} as const;
		expect(legacyEntryHashRedirect("entry-1", "#submissions")).toEqual(inbox);
		// Empty, oversized or undecodable ids fall back to the entry's inbox view.
		expect(legacyEntryHashRedirect("entry-1", "submissions/")).toEqual(inbox);
		expect(legacyEntryHashRedirect("entry-1", `submissions/${"a".repeat(65)}`)).toEqual(inbox);
		expect(legacyEntryHashRedirect("entry-1", "submissions/%E0%A4%A")).toEqual(inbox);
	});

	test("other URLs are left alone", () => {
		expect(legacyKnowledgeIndexRedirect({})).toBeNull();
		expect(legacyKnowledgeIndexRedirect({ tab: "admin", submission: "x" })).toBeNull();
		for (const hash of ["", "#content", "#draft", "#history", "#links", "#submissionsX"]) {
			expect(legacyEntryHashRedirect("entry-1", hash)).toBeNull();
		}
	});
});

describe("parseKnowledgeReviewSearch", () => {
	test("keeps known filters, drops the default and malformed ids", () => {
		expect(parseKnowledgeReviewSearch({})).toEqual({});
		expect(parseKnowledgeReviewSearch({ status: "open" })).toEqual({});
		expect(parseKnowledgeReviewSearch({ status: "conflict", collection: "col-1" })).toEqual({
			status: "conflict",
			collection: "col-1",
		});
		expect(parseKnowledgeReviewSearch({ status: "approved", entry: "../x" })).toEqual({});
	});
});

describe("knowledgeRequestDestination", () => {
	test("linked and standalone requests, for reviewers and authors, open the same page", () => {
		expect(knowledgeRequestDestination("-T3FW1KB")).toEqual({
			to: "/knowledge/requests/$submissionId",
			params: { submissionId: "-T3FW1KB" },
		});
	});
});

// One test per §4.2 row owned by P2b.
describe("legacy URL redirects (§4.2, P2b rows)", () => {
	test("/knowledge?tab=mylibrary → /knowledge/mine", () => {
		expect(legacyKnowledgeIndexRedirect({ tab: "mylibrary" })).toEqual({ to: "/knowledge/mine" });
	});

	test("/knowledge/$entryId#draft → /knowledge/mine/<draftId> when a personal version exists", () => {
		expect(legacyDraftHashRedirect("entry-1", "#draft", "-draft_1")).toEqual({
			to: "/knowledge/mine/$personalEntryId",
			params: { personalEntryId: "-draft_1" },
		});
	});

	test("/knowledge/$entryId#draft → entry page with the create prompt when there is none", () => {
		const prompt = {
			to: "/knowledge/$entryId",
			params: { entryId: "entry-1" },
			search: { personal: "create" },
		} as const;
		expect(legacyDraftHashRedirect("entry-1", "draft", null)).toEqual(prompt);
		expect(legacyDraftHashRedirect("entry-1", "#draft", undefined)).toEqual(prompt);
		// Other hashes are not this row's business.
		for (const hash of ["", "#content", "#history", "#drafts", "#submissions"]) {
			expect(legacyDraftHashRedirect("entry-1", hash, "d")).toBeNull();
			expect(isLegacyDraftHash(hash)).toBe(false);
		}
		expect(isLegacyDraftHash("#draft")).toBe(true);
	});

	test("/knowledge/personal/$id → /knowledge/mine/$id (linked and standalone alike)", () => {
		expect(knowledgePersonalEntryDestination("Wl5v48sFqX9hn_NC7Y5ja")).toEqual({
			to: "/knowledge/mine/$personalEntryId",
			params: { personalEntryId: "Wl5v48sFqX9hn_NC7Y5ja" },
		});
	});
});

describe("parseKnowledgeEntrySearch", () => {
	test("only the create prompt survives", () => {
		expect(parseKnowledgeEntrySearch({})).toEqual({});
		expect(parseKnowledgeEntrySearch({ personal: "create" })).toEqual({ personal: "create" });
		expect(parseKnowledgeEntrySearch({ personal: "delete" })).toEqual({});
	});
});
