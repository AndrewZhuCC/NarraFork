import type { MirrorAssetDescriptor } from "../../shared/independent-update-mirror";
import { validateReleasePatchMetadata } from "../../shared/release-patch";
import { compareReleaseVersions, isValidReleaseVersion } from "../../shared/release-version";
import {
	parseUpdateNotes,
	type UpdateIndexBinary,
	type UpdateIndexPatch,
	type UpdateIndexRelease,
} from "../../shared/update-index";
import { getConfig } from "../lib/config";
import type { StorageBackend } from "../storage/types";
import type { Channel, CheckUpdateResponse, ReleaseMeta } from "../types";
import type { MirrorIngestService } from "./mirror-ingest";
import { objectPath, verifyMirrorAsset } from "./mirror-ingest-io";

type Snapshot = NonNullable<Awaited<ReturnType<MirrorIngestService["readMain"]>>>;
type Step = NonNullable<CheckUpdateResponse["patchChain"]>[number];
const unavailable = () =>
	new Error(
		"Canonical update cannot be delivered to this legacy client; download the release from GitHub",
	);

/** No second latest/cache state: every request reads the same committed canonical catalog as v3. */
export async function readV2Canonical(ingest: MirrorIngestService, product: string) {
	if (!Object.hasOwn(getConfig().mirrorRepositories ?? {}, product)) return null;
	return ingest.readMain(product);
}

