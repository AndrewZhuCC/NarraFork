import { describe, expect, test } from "bun:test";
import { type ComposerDocument, composerDocumentText } from "@shared/composer-document";
import {
	type FileReference,
	type FileReferenceEditorSelection,
	MAX_FILE_REFERENCE_COUNT,
	MAX_FILE_REFERENCE_PATH_CHARS,
	MAX_FILE_REFERENCE_QUERY_CHARS,
	MAX_FILE_REFERENCE_SEARCH_RESULTS,
} from "@shared/file-reference";
import {
	copyFileReferences,
	editFileReferenceInput,
	type FileReferenceInput,
	fileReferenceKeyAction,
	fileReferenceToken,
	getFileReferenceQuery,
	insertFileReference,
	readFileReferences,
	readInputComposerDocument,
	recentFileReferences,
	rememberFileReference,
	sameFileReferenceInput,
	savedSelectionReference,
	trimFileReferenceInput,
} from "./file-reference-input";

const reference: FileReference = {
	id: "occurrence-1",
	deviceId: "RemoteABC",
	path: "/work/src/中文 file.ts",
	label: "src/中文 file.ts",
};
const query = (text: string) => getFileReferenceQuery(text, text.length);
const input = (): FileReferenceInput =>
	insertFileReference({ text: "before ", fileReferences: [] }, reference);

describe("structured input sidecars", () => {
	function richInput(): FileReferenceInput {
		const composerDocument: ComposerDocument = {
			version: 1,
			assets: [],
			paragraphs: [
				{
					children: [
						{ type: "text", text: "  before " },
						{ type: "fileReference", id: "node-ref", reference },
						{ type: "text", text: " after  " },
					],
				},
			],
		};
		return { ...composerDocumentText(composerDocument), composerDocument };
	}
	test("same projection with a different layout is a different draft", () => {
		const original = richInput();
		expect(
			sameFileReferenceInput(original, {
				text: original.text,
				fileReferences: original.fileReferences,
			}),
		).toBe(false);
		expect(sameFileReferenceInput(original, structuredClone(original))).toBe(true);
	});
	test("trimming a rich projection never shifts reference ranges", () => {
		const original = richInput();
		expect(trimFileReferenceInput(original)).toBe(original);
	});
	test("maps a local plain-text edit without moving inline atoms", () => {
		const original = richInput();
		const edited = editFileReferenceInput(original, original.text.replace("before", "earlier"));
		expect(edited.composerDocument).toBeDefined();
		expect(edited.composerDocument?.paragraphs[0].children[1]).toEqual(
			original.composerDocument?.paragraphs[0].children[1],
		);
		if (!edited.composerDocument) throw new Error("Missing mapped document");
		expect(composerDocumentText(edited.composerDocument)).toEqual({
			text: edited.text,
			fileReferences: edited.fileReferences,
		});
	});
	test("removes stale layout when a legacy edit touches an atom", () => {
		const original = richInput();
		expect(
			editFileReferenceInput(original, original.text.replace("#file:", "#renamed:"))
				.composerDocument,
		).toBeUndefined();
		expect(
			readInputComposerDocument(original.composerDocument, "other text", original.fileReferences),
		).toBeUndefined();
		expect(
			readInputComposerDocument({ version: 99 }, original.text, original.fileReferences),
		).toBeUndefined();
	});
});

describe("file query boundaries", () => {
	test.each([
		"#",
		"#src",
		"看一下，#src/中文",
		"(#my file",
		"hello\n#src/sub/",
	])("recognizes %s", (text) => {
		expect(query(text)).not.toBeNull();
	});
	test.each([
		"# heading",
		"## heading",
		"word#src",
		"\\#src",
		"https://example.com/a#src",
		"https:#src",
		"[anchor](#src",
		"`#src",
		"```ts\n#src",
		"~~~ts\n#src",
		"> ~~~ts\n> #src",
		"#file:src/a.ts",
		"#src\nnext",
		"#src @user",
	])("ignores non-reference syntax %s", (text) => {
		expect(query(text)).toBeNull();
	});
	test("closes a matching code fence but not a shorter embedded fence", () => {
		expect(query("```ts\ncode\n```\n#src")?.q).toBe("src");
		expect(query("````ts\n```\n#src")).toBeNull();
		expect(query("`code` #src")?.q).toBe("src");
	});
	test("does not activate before the hash or inside an already registered occurrence", () => {
		expect(getFileReferenceQuery("#src", 0)).toBeNull();
		const registered = input();
		expect(
			getFileReferenceQuery(registered.text, registered.text.length - 1, registered.fileReferences),
		).toBeNull();
	});
	test("parses inclusive line queries into exclusive selections", () => {
		expect(query("#src/a.ts:10-20")).toMatchObject({
			search: "src/a.ts",
			selection: { startLineNumber: 10, startColumn: 1, endLineNumber: 21, endColumn: 1 },
		});
		expect(query("#C:\\src\\a.ts:10")?.selection?.endLineNumber).toBe(11);
		expect(query("#src/a:0")?.error).toBe("invalidRange");
		expect(query("#src/a:20-10")?.error).toBe("invalidRange");
		expect(query("#src/a:10-")?.error).toBe("invalidRange");
	});
	test("reports overlong queries without silently searching their prefix", () => {
		expect(query(`#${"x".repeat(MAX_FILE_REFERENCE_QUERY_CHARS + 1)}`)?.error).toBe("queryTooLong");
	});
});

