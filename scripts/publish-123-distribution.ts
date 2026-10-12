import { createHash, randomUUID } from "node:crypto";
import { createReadStream } from "node:fs";
import { copyFile, lstat, mkdir, writeFile } from "node:fs/promises";
import { dirname, join, resolve } from "node:path";
import type { ReleaseMeta } from "../update-server/types";
import { readReleaseText } from "./lib/ci-release-io";
import { CI_RELEASE_TARGETS, type CiReleaseManifest } from "./lib/ci-release-types";
import { type MainMirrorSeal, verifyPreparedMainMirror } from "./lib/update-server-main-mirror";

export interface ImportFile {
	name: string;
	role: "patch" | "patch-metadata" | "binary-metadata" | "release-metadata";
	platform: string | null;
	size: number;
	sha256: string;
}
export interface ImportManifest {
	schemaVersion: 1;
	repository: "NarraFork/NarraFork";
	version: string;
	tag: string;
	commit: string;
	channel: "stable";
	buildRunId: number;
	buildRunAttempt: number;
	bridgeArtifactId: number;
	bridgeArtifactSha256: string;
	remoteDirectory: string;
	files: ImportFile[];
	release: ReleaseMeta;
}
export interface UploadFile {
	name: string;
	local: string;
	remote: string;
	size: number;
	sha256: string;
}
export interface DistributionPlan {
	manifest: ImportManifest;
	manifestSha256: string;
	outputDir: string;
	files: UploadFile[];
}
export interface PrepareOptions {
	bundleDir: string;
	bridgeDir: string;
	outputDir: string;
	version: string;
	remote: string;
	sealSha256: string;
	manifestSha256?: string;
	bridgeArtifactId: number;
	bridgeArtifactSha256: string;
	signal?: AbortSignal;
}
const sha = (bytes: string | Buffer) => createHash("sha256").update(bytes).digest("hex");
function check(ok: unknown, message: string): asserts ok {
	if (!ok) throw new Error(message);
}
export function flatName(name: string): string {
	check(/^[a-zA-Z0-9][a-zA-Z0-9._-]{0,255}$/.test(name), "Unsafe filename");
	return name;
}
async function safePath(path: string): Promise<void> {
	let current = resolve(path);
	for (;;) {
		const info = await lstat(current).catch((error: NodeJS.ErrnoException) => {
			if (error.code === "ENOENT") return undefined;
			throw error;
		});
		check(!info?.isSymbolicLink(), "Symlink rejected");
		const parent = dirname(current);
		if (parent === current) return;
		current = parent;
	}
}
export async function fileIdentity(path: string, signal?: AbortSignal) {
	await safePath(path);
	const info = await lstat(path);
	check(info.isFile() && info.size <= 1024 ** 3, "Invalid or oversized file");
	const hash = createHash("sha256");
	let size = 0;
	for await (const chunk of createReadStream(path, { signal })) {
		size += chunk.length;
		check(size <= 1024 ** 3, "File grew beyond limit");
		hash.update(chunk);
	}
	check(size === info.size, "File changed during read");
	return { size, sha256: hash.digest("hex") };
}
export async function prepareDistribution(options: PrepareOptions): Promise<DistributionPlan> {
	const signal = options.signal;
	signal?.throwIfAborted();
	check(options.remote === "nf_update", "Only nf_update remote is authorized");
	check(options.version === "0.9.1", "Only approved version 0.9.1 is authorized");
	check(
		Number.isSafeInteger(options.bridgeArtifactId) && options.bridgeArtifactId > 0,
		"Invalid artifact ID",
	);
	check(
		options.bridgeArtifactId === 11746942787 &&
			options.bridgeArtifactSha256 ===
				"920b6ccec156febcb170b243136daddecb3f22c5c275738b40a85f8a6fe0735f",
		"Unapproved bridge artifact",
	);
	for (const directory of [options.bundleDir, options.bridgeDir, options.outputDir])
		await safePath(directory);
	const sealText = await readReleaseText(join(options.bridgeDir, "prepared-main-mirror.json"));
	check(sha(sealText) === options.sealSha256, "Seal digest mismatch");
	const seal = JSON.parse(sealText) as MainMirrorSeal;
	const manifestText = await readReleaseText(join(options.bundleDir, "manifest.json"));
	if (options.manifestSha256)
		check(sha(manifestText) === options.manifestSha256, "Manifest digest mismatch");
	const source = JSON.parse(manifestText) as CiReleaseManifest;
	check(
		seal.version === options.version &&
			seal.repository === "NarraFork/NarraFork" &&
			// The pinned original was built as beta and subsequently promoted to stable.
			// Validate its immutable source identity, not the current distribution channel.
			seal.channel === "beta" &&
			seal.runId === 38238815855 &&
			seal.runAttempt === 1 &&
			seal.commit === "05a12a3148b8d985a78e24ffe2d1570ae6bbf970",
		"Unapproved source identity",
	);
	for (const file of source.files)
		await safePath(join(options.bundleDir, "dist", flatName(file.name)));
	for (const platform of seal.platforms)
		for (const patch of platform.patches) {
			await safePath(join(options.bridgeDir, flatName(patch.file)));
			await safePath(join(options.bridgeDir, flatName(patch.metadataFile)));
		}
	await verifyPreparedMainMirror(
		{
			seal,
			sealSha256: options.sealSha256,
			bundleDir: options.bundleDir,
			bridgeDir: options.bridgeDir,
		},
		{ serverUrl: seal.serverUrl, token: "unused-local-verification" },
		{ signal },
	);
	const remoteDirectory = `narrafork-ci-relay/NarraFork-NarraFork/v${seal.version}/${seal.runId}-${seal.runAttempt}/`;
	const release: ReleaseMeta = {
		version: seal.version,
		channel: "stable",
		releaseDate: source.plan.changelog.date,
		releaseNotes: seal.releaseNotes,
		platforms: {},
	};
	check(Number.isFinite(Date.parse(release.releaseDate)), "Invalid release date");
	const imports: ImportFile[] = [];
	const files: UploadFile[] = [];
	const names = new Set<string>();
	const pending: { source: string; name: string; role: ImportFile["role"]; platform: string }[] =
		[];
	for (const platform of seal.platforms) {
		const full = platform.full;
		files.push({
			name: full.filename,
			local: join(options.bundleDir, "dist", full.filename),
			remote: `nf_update:${seal.version}/${full.filename}`,
			size: full.size,
			sha256: full.sha256,
		});
		const bases = platform.patches.map((patch) => patch.source.version);
		release.platforms[platform.platform] = {
			filename: full.filename,
			size: full.size,
			sha512: full.sha512,
			hasZstdPatch: true,
			zstdPatchFromVersion: bases[0],
			zstdPatchFromVersions: bases,
		};
		pending.push({
			source: join(options.bundleDir, "dist", `${full.filename}.metadata.json`),
			name: `${full.filename}.metadata.json`,
			role: "binary-metadata",
			platform: platform.platform,
		});
		for (const patch of platform.patches) {
			pending.push(
				{
					source: join(options.bridgeDir, patch.file),
					name: patch.file,
					role: "patch",
					platform: platform.platform,
				},
				{
					source: join(options.bridgeDir, patch.metadataFile),
					name: patch.metadataFile,
					role: "patch-metadata",
					platform: platform.platform,
				},
			);
		}
	}
	for (const file of pending) {
		check(!names.has(flatName(file.name)), "Duplicate filename");
		names.add(file.name);
	}
	await mkdir(options.outputDir, { mode: 0o700 }); // Exclusive: never overwrite a previous staging directory.
	const stage = join(options.outputDir, "patches");
	await mkdir(stage, { mode: 0o700 });
	for (const file of pending) {
		signal?.throwIfAborted();
		const identity = await fileIdentity(file.source, signal);
		const sealedIdentity =
			file.role === "binary-metadata"
				? source.files.find((entry) => entry.name === file.name)
				: seal.platforms
						.flatMap((platform) => platform.patches)
						.map((patch) =>
							file.role === "patch"
								? { name: patch.file, ...patch.identity }
								: { name: patch.metadataFile, ...patch.metadataIdentity },
						)
						.find((entry) => entry.name === file.name);
		check(
			sealedIdentity &&
				identity.size === sealedIdentity.size &&
				identity.sha256 === sealedIdentity.sha256,
			"Original bytes changed after verification",
		);
		const local = join(stage, file.name);
		await copyFile(file.source, local, 1);
		check(
			JSON.stringify(await fileIdentity(local, signal)) === JSON.stringify(identity),
			"Staged bytes changed",
		);
		imports.push({ name: file.name, role: file.role, platform: file.platform, ...identity });
		files.push({
			name: file.name,
			local,
			remote: `nf_update:${remoteDirectory}${file.name}`,
			...identity,
		});
	}
	const metaText = `${JSON.stringify(release, null, 2)}\n`;
	const metaLocal = join(stage, "meta.json");
	await writeFile(metaLocal, metaText, { flag: "wx", mode: 0o600 });
	const metaIdentity = { size: Buffer.byteLength(metaText), sha256: sha(metaText) };
	imports.push({ name: "meta.json", role: "release-metadata", platform: null, ...metaIdentity });
	files.push({
		name: "meta.json",
		local: metaLocal,
		remote: `nf_update:${remoteDirectory}meta.json`,
		...metaIdentity,
	});
	const manifest = buildImportManifest(seal, release, imports, options);
	const text = `${JSON.stringify(manifest, null, 2)}\n`;
	const local = join(stage, "import-manifest.json");
	await writeFile(local, text, { flag: "wx", mode: 0o600 });
	const manifestSha256 = sha(text);
	files.push({
		name: "import-manifest.json",
		local,
		remote: `nf_update:${remoteDirectory}import-manifest.json`,
		size: Buffer.byteLength(text),
		sha256: manifestSha256,
	});
	return { manifest, manifestSha256, outputDir: options.outputDir, files };
}

