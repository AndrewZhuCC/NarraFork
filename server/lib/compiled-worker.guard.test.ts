import { expect, test } from "bun:test";
import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { COMPILED_WORKER_ENTRIES } from "../../shared/compiled-worker-entries";

const root = join(import.meta.dir, "../..");

function serverSources(): string[] {
	const files: string[] = [];
	for (const path of new Bun.Glob("**/*.ts").scanSync({ cwd: join(root, "server") })) {
		if (path.includes(".test.") || path.includes("__tests__/")) continue;
		files.push(path);
	}
	return files;
}

test("production compile spreads the worker registry and does not hard-code worker paths", () => {
	const source = readFileSync(join(root, "scripts/build-cross-platform.ts"), "utf8");
	const command = source.match(/const compile = await runBuildStep\(\s*\[([\s\S]*?)\]\s*,?\s*\)/);
	expect(command).not.toBeNull();
	const body = command?.[1].replace(/\/\*[\s\S]*?\*\/|\/\/[^\r\n]*/g, "") ?? "";
	expect(body).toContain("...compiledWorkerBuildEntrypoints()");
	const typeScriptEntries = [...body.matchAll(/"([^"\r\n]+\.ts)"/g)].map((match) => match[1]);
	expect(typeScriptEntries).toEqual(["./server/index.ts"]);
	for (const entry of COMPILED_WORKER_ENTRIES) {
		expect(existsSync(join(root, "server", entry))).toBe(true);
	}
});

test("every compiled worker is spawned through the shared resolver, and every spawn is registered", () => {
	const referenced = new Set<string>();
	const offenders: string[] = [];
	for (const path of serverSources()) {
		const text = readFileSync(join(root, "server", path), "utf8");
		for (const match of text.matchAll(/compiledWorkerSpecifiers\([\s\S]{0,320}/g)) {
			for (const literal of match[0].matchAll(/"((?:lib|services)\/[^"]+\.ts)"/g)) {
				referenced.add(literal[1]);
			}
		}
		if (!text.includes("new Worker(")) continue;
		const inline =
			text.includes("eval: true") ||
			text.includes("createObjectURL") ||
			path === "lib/compiled-worker.ts";
		if (inline) continue;
		// node:worker_threads rejects file: strings and Windows virtual URLs.
		// The global Worker accepts file: hrefs, including $bunfs.
		const nodeThreads = text.includes('from "node:worker_threads"');
		const routed = nodeThreads
			? text.includes("workerEntryFromSpecifier") || text.includes("connectCompiledWorker")
			: text.includes("compiledWorkerSpecifiers");
		if (!routed) offenders.push(path);
	}
	expect(offenders).toEqual([]);
	expect([...referenced].sort()).toEqual([...COMPILED_WORKER_ENTRIES].sort());
	const windowsUnwrap = serverSources().filter((path) =>
		/%7eBUN/.test(readFileSync(join(root, "server", path), "utf8")),
	);
	expect(windowsUnwrap).toEqual(["lib/compiled-worker.ts"]);
});