export class V2CanonicalProjection {
	private readonly signal: AbortSignal;
	constructor(
		private readonly storage: StorageBackend,
		private readonly product: string,
		private readonly snapshot: Snapshot,
		signal: AbortSignal,
	) {
		this.signal = AbortSignal.any([signal, AbortSignal.timeout(5_000)]);
	}
	private asset(name: string, version: string, kind: MirrorAssetDescriptor["kind"]) {
		return this.snapshot.assets.find(
			(a) => a.name === name && a.version === version && a.kind === kind,
		);
	}
	private async small(asset: MirrorAssetDescriptor | undefined) {
		this.signal.throwIfAborted();
		if (!asset) throw unavailable();
		const bytes = await verifyMirrorAsset(this.storage, asset, this.signal);
		if (!bytes) throw unavailable();
		return JSON.parse(bytes.toString("utf8"));
	}
	private release(version: string) {
		const release = this.snapshot.index.releases.find((r) => r.version === version);
		if (!release) throw unavailable();
		return release;
	}
	private async notes(release: UpdateIndexRelease) {
		if (!release.notes) return undefined;
		const value = parseUpdateNotes(
			await this.small(this.asset(release.notes.path, release.version, "notes")),
			this.snapshot.index.repository,
			release.version,
		).notes;
		// Bounded Unicode excerpts leave ample room for all 16 chain entries under the old 1 MiB cap.
		const excerpt = (text: string) => (text.length > 2048 ? `${text.slice(0, 2048)}\n…` : text);
		return typeof value === "string"
			? excerpt(value)
			: { en: excerpt(value.en), "zh-CN": excerpt(value["zh-CN"]) };
	}
	async metadata(version: string): Promise<ReleaseMeta> {
		const release = this.release(version);
		const platforms: ReleaseMeta["platforms"] = {};
		for (const file of release.files) {
			if (!this.asset(file.metadata.name, version, "binary-sidecar")) throw unavailable();
			const bases: string[] = [];
			for (const patch of file.patches) {
				if (await this.edge(release, file, patch)) bases.push(patch.fromVersion);
			}
			platforms[file.platform] = {
				filename: file.name,
				size: file.size,
				sha512: file.sha512,
				hasZstdPatch: bases.length > 0,
				zstdPatchFromVersions: bases,
			};
		}
		return {
			version,
			channel: release.prerelease ? "beta" : "stable",
			releaseDate: release.publishedAt,
			releaseNotes: await this.notes(release),
			platforms,
		};
	}
	private async edge(
		release: UpdateIndexRelease,
		file: UpdateIndexBinary,
		patch: UpdateIndexPatch,
		currentVersion?: string,
	): Promise<Step | null> {
		this.signal.throwIfAborted();
		const sourceRelease = this.snapshot.index.releases.find((r) => r.version === patch.fromVersion);
		const source = sourceRelease?.files.find((f) => f.platform === file.platform);
		// The bounded catalog may have evicted the running client's release. Only that
		// initial edge may use its committed patch metadata as the base identity. Legacy
		// clients verify the reconstructed size/SHA512; newer ones also verify the base.
		// Never relax a retained release's platform/identity or any intermediate edge.
		const externalBase = !sourceRelease && patch.fromVersion === currentVersion;
		const payload = this.asset(patch.name, release.version, "patch");
		const metadata = this.asset(patch.metadata.name, release.version, "patch-meta");
		if (
			(!source && !externalBase) ||
			!payload ||
			!metadata ||
			(await this.storage.getFileSize(objectPath(payload.sha256))) !== payload.size
		)
			return null;
		try {
			const meta = validateReleasePatchMetadata(await this.small(metadata), {
				fromVersion: patch.fromVersion,
				toVersion: release.version,
				patchSize: patch.size,
				newFileSize: file.size,
				newFileSha512: file.sha512,
			});
			if (source && (meta.oldFileSize !== source.size || meta.oldFileSha512 !== source.sha512))
				return null;
		} catch (error) {
			this.signal.throwIfAborted();
			if (error instanceof Error) return null;
			throw error;
		}
		const base = `/api/v3/products/${encodeURIComponent(this.product)}/mirror/assets/`;
		return {
			fromVersion: patch.fromVersion,
			toVersion: release.version,
			patchSize: patch.size,
			url: base + payload.sha256,
			metaUrl: base + metadata.sha256,
		};
	}
	async latest(
		channel: Channel,
		platform: string,
		currentVersion?: string,
	): Promise<CheckUpdateResponse> {
		if (currentVersion && !isValidReleaseVersion(currentVersion)) throw unavailable();
		const version = this.snapshot.index.channels[channel];
		if (!version) throw unavailable();
		const release = this.release(version);
		const file = release.files.find((f) => f.platform === platform);
		if (!file || !this.asset(file.metadata.name, version, "binary-sidecar")) throw unavailable();
		if (currentVersion && compareReleaseVersions(version, currentVersion) <= 0)
			return { updateAvailable: false, currentVersion, version };
		if (!currentVersion) throw unavailable();
		let chain: Step[] | undefined;
		// Always prefer a verified direct edge, irrespective of catalog ordering/channel.
		for (const patch of file.patches.filter((p) => p.fromVersion === currentVersion)) {
			const step = await this.edge(release, file, patch, currentVersion);
			if (step) {
				chain = [step];
				break;
			}
		}
		if (!chain) {
			const queue: { version: string; path: Step[] }[] = [{ version: currentVersion, path: [] }];
			const visited = new Set([currentVersion]);
			// The canonical parser bounds the graph to 32 releases / 64 patches per release.
			for (let i = 0; i < queue.length && !chain; i++) {
				const node = queue[i];
				if (node.path.length >= 16) continue;
				for (const target of this.snapshot.index.releases) {
					if (visited.has(target.version) || compareReleaseVersions(target.version, version) > 0)
						continue;
					const binary = target.files.find((f) => f.platform === platform);
					if (!binary) continue;
					for (const patch of binary.patches.filter((p) => p.fromVersion === node.version)) {
						const step = await this.edge(target, binary, patch, currentVersion);
						if (!step) continue;
						const path = [...node.path, step];
						visited.add(target.version);
						if (target.version === version) chain = path;
						else queue.push({ version: target.version, path });
						break;
					}
					if (chain) break;
				}
			}
		}
		if (!chain) throw unavailable();
		const response: CheckUpdateResponse = {
			updateAvailable: true,
			currentVersion,
			version,
			platform,
			releaseDate: release.publishedAt,
			releaseNotes: await this.notes(release),
			file: { filename: file.name, size: file.size, sha512: file.sha512 },
			zstdPatch: chain.length === 1 ? chain[0] : null,
			...(chain.length > 1 ? { patchChain: chain } : {}),
		};
		if (chain.length > 1) {
			response.releaseNotesPerVersion = [];
			for (const step of chain) {
				const target = this.release(step.toVersion);
				response.releaseNotesPerVersion.push({
					version: target.version,
					releaseDate: target.publishedAt,
					releaseNotes: await this.notes(target),
				});
			}
		}
		this.signal.throwIfAborted();
		if (Buffer.byteLength(JSON.stringify(response)) > 1024 * 1024) throw unavailable();
		return response;
	}
}
