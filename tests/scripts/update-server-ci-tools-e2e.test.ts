import { afterEach, describe, expect, test } from "bun:test";
import { createHash } from "node:crypto";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { EXECUTOR_LICENSE_FILES, HELPER_LICENSE_FILES } from "../../scripts/lib/helper-release";
import {
	type HelperPublicationOptions,
	type HelperReleasePlan,
	publishHelperReleasePlan,
} from "../../scripts/lib/helper-release-control";
import {
	getHelperAssetName,
	HELPER_MANIFEST_FILENAME,
	HELPER_PLATFORMS,
	HELPER_TOOL_VERSIONS,
	HELPER_TOOLS,
} from "../../shared/helper-distribution";
import {
	EXECUTOR_MANIFEST_FILENAME,
	EXECUTOR_PLATFORMS,
	executorPublishedFilename,
} from "../../shared/remote-executor";

const temporary: string[] = [];
afterEach(async () => {
	for (const root of temporary.splice(0)) await rm(root, { recursive: true, force: true });
});
const sha = (bytes: string | Uint8Array) => createHash("sha256").update(bytes).digest("hex");
const commit = "a".repeat(40);
async function fixture(kind: "helpers" | "executor") {
	const root = await mkdtemp(join(tmpdir(), "ci-tools-e2e-"));
	temporary.push(root);
	const bundleDir = join(root, "bundle");
	await mkdir(bundleDir);
	const version = kind === "helpers" ? "1.0.0" : "1.2.1";
	const plan: HelperReleasePlan = {
		schemaVersion: 1,
		repository: "Fixture/Fork",
		defaultBranch: "trunk",
		tag: `${kind}-v${version}`,
		commit,
		controlCommit: commit,
		kind,
		version,
		protocolVersion: 1,
		sourceRunId: "11",
		publish: true,
	};
	const token = "fixture-never-real";
	const environment = {
		GITHUB_REPOSITORY: plan.repository,
		GITHUB_EVENT_NAME: "workflow_dispatch",
		GITHUB_REF: "refs/heads/trunk",
		GITHUB_SHA: commit,
		GITHUB_RUN_ID: "22",
		GITHUB_RUN_ATTEMPT: "3",
		GH_TOKEN: "fixture-gh-never-real",
		GITHUB_OUTPUT: join(root, "output.txt"),
		GITHUB_STEP_SUMMARY: join(root, "summary.txt"),
		NF_UPDATE_SERVER: "https://fixture.invalid",
		NF_UPDATE_TOKEN: token,
	};
	const files = new Map<string, Buffer>();
	const artifactFile = (name: string) => {
		const bytes = Buffer.from(`${kind}:${name}\n`);
		files.set(name, bytes);
		return { name, size: bytes.length, sha256: sha(bytes) };
	};
	const licenses = (kind === "helpers" ? HELPER_LICENSE_FILES : EXECUTOR_LICENSE_FILES).map(
		artifactFile,
	);
	const manifest =
		kind === "helpers"
			? {
					schemaVersion: 1,
					repository: plan.repository,
					tag: plan.tag,
					commit,
					catalogVersion: version,
					files: HELPER_TOOLS.flatMap((tool) =>
						HELPER_PLATFORMS.map((platform) => ({
							tool,
							toolVersion: HELPER_TOOL_VERSIONS[tool],
							platform,
							...artifactFile(getHelperAssetName(tool, platform)),
						})),
					),
					licenses,
				}
			: {
					schemaVersion: 1,
					repository: plan.repository,
					tag: plan.tag,
					commit,
					manifest: {
						version,
						protocolVersion: 1,
						releasedAt: "1970-01-01T00:00:00Z",
						platforms: Object.fromEntries(
							EXECUTOR_PLATFORMS.map((platform) => {
								const file = artifactFile(executorPublishedFilename(version, platform));
								return [platform, { filename: file.name, size: file.size, sha256: file.sha256 }];
							}),
						),
					},
					licenses,
				};
	const manifestName = kind === "helpers" ? HELPER_MANIFEST_FILENAME : EXECUTOR_MANIFEST_FILENAME;
	files.set(manifestName, Buffer.from(`${JSON.stringify(manifest, null, 2)}\n`));
	for (const [name, bytes] of files) await writeFile(join(bundleDir, name), bytes);
	let publicRelease = false;
	let serverRequests = 0;
	const release = {
		id: 99,
		tag_name: plan.tag,
		draft: true,
		prerelease: false,
		assets: [] as { id: number; name: string; size: number; digest: string; state: string }[],
	};
	const githubWrites: string[] = [];
	const run = async (args: string[]) => {
		if (args[0] === "release") {
			githubWrites.push(args[1] ?? "");
			if (args[1] === "upload") {
				const path = args[3];
				if (!path) throw new Error("Missing upload path");
				const bytes = await readFile(path);
				const name = path.split("/").at(-1) as string;
				release.assets.push({
					id: release.assets.length + 1,
					name,
					size: bytes.length,
					digest: `sha256:${sha(bytes)}`,
					state: "uploaded",
				});
				return "";
			}
			if (args[1] === "edit") {
				release.draft = false;
				publicRelease = true;
				return "";
			}
			throw new Error("Unexpected GitHub write");
		}
		if (args[0] !== "api") throw new Error("Unexpected GitHub command");
		const path = args[1] ?? "";
		if (path === `repos/${plan.repository}`)
			return JSON.stringify({ full_name: plan.repository, default_branch: "trunk" });
		if (path.endsWith("/environments/release"))
			return JSON.stringify({
				name: "release",
				protection_rules: [
					{ type: "required_reviewers", reviewers: [{ type: "User", reviewer: { id: 1 } }] },
				],
				deployment_branch_policy: { protected_branches: false, custom_branch_policies: true },
			});
		if (path.includes("deployment-branch-policies"))
			return JSON.stringify({
				total_count: 1,
				branch_policies: [{ name: "trunk", type: "branch" }],
			});
		if (path.includes("/git/ref/"))
			return JSON.stringify({ object: { type: "commit", sha: commit } });
		if (path.includes("/compare/"))
			return JSON.stringify({ status: "identical", merge_base_commit: { sha: commit } });
		if (path === `repos/${plan.repository}/releases/${release.id}`) {
			// The publisher binds the uploaded asset set to this known draft ID before publication.

			return JSON.stringify(release);
		}
		if (path === `repos/${plan.repository}/releases/tags/${plan.tag}`)
			return JSON.stringify(release);
		if (path.includes("/releases/assets/")) {
			const id = Number(path.split("/").at(-1));
			const asset = release.assets.find((entry) => entry.id === id);
			if (!asset) throw new Error("Missing asset");
			return files.get(asset.name)?.toString("utf8") ?? "";
		}
		throw new Error(`Unexpected GitHub API ${path}`);
	};
	const options = {
		environment,
		run,
		sourceRunAttempt: "1",
		receiptPath: join(root, "receipt.json"),
		fetchImpl: (async () => {
			serverRequests++;
			throw new Error("Forbidden server request");
		}) as unknown as typeof fetch,
	};
	return {
		root,
		plan,
		bundleDir,
		files,
		release,
		githubWrites,
		options,
		serverRequests: () => serverRequests,
		publicRelease: () => publicRelease,
	};
}
for (const kind of ["helpers", "executor"] as const)
	describe(`${kind} GitHub-only real publisher`, () => {
		for (const config of ["empty", "partial", "invalid", "configured"] as const)
			test(`publishes exact bundle with ${config} mirror configuration and zero server IO`, async () => {
				const f = await fixture(kind);
				if (config === "empty") {
					delete (f.options.environment as NodeJS.ProcessEnv).NF_UPDATE_SERVER;
					delete (f.options.environment as NodeJS.ProcessEnv).NF_UPDATE_TOKEN;
				}
				if (config === "partial")
					delete (f.options.environment as NodeJS.ProcessEnv).NF_UPDATE_TOKEN;
				if (config === "invalid") f.options.environment.NF_UPDATE_SERVER = "not a valid URL";
				await publishHelperReleasePlan(f.plan, f.bundleDir, false, f.options);
				expect(f.publicRelease()).toBe(true);
				expect(f.release.draft).toBe(false);
				expect(f.serverRequests()).toBe(0);
				const receipt = JSON.parse(await readFile(f.options.receiptPath, "utf8"));
				expect(receipt.releaseId).toBe(99);
				expect(receipt.repository).toBe("Fixture/Fork");
				expect(receipt.assets).toHaveLength(f.files.size);
				for (const asset of receipt.assets)
					expect(asset.digest).toBe(`sha256:${sha(f.files.get(asset.name) as Buffer)}`);
				expect(receipt.source).toEqual({
					runId: 22,
					runAttempt: 3,
					workflowPath: ".github/workflows/helpers-release.yml",
					nativeProof: "verified-original-bundle",
				});
				expect(receipt.buildSource).toEqual({
					runId: 11,
					runAttempt: 1,
					workflowPath: ".github/workflows/helpers-release.yml",
					nativeProof: "verified-original-bundle",
				});
			});
		for (const field of [
			"publisher-run",
			"publisher-attempt",
			"build-run",
			"build-attempt",
			"artifact",
		] as const)
			test(`${field === "artifact" ? "invalid" : "missing/invalid"} ${field} provenance fails before any GitHub write`, async () => {
				const f = await fixture(kind);
				for (const value of [
					undefined,
					"",
					"0",
					"-1",
					"01",
					"1.5",
					"1e2",
					" 1",
					"not-an-id",
					"9007199254740992",
				]) {
					// Empty sourceRunId explicitly denotes this dispatch, and artifact ID is optional.
					if (field === "build-run" && value === "") continue;
					if (field === "artifact" && (value === undefined || value === "")) continue;
					const plan = { ...f.plan };
					const environment: NodeJS.ProcessEnv = { ...f.options.environment };
					const options: HelperPublicationOptions = {
						...f.options,
						environment,
						bundleArtifactId: "456",
					};
					if (field === "publisher-run") environment.GITHUB_RUN_ID = value;
					if (field === "publisher-attempt") environment.GITHUB_RUN_ATTEMPT = value;
					if (field === "build-run") plan.sourceRunId = value as string;
					if (field === "build-attempt") options.sourceRunAttempt = value;
					if (field === "artifact") options.bundleArtifactId = value;
					await expect(
						publishHelperReleasePlan(plan, f.bundleDir, false, options),
					).rejects.toThrow();
					expect(f.githubWrites).toEqual([]);
					expect(f.publicRelease()).toBe(false);
				}
				expect(f.serverRequests()).toBe(0);
				await expect(readFile(f.options.receiptPath)).rejects.toThrow();
				await expect(readFile(f.options.environment.GITHUB_OUTPUT)).rejects.toThrow();
			});
		test("publication freezes original run, attempt and artifact before writes", async () => {
			const f = await fixture(kind);
			const environment = { ...f.options.environment };
			const options = { ...f.options, environment, bundleArtifactId: "456" };
			let mutated = false;
			const run = async (args: string[]) => {
				if (args[0] === "release" && !mutated) {
					mutated = true;
					environment.GITHUB_RUN_ID = "33";
					environment.GITHUB_RUN_ATTEMPT = "4";
					f.plan.sourceRunId = "44";
					options.sourceRunAttempt = "5";
					options.bundleArtifactId = "789";
				}
				return f.options.run(args);
			};
			// Keep the same options object so late mutation would expose a receipt-time reparse.
			options.run = run;
			await publishHelperReleasePlan(f.plan, f.bundleDir, false, options);
			expect(mutated).toBe(true);
			expect(f.publicRelease()).toBe(true);
			const receipt = JSON.parse(await readFile(f.options.receiptPath, "utf8"));
			expect(receipt.source).toEqual({
				runId: 22,
				runAttempt: 3,
				workflowPath: ".github/workflows/helpers-release.yml",
				nativeProof: "verified-original-bundle",
			});
			expect(receipt.buildSource).toEqual({
				runId: 11,
				runAttempt: 1,
				workflowPath: ".github/workflows/helpers-release.yml",
				bundleArtifactId: 456,
				nativeProof: "verified-original-bundle",
			});
		});
		test("normal dispatch uses current run ID but retains the verified original attempt", async () => {
			const f = await fixture(kind);
			await publishHelperReleasePlan({ ...f.plan, sourceRunId: "" }, f.bundleDir, false, f.options);
			const receipt = JSON.parse(await readFile(f.options.receiptPath, "utf8"));
			expect(receipt.source.runAttempt).toBe(3);
			expect(receipt.buildSource.runId).toBe(22);
			expect(receipt.buildSource.runAttempt).toBe(1);
			expect(receipt.buildSource.bundleArtifactId).toBeUndefined();
		});
		for (const withProvenance of [false, true])
			test(`offline preview needs no CI identities or network; provenance flags=${withProvenance}`, async () => {
				const f = await fixture(kind);
				let requests = 0;
				await publishHelperReleasePlan(f.plan, f.bundleDir, true, {
					environment: {},
					...(withProvenance ? { sourceRunAttempt: "1", bundleArtifactId: "456" } : {}),
					run: async () => {
						requests++;
						throw new Error("Forbidden offline GitHub request");
					},
				});
				expect(requests).toBe(0);
				expect(f.githubWrites).toEqual([]);
				expect(f.serverRequests()).toBe(0);
				await expect(readFile(f.options.receiptPath)).rejects.toThrow();
			});
		test("corrupt source bundle cannot make the draft public", async () => {
			const f = await fixture(kind);
			const binary = [...f.files.keys()][0] as string;
			await writeFile(join(f.bundleDir, binary), "corrupt");
			await expect(
				publishHelperReleasePlan(f.plan, f.bundleDir, false, f.options),
			).rejects.toThrow();
			expect(f.publicRelease()).toBe(false);
			expect(f.githubWrites).toEqual([]);
		});
		for (const revoked of ["tag", "environment", "ancestor"] as const)
			test(`revoked ${revoked} after upload blocks public promotion`, async () => {
				const f = await fixture(kind);
				const original = f.options.run;
				let uploaded = false;
				const run = async (args: string[]) => {
					const path = args[1] ?? "";
					if (uploaded && revoked === "tag" && path.includes("/git/ref/"))
						return JSON.stringify({ object: { type: "commit", sha: "b".repeat(40) } });
					if (uploaded && revoked === "environment" && path.endsWith("/environments/release"))
						return JSON.stringify({
							name: "release",
							protection_rules: [],
							deployment_branch_policy: { protected_branches: false, custom_branch_policies: true },
						});
					if (uploaded && revoked === "ancestor" && path.includes("/compare/"))
						return JSON.stringify({
							status: "diverged",
							merge_base_commit: { sha: "b".repeat(40) },
						});
					const value = await original(args);
					if (args[0] === "release" && args[1] === "upload") uploaded = true;
					return value;
				};
				await expect(
					publishHelperReleasePlan(f.plan, f.bundleDir, false, { ...f.options, run }),
				).rejects.toThrow();
				expect(uploaded).toBe(true);
				expect(f.publicRelease()).toBe(false);
				expect(f.githubWrites).not.toContain("edit");
				await expect(readFile(f.options.receiptPath)).rejects.toThrow();
			});
		test("parent cancellation never writes GitHub or contacts a server", async () => {
			const f = await fixture(kind);
			await expect(
				publishHelperReleasePlan(f.plan, f.bundleDir, false, {
					...f.options,
					signal: AbortSignal.abort(new Error("cancelled")),
				}),
			).rejects.toThrow("cancelled");
			expect(f.githubWrites).toEqual([]);
			expect(f.serverRequests()).toBe(0);
		});
	});
