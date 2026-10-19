// Read-only verifier for explicitly pinned historical 123 artifacts. Never imported by a publisher.
import { createHash } from "node:crypto";
import { join, relative, resolve } from "node:path";
import { isValidGitHubRepository } from "../../shared/github-repository";
import {
	MAX_RELEASE_BINARY_BYTES,
	MAX_RELEASE_PATCH_BYTES,
	type ReleasePatchMetadata,
	validateReleasePatchMetadata,
} from "../../shared/release-patch";
import { isValidReleaseVersion } from "../../shared/release-version";
import { hashReleaseFile, readReleaseText } from "./ci-release-io";
import { CI_RELEASE_TARGETS, type CiReleaseManifest } from "./ci-release-types";
import { validateBaselineMetadata } from "./github-release-baseline";
import type { FileHash, UpdateServerBridgeConfig } from "./update-server-bridge-http";

interface SourceIdentity {
	version: string;
	filename: string;
	size: number;
	sha512: string;
}
export interface MainMirrorPatch {
	source: SourceIdentity;
	file: string;
	metadataFile: string;
	identity: FileHash;
	metadataIdentity: FileHash;
	metadata: ReleasePatchMetadata;
	origin: "github-bundle" | "bridge-generated";
}
export interface MainMirrorPlatform {
	platform: string;
	full: FileHash & { filename: string };
	patches: MainMirrorPatch[];
}
export interface MainMirrorSeal {
	schemaVersion: 1;
	product: "narrafork";
	serverUrl: string;
	repository: string;
	commit: string;
	workflowCommit: string;
	tag: string;
	version: string;
	channel: "stable" | "beta";
	runId: number;
	runAttempt: number;
	manifestSha256: string;
	releaseNotes: { en: string; "zh-CN": string };
	platforms: MainMirrorPlatform[];
}
export interface PreparedMainMirror {
	seal: MainMirrorSeal;
	sealSha256: string;
	bundleDir: string;
	bridgeDir: string;
}
const digest = (raw: string) => createHash("sha256").update(raw).digest("hex");
const same = (a: FileHash, b: FileHash) =>
	a.size === b.size && a.sha256 === b.sha256 && a.sha512 === b.sha512;
