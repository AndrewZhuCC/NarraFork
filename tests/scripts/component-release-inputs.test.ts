import { describe, expect, test } from "bun:test";
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { mkdtempSync, readdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { basename, join } from "node:path";
import {
	computeBinaryMetadataFromBuffer,
	formatChecksumsReport,
	formatMetadataJson,
	formatSha256Sums,
} from "../../scripts/lib/binary-metadata";
import {
	collectComponentReleaseEvidence,
	compareExecutorRecipeIdentity,
	readTargetComponentDependencies,
	verifyComponentReleaseEvidence,
} from "../../scripts/lib/component-release-inputs";
import { type GhRunner, publishGitHubRelease } from "../../scripts/lib/github-release";
import { EXECUTOR_LICENSE_FILES, HELPER_LICENSE_FILES } from "../../scripts/lib/helper-release";
import {
	createHelperDistributionDependencies,
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
const targetCommit = "b".repeat(40);
const componentCommit = "96a089cfdb5a05edc9b7aff5b170f5736313ff6e";
const dependencies = createHelperDistributionDependencies("0.9.1", 1);
const digest = (text: string) => createHash("sha256").update(text).digest("hex");
const identity = (name: string, text = "binary") => ({
	name,
	size: Buffer.byteLength(text),
	sha256: digest(text),
});
const blobSha = (text: string | Buffer) =>
	createHash("sha1")
		.update(`blob ${Buffer.byteLength(text)}\0`)
		.update(text)
		.digest("hex");
type SourceFiles = Map<string, string | Buffer>;
const baseFiles = (): SourceFiles =>
	new Map([
		[
			"shared/component-versions.json",
			JSON.stringify({ executor: { version: "0.9.1", protocolVersion: 1 } }),
		],
		["package.json", '{"version":"0.9.2"}'],
		["server/lib/agent/execution/rpc-types.ts", "export const DEVICE_PROTOCOL_VERSION = 1;\n"],
		["remote-executor/go.mod", "module github.com/narrafork/remote-executor\n\ngo 1.24.0\n"],
		["remote-executor/go.sum", "dependency v1.0.0 h1:hash\n"],
		[
			"remote-executor/cmd/narrafork-executor/main.go",
			'package main\nimport "github.com/narrafork/remote-executor/internal/rpc"\nfunc main(){println(rpc.ProtocolVersion)}\n',
		],
		["remote-executor/internal/rpc/protocol.go", "package rpc\nconst ProtocolVersion = 1\n"],
		["remote-executor/internal/testgit/helpers.go", "package testgit\n// test-only helper\n"],
		["remote-executor/cmd/narrafork-executor/main_test.go", "package main\n// tests\n"],
		["remote-executor/README.md", "Documentation"],
		["scripts/lib/helper-build.ts", readFileSync("scripts/lib/helper-build.ts", "utf8")],
		["shared/remote-executor.ts", readFileSync("shared/remote-executor.ts", "utf8")],
		[
			".github/workflows/helpers-release.yml",
			readFileSync(".github/workflows/helpers-release.yml", "utf8"),
		],
	]);
function fixture(
	change: (files: SourceFiles) => void = () => {},
	oldChange: (files: SourceFiles) => void = () => {},
) {
	const target = baseFiles();
	const baseline = baseFiles();
	baseline.delete("shared/component-versions.json"); // published 0.9.1 needs no envelope fingerprint
	change(target);
	oldChange(baseline);
	const trees = new Map<string, unknown>();
	const blobs: SourceFiles = new Map();
	const store = (files: SourceFiles, commit: string) => {
		const visit = (prefix: string): string => {
			const entries: Record<string, unknown>[] = [];
			const names = [
				...new Set(
					[...files.keys()]
						.filter((path) => path.startsWith(prefix))
						.map((path) => path.slice(prefix.length).split("/")[0] as string),
				),
			].sort();
			for (const name of names) {
				const path = prefix + name;
				const text = files.get(path);
				if (text !== undefined) {
					const sha = blobSha(text);
					blobs.set(sha, text);
					entries.push({
						path: name,
						mode: "100644",
						type: "blob",
						sha,
						size: Buffer.byteLength(text),
					});
				} else entries.push({ path: name, mode: "040000", type: "tree", sha: visit(`${path}/`) });
			}
			const sha = createHash("sha1").update(JSON.stringify(entries)).digest("hex");
			trees.set(sha, { truncated: false, tree: entries });
			return sha;
		};
		trees.set(commit, trees.get(visit("")));
	};
	store(target, targetCommit);
	store(baseline, componentCommit);
	const helper = {
		schemaVersion: 1,
		repository,
		tag: HELPER_RELEASE_TAG,
		commit: componentCommit,
		catalogVersion: "1.0.0",
		files: HELPER_TOOLS.flatMap((tool) =>
			HELPER_PLATFORMS.map((platform) => ({
				tool,
				platform,
				toolVersion: HELPER_TOOL_VERSIONS[tool],
				...identity(getHelperAssetName(tool, platform)),
			})),
		),
		licenses: HELPER_LICENSE_FILES.map((name) => identity(name, "license")),
	};
	const executor = {
		schemaVersion: 1,
		repository,
		tag: dependencies.executor.tag,
		commit: componentCommit,
		manifest: {
			version: "0.9.1",
			protocolVersion: 1,
			releasedAt: "2026-10-09T00:00:00Z",
			platforms: Object.fromEntries(
				EXECUTOR_PLATFORMS.map((platform) => {
					const file = identity(executorPublishedFilename("0.9.1", platform));
					return [platform, { filename: file.name, size: file.size, sha256: file.sha256 }];
				}),
			),
		},
		licenses: EXECUTOR_LICENSE_FILES.map((name) => identity(name, "license")),
	};
	let id = 100;
	const manifests = new Map<number, string>();
	const release = (
		releaseId: number,
		tag: string,
		manifestName: string,
		manifest: unknown,
		files: ReturnType<typeof identity>[],
	) => {
		const raw = JSON.stringify(manifest);
		const manifestId = id;
		manifests.set(manifestId, raw);
		return {
			id: releaseId,
			tag_name: tag,
			draft: false,
			prerelease: false,
			published_at: "2026-10-09T00:00:00Z",
			assets: [identity(manifestName, raw), ...files].map((file) => ({
				id: id++,
				name: file.name,
				size: file.size,
				state: "uploaded",
				digest: `sha256:${file.sha256}`,
			})),
		};
	};
	const helperRelease = release(1, helper.tag, HELPER_MANIFEST_FILENAME, helper, [
		...helper.files,
		...helper.licenses,
	]);
	const executorRelease = release(2, executor.tag, EXECUTOR_MANIFEST_FILENAME, executor, [
		...Object.values(executor.manifest.platforms).map((file) => ({
			name: file.filename,
			size: file.size,
			sha256: file.sha256,
		})),
		...executor.licenses,
	]);
	const calls: string[][] = [];
	let executorTagReads = 0;
	let moveTag = false;
	const run: GhRunner = (args) => {
		calls.push(args);
		if (args[0] !== "api" || args.includes("--method"))
			throw new Error("Read-only source gate attempted mutation");
		const endpoint = args[1] as string;
		if (!endpoint.startsWith(`repos/${repository}/`)) throw new Error("Cross-repository fallback");
		if (endpoint.includes("/git/trees/")) {
			const result = trees.get(endpoint.split("/").at(-1) as string);
			if (!result) throw new Error("Missing tree");
			return JSON.stringify(result);
		}
		if (endpoint.includes("/git/blobs/")) {
			const raw = blobs.get(endpoint.split("/").at(-1) as string);
			if (raw === undefined) throw new Error("Missing blob");
			const range = args.at(-1)?.match(/\.\[(\d+):(\d+)\]$/);
			if (!range) throw new Error("Blob output must use bounded chunks");
			return Buffer.from(raw).toString("base64").slice(Number(range[1]), Number(range[2]));
		}
		if (endpoint.includes("/git/ref/tags/")) {
			if (endpoint.endsWith(dependencies.executor.tag)) executorTagReads++;
			return JSON.stringify({
				object: {
					type: "commit",
					sha: moveTag && executorTagReads > 1 ? targetCommit : componentCommit,
				},
			});
		}
		if (endpoint.endsWith(`/tags/${helper.tag}`)) return JSON.stringify(helperRelease);
		if (endpoint.endsWith(`/tags/${executor.tag}`)) return JSON.stringify(executorRelease);
		if (endpoint.endsWith("/releases/1")) return JSON.stringify(helperRelease);
		if (endpoint.endsWith("/releases/2")) return JSON.stringify(executorRelease);
		const manifest = manifests.get(Number(endpoint.split("/").at(-1)));
		if (endpoint.includes("/releases/assets/") && manifest !== undefined) return manifest;
		throw new Error(`Unexpected source gate operation: ${args.join(" ")}`);
	};
	return {
		options: { repository, targetCommit, dependencies, run },
		calls,
		trees,
		blobs,
		executorRelease,
		manifests,
		moveTag: () => {
			moveTag = true;
		},
	};
}

function loadActualExecutor(files: SourceFiles, tag?: string): void {
	for (const path of files.keys()) if (path.startsWith("remote-executor/")) files.delete(path);
	let entries = 0;
	let bytes = 0;
	const add = (path: string, value: Buffer) => {
		bytes += value.length;
		if (++entries > 4096 || value.length > 2 * 1024 * 1024 || bytes > 16 * 1024 * 1024)
			throw new Error("Real source fixture budget exceeded");
		files.set(path, value);
	};
	if (tag) {
		if (!/^executor-v\d+\.\d+\.\d+$/.test(tag)) throw new Error("Invalid real source fixture tag");
		const frozen = execFileSync("git", ["rev-parse", `${tag}^{commit}`], {
			timeout: 30_000,
			maxBuffer: 4096,
		})
			.toString()
			.trim();
		if (frozen !== componentCommit)
			throw new Error("Actual executor tag moved from the verified 0.9.1 commit");
		const paths = execFileSync(
			"git",
			["ls-tree", "-r", "--name-only", frozen, "--", "remote-executor"],
			{ timeout: 30_000, maxBuffer: 1024 * 1024 },
		)
			.toString()
			.trim()
			.split("\n");
		if (paths.length > 4096) throw new Error("Real source fixture entry budget exceeded");
		for (const path of [
			...paths.filter((path) => path.endsWith(".go") || /\/go\.(mod|sum)$/.test(path)),
			"scripts/lib/helper-build.ts",
			"shared/remote-executor.ts",
			".github/workflows/helpers-release.yml",
		])
			add(
				path,
				execFileSync("git", ["show", `${frozen}:${path}`], {
					timeout: 30_000,
					maxBuffer: 2 * 1024 * 1024,
				}),
			);
	} else {
		const visit = (directory: string) => {
			for (const entry of readdirSync(directory, { withFileTypes: true })) {
				if (++entries > 4096) throw new Error("Real source fixture entry budget exceeded");
				const path = `${directory}/${entry.name}`;
				if (entry.isDirectory()) visit(path);
				else if (entry.isFile() && (path.endsWith(".go") || /\/go\.(mod|sum)$/.test(path)))
					add(path, readFileSync(path));
			}
		};
		visit("remote-executor");
	}
}

describe("read-only executor build input reuse", () => {
	test("actual repository Go package/import closure reuses identical executor inputs", async () => {
		const f = fixture(
			(files) => loadActualExecutor(files),
			(files) => loadActualExecutor(files, process.env.NF_COMPONENT_BASELINE_TAG),
		);
		const evidence = await collectComponentReleaseEvidence(f.options);
		expect(evidence?.executor.buildInputsSha256).toMatch(/^[a-f0-9]{64}$/);
		expect(
			f.calls.filter((args) => (args[1] as string).includes("/git/blobs/")).length,
		).toBeGreaterThan(30);
	}, 30_000);
	test("trusted control recipe must match component tag; auxiliary-only edits are ignored", async () => {
		const files = baseFiles();
		const readSource = (commit: string, path: string) => {
			const text = files.get(path) as string;
			return commit === targetCommit && path === "scripts/lib/helper-build.ts"
				? text
						.replace("eb33e51f", "ff33e51f")
						.replace("environment.ZSTD_BUILD_PROXY", "environment.CHANGED_HELPER_PROXY")
				: text;
		};
		await expect(
			compareExecutorRecipeIdentity({
				commit: componentCommit,
				controlCommit: targetCommit,
				readSource,
			}),
		).resolves.toBeUndefined();
		await expect(
			compareExecutorRecipeIdentity({
				commit: componentCommit,
				controlCommit: targetCommit,
				readSource: (commit, path) =>
					commit === targetCommit && path === "scripts/lib/helper-build.ts"
						? (files.get(path) as string).replace('"-trimpath"', '"-race"')
						: (files.get(path) as string),
			}),
		).rejects.toThrow("control executor recipe/toolchain differs");
		let reads = 0;
		await compareExecutorRecipeIdentity({
			commit: componentCommit,
			controlCommit: componentCommit,
			readSource: () => {
				reads++;
				throw new Error("same source requires no reads");
			},
		});
		expect(reads).toBe(0);
	});
	test("binary production embed resources preserve exact byte identity", async () => {
		const embed = (files: SourceFiles) => {
			files.set(
				"remote-executor/cmd/narrafork-executor/main.go",
				`${files.get("remote-executor/cmd/narrafork-executor/main.go")}\n//go:embed image.bin\nvar image []byte\n`,
			);
			files.set(
				"remote-executor/cmd/narrafork-executor/image.bin",
				Buffer.from([0, 255, 128, 192]),
			);
		};
		await expect(
			collectComponentReleaseEvidence(fixture(embed, embed).options),
		).resolves.toBeDefined();
		const changed = fixture((files) => {
			embed(files);
			files.set(
				"remote-executor/cmd/narrafork-executor/image.bin",
				Buffer.from([0, 255, 128, 193]),
			);
		}, embed);
		await expect(collectComponentReleaseEvidence(changed.options)).rejects.toThrow(
			"build inputs changed",
		);
	});
	test("APP server protocol must match the target pin, not merely unchanged Go sources", async () => {
		const f = fixture((files) =>
			files.set(
				"server/lib/agent/execution/rpc-types.ts",
				"export const DEVICE_PROTOCOL_VERSION = 2;",
			),
		);
		await expect(collectComponentReleaseEvidence(f.options)).rejects.toThrow(
			"Application RPC protocol",
		);
		expect(f.calls.some((args) => (args[1] as string).includes("/releases"))).toBe(false);
	});
	test.each([
		"valid",
		"moved-tag",
		"changed-manifest",
		"cancel-during-upload",
	])("APP 0.9.2 publisher freezes evidence, avoids auxiliary writes, and rechecks before promotion: %s", async (mode) => {
		const f = fixture();
		const controller = new AbortController();
		const root = mkdtempSync(join(tmpdir(), "component-publisher-test-"));
		const name = "narrafork-0.9.2-linux-x64";
		const bytes = Buffer.from("application executable");
		const metadata = computeBinaryMetadataFromBuffer(name, bytes, {
			version: "0.9.2",
			platformId: "linux-x64",
			target: "bun-linux-x64",
			commit: targetCommit.slice(0, 12),
			buildDate: "2026-10-09T00:00:00Z",
			helperDistribution: dependencies,
		});
		const calls: string[][] = [];
		let release:
			| {
					id: number;
					tag_name: string;
					body: string;
					draft: boolean;
					prerelease: boolean;
					assets: { name: string; size: number; state: string; digest: string }[];
			  }
			| undefined;
		const run: GhRunner = (args) => {
			calls.push(args);
			if (args[0] === "api" && args[1]?.endsWith("/git/ref/tags/v0.9.2"))
				return JSON.stringify({ object: { type: "commit", sha: targetCommit } });
			if (args[0] === "api" && args.includes("--method")) {
				const request = JSON.parse(
					readFileSync(args[args.indexOf("--input") + 1] as string, "utf8"),
				);
				expect(request.tag_name).toBe("v0.9.2");
				release = { id: 12345, ...request, assets: [] };
				return JSON.stringify(release);
			}
			if (args[0] === "release" && args[1] === "upload") {
				if (!release) throw new Error("Missing application draft");
				const asset = readFileSync(args[3] as string);
				release.assets.push({
					name: basename(args[3] as string),
					size: asset.length,
					state: "uploaded",
					digest: `sha256:${createHash("sha256").update(asset).digest("hex")}`,
				});
				if (mode === "moved-tag") f.moveTag();
				if (mode === "changed-manifest") {
					const manifest = f.executorRelease.assets[0];
					if (manifest) {
						const value = JSON.parse(f.manifests.get(manifest.id) as string);
						value.manifest.releasedAt = "2026-10-10T00:00:00Z";
						const raw = JSON.stringify(value);
						f.manifests.set(manifest.id, raw);
						manifest.digest = `sha256:${digest(raw)}`;
						manifest.size = Buffer.byteLength(raw);
					}
				}
				if (mode === "cancel-during-upload") controller.abort(new Error("cancelled upload"));
				return "";
			}
			if (args[0] === "release" && args[1] === "edit") {
				if (release) release.draft = false;
				return "";
			}
			if (args[1]?.endsWith("/releases/tags/v0.9.2")) {
				if (!release) throw new Error("gh failed: HTTP 404");
				return JSON.stringify(release);
			}
			if (args[1]?.includes("/releases?")) return "[]";
			if (args[1]?.endsWith("/releases/12345")) return JSON.stringify(release);
			return f.options.run(args);
		};
		try {
			writeFileSync(join(root, name), bytes);
			writeFileSync(join(root, `${name}.metadata.json`), formatMetadataJson(metadata));
			writeFileSync(join(root, "narrafork-0.9.2-SHA256SUMS"), formatSha256Sums([metadata]));
			writeFileSync(
				join(root, "narrafork-0.9.2-checksums.txt"),
				formatChecksumsReport("0.9.2", [metadata]),
			);
			const options = {
				distDir: root,
				version: "0.9.2",
				repository,
				commit: targetCommit,
				platformSuffixes: new Map([["linux-x64", "linux-x64"]]),
				signal: controller.signal,
				run,
			};
			await expect(publishGitHubRelease({ ...options, dryRun: true })).resolves.toMatchObject({
				dryRun: true,
			});
			expect(calls).toHaveLength(0);
			if (mode === "valid")
				await expect(publishGitHubRelease(options)).resolves.toMatchObject({
					dryRun: false,
					alreadyPublished: false,
				});
			else {
				await expect(publishGitHubRelease(options)).rejects.toThrow();
				expect(release?.draft).toBe(true);
				expect(calls.some((args) => args[0] === "release" && args[1] === "edit")).toBe(false);
			}
			const firstWrite = calls.findIndex(
				(args) => args.includes("--method") || args[0] === "release",
			);
			expect(firstWrite).toBeGreaterThan(0);
			expect(
				calls
					.slice(0, firstWrite)
					.filter((args) => args[1]?.endsWith(`/git/ref/tags/${dependencies.executor.tag}`)),
			).toHaveLength(2);
			expect(calls.filter((args) => args.includes("--method"))).toHaveLength(1);
			expect(
				calls.filter((args) => args[0] === "release").every((args) => args[2] === "v0.9.2"),
			).toBe(true);
		} finally {
			rmSync(root, { recursive: true, force: true });
		}
	});
	test("APP 0.9.2 reuses published 0.9.1 with no envelope fingerprint and reads manifest once", async () => {
		const f = fixture();
		const evidence = await collectComponentReleaseEvidence(f.options);
		expect(evidence?.executor).toMatchObject({ ...dependencies.executor, commit: componentCommit });
		expect(evidence?.executor.buildInputsSha256).toMatch(/^[a-f0-9]{64}$/);
		expect(
			f.calls.filter(
				(args) =>
					args[1] === `repos/${repository}/releases/assets/${f.executorRelease.assets[0]?.id}`,
			),
		).toHaveLength(1);
		expect(f.calls.every((args) => args[0] === "api" && !args.includes("--method"))).toBe(true);
	});
	test("APP-only, docs, pure test and rg/zstd recipe edits do not change inputs", async () => {
		const f = fixture((files) => {
			files.set("package.json", '{"version":"0.9.3"}');
			files.set("frontend/app.ts", "app-only");
			files.set("remote-executor/README.md", "Changed docs");
			files.set(
				"remote-executor/internal/testgit/helpers.go",
				"package testgit\nfunc DifferentHelper(){}\n",
			);
			files.set(
				"remote-executor/cmd/narrafork-executor/main_test.go",
				"package main\n// different tests\n",
			);
			files.set(
				"scripts/lib/helper-build.ts",
				(files.get("scripts/lib/helper-build.ts") as string).replace("eb33e51f", "aa33e51f"),
			);
			files.set(
				".github/workflows/helpers-release.yml",
				(files.get(".github/workflows/helpers-release.yml") as string)
					.replace("name: Auxiliary helpers and executor release", "name: Docs title")
					.replace(
						"preflight:\n    name: Helper preflight",
						"preflight:\n    name: unrelated helper control",
					),
			);
		});
		await expect(collectComponentReleaseEvidence(f.options)).resolves.toBeDefined();
	});
	test("Bun control runtime upgrades reuse the unchanged Go executor", async () => {
		const workflowPath = ".github/workflows/helpers-release.yml";
		const f = fixture(
			(files) =>
				files.set(
					workflowPath,
					(files.get(workflowPath) as string).replace(
						/bun-version: \d+\.\d+\.\d+/g,
						"bun-version: 1.4.3",
					),
				),
			(files) =>
				files.set(
					workflowPath,
					(files.get(workflowPath) as string).replace(
						/bun-version: \d+\.\d+\.\d+/g,
						"bun-version: 1.4.2",
					),
				),
		);
		await expect(collectComponentReleaseEvidence(f.options)).resolves.toBeDefined();
		for (const value of ["latest", "1.4.3\n          bun-version-file: attacker.json"]) {
			const invalid = fixture((files) =>
				files.set(
					workflowPath,
					(files.get(workflowPath) as string).replace(
						/bun-version: \d+\.\d+\.\d+/g,
						`bun-version: ${value}`,
					),
				),
			);
			await expect(collectComponentReleaseEvidence(invalid.options)).rejects.toThrow();
		}
	});
	for (const [name, path, update] of [
		[
			"production Go",
			"remote-executor/cmd/narrafork-executor/main.go",
			(s: string) => `${s}\nfunc Changed(){}\n`,
		],
		[
			"Go module",
			"remote-executor/go.mod",
			(s: string) => `${s}\nrequire example.org/dep v2.0.0\n`,
		],
		["Go sums", "remote-executor/go.sum", (s: string) => `${s}\nchanged\n`],
		[
			"RPC protocol",
			"remote-executor/internal/rpc/protocol.go",
			(s: string) => s.replace("= 1", "= 2"),
		],
		[
			"executor flags",
			"scripts/lib/helper-build.ts",
			(s: string) => s.replace('"-trimpath"', '"-race"'),
		],
		[
			"executor platform mapping",
			"scripts/lib/helper-build.ts",
			(s: string) => s.replace('replace("x64", "amd64")', 'replace("x64", "arm64")'),
		],
		[
			"Go toolchain",
			".github/workflows/helpers-release.yml",
			(s: string) => s.replace("go-version: 1.24.13", "go-version: 1.24.14"),
		],
	] as const)
		test(`${name} changes require independent component publication`, async () => {
			const f = fixture((files) => files.set(path, update(files.get(path) as string)));
			await expect(collectComponentReleaseEvidence(f.options)).rejects.toThrow(
				/build inputs changed|source protocol/,
			);
		});
	test("production embed assets are compared, unrelated package resources excluded", async () => {
		const embed = (files: SourceFiles) => {
			files.set(
				"remote-executor/cmd/narrafork-executor/main.go",
				`${files.get("remote-executor/cmd/narrafork-executor/main.go")}\n//go:embed resources/*.txt\nvar resource string\n`,
			);
			files.set("remote-executor/cmd/narrafork-executor/resources/input.txt", "embedded");
		};
		const unchanged = fixture(embed, embed);
		await expect(collectComponentReleaseEvidence(unchanged.options)).resolves.toBeDefined();
		const changed = fixture((files) => {
			embed(files);
			files.set("remote-executor/cmd/narrafork-executor/resources/input.txt", "changed");
		}, embed);
		await expect(collectComponentReleaseEvidence(changed.options)).rejects.toThrow(
			"build inputs changed",
		);
	});
	test("unknown build-affecting fields and unparsed workflow steps fail closed", async () => {
		for (const update of [
			(s: string) =>
				s.replace(
					"go-version: 1.24.13",
					"go-version: 1.24.13\n          go-version-file: attacker.mod",
				),
			(s: string) =>
				s.replace(
					"CONTAINER_RUNTIME: docker",
					"CONTAINER_RUNTIME: docker\n          GOEXPERIMENT: unknown",
				),
			(s: string) =>
				s.replace(
					"    steps:\n      - uses: actions/checkout",
					"    steps:\n      - run: arbitrary-build-command\n      - uses: actions/checkout",
				),
		]) {
			const f = fixture((files) =>
				files.set(
					".github/workflows/helpers-release.yml",
					update(files.get(".github/workflows/helpers-release.yml") as string),
				),
			);
			await expect(collectComponentReleaseEvidence(f.options)).rejects.toThrow(/Unknown executor/);
		}
	});
	test("moved executor tag and changed manifest evidence are rejected", async () => {
		const moving = fixture();
		moving.moveTag();
		await expect(collectComponentReleaseEvidence(moving.options)).rejects.toThrow("tag moved");
		const f = fixture();
		const evidence = await collectComponentReleaseEvidence(f.options);
		if (!evidence) throw new Error("Missing test evidence");
		await expect(
			verifyComponentReleaseEvidence({
				...f.options,
				evidence: { executor: { ...evidence.executor, manifestSha256: "c".repeat(64) } },
			}),
		).rejects.toThrow("evidence mismatch");
		await expect(
			verifyComponentReleaseEvidence({
				...f.options,
				evidence: { executor: { ...evidence.executor, commit: targetCommit } },
			}),
		).rejects.toThrow("evidence mismatch");
	});
	test("manifest digest corruption and missing platform/license retain full asset validation", async () => {
		for (const breakRelease of [
			(f: ReturnType<typeof fixture>) => {
				const asset = f.executorRelease.assets[0];
				if (asset) asset.digest = `sha256:${"f".repeat(64)}`;
			},
			(f: ReturnType<typeof fixture>) => {
				f.executorRelease.assets.pop();
			},
		]) {
			const f = fixture();
			breakRelease(f);
			await expect(collectComponentReleaseEvidence(f.options)).rejects.toThrow();
		}
	});
	test("legacy no-JSON target returns undefined with zero helper requests, never substitutes current pin", async () => {
		const f = fixture((files) => files.delete("shared/component-versions.json"));
		await expect(
			collectComponentReleaseEvidence({ ...f.options, dependencies: undefined }),
		).resolves.toBeUndefined();
		expect(f.calls.some((args) => /\/releases|\/git\/ref\/tags/.test(args[1] as string))).toBe(
			false,
		);
	});
	test("present target configuration requires exact declaration including protocol", async () => {
		const f = fixture();
		for (const declared of [
			undefined,
			createHelperDistributionDependencies("0.9.2", 1),
			createHelperDistributionDependencies("0.9.1", 2),
		])
			await expect(
				collectComponentReleaseEvidence({ ...f.options, dependencies: declared }),
			).rejects.toThrow("target component pin");
		expect(f.calls.some((args) => (args[1] as string).includes("/releases"))).toBe(false);
	});
	test("source-pin inspection used by build-only never queries helper network", async () => {
		const f = fixture();
		await expect(readTargetComponentDependencies(f.options)).resolves.toEqual(dependencies);
		expect(f.calls.every((args) => /\/git\/(trees|blobs)\//.test(args[1] as string))).toBe(true);
	});
	test("fork missing executor release/tag hard-fails without upstream fallback", async () => {
		const f = fixture();
		const run: GhRunner = (args) => {
			if ((args[1] as string).includes(`/git/ref/tags/${dependencies.executor.tag}`))
				throw new Error("gh: Not Found (HTTP 404)");
			return f.options.run(args);
		};
		await expect(collectComponentReleaseEvidence({ ...f.options, run })).rejects.toThrow("404");
		expect(f.calls.every((args) => (args[1] as string).startsWith(`repos/${repository}/`))).toBe(
			true,
		);
	});
	test("nested executor workspace inputs fail closed", async () => {
		for (const path of [
			"go.work",
			"go.work.sum",
			"remote-executor/go.work",
			"remote-executor/go.work.sum",
		]) {
			const f = fixture((files) => files.set(path, "go 1.24.0\nuse .\n"));
			await expect(collectComponentReleaseEvidence(f.options)).rejects.toThrow("workspace input");
		}
	});
	test("the module root import is a production package, not an ignored dependency", async () => {
		const root = (files: SourceFiles) => {
			files.set(
				"remote-executor/cmd/narrafork-executor/main.go",
				'package main\nimport ("github.com/narrafork/remote-executor"; "github.com/narrafork/remote-executor/internal/rpc")\nfunc main(){println(executor.Root, rpc.ProtocolVersion)}\n',
			);
			files.set("remote-executor/root.go", "package executor\nconst Root = 1\n");
		};
		await expect(
			collectComponentReleaseEvidence(fixture(root, root).options),
		).resolves.toBeDefined();
		const changed = fixture((files) => {
			root(files);
			files.set("remote-executor/root.go", "package executor\nconst Root = 2\n");
		}, root);
		await expect(collectComponentReleaseEvidence(changed.options)).rejects.toThrow(
			"build inputs changed",
		);
	});
	test("unselected array/object/asserted initializers cannot hide build side effects", async () => {
		for (const initializer of [
			"{ x: (process.env.GOFLAGS = '-tags=feature') }",
			"[process.env.GOTOOLCHAIN = 'auto']",
			"({ x: mutateBuildEnvironment() } as const)",
		]) {
			const f = fixture((files) =>
				files.set(
					"scripts/lib/helper-build.ts",
					`${files.get("scripts/lib/helper-build.ts")}\nconst unused = ${initializer};`,
				),
			);
			await expect(collectComponentReleaseEvidence(f.options)).rejects.toThrow(
				"module initialization",
			);
		}
	});
	test("unknown imported recipe dependencies fail closed instead of hashing only their import name", async () => {
		const f = fixture((files) =>
			files.set(
				"scripts/lib/helper-build.ts",
				`import {flags} from './new-executor-recipe';\n${(files.get("scripts/lib/helper-build.ts") as string).replace('"-trimpath"', "flags")}`,
			),
		);
		await expect(collectComponentReleaseEvidence(f.options)).rejects.toThrow("recipe dependency");
	});
	test("source runners receive cancellation, 30s timeout and one-MiB output controls", async () => {
		const f = fixture();
		let received = 0;
		const run: GhRunner = (args, controls) => {
			expect(controls?.timeoutMs).toBe(30_000);
			expect(controls?.maxOutputBytes).toBe(1024 * 1024);
			expect(controls?.signal).toBeInstanceOf(AbortSignal);
			received++;
			return f.options.run(args);
		};
		await collectComponentReleaseEvidence({ ...f.options, run });
		expect(received).toBeGreaterThan(10);
		const expired = fixture();
		await expect(
			collectComponentReleaseEvidence({ ...expired.options, deadline: Date.now() - 1 }),
		).rejects.toThrow("deadline");
		expect(expired.calls).toHaveLength(0);
		const parent = new AbortController();
		await expect(
			collectComponentReleaseEvidence({
				...expired.options,
				signal: parent.signal,
				run: (_args, controls) =>
					new Promise<never>((_, reject) => {
						controls?.signal?.addEventListener("abort", () => reject(controls.signal?.reason), {
							once: true,
						});
						parent.abort(new Error("parent cancelled active source read"));
					}),
			}),
		).rejects.toThrow("parent cancelled active source read");
	});
	test("total source bytes are bounded even with individually legal inputs", async () => {
		const f = fixture((files) => {
			for (let i = 0; i < 9; i++)
				files.set(
					`remote-executor/cmd/narrafork-executor/input${i}.go`,
					`package main\n//${i}${"x".repeat(2 * 1024 * 1024 - 40)}\n`,
				);
		});
		await expect(collectComponentReleaseEvidence(f.options)).rejects.toThrow("16MiB");
	});
	test("entry/text/output budgets and parent cancellation fail before unbounded collection", async () => {
		const cancelled = fixture();
		await expect(
			collectComponentReleaseEvidence({
				...cancelled.options,
				signal: AbortSignal.abort(new Error("cancelled")),
			}),
		).rejects.toThrow("cancelled");
		expect(cancelled.calls).toHaveLength(0);
		const entries = fixture();
		entries.trees.set(targetCommit, {
			truncated: false,
			tree: Array.from({ length: 4097 }, (_, i) => ({
				path: `entry${i}`,
				sha: componentCommit,
				mode: "100644",
				type: "blob",
				size: 1,
			})),
		});
		await expect(collectComponentReleaseEvidence(entries.options)).rejects.toThrow("4096");
		const huge = fixture((files) =>
			files.set("shared/component-versions.json", " ".repeat(2 * 1024 * 1024 + 1)),
		);
		await expect(collectComponentReleaseEvidence(huge.options)).rejects.toThrow("2MiB");
		const output = fixture();
		await expect(
			collectComponentReleaseEvidence({
				...output.options,
				run: () => "x".repeat(1024 * 1024 + 1),
			}),
		).rejects.toThrow("output exceeds");
	});
	test("blob byte identity is checked rather than treating its supplied hash as source evidence", async () => {
		const f = fixture();
		const first = f.blobs.keys().next().value as string;
		f.blobs.set(first, "malicious replacement source");
		await expect(collectComponentReleaseEvidence(f.options)).rejects.toThrow("blob identity");
	});
});
