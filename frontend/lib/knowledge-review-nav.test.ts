import { describe, expect, test } from "bun:test";
import {
	entryEditRedirect,
	isLegacyDraftHash,
	knowledgePersonalEntryDestination,
	knowledgeRequestDestination,
	legacyDraftHashRedirect,
	legacyEntryHashRedirect,
	legacyEntryTabRedirect,
	legacyKnowledgeIndexRedirect,
	parseKnowledgeEntrySearch,
	parseKnowledgeReviewSearch,
} from "./knowledge-review-nav";

// One test per §4.2 row owned by P2c: the entry tabs became pages.
describe("legacy URL redirects (§4.2, P2c rows)", () => {
	test("/knowledge/$entryId#content → /knowledge/$entryId", () => {
		expect(legacyEntryTabRedirect("entry-1", "#content")).toEqual({
			to: "/knowledge/$entryId",
			params: { entryId: "entry-1" },
		});
	});

	test("/knowledge/$entryId#history → /knowledge/$entryId/history", () => {
		expect(legacyEntryTabRedirect("entry-1", "history")).toEqual({
			to: "/knowledge/$entryId/history",
			params: { entryId: "entry-1" },
		});
	});

	test("/knowledge/$entryId#links → /knowledge/$entryId#related (Related entries)", () => {
		expect(legacyEntryTabRedirect("entry-1", "#links")).toEqual({
			to: "/knowledge/$entryId",
			params: { entryId: "entry-1" },
			hash: "related",
		});
	});

	test("other hashes are left alone (no redirect loop on #related)", () => {
		for (const hash of ["", null, "#related", "#draft", "#submissions", "#submissions/x", "#x"]) {
			expect(legacyEntryTabRedirect("entry-1", hash)).toBeNull();
		}
	});
});

describe("entryEditRedirect", () => {
	test("only direct writers stay on /edit; everyone else reads", () => {
		expect(entryEditRedirect("entry-1", true)).toBeNull();
		const read = { to: "/knowledge/$entryId", params: { entryId: "entry-1" } } as const;
		expect(entryEditRedirect("entry-1", false)).toEqual(read);
		// Unknown (entry unreadable / failed to load) fails closed to the read page.
		expect(entryEditRedirect("entry-1", null)).toEqual(read);
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
		expect(legacyKnowledgeIndexRedirect({ tab: "entries" })).toBeNull();
		expect(legacyKnowledgeIndexRedirect({ collection: "col-1", q: "x" })).toBeNull();
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

// One test per §4.2 row owned by P2d.
describe("legacy URL redirects (§4.2, P2d rows)", () => {
	test("/knowledge?tab=admin → /knowledge/admin", () => {
		expect(legacyKnowledgeIndexRedirect({ tab: "admin" })).toEqual({ to: "/knowledge/admin" });
		// Stray params from the old tab URL do not change the destination.
		expect(legacyKnowledgeIndexRedirect({ tab: "admin", submission: "x" })).toEqual({
			to: "/knowledge/admin",
		});
	});
});
