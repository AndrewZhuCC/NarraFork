import { afterEach, describe, expect, spyOn, test } from "bun:test";
import { mkdirSync, mkdtempSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import * as fs from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import * as spawn from "../lib/spawn";
import { gitService } from "./git-service";
import { worktreeWatcherMetadata } from "./worktree-watcher-metadata";

const tempDirs: string[] = [];
const singleFile = { totalFiles: 1, files: [{ path: "seed.txt" }] };

async function git(cwd: string, ...args: string[]) {
	const result = await spawn.safeSpawn({
		cmd: ["git", ...args],
		cwd,
		timeout: 15_000,
		maxOutputBytes: 8192,
	});
	expect(result.exitCode).toBe(0);
	return result;
}

async function createRepo() {
	const dir = mkdtempSync(join(tmpdir(), "nf-watch-metadata-"));
	tempDirs.push(dir);
	await git(dir, "init");
	await git(dir, "config", "user.email", "test@example.com");
	await git(dir, "config", "user.name", "Test");
	writeFileSync(join(dir, "seed.txt"), "seed\n");
	await git(dir, "add", "seed.txt");
	await git(dir, "commit", "-m", "seed");
	return dir;
}

function deferred<T>() {
	let resolve!: (value: T) => void;
	let reject!: (error: unknown) => void;
	const promise = new Promise<T>((yes, no) => {
		resolve = yes;
		reject = no;
	});
	return { promise, resolve, reject };
}

function flushInspection(): Promise<void> {
	return new Promise((resolve) => setImmediate(resolve));
}

afterEach(() => {
	for (const dir of tempDirs.splice(0)) rmSync(dir, { force: true, recursive: true });
});

describe("bounded watcher metadata", () => {
	test("fingerprints same-size rewrites without reading content", async () => {
		const repo = await createRepo();
		const reads = spyOn(fs, "readFile").mockImplementation(() => {
			throw new Error("metadata probe must not read file contents");
		});
		try {
			const before = await worktreeWatcherMetadata.probe(repo, singleFile);
			expect(before.uncertain).toBe(false);
			expect(await worktreeWatcherMetadata.probe(repo, singleFile)).toEqual(before);
			writeFileSync(join(repo, "seed.txt"), "edit\n");
			const after = await worktreeWatcherMetadata.probe(repo, singleFile);
			expect(after.uncertain).toBe(false);
			expect(after.fingerprint).not.toBe(before.fingerprint);
			expect(reads).not.toHaveBeenCalled();
		} finally {
			reads.mockRestore();
		}
	});

	test("index metadata alone changes the fingerprint", async () => {
		const repo = await createRepo();
		const empty = { files: [], totalFiles: 0 };
		const before = await worktreeWatcherMetadata.probe(repo, empty);
		await git(repo, "update-index", "--assume-unchanged", "seed.txt");
		const after = await worktreeWatcherMetadata.probe(repo, empty);
		expect(before.uncertain).toBe(false);
		expect(after.uncertain).toBe(false);
		expect(after.fingerprint).not.toBe(before.fingerprint);
	});

	test("uses real CWD-relative status for subdirectories, aliases and linked worktrees", async () => {
		const repo = await createRepo();
		const subdir = join(repo, "nested");
		mkdirSync(subdir);
		writeFileSync(join(subdir, "seed.txt"), "seed\n");
		await git(repo, "add", "nested/seed.txt");
		await git(repo, "commit", "-m", "nested fixture");
		const linked = join(repo, "linked");
		await git(repo, "worktree", "add", "--detach", linked, "HEAD");
		const alias = join(repo, "nested-alias");
		symlinkSync(subdir, alias, "junction");

		for (const cwd of [subdir, alias, join(linked, "nested")]) {
			writeFileSync(join(cwd, "seed.txt"), "aaaa\n");
			const status = await gitService.getStatusSummary(cwd);
			expect(status.files).toEqual([{ path: "seed.txt", status: " M" }]);
			const before = await worktreeWatcherMetadata.probe(cwd, status);
			expect(before.uncertain).toBe(false);
			const [targets] = JSON.parse(before.fingerprint) as [string[], string[]];
			expect(targets).toContain(join(await fs.realpath(cwd), "seed.txt"));
			expect(await worktreeWatcherMetadata.probe(cwd, status)).toEqual(before);

			writeFileSync(join(cwd, "seed.txt"), "bbbb\n");
			const sameSizeStatus = await gitService.getStatusSummary(cwd);
			expect(sameSizeStatus).toEqual(status);
			const sameSize = await worktreeWatcherMetadata.probe(cwd, sameSizeStatus);
			expect(sameSize.uncertain).toBe(false);
			expect(sameSize.fingerprint).not.toBe(before.fingerprint);

			writeFileSync(join(cwd, "seed.txt"), "bbbb\nanother line\n");
			const addedLineStatus = await gitService.getStatusSummary(cwd);
			expect(addedLineStatus).toEqual(status);
			const addedLine = await worktreeWatcherMetadata.probe(cwd, addedLineStatus);
			expect(addedLine.uncertain).toBe(false);
			expect(addedLine.fingerprint).not.toBe(sameSize.fingerprint);
		}

		const linkedSubdir = join(linked, "nested");
		const status = await gitService.getStatusSummary(linkedSubdir);
		const before = await worktreeWatcherMetadata.probe(linkedSubdir, status);
		expect(before.fingerprint).toContain("worktrees");
		await git(linked, "update-index", "--assume-unchanged", "seed.txt");
		const after = await worktreeWatcherMetadata.probe(linkedSubdir, status);
		expect(after.uncertain).toBe(false);
		expect(after.fingerprint).not.toBe(before.fingerprint);
	});

	test("deleted paths have stable missing metadata; old rename paths are included", async () => {
		const repo = await createRepo();
		const renamed = { totalFiles: 1, files: [{ path: "seed.txt", oldPath: "old.txt" }] };
		const before = await worktreeWatcherMetadata.probe(repo, renamed);
		expect(before.uncertain).toBe(false);
		expect(await worktreeWatcherMetadata.probe(repo, renamed)).toEqual(before);
		writeFileSync(join(repo, "old.txt"), "recreated\n");
		const after = await worktreeWatcherMetadata.probe(repo, renamed);
		expect(after.uncertain).toBe(false);
		expect(after.fingerprint).not.toBe(before.fingerprint);
	});

	test("truncated, inexact, omitted and directory paths are uncertain", async () => {
		const repo = await createRepo();
		mkdirSync(join(repo, "submodule"));
		for (const status of [
			{ ...singleFile, truncated: true },
			{ ...singleFile, totalFilesExact: false },
			{ ...singleFile, totalFiles: 2 },
			{ totalFiles: 1, files: [{ path: "submodule" }] },
			{ totalFiles: 1, files: [{ path: "../outside" }] },
			{ totalFiles: 1, files: [{ path: "a".repeat(4097) }] },
		]) {
			expect((await worktreeWatcherMetadata.probe(repo, status)).uncertain).toBe(true);
		}
	});

	test("metadata permission failure is uncertain rather than a stable idle result", async () => {
		const repo = await createRepo();
		const stat = spyOn(fs, "lstat").mockRejectedValue(
			Object.assign(new Error("denied"), { code: "EACCES" }),
		);
		try {
			expect((await worktreeWatcherMetadata.probe(repo, singleFile)).uncertain).toBe(true);
		} finally {
			stat.mockRestore();
		}
	});

	test("caps file metadata at 200 paths and 10 concurrent stats", async () => {
		const repo = await createRepo();
		const realStat = await fs.lstat(join(repo, "seed.txt"), { bigint: true });
		let active = 0;
		let peak = 0;
		const fakeStat = (async () => {
			active++;
			peak = Math.max(peak, active);
			await Promise.resolve();
			active--;
			return realStat;
		}) as unknown as typeof fs.lstat; // The probe exclusively uses the bigint overload.
		const stat = spyOn(fs, "lstat").mockImplementation(fakeStat);
		try {
			const result = await worktreeWatcherMetadata.probe(repo, {
				totalFiles: 250,
				files: Array.from({ length: 250 }, (_, i) => ({ path: `file-${i}.txt` })),
			});
			expect(result.uncertain).toBe(true);
			expect(stat).toHaveBeenCalledTimes(201); // 200 paths plus the Git index
			expect(stat.mock.calls.every(([, options]) => options?.bigint === true)).toBe(true);
			expect(peak).toBeLessThanOrEqual(10);
			expect(peak).toBeGreaterThan(1);
		} finally {
			stat.mockRestore();
		}
	});

	test("timed-out lstat batches stay gated across polls until every syscall settles", async () => {
		const root = await fs.realpath(tmpdir());
		const command = spyOn(spawn, "safeSpawn").mockResolvedValue({
			exitCode: 0,
			stdout: `${root}\n${join(root, ".git", "index")}\n`,
			stderr: "",
		});
		const blocked = deferred<never>();
		const stat = spyOn(fs, "lstat").mockImplementation(() => blocked.promise);
		const canonical = spyOn(fs, "realpath");
		try {
			const started = performance.now();
			const status = {
				totalFiles: 200,
				files: Array.from({ length: 200 }, (_, i) => ({ path: `file-${i}.txt` })),
			};
			const result = await worktreeWatcherMetadata.probe(root, status, { budgetMs: 20 });
			expect(result.uncertain).toBe(true);
			expect(performance.now() - started).toBeLessThan(1000);
			for (let poll = 0; poll < 20; poll++) {
				expect(
					await worktreeWatcherMetadata.probe(join(root, "."), status, { budgetMs: 20 }),
				).toEqual({ fingerprint: "", uncertain: true });
			}
			expect(canonical).toHaveBeenCalledTimes(1);
			expect(command).toHaveBeenCalledTimes(1);
			expect(stat).toHaveBeenCalledTimes(10);
			expect(command.mock.calls[0][0].signal?.aborted).toBe(true);
			expect(command.mock.calls[0][0].maxOutputBytes).toBe(8194);
			blocked.reject(new Error("late I/O failure"));
			await flushInspection();
			expect(stat).toHaveBeenCalledTimes(10); // No more paths scheduled after cancellation.
			stat.mockRejectedValue(Object.assign(new Error("missing"), { code: "ENOENT" }));
			expect((await worktreeWatcherMetadata.probe(root, singleFile)).uncertain).toBe(false);
			expect(command).toHaveBeenCalledTimes(2);
		} finally {
			blocked.reject(new Error("test cleanup"));
			await flushInspection();
			stat.mockRestore();
			canonical.mockRestore();
			command.mockRestore();
		}
	});

	test("hung realpath remains gated across polls and a late failure permits recovery", async () => {
		const blocked = deferred<never>();
		const canonical = spyOn(fs, "realpath").mockImplementation(() => blocked.promise);
		const command = spyOn(spawn, "safeSpawn");
		try {
			const result = await worktreeWatcherMetadata.probe(tmpdir(), singleFile, { budgetMs: 20 });
			expect(result.uncertain).toBe(true);
			for (let poll = 0; poll < 20; poll++)
				expect((await worktreeWatcherMetadata.probe(tmpdir(), singleFile)).uncertain).toBe(true);
			expect(canonical).toHaveBeenCalledTimes(1);
			expect(command).not.toHaveBeenCalled();
			blocked.reject(new Error("late realpath failure"));
			await flushInspection();
			canonical.mockRejectedValue(new Error("missing directory"));
			expect((await worktreeWatcherMetadata.probe(tmpdir(), singleFile)).uncertain).toBe(true);
			expect(canonical).toHaveBeenCalledTimes(2);
			expect(command).not.toHaveBeenCalled();
		} finally {
			blocked.reject(new Error("test cleanup"));
			await flushInspection();
			canonical.mockRestore();
			command.mockRestore();
		}
	});

	test("timed-out Git probes keep their slot until subprocess settlement", async () => {
		const blocked = deferred<never>();
		const command = spyOn(spawn, "safeSpawn").mockImplementation(() => blocked.promise);
		const stat = spyOn(fs, "lstat");
		try {
			const root = await fs.realpath(tmpdir());
			const result = await worktreeWatcherMetadata.probe(root, singleFile, { budgetMs: 20 });
			expect(result.uncertain).toBe(true);
			for (let poll = 0; poll < 20; poll++)
				expect((await worktreeWatcherMetadata.probe(root, singleFile)).uncertain).toBe(true);
			expect(command).toHaveBeenCalledTimes(1);
			expect(stat).not.toHaveBeenCalled();
			expect(command.mock.calls[0][0].signal?.aborted).toBe(true);
			expect(command.mock.calls[0][0].timeout).toBeLessThanOrEqual(20);
		} finally {
			blocked.reject(new Error("late Git failure"));
			await flushInspection();
			stat.mockRestore();
			command.mockRestore();
		}
	});

	test("truncated git-path output cannot become a valid fingerprint", async () => {
		const root = await fs.realpath(tmpdir());
		const command = spyOn(spawn, "safeSpawn").mockResolvedValue({
			exitCode: 0,
			stdout: `${root}\n${join(root, ".git", "index")}\n`,
			stderr: "",
			stdoutTruncated: true,
		});
		const stat = spyOn(fs, "lstat");
		try {
			expect((await worktreeWatcherMetadata.probe(root, singleFile)).uncertain).toBe(true);
			expect(stat).not.toHaveBeenCalled();
		} finally {
			stat.mockRestore();
			command.mockRestore();
		}
	});
});

describe("metadata probe admission across timeouts", () => {
	test("a partially settled batch still prevents new filesystem work", async () => {
		const root = await fs.realpath(tmpdir());
		const metadata = await fs.lstat(root, { bigint: true });
		const pending = Array.from({ length: 10 }, () => deferred<typeof metadata>());
		const command = spyOn(spawn, "safeSpawn").mockResolvedValue({
			exitCode: 0,
			stdout: `${root}\n${join(root, ".git", "index")}\n`,
			stderr: "",
		});
		let started = 0;
		const stat = spyOn(fs, "lstat").mockImplementation(
			(() => pending[started++].promise) as unknown as typeof fs.lstat, // bigint overload only
		);
		try {
			const status = {
				totalFiles: 20,
				files: Array.from({ length: 20 }, (_, i) => ({ path: `${i}.txt` })),
			};
			expect((await worktreeWatcherMetadata.probe(root, status, { budgetMs: 20 })).uncertain).toBe(
				true,
			);
			expect(started).toBe(10);
			for (const request of pending.slice(0, 9)) request.resolve(metadata);
			await flushInspection();
			expect((await worktreeWatcherMetadata.probe(root, status)).uncertain).toBe(true);
			expect(command).toHaveBeenCalledTimes(1);
			expect(started).toBe(10);
			pending[9].resolve(metadata);
			await flushInspection();
			stat.mockRejectedValue(Object.assign(new Error("missing"), { code: "ENOENT" }));
			expect((await worktreeWatcherMetadata.probe(root, singleFile)).uncertain).toBe(false);
			expect(command).toHaveBeenCalledTimes(2);
		} finally {
			for (const request of pending) request.resolve(metadata);
			await flushInspection();
			stat.mockRestore();
			command.mockRestore();
		}
	});

	test("abandoned directories have a global bound and late settlement releases capacity", async () => {
		const blocked = deferred<never>();
		const canonical = spyOn(fs, "realpath").mockImplementation(() => blocked.promise);
		const command = spyOn(spawn, "safeSpawn");
		try {
			const results = await Promise.all(
				Array.from({ length: 129 }, (_, i) =>
					worktreeWatcherMetadata.probe(join(tmpdir(), "nf-watch-pending", String(i)), singleFile, {
						budgetMs: 20,
					}),
				),
			);
			expect(results.every((result) => result.uncertain)).toBe(true);
			expect(canonical).toHaveBeenCalledTimes(128);
			for (let i = 0; i < 10; i++)
				await worktreeWatcherMetadata.probe(
					join(tmpdir(), "nf-watch-overflow", String(i)),
					singleFile,
				);
			expect(canonical).toHaveBeenCalledTimes(128);
			expect(command).not.toHaveBeenCalled();
			blocked.reject(new Error("filesystem recovered with missing paths"));
			await flushInspection();
			canonical.mockRejectedValue(new Error("missing path"));
			await worktreeWatcherMetadata.probe(
				join(tmpdir(), "nf-watch-overflow", "recovered"),
				singleFile,
			);
			expect(canonical).toHaveBeenCalledTimes(129);
		} finally {
			blocked.reject(new Error("test cleanup"));
			await flushInspection();
			canonical.mockRestore();
			command.mockRestore();
		}
	});
});
