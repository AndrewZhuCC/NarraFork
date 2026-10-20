import { expect, test } from "bun:test";
import {
	type ComposerDocument,
	composerDocumentText,
	legacyComposerDocumentText,
	parseComposerDocument,
} from "@shared/composer-document";
import {
	composerInputFromDocument,
	importComposerInput,
	missingComposerAssets,
	withComposerAttachments,
} from "./composer-document-adapter";
import { composerOptimizeTarget } from "./composer-optimize-target";
import { fileReferenceQuote, joinQuotedComposerInput } from "./composer-quotes";
import { getComposerAssetId } from "./draft-image-attachments";
import { insertFileReference } from "./file-reference-input";
import type { InlineComposerEditorHandle } from "./InlineComposerEditor";

const empty: ComposerDocument = { version: 1, paragraphs: [{ children: [] }], assets: [] };

test("legacy quotes and UTF-16 file occurrences import to atoms with the same projection", () => {
	const body = insertFileReference(
		{ text: "😀 before ", fileReferences: [] },
		{ id: "ref-1", label: "a.ts", path: "/a.ts", deviceId: "local" },
	);
	const input = joinQuotedComposerInput(
		[{ label: "source", text: "quote", anchor: "m:abcd" }],
		body,
	);
	const document = importComposerInput(input);
	expect(document.paragraphs[0].children.map((node) => node.type)).toEqual([
		"quote",
		"text",
		"fileReference",
		"text",
	]);
	expect(composerDocumentText(document)).toEqual(input);
	expect(parseComposerDocument(document)).toBeDefined();
});

test("legacy text comments round-trip without becoming part of the original excerpt", () => {
	const quote = {
		label: "source",
		text: "original\n[quote] escaped\n| [comment]\n",
		anchor: "t:tool-1",
		comment: "edit this\n\n> [quote] not a new quote\n| pipe\n",
	};
	const input = joinQuotedComposerInput([quote], { text: "😀 body", fileReferences: [] });
	const document = importComposerInput(input);
	expect(document.paragraphs[0].children[0]).toMatchObject({ type: "quote", ...quote });
	expect(parseComposerDocument(document)).toEqual(document);
	expect(composerDocumentText(document)).toEqual(input);
	expect(importComposerInput(composerInputFromDocument(document))).toEqual(document);
});

test("legacy file comments bind to the existing ranged file atom and keep the same offsets", () => {
	const body = insertFileReference(
		{ text: "😀 before ", fileReferences: [] },
		{
			id: "ref-1",
			label: "a.ts",
			path: "/a.ts",
			deviceId: "local",
			expectedHash: "saved-hash",
			selection: { startLineNumber: 2, startColumn: 1, endLineNumber: 5, endColumn: 1 },
		},
	);
	const reference = body.fileReferences[0];
	const quote = { ...fileReferenceQuote(reference), comment: "explain this\nkeep bytes\n" };
	const input = joinQuotedComposerInput([quote], body);
	const document = importComposerInput(input);
	const files = document.paragraphs[0].children.filter((node) => node.type === "fileReference");
	expect(files).toEqual([
		{
			type: "fileReference",
			id: reference.id,
			reference: input.fileReferences[0],
			comment: quote.comment,
		},
	]);
	expect(document.paragraphs[0].children.some((node) => node.type === "quote")).toBe(false);
	expect(parseComposerDocument(document)).toEqual(document);
	expect(legacyComposerDocumentText(document)).toMatchObject(input);
	const current = composerDocumentText(document);
	expect(current.text).toStartWith("😀 before \n> [quote] a.ts:2-4 @@f:ref-1");
	expect(current.text).toContain("> #file:a.ts:2-4\n| [comment]\n");
	expect(current.fileReferences[0].inputRange?.[0]).toBe(
		current.text.lastIndexOf("#file:a.ts:2-4"),
	);
	expect(importComposerInput(composerInputFromDocument(document))).toEqual(document);
});

