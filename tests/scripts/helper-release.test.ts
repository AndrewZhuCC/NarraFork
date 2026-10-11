import { afterEach, describe, expect, test } from "bun:test";
import { createHash } from "node:crypto";
import { existsSync } from "node:fs";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { basename, join } from "node:path";
import type { GhRunner } from "../../scripts/lib/github-release";
import {
	EXECUTOR_LICENSE_FILES,
	HELPER_LICENSE_FILES,
	publishHelperRelease,
	resolveHelperTagCommit,
	validateHelperReleaseBundle,
	verifyPublishedHelperDependencies,
} from "../../scripts/lib/helper-release";
import { publishHelperReleasePlan } from "../../scripts/lib/helper-release-control";
import {
	getHelperAssetName,
	HELPER_MANIFEST_FILENAME,
	HELPER_PLATFORMS,
	HELPER_RELEASE_TAG,
	HELPER_TOOL_VERSIONS,
	HELPER_TOOLS,
} from "../../shared/helper-distribution";
import {
	EXECUTOR_MANIFEST_FILENAME,
	EXECUTOR_PLATFORMS,
	executorPublishedFilename,
} from "../../shared/remote-executor";

const repository = "ForkOwner/fork-repo";
const commit = "a".repeat(40);
const version = "0.7.2";
const digest = (bytes: string) => createHash("sha256").update(bytes).digest("hex");
const byteIdentity = (name: string, content = "binary") => ({
	name,
	size: Buffer.byteLength(content),
	sha256: digest(content),
});
const makeHelper = () => ({
	schemaVersion: 1,
	repository,
	tag: HELPER_RELEASE_TAG,
	commit,
	catalogVersion: "1.0.0",
	files: HELPER_TOOLS.flatMap((tool) =>
		HELPER_PLATFORMS.map((platform) => ({
			tool,
			platform,
			toolVersion: HELPER_TOOL_VERSIONS[tool],
			...byteIdentity(getHelperAssetName(tool, platform)),
		})),
	),
	licenses: HELPER_LICENSE_FILES.map((name) => byteIdentity(name, "license")),
});
const makeExecutor = () => ({
	schemaVersion: 1,
	repository,
	tag: `executor-v${version}`,
	commit,
	manifest: {
		version,
		protocolVersion: 1,
		releasedAt: "1970-01-01T00:00:00Z",
		platforms: Object.fromEntries(
			EXECUTOR_PLATFORMS.map((platform) => {
				const file = byteIdentity(executorPublishedFilename(version, platform));
				return [platform, { filename: file.name, size: file.size, sha256: file.sha256 }];
			}),
		),
	},
	licenses: EXECUTOR_LICENSE_FILES.map((name) => byteIdentity(name, "license")),
});
type Remote = {
	id: number;
	tag_name: string;
	draft: boolean;
	prerelease: boolean;
	published_at: string | null;
	assets: { id: number; name: string; size: number; state: string; digest: string | null }[];
};
function fixture() {
	const helper = makeHelper();
	const executor = makeExecutor();
	const raws = new Map([
		[HELPER_MANIFEST_FILENAME, `${JSON.stringify(helper)}\n`],
		[EXECUTOR_MANIFEST_FILENAME, `${JSON.stringify(executor)}\n`],
	]);
	let id = 100;
	const asset = (file: { name: string; size: number; sha256: string }) => ({
		id: id++,
		name: file.name,
		size: file.size,
		state: "uploaded",
		digest: `sha256:${file.sha256}`,
	});
	const helperRelease: Remote = {
		id: 1,
		tag_name: HELPER_RELEASE_TAG,
		draft: false,
		prerelease: false,
		published_at: "2026-10-09T00:00:00Z",
		assets: [
			...helper.files,
			...helper.licenses,
			byteIdentity(HELPER_MANIFEST_FILENAME, raws.get(HELPER_MANIFEST_FILENAME)),
		].map(asset),
	};
	const executorRelease: Remote = {
		id: 2,
		tag_name: `executor-v${version}`,
		draft: false,
		prerelease: false,
		published_at: "2026-10-09T00:00:00Z",
		assets: [
			...Object.values(executor.manifest.platforms).map((entry) => ({
				name: entry.filename,
				size: entry.size,
				sha256: entry.sha256,
			})),
			...executor.licenses,
			byteIdentity(EXECUTOR_MANIFEST_FILENAME, raws.get(EXECUTOR_MANIFEST_FILENAME)),
		].map(asset),
	};
	const calls: string[][] = [];
	const run: GhRunner = (args) => {
		calls.push(args);
		if (args[0] !== "api") throw new Error("Read-only verifier attempted mutation");
		if (args[1]?.includes("/git/ref/tags/"))
			return JSON.stringify({ object: { type: "commit", sha: commit } });
		if (args[1]?.endsWith(`/tags/${HELPER_RELEASE_TAG}`)) return JSON.stringify(helperRelease);
		if (args[1]?.endsWith(`/tags/executor-v${version}`)) return JSON.stringify(executorRelease);
		if (args[1] === `repos/${repository}/releases/1`) return JSON.stringify(helperRelease);
		if (args[1] === `repos/${repository}/releases/2`) return JSON.stringify(executorRelease);
		const all = [...helperRelease.assets, ...executorRelease.assets];
		const found = all.find((entry) => args[1]?.endsWith(`/assets/${entry.id}`));
		if (found && raws.has(found.name)) return raws.get(found.name) as string;
		throw new Error(`Unexpected gh operation: ${args.join(" ")}`);
	};
	const options = {
		repository,
		helpersTag: HELPER_RELEASE_TAG,
		executorVersion: version,
		protocolVersion: 1,
		run,
	};
	return { helper, executor, raws, helperRelease, executorRelease, options, calls, asset };
}
function draftFixture(existing = true, kind: "helpers" | "executor" = "helpers") {
	const f = fixture();
	const release = kind === "helpers" ? f.helperRelease : f.executorRelease;
	release.draft = true;
	release.published_at = null;
	if (!existing) release.assets = [];
	const state = {
		present: existing,
		calls: [] as string[][],
		restOverride: undefined as unknown,
		idOverride: undefined as Remote | undefined,
	};
	const summaries = () =>
		state.present
			? [
					{
						id: release.id,
						url: `https://api.github.com/repos/${repository}/releases/${release.id}`,
						tagName: release.tag_name,
						draft: release.draft,
						prerelease: release.prerelease,
						publishedAt: release.published_at,
						assetCount: release.assets.length,
					},
				]
			: [];
	const run: GhRunner = async (args) => {
		state.calls.push(args);
		if (args[0] === "api") {
			if (args[1] === `repos/${repository}/releases/tags/${release.tag_name}`) {
				if (!state.present || release.draft) throw new Error("gh: Not Found (HTTP 404)");
				return JSON.stringify(release);
			}
			// Model the observed visibility problem: GraphQL never sees the draft.
			if (args[1] === "graphql")
				return JSON.stringify({
					data: {
						repository: {
							nameWithOwner: repository,
							releases: { nodes: [], pageInfo: { hasNextPage: false, endCursor: null } },
						},
					},
				});
			if (args[1]?.startsWith(`repos/${repository}/releases?`))
				return JSON.stringify(state.restOverride === undefined ? summaries() : state.restOverride);
			if (args[1] === `repos/${repository}/releases/${release.id}`)
				return JSON.stringify(state.idOverride ?? release);
			return f.options.run(args);
		}
		if (args[0] !== "release") throw new Error("Unexpected mutation");
		if (args[1] === "create") {
			if (state.present) throw new Error("Duplicate create");
			state.present = true;
			return "";
		}
		if (args[1] === "upload") {
			const path = args[3] as string;
			const bytes = await readFile(path);
			release.assets.push(
				f.asset({
					name: basename(path),
					size: bytes.length,
					sha256: createHash("sha256").update(bytes).digest("hex"),
				}),
			);
			return "";
		}
		if (args[1] === "edit") {
			release.draft = false;
			release.published_at = "2026-10-09T00:00:00Z";
			return "";
		}
		throw new Error("Unexpected release operation");
	};
	return { ...f, state, run, summaries, release };
}
const dirs: string[] = [];
afterEach(async () => {
	for (const dir of dirs.splice(0)) await rm(dir, { recursive: true, force: true });
});
async function bundle(value = makeHelper()) {
	const dir = await mkdtemp(join(import.meta.dir, "../../.narrafork/helper-release-test-"));
	dirs.push(dir);
	for (const entry of value.files) await writeFile(join(dir, entry.name), "binary");
	for (const entry of value.licenses) await writeFile(join(dir, entry.name), "license");
	await writeFile(join(dir, HELPER_MANIFEST_FILENAME), `${JSON.stringify(value)}\n`);
	return dir;
}
describe("actual helper tag refs", () => {
	test("annotated tags peel through bounded same-repository git/tag objects", async () => {
		const calls: string[][] = [];
		const tagObject = "b".repeat(40);
		const run: GhRunner = (args) => {
			calls.push(args);
			if (args[1] === `repos/${repository}/git/ref/tags/${HELPER_RELEASE_TAG}`)
				return JSON.stringify({ object: { type: "tag", sha: tagObject } });
			if (args[1] === `repos/${repository}/git/tags/${tagObject}`)
				return JSON.stringify({ object: { type: "commit", sha: commit } });
			throw new Error("Unexpected API");
		};
		expect(await resolveHelperTagCommit({ repository, run }, HELPER_RELEASE_TAG)).toBe(commit);
		expect(calls.length).toBe(2);
		expect(calls.every((args) => !args[1]?.includes("/commits/"))).toBe(true);
	});
	test("a same-named branch cannot stand in for a deleted/nonexistent tag", async () => {
		const f = draftFixture(false);
		const dir = await bundle();
		const calls: string[][] = [];
		const run: GhRunner = (args) => {
			calls.push(args);
			if (args[1]?.includes("/git/ref/tags/")) throw new Error("HTTP 404");
			if (args[1]?.includes("/commits/")) return commit;
			return f.run(args);
		};
		await expect(
			publishHelperRelease({
				repository,
				kind: "helpers",
				version: "1.0.0",
				protocolVersion: 1,
				commit,
				bundleDir: dir,
				run,
			}),
		).rejects.toThrow("404");
		expect(calls.length).toBe(1);
		expect(calls[0]?.[1]).toContain("/git/ref/tags/");
		expect(f.state.calls.length).toBe(0);
	});
	for (const mode of ["cycle", "too-deep", "wrong-type", "bad-sha"])
		test(`rejects ${mode} tag objects with bounded reads`, async () => {
			let count = 0;
			const run: GhRunner = () => {
				count++;
				return JSON.stringify({
					object: {
						type: mode === "wrong-type" ? "tree" : "tag",
						sha:
							mode === "bad-sha"
								? "a"
								: mode === "cycle"
									? "b".repeat(40)
									: count.toString(16).padStart(40, "0"),
					},
				});
			};
			await expect(
				resolveHelperTagCommit({ repository, run }, HELPER_RELEASE_TAG),
			).rejects.toThrow();
			expect(count).toBeLessThanOrEqual(5);
		});
});

