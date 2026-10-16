import { createHash } from "node:crypto";
import { open, unlink } from "node:fs/promises";
import { parseMirrorAvailability } from "../../shared/independent-update-mirror";
import { validateReleasePatchMetadata } from "../../shared/release-patch";
import { compareReleaseVersions } from "../../shared/release-version";
import type { UpdateSourceIdentity } from "../../shared/update-identity";
import {
	MAX_UPDATE_INDEX_BYTES,
	MAX_UPDATE_NOTES_BYTES,
	parseUpdateIndex,
	parseUpdateNotes,
	type UpdateIndexAsset,
} from "../../shared/update-index";
import {
	fetchUpdateWithTimeout,
	readUpdateJson,
	type UpdateFetchContext,
} from "../lib/net/update-fetch";
import { GithubUpdateError, validateGithubAssetUrl } from "./github-release-update";
import type { ReleaseInfo, UpdateCheckResult } from "./update-service";

function mirrorBase(identity: Extract<UpdateSourceIdentity, { source: "update-server" }>) {
	return `${identity.serverUrl}/api/v3/products/${encodeURIComponent(identity.product)}/mirror`;
}

/** Snapshot JSON is a consistency descriptor, not cryptographic proof of GitHub provenance. */
export async function checkMirrorUpdate(
	identity: Extract<UpdateSourceIdentity, { source: "update-server" }>,
	currentVersion: string,
	transport: UpdateFetchContext,
): Promise<UpdateCheckResult> {
	const signal = AbortSignal.timeout(30_000);
	const base = mirrorBase(identity);
	const response = await fetchUpdateWithTimeout(`${base}/index`, {
		timeoutMs: 10_000,
		signal,
		transport,
	});
	if (!response.ok) {
		await response.body?.cancel().catch(() => {});
		throw new GithubUpdateError(
			[404, 410].includes(response.status) ? "CANONICAL_MIRROR_REQUIRED" : "NETWORK_ERROR",
			"Independent update server requires canonical mirror API v3; upgrade it or select GitHub",
		);
	}
	const index = parseUpdateIndex(
		await readUpdateJson(response, MAX_UPDATE_INDEX_BYTES),
		identity.repository,
	);
	const result: UpdateCheckResult = {
		source: "update-server",
		repository: identity.repository,
		currentVersion,
		updateAvailable: false,
	};
	const target = index.releases.find(
		(release) => release.version === index.channels[identity.channel],
	);
	if (!target)
		return {
			...result,
			errorCode: "NO_RELEASE",
			error: "No published application releases in this channel",
		};
	result.latestVersion = target.version;
	if (compareReleaseVersions(target.version, currentVersion) <= 0) return result;
	const file = target.files.find((item) => item.platform === identity.platform);
	if (!file) throw new Error("Canonical update index does not contain this platform");
	const assetUrl = (name: string) =>
		`https://github.com/${identity.repository}/releases/download/${target.tag}/${name}`;
	const fullUrl = assetUrl(file.name);
	validateGithubAssetUrl(fullUrl, identity.repository, target.version, file.name);
	const release: ReleaseInfo = {
		source: "update-server",
		repository: identity.repository,
		sourceIdentity: identity,
		version: target.version,
		releaseDate: target.publishedAt,
		path: file.name,
		sha512: file.sha512,
		files: [{ url: file.name, size: file.size, sha512: file.sha512 }],
		notesDeferred: true,
		notesAvailable: !!target.notes,
		_github: { repository: identity.repository, downloadUrl: fullUrl },
		_mirror: { serverUrl: identity.serverUrl },
	};
	// Availability is advisory: only a current direct patch is eligible; never mirror a GitHub-only chain.
	try {
		const availabilityResponse = await fetchUpdateWithTimeout(
			`${base}/availability/${target.version}/${identity.platform}`,
			{ timeoutMs: 10_000, signal, transport },
		);
		if (!availabilityResponse.ok) {
			await availabilityResponse.body?.cancel().catch(() => {});
			throw new Error("Mirror availability unavailable");
		}
		const availability = parseMirrorAvailability(
			await readUpdateJson(availabilityResponse, MAX_UPDATE_INDEX_BYTES),
			identity.repository,
			target.version,
			identity.platform,
			index,
		);
		const available = (asset: UpdateIndexAsset) =>
			availability.assets.some(
				(item) =>
					item.name === asset.name && item.size === asset.size && item.sha256 === asset.sha256,
			);
		const patch = file.patches.find(
			(item) =>
				item.fromVersion === currentVersion &&
				item.size < file.size &&
				available(item) &&
				available(item.metadata),
		);
		if (patch) {
			const metadata = await readMirrorAsset(
				`${base}/assets/${patch.metadata.sha256}`,
				patch.metadata,
				signal,
				transport,
			);
			const meta = validateReleasePatchMetadata(
				JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(metadata)),
				{
					fromVersion: currentVersion,
					toVersion: target.version,
					patchSize: patch.size,
					newFileSize: file.size,
					newFileSha512: file.sha512,
				},
			);
			release._github = {
				repository: identity.repository,
				downloadUrl: fullUrl,
				patchChain: [
					{
						fromVersion: currentVersion,
						toVersion: target.version,
						patchSize: patch.size,
						url: assetUrl(patch.name),
						metaUrl: assetUrl(patch.metadata.name),
						sha256: patch.sha256,
						meta,
					},
				],
			};
			release._mirror = {
				serverUrl: identity.serverUrl,
				patch: {
					name: patch.name,
					size: patch.size,
					sha256: patch.sha256,
					url: `${base}/assets/${patch.sha256}`,
				},
			};
		}
	} catch {
		signal.throwIfAborted();
		// Missing or corrupt delivery hints cannot change the selected canonical target.
	}
	const patchSize = release._mirror?.patch?.size;
	return {
		...result,
		updateAvailable: true,
		releaseInfo: release,
		notesDeferred: true,
		notesAvailable: !!target.notes,
		strategy: patchSize ? "zstd" : "full",
		downloadSize: patchSize ?? file.size,
		totalSize: file.size,
		zstdPatchSize: patchSize,
	};
}