test("positional message and file comments reimport without moving the source or duplicating files", () => {
	const reference = { id: "ref-1", deviceId: "local", path: "/a.ts", label: "a.ts" };
	const document: ComposerDocument = {
		...empty,
		paragraphs: [
			{
				children: [
					{ type: "text", text: "before" },
					{
						type: "quote",
						id: "q",
						label: "source",
						text: "original",
						anchor: "m:abcd",
						comment: "message note",
					},
					{ type: "text", text: "middle\n" },
					{ type: "fileReference", id: "f", reference, comment: "file note" },
					{ type: "text", text: "after" },
				],
			},
		],
	};
	const projection = composerDocumentText(document);
	const imported = importComposerInput(projection);
	expect(imported.paragraphs[0].children.map((node) => node.type)).toEqual([
		"text",
		"quote",
		"text",
		"fileReference",
		"text",
	]);
	expect(imported.paragraphs[0].children[1]).toMatchObject({
		text: "original",
		comment: "message note",
	});
	expect(imported.paragraphs[0].children[3]).toMatchObject({ reference, comment: "file note" });
	expect(composerDocumentText(imported)).toEqual(projection);
	expect(parseComposerDocument(imported)).toEqual(imported);
});

test("legacy detached file comments normalize once without duplicating attachments or source", () => {
	const reference = {
		id: "ref-1",
		label: "a.ts",
		path: "/a.ts",
		deviceId: "remote-device",
		expectedHash: "saved-hash",
	};
	const fileQuote = { ...fileReferenceQuote(reference), comment: "change this\n\nkeep pipe |\n" };
	const textQuote = {
		label: "source",
		text: "untouched source",
		anchor: "m:abcd",
		comment: "note",
	};
	const input = joinQuotedComposerInput([fileQuote, textQuote], {
		text: "😀 other body\nkeep everything",
		fileReferences: [reference],
	});
	const image = new File(["image"], "image.png", { type: "image/png" });
	const textFile = new File(["text"], "note.txt", { type: "text/plain" });
	const document = withComposerAttachments(importComposerInput(input), [image], [textFile]);
	expect(document.paragraphs[0].children.map((node) => node.type)).toEqual([
		"text",
		"fileReference",
		"quote",
		"attachment",
		"attachment",
	]);
	expect(document.paragraphs[0].children[1]).toEqual({
		type: "fileReference",
		id: reference.id,
		reference,
		comment: fileQuote.comment,
	});
	expect(document.paragraphs[0].children[2]).toMatchObject({ type: "quote", ...textQuote });
	expect(document.assets.map((asset) => asset.id)).toEqual([
		getComposerAssetId(image),
		getComposerAssetId(textFile),
	]);
	const legacy = legacyComposerDocumentText(document);
	expect(legacy.text).toBe(`${input.text}#file:a.ts`);
	expect(legacy.fileReferences).toEqual([
		{ ...reference, inputRange: [input.text.length, input.text.length + "#file:a.ts".length] },
	]);
	const projection = composerDocumentText(document);
	expect(projection.text).toStartWith("😀 other body\nkeep everything\n> [quote] a.ts @@f:ref-1");
	expect(projection.text).toContain("[image #1: image.png][file #1: note.txt]");
	expect(reference).not.toHaveProperty("inputRange");
	expect(parseComposerDocument(document)).toEqual(document);
	const reimported = importComposerInput(projection);
	expect(composerDocumentText(reimported)).toEqual(projection);
	expect(
		reimported.paragraphs[0].children.filter((node) => node.type === "fileReference"),
	).toHaveLength(1);
});

test.each([
	{ label: "a.ts", text: "#file:a.ts", anchor: "f:missing", comment: "keep unmatched comment" },
	{ label: "a.ts", text: "#file:a.ts", anchor: "f:x", comment: "keep malformed anchor" },
	{
		label: "a.ts",
		text: "different original excerpt",
		anchor: "f:ref-1",
		comment: "keep original",
	},
	{ label: "a.ts", text: "#file:a.ts", anchor: "f:ref-1", comment: "" },
])("unknown or noncanonical file comments retain their original text: %j", (quote) => {
	const text = joinQuotedComposerInput([quote], { text: "body", fileReferences: [] }).text;
	const document = importComposerInput({ text, fileReferences: [] });
	expect(document.paragraphs[0].children).toEqual([{ type: "text", text }]);
	expect(composerDocumentText(document)).toEqual({ text, fileReferences: [] });
	expect(parseComposerDocument(document)).toEqual(document);
});

