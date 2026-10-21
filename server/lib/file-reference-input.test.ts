import { describe, expect, mock, test } from "bun:test";
import type { FileReference, FileReferenceSnapshot } from "@shared/file-reference";
import { ValidationError } from "./errors";
import { parseFileReferenceInput, replaceFileReferenceSnapshots } from "./file-reference-input";

function reference(id = "r1", path = "/work/a.ts"): FileReference {
	return { id, deviceId: "local", path, label: "a.ts" };
}
function frozen(ref = reference(), text = "original bytes"): FileReferenceSnapshot {
	return {
		type: "file_reference",
		reference: ref,
		snapshotText: text,
		snapshotHash: "h",
		capturedAt: "2026-01-01T00:00:00Z",
	};
}

describe("file-reference HTTP input and edit snapshot reuse", () => {
	test("JSON and multipart share the same strict reference format", () => {
		expect(parseFileReferenceInput([reference()])).toEqual(
			parseFileReferenceInput(JSON.stringify([reference()])),
		);
		expect(parseFileReferenceInput(undefined)).toBeUndefined();
		expect(parseFileReferenceInput([])).toEqual([]);
		for (const input of [
			"{",
			[{ ...reference(), snapshotText: "forged" }],
			[reference(), reference()],
		]) {
			expect(() => parseFileReferenceInput(input)).toThrow(ValidationError);
		}
	});

	test("JSON and multipart preserve hash kinds while rejecting unknown kinds or missing hashes", () => {
		const legacy = { ...reference(), expectedHash: "a".repeat(64) };
		const parsed = parseFileReferenceInput([legacy]);
		expect(parsed).toEqual([legacy]);
		expect(parsed?.[0]).not.toHaveProperty("expectedHashKind");
		for (const expectedHashKind of ["decoded-sha256", "raw-sha256"] as const) {
			const refs = [{ ...legacy, expectedHashKind }];
			expect(parseFileReferenceInput(refs)).toEqual(refs);
			expect(parseFileReferenceInput(JSON.stringify(refs))).toEqual(refs);
		}
		for (const item of [
			{ ...legacy, expectedHashKind: "sha256" },
			{ ...legacy, expectedHashKind: null },
			{ ...reference(), expectedHashKind: "raw-sha256" },
			{ ...reference(), expectedHashKind: "decoded-sha256", expectedHash: "" },
		]) {
			for (const input of [[item], JSON.stringify([item])])
				expect(() => parseFileReferenceInput(input)).toThrow(ValidationError);
		}
	});

	test("omission preserves and an empty replacement removes without rereading", async () => {
		const capture = mock(async () => []);
		expect(await replaceFileReferenceSnapshots([frozen()], undefined, capture)).toBeUndefined();
		expect(await replaceFileReferenceSnapshots([frozen()], [], capture)).toEqual([]);
		expect(capture).not.toHaveBeenCalled();
	});

	test("a text-only edit keeps accepted bytes even after the source disappears", async () => {
		const capture = mock(async (): Promise<FileReferenceSnapshot[]> => {
			throw new Error("file gone");
		});
		const previous = frozen();
		const result = await replaceFileReferenceSnapshots(
			[previous],
			[{ ...previous.reference, inputRange: [5, 10] }],
			capture,
		);
		expect(result?.[0]?.snapshotText).toBe("original bytes");
		expect(result?.[0]?.reference.inputRange).toEqual([5, 10]);
		expect(previous.reference.inputRange).toBeUndefined();
		expect(capture).not.toHaveBeenCalled();
	});

	test("an id from another message and a changed target require fresh capture", async () => {
		const capture = mock(async (refs: readonly FileReference[]) =>
			refs.map((ref) => frozen(ref, "new bytes")),
		);
		const result = await replaceFileReferenceSnapshots(
			[frozen()],
			[reference("r1", "/work/b.ts"), reference("foreign")],
			capture,
		);
		expect(capture).toHaveBeenCalledTimes(1);
		expect(capture.mock.calls[0]?.[0]).toHaveLength(2);
		expect(result?.map((item) => item.snapshotText)).toEqual(["new bytes", "new bytes"]);
	});

	test("legacy and explicit decoded kinds preserve an accepted snapshot without rereading", async () => {
		const previous = frozen({ ...reference(), expectedHash: "a".repeat(64) });
		const noRead = mock(async (): Promise<FileReferenceSnapshot[]> => {
			throw new Error("source no longer exists");
		});
		const result = await replaceFileReferenceSnapshots(
			[previous],
			[{ ...previous.reference, expectedHashKind: "decoded-sha256" }],
			noRead,
		);
		expect(result?.[0]?.snapshotText).toBe(previous.snapshotText);
		expect(result?.[0]?.reference).not.toHaveProperty("expectedHashKind");
		expect(noRead).not.toHaveBeenCalled();
	});

	test("changing hash kind recaptures while unchanged raw metadata reuses accepted bytes", async () => {
		const legacy = frozen({ ...reference(), expectedHash: "a".repeat(64) });
		const raw = { ...legacy.reference, expectedHashKind: "raw-sha256" as const };
		const capture = mock(async (refs: readonly FileReference[]) =>
			refs.map((ref) => frozen(ref, "raw-verified selection")),
		);
		const result = await replaceFileReferenceSnapshots([legacy], [raw], capture);
		expect(capture).toHaveBeenCalledTimes(1);
		expect(capture.mock.calls[0]?.[0]).toEqual([raw]);
		expect(result?.[0]).toMatchObject({
			reference: raw,
			snapshotText: "raw-verified selection",
		});
		const noRead = mock(async (): Promise<FileReferenceSnapshot[]> => {
			throw new Error("source no longer exists");
		});
		expect(await replaceFileReferenceSnapshots(result ?? [], [raw], noRead)).toEqual(result);
		expect(noRead).not.toHaveBeenCalled();
		const decoded = { ...raw, expectedHashKind: "decoded-sha256" as const };
		await replaceFileReferenceSnapshots(result ?? [], [decoded], capture);
		expect(capture).toHaveBeenCalledTimes(2);
		expect(capture.mock.calls[1]?.[0]).toEqual([decoded]);
		await replaceFileReferenceSnapshots(result ?? [], [legacy.reference], capture);
		expect(capture).toHaveBeenCalledTimes(3);
	});

	test("canonicalized additions cannot overflow the merged metadata budget", async () => {
		const longPath = `/${"x".repeat(4090)}.ts`;
		const retained = Array.from({ length: 15 }, (_, index) =>
			frozen({ ...reference(`r${index}`, longPath), label: "label".repeat(20) }),
		);
		const added = reference("new", "/s");
		const requested = [...retained.map((item) => item.reference), added];
		// Both the original request and the canonicalized addition are individually valid.
		expect(parseFileReferenceInput(requested)).toHaveLength(16);
		const expanded = { ...added, path: longPath };
		expect(parseFileReferenceInput([expanded])).toHaveLength(1);
		await expect(
			replaceFileReferenceSnapshots(retained, requested, async () => [frozen(expanded)]),
		).rejects.toThrow(ValidationError);
	});
});
