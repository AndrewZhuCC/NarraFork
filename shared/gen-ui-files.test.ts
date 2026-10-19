import { describe, expect, test } from "bun:test";
import { GEN_UI_ENTRY, GEN_UI_FILE_LIMITS, readGenUiFileReference } from "./gen-ui-files";

const reference = {
	version: 1 as const,
	toolUseId: "tool-use",
	toolCallId: "tool-call",
	deviceId: "local",
	workspaceKey: "workspace",
	cwd: "/project",
	directory: "/project/.narrafork/gen_ui/订单-form-abc",
	entry: GEN_UI_ENTRY,
	title: "订单表单",
};

describe("file-backed GenUI metadata", () => {
	test("keeps only the ordinary-file reference, not source or revisions", () => {
		expect(readGenUiFileReference({ ...reference, document: "private", epoch: 3 })).toEqual(
			reference,
		);
	});

	test("rejects legacy document references and malformed values", () => {
		for (const value of [
			null,
			[],
			"ui",
			{ uiId: "old", revision: "old" },
			{ ...reference, version: 2 },
		]) {
			expect(readGenUiFileReference(value)).toBeNull();
		}
		for (const key of Object.keys(reference).filter((key) => key !== "version")) {
			for (const value of [null, undefined, "", "x".repeat(4097)]) {
				expect(readGenUiFileReference({ ...reference, [key]: value })).toBeNull();
			}
		}
	});

	test("limits compile input/output without inventing a history quota", () => {
		expect(GEN_UI_FILE_LIMITS.fileBytes).toBe(512 * 1024);
		expect(GEN_UI_FILE_LIMITS.sourceBytes).toBe(2 * 1024 * 1024);
		expect(GEN_UI_FILE_LIMITS.outputBytes).toBe(4 * 1024 * 1024);
		expect(GEN_UI_FILE_LIMITS.files).toBe(128);
		expect(GEN_UI_FILE_LIMITS.timeoutMs).toBe(15_000);
		expect("historyRefs" in GEN_UI_FILE_LIMITS).toBe(false);
	});
});
