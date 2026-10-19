import { expect, test } from "bun:test";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
import {
	compiledWorkerSpecifiers,
	connectCompiledWorker,
	workerEntryFromSpecifier,
} from "./compiled-worker";

const source = "file:///project/server/services/git-line-stats.ts";
const binary = "file:///$bunfs/root/narrafork";
const windows = "file:///C:/%7EBUN/root/narrafork.exe";

test("source runs resolve the worker next to the caller", () => {
	expect(
		compiledWorkerSpecifiers(
			"services/git-untracked-line-worker.ts",
			"./git-untracked-line-worker.ts",
			source,
			false,
		),
	).toEqual(["file:///project/server/services/git-untracked-line-worker.ts"]);
});

test("compiled runs probe server-root, repo-root, and basename layouts", () => {
	expect(
		compiledWorkerSpecifiers(
			"services/git-untracked-line-worker.ts",
			"./git-untracked-line-worker.ts",
			binary,
			true,
		),
	).toEqual([
		"file:///$bunfs/root/services/git-untracked-line-worker.js",
		"file:///$bunfs/root/server/services/git-untracked-line-worker.js",
		"file:///$bunfs/root/git-untracked-line-worker.js",
	]);
	expect(
		compiledWorkerSpecifiers("lib/db-worker/worker-entry.ts", "./worker-entry.ts", binary, true),
	).toEqual([
		"file:///$bunfs/root/lib/db-worker/worker-entry.js",
		"file:///$bunfs/root/server/lib/db-worker/worker-entry.js",
		"file:///$bunfs/root/worker-entry.js",
	]);
});

test("Worker accepts URL objects on POSIX and forward-slash virtual keys on Windows", () => {
	const posix = workerEntryFromSpecifier(
		"file:///$bunfs/root/services/git-untracked-line-worker.js",
	);
	expect(posix).toBeInstanceOf(URL);
	expect(String(posix)).toBe("file:///$bunfs/root/services/git-untracked-line-worker.js");
	expect(
		compiledWorkerSpecifiers(
			"services/git-untracked-line-worker.ts",
			"./git-untracked-line-worker.ts",
			windows,
			true,
		).map((specifier) => workerEntryFromSpecifier(specifier)),
	).toEqual([
		"C:/~BUN/root/services/git-untracked-line-worker.js",
		"C:/~BUN/root/server/services/git-untracked-line-worker.js",
		"C:/~BUN/root/git-untracked-line-worker.js",
	]);
	expect(workerEntryFromSpecifier("file:///B:/~BUN/root/lib/worker.js")).toBe(
		"B:/~BUN/root/lib/worker.js",
	);
	for (const real of ["file:///C:/workspace%20name/worker.ts", "file:///workspace/worker.ts"]) {
		const entry = workerEntryFromSpecifier(real);
		expect(entry).toBeInstanceOf(URL);
		expect(String(entry)).toBe(real);
	}
	expect(workerEntryFromSpecifier("/tmp/worker.js")).toBe("/tmp/worker.js");
	expect(workerEntryFromSpecifier("./worker.ts")).toBe("./worker.ts");
});

test("only earlier candidates time out; the last one may start slowly", async () => {
	const dir = await mkdtemp(join(tmpdir(), "nf-compiled-worker-"));
	try {
		const slow = join(dir, "slow.ts");
		await writeFile(
			slow,
			`import { parentPort } from "node:worker_threads";
await Bun.sleep(300);
parentPort?.on("message", () => parentPort?.postMessage({ ready: true }));`,
		);
		const href = pathToFileURL(slow).href;
		await expect(
			connectCompiledWorker([href, pathToFileURL(join(dir, "missing.ts")).href], { timeoutMs: 50 }),
		).rejects.toThrow("worker did not become ready");
		const started = await connectCompiledWorker([href], { timeoutMs: 50 });
		expect(started.specifier).toBe(href);
		await started.worker.terminate();
	} finally {
		await rm(dir, { recursive: true, force: true });
	}
});