async function readMirrorAsset(
	url: string,
	asset: UpdateIndexAsset,
	signal: AbortSignal,
	transport: UpdateFetchContext,
	maximumBytes = 64 * 1024,
): Promise<Uint8Array> {
	if (asset.size > maximumBytes) throw new Error("Mirror metadata exceeds size limit");
	const response = await fetchUpdateWithTimeout(url, { timeoutMs: 10_000, signal, transport });
	if (!response.ok || !response.body) {
		await response.body?.cancel().catch(() => {});
		throw new Error("Mirror metadata unavailable");
	}
	const reader = response.body.getReader();
	const data = new Uint8Array(asset.size);
	let size = 0;
	try {
		for (;;) {
			const next = await reader.read();
			if (next.done) break;
			size += next.value.length;
			if (size > data.length) throw new Error("Mirror metadata exceeds announced size");
			data.set(next.value, size - next.value.length);
		}
		signal.throwIfAborted();
		if (size !== asset.size || createHash("sha256").update(data).digest("hex") !== asset.sha256)
			throw new Error("Mirror metadata size or digest mismatch");
		return data;
	} finally {
		await reader.cancel().catch(() => {});
	}
}

/** Optional notes use the same frozen authority and canonical byte descriptor as the binary. */
export async function getMirrorNotes(
	identity: Extract<UpdateSourceIdentity, { source: "update-server" }>,
	version: string,
	sha512: string,
	transport: UpdateFetchContext,
	parent?: AbortSignal,
) {
	const signal = AbortSignal.any([AbortSignal.timeout(30_000), ...(parent ? [parent] : [])]);
	const base = mirrorBase(identity);
	const response = await fetchUpdateWithTimeout(`${base}/index`, {
		timeoutMs: 10_000,
		signal,
		transport,
	});
	if (!response.ok) {
		await response.body?.cancel().catch(() => {});
		throw new GithubUpdateError("NETWORK_ERROR", "Mirror notes catalog unavailable");
	}
	const index = parseUpdateIndex(
		await readUpdateJson(response, MAX_UPDATE_INDEX_BYTES),
		identity.repository,
	);
	const target = index.releases.find((item) => item.version === version);
	const file = target?.files.find((item) => item.platform === identity.platform);
	if (!file || file.sha512 !== sha512)
		throw new GithubUpdateError("UPDATE_ARTIFACT_CHANGED", "Selected mirror artifact changed");
	if (!target?.notes) return { notes: undefined };
	const descriptor = {
		name: target.notes.path,
		size: target.notes.size,
		sha256: target.notes.sha256,
	};
	const bytes = await readMirrorAsset(
		`${base}/assets/${descriptor.sha256}`,
		descriptor,
		signal,
		transport,
		MAX_UPDATE_NOTES_BYTES,
	);
	const notes = parseUpdateNotes(
		JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(bytes)),
		identity.repository,
		version,
	);
	signal.throwIfAborted();
	return { notes: notes.notes };
}

