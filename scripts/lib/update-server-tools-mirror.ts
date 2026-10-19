import { createHash } from "node:crypto";
import {
	HELPER_MANIFEST_FILENAME,
	parseExecutorReleaseManifest,
	parseHelperManifest,
} from "../../shared/helper-distribution";
import { EXECUTOR_MANIFEST_FILENAME } from "../../shared/remote-executor";
import {
	currentPublicRelease,
	matchPublicAsset,
	mirrorGhRunner,
	publicTagCommit,
} from "./mirror-public-github";
import {
	assertServerVerified,
	MirrorRelayStage,
	type MirrorRelayStageOptions,
} from "./mirror-relay-stage";
import { UpdateServerMirrorHttp } from "./update-server-bridge-http";
import {
	IndependentMirrorError,
	type IndependentMirrorReceipt,
	type MirrorPublishedOptions,
	mirrorReceipt,
} from "./update-server-main-mirror";

/** Auxiliary mirrors contain only the canonical public manifest and its authorized license text. */
export async function mirrorPublishedToolsRelease(
	options: MirrorPublishedOptions,
): Promise<IndependentMirrorReceipt> {
	const receipt = mirrorReceipt(options);
	const signal = AbortSignal.any([
		AbortSignal.timeout(10 * 60_000),
		...(options.signal ? [options.signal] : []),
	]);
	try {
		const kind = options.kind;
		if (kind !== "helpers" && kind !== "executor") throw new Error("Invalid auxiliary mirror kind");
		if (!new RegExp(`^${kind}-v[0-9]+\\.[0-9]+\\.[0-9]+(?:-[A-Za-z0-9.-]+)?$`).test(options.tag))
			throw new Error("Exact public component tag required");
		const run = mirrorGhRunner(options.run, signal);
		const remote = await currentPublicRelease(run, options.repository, options.tag);
		const commit = await publicTagCommit(run, options.repository, options.tag);
		const name = kind === "helpers" ? HELPER_MANIFEST_FILENAME : EXECUTOR_MANIFEST_FILENAME;
		const manifestAsset = remote.assets.find((a) => a.name === name);
		if (!manifestAsset || manifestAsset.size > 64 * 1024)
			throw new Error("Missing/bounded public component manifest required");
		const read = async (asset: { id: number; size: number; digest: string }) => {
			if (asset.size > 64 * 1024) throw new Error("Auxiliary metadata exceeds 64KiB");
			const content = await run([
				"api",
				`repos/${options.repository}/releases/assets/${asset.id}`,
				"-H",
				"Accept: application/octet-stream",
			]);
			const sha256 = createHash("sha256").update(content).digest("hex");
			if (Buffer.byteLength(content) !== asset.size || asset.digest !== `sha256:${sha256}`)
				throw new Error("Public auxiliary metadata raw bytes mismatch");
			return { content, sha256 };
		};
		const manifestBytes = await read(manifestAsset);
		const raw = JSON.parse(manifestBytes.content);
		const manifest =
			kind === "helpers"
				? parseHelperManifest(raw, { repository: options.repository, commit, tag: options.tag })
				: parseExecutorReleaseManifest(raw, {
						repository: options.repository,
						commit,
						tag: options.tag,
						version: options.tag.slice("executor-v".length),
						protocolVersion: raw.manifest?.protocolVersion,
					});
		const binaries =
			"files" in manifest
				? manifest.files
				: Object.values(manifest.manifest.platforms).map((f) => ({
						name: f.filename,
						size: f.size,
						sha256: f.sha256,
					}));
		for (const binary of binaries) matchPublicAsset(remote, binary); // identity only, NEVER fetch a tool/executor binary
		const assets = [{ name, size: manifestAsset.size, ...manifestBytes }];
		for (const license of manifest.licenses) {
			const asset = matchPublicAsset(remote, license);
			assets.push({ ...license, ...(await read(asset)) });
		}
		if (assets.reduce((sum, a) => sum + a.size, 0) > (options.maxTotalBytes ?? 1024 * 1024))
			throw new Error("Auxiliary mirror total byte budget exceeded");
		if (options.dryRun) {
			receipt.status = "DRY_RUN";
			return receipt;
		}
		const stage =
			options.transport === "webdav"
				? new MirrorRelayStage(options, options.relay as MirrorRelayStageOptions, signal)
				: undefined;
		if (!stage && !options.config) {
			receipt.status = "SKIPPED_UNCONFIGURED";
			return receipt;
		}
		const root = `/api/v3/products/narrafork/mirror/tools/${kind}/${encodeURIComponent(options.tag)}`;
		const revalidate = async () => {
			if ((await publicTagCommit(run, options.repository, options.tag)) !== commit)
				throw new Error("Auxiliary source tag changed");
			const current = await currentPublicRelease(run, options.repository, options.tag);
			if (
				current.id !== remote.id ||
				JSON.stringify(current.assets) !== JSON.stringify(remote.assets)
			)
				throw new Error("Auxiliary public assets changed");
		};
		const projection = {
			schemaVersion: 1,
			repository: options.repository,
			kind,
			tag: options.tag,
			assets,
		};
		if (stage) {
			for (const asset of assets) {
				await stage.uploadObject(asset, new TextEncoder().encode(asset.content));
				receipt.uploaded.push(asset.name);
			}
			await stage.preparePayload(projection, 1024 * 1024);
			receipt.relay = await stage.publish(revalidate);
			receipt.status = "STAGED_FOR_PULL";
			return receipt;
		}
		await revalidate();
		const http =
			options.http ??
			new UpdateServerMirrorHttp(options.config as NonNullable<MirrorPublishedOptions["config"]>);
		const result = await http.postJson(root, projection, { maxBytes: 1024 * 1024, signal });
		assertServerVerified(
			result,
			{ repository: options.repository, kind, tag: options.tag },
			assets,
		);
		receipt.uploaded = assets.map((a) => a.name);
		receipt.verified = assets.map((a) => a.name);
		receipt.snapshotCommitted = true;
		receipt.status = "METADATA_ONLY";
		return receipt;
	} catch (error) {
		receipt.failureCode = signal.aborted
			? "CANCELLED"
			: error instanceof Error && error.message.includes("upgrade the update server")
				? "SERVER_RECEIPT_REQUIRED"
				: "MIRROR_FAILED";
		throw new IndependentMirrorError(receipt);
	}
}
/** @deprecated A pre-publication bundle/old tool alias never authorizes canonical mirrors. */
export async function prepareUpdateServerToolsMirror(..._args: unknown[]): Promise<never> {
	throw new Error("prepare-bridge is retired; mirror the exact public component tag");
}
export const restoreUpdateServerToolsMirror = prepareUpdateServerToolsMirror;
export const publishUpdateServerToolsMirror = prepareUpdateServerToolsMirror;
