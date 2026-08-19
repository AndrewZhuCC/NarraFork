import { describe, expect, test } from "bun:test";
import {
	addCommentDraft,
	type CommentDraft,
	clearAllCommentDrafts,
	clearCommentDrafts,
	composeUserMessageWithComments,
	createCommentDraft,
	formatCommentDraftsForSend,
	getCommentDrafts,
	removeCommentDraft,
	setCommentDrafts,
} from "./comment-draft-store";

function specDraft(overrides: Partial<CommentDraft> = {}): CommentDraft {
	return createCommentDraft({
		source: "spec",
		excerpt: "do not persist comments",
		comment: "keep this as a session draft",
		uri: "spec://index.md",
		revisionId: "rev-1",
		headingPath: ["Goals"],
		...overrides,
	});
}

function chatDraft(overrides: Partial<CommentDraft> = {}): CommentDraft {
	return createCommentDraft({
		source: "chat",
		excerpt: "the assistant said this",
		comment: "please rephrase",
		messageId: "m1",
		messageSeq: 42,
		role: "assistant",
		blockId: "msg-m1-0",
		blockIndex: 0,
		...overrides,
	});
}

describe("comment draft store", () => {
	test("isolates drafts per narrator", () => {
		clearAllCommentDrafts();
		addCommentDraft("n-a", specDraft({ id: "a1" }));
		addCommentDraft("n-b", chatDraft({ id: "b1" }));

		expect(getCommentDrafts("n-a").map((d) => d.id)).toEqual(["a1"]);
		expect(getCommentDrafts("n-b").map((d) => d.id)).toEqual(["b1"]);
		expect(getCommentDrafts("missing")).toEqual([]);
	});

	test("removeDraft drops one entry and leaves the rest", () => {
		clearAllCommentDrafts();
		addCommentDraft("n-a", specDraft({ id: "a1" }));
		addCommentDraft("n-a", chatDraft({ id: "a2" }));

		removeCommentDraft("n-a", "a1");
		expect(getCommentDrafts("n-a").map((d) => d.id)).toEqual(["a2"]);
	});

	test("clear returns the previous list so a failed send can restore", () => {
		clearAllCommentDrafts();
		const first = addCommentDraft("n-a", specDraft({ id: "a1" }));
		if (!first) throw new Error("expected a created draft");
		const previous = clearCommentDrafts("n-a");

		expect(previous).toEqual([first]);
		expect(getCommentDrafts("n-a")).toEqual([]);

		setCommentDrafts("n-a", previous);
		expect(getCommentDrafts("n-a")).toEqual(previous);
	});

	test("fills an id when the caller omits one", () => {
		clearAllCommentDrafts();
		const created = addCommentDraft("n-a", {
			source: "chat",
			excerpt: "hi",
			comment: "note",
		});
		if (!created) throw new Error("expected a created draft");
		expect(created.id).toBeTruthy();
		expect(getCommentDrafts("n-a")[0]?.id).toBe(created.id);
	});

	test("ignores an empty narratorId", () => {
		clearAllCommentDrafts();
		expect(addCommentDraft("", specDraft({ id: "x" }))).toBeNull();
		expect(getCommentDrafts("")).toEqual([]);
	});
});

describe("comment packing for send", () => {
	test("formats spec and chat drafts into one <comments> block", () => {
		const packed = formatCommentDraftsForSend([
			specDraft({
				id: "s1",
				excerpt: "line one\nline two",
				comment: "spec note",
				revisionId: undefined,
			}),
			chatDraft({ id: "c1" }),
		]);

		expect(packed).toBe(
			[
				"<comments>",
				"[spec] spec://index.md · Goals",
				"> line one",
				"> line two",
				"spec note",
				"",
				"[chat] assistant · seq 42",
				"> the assistant said this",
				"please rephrase",
				"</comments>",
			].join("\n"),
		);
	});

	test("omits seq when the chat message has none", () => {
		const packed = formatCommentDraftsForSend([
			chatDraft({ id: "c1", messageSeq: undefined, role: "user" }),
		]);
		expect(packed).toContain("[chat] user\n");
		expect(packed).not.toContain("seq");
	});

	test("prepends comments to a typed body, and allows comments-only", () => {
		const drafts = [chatDraft({ id: "c1", revisionId: undefined })];
		expect(composeUserMessageWithComments("  please look  ", drafts)).toBe(
			`${formatCommentDraftsForSend(drafts)}\n\nplease look`,
		);
		expect(composeUserMessageWithComments("   ", drafts)).toBe(formatCommentDraftsForSend(drafts));
		expect(composeUserMessageWithComments("hello", [])).toBe("hello");
	});
});
