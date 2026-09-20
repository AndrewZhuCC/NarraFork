import assert from "node:assert/strict";
import { mkdir, mkdtemp, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { ExecutionBackend, GlobMatches } from "../lib/agent/execution/backend";
import { localBackend } from "../lib/agent/execution/local-backend";
import { posixPathSemantics } from "../lib/agent/execution/path-semantics";
import {
	clearGitWorkspaceDiscoveryCache,
	discoverGitWorkspacePaths,
} from "./git-workspace-scanner";

clearGitWorkspaceDiscoveryCache();
let calls = 0;
const matches = Object.assign(
	["z/two/.git", "one/.git", "a/b/three/.git", "a/two/.git", "a/b/c/four/.git", "../outside/.git"],
	{
		truncated: true,
	},
) as GlobMatches;
const backend = {
	deviceId: "self-check",
	kind: "local",
	runtimeGeneration: 7,
	paths: posixPathSemantics,
	glob: async (pattern: string, options: Record<string, unknown>) => {
		calls++;
		assert.equal(pattern, "*/.git");
		assert.equal(options.maxResults, 32);
		assert.equal(options.maxBytes, 64 * 1024 - 2);
		assert(Number(options.timeoutMs) > 0 && Number(options.timeoutMs) <= 3000);
		return matches;
	},
} as unknown as ExecutionBackend;

const first = await discoverGitWorkspacePaths(backend, "/work");
assert.deepEqual(first.paths, ["/work/one", "/work/a/two", "/work/z/two", "/work/a/b/three"]);
assert.equal(first.truncated, true);
assert.equal(first.unsupported, false);
assert.equal(calls, 1);

const cached = await discoverGitWorkspacePaths(backend, "/work");
assert.deepEqual(cached, first);
assert.equal(calls, 1);

const root = await mkdtemp(join(tmpdir(), "narrafork-git-workspace-"));
try {
	await Promise.all([
		mkdir(join(root, ".hidden", "repo", ".git"), { recursive: true }),
		mkdir(join(root, "real", "repo", ".git"), { recursive: true }),
		mkdir(join(root, "a", "b", "c", "four", ".git"), { recursive: true }),
		mkdir(join(root, "a", "b", "three", ".git"), { recursive: true }),
		mkdir(join(root, "file-repo"), { recursive: true }),
	]);
	await writeFile(join(root, "file-repo", ".git"), "gitdir: ../metadata\n");
	await symlink(join(root, "real"), join(root, "alias"), "dir");
	const local = await discoverGitWorkspacePaths(localBackend, root);
	assert(local.paths.includes(join(root, ".hidden", "repo")), JSON.stringify(local.paths));
	assert(local.paths.includes(join(root, "file-repo")));
	assert(local.paths.includes(join(root, "real", "repo")));
	assert(local.paths.includes(join(root, "a", "b", "three")));
	assert(!local.paths.includes(join(root, "alias", "repo")));
	assert(!local.paths.includes(join(root, "a", "b", "c", "four")));
} finally {
	await rm(root, { recursive: true, force: true });
}

console.log("git workspace scanner self-check passed");
