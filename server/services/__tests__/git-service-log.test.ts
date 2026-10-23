/**
 * `gitService.getLog` — the history page behind `GET /api/git/.../log`.
 *
 * Built on a history with a merge, so a page can mix mainline and side-branch commits
 * and the merge commit carries two parents. A real repository throughout: the
 * behaviour under test is git's own walk, and a mock would just re-encode an
 * assumption about it.
 */
import { afterEach, describe, expect, test } from "bun:test";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { safeSpawn } from "../../lib/spawn";
import { gitService } from "../git-service";

const tempDirs: string[] = [];

async function git(args: string[], cwd: string): Promise<string> {
	const result = await safeSpawn({ cmd: ["git", ...args], cwd, timeout: 15_000 });
	if (result.exitCode !== 0) {
		throw new Error(`git ${args.join(" ")} failed: ${result.stderr || result.stdout}`);
	}
	return result.stdout.trim();
}

/** Commit one new file named after `message`. */
async function commitFile(dir: string, message: string): Promise<void> {
	writeFileSync(join(dir, `${message}.txt`), `${message}\n`);
	await git(["add", "-A"], dir);
	await git(["commit", "-m", message], dir);
}

async function createRepo(prefix: string): Promise<string> {
	const dir = mkdtempSync(join(tmpdir(), `nf-${prefix}-`));
	tempDirs.push(dir);
	await git(["init", "-b", "main"], dir);
	await git(["config", "user.email", "test@example.com"], dir);
	await git(["config", "user.name", "Test"], dir);
	await git(["config", "commit.gpgsign", "false"], dir);
	return dir;
}

/**
 *   main:  seed ── A ─────────── M (merge)
 *            └─ side: S ── S2 ──┘
 */
async function createMergedHistory(): Promise<string> {
	const dir = await createRepo("gitlog");
	await commitFile(dir, "seed");
	await git(["checkout", "-b", "side"], dir);
	await commitFile(dir, "S");
	await commitFile(dir, "S2");
	await git(["checkout", "main"], dir);
	await commitFile(dir, "A");
	await git(["merge", "--no-ff", "-m", "M", "side"], dir);
	return dir;
}

afterEach(() => {
	while (tempDirs.length > 0) {
		const dir = tempDirs.pop();
		if (dir) rmSync(dir, { recursive: true, force: true });
	}
});

describe("getLog", () => {
	test("consecutive skip pages concatenate to one continuous walk", async () => {
		const dir = await createMergedHistory();
		const pageSize = 2;
		const page1 = await gitService.getLog(dir, { limit: pageSize, skip: 0, branch: "main" });
		const page2 = await gitService.getLog(dir, {
			limit: pageSize,
			skip: pageSize,
			branch: "main",
		});
		const continuous = await gitService.getLog(dir, {
			limit: pageSize * 2,
			skip: 0,
			branch: "main",
		});
		expect(page1).toHaveLength(pageSize);
		expect(page2).toHaveLength(pageSize);
		expect([...page1, ...page2].map((c) => c.sha)).toEqual(continuous.map((c) => c.sha));
	});

	test("returns parents from git %P: merge has 2, root has 0", async () => {
		const dir = await createMergedHistory();
		const all = await gitService.getLog(dir, { limit: 100, skip: 0, branch: "main" });
		const byMessage = Object.fromEntries(all.map((c) => [c.message, c]));
		const merge = byMessage.M;
		const root = byMessage.seed;
		if (!merge || !root) throw new Error("expected merge M and root seed in log");

		expect(merge.parents).toHaveLength(2);
		// parents are full SHAs of the two first-parent and side tips at merge time
		for (const parent of merge.parents) {
			expect(parent).toMatch(/^[0-9a-f]{40}$/);
		}
		const parentShas = new Set(merge.parents);
		expect(parentShas.has(byMessage.A?.sha ?? "")).toBe(true);
		expect(parentShas.has(byMessage.S2?.sha ?? "")).toBe(true);

		expect(root.parents).toEqual([]);
		// Every entry always exposes a parents array (never undefined) on the local path.
		for (const entry of all) {
			expect(Array.isArray(entry.parents)).toBe(true);
		}
	});

	test("a repository with no commits yet has an empty history, not an error", async () => {
		const dir = await createRepo("gitlog-unborn");
		expect(await gitService.getLog(dir)).toEqual([]);
	});
});
