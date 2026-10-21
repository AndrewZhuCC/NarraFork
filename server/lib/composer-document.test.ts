import { describe, expect, spyOn, test } from "bun:test";
import { type ComposerDocument, composerDocumentText } from "@shared/composer-document";
import {
	parseOptionalComposerDocument,
	validateComposerAssets,
	validateComposerProjection,
	validatePersistedComposerDocument,
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
		])
			expect(() => validateComposerAssets(document, images, [])).toThrow();
		expect(() => validateComposerProjection(document, "extra", [])).toThrow();
		expect(() =>
			validateComposerAssets(document, [], [{ name: "image.png", size: 9, type: "image/png" }]),
		).toThrow();
	});
	test("projection identity compares hash kinds without adding defaults to legacy metadata", () => {
		for (const expectedHashKind of [undefined, "decoded-sha256", "raw-sha256"] as const) {
			const reference = {
				id: "ref",
				deviceId: "local",
				path: "/work/a.ts",
				label: "a.ts",
				expectedHash: "a".repeat(64),
				...(expectedHashKind ? { expectedHashKind } : {}),
			};
			const document: ComposerDocument = {
				version: 1,
				paragraphs: [{ children: [{ type: "fileReference", id: "f", reference }] }],
				assets: [],
			};
			const before = JSON.stringify(document);
			const accepted = parseOptionalComposerDocument(before);
			if (!accepted) throw new Error("Expected composer document");
			const { text, fileReferences } = composerDocumentText(accepted);
			validateComposerProjection(accepted, text, fileReferences);
			expect(() =>
				validateComposerProjection(accepted, text, [
					{
						...fileReferences[0],
						expectedHashKind: expectedHashKind === "raw-sha256" ? "decoded-sha256" : "raw-sha256",
					},
				]),
			).toThrow("Composer document does not match");
			if (expectedHashKind !== "raw-sha256")
				validateComposerProjection(accepted, text, [
					{
						...fileReferences[0],
						expectedHashKind: expectedHashKind ? undefined : "decoded-sha256",
					},
				]);
			expect(JSON.stringify(accepted)).toBe(before);
		}
	});
	test("block discriminators and advisory MIME values do not participate in attachment identity", () => {
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
				validateComposerAssets(document, [], [{ filename: "note.txt", size: 5, ...metadata }]);
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

async function multipartFiles(files: File[]): Promise<File[]> {
	const data = new FormData();
	for (const file of files) data.append("files", file);
	const parsed = await new Request("http://localhost", { method: "POST", body: data }).formData();
	return parsed.getAll("files") as File[];
}

function filesDocument(files: File[], kind: "image" | "textFile"): ComposerDocument {
	return {
		version: 1,
		paragraphs: [
			{
				children: files.map((_, index) => ({
					type: "attachment",
					id: `node-${index}`,
					assetId: `asset-${index}`,
				})),
			},
		],
		assets: files.map((file, index) => ({
			id: `asset-${index}`,
			kind,
			name: file.name,
			size: file.size,
			mimeType: file.type,
		})),
	};
}

describe("composer MIME is advisory across upload and persistence", () => {
	test("accepts ZIP MIME aliases after a real multipart round trip", async () => {
		const original = new File(["PK archive bytes"], "附件.zip", {
			type: "application/x-zip-compressed",
		});
		const document = filesDocument([original], "textFile");
		const uploads = await multipartFiles([original]);
		expect(uploads[0].type).toBe("application/zip");
		expect(uploads[0].name).toBe(original.name);
		expect(uploads[0].size).toBe(original.size);
		validateComposerAssets(document, [], uploads);
		validatePersistedComposerDocument(document, [
			{
				type: "text_file",
				filename: original.name,
				size: original.size,
				sourceMimeType: uploads[0].type,
			},
		]);
	});

	test("accepts empty, source-code and image MIME changes without rewriting documents", async () => {
		for (const [name, mimeType, kind] of [
			["unknown.custom", "", "textFile"],
			["note.txt", "", "textFile"],
			["main.ts", "text/plain", "textFile"],
			["image.png", "image/jpeg", "image"],
			["image.png", "", "image"],
		] as const) {
			const original = new File(["bytes"], name, { type: mimeType });
			const document = filesDocument([original], kind);
			const uploads = await multipartFiles([original]);
			validateComposerAssets(
				document,
				kind === "image" ? uploads : [],
				kind === "textFile" ? uploads : [],
			);
			expect(document.assets[0].mimeType).toBe(original.type);
			if (kind === "image") {
				validatePersistedComposerDocument(document, [
					{
						type: "image",
						filename: original.name,
						size: original.size,
						sourceMimeType: uploads[0].type,
						mediaType: "image/jpeg",
					},
				]);
			}
		}
	});

	test("still binds duplicate names by count, size and ordinal position", async () => {
		const files = [
			new File(["first"], "same.zip", { type: "application/x-zip-compressed" }),
			new File(["second"], "same.zip", { type: "application/zip" }),
		];
		const document = filesDocument(files, "textFile");
		const uploads = await multipartFiles(files);
		validateComposerAssets(document, [], uploads);
		expect(() => validateComposerAssets(document, [], uploads.toReversed())).toThrow("metadata");
		expect(() => validateComposerAssets(document, [], uploads.slice(1))).toThrow("count");
	});
});