/** Same-origin delivery remains strict; only canonical full transport may cross to GitHub CDN. */
export async function downloadMirrorPatch(
	release: ReleaseInfo,
	path: string,
	options: { signal: AbortSignal; onProgress?: (bytes: number) => void },
	transport: UpdateFetchContext,
): Promise<void> {
	const patch = release._mirror?.patch;
	const identity = release.sourceIdentity;
	if (
		!patch ||
		identity?.source !== "update-server" ||
		release._mirror?.serverUrl !== identity.serverUrl ||
		patch.url !== `${mirrorBase(identity)}/assets/${patch.sha256}` ||
		!/^[a-f0-9]{64}$/.test(patch.sha256) ||
		patch.size <= 0 ||
		patch.size > 512 * 1024 * 1024
	)
		throw new Error("Invalid frozen mirror patch descriptor");
	let created = false;
	const response = await fetchUpdateWithTimeout(patch.url, {
		timeoutMs: 10 * 60_000,
		signal: options.signal,
		transport,
	});
	try {
		if (!response.ok || !response.body)
			throw new Error(`Mirror patch unavailable (${response.status})`);
		const declared = response.headers.get("content-length");
		if (declared !== null && Number(declared) !== patch.size)
			throw new Error("Mirror patch Content-Length mismatch");
		const handle = await open(path, "wx", 0o600);
		created = true;
		const reader = response.body.getReader();
		let bytes = 0;
		let progressAt = 0;
		const hash = createHash("sha256");
		try {
			for (;;) {
				options.signal.throwIfAborted();
				const chunk = await reader.read();
				if (chunk.done) break;
				bytes += chunk.value.length;
				if (bytes > patch.size) throw new Error("Mirror patch exceeds announced size");
				hash.update(chunk.value);
				let offset = 0;
				while (offset < chunk.value.length) {
					const { bytesWritten } = await handle.write(
						chunk.value,
						offset,
						chunk.value.length - offset,
					);
					if (!bytesWritten) throw new Error("Mirror patch write made no progress");
					offset += bytesWritten;
				}
				if (Date.now() - progressAt >= 100 || bytes === patch.size) {
					progressAt = Date.now();
					options.onProgress?.(bytes);
				}
			}
			options.signal.throwIfAborted();
			if (bytes !== patch.size || hash.digest("hex") !== patch.sha256)
				throw new Error("Mirror patch size or SHA256 mismatch");
		} finally {
			await reader.cancel().catch(() => {});
			await handle.close();
		}
	} catch (error) {
		if (created) await unlink(path).catch(() => {});
		throw error;
	} finally {
		await response.body?.cancel().catch(() => {});
	}
}
