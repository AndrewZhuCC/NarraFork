import { describe, expect, test } from "bun:test";
import {
	type ComposerDocument,
	composerDocumentText,
	parseComposerDocument,
	readComposerDocument,
} from "./composer-document";
import { fileReferenceMessageForDisplay } from "./file-reference";

const empty = (): ComposerDocument => ({ version: 1, paragraphs: [{ children: [] }], assets: [] });
describe("semantic composer document", () => {
	test("projects quotes, unicode token offsets and independent attachment order", () => {
		const doc: ComposerDocument = {
			version: 1,
			assets: [{ id: "asset", kind: "image", name: "x.png", size: 9, mimeType: "image/png" }],
			paragraphs: [
				{
					children: [
						{ type: "text", text: "😀 " },
						{
							type: "fileReference",
							id: "file-node",
							reference: {
								id: "ref",
								deviceId: "local",
								path: "/a.ts",
								label: "a.ts",
								selection: { startLineNumber: 2, startColumn: 1, endLineNumber: 5, endColumn: 1 },
							},
						},
						{ type: "attachment", id: "image-node", assetId: "asset" },
						{ type: "quote", id: "quote", label: "来源", text: "[quote] hi\n", anchor: "m:abcd" },
						{ type: "break" },
						{ type: "text", text: "end" },
					],
				},
			],
		};
		const accepted = parseComposerDocument(doc);
		const { text, fileReferences } = composerDocumentText(accepted);
		expect(text).toBe("> [quote] 来源 @@m:abcd\n> \\[quote] hi\n>\n\n😀 #file:a.ts:2-4\nend");
		const range = fileReferences[0].inputRange;
		if (!range) throw new Error("Expected token offsets");
		const [start, end] = range;
		expect(text.slice(start, end)).toBe("#file:a.ts:2-4");
		expect(start).toBe(text.indexOf("#file"));
		expect(accepted).toEqual(doc);
	});
	test("projects comments separately from source text and immutable file metadata", () => {
		const reference = {
			id: "ref-1",
			deviceId: "local",
			path: "/a.ts",
			label: "a.ts",
			expectedHash: "saved-hash",
		};
		const source = "original\n[quote] escaped\n| [comment]\n";
		const comment = "change this\n\n> [quote] not a source\n| keep pipe\n";
		const doc: ComposerDocument = {
			...empty(),
			paragraphs: [
				{
					children: [
						{ type: "quote", id: "q", label: "source", text: source, anchor: "m:abcd", comment },
						{ type: "fileReference", id: "f", reference, comment: "explain file" },
					],
				},
			],
		};
		const accepted = parseComposerDocument(doc);
		expect(accepted).toEqual(doc);
		const { text, fileReferences } = composerDocumentText(accepted);
		expect(text).toBe(
			"> [quote] source @@m:abcd\n> original\n> \\[quote] escaped\n> | [comment]\n>\n" +
				"| [comment]\n| change this\n| \n| > [quote] not a source\n| | keep pipe\n| \n\n" +
				"> [quote] a.ts @@f:ref-1\n> #file:a.ts\n| [comment]\n| explain file\n\n#file:a.ts",
		);
		expect(accepted.paragraphs[0].children[0]).toMatchObject({ text: source, comment });
		expect(fileReferences).toEqual([
			{ ...reference, inputRange: [text.lastIndexOf("#file:a.ts"), text.length] },
		]);
		expect(reference).toEqual({
			id: "ref-1",
			deviceId: "local",
			path: "/a.ts",
			label: "a.ts",
			expectedHash: "saved-hash",
		});
	});
	test("empty comments remain optional semantic fields without changing legacy projection", () => {
		const doc: ComposerDocument = {
			...empty(),
			paragraphs: [
				{
					children: [
						{ type: "quote", id: "q", label: "source", text: "original", comment: "" },
						{
							type: "fileReference",
							id: "f",
							reference: { id: "ref-1", deviceId: "local", path: "/a.ts", label: "a.ts" },
							comment: "",
						},
					],
				},
			],
		};
		expect(parseComposerDocument(doc)).toEqual(doc);
		expect(composerDocumentText(doc).text).toBe("> [quote] source\n> original\n\n#file:a.ts");
	});
	test("comments use the text budget without admitting unknown node or snapshot fields", () => {
		for (const node of [
			{ type: "quote", id: "q", label: "source", text: "x" },
			{
				type: "fileReference",
				id: "f",
				reference: { id: "ref-1", deviceId: "local", path: "/a.ts", label: "a.ts" },
			},
		]) {
			for (const comment of [42, null, {}, "c".repeat(200001)])
				expect(() =>
					parseComposerDocument({
						...empty(),
						paragraphs: [{ children: [{ ...node, comment }] }],
					}),
				).toThrow();
			expect(() =>
				parseComposerDocument({
					...empty(),
					paragraphs: [
						{
							children: [
								{ ...node, comment: "c".repeat(100000) },
								{ type: "text", text: "x".repeat(100001) },
							],
						},
					],
				}),
			).toThrow("budget");
		}
		for (const node of [
			{ type: "text", text: "x", comment: "not an annotation" },
			{ type: "break", comment: "not an annotation" },
			{ type: "quote", id: "q", label: "source", text: "x", comment: "ok", html: "no" },
			{
				type: "fileReference",
				id: "f",
				reference: { id: "ref-1", deviceId: "local", path: "/a.ts", label: "a.ts", comment: "no" },
			},
		])
			expect(() =>
				parseComposerDocument({ ...empty(), paragraphs: [{ children: [node] }] }),
			).toThrow("Unknown composer field");
	});
	test("rejects arbitrary editor/html/path fields and unknown versions", () => {
		for (const value of [
			{ ...empty(), html: "<img>" },
			{ ...empty(), version: 2 },
			{ ...empty(), paragraphs: [{ children: [{ type: "html", text: "x" }] }] },
			{
				...empty(),
				assets: [
					{
						id: "x",
						kind: "image",
						name: "x",
						size: 1,
						mimeType: "image/png",
						path: "/etc/passwd",
					},
				],
			},
		])
			expect(readComposerDocument(value)).toBeUndefined();
	});
	test("rejects duplicate ids and dangling or unused asset identities", () => {
		const quote = { type: "quote", id: "q", label: "Q", text: "x" };
		expect(() =>
			parseComposerDocument({ ...empty(), paragraphs: [{ children: [quote, quote] }] }),
		).toThrow();
		expect(() =>
			parseComposerDocument({
				...empty(),
				paragraphs: [{ children: [{ type: "attachment", id: "a", assetId: "missing" }] }],
			}),
		).toThrow();
		expect(() =>
			parseComposerDocument({
				...empty(),
				assets: [{ id: "a", kind: "image", name: "x", size: 1, mimeType: "image/png" }],
			}),
		).toThrow();
	});
	test("enforces text, quote, node, reference and UTF8 budgets", () => {
		expect(() =>
			parseComposerDocument({
				...empty(),
				paragraphs: [{ children: [{ type: "text", text: "x".repeat(200001) }] }],
			}),
		).toThrow();
		expect(() =>
			parseComposerDocument({
				...empty(),
				paragraphs: [
					{
						children: Array.from({ length: 9 }, (_, i) => ({
							type: "quote",
							id: `q${i}`,
							label: "Q",
							text: "x",
						})),
					},
				],
			}),
		).toThrow();
		expect(() =>
			parseComposerDocument({
				...empty(),
				paragraphs: [
					{ children: [{ type: "quote", id: "q", label: "Q", text: "x".repeat(8001) }] },
				],
			}),
		).toThrow();
		expect(() =>
			parseComposerDocument({
				...empty(),
				paragraphs: Array.from({ length: 4097 }, () => ({ children: [] })),
			}),
		).toThrow();
		expect(() =>
			parseComposerDocument({
				...empty(),
				paragraphs: [
					{
						children: Array.from({ length: 17 }, (_, i) => ({
							type: "fileReference",
							id: `n${i}`,
							reference: { id: `r${i}`, deviceId: "local", path: "/x", label: "x" },
						})),
					},
				],
			}),
		).toThrow();
		expect(() => parseComposerDocument({ ...empty(), extra: "中".repeat(750000) })).toThrow();
	});
	test("safe reader rejects cyclic input and accepts empty tombstones", () => {
		const cyclic: Record<string, unknown> = {};
		cyclic.self = cyclic;
		expect(readComposerDocument(cyclic)).toBeUndefined();
		expect(readComposerDocument(null)).toBeUndefined();
		expect(composerDocumentText(parseComposerDocument(empty()))).toEqual({
			text: "",
			fileReferences: [],
		});
	});
	test("message display never broadcasts the full layout", () => {
		const displayed = fileReferenceMessageForDisplay({
			id: "m",
			contentJson: [],
			composerDocument: empty(),
		});
		expect(displayed).toEqual({ id: "m", contentJson: [], hasComposerDocument: true });
		expect("composerDocument" in displayed).toBe(false);
	});
});
