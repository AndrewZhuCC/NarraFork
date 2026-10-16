/** Read-only provenance guard for local GitHub publication of existing patches. */
import { open, opendir } from "node:fs/promises";
import { join } from "node:path";
import { parseReleasePatchName, validateReleasePatchMetadata } from "../../shared/release-patch";
import type { UpdateIndexRelease } from "../../shared/update-index";
import { type GhRunner, runGh, validateGitHubRepository } from "./github-release";
import { preparePublishedUpdateIndexRelease } from "./update-index-github";

export async function validatePublishedPatchSources(options: {
	distDir: string;
	version: string;
	repository: string;
	platformSuffixes: ReadonlyMap<string, string>;
	dryRun?: boolean;
	signal?: AbortSignal;
	run?: GhRunner;
}): Promise<void> {
	validateGitHubRepository(options.repository);
	const repository = options.repository.toLowerCase();
	const signal = AbortSignal.any([
		AbortSignal.timeout(5 * 60_000),
		...(options.signal ? [options.signal] : []),
	]);
	const run: GhRunner = async (args) => {
		signal.throwIfAborted();
		const result = await (options.run ?? runGh)(args, {
			signal,
			timeoutMs: 30_000,
			maxOutputBytes: 2 * 1024 * 1024,
		});
		signal.throwIfAborted();
		if (Buffer.byteLength(result) > 2 * 1024 * 1024)
			throw new Error("Patch source GitHub output limit");
		return result;
	};
	const bases = new Map<string, Promise<UpdateIndexRelease>>();
	async function publicBase(version: string): Promise<UpdateIndexRelease> {
		let result = bases.get(version);
		if (!result) {
			result = (async () => {
				const tag = `v${version}`;
				let ref = JSON.parse(
					await run(["api", `repos/${repository}/git/ref/tags/${encodeURIComponent(tag)}`]),
				) as { ref?: string; object: { type: string; sha: string } };
				if (ref.ref !== `refs/tags/${tag}`) throw new Error("Patch source tag ref mismatch");
				for (let depth = 0; depth < 5; depth++) {
					if (!/^[a-f0-9]{40}$/.test(ref.object?.sha ?? ""))
						throw new Error("Invalid patch source commit");
					if (ref.object.type === "commit")
						return (
							await preparePublishedUpdateIndexRelease({
								repository,
								version,
								commit: ref.object.sha,
								run,
								signal,
							})
						).release;
					if (ref.object.type !== "tag") throw new Error("Patch source is not a Git commit");
					ref = JSON.parse(await run(["api", `repos/${repository}/git/tags/${ref.object.sha}`]));
				}
				throw new Error("Patch source annotated tag depth exceeded");
			})();
			bases.set(version, result);
		}
		return result;
	}
	let directory: Awaited<ReturnType<typeof opendir>>;
	try {
		directory = await opendir(options.distDir);
	} catch (error) {
		if ((error as NodeJS.ErrnoException).code === "ENOENT") return;
		throw error;
	}
	let entries = 0;
	let pairs = 0;
	for await (const entry of directory) {
		signal.throwIfAborted();
		if (++entries > 4096) throw new Error("Patch source dist entry limit");
		for (const [platform, suffix] of options.platformSuffixes) {
			const filename = `narrafork-${options.version}-${suffix}`;
			if (!entry.name.endsWith(".meta.json")) continue;
			const parsed = parseReleasePatchName(filename, entry.name.slice(0, -10));
			if (!parsed) continue;
			if (++pairs > 64) throw new Error("Patch source pair limit");
			if (options.dryRun)
				throw new Error(
					"Offline GitHub dry-run cannot verify public patch source identity; use full-only artifacts or verified Release CI",
				);
			const handle = await open(join(options.distDir, entry.name), "r");
			let raw: string;
			try {
				const stat = await handle.stat();
				if (!stat.isFile() || stat.size <= 0 || stat.size > 64 * 1024)
					throw new Error("Patch source metadata size limit");
				const buffer = Buffer.alloc(64 * 1024 + 1);
				const { bytesRead } = await handle.read(buffer, 0, buffer.length, 0);
				if (bytesRead !== stat.size) throw new Error("Patch source metadata changed/oversized");
				raw = new TextDecoder("utf-8", { fatal: true }).decode(buffer.subarray(0, bytesRead));
			} finally {
				await handle.close();
			}
			const meta = validateReleasePatchMetadata(JSON.parse(raw), {
				...parsed,
				toVersion: options.version,
			});
			const base = await publicBase(meta.fromVersion);
			const file = base.files.find((file) => file.platform === platform);
			if (
				!file ||
				file.name !== `narrafork-${meta.fromVersion}-${suffix}` ||
				file.size !== meta.oldFileSize ||
				file.sha512 !== meta.oldFileSha512
			)
				throw new Error(
					`Public GitHub patch source identity mismatch: ${platform} v${meta.fromVersion} oldFileSize/oldFileSha512`,
				);
		}
	}
}