describe("file occurrence editing", () => {
	test("registers only explicit selections, preserving device identity and visible offsets", () => {
		const state = input();
		const ref = state.fileReferences[0];
		expect(ref.deviceId).toBe("RemoteABC");
		expect(state.text.slice(...(ref.inputRange ?? [0, 0]))).toBe(fileReferenceToken(ref));
		expect(
			editFileReferenceInput({ text: "", fileReferences: [] }, "#file:src/a.ts").fileReferences,
		).toEqual([]);
	});
	test("moves offsets before the token and leaves them unchanged after it", () => {
		const state = input();
		const moved = editFileReferenceInput(state, `你好 ${state.text}`);
		const [start, end] = state.fileReferences[0].inputRange ?? [0, 0];
		expect(moved.fileReferences[0].inputRange).toEqual([start + 3, end + 3]);
		const after = editFileReferenceInput(moved, `${moved.text}after`);
		expect(after.fileReferences).toEqual(moved.fileReferences);
		expect(editFileReferenceInput(moved, state.text).fileReferences).toEqual(state.fileReferences);
	});
	test("invalidates an edited token, and undo-like text restoration cannot recreate metadata", () => {
		const state = input();
		const edited = editFileReferenceInput(state, state.text.replace("中文", "wrong"));
		expect(edited.fileReferences).toEqual([]);
		expect(editFileReferenceInput(edited, state.text).fileReferences).toEqual([]);
	});
	test("handles exact boundary insertions and invalidates overlapping replacements", () => {
		const state = input();
		const [start, end] = state.fileReferences[0].inputRange ?? [0, 0];
		const before = editFileReferenceInput(
			state,
			`${state.text.slice(0, start)}#${state.text.slice(start)}`,
			{ start, end: start },
		);
		expect(before.fileReferences[0].inputRange).toEqual([start + 1, end + 1]);
		const after = editFileReferenceInput(
			state,
			`${state.text.slice(0, end)}x${state.text.slice(end)}`,
			{ start: end, end },
		);
		expect(after.fileReferences).toEqual(state.fileReferences);
		expect(
			editFileReferenceInput(state, "gone", { start: 0, end: state.text.length }).fileReferences,
		).toEqual([]);
	});
	test("same-file occurrences have distinct ids and survive disjoint edits independently", () => {
		const first = input();
		const second = insertFileReference(first, reference);
		expect(new Set(second.fileReferences.map((ref) => ref.id)).size).toBe(2);
		const end = second.fileReferences[0].inputRange?.[1] ?? 0;
		const edited = editFileReferenceInput(second, second.text.slice(end), { start: 0, end });
		expect(edited.fileReferences).toHaveLength(1);
		expect(edited.fileReferences[0].id).toBe(second.fileReferences[1].id);
	});
	test("trimming both ends keeps intact references and correct offsets", () => {
		const state = insertFileReference({ text: "  ", fileReferences: [] }, reference);
		const trimmed = trimFileReferenceInput(state);
		expect(trimmed.fileReferences).toHaveLength(1);
		expect(trimmed.fileReferences[0].inputRange).toEqual([0, trimmed.text.length]);
	});
	test("same text with different reference identities is not the same draft", () => {
		const state = input();
		expect(
			sameFileReferenceInput(state, {
				...state,
				fileReferences: state.fileReferences.map((ref) => ({ ...ref, deviceId: "local" })),
			}),
		).toBe(false);
	});
});

