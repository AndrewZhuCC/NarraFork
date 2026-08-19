import { describe, expect, test } from "bun:test";
import { isSpecCommentStale } from "./CommentDraftChips";
import { createCommentDraft } from "./comment-draft-store";

describe("isSpecCommentStale", () => {
	test("is false for chat drafts", () => {
		const draft = createCommentDraft({
			source: "chat",
			excerpt: "hi",
			comment: "note",
			messageId: "m1",
			revisionId: "old",
			uri: "spec://index.md",
		});
		expect(isSpecCommentStale(draft, new Map([["spec://index.md", "new"]]))).toBe(false);
	});

	test("is false when the current revision still matches", () => {
		const draft = createCommentDraft({
			source: "spec",
			excerpt: "hi",
			comment: "note",
			uri: "spec://index.md",
			revisionId: "rev-1",
		});
		expect(isSpecCommentStale(draft, new Map([["spec://index.md", "rev-1"]]))).toBe(false);
	});

	test("is true when the spec file revision has moved on", () => {
		const draft = createCommentDraft({
			source: "spec",
			excerpt: "hi",
			comment: "note",
			uri: "spec://index.md",
			revisionId: "rev-1",
		});
		expect(isSpecCommentStale(draft, new Map([["spec://index.md", "rev-2"]]))).toBe(true);
	});

	test("is false when the live revision is unknown", () => {
		const draft = createCommentDraft({
			source: "spec",
			excerpt: "hi",
			comment: "note",
			uri: "spec://index.md",
			revisionId: "rev-1",
		});
		expect(isSpecCommentStale(draft, new Map())).toBe(false);
		expect(isSpecCommentStale(draft)).toBe(false);
	});
});
