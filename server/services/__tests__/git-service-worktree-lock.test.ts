/**
 * The safety net for the worktree mutex behind `gitService`'s write methods.
 *
 * A worktree is guarded by exactly one lock: `worktreeLock` from `lib/async-mutex`. Every
 * `gitService` write method (stage, unstage, commit, discard, stash, reset) wraps its whole
 * command sequence in it, so two writes on one worktree run one after the other instead of
 * interleaving `git` invocations on the same index.
 *
 * Three properties are pinned here:
 *
 *   1. The locked methods still do their work for callers that hold nothing — the
 *      `POST /api/git/...` routes — including the early-return guards for empty lists.
 *   2. Writes on one worktree are serialized (also across differently spelled paths),
 *      while writes on unrelated worktrees are not.
 *   3. A write method called from inside a held `worktreeLock` block *does* hang. The
 *      mutex is not re-entrant, so this pins the hazard as real: code that ever holds a
 *      worktree across several commands must not call these methods from inside its block.
 *
 * Real repositories throughout: the behaviour under test is git's own (which options
 * exist, what exit codes mean), and a mock would encode whatever misunderstanding made a
 * deadlock possible. The bounded `Promise.race` is essential — a plain `await` on a
 * self-deadlock wedges the test runner instead of failing it, so a regression would look
 * like CI hanging rather than a red test.
 */
import { afterEach, describe, expect, test } from "bun:test";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { worktreeLock } from "../../lib/async-mutex";
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

/** A repository with two commits on `main`. */
async function createRepo(): Promise<string> {
	const dir = mkdtempSync(join(tmpdir(), "nf-wtlock-"));
	tempDirs.push(dir);
	await git(["init", "-b", "main"], dir);
	await git(["config", "user.email", "test@example.com"], dir);
	await git(["config", "user.name", "Test"], dir);
	writeFileSync(join(dir, SEED_FILE), "one\n");
	await git(["add", "-A"], dir);
	await git(["commit", "-m", "seed"], dir);
	writeFileSync(join(dir, SEED_FILE), "one\ntwo\n");
	await git(["add", "-A"], dir);
	await git(["commit", "-m", "second"], dir);
	return dir;
}

/**
 * How long a git write may take before we call it a deadlock.
 *
 * Pinned from both sides. It must exceed a slow-but-healthy run — these spawn real `git`
 * processes on whatever filesystem CI provides, and observed cases finish in under 120ms,
 * so 3s leaves over an order of magnitude of headroom. It must also stay *below* bun's 5s
 * per-test timeout, because otherwise bun kills the test first and reports a bare "timed
 * out" with no hint that a lock caused it.
 */
const WRITE_TIMEOUT_MS = 3_000;

/**
 * How long a queued write is watched to confirm it is NOT running. Much shorter, since it
 * is paid on every green run, and no amount of waiting would change a blocked write's
 * outcome.
 */
const BLOCKED_WRITE_WATCH_MS = 300;

class LockDeadlineError extends Error {}

/** Reject rather than hang if `fn` does not settle, so a deadlock fails the test. */
async function withinDeadline<T>(
	label: string,
	fn: () => Promise<T>,
	ms = WRITE_TIMEOUT_MS,
): Promise<T> {
	let timer: ReturnType<typeof setTimeout> | undefined;
	try {
		return await Promise.race([
			fn(),
			new Promise<never>((_, reject) => {
				timer = setTimeout(
					() => reject(new LockDeadlineError(`${label} did not settle within ${ms}ms`)),
					ms,
				);
			}),
		]);
	} finally {
		if (timer) clearTimeout(timer);
	}
}

/** Explicit barriers, not sleeps: the holder releases exactly when the test says so. */
function barrier() {
	let release!: () => void;
	const promise = new Promise<void>((resolve) => {
		release = resolve;
	});
	return { promise, release };
}

async function stagedFiles(dir: string): Promise<string> {
	return git(["diff", "--cached", "--name-only"], dir);
}

afterEach(() => {
	for (const dir of tempDirs.splice(0)) {
		rmSync(dir, { recursive: true, force: true });
	}
});

describe("gitService locked writes for callers holding no lock", () => {
	test("locked writes still perform their work", async () => {
		const dir = await createRepo();
		writeFileSync(join(dir, SEED_FILE), "dirty\n");
		await gitService.stageAll(dir);
		expect(await stagedFiles(dir)).toBe(SEED_FILE);
		await gitService.unstageAll(dir);
		expect(await stagedFiles(dir)).toBe("");
		await gitService.stageFiles(dir, [SEED_FILE]);
		expect(await stagedFiles(dir)).toBe(SEED_FILE);
		await gitService.unstageFiles(dir, [SEED_FILE]);
		expect(await stagedFiles(dir)).toBe("");
		await gitService.stageAll(dir);
		const sha = await gitService.commit(dir, "locked commit");
		expect(sha).toMatch(/^[0-9a-f]{40}$/);
		expect(await git(["log", "-1", "--format=%s"], dir)).toBe("locked commit");
		expect(await git(["status", "--porcelain"], dir)).toBe("");
	});

	test("stash, pop and drop round-trip", async () => {
		const dir = await createRepo();
		writeFileSync(join(dir, SEED_FILE), "dirty\n");
		await gitService.stash(dir, "locked stash");
		expect(await git(["status", "--porcelain"], dir)).toBe("");
		expect((await gitService.stashPop(dir)).hasConflicts).toBe(false);
		expect(await git(["status", "--porcelain"], dir)).not.toBe("");
		await gitService.stash(dir, "locked stash again");
		await gitService.stashDrop(dir, 0);
		expect(await gitService.stashList(dir)).toEqual([]);
	});

	test("discard and reset restore the worktree", async () => {
		const dir = await createRepo();
		writeFileSync(join(dir, SEED_FILE), "dirty\n");
		await gitService.discardFiles(dir, [SEED_FILE]);
		expect(await git(["status", "--porcelain"], dir)).toBe("");
		writeFileSync(join(dir, SEED_FILE), "dirty\n");
		writeFileSync(join(dir, "extra.txt"), "untracked\n");
		await gitService.discardAll(dir);
		expect(await git(["status", "--porcelain"], dir)).toBe("");
		await gitService.resetSoft(dir, "HEAD~1");
		expect(await git(["log", "-1", "--format=%s"], dir)).toBe("seed");
		await gitService.resetHard(dir, "HEAD");
		expect(await git(["status", "--porcelain"], dir)).toBe("");
	});

	test("empty file lists remain no-ops rather than staging everything", async () => {
		const dir = await createRepo();
		writeFileSync(join(dir, SEED_FILE), "dirty\n");
		// Without the early return, discard's status query would carry no pathspec, list
		// every change in the worktree, and then check out / clean all of it.
		await gitService.stageFiles(dir, []);
		expect(await stagedFiles(dir)).toBe("");
		await gitService.unstageFiles(dir, []);
		await gitService.discardFiles(dir, []);
		// Nothing was discarded either.
		expect(await git(["status", "--porcelain"], dir)).not.toBe("");
	});
});

