import { describe, expect, spyOn, test } from "bun:test";
import type { ComposerDocument } from "@shared/composer-document";
import {
	parseOptionalComposerDocument,
	validateComposerAssets,
	validateComposerProjection,
} from "./composer-document";

function imageDocument(): ComposerDocument {
	return {
		version: 1,
		paragraphs: [{ children: [{ type: "attachment", id: "node", assetId: "asset" }] }],
		assets: [{ id: "asset", kind: "image", name: "image.png", size: 9, mimeType: "image/png" }],
	};
}
describe("server composer acceptance", () => {
	test("raw UTF8 and character budgets reject before JSON.parse, including unknown fields", () => {
		const parse = spyOn(JSON, "parse");
		try {
			for (const raw of [
				"x".repeat(2 * 1024 * 1024 + 1),
				`{"unknown":"${"x".repeat(2 * 1024 * 1024)}"}`,
				`{"unknown":"${"中".repeat(750_000)}"}`,
			])
				expect(() => parseOptionalComposerDocument(raw)).toThrow("2 MiB");
			expect(parse).not.toHaveBeenCalled();
		} finally {
			parse.mockRestore();
		}
	});
	test("unknown object fields and oversized child arrays fail before full serialization", () => {
		const stringify = spyOn(JSON, "stringify");
		try {
			expect(() =>
				parseOptionalComposerDocument({ ...imageDocument(), unknown: "x".repeat(2 * 1024 * 1024) }),
			).toThrow();
			expect(() =>
				parseOptionalComposerDocument({
					version: 1,
					paragraphs: [{ children: Array.from({ length: 4097 }, () => ({ type: "break" })) }],
					assets: [],
				}),
			).toThrow();
			expect(stringify).not.toHaveBeenCalled();
		} finally {
			stringify.mockRestore();
		}
	});
	test("accepts omitted legacy layout but rejects malformed structured input", () => {
		expect(parseOptionalComposerDocument(null)).toBeUndefined();
		expect(parseOptionalComposerDocument(JSON.stringify(imageDocument()))).toEqual(imageDocument());
		expect(() => parseOptionalComposerDocument("{bad-json")).toThrow("Invalid composer document");
	});
	test("requires exact legacy projection and attachment binding", () => {
		const document = imageDocument();
		validateComposerProjection(document, "", []);
		validateComposerAssets(
			document,
			[{ filename: "image.png", size: 9, mediaType: "image/png" }],
			[],
		);
		for (const images of [
			[],
			[{ filename: "other.png", size: 9, mediaType: "image/png" }],
			[{ filename: "image.png", size: 10, mediaType: "image/png" }],
			[{ filename: "image.png", size: 9, mediaType: "image/jpeg" }],
		])
			expect(() => validateComposerAssets(document, images, [])).toThrow();
		expect(() => validateComposerProjection(document, "extra", [])).toThrow();
		expect(() =>
			validateComposerAssets(document, [], [{ name: "image.png", size: 9, type: "image/png" }]),
		).toThrow();
	});
	test("block discriminators are not MIME values, but real attachment MIME still binds", () => {
		const document: ComposerDocument = {
			version: 1,
			paragraphs: [{ children: [{ type: "attachment", id: "node", assetId: "asset" }] }],
			assets: [
				{ id: "asset", kind: "textFile", name: "note.txt", size: 5, mimeType: "text/plain" },
			],
		};
		for (const metadata of [
			{ name: "note.txt", size: 5, type: "text/plain" },
			{ filename: "note.txt", size: 5, type: "text_file" },
			{ filename: "note.txt", size: 5, type: "text_file", mimeType: "text/plain" },
		])
			validateComposerAssets(document, [], [metadata]);
		for (const mime of ["application/json", "text/html"])
			for (const metadata of [
				{ type: mime },
				{ type: "text_file", mimeType: mime },
				{ type: "text_file", sourceMimeType: mime },
			])
				expect(() =>
					validateComposerAssets(document, [], [{ filename: "note.txt", size: 5, ...metadata }]),
				).toThrow("metadata");
		validateComposerAssets(
			imageDocument(),
			[{ type: "image", filename: "image.png", size: 9, mediaType: "image/png" }],
			[],
		);
	});
	test("uses original upload MIME while keeping sniffed transport metadata separate", () => {
		const document = imageDocument();
		document.assets[0].mimeType = "";
		validateComposerAssets(
			document,
			[{ filename: "image.png", size: 9, sourceMimeType: "", mediaType: "image/png" }],
			[],
		);
	});
});