test("nonmatching file excerpts are not silently replaced by real attachment metadata", () => {
	const body = insertFileReference(
		{ text: "body ", fileReferences: [] },
		{ id: "ref-1", label: "a.ts", path: "/a.ts", deviceId: "local" },
	);
	const input = joinQuotedComposerInput(
		[
			{
				...fileReferenceQuote(body.fileReferences[0]),
				text: "original excerpt",
				comment: "keep it",
			},
		],
		body,
	);
	const document = importComposerInput(input);
	expect(document.paragraphs[0].children.some((node) => "comment" in node)).toBe(false);
	expect(composerDocumentText(document)).toEqual(input);
});

test("conflicting legacy file-comment order preserves raw comments and real file occurrences", () => {
	const first = insertFileReference(
		{ text: "body ", fileReferences: [] },
		{ id: "ref-1", label: "a.ts", path: "/a.ts", deviceId: "local" },
	);
	const body = insertFileReference(first, {
		id: "ref-2",
		label: "b.ts",
		path: "/b.ts",
		deviceId: "local",
	});
	const input = joinQuotedComposerInput(
		[...body.fileReferences]
			.reverse()
			.map((ref) => ({ ...fileReferenceQuote(ref), comment: ref.id })),
		body,
	);
	const document = importComposerInput(input);
	expect(
		document.paragraphs[0].children.some((node) => "comment" in node || node.type === "quote"),
	).toBe(false);
	expect(composerDocumentText(document)).toEqual(input);
	expect(parseComposerDocument(document)).toEqual(document);
});

test.each([
	["empty quote body", "> [quote] source\n>\n\n"],
	["8001-character quote body", `> [quote] source\n> ${"q".repeat(8001)}\n\n`],
	["81-character label", `> [quote] ${"l".repeat(81)}\n> quote\n\n`],
	["257-character anchor", `> [quote] source @@m:${"a".repeat(255)}\n> quote\n\n`],
	["noncanonical header", "> [quote]  source  \n> quote\n\n"],
	["empty comment marker", "> [quote] source\n> quote\n| [comment]\n| \n\n"],
	["mixed valid and invalid quotes", "> [quote] source\n> valid\n\n> [quote] empty\n>\n\n"],
])("legacy %s prefixes restore losslessly as text with original file offsets", (_name, prefix) => {
	const inserted = insertFileReference(
		{ text: `${prefix}😀 body before `, fileReferences: [] },
		{ id: "ref-1", label: "a.ts", path: "/a.ts", deviceId: "local" },
	);
	const input = {
		text: `${inserted.text} after\nrest of body`,
		fileReferences: inserted.fileReferences,
	};
	const document = importComposerInput(input);
	expect(document.paragraphs[0].children.map((node) => node.type)).toEqual([
		"text",
		"fileReference",
		"text",
	]);
	expect(document.paragraphs[0].children[0]).toEqual({
		type: "text",
		text: input.text.slice(0, input.fileReferences[0].inputRange?.[0]),
	});
	expect(parseComposerDocument(document)).toEqual(document);
	expect(composerDocumentText(document)).toEqual(input);
});

test.each([
	"> [quote] source\n> quote",
	"> [quote] first\n> quote\n> [quote] second\n> more\n\nbody",
])("noncanonical quote separators preserve the exact original text: %j", (text) => {
	const input = { text, fileReferences: [] };
	const document = importComposerInput(input);
	expect(document.paragraphs[0].children).toEqual([{ type: "text", text }]);
	expect(parseComposerDocument(document)).toEqual(document);
	expect(composerDocumentText(document)).toEqual(input);
});

test("quote limits remain inclusive and legal quotes still import as quote atoms", () => {
	const input = joinQuotedComposerInput(
		[{ label: "l".repeat(80), text: "q".repeat(8000), anchor: `m:${"a".repeat(254)}` }],
		{ text: "😀 body", fileReferences: [] },
	);
	const document = importComposerInput(input);
	expect(document.paragraphs[0].children.map((node) => node.type)).toEqual(["quote", "text"]);
	expect(parseComposerDocument(document)).toEqual(document);
	expect(composerDocumentText(document)).toEqual(input);
});

