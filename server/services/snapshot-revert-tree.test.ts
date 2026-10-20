import { afterEach, describe, expect, test } from "bun:test";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { eq } from "drizzle-orm";
import { db } from "../db";
import { chapters, narrators, projects } from "../db/schema";
import { generateId } from "../lib/id";
import { safeSpawn } from "../lib/spawn";
import {
	assertSnapshotRevertComplete,
	emptyRevertResult,
	resolveNarratorCwd,
	SnapshotRevertError,
	snapshotHistoryFailure,
	unavailableSnapshotRevert,
} from "./snapshot-revert";
import { worktreeTreeSnapshot } from "./worktree-tree-snapshot";

const dirs: string[] = [];
async function fixture() {
	const repo = mkdtempSync(join(tmpdir(), "nf-tree-inspection-"));
	dirs.push(repo);
	expect((await safeSpawn({ cmd: ["git", "init"], cwd: repo, timeout: 15_000 })).exitCode).toBe(0);
	const narratorId = generateId();
	const now = new Date().toISOString();
	db.insert(narrators).values({ id: narratorId, cwd: repo, createdAt: now, updatedAt: now }).run();
	return { repo, narratorId, now };
}
afterEach(async () => {
	for (const dir of dirs.splice(0)) {
		await worktreeTreeSnapshot.destroy(dir);
		rmSync(dir, { recursive: true, force: true });
	}
});

describe("per-file restore results are independent of message history", () => {
	test("unavailable history names the item and cannot authorize a message commit", () => {
		const result = unavailableSnapshotRevert("history_unavailable", "/workspace/a.txt");
		expect(result).toMatchObject({
			status: "failed",
			historyStatus: "unavailable",
			historyCommitted: false,
			files: [
				{ filePath: "/workspace/a.txt", status: "not_attempted", reason: "history_unavailable" },
			],
		});
		expect(() => assertSnapshotRevertComplete(result)).toThrow(SnapshotRevertError);
	});
	test("successful files remain in the failure result when history commit fails", () => {
		const result = {
			...emptyRevertResult(),
			planId: "fixed-plan",
			files: [{ deviceId: "local", filePath: "/workspace/a.txt", status: "applied" as const }],
		};
		const error = snapshotHistoryFailure(result, new Error("db failed"));
		expect(error.result).toMatchObject({
			planId: "fixed-plan",
			status: "partial",
			historyCommitted: false,
			historyStatus: "unavailable",
		});
		expect(error.result.files).toBe(result.files);
	});
	test.each([
		"conflict",
		"failed",
		"unknown",
		"not_attempted",
	] as const)("%s cannot commit history even under a malformed success summary", (status) => {
		expect(() =>
			assertSnapshotRevertComplete({
				...emptyRevertResult(),
				files: [{ deviceId: "local", filePath: "a", status }],
			}),
		).toThrow(SnapshotRevertError);
	});
	test("only explicit empty selection is a successful no-op", () => {
		expect(() => assertSnapshotRevertComplete(emptyRevertResult())).not.toThrow();
		expect(unavailableSnapshotRevert("missing target").files).toHaveLength(1);
	});
});

describe("chapter workspace resolution remains available", () => {
	test.each([
		false,
		true,
	])("project fallback resolves the workspace (chapter=%s)", async (chapter) => {
		const f = await fixture();
		const projectId = generateId();
		db.insert(projects)
			.values({ id: projectId, name: "test", gitPath: f.repo, createdAt: f.now, updatedAt: f.now })
			.run();
		let chapterId: string | null = null;
		if (chapter) {
			chapterId = generateId();
			db.insert(chapters)
				.values({
					id: chapterId,
					projectId,
					title: "dormant",
					status: "dormant",
					branch: "test",
					baseBranch: "main",
					worktreePath: null,
					createdAt: f.now,
					updatedAt: f.now,
				})
				.run();
		}
		db.update(narrators)
			.set({ cwd: null, chapterId, contextProjectId: projectId })
			.where(eq(narrators.id, f.narratorId))
			.run();
		expect(await resolveNarratorCwd(f.narratorId)).toBe(f.repo);
	});
});