/** Pure deterministic manifest construction; caller must first verify the source seal. */
export function buildImportManifest(
	seal: MainMirrorSeal,
	release: ReleaseMeta,
	files: ImportFile[],
	artifact: Pick<PrepareOptions, "bridgeArtifactId" | "bridgeArtifactSha256">,
): ImportManifest {
	check(
		seal.repository === "NarraFork/NarraFork" &&
			seal.channel === "beta" &&
			release.channel === "stable",
		"Invalid manifest source",
	);
	return {
		schemaVersion: 1,
		repository: "NarraFork/NarraFork",
		version: seal.version,
		tag: seal.tag,
		commit: seal.commit,
		channel: "stable",
		buildRunId: seal.runId,
		buildRunAttempt: seal.runAttempt,
		bridgeArtifactId: artifact.bridgeArtifactId,
		bridgeArtifactSha256: artifact.bridgeArtifactSha256,
		remoteDirectory: `narrafork-ci-relay/NarraFork-NarraFork/v${seal.version}/${seal.runId}-${seal.runAttempt}/`,
		files: structuredClone(files),
		release: structuredClone(release),
	};
}

/** Pure fail-closed authorization: uploader cannot broaden the prepared file set. */
export function validateDistributionPlan(plan: DistributionPlan): void {
	const manifest = plan.manifest;
	check(
		manifest.schemaVersion === 1 &&
			manifest.repository === "NarraFork/NarraFork" &&
			manifest.version === "0.9.1" &&
			manifest.tag === "v0.9.1" &&
			manifest.channel === "stable" &&
			manifest.buildRunId === 38238815855 &&
			manifest.buildRunAttempt === 1,
		"Unapproved manifest identity",
	);
	check(
		manifest.remoteDirectory === "narrafork-ci-relay/NarraFork-NarraFork/v0.9.1/38238815855-1/",
		"Unapproved directory",
	);
	check(plan.files.at(-1)?.name === "import-manifest.json", "Manifest must be last");
	check(
		Object.keys(manifest.release.platforms).length === CI_RELEASE_TARGETS.length,
		"Incomplete platform set",
	);
	const expected = new Map<string, { size: number; sha256?: string; full: boolean }>();
	for (const target of CI_RELEASE_TARGETS) {
		const platform = manifest.release.platforms[target.platform];
		check(
			platform?.filename === `narrafork-0.9.1-${target.suffix}` &&
				platform.hasZstdPatch === true &&
				platform.zstdPatchFromVersions?.length &&
				platform.zstdPatchFromVersion === platform.zstdPatchFromVersions[0],
			"Invalid platform identity",
		);
		expected.set(platform.filename, { size: platform.size, full: true });
		const sidecar = manifest.files.filter(
			(file) => file.platform === target.platform && file.role === "binary-metadata",
		);
		check(
			sidecar.length === 1 && sidecar[0].name === `${platform.filename}.metadata.json`,
			"Invalid binary metadata coverage",
		);
		for (const base of platform.zstdPatchFromVersions) {
			check(/^\d+\.\d+\.\d+$/.test(base), "Invalid patch base");
			const name = `${platform.filename}.from-${base}.zstd-patch`;
			for (const [filename, role] of [
				[name, "patch"],
				[`${name}.meta.json`, "patch-metadata"],
			]) {
				check(
					manifest.files.filter(
						(file) =>
							file.platform === target.platform && file.name === filename && file.role === role,
					).length === 1,
					"Invalid patch coverage",
				);
			}
		}
	}
	for (const file of manifest.files) {
		flatName(file.name);
		check(
			!expected.has(file.name) &&
				Number.isSafeInteger(file.size) &&
				file.size > 0 &&
				/^[a-f0-9]{64}$/.test(file.sha256),
			"Invalid import file identity",
		);
		if (file.role === "release-metadata")
			check(file.name === "meta.json" && file.platform === null, "Invalid release metadata");
		else {
			const platform = file.platform && manifest.release.platforms[file.platform];
			check(platform, "Unknown import platform");
			const permitted =
				file.role === "binary-metadata"
					? file.name === `${platform.filename}.metadata.json`
					: platform.zstdPatchFromVersions?.some(
							(base) =>
								file.name ===
								`${platform.filename}.from-${base}.zstd-patch${file.role === "patch-metadata" ? ".meta.json" : ""}`,
						) && ["patch", "patch-metadata"].includes(file.role);
			check(permitted, "Unknown import file");
		}
		expected.set(file.name, { size: file.size, sha256: file.sha256, full: false });
	}
	check(expected.has("meta.json"), "Missing release metadata");
	expected.set("import-manifest.json", {
		size: Buffer.byteLength(`${JSON.stringify(manifest, null, 2)}\n`),
		sha256: plan.manifestSha256,
		full: false,
	});
	check(plan.files.length === expected.size, "Incomplete upload list");
	const seen = new Set<string>();
	for (const file of plan.files) {
		const identity = expected.get(flatName(file.name));
		check(identity && !seen.has(file.name), "Unexpected or duplicate upload file");
		seen.add(file.name);
		check(
			file.size === identity.size &&
				/^[a-f0-9]{64}$/.test(file.sha256) &&
				(!identity.sha256 || file.sha256 === identity.sha256),
			"Upload identity mismatch",
		);
		check(
			file.remote ===
				`nf_update:${identity.full ? "0.9.1/" : manifest.remoteDirectory}${file.name}`,
			"Invalid destination",
		);
	}
	check(
		sha(`${JSON.stringify(manifest, null, 2)}\n`) === plan.manifestSha256,
		"Manifest pin mismatch",
	);
}