test("unknown document versions retain the existing legacy-text recovery path", () => {
	const input = {
		text: "> [quote] source\n>\n\nbody",
		fileReferences: [],
		composerDocument: { version: 99, paragraphs: [], assets: [] } as unknown as ComposerDocument,
	};
	const document = importComposerInput(input);
	expect(document.paragraphs[0].children).toEqual([{ type: "text", text: input.text }]);
	expect(parseComposerDocument(document)).toEqual(document);
	expect(composerDocumentText(document)).toEqual({ text: input.text, fileReferences: [] });
});

test("mixed document positions survive projection and reimport without grouping atoms", () => {
	const image = new File(["x"], "same.png", { type: "image/png" });
	const attachment = withComposerAttachments(empty, [image], []);
	const document: ComposerDocument = {
		...attachment,
		paragraphs: [
			{
				children: [
					{ type: "text", text: "A" },
					...attachment.paragraphs[0].children,
					{ type: "quote", id: "q1", label: "source", text: "quote" },
					{ type: "text", text: "B" },
				],
			},
		],
	};
	expect(importComposerInput(composerInputFromDocument(document))).toEqual(document);
	expect(document.assets[0].id).toBe(getComposerAssetId(image));
});

test("missing attachments keep their positions and same-name files never share identity", () => {
	const first = new File(["a"], "same.png", { type: "image/png" });
	const second = new File(["b"], "same.png", { type: "image/png" });
	const document = withComposerAttachments(empty, [first, second], []);
	expect(document.assets[0].id).not.toBe(document.assets[1].id);
	expect(withComposerAttachments(document, [], [])).toBe(document);
	expect(missingComposerAssets(document, new Map([[getComposerAssetId(first), first]]))).toBe(true);
});

function editor(initial: ComposerDocument, start = 0, end = start) {
	let document = initial;
	let preserved = false;
	const handle = {
		getDocument: () => document,
		getSelection: () => ({ start, end }),
		setDocument: (value: ComposerDocument, history?: boolean) => {
			document = value;
			preserved = !!history;
		},
	} as InlineComposerEditorHandle;
	return {
		handle,
		get: () => document,
		preserved: () => preserved,
		change: (next: ComposerDocument) => {
			document = next;
		},
	};
}

test("optimization replaces only selected text, preserving atoms and a single undo transaction", () => {
	const value: ComposerDocument = {
		...empty,
		paragraphs: [
			{
				children: [
					{ type: "text", text: "hello world" },
					{ type: "quote", id: "q1", label: "source", text: "do not optimize this" },
				],
			},
		],
	};
	const state = editor(value, 6, 11);
	const target = composerOptimizeTarget(state.handle);
	expect(target?.text).toBe("world");
	expect(target?.apply("earth")).toBe(true);
	expect(state.get().paragraphs[0].children[0]).toEqual({ type: "text", text: "hello earth" });
	expect(state.get().paragraphs[0].children[1]).toEqual(value.paragraphs[0].children[1]);
	expect(state.preserved()).toBe(true);
});

test("optimization rejects stale content and malformed multi-segment responses atomically", () => {
	const value: ComposerDocument = {
		...empty,
		paragraphs: [
			{
				children: [
					{ type: "text", text: "A" },
					{ type: "quote", id: "q1", label: "source", text: "quote" },
					{ type: "text", text: "B" },
				],
			},
		],
	};
	const state = editor(value);
	const target = composerOptimizeTarget(state.handle);
	expect(target?.text).not.toContain("quote");
	expect(target?.apply("merged")).toBe(false);
	expect(state.get()).toBe(value);
	state.change(empty);
	expect(target?.apply(target.text)).toBe(false);
	expect(state.get()).toBe(empty);
});

test("optimization validates segment markers and leaves quote/image order intact", () => {
	const value: ComposerDocument = {
		...empty,
		paragraphs: [
			{
				children: [
					{ type: "text", text: "alpha" },
					{ type: "quote", id: "q1", label: "source", text: "quote" },
					{ type: "text", text: "beta" },
				],
			},
		],
	};
	const state = editor(value);
	const target = composerOptimizeTarget(state.handle);
	const response =
		target?.text
			.slice(target.text.indexOf("<<<"))
			.replace("alpha", "ALPHA")
			.replace("beta", "BETA") ?? "";
	expect(target?.apply(response)).toBe(true);
	expect(state.get().paragraphs[0].children).toEqual([
		{ type: "text", text: "ALPHA" },
		value.paragraphs[0].children[1],
		{ type: "text", text: "BETA" },
	]);
});
