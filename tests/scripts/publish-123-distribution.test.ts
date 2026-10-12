import { afterEach, describe, expect, test } from "bun:test";
import { createHash } from "node:crypto";
import { mkdir, mkdtemp, readdir, readFile, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import {
	computeBinaryMetadataFromBuffer,
	formatMetadataJson,
} from "../../scripts/lib/binary-metadata";
import { hashReleaseFile } from "../../scripts/lib/ci-release-io";
import { CI_RELEASE_TARGETS, type CiReleaseManifest } from "../../scripts/lib/ci-release-types";
import type { MainMirrorSeal } from "../../scripts/lib/update-server-main-mirror";
import {
	flatName,
	type PrepareOptions,
	prepareDistribution,
	type RcloneRunner,
	uploadDistribution,
} from "../../scripts/publish-123-distribution";

const roots: string[] = [];
afterEach(async () => {
	await Promise.all(roots.splice(0).map((root) => rm(root, { recursive: true, force: true })));
});
const sha256 = (bytes: string | Buffer) => createHash("sha256").update(bytes).digest("hex");
async function fixture() {
	const root = await mkdtemp(join(tmpdir(), "nf-123-test-"));
	roots.push(root);
	const bundleDir = join(root, "bundle");
	const bridgeDir = join(root, "bridge");
	await mkdir(join(bundleDir, "dist"), { recursive: true });
	await mkdir(bridgeDir);
	const source: CiReleaseManifest = {
		schemaVersion: 1,
		plan: {
			schemaVersion: 1,
			repository: "NarraFork/NarraFork",
			version: "0.9.1",
			tag: "v0.9.1",
			commit: "05a12a3148b8d985a78e24ffe2d1570ae6bbf970",
			workflowCommit: "b".repeat(40),
			bunVersion: "1.4.2",
			channel: "stable",
			changelog: { version: "0.9.1", date: "2026-10-09", en: "release", "zh-CN": "发布" },
			runId: 38238815855,
			runAttempt: 1,
			baselines: [],
		},
		files: [],
		smoke: [],
	};
	const seal: MainMirrorSeal = {
		schemaVersion: 1,
		product: "narrafork",
		serverUrl: "https://private.example",
		repository: source.plan.repository,
		commit: source.plan.commit,
		workflowCommit: source.plan.workflowCommit,
		tag: source.plan.tag,
		version: source.plan.version,
		channel: "stable",
		runId: source.plan.runId,
		runAttempt: 1,
		manifestSha256: "",
		releaseNotes: { en: "release", "zh-CN": "发布" },
		platforms: [],
	};
	for (const target of CI_RELEASE_TARGETS) {
		const name = `narrafork-0.9.1-${target.suffix}`;
		const binary = Buffer.from(`original-${target.platform}`);
		const meta = computeBinaryMetadataFromBuffer(name, binary, {
			version: "0.9.1",
			platformId: target.platform,
			target: `bun-${target.target}`,
			commit: source.plan.commit,
			repository: source.plan.repository,
			buildDate: "2026-10-09T00:00:00.000Z",
		});
		await writeFile(join(bundleDir, "dist", name), binary);
		await writeFile(join(bundleDir, "dist", `${name}.metadata.json`), formatMetadataJson(meta));
		for (const filename of [name, `${name}.metadata.json`])
			source.files.push({
				name: filename,
				...(await hashReleaseFile(join(bundleDir, "dist", filename))),
			});
		const full = { filename: name, ...(await hashReleaseFile(join(bundleDir, "dist", name))) };
		const patchFile = `${name}.from-0.9.0.zstd-patch`;
		const metadata = {
			fromVersion: "0.9.0",
			toVersion: "0.9.1",
			oldFileSize: binary.length,
			oldFileSha512: full.sha512,
			stableEnd: 0,
			newTailSize: binary.length,
			patchSize: 5,
			newFileSize: binary.length,
			newFileSha512: full.sha512,
			mode: "patch-from" as const,
		};
		await writeFile(join(bridgeDir, patchFile), "patch");
		await writeFile(join(bridgeDir, `${patchFile}.meta.json`), JSON.stringify(metadata));
		seal.platforms.push({
			platform: target.platform,
			full,
			patches: [
				{
					source: {
						version: "0.9.0",
						filename: `narrafork-0.9.0-${target.suffix}`,
						size: binary.length,
						sha512: full.sha512,
					},
					file: patchFile,
					metadataFile: `${patchFile}.meta.json`,
					identity: await hashReleaseFile(join(bridgeDir, patchFile)),
					metadataIdentity: await hashReleaseFile(join(bridgeDir, `${patchFile}.meta.json`)),
					metadata,
					origin: "bridge-generated",
				},
			],
		});
	}
	const sourceText = JSON.stringify(source);
	await writeFile(join(bundleDir, "manifest.json"), sourceText);
	seal.manifestSha256 = sha256(sourceText);
	const sealText = JSON.stringify(seal);
	await writeFile(join(bridgeDir, "prepared-main-mirror.json"), sealText);
	const options: PrepareOptions = {
		bundleDir,
		bridgeDir,
		outputDir: join(root, "output"),
		version: "0.9.1",
		remote: "nf_update",
		sealSha256: sha256(sealText),
		manifestSha256: sha256(sourceText),
		bridgeArtifactId: 11746942787,
		bridgeArtifactSha256: "920b6ccec156febcb170b243136daddecb3f22c5c275738b40a85f8a6fe0735f",
	};
	return { root, source, seal, options };
}
function fakeCloud(initial = new Map<string, Buffer>()) {
	const calls: string[][] = [];
	const remote = initial;
	const run: RcloneRunner = async (args, options) => {
		options.signal.throwIfAborted();
		calls.push(args);
		if (args[0] === "lsjson") {
			const value = remote.get(args[1]);
			return value
				? { code: 0, stdout: JSON.stringify({ IsDir: false, Size: value.length }) }
				: { code: 3, stdout: "" };
		}
		if (args[1].startsWith("nf_update:")) {
			const value = remote.get(args[1]);
			if (!value) return { code: 3, stdout: "" };
			await writeFile(args[2], value, { flag: "wx", mode: 0o600 });
		} else {
			if (remote.has(args[2])) throw new Error("Mock would overwrite");
			remote.set(args[2], await readFile(args[1]));
		}
		return { code: 0, stdout: "" };
	};
	return { remote, run, calls };
}
describe("123 patch-only distribution", () => {
	test("patch digest, sidecar bytes and sealed baseline mismatch fail before staging", async () => {
		const patch = await fixture();
		await writeFile(
			join(patch.options.bridgeDir, patch.seal.platforms[0].patches[0].file),
			"wrong",
		);
		await expect(prepareDistribution(patch.options)).rejects.toThrow("bytes changed");
		const sidecar = await fixture();
		await writeFile(join(sidecar.options.bundleDir, "dist", sidecar.source.files[1].name), "{}");
		await expect(prepareDistribution(sidecar.options)).rejects.toThrow("hash mismatch");
		const base = await fixture();
		base.seal.platforms[0].patches[0].source.sha512 = createHash("sha512")
			.update("different-old-NF-binary")
			.digest("base64");
		const text = JSON.stringify(base.seal);
		await writeFile(join(base.options.bridgeDir, "prepared-main-mirror.json"), text);
		await expect(
			prepareDistribution({ ...base.options, sealSha256: sha256(text) }),
		).rejects.toThrow("source identity mismatch");
	});
	test("missing whitelist file or extra binary in relay plan is rejected before cloud access", async () => {
		const missing = await fixture();
		const first = await prepareDistribution(missing.options);
		const cloud = fakeCloud();
		first.files.splice(8, 1);
		await expect(uploadDistribution(first, { run: cloud.run })).rejects.toThrow();
		expect(cloud.calls).toHaveLength(0);
		const extra = await fixture();
		const second = await prepareDistribution(extra.options);
		second.files.splice(8, 0, {
			...second.files[0],
			name: "extra-full",
			remote: `nf_update:${second.manifest.remoteDirectory}extra-full`,
		});
		await expect(uploadDistribution(second, { run: cloud.run })).rejects.toThrow();
		expect(cloud.calls).toHaveLength(0);
	});
	test("complete exact split, original full byte identity, importer manifest last", async () => {
		const { options } = await fixture();
		const plan = await prepareDistribution(options);
		expect(plan.files).toHaveLength(34);
		expect(plan.manifest.files).toHaveLength(25);
		expect(plan.files.filter((file) => file.remote.startsWith("nf_update:0.9.1/"))).toHaveLength(8);
		expect(plan.manifest.files.every((file) => file.role !== ("binary" as string))).toBe(true);
		expect(Object.keys(plan.manifest.release.platforms).sort()).toEqual(
			CI_RELEASE_TARGETS.map((target) => target.platform).sort(),
		);
		const staged = await readdir(join(options.outputDir, "patches"));
		expect(staged).toHaveLength(26);
		expect(
			staged.some((name) =>
				CI_RELEASE_TARGETS.some((target) => name === `narrafork-0.9.1-${target.suffix}`),
			),
		).toBe(false);
		const cloud = fakeCloud();
		await uploadDistribution(plan, { run: cloud.run });
		expect([...cloud.remote.keys()]).toEqual(plan.files.map((file) => file.remote));
		const uploads = cloud.calls.filter(
			(args) => args[0] === "copyto" && !args[1].startsWith("nf_update:"),
		);
		expect(uploads.at(-1)?.[2]).toEndWith("/import-manifest.json");
		expect(
			uploads.every((args) => args.includes("--immutable") && args.includes("--max-duration")),
		).toBe(true);
		expect(sha256(cloud.remote.get(plan.files.at(-1)?.remote ?? "") ?? "")).toBe(
			plan.manifestSha256,
		);
	});
	test("existing identical remote is digest checked and reused without writes", async () => {
		const { options } = await fixture();
		const plan = await prepareDistribution(options);
		const cloud = fakeCloud();
		await uploadDistribution(plan, { run: cloud.run });
		cloud.calls.length = 0;
		await uploadDistribution(plan, { run: cloud.run });
		expect(
			cloud.calls.filter((args) => args[0] === "copyto" && !args[1].startsWith("nf_update:")),
		).toHaveLength(0);
		expect(cloud.calls.filter((args) => args[0] === "copyto")).toHaveLength(34);
	});
	test("same size conflicting remote never overwritten or final manifest uploaded", async () => {
		const { options } = await fixture();
		const plan = await prepareDistribution(options);
		const first = plan.files[0];
		const bad = Buffer.alloc(first.size, 120);
		const cloud = fakeCloud(new Map([[first.remote, bad]]));
		await expect(uploadDistribution(plan, { run: cloud.run })).rejects.toThrow("incomplete");
		expect(cloud.remote.get(first.remote)).toEqual(bad);
		expect(cloud.remote.size).toBe(1);
	});
	test("local drift and duplicate destinations fail before any cloud operation", async () => {
		const { options } = await fixture();
		const plan = await prepareDistribution(options);
		const cloud = fakeCloud();
		await writeFile(plan.files[0].local, "tampered");
		await expect(uploadDistribution(plan, { run: cloud.run })).rejects.toThrow();
		expect(cloud.calls).toHaveLength(0);
		const second = await fixture();
		const valid = await prepareDistribution(second.options);
		valid.files.splice(1, 0, valid.files[0]);
		await expect(uploadDistribution(valid, { run: cloud.run })).rejects.toThrow();
		expect(cloud.calls).toHaveLength(0);
	});
	test("upload failure or cancellation cannot publish final manifest", async () => {
		const { options } = await fixture();
		const plan = await prepareDistribution(options);
		const cloud = fakeCloud();
		const run: RcloneRunner = async (args, context) =>
			args[0] === "copyto" && !args[1].startsWith("nf_update:")
				? { code: 1, stdout: "private secret" }
				: cloud.run(args, context);
		await expect(uploadDistribution(plan, { run })).rejects.toThrow("incomplete");
		expect(cloud.remote.size).toBe(0);
		await expect(
			uploadDistribution(plan, { run: cloud.run, signal: AbortSignal.abort() }),
		).rejects.toThrow();
		expect(cloud.remote.size).toBe(0);
	});
	test("rejects unsafe flat names, symlinks, wrong pins, duplicate originals, staging overwrite", async () => {
		for (const name of ["../x", "/x", "a/b", "a\\b", "-x"]) expect(() => flatName(name)).toThrow();
		const { options } = await fixture();
		await expect(prepareDistribution({ ...options, sealSha256: "0".repeat(64) })).rejects.toThrow(
			"Seal",
		);
		await expect(
			prepareDistribution({ ...options, manifestSha256: "0".repeat(64) }),
		).rejects.toThrow("Manifest");
		await prepareDistribution(options);
		await expect(prepareDistribution(options)).rejects.toThrow();
		const linked = await fixture();
		const target = join(linked.options.bundleDir, "dist", linked.source.files[0].name);
		await rm(target);
		await symlink(join(linked.options.bundleDir, "manifest.json"), target);
		await expect(prepareDistribution(linked.options)).rejects.toThrow("Symlink");
		const duplicate = await fixture();
		duplicate.source.files.push(duplicate.source.files[0]);
		await writeFile(
			join(duplicate.options.bundleDir, "manifest.json"),
			JSON.stringify(duplicate.source),
		);
		await expect(
			prepareDistribution({ ...duplicate.options, manifestSha256: undefined }),
		).rejects.toThrow("Duplicate");
	});
	test("invalid destinations and non-private configs rejected without exposing config", async () => {
		const { options, root } = await fixture();
		const plan = await prepareDistribution(options);
		const cloud = fakeCloud();
		const config = join(root, "private-config");
		await writeFile(config, "account=secret", { mode: 0o644 });
		await expect(uploadDistribution(plan, { run: cloud.run, config })).rejects.toThrow("private");
		plan.files[0].remote = `nf_update:${plan.manifest.remoteDirectory}${plan.files[0].name}`;
		await expect(uploadDistribution(plan, { run: cloud.run })).rejects.toThrow();
		expect(cloud.calls).toHaveLength(0);
	});
});
