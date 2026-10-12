import { afterEach, expect, test } from "bun:test";
import { createHash } from "node:crypto";
import {
	existsSync,
	mkdirSync,
	mkdtempSync,
	readdirSync,
	readFileSync,
	rmSync,
	symlinkSync,
	writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import type { MainMirrorSeal } from "../../scripts/lib/update-server-main-mirror";
import { buildImportManifest, type ImportFile } from "../../scripts/publish-123-distribution";

const roots: string[] = [];
afterEach(() => {
	for (const root of roots.splice(0)) rmSync(root, { recursive: true, force: true });
});
const platforms = {
	"linux-x64": "linux-x64",
	"linux-x64-baseline": "linux-x64-baseline",
	"linux-arm64": "linux-arm64",
	"darwin-x64": "macos-x64",
	"darwin-arm64": "macos-arm64",
	"win-x64": "windows-x64.exe",
	"win-x64-baseline": "windows-x64-baseline.exe",
	"win-arm64": "windows-arm64.exe",
};
const hash = (data: string | Buffer) => createHash("sha256").update(data).digest("hex");
function fixture() {
	const root = mkdtempSync(join(tmpdir(), "nf-patch-import-"));
	roots.push(root);
	const cloud = join(root, "cloud");
	const data = join(root, "data");
	const releaseRoot = join(data, "products/narrafork/releases");
	mkdirSync(cloud);
	mkdirSync(join(releaseRoot, "0.9.0"), { recursive: true });
	const sha512 = createHash("sha512").update("target").digest("base64");
	const oldHash = createHash("sha512").update("base").digest("base64");
	const release = {
		version: "0.9.1",
		channel: "stable",
		releaseDate: "2026-01-01T00:00:00Z",
		releaseNotes: { en: "notes", "zh-CN": "说明" },
		platforms: {} as Record<
			string,
			{
				filename: string;
				size: number;
				sha512: string;
				hasZstdPatch: boolean;
				zstdPatchFromVersion: string;
				zstdPatchFromVersions: string[];
			}
		>,
	};
	const files: {
		name: string;
		role: string;
		platform: string | null;
		size: number;
		sha256: string;
	}[] = [];
	const add = (name: string, role: string, platform: string | null, bytes: string) => {
		writeFileSync(join(cloud, name), bytes);
		files.push({ name, role, platform, size: Buffer.byteLength(bytes), sha256: hash(bytes) });
	};
	for (const [platform, suffix] of Object.entries(platforms)) {
		const filename = `narrafork-0.9.1-${suffix}`;
		release.platforms[platform] = {
			filename,
			size: 6,
			sha512,
			hasZstdPatch: true,
			zstdPatchFromVersion: "0.9.0",
			zstdPatchFromVersions: ["0.9.0"],
		};
		const patch = `${filename}.from-0.9.0.zstd-patch`;
		add(patch, "patch", platform, "patch");
		add(
			`${patch}.meta.json`,
			"patch-metadata",
			platform,
			JSON.stringify({
				fromVersion: "0.9.0",
				toVersion: "0.9.1",
				oldFileSize: 4,
				oldFileSha512: oldHash,
				newFileSize: 6,
				newFileSha512: sha512,
				stableEnd: 0,
				newTailSize: 6,
				patchSize: 5,
				mode: "patch-from",
			}),
		);
	}
	writeFileSync(
		join(releaseRoot, "0.9.0/meta.json"),
		JSON.stringify({
			version: "0.9.0",
			platforms: Object.fromEntries(
				Object.keys(platforms).map((p) => [p, { size: 4, sha512: oldHash }]),
			),
		}),
	);
	add("meta.json", "release-metadata", null, JSON.stringify(release));
	const manifest = {
		schemaVersion: 1,
		repository: "NarraFork/NarraFork",
		version: "0.9.1",
		tag: "v0.9.1",
		commit: "a".repeat(40),
		channel: "stable",
		buildRunId: 123,
		buildRunAttempt: 1,
		bridgeArtifactId: 456,
		bridgeArtifactSha256: "b".repeat(64),
		remoteDirectory: "narrafork-ci-relay/NarraFork-NarraFork/v0.9.1/123-1",
		files,
		release,
	};
	const fake = join(root, "rclone");
	const log = join(root, "calls.jsonl");
	writeFileSync(
		fake,
		`#!/usr/bin/env python3\nimport json,os,sys,shutil,time\nfrom pathlib import Path\na=sys.argv[1:]\nwith open(os.environ['CALL_LOG'],'a') as f: f.write(json.dumps(a)+'\\n')\nassert a[0]=='copyto'\nname=a[1].rsplit('/',1)[1]\nif os.environ.get('FAIL_NAME')==name: sys.exit(7)\nif os.environ.get('SLOW_NAME')==name: time.sleep(10)\nshutil.copyfile(Path(os.environ['CLOUD'])/name,a[2])\n`,
		{ mode: 0o700 },
	);
	let runCount = 0;
	const run = async (env: Record<string, string> = {}, extra: string[] = []) => {
		writeFileSync(join(cloud, "import-manifest.json"), JSON.stringify(manifest));
		const receipt = join(root, `receipt-${++runCount}.json`);
		const proc = Bun.spawn(
			[
				"python3",
				resolve("scripts/import-123-patches.py"),
				"--version",
				"0.9.1",
				"--remote-dir",
				manifest.remoteDirectory.replace(/\/$/, ""),
				"--manifest-sha256",
				hash(JSON.stringify(manifest)),
				"--data-dir",
				data,
				"--receipt",
				receipt,
				"--rclone",
				fake,
				...extra,
			],
			{
				env: { ...process.env, CLOUD: cloud, CALL_LOG: log, ...env },
				stdout: "pipe",
				stderr: "pipe",
			},
		);
		const cancellation = env.CANCEL_IMPORT
			? setTimeout(() => proc.kill("SIGTERM"), 200)
			: undefined;
		const code = await proc.exited;
		if (cancellation) clearTimeout(cancellation);
		return {
			code,
			receipt: JSON.parse(readFileSync(receipt, "utf8")),
			stderr: await new Response(proc.stderr).text(),
		};
	};
	const calls = () =>
		readFileSync(log, "utf8")
			.trim()
			.split("\n")
			.map((line) => JSON.parse(line) as string[]);
	return {
		root,
		cloud,
		data,
		releaseRoot,
		manifest,
		run,
		calls,
		final: join(releaseRoot, "0.9.1"),
	};
}

test("real rclone subprocess imports only pinned patch pairs, then strictly reuses", async () => {
	const f = fixture();
	expect((await f.run()).code).toBe(0);
	expect(f.calls()).toHaveLength(18);
	for (const call of f.calls()) {
		expect(call[0]).toBe("copyto");
		expect(call[1]).toMatch(/(?:import-manifest\.json|meta\.json|zstd-patch)$/);
	}
	expect(readdirSync(f.final).sort()).toEqual(["meta.json", ...Object.keys(platforms)].sort());
	const result = await f.run();
	expect(result.receipt.patchOnly).toBe(true);
	expect(result.receipt.binariesDownloaded).toBe(0);
	expect(result.code).toBe(0);
	expect(f.calls()).toHaveLength(19);
});

test("real publisher manifest imports with its canonical single trailing slash", async () => {
	const f = fixture();
	const seal: MainMirrorSeal = {
		schemaVersion: 1,
		product: "narrafork",
		serverUrl: "https://unused.invalid",
		repository: f.manifest.repository,
		commit: f.manifest.commit,
		workflowCommit: f.manifest.commit,
		tag: f.manifest.tag,
		version: f.manifest.version,
		channel: "beta",
		runId: f.manifest.buildRunId,
		runAttempt: f.manifest.buildRunAttempt,
		manifestSha256: "d".repeat(64),
		releaseNotes: f.manifest.release.releaseNotes,
		platforms: [],
	};
	const exported = buildImportManifest(
		seal,
		{ ...f.manifest.release, channel: "stable" },
		f.manifest.files.map((file) => ({ ...file, role: file.role as ImportFile["role"] })),
		{
			bridgeArtifactId: f.manifest.bridgeArtifactId,
			bridgeArtifactSha256: f.manifest.bridgeArtifactSha256,
		},
	);
	expect(exported.remoteDirectory).toBe(`${f.manifest.remoteDirectory}/`);
	Object.assign(f.manifest, exported);
	const result = await f.run();
	expect(result.code).toBe(0);
	expect(result.receipt.remoteDirectory).toBe(exported.remoteDirectory);
	expect(f.calls()).toHaveLength(18);
	for (const call of f.calls()) expect(call[1]).not.toContain("//");
	expect(JSON.parse(readFileSync(join(f.final, "meta.json"), "utf8"))).toEqual(exported.release);
	for (const [platform, identity] of Object.entries(exported.release.platforms)) {
		expect(readdirSync(join(f.final, platform)).sort()).toEqual([
			`${identity.filename}.from-0.9.0.zstd-patch`,
			`${identity.filename}.from-0.9.0.zstd-patch.meta.json`,
		]);
	}
});

test("remote directory repeated slash or traversal is rejected", async () => {
	for (const suffix of ["//", "/../", "/extra"]) {
		const f = fixture();
		f.manifest.remoteDirectory += suffix;
		// Keep the CLI canonical: only the pinned manifest carries the bad directory.
		const result = await f.run({}, [
			"--remote-dir",
			"narrafork-ci-relay/NarraFork-NarraFork/v0.9.1/123-1",
		]);
		expect(result.code).toBe(1);
		expect(f.calls()).toHaveLength(1);
		expect(existsSync(f.final)).toBe(false);
	}
});

test("canonical binary sidecar metadata is permitted, never its full binary", async () => {
	const f = fixture();
	const platform = "linux-x64";
	const identity = f.manifest.release.platforms[platform];
	const name = `${identity.filename}.metadata.json`;
	const bytes = JSON.stringify({
		name: identity.filename,
		platform,
		target: "bun-linux-x64",
		version: "0.9.1",
		commit: "a".repeat(40),
		repository: "NarraFork/NarraFork",
		buildDate: "2026-01-01T00:00:00Z",
		size: identity.size,
		sha512: identity.sha512,
		sha256: "c".repeat(64),
	});
	writeFileSync(join(f.cloud, name), bytes);
	f.manifest.files.push({
		name,
		role: "binary-metadata",
		platform,
		size: Buffer.byteLength(bytes),
		sha256: hash(bytes),
	});
	expect((await f.run()).code).toBe(0);
	expect(readFileSync(join(f.final, platform, name), "utf8")).toBe(bytes);
	expect(existsSync(join(f.final, platform, identity.filename))).toBe(false);
});

test("full binary injection and unknown fields fail before any asset download", async () => {
	for (const variant of ["binary", "field"]) {
		const f = fixture();
		if (variant === "binary")
			f.manifest.files.push({
				name: "narrafork-0.9.1-linux-x64",
				role: "binary",
				platform: "linux-x64",
				size: 3,
				sha256: hash("bad"),
			});
		else Object.assign(f.manifest, { downloadUrl: "https://unsafe.invalid" });
		expect((await f.run()).code).toBe(1);
		expect(f.calls()).toHaveLength(1);
		expect(existsSync(f.final)).toBe(false);
	}
});

test("manifest pin, file hashes, versions and old-base identities are enforced", async () => {
	for (const variant of ["pin", "hash", "version", "base", "release"]) {
		const f = fixture();
		let extra: string[] = [];
		if (variant === "pin") extra = ["--manifest-sha256", "0".repeat(64)];
		if (variant === "hash") f.manifest.files[0].sha256 = "0".repeat(64);
		if (variant === "version") f.manifest.version = "0.9.2";
		if (variant === "base") writeFileSync(join(f.releaseRoot, "0.9.0/meta.json"), "{}");
		if (variant === "release") {
			const file = f.manifest.files.find((file) => file.name === "meta.json");
			if (!file) throw new Error("fixture missing release");
			writeFileSync(join(f.cloud, "meta.json"), "{}");
			file.size = 2;
			file.sha256 = hash("{}");
		}
		expect((await f.run({}, extra)).code).toBe(1);
		expect(existsSync(join(f.final, "meta.json"))).toBe(false);
	}
});

test("partial downloads, timeouts and extra existing binaries never publish or overwrite", async () => {
	for (const variant of ["partial", "timeout", "cancel", "conflict", "symlink"]) {
		const f = fixture();
		const name = f.manifest.files[2].name;
		if (variant === "conflict") {
			expect((await f.run()).code).toBe(0);
			writeFileSync(join(f.final, "narrafork-full"), "keep");
		}
		if (variant === "symlink") symlinkSync(f.cloud, join(f.releaseRoot, "0.9.1"));
		const result = await f.run(
			variant === "partial"
				? { FAIL_NAME: name }
				: variant === "timeout"
					? { SLOW_NAME: name }
					: variant === "cancel"
						? { SLOW_NAME: "import-manifest.json", CANCEL_IMPORT: "1" }
						: {},
			variant === "timeout" ? ["--timeout-seconds", "0.4"] : [],
		);
		expect(result.code).toBe(1);
		if (variant === "conflict")
			expect(readFileSync(join(f.final, "narrafork-full"), "utf8")).toBe("keep");
		else if (variant !== "symlink") expect(existsSync(f.final)).toBe(false);
		expect(
			readdirSync(f.releaseRoot).filter((name) => name.startsWith(".patch-import-")),
		).toHaveLength(0);
	}
});
