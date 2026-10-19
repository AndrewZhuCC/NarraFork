import { afterEach, describe, expect, setDefaultTimeout, spyOn, test } from "bun:test";
import { mkdirSync, mkdtempSync, rmSync, symlinkSync, utimesSync, writeFileSync } from "node:fs";
import * as fs from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { Worker } from "node:worker_threads";
import type { SafeSpawnOptions } from "../lib/spawn";
import * as spawn from "../lib/spawn";
import {
	getGitLineStats,
	gitLineStatsWorkerSpecifiers,
	parseGitLineNumstat,
} from "./git-line-stats";
import { UntrackedLineReader } from "./git-untracked-line-worker";

setDefaultTimeout(30_000);
const roots: string[] = [];
const spies: Array<{ mockRestore(): void }> = [];
function directory(): string {
	const root = mkdtempSync(join(tmpdir(), "nf-git-lines-"));
	roots.push(root);
	return root;
}
async function git(cwd: string, ...args: string[]): Promise<void> {
	const result = await spawn.safeSpawn({ cmd: ["git", ...args], cwd, timeout: 15_000 });
	if (result.exitCode) throw new Error(result.stderr);
}
async function repo(): Promise<string> {
	const cwd = directory();
	await git(cwd, "init", "-q", "-b", "main");
	await git(cwd, "config", "user.email", "test@example.com");
	await git(cwd, "config", "user.name", "Test");
	return cwd;
}
async function commit(cwd: string): Promise<void> {
	await git(cwd, "add", ".");
	await git(cwd, "commit", "-q", "-m", "base");
}
function mockGit(
	output: (options: SafeSpawnOptions) => { text?: string; truncated?: boolean; exitCode?: number },
): void {
	spies.push(
		spyOn(spawn, "safeSpawn").mockImplementation(async (options) => {
			const result = output(options);
			if (result.text) options.stdoutStream?.onChunk(Buffer.from(result.text));
			return {
				stdout: "",
				stderr: "",
				exitCode: result.exitCode ?? 0,
				stdoutTruncated: result.truncated,
				terminationReason: result.truncated ? "output-limit" : undefined,
			};
		}),
	);
}

afterEach(() => {
	for (const spy of spies.splice(0)) spy.mockRestore();
	for (const root of roots.splice(0)) rmSync(root, { recursive: true, force: true });
});