describe("read-only auxiliary dependency readiness", () => {
	test("checks same-repository fixed tags, exact twelve helpers and six executors", async () => {
		const f = fixture();
		await verifyPublishedHelperDependencies(f.options);
		expect(f.calls.length).toBe(6);
		expect(
			f.calls.every((args) => args[0] === "api" && args[1]?.startsWith(`repos/${repository}/`)),
		).toBe(true);
		expect(JSON.stringify(f.calls)).not.toContain("latest");
	});
	for (const [name, mutate] of [
		[
			"draft",
			(f: ReturnType<typeof fixture>) => {
				f.helperRelease.draft = true;
			},
		],
		[
			"prerelease",
			(f: ReturnType<typeof fixture>) => {
				f.executorRelease.prerelease = true;
			},
		],
		[
			"wrong tag",
			(f: ReturnType<typeof fixture>) => {
				f.helperRelease.tag_name = "helpers-v2.0.0";
			},
		],
		[
			"missing binary",
			(f: ReturnType<typeof fixture>) => {
				f.helperRelease.assets.shift();
			},
		],
		[
			"missing license",
			(f: ReturnType<typeof fixture>) => {
				f.helperRelease.assets = f.helperRelease.assets.filter(
					(asset) => asset.name !== "musl.txt",
				);
			},
		],
		[
			"unexpected asset",
			(f: ReturnType<typeof fixture>) => {
				f.helperRelease.assets.push(f.asset(byteIdentity("extra.exe")));
			},
		],
		[
			"duplicate asset",
			(f: ReturnType<typeof fixture>) => {
				f.helperRelease.assets.push(f.helperRelease.assets[0] as Remote["assets"][number]);
			},
		],
		[
			"wrong size",
			(f: ReturnType<typeof fixture>) => {
				(f.helperRelease.assets[0] as Remote["assets"][number]).size++;
			},
		],
		[
			"wrong digest",
			(f: ReturnType<typeof fixture>) => {
				(f.executorRelease.assets[0] as Remote["assets"][number]).digest =
					`sha256:${"0".repeat(64)}`;
			},
		],
		[
			"missing digest",
			(f: ReturnType<typeof fixture>) => {
				(f.helperRelease.assets[0] as Remote["assets"][number]).digest = null;
			},
		],
		[
			"unfinished upload",
			(f: ReturnType<typeof fixture>) => {
				(f.helperRelease.assets[0] as Remote["assets"][number]).state = "new";
			},
		],
		[
			"oversized manifest",
			(f: ReturnType<typeof fixture>) => {
				(f.helperRelease.assets.at(-1) as Remote["assets"][number]).size = 65537;
			},
		],
	] as const)
		test(`rejects ${name}, even with successful API responses`, async () => {
			const f = fixture();
			mutate(f);
			await expect(verifyPublishedHelperDependencies(f.options)).rejects.toThrow();
		});
	for (const [name, mutate] of [
		[
			"repository",
			(f: ReturnType<typeof fixture>) => {
				f.helper.repository = "Other/repo";
			},
		],
		[
			"commit",
			(f: ReturnType<typeof fixture>) => {
				f.helper.commit = "b".repeat(40);
			},
		],
		[
			"tool version",
			(f: ReturnType<typeof fixture>) => {
				(f.helper.files[0] as (typeof f.helper.files)[number]).toolVersion = "0.0.0" as "15.1.0";
			},
		],
		[
			"incomplete platforms",
			(f: ReturnType<typeof fixture>) => {
				f.helper.files.pop();
			},
		],
		[
			"protocol",
			(f: ReturnType<typeof fixture>) => {
				f.executor.manifest.protocolVersion = 2;
			},
		],
		[
			"executor version",
			(f: ReturnType<typeof fixture>) => {
				f.executor.manifest.version = "0.0.0";
			},
		],
	] as const)
		test(`rejects correctly rehashed manifest with wrong ${name}`, async () => {
			const f = fixture();
			mutate(f);
			for (const [manifestName, value, release] of [
				[HELPER_MANIFEST_FILENAME, f.helper, f.helperRelease],
				[EXECUTOR_MANIFEST_FILENAME, f.executor, f.executorRelease],
			] as const) {
				const raw = `${JSON.stringify(value)}\n`;
				f.raws.set(manifestName, raw);
				const remote = release.assets.find(
					(asset) => asset.name === manifestName,
				) as Remote["assets"][number];
				remote.size = Buffer.byteLength(raw);
				remote.digest = `sha256:${digest(raw)}`;
			}
			await expect(verifyPublishedHelperDependencies(f.options)).rejects.toThrow();
		});
	test("rejects moved remote tag commit", async () => {
		const f = fixture();
		const run: GhRunner = (args) =>
			args[1]?.includes("/git/ref/tags/")
				? JSON.stringify({ object: { type: "commit", sha: "b".repeat(40) } })
				: f.options.run(args);
		await expect(verifyPublishedHelperDependencies({ ...f.options, run })).rejects.toThrow();
	});
	test("cancellation stops future API operations", async () => {
		const f = fixture();
		const abort = new AbortController();
		abort.abort();
		await expect(
			verifyPublishedHelperDependencies({ ...f.options, signal: abort.signal }),
		).rejects.toThrow();
		expect(f.calls.length).toBe(0);
	});
});
describe("authenticated draft lookup", () => {
	for (const existing of [false, true])
		test(`executor ${existing ? "existing partial" : "new"} draft publishes using REST, not invisible GraphQL`, async () => {
			const f = draftFixture(existing, "executor");
			if (existing) f.executorRelease.assets.splice(0, 2);
			let postCreateReads = 0;
			const run: GhRunner = async (args) => {
				const result = await f.run(args);
				if (!existing && f.state.present && args[1]?.includes("/releases?")) {
					postCreateReads++;
					if (postCreateReads === 1) return "[]";
				}
				return result;
			};
			const dir = await mkdtemp(join(import.meta.dir, "../../.narrafork/executor-release-test-"));
			dirs.push(dir);
			for (const entry of Object.values(f.executor.manifest.platforms))
				await writeFile(join(dir, entry.filename), "binary");
			for (const entry of f.executor.licenses) await writeFile(join(dir, entry.name), "license");
			await writeFile(
				join(dir, EXECUTOR_MANIFEST_FILENAME),
				f.raws.get(EXECUTOR_MANIFEST_FILENAME) as string,
			);
			await publishHelperRelease({
				repository,
				kind: "executor",
				version,
				protocolVersion: 1,
				commit,
				bundleDir: dir,
				run,
			});
			expect(f.executorRelease.draft).toBe(false);
			expect(f.state.calls.filter((args) => args[1] === "create").length).toBe(existing ? 0 : 1);
			expect(f.state.calls.filter((args) => args[1] === "upload").length).toBe(
				existing ? 2 : 7 + EXECUTOR_LICENSE_FILES.length,
			);
			expect(f.state.calls.some((args) => args[1] === "graphql")).toBe(false);
			expect(
				f.state.calls.filter((args) => args[1]?.includes("releases?per_page=100")).length,
			).toBe(existing ? 1 : 3);
			expect(postCreateReads).toBe(existing ? 0 : 2);
			expect(
				f.state.calls.filter((args) => args[1] === `repos/${repository}/releases/2`).length,
			).toBe(2);
			expect(JSON.stringify(f.state.calls)).not.toContain("clobber");
		});
	test("a public release discovered through REST remains immutable with zero writes", async () => {
		const f = draftFixture();
		f.helperRelease.draft = false;
		f.helperRelease.published_at = "2026-10-09T00:00:00Z";
		const dir = await bundle();
		const run: GhRunner = (args) => {
			if (args[1] === `repos/${repository}/releases/tags/${HELPER_RELEASE_TAG}`)
				throw new Error("HTTP 404");
			return f.run(args);
		};
		await expect(
			publishHelperRelease({
				repository,
				kind: "helpers",
				version: "1.0.0",
				protocolVersion: 1,
				commit,
				bundleDir: dir,
				run,
			}),
		).rejects.toThrow("immutable");
		expect(f.state.calls.every((args) => args[0] === "api")).toBe(true);
	});
	test("REST absence after creation is an explicit failure and never retries create", async () => {
		const f = draftFixture(false);
		f.state.restOverride = [];
		const dir = await bundle();
		await expect(
			publishHelperRelease({
				repository,
				kind: "helpers",
				version: "1.0.0",
				protocolVersion: 1,
				commit,
				bundleDir: dir,
				run: f.run,
			}),
		).rejects.toThrow("Created helper draft not visible in authenticated REST lookup");
		expect(f.state.calls.filter((args) => args[0] === "release").map((args) => args[1])).toEqual([
			"create",
		]);
		// One pre-create listing, then exactly four bounded post-create reads.
		expect(f.state.calls.filter((args) => args[1]?.includes("/releases?")).length).toBe(5);
		expect(f.state.calls.filter((args) => args[1]?.includes("/releases/tags/")).length).toBe(5);
		expect(f.helperRelease.draft).toBe(true);
		expect(f.helperRelease.assets).toHaveLength(0);
	});
	test("a newly created draft missing on the first REST read becomes visible on a read-only retry", async () => {
		const f = draftFixture(false);
		let postCreateReads = 0;
		const run: GhRunner = async (args) => {
			const result = await f.run(args);
			if (f.state.present && args[1]?.includes("/releases?")) {
				postCreateReads++;
				if (postCreateReads === 1) return "[]";
			}
			return result;
		};
		const dir = await bundle();
		await publishHelperRelease({
			repository,
			kind: "helpers",
			version: "1.0.0",
			protocolVersion: 1,
			commit,
			bundleDir: dir,
			run,
		});
		expect(postCreateReads).toBe(2);
		expect(f.state.calls.filter((args) => args[1] === "create")).toHaveLength(1);
		expect(f.state.calls.filter((args) => args[1] === "upload")).toHaveLength(
			13 + HELPER_LICENSE_FILES.length,
		);
		expect(f.state.calls.filter((args) => args[1] === "edit")).toHaveLength(1);
		expect(f.helperRelease.draft).toBe(false);
		expect(JSON.stringify(f.state.calls)).not.toContain("clobber");
	});
	test("first complete post-create read is missing, then AbortController cancels the 250ms delay before a second read", async () => {
		const f = draftFixture(false);
		const controller = new AbortController();
		const reason = new Error("parent deadline while waiting for REST visibility");
		const phases: string[] = [];
		let postCreateReads = 0;
		let abortTimer: ReturnType<typeof setTimeout> | undefined;
		const run: GhRunner = async (args) => {
			const result = await f.run(args);
			if (args[0] === "release" && args[1] === "create") phases.push("created");
			if (f.state.present && args[1]?.includes("/releases?")) {
				postCreateReads++;
				phases.push("complete authenticated read: missing");
				abortTimer = setTimeout(() => {
					phases.push("aborted during 250ms delay");
					controller.abort(reason);
				}, 25);
				// A short, valid page proves absence, rather than a transport error.
				return "[]";
			}
			return result;
		};
		const dir = await bundle();
		try {
			await expect(
				publishHelperRelease({
					repository,
					kind: "helpers",
					version: "1.0.0",
					protocolVersion: 1,
					commit,
					bundleDir: dir,
					run,
					signal: controller.signal,
				}),
			).rejects.toMatchObject({ name: "AbortError", cause: reason });
		} finally {
			clearTimeout(abortTimer);
		}
		expect(phases).toEqual([
			"created",
			"complete authenticated read: missing",
			"aborted during 250ms delay",
		]);
		expect(controller.signal.aborted).toBe(true);
		expect(postCreateReads).toBe(1);
		// Pre-create + first post-create listing only; never a second post-create read.
		expect(f.state.calls.filter((args) => args[1]?.includes("/releases?")).length).toBe(2);
		expect(f.state.calls.filter((args) => args[1]?.includes("/releases/tags/")).length).toBe(2);
		expect(f.state.calls.at(-1)?.[1]).toBe(`repos/${repository}/releases?per_page=100&page=1`);
		expect(f.state.calls.filter((args) => args[0] === "release").map((args) => args[1])).toEqual([
			"create",
		]);
		expect(f.helperRelease.draft).toBe(true);
		expect(f.helperRelease.assets).toHaveLength(0);
	});
	for (const mode of [
		"401",
		"403",
		"network",
		"invalid-json",
		"metadata-limit",
		"invalid-state",
		"duplicate",
		"asset-mismatch",
		"public",
		"prerelease",
	])
		test(`post-create ${mode} fails immediately instead of retrying a read or create`, async () => {
			const f = draftFixture(false);
			const calls: string[][] = [];
			let postCreateReads = 0;
			const run: GhRunner = async (args) => {
				calls.push(args);
				if (f.state.present && args[1]?.includes("/releases?")) {
					postCreateReads++;
					if (mode === "401" || mode === "403") throw new Error(`HTTP ${mode}`);
					if (mode === "network") throw new Error("network failure");
					if (mode === "invalid-json") return "{";
					if (mode === "metadata-limit") return " ".repeat(128 * 1024 + 1);
					if (mode === "public") {
						f.helperRelease.draft = false;
						f.helperRelease.published_at = "2026-10-09T00:00:00Z";
					}
					if (mode === "prerelease") f.helperRelease.prerelease = true;
					const nodes = f.summaries();
					const node = nodes[0] as (typeof nodes)[number];
					if (mode === "invalid-state") node.publishedAt = "2026-10-09T00:00:00Z";
					if (mode === "duplicate") nodes.push({ ...node });
					if (mode === "asset-mismatch") node.assetCount = 1;
					return JSON.stringify(nodes);
				}
				return f.run(args);
			};
			const dir = await bundle();
			await expect(
				publishHelperRelease({
					repository,
					kind: "helpers",
					version: "1.0.0",
					protocolVersion: 1,
					commit,
					bundleDir: dir,
					run,
				}),
			).rejects.toThrow();
			expect(postCreateReads).toBe(1);
			expect(calls.filter((args) => args[0] === "release").map((args) => args[1])).toEqual([
				"create",
			]);
			expect(f.helperRelease.draft).toBe(mode !== "public");
			expect(f.helperRelease.assets).toHaveLength(0);
		});
	for (const endpoint of ["tag", "list"])
		for (const message of ["HTTP 403", "HTTP 401", "network failure", "invalid JSON"])
			test(`${endpoint} ${message} is not absence and causes zero writes`, async () => {
				const f = draftFixture(false);
				const calls: string[][] = [];
				const run: GhRunner = (args) => {
					calls.push(args);
					if (
						endpoint === "tag"
							? args[1]?.includes("/releases/tags/")
							: args[1]?.includes("/releases?")
					) {
						if (message === "invalid JSON") return "{";
						throw new Error(message);
					}
					return f.run(args);
				};
				const dir = await bundle();
				await expect(
					publishHelperRelease({
						repository,
						kind: "helpers",
						version: "1.0.0",
						protocolVersion: 1,
						commit,
						bundleDir: dir,
						run,
					}),
				).rejects.toThrow();
				expect(calls.every((args) => args[0] === "api")).toBe(true);
				if (endpoint === "tag")
					expect(calls.some((args) => args[1]?.includes("/releases?"))).toBe(false);
			});
	for (const mode of ["complete", "duplicate-tag", "duplicate-id", "403", "full-limit"])
		test(`a first-page draft requires complete REST pagination: ${mode}`, async () => {
			const f = draftFixture();
			const calls: string[][] = [];
			let pages = 0;
			const run: GhRunner = (args) => {
				calls.push(args);
				if (!args[1]?.includes("/releases?")) return f.run(args);
				pages++;
				const node = f.summaries()[0] as ReturnType<typeof f.summaries>[number];
				if (pages > 1 && mode !== "full-limit") {
					if (mode === "403") throw new Error("HTTP 403");
					if (mode === "duplicate-id") return JSON.stringify([{ ...node, tagName: "other" }]);
					if (mode === "duplicate-tag")
						return JSON.stringify([
							{ ...node, id: 2, url: `https://api.github.com/repos/${repository}/releases/2` },
						]);
					return "[]";
				}
				const nodes = Array.from({ length: 100 }, (_, index) => {
					const id = pages * 100 + index;
					return {
						...node,
						id,
						tagName: `other-${id}`,
						url: `https://api.github.com/repos/${repository}/releases/${id}`,
					};
				});
				if (pages === 1) nodes[0] = node;
				return JSON.stringify(nodes);
			};
			const dir = await bundle();
			const task = publishHelperRelease({
				repository,
				kind: "helpers",
				version: "1.0.0",
				protocolVersion: 1,
				commit,
				bundleDir: dir,
				run,
			});
			if (mode === "complete") {
				await task;
				expect(f.helperRelease.draft).toBe(false);
				expect(calls.filter((args) => args[0] === "release").map((args) => args[1])).toEqual([
					"edit",
				]);
			} else {
				await expect(task).rejects.toThrow();
				expect(calls.every((args) => args[0] === "api")).toBe(true);
				expect(f.helperRelease.draft).toBe(true);
			}
			expect(pages).toBe(mode === "full-limit" ? 10 : 2);
		});
	test("parent cancellation during REST enumeration stops all subsequent API and writes", async () => {
		const f = draftFixture(false);
		const controller = new AbortController();
		const calls: string[][] = [];
		const run: GhRunner = (args) => {
			calls.push(args);
			if (args[1]?.includes("/releases?")) controller.abort(new Error("parent deadline"));
			return f.run(args);
		};
		const dir = await bundle();
		await expect(
			publishHelperRelease({
				repository,
				kind: "helpers",
				version: "1.0.0",
				protocolVersion: 1,
				commit,
				bundleDir: dir,
				run,
				signal: controller.signal,
			}),
		).rejects.toThrow("parent deadline");
		expect(calls.at(-1)?.[1]).toContain("/releases?");
		expect(calls.every((args) => args[0] === "api")).toBe(true);
	});
	for (const field of ["id", "tag", "draft", "prerelease", "published", "assets", "metadata-limit"])
		test(`REST summary/ID ${field} mismatch fails before any write`, async () => {
			const f = draftFixture();
			f.state.idOverride = { ...f.helperRelease };
			if (field === "id") f.state.idOverride.id = 2;
			if (field === "tag") f.state.idOverride.tag_name = "other";
			if (field === "draft") f.state.idOverride.draft = false;
			if (field === "prerelease") f.state.idOverride.prerelease = true;
			if (field === "published") f.state.idOverride.published_at = "2026-10-09T00:00:00Z";
			if (field === "assets")
				f.state.idOverride.assets = Array.from(
					{ length: 46 },
					() => f.helperRelease.assets[0] as Remote["assets"][number],
				);
			const run: GhRunner = (args) =>
				field === "metadata-limit" && args[1] === `repos/${repository}/releases/1`
					? " ".repeat(128 * 1024 + 1)
					: f.run(args);
			const dir = await bundle();
			await expect(
				publishHelperRelease({
					repository,
					kind: "helpers",
					version: "1.0.0",
					protocolVersion: 1,
					commit,
					bundleDir: dir,
					run,
				}),
			).rejects.toThrow();
			expect(f.state.calls.every((args) => args[0] === "api")).toBe(true);
		});
	for (const field of ["id", "tag", "draft", "prerelease", "published", "digest", "missing-asset"])
		test(`known-ID ${field} change after upload cannot publish`, async () => {
			const f = draftFixture();
			f.helperRelease.assets.splice(0, 1);
			let idReads = 0;
			const run: GhRunner = (args) => {
				if (args[1] === `repos/${repository}/releases/1` && ++idReads === 2) {
					const value = {
						...f.helperRelease,
						assets: f.helperRelease.assets.map((asset) => ({ ...asset })),
					};
					if (field === "id") value.id = 2;
					if (field === "tag") value.tag_name = "other";
					if (field === "draft") value.draft = false;
					if (field === "prerelease") value.prerelease = true;
					if (field === "published") value.published_at = "2026-10-09T00:00:00Z";
					if (field === "digest")
						(value.assets[0] as Remote["assets"][number]).digest = `sha256:${"0".repeat(64)}`;
					if (field === "missing-asset") value.assets.pop();
					return JSON.stringify(value);
				}
				return f.run(args);
			};
			const dir = await bundle();
			await expect(
				publishHelperRelease({
					repository,
					kind: "helpers",
					version: "1.0.0",
					protocolVersion: 1,
					commit,
					bundleDir: dir,
					run,
				}),
			).rejects.toThrow();
			expect(f.state.calls.filter((args) => args[0] === "release").map((args) => args[1])).toEqual([
				"upload",
			]);
			expect(f.helperRelease.draft).toBe(true);
		});
	test("new create survives the tag endpoint's real draft 404 and reads the draft by ID", async () => {
		const f = draftFixture(false);
		const dir = await bundle();
		await publishHelperRelease({
			repository,
			kind: "helpers",
			version: "1.0.0",
			protocolVersion: 1,
			commit,
			bundleDir: dir,
			run: f.run,
		});
		expect(
			f.state.calls.filter((args) => args[0] === "release" && args[1] === "create").length,
		).toBe(1);
		expect(f.state.calls.some((args) => args[1] === "graphql")).toBe(false);
		expect(f.state.calls.filter((args) => args[1]?.includes("releases?per_page=100")).length).toBe(
			2,
		);
		expect(f.state.calls.some((args) => args[1] === `repos/${repository}/releases/1`)).toBe(true);
		expect(f.state.calls.filter((args) => args[1] === "upload").length).toBe(
			13 + HELPER_LICENSE_FILES.length,
		);
		expect(f.state.calls.find((args) => args[1] === "create")).toContain("--verify-tag");
		expect(f.state.calls.find((args) => args[1] === "edit")).toContain("--verify-tag");
		expect(f.helperRelease.draft).toBe(false);
	});
	test("existing draft uses authenticated summaries without re-creating or clobbering assets", async () => {
		const f = draftFixture();
		f.helperRelease.assets.splice(0, 2);
		const dir = await bundle();
		await publishHelperRelease({
			repository,
			kind: "helpers",
			version: "1.0.0",
			protocolVersion: 1,
			commit,
			bundleDir: dir,
			run: f.run,
		});
		expect(f.state.calls.some((args) => args[1] === "create")).toBe(false);
		expect(f.state.calls.filter((args) => args[1] === "upload").length).toBe(2);
		expect(JSON.stringify(f.state.calls)).not.toContain("clobber");
	});
	test("ready checker rejects a draft even though authenticated summaries could see it", async () => {
		const f = draftFixture();
		await expect(verifyPublishedHelperDependencies({ ...f.options, run: f.run })).rejects.toThrow();
		expect(f.state.calls.every((args) => args[0] === "api")).toBe(true);
	});
	for (const mode of [
		"malformed",
		"errors",
		"foreign",
		"duplicate-id",
		"duplicate-tag",
		"state",
		"prerelease",
		"too-many-assets",
	])
		test(`invalid ${mode} summary cannot prove absence or create a release`, async () => {
			const f = draftFixture();
			const value = f.summaries();
			const node = value[0] as (typeof value)[number];
			if (mode === "malformed") f.state.restOverride = null;
			if (mode === "errors") f.state.restOverride = { message: "denied" };
			if (mode === "foreign") node.url = "https://api.github.com/repos/Other/repo/releases/1";
			if (mode === "duplicate-id") value.push({ ...node, tagName: "other" });
			if (mode === "duplicate-tag")
				value.push({
					...node,
					id: 2,
					url: `https://api.github.com/repos/${repository}/releases/2`,
				});
			if (mode === "state") node.publishedAt = "2026-10-09T00:00:00Z";
			if (mode === "prerelease") {
				node.prerelease = true;
				f.helperRelease.prerelease = true;
			}
			if (mode === "too-many-assets") node.assetCount = 46;
			if (mode !== "malformed" && mode !== "errors") f.state.restOverride = value;
			const dir = await bundle();
			await expect(
				publishHelperRelease({
					repository,
					kind: "helpers",
					version: "1.0.0",
					protocolVersion: 1,
					commit,
					bundleDir: dir,
					run: f.run,
				}),
			).rejects.toThrow();
			expect(f.state.calls.every((args) => args[0] === "api")).toBe(true);
		});
	test("summary count disagreement rejects truncated draft assets without creating/uploading", async () => {
		const f = draftFixture();
		f.state.idOverride = { ...f.helperRelease, assets: f.helperRelease.assets.slice(1) };
		const dir = await bundle();
		await expect(
			publishHelperRelease({
				repository,
				kind: "helpers",
				version: "1.0.0",
				protocolVersion: 1,
				commit,
				bundleDir: dir,
				run: f.run,
			}),
		).rejects.toThrow("completeness mismatch");
		expect(f.state.calls.every((args) => args[0] === "api")).toBe(true);
	});
	for (const moved of [false, true])
		test(`a tag ${moved ? "moved" : "deleted"} after asset verification prevents publishing and never recreates it`, async () => {
			const f = draftFixture();
			const dir = await bundle();
			let idReads = 0;
			let changed = false;
			const run: GhRunner = (args) => {
				if (args[1] === `repos/${repository}/releases/1` && ++idReads === 2) changed = true;
				if (changed && args[1]?.includes("/git/ref/tags/")) {
					if (!moved) throw new Error("HTTP 404");
					return JSON.stringify({ object: { type: "commit", sha: "b".repeat(40) } });
				}
				return f.run(args);
			};
			await expect(
				publishHelperRelease({
					repository,
					kind: "helpers",
					version: "1.0.0",
					protocolVersion: 1,
					commit,
					bundleDir: dir,
					run,
				}),
			).rejects.toThrow();
			expect(f.helperRelease.draft).toBe(true);
			expect(f.state.calls.every((args) => args[0] === "api")).toBe(true);
		});
	test("actual reviewer removal after final asset verification is re-read before publishing the draft", async () => {
		const f = draftFixture();
		const dir = await bundle();
		let idReads = 0;
		let reviewersPresent = true;
		const environment = {
			GITHUB_REPOSITORY: repository,
			GITHUB_EVENT_NAME: "workflow_dispatch",
			GITHUB_REF: "refs/heads/main",
			GITHUB_SHA: commit,
		};
		const calls: string[][] = [];
		const run: GhRunner = async (args) => {
			calls.push(args);
			if (args[1] === `repos/${repository}`)
				return JSON.stringify({ full_name: repository, default_branch: "main" });
			if (args[1] === `repos/${repository}/environments/release`)
				return JSON.stringify({
					name: "release",
					protection_rules: [
						{
							type: "required_reviewers",
							reviewers: reviewersPresent ? [{ type: "User", reviewer: { id: 1 } }] : [],
						},
					],
					deployment_branch_policy: { protected_branches: false, custom_branch_policies: true },
				});
			if (
				args[1]?.startsWith(`repos/${repository}/environments/release/deployment-branch-policies?`)
			)
				return JSON.stringify({
					total_count: 1,
					branch_policies: [{ name: "main", type: "branch" }],
				});
			if (args[1]?.includes("/git/ref/heads/"))
				return JSON.stringify({ object: { type: "commit", sha: "c".repeat(40) } });
			if (args[1]?.includes("/compare/"))
				return JSON.stringify({ status: "ahead", merge_base_commit: { sha: commit } });
			if (args[1] === `repos/${repository}/releases/1` && ++idReads === 2) reviewersPresent = false;
			return f.run(args);
		};
		await expect(
			publishHelperReleasePlan(
				{
					schemaVersion: 1,
					repository,
					defaultBranch: "main",
					tag: HELPER_RELEASE_TAG,
					commit,
					controlCommit: commit,
					kind: "helpers",
					version: "1.0.0",
					protocolVersion: 1,
					sourceRunId: "",
					publish: true,
				},
				dir,
				false,
				{ run, environment },
			),
		).rejects.toThrow("required reviewers");
		expect(f.helperRelease.draft).toBe(true);
		expect(calls.every((args) => args[0] === "api")).toBe(true);
		expect(
			calls.filter((args) => args[1] === `repos/${repository}/environments/release`).length,
		).toBe(2);
	});
	test("failed protected-environment recheck immediately before edit leaves the draft unpublished", async () => {
		const f = draftFixture();
		const dir = await bundle();
		await expect(
			publishHelperRelease({
				repository,
				kind: "helpers",
				version: "1.0.0",
				protocolVersion: 1,
				commit,
				bundleDir: dir,
				run: f.run,
				beforeWrite: async () => {
					throw new Error("reviewers removed");
				},
			}),
		).rejects.toThrow("reviewers removed");
		expect(f.helperRelease.draft).toBe(true);
		expect(f.state.calls.every((args) => args[0] === "api")).toBe(true);
	});
});

