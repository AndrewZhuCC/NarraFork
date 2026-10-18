import { expect, test } from "bun:test";
import { compiledWorkerSpecifiers, workerEntryFromSpecifier } from "./compiled-worker";

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
	expect(workerEntryFromSpecifier("/tmp/worker.js")).toBe("/tmp/worker.js");
	expect(workerEntryFromSpecifier("./worker.ts")).toBe("./worker.ts");
});
