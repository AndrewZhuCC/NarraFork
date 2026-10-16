import { describe, expect, test } from "bun:test";
import { classifyToolDetail } from "@shared/pretext-layout/tool-detail";
import { measureToolBody } from "../narrator/vlist/measure/measure-tool-call";
import { installCanvasStub } from "../narrator/vlist/measure/test-canvas-stub";
import { fileReference } from "./test-fixtures";

installCanvasStub();
describe("GenUI file preview classification", () => {
	for (const action of ["create", "show"]) {
		test(`${action} reserves one small file card, without duplicating tool input/output`, () => {
			const result = classifyToolDetail({
				toolName: "GenUI",
				toolUseId: fileReference.toolUseId,
				category: "generic",
				status: "completed",
				inputJson: { action, prefix: "demo", path: "App.tsx" },
				outputJson: "Done",
				metadata: { genUi: { ...fileReference, narratorId: "forged" } },
			});
			expect(result?.sections).toHaveLength(1);
			const body = result?.sections[0].body;
			expect(body).toEqual({ kind: "gen-ui", file: fileReference });
			if (!body) throw new Error("Missing card");
			expect(measureToolBody(body, 360).height).toBe(88);
		});
	}
	test("a COW card selects its visible row without rewriting the stored file reference", () => {
		const result = classifyToolDetail({
			toolName: "GenUI",
			toolUseId: fileReference.toolUseId,
			toolCallId: "visible-copy",
			category: "generic",
			metadata: { genUi: fileReference },
		});
		expect(result?.sections[0].body).toEqual({
			kind: "gen-ui",
			file: { ...fileReference, toolCallId: "visible-copy" },
		});
		expect(fileReference.toolCallId).toBe("call/1");
	});
	test("remote create without journal exposes a rollback warning in the special card", () => {
		const result = classifyToolDetail({
			toolName: "GenUI",
			toolUseId: fileReference.toolUseId,
			category: "generic",
			metadata: { genUi: fileReference, fileChangeHistoryUnavailable: true },
		});
		expect(result?.sections[0].body).toMatchObject({
			kind: "gen-ui",
			historyUnavailable: true,
		});
	});
	test("a running wait: true render answers its call; a finished one does not", () => {
		const classify = (status: string) =>
			classifyToolDetail({
				toolName: "GenUI",
				toolUseId: "form-use",
				category: "generic",
				status,
				inputJson: {
					action: "render",
					wait: true,
					source: "<Button onClick={submit()}>Go</Button>",
				},
			})?.sections[0].body;
		expect(classify("running")).toMatchObject({ kind: "gen-ui", formToolUseId: "form-use" });
		expect(classify("completed")).not.toHaveProperty("formToolUseId");
		const plain = classifyToolDetail({
			toolName: "GenUI",
			toolUseId: "plain-use",
			category: "generic",
			status: "running",
			inputJson: { action: "render", source: "<Stack />" },
		})?.sections[0].body;
		expect(plain).not.toHaveProperty("formToolUseId");
	});
	test("a shown app reserves room to run in the card; a created one stays a small card", () => {
		const shownAt = "2026-10-16T19:00:00.000Z";
		const shown = classifyToolDetail({
			toolName: "GenUI",
			toolUseId: fileReference.toolUseId,
			category: "generic",
			status: "completed",
			inputJson: { action: "show", path: "App.tsx", size: "lg" },
			metadata: { genUi: fileReference, genUiShow: { version: 1, shownAt, size: "lg" } },
		})?.sections[0].body;
		expect(shown).toMatchObject({ kind: "gen-ui", appShownAt: shownAt, contentPx: 580 });
		const created = classifyToolDetail({
			toolName: "GenUI",
			toolUseId: fileReference.toolUseId,
			category: "generic",
			status: "completed",
			inputJson: { action: "create", prefix: "demo" },
			metadata: { genUi: fileReference },
		})?.sections[0].body;
		expect(created).not.toHaveProperty("appShownAt");
		expect(created).not.toHaveProperty("contentPx");
	});
	test("public summaries never carry a private file reference", () => {
		const result = classifyToolDetail({
			toolName: "GenUI",
			toolUseId: "public",
			category: "generic",
			publicSummary: "GEN_UI_PRIVATE",
			metadata: { genUi: fileReference },
		});
		expect(result?.sections[0].body).toEqual({
			kind: "gen-ui",
			file: null,
			publicSummary: true,
		});
	});
	test("legacy metadata is unavailable, not a compatibility document", () => {
		const result = classifyToolDetail({
			toolName: "GenUI",
			toolUseId: "old",
			category: "generic",
			status: "completed",
			metadata: { genUi: { uiId: "old", revision: "r1" } },
		});
		expect(result?.sections[0].body).toEqual({ kind: "gen-ui", file: null });
	});
	test("incomplete input cannot become a file capability and ordinary Read stays ordinary", () => {
		expect(
			classifyToolDetail({
				toolName: "GenUI",
				toolUseId: "pending",
				category: "generic",
				status: "running",
				inputJson: { genUi: fileReference },
			}),
		).toBeNull();
		const read = classifyToolDetail({
			toolName: "Read",
			toolUseId: "read",
			category: "read",
			status: "completed",
			inputJson: { file_path: ".narrafork/gen_ui/demo-123/App.tsx" },
			outputJson: "export default function App() { return null; }",
		});
		expect(read?.sections.some((part) => part.body.kind === "gen-ui")).toBe(false);
	});
});