export type RcloneRunner = (
	args: string[],
	options: { signal: AbortSignal; maxOutputBytes: number },
) => Promise<{ code: number; stdout: string }>;
export const runRclone: RcloneRunner = async (args, options) => {
	options.signal.throwIfAborted();
	const process = Bun.spawn(["rclone", ...args], {
		stdout: "pipe",
		stderr: "ignore",
		env: { PATH: Bun.env.PATH, HOME: Bun.env.HOME },
	});
	const abort = () => process.kill();
	options.signal.addEventListener("abort", abort, { once: true });
	try {
		options.signal.throwIfAborted();
		const reader = process.stdout.getReader();
		const chunks: Uint8Array[] = [];
		let size = 0;
		for (;;) {
			const result = await reader.read();
			if (result.done) break;
			size += result.value.length;
			if (size > options.maxOutputBytes) {
				process.kill();
				throw new Error("Rclone output limit");
			}
			chunks.push(result.value);
		}
		const code = await process.exited;
		options.signal.throwIfAborted();
		return { code, stdout: Buffer.concat(chunks).toString("utf8") };
	} catch {
		process.kill();
		await process.exited;
		throw new Error("Rclone execution failed");
	} finally {
		options.signal.removeEventListener("abort", abort);
	}
};
export async function uploadDistribution(
	plan: DistributionPlan,
	options: { config?: string; run?: RcloneRunner; signal?: AbortSignal } = {},
) {
	const signal = AbortSignal.any([
		AbortSignal.timeout(60 * 60 * 1000),
		...(options.signal ? [options.signal] : []),
	]);
	if (options.config) {
		await safePath(options.config);
		const info = await lstat(options.config);
		check(info.isFile() && (info.mode & 0o077) === 0, "Config must be private");
	}
	const run = options.run ?? runRclone;
	await safePath(plan.outputDir);
	const readbackRoot = join(plan.outputDir, `readbacks-${randomUUID()}`);
	await mkdir(readbackRoot, { mode: 0o700 });
	await mkdir(join(readbackRoot, "full"), { mode: 0o700 });
	await mkdir(join(readbackRoot, "patches"), { mode: 0o700 });
	const receipts: { name: string; remote: string; sha256: string; status: "verified" }[] = [];
	const invoke = (args: string[]) =>
		run(
			[
				...args,
				"--retries",
				"1",
				"--low-level-retries",
				"1",
				"--contimeout",
				"15s",
				"--timeout",
				"30s",
				"--max-duration",
				"10m",
				...(options.config ? ["--config", options.config] : []),
			],
			{
				signal: AbortSignal.any([signal, AbortSignal.timeout(11 * 60 * 1000)]),
				maxOutputBytes: 65536,
			},
		);
	const receipt = join(plan.outputDir, `receipt-${randomUUID()}.json`);
	try {
		validateDistributionPlan(plan);
		check(plan.files.at(-1)?.name === "import-manifest.json", "Manifest must be last");
		check(
			plan.files.filter((file) => file.name === "import-manifest.json").length === 1,
			"Duplicate manifest",
		);
		const seen = new Set<string>();
		for (const file of plan.files) {
			flatName(file.name);
			check(!seen.has(file.remote), "Duplicate destination");
			seen.add(file.remote);
			const full = Object.values(plan.manifest.release.platforms).some(
				(platform) => platform.filename === file.name,
			);
			check(
				file.remote ===
					`nf_update:${full ? `${plan.manifest.version}/` : plan.manifest.remoteDirectory}${file.name}`,
				"Invalid destination",
			);
			check(
				JSON.stringify(await fileIdentity(file.local, signal)) ===
					JSON.stringify({ size: file.size, sha256: file.sha256 }),
				"Local bytes changed",
			);
		}
		for (const file of plan.files) {
			signal.throwIfAborted();
			const probe = await invoke(["lsjson", file.remote, "--stat"]);
			if (probe.code !== 0 && probe.code !== 3 && probe.code !== 4)
				throw new Error("Remote probe failed");
			if (probe.code === 0) {
				const entry = JSON.parse(probe.stdout);
				check(entry.IsDir === false && entry.Size === file.size, "Remote conflict");
			} else {
				check(
					(await invoke(["copyto", file.local, file.remote, "--immutable"])).code === 0,
					"Upload failed",
				);
			}
			const readback = join(
				readbackRoot,
				file.remote.startsWith("nf_update:0.9.1/") ? "full" : "patches",
				file.name,
			);
			check(
				(
					await invoke([
						"copyto",
						file.remote,
						readback,
						"--immutable",
						"--max-transfer",
						String(file.size + 1),
						"--cutoff-mode",
						"immediate",
					])
				).code === 0,
				"Readback failed",
			);
			const actual = await fileIdentity(readback, signal);
			check(actual.size === file.size && actual.sha256 === file.sha256, "Remote digest conflict");
			receipts.push({
				name: file.name,
				remote: file.remote,
				sha256: file.sha256,
				status: "verified",
			});
		}
		await writeFile(
			receipt,
			JSON.stringify({ status: "verified", manifestSha256: plan.manifestSha256, files: receipts }),
			{ flag: "wx", mode: 0o600 },
		);
	} catch {
		await writeFile(
			receipt,
			JSON.stringify({ status: "partial", manifestSha256: plan.manifestSha256, files: receipts }),
			{ flag: "wx", mode: 0o600 },
		);
		throw new Error("Distribution incomplete; private receipt retained");
	}
	return receipt;
}
if (import.meta.main) {
	try {
		const args = Bun.argv.slice(2);
		const values = new Map<string, string>();
		let upload = false;
		const allowed = new Set([
			"bundle-dir",
			"bridge-dir",
			"output-dir",
			"version",
			"remote",
			"seal-sha256",
			"manifest-sha",
			"bridge-artifact-id",
			"bridge-artifact-sha256",
			"config",
		]);
		for (let index = 0; index < args.length; index++) {
			if (args[index] === "--upload") {
				check(!upload, "Duplicate upload flag");
				upload = true;
				continue;
			}
			const key = args[index].replace(/^--/, "");
			check(
				args[index].startsWith("--") &&
					allowed.has(key) &&
					!values.has(key) &&
					args[index + 1] &&
					!args[index + 1].startsWith("--"),
				"Invalid arguments",
			);
			values.set(key, args[++index]);
		}
		const required = (key: string) => {
			const value = values.get(key);
			check(value, `Missing --${key}`);
			return value;
		};
		const plan = await prepareDistribution({
			bundleDir: required("bundle-dir"),
			bridgeDir: required("bridge-dir"),
			outputDir: required("output-dir"),
			version: required("version"),
			remote: required("remote"),
			sealSha256: required("seal-sha256"),
			manifestSha256: values.get("manifest-sha"),
			bridgeArtifactId: Number(required("bridge-artifact-id")),
			bridgeArtifactSha256: required("bridge-artifact-sha256"),
		});
		console.log(
			JSON.stringify({
				manifestSha256: plan.manifestSha256,
				files: plan.files.map(({ name, remote, size, sha256 }) => ({ name, remote, size, sha256 })),
				upload,
			}),
		);
		if (upload) await uploadDistribution(plan, { config: values.get("config") });
	} catch {
		console.error("Distribution failed; no secrets logged");
		process.exitCode = 1;
	}
}
