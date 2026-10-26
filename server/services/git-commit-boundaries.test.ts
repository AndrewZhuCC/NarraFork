/**
 * git-commit-boundaries.test.ts — where does "this round of changes" begin.
 *
 * The Git panel shows UNCOMMITTED changes, so attribution for a file must start at that
 * file's last commit. A single repository-wide HEAD timestamp is not that boundary: in
 * the NarraFork repository 90 of 127 changed files were last committed before HEAD, by a
 * median of 164 hours, and using HEAD dropped real contributors.
 *
 * These tests build throwaway repositories so the boundaries are exact and independent
 * of any developer's history.
 */
import { afterEach, describe, expect, test } from "bun:test";
import { mkdtempSync, renameSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { safeSpawn } from "../lib/spawn";
import { clearBoundaryCache } from "./git-commit-boundary-cache";
import { gitService } from "./git-service";
import { clearStatusCache } from "./git-status-cache";

const repos: string[] = [];

afterEach(() => {
	// Both caches are keyed by workspace path. Temp directories are unique per test, so a
	// leftover entry cannot be served to another test — but clearing keeps the process from
	// retaining every throwaway repository's status for its lifetime.
	clearStatusCache();
	clearBoundaryCache();
	for (const dir of repos.splice(0)) rmSync(dir, { recursive: true, force: true });
});

/**
 * A git identity is required for `commit` to work in a clean environment.
 *
 * Asynchronous with its own deadline. This used to be `Bun.spawnSync`: on a loaded 4-vCPU
 * runner a synchronous child that never returned blocked the whole worker, and every
 * remaining test in the file then timed out with "killed 1 dangling process".
 */
async function run(cwd: string, args: string[], date?: string): Promise<void> {
	const result = await safeSpawn({
		cmd: ["git", ...args],
		cwd,
		timeout: 15_000,
		env: {
			...process.env,
			GIT_AUTHOR_NAME: "Test",
			GIT_AUTHOR_EMAIL: "test@example.com",
			GIT_COMMITTER_NAME: "Test",
			GIT_COMMITTER_EMAIL: "test@example.com",
			...(date ? { GIT_AUTHOR_DATE: date, GIT_COMMITTER_DATE: date } : {}),
		},
	});
	if (result.exitCode !== 0) {
		throw new Error(`git ${args.join(" ")} failed: ${result.stderr || result.stdout}`);
	}
}

async function makeRepo(): Promise<string> {
	const dir = mkdtempSync(join(tmpdir(), "nf-boundary-"));
	repos.push(dir);
	await run(dir, ["init", "-q", "-b", "main"]);
	return dir;
}

/** Move a file on disk, leaving git to detect the rename from content similarity. */
function rename(dir: string, from: string, to: string): void {
	renameSync(join(dir, from), join(dir, to));
}

/**
 * Commit `files`, optionally at a fixed date.
 *
 * Dates must be given explicitly whenever a test compares two boundaries: `%cI` has
 * one-second precision, so two commits made in the same second are indistinguishable
 * and an ordering assertion would fail for reasons unrelated to the code.
 */
async function commit(
	dir: string,
	files: Record<string, string>,
	message: string,
	date?: string,
): Promise<void> {
	for (const [path, content] of Object.entries(files)) {
		writeFileSync(join(dir, path), content);
	}
	await run(dir, ["add", "-A"]);
	await run(dir, ["commit", "-q", "-m", message], date);
}

describe("getLastCommitTimes", () => {
	test("reports each path's own last commit, not a single repository boundary", async () => {
		const dir = await makeRepo();
		await commit(dir, { "old.txt": "v1", "new.txt": "v1" }, "both", "2026-01-01T00:00:00+00:00");
		// A later commit touching only one file: the other's boundary must stay behind.
		await commit(dir, { "new.txt": "v2" }, "only new", "2026-06-01T00:00:00+00:00");

		const { byPath } = await gitService.getLastCommitTimes(dir, ["old.txt", "new.txt"]);

		const older = byPath.get("old.txt") ?? "";
		const newer = byPath.get("new.txt") ?? "";
		expect(older).not.toBe("");
		expect(newer).not.toBe("");
		// This inequality is the entire point: a global HEAD boundary would report the
		// same, newer timestamp for both and discard old.txt's real history window.
		expect(older < newer).toBe(true);
	});

	test("returns UTC timestamps so string comparison against changedAt is valid", async () => {
		// `%cI` emits a local offset; `file_attributions.changedAt` is UTC with `Z`.
		// Compared as strings, "2026-08-14T18:19:20+08:00" sorts AFTER
		// "2026-08-14T11:00:00Z" despite being two hours earlier.
		const dir = await makeRepo();
		await commit(dir, { "a.txt": "v1" }, "one");

		const { byPath, oldestInWindow } = await gitService.getLastCommitTimes(dir, ["a.txt"]);

		expect(byPath.get("a.txt")).toMatch(/Z$/);
		expect(oldestInWindow).toMatch(/Z$/);
	});

	test("omits a path that was never committed", async () => {
		const dir = await makeRepo();
		await commit(dir, { "tracked.txt": "v1" }, "one");
		writeFileSync(join(dir, "untracked.txt"), "fresh");

		const { byPath } = await gitService.getLastCommitTimes(dir, ["tracked.txt", "untracked.txt"]);

		expect(byPath.has("tracked.txt")).toBe(true);
		// No boundary means "everything recorded for it belongs to the current change",
		// which is exactly right for a file that has never been committed.
		expect(byPath.has("untracked.txt")).toBe(false);
	});

	test("oldestInWindow is the walk's oldest commit, usable as a fallback boundary", async () => {
		const dir = await makeRepo();
		await commit(dir, { "a.txt": "v1" }, "first", "2026-01-01T00:00:00+00:00");
		await commit(dir, { "a.txt": "v2" }, "second", "2026-06-01T00:00:00+00:00");

		const { byPath, oldestInWindow } = await gitService.getLastCommitTimes(dir, ["a.txt"]);

		// The fallback must never be newer than a resolved boundary would allow it to be
		// used in place of; being older-or-equal keeps it conservative.
		const fallback = oldestInWindow ?? "";
		const resolved = byPath.get("a.txt") ?? "";
		expect(fallback).not.toBe("");
		expect(resolved).not.toBe("");
		expect(fallback <= resolved).toBe(true);
	});

	test("an unborn HEAD yields no boundaries instead of failing", async () => {
		// A fresh chapter worktree can legitimately have zero commits.
		const dir = await makeRepo();
		writeFileSync(join(dir, "a.txt"), "v1");

		const result = await gitService.getLastCommitTimes(dir, ["a.txt"]);

		expect(result.byPath.size).toBe(0);
		expect(result.oldestInWindow).toBeNull();
	});

	test("an empty path list does no git work", async () => {
		const dir = await makeRepo();
		await commit(dir, { "a.txt": "v1" }, "one");

		const result = await gitService.getLastCommitTimes(dir, []);

		expect(result.byPath.size).toBe(0);
		expect(result.oldestInWindow).toBeNull();
	});

	test("paths beyond the cap are omitted rather than truncating the argument vector", async () => {
		const dir = await makeRepo();
		const files: Record<string, string> = {};
		for (let i = 0; i < 5; i++) files[`f${i}.txt`] = "v1";
		await commit(dir, files, "many");

		// Far more paths than the 200 cap; the call must still succeed and resolve the
		// ones it kept.
		const padded = [...Object.keys(files), ...Array.from({ length: 400 }, (_, i) => `pad${i}.txt`)];
		const { byPath } = await gitService.getLastCommitTimes(dir, padded);

		expect(byPath.get("f0.txt")).toBeDefined();
		expect(byPath.size).toBeLessThanOrEqual(200);
	});

	test("a path containing spaces is matched exactly", async () => {
		const dir = await makeRepo();
		await commit(dir, { "with space.txt": "v1" }, "spaced");

		const { byPath } = await gitService.getLastCommitTimes(dir, ["with space.txt"]);

		// Paths go through argv, never a shell, so no quoting is involved — but the
		// marker-based parse must not split on whitespace either.
		expect(byPath.get("with space.txt")).toBeDefined();
	});

	test("a renamed file's OLD path still resolves, which is where its history lives", async () => {
		// Attribution rows carry the path that existed when the write happened, so a
		// renamed file's whole recorded history sits under a path that is no longer in the
		// tree. The boundary walk must be able to bound those rows.
		const dir = await makeRepo();
		await commit(dir, { "old.txt": "v1" }, "create", "2026-01-01T00:00:00+00:00");
		rename(dir, "old.txt", "new.txt");

		const { byPath } = await gitService.getLastCommitTimes(dir, ["old.txt", "new.txt"]);

		// `old.txt` was committed; `new.txt` exists only as an uncommitted rename, so it
		// has no boundary of its own yet.
		expect(byPath.get("old.txt")).toBeDefined();
		expect(byPath.has("new.txt")).toBe(false);
	});
});