describe("independent Git line statistics", () => {
	test("sums index + worktree + untracked, not HEAD net changes", async () => {
		const cwd = await repo();
		writeFileSync(join(cwd, "tracked.txt"), "original\n");
		await commit(cwd);
		writeFileSync(join(cwd, "tracked.txt"), "staged\n");
		await git(cwd, "add", "tracked.txt");
		writeFileSync(join(cwd, "tracked.txt"), "original\n");
		writeFileSync(join(cwd, "new.txt"), "one\ntwo");
		const result = await getGitLineStats(cwd);
		expect(result).toMatchObject({ linesAdded: 4, linesRemoved: 2, complete: true });
		expect(result.files.find((file) => file.path === "tracked.txt")).toEqual({
			path: "tracked.txt",
			stagedLinesAdded: 1,
			stagedLinesRemoved: 1,
			unstagedLinesAdded: 1,
			unstagedLinesRemoved: 1,
			complete: true,
		});
		expect(result.files.find((file) => file.path === "new.txt")).toMatchObject({
			stagedLinesAdded: 0,
			unstagedLinesAdded: 2,
			complete: true,
		});
	});

	test("staged deletion and untracked recreation share one row without losing either side", async () => {
		const cwd = await repo();
		writeFileSync(join(cwd, "same.txt"), "original\n");
		await commit(cwd);
		await git(cwd, "rm", "--cached", "same.txt");
		writeFileSync(join(cwd, "same.txt"), "one\ntwo\n");
		const result = await getGitLineStats(cwd);
		expect(result).toEqual({
			files: [
				{
					path: "same.txt",
					stagedLinesAdded: 0,
					stagedLinesRemoved: 1,
					unstagedLinesAdded: 2,
					unstagedLinesRemoved: 0,
					complete: true,
				},
			],
			linesAdded: 2,
			linesRemoved: 1,
			complete: true,
		});
	});

	test("worker URLs resolve next to the source and probe compiled layouts", () => {
		expect(
			gitLineStatsWorkerSpecifiers(false, "file:///project/server/services/git-line-stats.ts"),
		).toEqual(["file:///project/server/services/git-untracked-line-worker.ts"]);
		expect(gitLineStatsWorkerSpecifiers(true, "file:///$bunfs/root/binary")).toEqual([
			"file:///$bunfs/root/services/git-untracked-line-worker.js",
			"file:///$bunfs/root/server/services/git-untracked-line-worker.js",
			"file:///$bunfs/root/git-untracked-line-worker.js",
		]);
	});

	test("unborn repositories need no HEAD and binary files measure zero text lines", async () => {
		const cwd = await repo();
		writeFileSync(join(cwd, "staged.txt"), "one\ntwo\n");
		writeFileSync(join(cwd, "staged.bin"), Buffer.from([0, 10]));
		await git(cwd, "add", ".");
		writeFileSync(join(cwd, "loose.bin"), Buffer.from([0, 10]));
		expect(await getGitLineStats(cwd)).toMatchObject({
			linesAdded: 2,
			linesRemoved: 0,
			complete: true,
		});
	});

	test("scopes both numstat root-relative paths and cwd-relative untracked paths", async () => {
		const cwd = await repo();
		const sub = join(cwd, "nested");
		const trackedName = process.platform === "win32" ? "tracked name.txt" : "tracked\tname.txt";
		const newName = process.platform === "win32" ? "new name.txt" : "new\nname.txt";
		mkdirSync(sub);
		writeFileSync(join(sub, trackedName), "base\n");
		writeFileSync(join(cwd, "outside.txt"), "base\n");
		await commit(cwd);
		writeFileSync(join(sub, trackedName), "base\ninside\n");
		writeFileSync(join(sub, newName), "a\nb\n");
		writeFileSync(join(cwd, "outside.txt"), "base\noutside\n");
		const result = await getGitLineStats(sub);
		expect(result).toMatchObject({ linesAdded: 3, complete: true });
		expect(result.files.map((file) => file.path).sort()).toEqual([newName, trackedName]);
	});

	test("rename NUL records keep their destination, never a partial source name", () => {
		expect(parseGitLineNumstat("1\t2\t\0sub/old\nfile\0sub/new\tfile\0", "sub/")).toEqual({
			files: [{ path: "new\tfile", added: 1, removed: 2 }],
			complete: true,
		});
		expect(parseGitLineNumstat("3\t4\tgood\0" + "1\t2\t\0old\0partial")).toEqual({
			files: [{ path: "good", added: 3, removed: 4 }],
			complete: false,
		});
		expect(parseGitLineNumstat("-\t-\tbinary\0").files[0]).toEqual({
			path: "binary",
			added: 0,
			removed: 0,
		});
	});

	test("truncated command keeps measured complete records and unknown missing sides", async () => {
		mockGit(({ cmd, timeout, stdoutStream, maxStderrBytes }) => {
			expect(cmd).toContain("--no-optional-locks");
			expect(timeout).toBe(30_000);
			expect(stdoutStream?.maxBytes).toBe(1024 * 1024);
			expect(maxStderrBytes).toBe(8192);
			if (cmd.includes("rev-parse")) return { text: "sub/\n" };
			if (cmd.includes("--cached")) return { text: "2\t1\tsub/good\0" };
			if (cmd.includes("diff")) return { text: "3\t4\tsub/other\0bad", truncated: true };
			return {};
		});
		const result = await getGitLineStats("/unused");
		expect(result).toMatchObject({ linesAdded: 5, linesRemoved: 5, complete: false });
		expect(result.files.find((file) => file.path === "good")).toMatchObject({
			stagedLinesAdded: 2,
			complete: false,
		});
		expect(result.files.find((file) => file.path === "good")?.unstagedLinesAdded).toBeUndefined();
		expect(result.files.map((file) => file.path)).toEqual(["good", "other"]);
	});

	test("real stdout byte cap stops the child and keeps a nonzero measured prefix", async () => {
		const original = spawn.safeSpawn;
		spies.push(
			spyOn(spawn, "safeSpawn").mockImplementation((options) =>
				original({
					...options,
					cmd: [
						process.execPath,
						"-e",
						options.cmd.includes("--cached")
							? 'process.stdout.write("1\\t0\\tknown\\0".repeat(150000))'
							: "void 0",
					],
				}),
			),
		);
		const result = await getGitLineStats(directory());
		expect(result.complete).toBe(false);
		expect(result.linesAdded).toBe(Math.floor((1024 * 1024) / 10));
		expect(result.linesRemoved).toBe(0);
		expect(result.files).toHaveLength(1);
		expect(result.files[0].path).toBe("known");
	});

	test("final DTO stays within 1 MiB and totals describe only its measured prefix", async () => {
		mockGit(({ cmd }) =>
			cmd.includes("--cached")
				? { text: Array.from({ length: 9000 }, (_, i) => `1\t0\tfile-${i}\0`).join("") }
				: {},
		);
		const result = await getGitLineStats("/unused");
		expect(result.complete).toBe(false);
		expect(result.files.length).toBeGreaterThan(0);
		expect(result.files.length).toBeLessThan(9000);
		expect(result.linesAdded).toBe(result.files.length);
		expect(Buffer.byteLength(JSON.stringify(result))).toBeLessThanOrEqual(1024 * 1024);
	});

	test("final DTO never returns more than 10000 files even with unknown sides", async () => {
		mockGit(({ cmd }) => {
			if (cmd.includes("--cached")) {
				return { text: Array.from({ length: 10001 }, (_, i) => `1\t0\tf${i}\0`).join("") };
			}
			return cmd.includes("diff") ? { truncated: true } : {};
		});
		const result = await getGitLineStats("/unused");
		expect(result.complete).toBe(false);
		expect(result.files).toHaveLength(10000);
		expect(result.linesAdded).toBe(10000);
		expect(Buffer.byteLength(JSON.stringify(result))).toBeLessThanOrEqual(1024 * 1024);
	});

	test("more than 200 untracked paths returns a useful bounded partial measurement", async () => {
		const cwd = await repo();
		for (let i = 0; i < 201; i++) writeFileSync(join(cwd, `${i}.txt`), "one\n");
		const result = await getGitLineStats(cwd);
		expect(result).toMatchObject({ linesAdded: 200, complete: false });
		expect(result.files).toHaveLength(200);
	});

	test("ordinary concurrent repositories serialize their batches on the same worker", async () => {
		const repos = await Promise.all([repo(), repo(), repo(), repo()]);
		for (const cwd of repos) writeFileSync(join(cwd, "new.txt"), "one\n");
		const results = await Promise.all(repos.map((cwd) => getGitLineStats(cwd)));
		expect(results.every((result) => result.complete && result.linesAdded === 1)).toBe(true);
	});

	test("worker queue is bounded and waiting cancellation removes requests immediately", async () => {
		const cwd = directory();
		writeFileSync(join(cwd, "new.txt"), "one\n");
		mockGit(({ cmd }) => (cmd.includes("ls-files") ? { text: "new.txt\0" } : {}));
		const started = Promise.withResolvers<void>();
		let held: { thread: Worker; message: unknown } | undefined;
		const postMessage = Worker.prototype.postMessage;
		const postSpy = spyOn(Worker.prototype, "postMessage").mockImplementation(function (
			this: Worker,
			message: unknown,
		) {
			if ((message as { action: string }).action === "read" && !held) {
				held = { thread: this, message };
				started.resolve();
				return;
			}
			postMessage.call(this, message);
		});
		spies.push(postSpy);
		const first = getGitLineStats(cwd);
		await started.promise;
		const controllers = Array.from({ length: 8 }, () => new AbortController());
		const waiting = controllers.map((controller) =>
			getGitLineStats(cwd, controller.signal).catch((error: Error) => error),
		);
		await new Promise<void>((resolve) => setImmediate(resolve));
		await expect(getGitLineStats(cwd)).rejects.toThrow("queue is full");
		for (const controller of controllers) controller.abort(new Error("queued cancellation"));
		const cancelled = await Promise.all(waiting);
		expect(cancelled.every((result) => result instanceof Error)).toBe(true);
		const reads = postSpy.mock.calls.filter(
			(call) => (call[0] as { action?: string }).action === "read",
		);
		expect(reads).toHaveLength(1);
		if (!held) throw new Error("Worker did not start");
		postMessage.call(held.thread, held.message);
		expect(await first).toMatchObject({ complete: true, linesAdded: 1 });
	});

	test("running cancellation waits for worker termination before rejecting", async () => {
		const cwd = directory();
		writeFileSync(join(cwd, "new.txt"), "one\n");
		mockGit(({ cmd }) => (cmd.includes("ls-files") ? { text: "new.txt\0" } : {}));
		const controller = new AbortController();
		const postMessage = Worker.prototype.postMessage;
		const terminate = Worker.prototype.terminate;
		let terminated = false;
		spies.push(
			spyOn(Worker.prototype, "postMessage").mockImplementation(function (
				this: Worker,
				message: unknown,
			) {
				postMessage.call(this, message);
				if ((message as { action: string }).action === "read") {
					controller.abort(new Error("running cancellation"));
				}
			}),
			spyOn(Worker.prototype, "terminate").mockImplementation(async function (this: Worker) {
				const result = await terminate.call(this);
				terminated = true;
				return result;
			}),
		);
		await expect(getGitLineStats(cwd, controller.signal)).rejects.toThrow("running cancellation");
		expect(terminated).toBe(true);
	});

	test("real Git failure rejects instead of returning an empty complete summary", async () => {
		await expect(getGitLineStats(directory())).rejects.toThrow();
	});

	test("pre-abort does not spawn Git", async () => {
		const spy = spyOn(spawn, "safeSpawn");
		spies.push(spy);
		const signal = AbortSignal.abort(new Error("cancelled"));
		await expect(getGitLineStats("/unused", signal)).rejects.toThrow("cancelled");
		expect(spy).not.toHaveBeenCalled();
	});

	test("failure aborts siblings and waits for all their cleanup before rejecting", async () => {
		let cleaned = 0;
		spies.push(
			spyOn(spawn, "safeSpawn").mockImplementation(async ({ cmd, signal }) => {
				if (cmd.includes("rev-parse")) {
					return { stdout: "", stderr: "real failure", exitCode: 128 };
				}
				await new Promise<void>((resolve) => {
					signal?.addEventListener("abort", () => setTimeout(resolve, 10), { once: true });
				});
				cleaned++;
				return { stdout: "", stderr: "", exitCode: 143, terminationReason: "abort" };
			}),
		);
		await expect(getGitLineStats("/unused")).rejects.toThrow("real failure");
		expect(cleaned).toBe(3);
	});

	test("timeout termination rejects, rather than pretending to be a budget prefix", async () => {
		spies.push(
			spyOn(spawn, "safeSpawn").mockResolvedValue({
				stdout: "",
				stderr: "",
				exitCode: 143,
				terminationReason: "timeout",
			}),
		);
		await expect(getGitLineStats("/unused")).rejects.toThrow("timeout");
	});
});