describe("bounded metadata and saved selection", () => {
	test("copies only client fields, never a snapshot, and gives callers detached metadata", () => {
		const refs = copyFileReferences([
			{ ...reference, snapshotText: "never store this", snapshotHash: "server-only" },
		]);
		expect(refs).toEqual([reference]);
		refs[0].path = "/changed";
		expect(reference.path).toBe("/work/src/中文 file.ts");
	});
	test("rejects occurrence/path/aggregate budgets rather than silently selecting a prefix", () => {
		expect(() =>
			copyFileReferences(
				Array.from({ length: MAX_FILE_REFERENCE_COUNT + 1 }, (_, i) => ({
					...reference,
					id: String(i),
				})),
			),
		).toThrow();
		expect(() =>
			copyFileReferences([{ ...reference, path: "x".repeat(MAX_FILE_REFERENCE_PATH_CHARS + 1) }]),
		).toThrow();
		expect(() =>
			copyFileReferences(
				Array.from({ length: 16 }, (_, i) => ({
					...reference,
					id: String(i),
					path: "中".repeat(4096),
				})),
			),
		).toThrow();
		const full = Array.from({ length: MAX_FILE_REFERENCE_COUNT }, (_, i) => ({
			...reference,
			id: String(i),
		}));
		expect(() => insertFileReference({ text: "", fileReferences: full }, reference)).toThrow();
		expect(full).toHaveLength(MAX_FILE_REFERENCE_COUNT);
	});
	test("malformed and old metadata fails closed without guessing from text", () => {
		expect(readFileReferences(undefined)).toEqual([]);
		expect(readFileReferences([{ ...reference, inputRange: [0, 5] }], "wrong")).toEqual([]);
		expect(readFileReferences([{ ...reference, path: "bad\npath" }])).toEqual([]);
	});
	test("only a non-dirty saved range produces #selection", () => {
		const selection: FileReferenceEditorSelection = {
			target: {
				deviceId: reference.deviceId,
				path: reference.path,
				selection: { startLineNumber: 10, startColumn: 2, endLineNumber: 20, endColumn: 4 },
			},
			label: reference.label,
			expectedHash: "saved-hash",
			dirty: false,
		};
		expect(savedSelectionReference(selection)).toMatchObject({
			deviceId: reference.deviceId,
			selection: selection.target.selection,
			expectedHash: "saved-hash",
		});
		expect(savedSelectionReference({ ...selection, dirty: true })).toBeNull();
		expect(savedSelectionReference({ ...selection, expectedHash: "" })).toBeNull();
		expect(
			savedSelectionReference({ ...selection, target: { deviceId: "local", path: "/a" } }),
		).toBeNull();
	});
});

describe("keyboard and recent metadata", () => {
	test("IME keys and Shift+Enter are left to the browser", () => {
		const event = { key: "Enter", isComposing: false, keyCode: 13, shiftKey: false };
		expect(fileReferenceKeyAction(event)).toBe("select");
		expect(fileReferenceKeyAction({ ...event, isComposing: true })).toBeNull();
		expect(fileReferenceKeyAction({ ...event, keyCode: 229 })).toBeNull();
		expect(fileReferenceKeyAction({ ...event, shiftKey: true })).toBeNull();
	});
	test("recent files are bounded, device-aware and isolated by trusted scope", () => {
		for (let i = 0; i < 70; i++)
			rememberFileReference("scope-a", { ...reference, path: `/work/${i}` });
		expect(recentFileReferences("scope-a")).toHaveLength(MAX_FILE_REFERENCE_SEARCH_RESULTS);
		expect(recentFileReferences("scope-b")).toEqual([]);
		rememberFileReference("scope-a", { ...reference, deviceId: "local" });
		rememberFileReference("scope-a", reference);
		expect(
			recentFileReferences("scope-a")
				.slice(0, 2)
				.map((item) => item.deviceId),
		).toEqual(["RemoteABC", "local"]);
		for (let i = 0; i < 9; i++) rememberFileReference(`new-scope-${i}`, reference);
		expect(recentFileReferences("scope-a")).toEqual([]);
	});
});

describe("rich composer text-run query boundaries", () => {
	test("a quote or image cannot join a file token from two text runs", () => {
		expect(getFileReferenceQuery("#foo", 4, [], 3)).toBeNull();
		expect(getFileReferenceQuery("#foo", 4, [], 0)?.q).toBe("foo");
	});
	test("the start of a text run is a boundary without requiring a typed space", () => {
		expect(getFileReferenceQuery("before#foo", 10, [], 6)).toMatchObject({
			start: 6,
			end: 10,
			q: "foo",
		});
	});
	test("code fences still use the complete document prefix", () => {
		const text = "```ts\n#foo";
		expect(getFileReferenceQuery(text, text.length, [], 6)).toBeNull();
	});
});
