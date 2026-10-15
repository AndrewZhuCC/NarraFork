import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import {
	resolveMirrorWorkflowTrigger,
	verifyMirrorPublicationReceipt,
	verifyProtectedMirrorEnvironment,
} from "../../scripts/lib/mirror-workflow";
import { commit, indexCommit, mirrorFixture, repository } from "./mirror-fixture";

function sourceFixture(main = true) {
	const path = main ? ".github/workflows/release.yml" : ".github/workflows/helpers-release.yml";
	const source = {
		id: 123,
		run_attempt: 2,
		event: "workflow_dispatch",
		status: "completed",
		conclusion: "success",
		path,
		head_branch: "main",
		repository: { full_name: repository },
		head_repository: { full_name: repository },
	};
	const event = {
		repository: { full_name: repository, default_branch: "main" },
		workflow_run: source,
	};
	const artifact = {
		id: 81,
		name: `${main ? "main" : "auxiliary"}-publication-receipt-123-2`,
		expired: false,
		size_in_bytes: 1024,
	};
	const jobs = (
		main
			? ["Publish verified release", "Release Gate"]
			: ["Publish approved helper draft", "Auxiliary Release Gate"]
	).map((name) => ({ name, status: "completed", conclusion: "success" }));
	const state = { artifacts: [artifact], jobs, protection: true };
	const calls: string[][] = [];
	const run = async (args: string[]) => {
		calls.push(args);
		const endpoint = args[1];
		if (endpoint.includes("/artifacts?"))
			return JSON.stringify({ total_count: state.artifacts.length, artifacts: state.artifacts });
		if (endpoint.includes("/jobs?"))
			return JSON.stringify({ total_count: state.jobs.length, jobs: state.jobs });
		if (endpoint.endsWith("/attempts/2")) return JSON.stringify(source);
		if (endpoint.endsWith("environments/release"))
			return JSON.stringify({
				name: "release",
				protection_rules: state.protection
					? [{ type: "required_reviewers", reviewers: [{ type: "User" }] }]
					: [],
			});
		throw new Error("Read-only trust spy refused unexpected source API");
	};
	return { source, event, artifact, state, run, calls };
}
describe("independent mirror trigger provenance", () => {
	for (const main of [true, false])
		test(`exact ${main ? "main" : "auxiliary"} receipt attempt plus real publisher and gate is eligible`, async () => {
			const f = sourceFixture(main);
			const result = await resolveMirrorWorkflowTrigger(f.event, repository, f.run);
			expect(result?.artifactName).toBe(f.artifact.name);
			expect(result?.runAttempt).toBe(2);
			expect(f.calls.some((c) => c[1].includes("/attempts/2/jobs?"))).toBe(true);
		});
	test("build-only success without publication receipt is not eligible and never requests approval", async () => {
		const f = sourceFixture();
		f.state.artifacts = [];
		expect(await resolveMirrorWorkflowTrigger(f.event, repository, f.run)).toBeUndefined();
		expect(f.calls.some((c) => c[1].includes("environments"))).toBe(false);
	});
	for (const key of ["event", "head_branch", "conclusion", "path"] as const)
		test(`rejects untrusted source ${key}`, async () => {
			const f = sourceFixture();
			f.source[key] = "untrusted";
			await expect(resolveMirrorWorkflowTrigger(f.event, repository, f.run)).rejects.toThrow();
		});
	test("fork head repository cannot use privileged workflow_run", async () => {
		const f = sourceFixture();
		f.source.head_repository.full_name = "attacker/fork";
		await expect(resolveMirrorWorkflowTrigger(f.event, repository, f.run)).rejects.toThrow();
	});
	test("prior-attempt receipt does not authorize current source run", async () => {
		const f = sourceFixture();
		f.artifact.name = "main-publication-receipt-123-1";
		expect(await resolveMirrorWorkflowTrigger(f.event, repository, f.run)).toBeUndefined();
	});
	test("artifact duplicates and oversized raw-data receipts are rejected", async () => {
		const f = sourceFixture();
		f.state.artifacts.push({ ...f.artifact });
		await expect(resolveMirrorWorkflowTrigger(f.event, repository, f.run)).rejects.toThrow();
		f.state.artifacts.pop();
		f.artifact.size_in_bytes = 129 * 1024;
		await expect(resolveMirrorWorkflowTrigger(f.event, repository, f.run)).rejects.toThrow();
	});
	for (const conclusion of ["skipped", "failure", "cancelled"])
		test(`${conclusion} publisher is never papered over by a green workflow`, async () => {
			const f = sourceFixture();
			f.state.jobs[0].conclusion = conclusion;
			await expect(resolveMirrorWorkflowTrigger(f.event, repository, f.run)).rejects.toThrow();
		});
	test("requires existing protected release Environment; never creates/bypasses one", async () => {
		const f = sourceFixture();
		await verifyProtectedMirrorEnvironment(repository, f.run);
		f.state.protection = false;
		await expect(verifyProtectedMirrorEnvironment(repository, f.run)).rejects.toThrow(
			"existing release",
		);
		expect(f.calls.every((c) => c[0] === "api" && !c.includes("-X"))).toBe(true);
	});
	test("bounded receipt is only a hint; current public Release/tag/assets/index are revalidated", async () => {
		const s = sourceFixture();
		const f = mirrorFixture(false);
		const trigger = await resolveMirrorWorkflowTrigger(s.event, repository, s.run);
		if (!trigger) throw new Error("missing trigger");
		const receipt = {
			schemaVersion: 1,
			kind: "main",
			repository,
			tag: "v1.2.0",
			version: "1.2.0",
			commit,
			releaseId: 17,
			assets: f.assets.map(({ id, name, size, digest }) => ({ id, name, size, digest })),
			indexCommit,
			generation: 1,
			buildSource: {
				runId: 9,
				runAttempt: 1,
				workflowPath: ".github/workflows/release.yml",
				bundleArtifactId: 7,
				nativeProof: "verified-original-bundle",
			},
			source: {
				runId: 123,
				runAttempt: 2,
				workflowPath: ".github/workflows/release.yml",
				nativeProof: "verified-original-bundle",
			},
		};
		const run = (args: string[]) =>
			args[1].includes("/compare/") ? JSON.stringify({ status: "identical" }) : f.run(args);
		expect((await verifyMirrorPublicationReceipt(receipt, trigger, run)).tag).toBe(receipt.tag);
		// Original QA run A=9/1 does not replace publisher run B=123/2.
		receipt.source.runId = receipt.buildSource.runId;
		await expect(verifyMirrorPublicationReceipt(receipt, trigger, run)).rejects.toThrow(
			"provenance",
		);
		receipt.source.runId = 123;
		receipt.source.runAttempt = 1;
		await expect(verifyMirrorPublicationReceipt(receipt, trigger, run)).rejects.toThrow(
			"provenance",
		);
		receipt.source.runAttempt = 2;
		receipt.buildSource.runAttempt = 0;
		await expect(verifyMirrorPublicationReceipt(receipt, trigger, run)).rejects.toThrow(
			"build provenance",
		);
		receipt.buildSource.runAttempt = 1;
		f.publicRelease.draft = true;
		await expect(verifyMirrorPublicationReceipt(receipt, trigger, run)).rejects.toThrow("public");
	});
	test("auxiliary publisher B/attempt is bound independently of its original build A/attempt", async () => {
		const s = sourceFixture(false);
		const trigger = await resolveMirrorWorkflowTrigger(s.event, repository, s.run);
		if (!trigger) throw new Error("missing auxiliary trigger");
		const tag = "helpers-v1.0.0";
		const asset = {
			id: 7,
			name: "helper-manifest-v1.json",
			size: 7,
			digest: `sha256:${"a".repeat(64)}`,
			state: "uploaded",
		};
		const receipt = {
			schemaVersion: 1,
			kind: "helpers",
			repository,
			tag,
			version: "1.0.0",
			commit,
			releaseId: 17,
			assets: [{ id: asset.id, name: asset.name, size: asset.size, digest: asset.digest }],
			source: {
				runId: 123,
				runAttempt: 2,
				workflowPath: ".github/workflows/helpers-release.yml",
				nativeProof: "verified-original-bundle",
			},
			buildSource: {
				runId: 11,
				runAttempt: 1,
				workflowPath: ".github/workflows/helpers-release.yml",
				bundleArtifactId: 55,
				nativeProof: "verified-original-bundle",
			},
		};
		const run = async (args: string[]) => {
			if (args[1].includes("git/ref/tags/"))
				return JSON.stringify({ ref: `refs/tags/${tag}`, object: { sha: commit, type: "commit" } });
			if (args[1].includes("releases/tags/"))
				return JSON.stringify({
					id: 17,
					tag_name: tag,
					draft: false,
					published_at: "2026-07-01T00:00:00Z",
				});
			if (args[1].includes("releases/17/assets?")) return JSON.stringify([asset]);
			throw new Error("Unexpected auxiliary receipt request");
		};
		expect((await verifyMirrorPublicationReceipt(receipt, trigger, run)).source.runId).toBe(123);
		receipt.source.runId = 11;
		await expect(verifyMirrorPublicationReceipt(receipt, trigger, run)).rejects.toThrow(
			"provenance",
		);
	});
	test("workflow mode steps do not cross-inject direct and relay secrets", () => {
		const workflow = readFileSync(
			new URL("../../.github/workflows/update-server-mirror.yml", import.meta.url),
			"utf8",
		);
		const direct = workflow
			.split("- name: Direct mirror")[1]
			.split("- name: Stage for server pull")[0];
		const dav = workflow
			.split("- name: Stage for server pull")[1]
			.split("- name: Preserve independent mirror result")[0];
		expect(direct).toContain("NF_MIRROR_TRANSPORT: direct");
		expect(direct).toContain("secrets.NF_UPDATE_TOKEN");
		expect(direct).not.toContain("NF_MIRROR_WEBDAV_");
		expect(direct).not.toContain("NF_MIRROR_RELAY_SIGNING_KEY");
		expect(dav).toContain("if: vars.NF_MIRROR_TRANSPORT == 'webdav'");
		expect(dav).toContain("secrets.NF_MIRROR_RELAY_SIGNING_KEY");
		expect(dav).not.toContain("NF_UPDATE_TOKEN");
		expect(dav).not.toContain("NF_UPDATE_SERVER");
		expect(workflow).toContain("vars.NF_MIRROR_TRANSPORT || 'direct'");
		expect(workflow).toContain("resolveMirrorTransport(process.env)");
		expect(workflow).toContain("STAGED_FOR_PULL is not a committed mirror");
	});
	test("workflow is read-only, default-branch trusted, separately locked, bounded and independently approved", () => {
		const workflow = readFileSync(
			new URL("../../.github/workflows/update-server-mirror.yml", import.meta.url),
			"utf8",
		);
		expect(workflow).toContain("vars.NF_MIRROR_ENABLED == 'true'");
		expect(workflow).toMatch(/NF_UPDATE_SERVER: \$\{\{ vars\.NF_UPDATE_SERVER \}\}/);
		expect(workflow).toMatch(/NF_UPDATE_TOKEN: \$\{\{ secrets\.NF_UPDATE_TOKEN \}\}/);
		expect(workflow).not.toContain("secrets.NF_UPDATE_SERVER");
		expect(workflow).toContain("contents: read");
		expect(workflow).toContain("actions: read");
		expect(workflow).not.toContain("contents: write");
		expect(workflow).not.toContain("continue-on-error");
		expect(workflow).toContain("environment: release");
		expect(workflow).toContain("group: independent-update-server-mirror");
		expect(workflow).toContain("timeout-minutes: 35");
		expect(workflow).toContain("github.event.repository.default_branch");
		expect(workflow).toContain("needs.preflight.outputs.control-sha");
		expect(workflow).not.toContain("control.js");
		expect(workflow).not.toContain("zstd");
		expect(workflow).toContain("artifacts/source-publication/publication-receipt.json");
	});
});