describe("immutable auxiliary publisher", () => {
	test("release-executor --target=github --dry-run consumes a bundle without server config, build or network", async () => {
		const dir = await mkdtemp(join(import.meta.dir, "../../.narrafork/helper-cli-test-"));
		dirs.push(dir);
		const outer = makeExecutor();
		const output = join(dir, "bundle");
		const bin = join(dir, "bin");
		await mkdir(output);
		await mkdir(bin);
		for (const file of Object.values(outer.manifest.platforms))
			await writeFile(join(output, file.filename), "binary");
		for (const file of outer.licenses) await writeFile(join(output, file.name), "license");
		await writeFile(join(output, EXECUTOR_MANIFEST_FILENAME), `${JSON.stringify(outer)}\n`);
		const planPath = join(dir, "plan.json");
		await writeFile(
			planPath,
			JSON.stringify({
				schemaVersion: 1,
				repository,
				defaultBranch: "main",
				tag: outer.tag,
				commit,
				controlCommit: commit,
				kind: "executor",
				version,
				protocolVersion: 1,
				sourceRunId: "",
				publish: false,
			}),
		);
		const marker = join(dir, "remote-called");
		for (const command of ["gh", "go", "curl"])
			await writeFile(join(bin, command), `#!/bin/sh\nprintf called > '${marker}'\nexit 1\n`, {
				mode: 0o755,
			});
		const result = Bun.spawnSync(
			[
				process.execPath,
				"scripts/release-executor.ts",
				version,
				"--target=github",
				"--dry-run",
				`--plan=${planPath}`,
				`--output=${output}`,
			],
			{
				cwd: join(import.meta.dir, "../.."),
				env: {
					...process.env,
					PATH: `${bin}:${process.env.PATH}`,
					GITHUB_REPOSITORY: repository,
					NF_UPDATE_SERVER: "http://127.0.0.1:1",
					NF_UPDATE_TOKEN: "fixture-only",
				},
				timeout: 5000,
				maxBuffer: 65536,
			},
		);
		expect(result.exitCode).toBe(0);
		expect(existsSync(marker)).toBe(false);
		// Give the typo case an otherwise-valid approved publisher context: ignoring
		// this flag would reach gh and create the marker, so rejection is observable.
		const typo = Bun.spawnSync(
			[
				process.execPath,
				"scripts/release-helpers.ts",
				"publish",
				"--dry-rnu",
				`--plan=${planPath}`,
				`--output=${output}`,
			],
			{
				cwd: join(import.meta.dir, "../.."),
				env: {
					...process.env,
					PATH: `${bin}:${process.env.PATH}`,
					GITHUB_REPOSITORY: repository,
					GITHUB_EVENT_NAME: "workflow_dispatch",
					GITHUB_REF: "refs/heads/main",
					GITHUB_SHA: commit,
					GITHUB_RUN_ID: "456",
				},
				timeout: 5000,
				maxBuffer: 65536,
			},
		);
		expect(typo.exitCode).not.toBe(0);
		expect(typo.stderr.toString()).toContain("Unknown option for publish: --dry-rnu");
		expect(existsSync(marker)).toBe(false);
		const frozenSourcePlan = JSON.parse(await readFile(planPath, "utf8"));
		frozenSourcePlan.sourceRunId = "123";
		await writeFile(planPath, JSON.stringify(frozenSourcePlan));
		const swapped = Bun.spawnSync(
			[
				process.execPath,
				"scripts/release-helpers.ts",
				"restore",
				"--source-run-id=456",
				`--plan=${planPath}`,
				`--output=${output}`,
			],
			{
				cwd: join(import.meta.dir, "../.."),
				env: {
					...process.env,
					PATH: `${bin}:${process.env.PATH}`,
					GITHUB_REPOSITORY: repository,
					GITHUB_EVENT_NAME: "workflow_dispatch",
					GITHUB_REF: "refs/heads/main",
					GITHUB_SHA: commit,
					GITHUB_RUN_ID: "456",
				},
				timeout: 5000,
				maxBuffer: 65536,
			},
		);
		expect(swapped.exitCode).not.toBe(0);
		expect(swapped.stderr.toString()).toContain("frozen plan provenance");
		expect(existsSync(marker)).toBe(false);
	});
	test("preview validates bundle and does zero network operations", async () => {
		const f = fixture();
		const dir = await bundle();
		await publishHelperRelease({
			repository,
			kind: "helpers",
			version: "1.0.0",
			protocolVersion: 1,
			commit,
			bundleDir: dir,
			dryRun: true,
			run: f.options.run,
		});
		expect(f.calls.length).toBe(0);
	});
	test("corrupted local binary is rejected before any network call", async () => {
		const f = fixture();
		const dir = await bundle();
		await writeFile(join(dir, "rg-linux-x64"), "damage");
		await expect(
			publishHelperRelease({
				repository,
				kind: "helpers",
				version: "1.0.0",
				protocolVersion: 1,
				commit,
				bundleDir: dir,
				run: f.options.run,
			}),
		).rejects.toThrow("Local helper size/SHA-256 mismatch");
		expect(f.calls.length).toBe(0);
	});
	test("unknown local bundle entries are rejected", async () => {
		const dir = await bundle();
		await writeFile(join(dir, "extra"), "wrong");
		await expect(
			validateHelperReleaseBundle({
				repository,
				kind: "helpers",
				version: "1.0.0",
				protocolVersion: 1,
				commit,
				bundleDir: dir,
			}),
		).rejects.toThrow("not exact");
	});
	test("public helper release is immutable, with no upload/edit calls", async () => {
		const f = fixture();
		const dir = await bundle();
		await expect(
			publishHelperRelease({
				repository,
				kind: "helpers",
				version: "1.0.0",
				protocolVersion: 1,
				commit,
				bundleDir: dir,
				run: f.options.run,
			}),
		).rejects.toThrow("immutable");
		expect(f.calls.every((args) => args[0] === "api")).toBe(true);
	});
	test("resumes only missing draft assets, no overwrite/publication before complete hashes", async () => {
		const f = fixture();
		const dir = await bundle();
		f.helperRelease.draft = true;
		f.helperRelease.assets.splice(0, 2);
		const writes: string[][] = [];
		const run: GhRunner = async (args) => {
			if (args[0] === "api") return f.options.run(args);
			writes.push(args);
			if (args[1] === "upload") {
				const path = args[3] as string;
				const raw = await readFile(path);
				const name = path.split("/").at(-1) as string;
				f.helperRelease.assets.push(
					f.asset({
						name,
						size: raw.length,
						sha256: createHash("sha256").update(raw).digest("hex"),
					}),
				);
				return "";
			}
			if (args[1] === "edit") {
				f.helperRelease.draft = false;
				return "";
			}
			throw new Error("Unexpected write");
		};
		await publishHelperRelease({
			repository,
			kind: "helpers",
			version: "1.0.0",
			protocolVersion: 1,
			commit,
			bundleDir: dir,
			run,
		});
		expect(writes.filter((args) => args[1] === "upload").length).toBe(2);
		expect(writes.at(-1)?.[1]).toBe("edit");
		expect(JSON.stringify(writes)).not.toContain("clobber");
		expect(writes.at(-1)).toContain("--latest=false");
	});
	test("a conflicting draft asset is not overwritten", async () => {
		const f = fixture();
		const dir = await bundle();
		f.helperRelease.draft = true;
		(f.helperRelease.assets[0] as Remote["assets"][number]).digest = `sha256:${"0".repeat(64)}`;
		await expect(
			publishHelperRelease({
				repository,
				kind: "helpers",
				version: "1.0.0",
				protocolVersion: 1,
				commit,
				bundleDir: dir,
				run: f.options.run,
			}),
		).rejects.toThrow("mismatch");
		expect(f.calls.every((args) => args[0] === "api")).toBe(true);
	});
});
