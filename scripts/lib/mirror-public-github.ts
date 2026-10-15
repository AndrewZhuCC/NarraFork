import { spawn } from "node:child_process";
import { isValidGitHubRepository } from "../../shared/github-repository";
import {
	parseUpdateIndex,
	UPDATE_INDEX_BRANCH,
	UPDATE_INDEX_FILE,
	type UpdateIndexV1,
} from "../../shared/update-index";
import { updateServerChildEnvironment } from "../../shared/update-server-child-env";
import type { GhRunner } from "./github-release";

export interface PublicMirrorAsset {
	id: number;
	name: string;
	size: number;
	digest: string;
	state: string;
}
export interface PublicMirrorRelease {
	id: number;
	tag_name: string;
	draft: boolean;
	published_at: string;
	prerelease: boolean;
	assets: PublicMirrorAsset[];
}
function boundedMirrorGh(args: string[], signal?: AbortSignal): Promise<string> {
	return new Promise((resolve, reject) => {
		signal?.throwIfAborted();
		const child = spawn("gh", args, {
			stdio: ["ignore", "pipe", "pipe"],
			env: { ...updateServerChildEnvironment(), GH_HOST: "github.com", GH_PROMPT_DISABLED: "1" },
		});
		const maximum = args.includes("Accept: application/octet-stream")
			? 64 * 1024
			: args[1].includes("/contents/notes/")
				? 1024 * 1024
				: args[1].includes("/contents/")
					? 256 * 1024
					: 2 * 1024 * 1024;
		const chunks: Buffer[] = [];
		let size = 0;
		let diagnostics = 0;
		let failure: Error | undefined;
		const stop = () => {
			failure ??= new Error("Mirror GitHub request failed (bounds, timeout or cancellation)");
			child.kill("SIGKILL");
		};
		const timer = setTimeout(stop, 30_000);
		const cleanup = () => {
			clearTimeout(timer);
			signal?.removeEventListener("abort", stop);
		};
		signal?.addEventListener("abort", stop, { once: true });
		child.stdout.on("data", (chunk: Buffer) => {
			size += chunk.length;
			if (size > maximum) stop();
			else chunks.push(chunk);
		});
		child.stderr.on("data", (chunk: Buffer) => {
			diagnostics += chunk.length;
			if (diagnostics > 64 * 1024) stop();
		});
		child.once("error", () => {
			cleanup();
			reject(new Error("Mirror GitHub child failed"));
		});
		child.once("close", (code) => {
			cleanup();
			if (failure || code !== 0) reject(failure ?? new Error("Mirror GitHub query failed"));
			else {
				try {
					resolve(new TextDecoder("utf-8", { fatal: true }).decode(Buffer.concat(chunks)));
				} catch {
					reject(new Error("Mirror GitHub returned invalid text"));
				}
			}
		});
	});
}
export function mirrorGhRunner(run?: GhRunner, signal?: AbortSignal): GhRunner {
	return async (args) => {
		signal?.throwIfAborted();
		if (
			args[0] !== "api" ||
			args.some((a) => /^(?:--method(?:=|$)|--input(?:=|$)|-X|-f|-F)/.test(a))
		)
			throw new Error("Mirror GitHub transport is read-only");
		const result = await (run ? run(args) : boundedMirrorGh(args, signal));
		signal?.throwIfAborted();
		if (Buffer.byteLength(result) > 2 * 1024 * 1024)
			throw new Error("Mirror GitHub response budget exceeded");
		return result;
	};
}
export async function mirrorApi<T>(run: GhRunner, repository: string, path: string): Promise<T> {
	if (!isValidGitHubRepository(repository)) throw new Error("Invalid mirror GitHub repository");
	return JSON.parse(await run(["api", `repos/${repository}/${path}`])) as T;
}
export async function publicTagCommit(
	run: GhRunner,
	repository: string,
	tag: string,
): Promise<string> {
	let result = await mirrorApi<{ ref: string; object: { type: string; sha: string } }>(
		run,
		repository,
		`git/ref/tags/${encodeURIComponent(tag)}`,
	);
	if (result.ref !== `refs/tags/${tag}`) throw new Error("Mirror source tag mismatch");
	for (let i = 0; i < 5; i++) {
		if (!/^[a-f0-9]{40}$/.test(result.object.sha)) throw new Error("Invalid mirror source commit");
		if (result.object.type === "commit") return result.object.sha;
		if (result.object.type !== "tag") break;
		result = await mirrorApi(run, repository, `git/tags/${result.object.sha}`);
	}
	throw new Error("Mirror source tag is not a bounded commit reference");
}
export async function currentPublicRelease(
	run: GhRunner,
	repository: string,
	tag: string,
): Promise<PublicMirrorRelease> {
	const release = await mirrorApi<PublicMirrorRelease>(
		run,
		repository,
		`releases/tags/${encodeURIComponent(tag)}`,
	);
	if (
		!Number.isSafeInteger(release.id) ||
		release.id <= 0 ||
		release.tag_name !== tag ||
		release.draft !== false ||
		!release.published_at ||
		!Number.isFinite(Date.parse(release.published_at))
	)
		throw new Error(
			"Mirror requires an already-public GitHub Release; publish through the primary workflow first",
		);
	// Do not trust the embedded Release assets array: it can be paginated/truncated.
	const assets: PublicMirrorAsset[] = [];
	for (let page = 1; page <= 3; page++) {
		const entries = await mirrorApi<PublicMirrorAsset[]>(
			run,
			repository,
			`releases/${release.id}/assets?per_page=100&page=${page}`,
		);
		if (!Array.isArray(entries) || entries.length > 100)
			throw new Error("Invalid public asset page");
		assets.push(...entries);
		if (assets.length > 200) throw new Error("Public asset count exceeds mirror budget");
		if (entries.length < 100) break;
		if (page === 3) throw new Error("Truncated public asset list");
	}
	if (
		new Set(assets.map((a) => a.name)).size !== assets.length ||
		new Set(assets.map((a) => a.id)).size !== assets.length
	)
		throw new Error("Duplicate public asset identity");
	for (const asset of assets) {
		if (
			!Number.isSafeInteger(asset.id) ||
			asset.id <= 0 ||
			typeof asset.name !== "string" ||
			!/^[A-Za-z0-9._+-]{1,255}$/.test(asset.name) ||
			!Number.isSafeInteger(asset.size) ||
			asset.size <= 0 ||
			asset.state !== "uploaded" ||
			!/^sha256:[a-f0-9]{64}$/.test(asset.digest ?? "")
		)
			throw new Error("Public GitHub asset lacks trusted digest/identity");
	}
	// GitHub's asset response also contains mutable counters, labels and API URLs.
	// Compare only the strictly validated publication/byte identity: our own downloads
	// can update download_count while a long mirror is still staging unchanged bytes.
	return {
		...release,
		assets: assets.map(({ id, name, size, digest, state }) => ({ id, name, size, digest, state })),
	};
}
export function matchPublicAsset(
	release: PublicMirrorRelease,
	expected: { name: string; size: number; sha256: string },
): PublicMirrorAsset {
	const actual = release.assets.find((a) => a.name === expected.name);
	if (!actual || actual.size !== expected.size || actual.digest !== `sha256:${expected.sha256}`)
		throw new Error("Canonical index/public asset mismatch");
	return actual;
}
export async function readIndexSnapshot(
	run: GhRunner,
	repository: string,
): Promise<{ indexCommit: string; index: UpdateIndexV1 }> {
	for (let attempt = 0; attempt < 3; attempt++) {
		const readRef = () =>
			mirrorApi<{ object: { sha: string } }>(
				run,
				repository,
				`git/ref/heads/${UPDATE_INDEX_BRANCH}`,
			);
		const before = (await readRef()).object.sha;
		if (!/^[a-f0-9]{40}$/.test(before)) throw new Error("Invalid canonical index commit");
		const raw = await run([
			"api",
			`repos/${repository}/contents/${UPDATE_INDEX_FILE}?ref=${before}`,
			"-H",
			"Accept: application/vnd.github.raw+json",
		]);
		if (Buffer.byteLength(raw) > 256 * 1024) throw new Error("Canonical index exceeds 256KiB");
		const index = parseUpdateIndex(JSON.parse(raw), repository);
		if ((await readRef()).object.sha === before) return { indexCommit: before, index };
	}
	throw new Error("Canonical GitHub index moved repeatedly");
}
export async function readIndexNotes(
	run: GhRunner,
	repository: string,
	indexCommit: string,
	notes: { path: string; size: number },
): Promise<string> {
	if (notes.size > 1024 * 1024) throw new Error("Mirror notes budget exceeded");
	const raw = await run([
		"api",
		`repos/${repository}/contents/${notes.path}?ref=${indexCommit}`,
		"-H",
		"Accept: application/vnd.github.raw+json",
	]);
	if (Buffer.byteLength(raw) !== notes.size) throw new Error("Canonical notes size mismatch");
	return raw;
}
