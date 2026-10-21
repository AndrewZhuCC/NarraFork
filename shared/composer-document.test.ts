import { describe, expect, test } from "bun:test";
import {
	type ComposerDocument,
	composerDocumentProjection,
	composerDocumentText,
	composerDocumentTextCandidates,
	escapeComposerText,
	legacyComposerDocumentText,
	parseComposerDocument,
	readComposerDocument,
	unescapeComposerText,
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
		// The quote and the image follow typed content, so both stay where they stand.
		expect(text).toBe(
			"😀 #file:a.ts:2-4[image #1: x.png]\n> [quote] 来源 @@m:abcd\n> \\[quote] hi\n>\n\n\nend",
		);
		expect(legacyComposerDocumentText(accepted).text).toBe(
			"> [quote] 来源 @@m:abcd\n> \\[quote] hi\n>\n\n😀 #file:a.ts:2-4\nend",
		);
		const range = fileReferences[0].inputRange;
		if (!range) throw new Error("Expected token offsets");
		const [start, end] = range;
		expect(text.slice(start, end)).toBe("#file:a.ts:2-4");
		expect(start).toBe(text.indexOf("#file"));
		expect(accepted).toEqual(doc);
	});
	test("preserves explicit saved-file hash kinds without rewriting legacy metadata", () => {
		const reference = {
			id: "ref-hash",
			deviceId: "local",
			path: "/a.ts",
			label: "a.ts",
			expectedHash: "a".repeat(64),
		};
		for (const metadata of [
			reference,
			{ ...reference, expectedHashKind: "decoded-sha256" as const },
			{ ...reference, expectedHashKind: "raw-sha256" as const },
		]) {
			const doc: ComposerDocument = {
				...empty(),
				paragraphs: [{ children: [{ type: "fileReference", id: "f", reference: metadata }] }],
			};
			const accepted = parseComposerDocument(doc);
			expect(JSON.stringify(accepted)).toBe(JSON.stringify(doc));
			const projected = composerDocumentText(accepted).fileReferences[0];
			expect(projected).toMatchObject(metadata);
			if (!("expectedHashKind" in metadata))
				expect(projected).not.toHaveProperty("expectedHashKind");
		}
	});
	test("rejects unknown saved-file hash kinds and hash kinds without hashes", () => {
		for (const reference of [
			{ expectedHash: "a".repeat(64), expectedHashKind: "sha256" },
			{ expectedHash: "a".repeat(64), expectedHashKind: null },
			{ expectedHashKind: "raw-sha256" },
			{ expectedHashKind: "decoded-sha256", expectedHash: "" },
		])
			expect(() =>
				parseComposerDocument({
					...empty(),
					paragraphs: [
						{
							children: [
								{
									type: "fileReference",
									id: "f",
									reference: {
										id: "ref",
										deviceId: "local",
										path: "/a.ts",
										label: "a.ts",
										...reference,
									},
								},
							],
						},
					],
				}),
			).toThrow();
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
	test("positional comments stay beside their source and keep current and legacy offset candidates", () => {
		const doc = parseComposerDocument({
			...empty(),
			paragraphs: [
				{
					children: [
						{ type: "text", text: "before" },
						{ type: "quote", id: "q", label: "source", text: "original", comment: "message note" },
						{ type: "text", text: "middle" },
						{
							type: "fileReference",
							id: "f",
							reference: { id: "ref-1", deviceId: "local", path: "/a.ts", label: "a.ts" },
							comment: "file note",
						},
						{ type: "text", text: "after" },
					],
				},
			],
		});
		const current = composerDocumentProjection(doc);
		expect(current.text).toBe(
			"before\n> [quote] source\n> original\n| [comment]\n| message note\n\nmiddle\n> [quote] a.ts @@f:ref-1\n> #file:a.ts\n| [comment]\n| file note\n\n#file:a.tsafter",
		);
		const file = current.fileReferences[0];
		expect(file.inputRange).toEqual([
			current.text.lastIndexOf("#file:a.ts"),
			current.text.length - "after".length,
		]);
		const middle = current.spans.find((span) => span.node === 2);
		expect(middle && current.text.slice(middle.start, middle.end)).toBe("middle");
		expect(composerDocumentTextCandidates(doc)).toHaveLength(2);
		expect(legacyComposerDocumentText(doc).text).toEndWith("beforemiddle#file:a.tsafter");
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
	test("keeps the historical projection while quotes and attachments lead", () => {
		const doc = parseComposerDocument({
			version: 1,
			assets: [
				{ id: "a", kind: "image", name: "a.png", size: 1, mimeType: "image/png" },
				{ id: "f", kind: "textFile", name: "n.txt", size: 1, mimeType: "text/plain" },
			],
			paragraphs: [
				{
					children: [
						{ type: "attachment", id: "an", assetId: "a" },
						{ type: "quote", id: "q", label: "Q", text: "quoted" },
						{ type: "text", text: " " },
						{ type: "attachment", id: "fn", assetId: "f" },
					],
				},
				{ children: [{ type: "text", text: "body" }] },
			],
		});
		const current = composerDocumentText(doc);
		const legacy = legacyComposerDocumentText(doc);
		expect(current).toEqual({ text: legacy.text, fileReferences: legacy.fileReferences });
		expect(current.text).toBe("> [quote] Q\n> quoted\n\n \nbody");
		expect(composerDocumentTextCandidates(doc)).toHaveLength(1);
	});
	test("numbers attachments per kind in upload order once one follows text", () => {
		const doc = parseComposerDocument({
			version: 1,
			assets: [
				{ id: "b", kind: "image", name: "b[1].png", size: 1, mimeType: "image/png" },
				{ id: "f", kind: "textFile", name: "n.txt", size: 1, mimeType: "text/plain" },
				{ id: "a", kind: "image", name: "a.png", size: 1, mimeType: "image/png" },
			],
			paragraphs: [
				{
					children: [
						{ type: "attachment", id: "an", assetId: "a" },
						{ type: "text", text: "看" },
						{ type: "attachment", id: "bn", assetId: "b" },
						{ type: "quote", id: "q", label: "Q", text: "x" },
						{ type: "text", text: "和" },
						{ type: "attachment", id: "fn", assetId: "f" },
					],
				},
			],
		});
		const { text, spans } = composerDocumentProjection(doc);
		expect(text).toBe(
			"[image #2: a.png]看[image #1: b_1_.png]\n> [quote] Q\n> x\n\n和[file #1: n.txt]",
		);
		const textSpan = spans.find((span) => span.node === 4);
		expect(textSpan && text.slice(textSpan.start, textSpan.end)).toBe("和");
		expect(composerDocumentTextCandidates(doc).map((candidate) => candidate.text)).toEqual([
			text,
			"> [quote] Q\n> x\n\n看和",
		]);
	});
	test("escapes typed placeholder look-alikes once the document has attachments", () => {
		const typed = "> [quote] mine\nsee [image #1: a.png] and \\[file #2: b]";
		const textOnly = parseComposerDocument({
			version: 1,
			assets: [],
			paragraphs: [{ children: [{ type: "text", text: typed }] }],
		});
		expect(composerDocumentText(textOnly).text).toBe(typed);
		const withImage = parseComposerDocument({
			version: 1,
			assets: [{ id: "a", kind: "image", name: "a.png", size: 1, mimeType: "image/png" }],
			paragraphs: [
				{
					children: [
						{ type: "text", text: typed },
						{ type: "attachment", id: "an", assetId: "a" },
					],
				},
			],
		});
		const { text } = composerDocumentText(withImage);
		expect(text).toBe(
			"> [quote] mine\nsee \\[image #1: a.png] and \\\\[file #2: b][image #1: a.png]",
		);
		expect(unescapeComposerText(escapeComposerText(typed))).toBe(typed);
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
