import { describe, expect, test } from "bun:test";
import { localBackend } from "../lib/agent/execution/local-backend";
import { applyLocalFileChange } from "./file-change-apply-result";
import type {
	FileChangeLocalIo,
	LocalFileApplyInput,
	LocalFileApplyResult,
} from "./file-change-local-io";
import { normalizeLocalIoEvidence } from "./file-change-local-io-evidence";
import type { LocalWriteFootprint } from "./file-change-write-footprint";

const path = localBackend.paths.resolve(process.cwd(), "fake-range/file.txt");
const parent = localBackend.paths.dirname(path);
const footprint: LocalWriteFootprint = {
	anchor: process.cwd(),
	anchorIdentity: "fixture",
	ranges: [{ kind: "subtree", canonicalPath: parent }],
};
const input: LocalFileApplyInput = {
	backend: localBackend,
	canonicalPath: path,
	lexicalPath: path,
	before: { bytes: null, mode: null, identity: null },
	nextBytes: new Uint8Array(),
	signal: new AbortController().signal,
	assertTarget: async () => {},
	onDispatch() {},
};
const emptyParents = { createdPaths: [], possiblePaths: [] };
function ioResult(result: LocalFileApplyResult): FileChangeLocalIo {
	return { read: async () => input.before, apply: async () => result };
}

describe("runtime trusts explicit local IO stage proofs only", () => {
	test("cancellation does not demote confirmed applied IO", async () => {
		const summary = await applyLocalFileChange(
			ioResult({ kind: "applied", error: null, parentEffects: emptyParents }),
			{ ...input, signal: AbortSignal.abort() },
			footprint,
		);
		expect(summary).toMatchObject({
			receiptOutcome: "applied",
			confirmed: true,
		});
	});

	test("editor receipts prove only their original operation and exact output", async () => {
		const digest = "a".repeat(64);
		const editor = {
			sourceId: "operation",
			userId: "user",
			narratorId: "narrator",
			preparedResult: {},
			snapshotRevision: 1,
			requestDigest: "b".repeat(64),
			root: process.cwd(),
			runtime: { runtimeEpoch: "epoch", runtimeGeneration: 0 },
			rawDigest: digest,
		};
		const receipt = {
			operationId: "operation",
			outcome: "committed" as const,
			rawDigest: digest,
			totalBytes: 0,
		};
		const valid = await applyLocalFileChange(
			ioResult({
				kind: "applied",
				error: null,
				parentEffects: emptyParents,
				editorReceipt: receipt,
			}),
			{ ...input, editor },
			footprint,
		);
		expect(valid).toMatchObject({ receiptOutcome: "applied", confirmed: true });
		for (const change of [
			{ operationId: "other" },
			{ rawDigest: "c".repeat(64) },
			{ totalBytes: 1 },
		]) {
			const invalid = await applyLocalFileChange(
				ioResult({
					kind: "applied",
					error: null,
					parentEffects: emptyParents,
					editorReceipt: { ...receipt, ...change },
				}),
				{ ...input, editor },
				footprint,
			);
			expect(invalid).toMatchObject({ receiptOutcome: "unknown", confirmed: false });
		}
		const tool = await applyLocalFileChange(
			ioResult({
				kind: "applied",
				error: null,
				parentEffects: emptyParents,
				editorReceipt: receipt,
			}),
			input,
			footprint,
		);
		expect(tool.receiptOutcome).toBe("unknown");
	});

	test("cancellation-shaped adapter rejection remains unknown", async () => {
		const summary = await applyLocalFileChange(
			{
				read: async () => input.before,
				apply: async () => {
					throw new DOMException("Aborted", "AbortError");
				},
			},
			{ ...input, signal: AbortSignal.abort() },
			footprint,
		);
		expect(summary).toMatchObject({
			receiptOutcome: "unknown",
			confirmed: false,
		});
	});
	test("bare EACCES or absent state is not evidence of no mutation", async () => {
		const io: FileChangeLocalIo = {
			read: async () => input.before,
			apply: async () => {
				throw Object.assign(new Error("wrapper failed after writing"), { code: "EACCES" });
			},
		};
		const summary = await applyLocalFileChange(io, input, footprint);
		expect(summary).toMatchObject({
			receiptOutcome: "unknown",
			confirmed: false,
		});
	});

	test("completed parent creation does not pretend the target was applied", async () => {
		const summary = await applyLocalFileChange(
			ioResult({
				kind: "parent_only",
				error: new Error("target denied"),
				parentEffects: { createdPaths: [parent], possiblePaths: [] },
			}),
			input,
			footprint,
		);
		expect(summary).toMatchObject({
			receiptOutcome: "not_applied",
			confirmed: true,
			diagnostics: { outcome: "parent_only", createdParentCount: 1 },
		});
	});

	test("uncertain parent creation records only the current operation effect", async () => {
		const summary = await applyLocalFileChange(
			ioResult({
				kind: "parent_only",
				error: new Error("parent IO failed"),
				parentEffects: { createdPaths: [], possiblePaths: [parent] },
			}),
			input,
			footprint,
		);
		expect(summary).toMatchObject({
			receiptOutcome: "not_applied",
			confirmed: true,
			diagnostics: { outcome: "parent_only", uncertainParentCount: 1 },
		});
	});

	test("parent effects outside the grant are never accepted as safe", async () => {
		const summary = await applyLocalFileChange(
			ioResult({
				kind: "parent_only",
				error: new Error("bad adapter"),
				parentEffects: { createdPaths: [process.cwd()], possiblePaths: [] },
			}),
			input,
			footprint,
		);
		expect(summary).toMatchObject({
			receiptOutcome: "unknown",
			confirmed: false,
		});
		expect(String(summary.result.error)).toContain("escaped");
	});

	test("unsupported or missing results are conservative, never implicit applied", async () => {
		for (const result of [
			undefined,
			{ kind: "applied", parentEffects: emptyParents, error: new Error("close failed") },
			{
				kind: "not_applied",
				error: new Error("bad"),
				parentEffects: { createdPaths: [parent], possiblePaths: [] },
			},
		]) {
			const summary = await applyLocalFileChange(
				ioResult(result as LocalFileApplyResult),
				input,
				footprint,
			);
			expect(summary.receiptOutcome).toBe("unknown");
			expect(summary.confirmed).toBe(false);
		}
	});

	test("bounded receipt diagnostics reject hidden fields and invalid counts", () => {
		expect(() =>
			normalizeLocalIoEvidence({
				version: 1,
				outcome: "applied",
				createdParentCount: 0,
				uncertainParentCount: 1,
			}),
		).toThrow();
		expect(() =>
			normalizeLocalIoEvidence({
				version: 1,
				outcome: "parent_only",
				createdParentCount: 129,
				uncertainParentCount: 0,
			}),
		).toThrow();
		expect(
			normalizeLocalIoEvidence({
				version: 1,
				outcome: "not_applied",
				createdParentCount: 0,
				uncertainParentCount: 0,
			}),
		).toEqual({
			version: 1,
			outcome: "not_applied",
			createdParentCount: 0,
			uncertainParentCount: 0,
		});
	});
});