describe("gitService write serialization", () => {
	test("queued writes on one worktree run one after the other, in call order", async () => {
		// What the lock is actually for. A stage racing a commit on one index would
		// otherwise produce `index.lock` contention or a commit holding the other's file.
		const dir = await createRepo();
		writeFileSync(join(dir, "a.txt"), "a\n");
		writeFileSync(join(dir, "b.txt"), "b\n");
		await Promise.all([
			gitService.stageFiles(dir, ["a.txt"]),
			gitService.commit(dir, "commit a"),
			gitService.stageFiles(dir, ["b.txt"]),
			gitService.commit(dir, "commit b"),
		]);
		expect(await git(["log", "-2", "--format=%s"], dir)).toBe("commit b\ncommit a");
		expect(await git(["show", "--name-only", "--format=", "HEAD~1"], dir)).toBe("a.txt");
		expect(await git(["show", "--name-only", "--format=", "HEAD"], dir)).toBe("b.txt");
		expect(await git(["status", "--porcelain"], dir)).toBe("");
	});

	test("a write through a path variant waits for the holder of the canonical spelling", async () => {
		// Normalization must make a differently-spelled path hit the *same* key, so the
		// write queues instead of running concurrently with the holder.
		const dir = await createRepo();
		const variant = `${dir}/`;
		expect(worktreeLock.key(variant)).toBe(worktreeLock.key(dir));
		writeFileSync(join(dir, SEED_FILE), "dirty\n");

		const release = barrier();
		const holder = worktreeLock.acquire(dir, () => release.promise);
		let staged = false;
		const write = gitService.stageAll(variant).then(() => {
			staged = true;
		});
		try {
			await new Promise((r) => setTimeout(r, BLOCKED_WRITE_WATCH_MS));
			expect(staged).toBe(false);
			expect(await stagedFiles(dir)).toBe("");
		} finally {
			release.release();
		}
		await withinDeadline("holder", () => holder);
		await withinDeadline("queued variant write", () => write);
		expect(await stagedFiles(dir)).toBe(SEED_FILE);
	});

	test("two distinct worktrees are not serialized against each other", async () => {
		// Normalization must not over-merge keys: serializing unrelated worktrees would be
		// a throughput regression invisible to every other test here.
		const a = await createRepo();
		const b = await createRepo();
		const release = barrier();
		const holder = worktreeLock.acquire(a, () => release.promise);
		try {
			writeFileSync(join(b, SEED_FILE), "dirty\n");
			await withinDeadline("stageAll on an unrelated worktree", () => gitService.stageAll(b));
			const sha = await withinDeadline("commit on an unrelated worktree", () =>
				gitService.commit(b, "independent"),
			);
			expect(sha).toMatch(/^[0-9a-f]{40}$/);
		} finally {
			release.release();
		}
		await holder;
	});
});

describe("the hazard of calling a write method while holding worktreeLock", () => {
	test("a write makes no progress while its own caller holds the lock", async () => {
		const dir = await createRepo();
		writeFileSync(join(dir, SEED_FILE), "dirty\n");
		await git(["add", "-A"], dir);
		let innerSettled = false;

		// Deliberately the wrong call, to pin that the danger is real rather than
		// hypothetical. If someone makes `AsyncMutex` re-entrant, this test fails and says
		// so — at which point the guidance on `withWorktreeLock` can be revisited on purpose.
		//
		// The waiter is *launched* inside the block and awaited outside it, which is what
		// makes the mechanism visible: the nested call is queued behind its own caller, so
		// it can only proceed once that caller returns. Awaiting it inline instead would
		// mean the caller never returns and the wait never ends — a request that hangs
		// forever with nothing thrown and nothing logged.
		let inner!: Promise<string>;
		await worktreeLock.acquire(dir, async () => {
			inner = gitService.commit(dir, "queued behind its own caller");
			void inner.then(() => {
				innerSettled = true;
			});
			// Long enough that a non-blocked commit would have finished several times over.
			await new Promise((r) => setTimeout(r, BLOCKED_WRITE_WATCH_MS));
			expect(innerSettled).toBe(false);
		});

		// Only now, with the holder gone, can it run. Note this also means the orphaned
		// call is not cancelled — it eventually performs its write, out of order with
		// respect to whatever the holder did afterwards, which is its own hazard.
		expect(
			await withinDeadline("the queued write, after its caller returned", () => inner),
		).toMatch(/^[0-9a-f]{40}$/);
	});
});
