import { describe, expect, test } from "bun:test";
import { MantineProvider } from "@mantine/core";
import { createInstance } from "i18next";
import { parseHTML } from "linkedom";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { I18nextProvider } from "react-i18next";
import { CommentDraftChips, isSpecCommentStale } from "./CommentDraftChips";
import { createCommentDraft } from "./comment-draft-store";

test("file chips show source details with edit and remove controls", async () => {
	const i18n = createInstance();
	await i18n.init({
		lng: "en",
		resources: {
			en: {
				narrator: {
					commentFileUnsaved: "Unsaved",
					removeComment: "Remove comment",
					editComment: "Edit comment",
				},
				common: { save: "Save", cancel: "Cancel" },
			},
		},
	});
	const draft = createCommentDraft({
		source: "file",
		excerpt: "captured text",
		comment: "please edit",
		fileTarget: { deviceId: "WindowsCase", path: "C:\\work\\完整 file.ts" },
		unsaved: true,
	});
	const html = renderToStaticMarkup(
		createElement(
			I18nextProvider,
			{ i18n },
			createElement(
				MantineProvider,
				{},
				createElement(CommentDraftChips, {
					drafts: [draft],
					onRemove: () => {},
					onUpdate: () => {},
				}),
			),
		),
	);
	const { document } = parseHTML(html);
	const label = document.querySelector('[title="C:\\\\work\\\\完整 file.ts · WindowsCase"]');
	expect(label?.textContent).toContain("完整 file.ts · Unsaved");
	expect(label?.textContent).not.toContain("C:");
	expect(label?.getAttribute("title")).not.toContain(":1");
	expect(document.querySelector('button[aria-label="Remove comment"]')).not.toBeNull();
	expect(document.querySelector('button[aria-label="Edit comment"]')).not.toBeNull();
	expect(document.querySelector('[role="dialog"]')).toBeNull();
	expect(isSpecCommentStale(draft, new Map([["spec://index.md", "new"]]))).toBe(false);
});

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
