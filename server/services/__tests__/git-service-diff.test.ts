/**
 * "What changed?" — the diff NarraFork hands to the AI commit-message generator.
 *
 * The failure mode here produced a *plausible* answer rather than an error: an
 * unsupported flag made git exit 129 with no output, and the wrapper's `silent` mode
 * turned that into an empty diff. So the cases assert on the positive shape (a
 * non-empty patch with the expected hunks) instead of merely checking that nothing
 * threw.
 *
 * Real repositories throughout: the behaviour under test *is* git's behaviour
 * (which options exist, what exit codes mean, how binaries are summarized), and a
 * mock would only encode the misunderstanding that caused the bug.
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

const SEED_FILE = "app.txt";
const SEED_CONTENT = "l1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\nl9\nl10\n";

/** A repository with one commit on `main`, nothing else. */
async function createRepo(prefix: string): Promise<string> {
	const dir = mkdtempSync(join(tmpdir(), `nf-${prefix}-`));
	tempDirs.push(dir);
	// -b main so the tests never depend on the host's init.defaultBranch.
	await git(["init", "-b", "main"], dir);
	await git(["config", "user.email", "test@example.com"], dir);
	await git(["config", "user.name", "Test"], dir);
	writeFileSync(join(dir, SEED_FILE), SEED_CONTENT);
	await git(["add", "-A"], dir);
	await git(["commit", "-m", "seed"], dir);
	return dir;
}

/** Bytes that are unambiguously binary to git: a NUL early in the file. */
function binaryBlob(marker: number): Buffer {
	return Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x00, 0x01, marker, 0xff, 0x00, 0xfe]);
}

afterEach(() => {
	for (const dir of tempDirs.splice(0)) {
		rmSync(dir, { recursive: true, force: true });
	}
});

describe("git's own diff contract", () => {
	test("rejects --no-binary outright, and hides binary content without it", async () => {
		// Pins the two git facts the diff fix rests on, so that anyone reintroducing
		// `--no-binary` as a 'safety' flag sees why it cannot work. Asserted against
		// the real binary rather than against our wrapper, because the wrapper's
		// `silent` mode swallows exit codes — which is exactly how this shipped.
		const repo = await createRepo("git-contract");
		writeFileSync(join(repo, "blob.bin"), binaryBlob(0x01));
		await git(["add", "-A"], repo);
		await git(["commit", "-m", "add binary"], repo);
		writeFileSync(join(repo, "blob.bin"), binaryBlob(0x02));

		const rejected = await safeSpawn({
			cmd: ["git", "diff", "HEAD", "--no-binary", "-D", "--no-color"],
			cwd: repo,
			timeout: 15_000,
		});
		// Usage error: the option does not exist, so *nothing* is diffed.
		expect(rejected.exitCode).toBe(129);
		expect(rejected.stdout).toBe("");

		const accepted = await safeSpawn({
			cmd: ["git", "diff", "HEAD", "-D", "--no-color"],
			cwd: repo,
			timeout: 15_000,
		});
		expect(accepted.exitCode).toBe(0);
		expect(accepted.stdout).toContain("Binary files");
		expect(accepted.stdout).not.toContain("GIT binary patch");
	});
});

describe("getFullDiff", () => {
	test("returns the actual patch for uncommitted work", async () => {
		const repo = await createRepo("diff");
		writeFileSync(join(repo, SEED_FILE), SEED_CONTENT.replace("l5", "l5-edited"));

		const diff = await gitService.getFullDiff(repo);

		// The `--no-binary` bug made git exit 129 with no stdout, so this whole method
		// returned "" for every possible input. An emptiness check is the regression test.
		expect(diff).not.toBe("");
		expect(diff).toContain(`--- a/${SEED_FILE}`);
		expect(diff).toContain("+l5-edited");
		expect(diff).toContain("-l5");
	});

	test("includes staged, unstaged and untracked changes together", async () => {
		const repo = await createRepo("diff-all");
		writeFileSync(join(repo, "staged.txt"), "staged content\n");
		await git(["add", "staged.txt"], repo);
		writeFileSync(join(repo, SEED_FILE), SEED_CONTENT.replace("l1", "l1-unstaged"));
		writeFileSync(join(repo, "untracked.txt"), "untracked content\n");

		const diff = await gitService.getFullDiff(repo);

		expect(diff).toContain("+staged content");
		expect(diff).toContain("+l1-unstaged");
		expect(diff).toContain("+untracked content");
	});

	test("names changed binary files without inlining their bytes", async () => {
		const repo = await createRepo("diff-bin");
		writeFileSync(join(repo, "tracked.bin"), binaryBlob(0x01));
		await git(["add", "-A"], repo);
		await git(["commit", "-m", "add binary"], repo);

		// One tracked binary modified, one untracked binary added: the two branches of
		// getFullDiff handle binaries by different code paths (diff HEAD vs --no-index).
		writeFileSync(join(repo, "tracked.bin"), binaryBlob(0x02));
		writeFileSync(join(repo, "fresh.bin"), binaryBlob(0x03));
		writeFileSync(join(repo, SEED_FILE), SEED_CONTENT.replace("l2", "l2-text-edit"));

		const diff = await gitService.getFullDiff(repo);

		// Text still comes through — the point is that dropping `--no-binary` did not
		// require giving up on excluding binary content.
		expect(diff).toContain("+l2-text-edit");
		expect(diff).toContain("tracked.bin");
		expect(diff).toContain("fresh.bin");
		expect(diff).toContain("Binary files");
		// Git's own marker for "content was emitted", produced by `--binary`.
		expect(diff).not.toContain("GIT binary patch");
		// No NUL byte can survive into the diff unless raw bytes were inlined.
		expect(diff.includes("\0")).toBe(false);
	});
});