describe("bounded worker file measurements and metadata cache", () => {
	test("counts empty/final-newline/no-final-newline files in chunks and skips binary", async () => {
		const cwd = directory();
		writeFileSync(join(cwd, "empty"), "");
		writeFileSync(join(cwd, "newline"), "a\nb\n");
		writeFileSync(join(cwd, "tail"), "a\nb");
		writeFileSync(join(cwd, "chunked"), `${"x".repeat(65_535)}\nlast`);
		writeFileSync(join(cwd, "binary"), Buffer.from([0, 10]));
		const result = await new UntrackedLineReader().measure(
			cwd,
			["empty", "newline", "tail", "chunked", "binary"],
			new AbortController().signal,
		);
		expect(result.complete).toBe(true);
		expect(result.files.map((file) => file.unstagedLinesAdded)).toEqual([0, 2, 2, 2, 0]);
	});

	test("cache hits do not reread, while same-size content edits invalidate the cache", async () => {
		const cwd = directory();
		const path = join(cwd, "file");
		writeFileSync(path, "a\nb\n");
		const reader = new UntrackedLineReader();
		const measure = () => reader.measure(cwd, ["file"], new AbortController().signal);
		expect((await measure()).readFiles).toBe(1);
		expect((await measure()).readFiles).toBe(0);
		writeFileSync(path, "abcd");
		utimesSync(path, new Date(), new Date(Date.now() + 2000));
		const updated = await measure();
		expect(updated.readFiles).toBe(1);
		expect(updated.files[0].unstagedLinesAdded).toBe(1);
	});

	test("oversize/missing/nonregular files are unknown, never complete zero", async () => {
		const cwd = directory();
		writeFileSync(join(cwd, "oversize"), Buffer.alloc(1_000_001, 10));
		mkdirSync(join(cwd, "directory"));
		const result = await new UntrackedLineReader().measure(
			cwd,
			["oversize", "missing", "directory"],
			new AbortController().signal,
		);
		expect(result.complete).toBe(false);
		expect(result.readFiles).toBe(0);
		for (const file of result.files) {
			expect(file.complete).toBe(false);
			expect(file.unstagedLinesAdded).toBeUndefined();
			expect(file.unstagedLinesRemoved).toBeUndefined();
		}
	});

	test("open failure is unknown and does not poison the measurement cache", async () => {
		const cwd = directory();
		writeFileSync(join(cwd, "file"), "one\n");
		const reader = new UntrackedLineReader();
		spies.push(spyOn(fs, "open").mockRejectedValueOnce(new Error("EACCES")));
		const failed = await reader.measure(cwd, ["file"], new AbortController().signal);
		expect(failed.complete).toBe(false);
		expect(failed.files[0].unstagedLinesAdded).toBeUndefined();
		const recovered = await reader.measure(cwd, ["file"], new AbortController().signal);
		expect(recovered.complete).toBe(true);
		expect(recovered.readFiles).toBe(1);
		expect(recovered.files[0].unstagedLinesAdded).toBe(1);
	});

	test.skipIf(process.platform === "win32")("symlinks are not followed", async () => {
		const cwd = directory();
		writeFileSync(join(cwd, "target"), "one\ntwo\n");
		symlinkSync("target", join(cwd, "link"));
		const result = await new UntrackedLineReader().measure(
			cwd,
			["link"],
			new AbortController().signal,
		);
		expect(result.complete).toBe(false);
		expect(result.readFiles).toBe(0);
		expect(result.files[0].unstagedLinesAdded).toBeUndefined();
	});

	test("20 MB total read budget and 200 file cap retain measured prefixes", async () => {
		const cwd = directory();
		const paths = Array.from({ length: 21 }, (_, index) => `${index}.txt`);
		for (const path of paths) writeFileSync(join(cwd, path), Buffer.alloc(1_000_000, 10));
		const reader = new UntrackedLineReader();
		const result = await reader.measure(cwd, paths, new AbortController().signal);
		expect(result.complete).toBe(false);
		expect(result.readFiles).toBe(20);
		expect(result.files[19].unstagedLinesAdded).toBe(1_000_000);
		expect(result.files[20].unstagedLinesAdded).toBeUndefined();
		const capped = await reader.measure(
			cwd,
			Array.from({ length: 201 }, () => "missing"),
			new AbortController().signal,
		);
		expect(capped.files).toHaveLength(200);
		expect(capped.complete).toBe(false);
	});

	test("cache is bounded to 512 file versions", async () => {
		const cwd = directory();
		const reader = new UntrackedLineReader();
		for (let start = 0; start < 513; start += 200) {
			const paths = Array.from({ length: Math.min(200, 513 - start) }, (_, i) => `${start + i}`);
			for (const path of paths) writeFileSync(join(cwd, path), "one\n");
			await reader.measure(cwd, paths, new AbortController().signal);
		}
		const result = await reader.measure(cwd, ["0", "512"], new AbortController().signal);
		expect(result.readFiles).toBe(1);
	});

	test("cancellation remains an error, not an incomplete zero", async () => {
		const cwd = directory();
		const controller = new AbortController();
		controller.abort(new Error("cancelled"));
		await expect(
			new UntrackedLineReader().measure(cwd, ["anything"], controller.signal),
		).rejects.toThrow("cancelled");
	});
});