function path(directory: string, name: string) {
	if (!/^[a-zA-Z0-9][a-zA-Z0-9._-]{0,255}$/.test(name))
		throw new Error("Invalid historical artifact filename");
	return join(directory, name);
}
export async function verifyPreparedMainMirror(
	prepared: PreparedMainMirror,
	config: UpdateServerBridgeConfig,
	options: { signal?: AbortSignal } = {},
): Promise<void> {
	const signal = AbortSignal.any([
		AbortSignal.timeout(30 * 60_000),
		...(options.signal ? [options.signal] : []),
	]);
	const directories = [resolve(prepared.bundleDir), resolve(prepared.bridgeDir)];
	for (let i = 0; i < 2; i++) {
		const rel = relative(directories[i], directories[1 - i]);
		if (!rel || (!rel.startsWith("..") && !rel.startsWith("/")))
			throw new Error("Historical seal and bundle directories must be separate");
	}
	const seal = prepared.seal;
	const sealText = await readReleaseText(join(prepared.bridgeDir, "prepared-main-mirror.json"));
	if (
		!/^[a-f0-9]{64}$/.test(prepared.sealSha256) ||
		digest(sealText) !== prepared.sealSha256 ||
		JSON.stringify(JSON.parse(sealText)) !== JSON.stringify(seal) ||
		seal.schemaVersion !== 1 ||
		seal.product !== "narrafork" ||
		seal.serverUrl !== config.serverUrl
	)
		throw new Error("Historical seal identity mismatch");
	const manifestText = await readReleaseText(join(prepared.bundleDir, "manifest.json"));
	const manifest = JSON.parse(manifestText) as CiReleaseManifest;
	const plan = manifest.plan;
	if (
		!isValidGitHubRepository(plan.repository) ||
		!isValidReleaseVersion(plan.version) ||
		!/^[a-f0-9]{40}$/.test(plan.commit) ||
		!/^[a-f0-9]{40}$/.test(plan.workflowCommit) ||
		plan.tag !== `v${plan.version}` ||
		!["stable", "beta"].includes(plan.channel) ||
		!Number.isSafeInteger(plan.runId) ||
		plan.runId <= 0 ||
		!Number.isSafeInteger(plan.runAttempt) ||
		plan.runAttempt <= 0 ||
		typeof plan.changelog.en !== "string" ||
		typeof plan.changelog["zh-CN"] !== "string"
	)
		throw new Error("Invalid historical source plan");
	if (new Set(manifest.files.map((f) => f.name)).size !== manifest.files.length)
		throw new Error("Duplicate historical original filename");
	if (
		digest(manifestText) !== seal.manifestSha256 ||
		plan.repository !== seal.repository ||
		plan.commit !== seal.commit ||
		plan.workflowCommit !== seal.workflowCommit ||
		plan.tag !== seal.tag ||
		plan.version !== seal.version ||
		plan.channel !== seal.channel ||
		plan.runId !== seal.runId ||
		plan.runAttempt !== seal.runAttempt ||
		JSON.stringify(seal.releaseNotes) !==
			JSON.stringify({ en: plan.changelog.en, "zh-CN": plan.changelog["zh-CN"] }) ||
		!Array.isArray(seal.platforms) ||
		seal.platforms.length !== CI_RELEASE_TARGETS.length
	)
		throw new Error("Historical seal does not match original artifact");
	for (const target of CI_RELEASE_TARGETS) {
		const platform = seal.platforms.find((p) => p.platform === target.platform);
		const filename = `narrafork-${plan.version}-${target.suffix}`;
		const binary = manifest.files.find((f) => f.name === filename);
		const sidecar = manifest.files.find((f) => f.name === `${filename}.metadata.json`);
		if (
			!platform ||
			!binary ||
			!sidecar ||
			platform.full.filename !== filename ||
			!Array.isArray(platform.patches) ||
			!platform.patches.length ||
			platform.patches.length > 2
		)
			throw new Error("Historical platform coverage mismatch");
		const full = await hashReleaseFile(
			path(join(prepared.bundleDir, "dist"), filename),
			MAX_RELEASE_BINARY_BYTES,
			signal,
		);
		const metadataPath = path(join(prepared.bundleDir, "dist"), sidecar.name);
		if (
			!same(full, binary) ||
			!same(full, platform.full) ||
			!same(await hashReleaseFile(metadataPath, 64 * 1024, signal), sidecar)
		)
			throw new Error("Historical original artifact hash mismatch");
		const rawMetadata = JSON.parse(await readReleaseText(metadataPath, 64 * 1024));
		const metadata = validateBaselineMetadata(rawMetadata, {
			version: plan.version,
			platform: target.platform,
			name: filename,
			size: full.size,
			commit: plan.commit,
			repository: plan.repository,
		});
		if (!same(full, metadata)) throw new Error("Historical original sidecar hash mismatch");
		const bases = new Set<string>();
		for (const patch of platform.patches) {
			const meta = validateReleasePatchMetadata(patch.metadata, {
				toVersion: seal.version,
				newFileSize: full.size,
				newFileSha512: full.sha512,
				patchSize: patch.identity.size,
			});
			if (
				bases.has(meta.fromVersion) ||
				patch.source.version !== meta.fromVersion ||
				patch.source.size !== meta.oldFileSize ||
				patch.source.sha512 !== meta.oldFileSha512 ||
				patch.file !== `${filename}.from-${meta.fromVersion}.zstd-patch` ||
				patch.metadataFile !== `${patch.file}.meta.json`
			)
				throw new Error("Historical sealed source identity mismatch");
			bases.add(meta.fromVersion);
			const metadataPath = path(prepared.bridgeDir, patch.metadataFile);
			if (
				!same(
					await hashReleaseFile(
						path(prepared.bridgeDir, patch.file),
						MAX_RELEASE_PATCH_BYTES,
						signal,
					),
					patch.identity,
				) ||
				!same(await hashReleaseFile(metadataPath, 64 * 1024, signal), patch.metadataIdentity) ||
				JSON.stringify(
					validateReleasePatchMetadata(JSON.parse(await readReleaseText(metadataPath, 64 * 1024))),
				) !== JSON.stringify(meta)
			)
				throw new Error("Historical patch bytes changed");
		}
	}
}
