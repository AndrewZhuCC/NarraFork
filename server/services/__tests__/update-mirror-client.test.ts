import { afterEach, beforeEach, describe, expect, test } from "bun:test";
import { createHash } from "node:crypto";
import { mkdtemp, readdir, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { zstdCompressSync } from "node:zlib";
import type { UpdateSourceIdentity } from "../../../shared/update-identity";
import { UPDATE_INDEX_PLATFORMS, type UpdateIndexV1 } from "../../../shared/update-index";
import type { UpdateFetchContext } from "../../lib/net/update-fetch";
import { settings } from "../../lib/settings";
import { BUILD_PLATFORM } from "../../lib/version";
import {
	createGithubUpdateDeadline,
	downloadGithubUpdateToFile,
} from "../github-release-patch-apply";
import {
	downloadGithubBinaryToFile,
	readIndexedGithubBinaryMetadata,
} from "../github-release-update";
import { checkMirrorUpdate, downloadMirrorPatch, getMirrorNotes } from "../update-mirror-client";
import {
	checkForUpdate,
	downloadUpdate,
	getCurrentUpdateSourceIdentity,
	getUpdateDirectory,
	getUpdateStatus,
	isUpdateSourceCurrent,
	type ReleaseInfo,
} from "../update-service";

const hash = (bytes: Uint8Array | string, algorithm = "sha256") =>
	createHash(algorithm)
		.update(bytes)
		.digest(algorithm === "sha512" ? "base64" : "hex");
const currentVersion = "1.0.0";
const version = "1.0.1";
const binary = Buffer.from("canonical target fixture".repeat(400));
const baseBinary = Buffer.from("canonical source fixture".repeat(400));
const patchBytes = zstdCompressSync(binary);
const platform =
	BUILD_PLATFORM ||
	`${process.platform === "win32" ? "win" : process.platform === "darwin" ? "darwin" : "linux"}-${process.arch === "arm64" ? "arm64" : "x64"}`;
const name = `narrafork-${version}-${UPDATE_INDEX_PLATFORMS[platform]}`;
const patchName = `${name}.from-${currentVersion}.zstd-patch`;
const meta = {
	fromVersion: currentVersion,
	toVersion: version,
	oldFileSize: baseBinary.length,
	oldFileSha512: hash(baseBinary, "sha512"),
	newFileSize: binary.length,
	newFileSha512: hash(binary, "sha512"),
	patchSize: patchBytes.length,
	stableEnd: 0,
	newTailSize: binary.length,
	mode: "dictionary",
};
const identity: Extract<UpdateSourceIdentity, { source: "update-server" }> = {
	source: "update-server",
	repository: "fixture/fork",
	serverUrl: "https://mirror.example",
	product: "narrafork",
	channel: "stable",
	platform,
};
const notesBytes = Buffer.from(
	JSON.stringify({
		schemaVersion: 1,
		repository: identity.repository,
		version,
		notes: { en: "Canonical fixture notes", "zh-CN": "Canonical fixture notes" },
	}),
);
const baseUrl = `${identity.serverUrl}/api/v3/products/narrafork/mirror`;
const fullUrl = `https://github.com/${identity.repository}/releases/download/v${version}/${name}`;
const originalUpdate = structuredClone(settings.update);
const originalFetch = globalThis.fetch;
let directory: string;
let basePath: string;
let outputPath: string;
let calls: string[];
let mode: string;
let transport: UpdateFetchContext;
let binaryMetadataChanges: Record<string, unknown>;

function binarySidecar() {
	return Buffer.from(
		JSON.stringify({
			name,
			version,
			platform,
			size: binary.length,
			sha256: hash(binary),
			sha512: hash(binary, "sha512"),
			commit: "a".repeat(40),
			recoveryManifestVersions: [1, 2, 3],
			...binaryMetadataChanges,
		}),
	);
}

function fixturePatch() {
	return mode === "rebuild" ? zstdCompressSync(Buffer.alloc(binary.length)) : patchBytes;
}
function fixtureMetadata() {
	return Buffer.from(JSON.stringify({ ...meta, patchSize: fixturePatch().length }));
}

function index(): UpdateIndexV1 {
	return {
		schemaVersion: 1,
		repository: identity.repository,
		generation: 1,
		generatedAt: "2026-10-14T00:00:00Z",
		channels: { stable: version, beta: version },
		releases: [
			{
				version,
				tag: `v${version}`,
				commit: "a".repeat(40),
				prerelease: false,
				publishedAt: "2026-10-14T00:00:00Z",
				notes: {
					path: `notes/${version}-${hash(notesBytes)}.json`,
					size: notesBytes.length,
					sha256: hash(notesBytes),
				},
				files: [
					{
						name,
						size: binary.length,
						sha256: hash(binary),
						sha512: hash(binary, "sha512"),
						platform: identity.platform,
						metadata: {
							name: `${name}.metadata.json`,
							size: binarySidecar().length,
							sha256: hash(binarySidecar()),
						},
						patches: [
							{
								name: patchName,
								size: fixturePatch().length,
								sha256: hash(fixturePatch()),
								fromVersion: currentVersion,
								metadata: {
									name: `${patchName}.meta.json`,
									size: fixtureMetadata().length,
									sha256: hash(fixtureMetadata()),
								},
							},
						],
					},
				],
			},
		],
	};
}
function streamed(bytes: Uint8Array) {
	return new Response(
		new ReadableStream<Uint8Array>({
			start(controller) {
				controller.enqueue(bytes.subarray(0, Math.max(1, bytes.length >> 1)));
				controller.enqueue(bytes.subarray(Math.max(1, bytes.length >> 1)));
				controller.close();
			},
		}),
		{ headers: { "Content-Length": String(bytes.length) } },
	);
}
beforeEach(async () => {
	directory = await mkdtemp(join(tmpdir(), "nf-canonical-mirror-"));
	basePath = join(directory, "current");
	outputPath = join(directory, "output");
	await writeFile(basePath, baseBinary);
	calls = [];
	mode = "good";
	binaryMetadataChanges = {};
	transport = {
		key: "fixture-frozen-proxy",
		isCurrent: () => true,
		fetch: async (url, init) => {
			calls.push(url);
			expect(init?.redirect).toBe("manual");
			if (url === `${baseUrl}/index`) return Response.json(index());
			if (url === `${fullUrl}.metadata.json`) return streamed(binarySidecar());
			if (url === `${baseUrl}/assets/${hash(notesBytes)}`)
				return streamed(mode === "badnotes" ? Buffer.alloc(notesBytes.length) : notesBytes);
			if (url.startsWith(`${baseUrl}/availability/`)) {
				const file = index().releases[0].files[0];
				return Response.json({
					schemaVersion: 1,
					repository: identity.repository,
					version: mode === "wrongavailability" ? "999.0.0" : version,
					platform: identity.platform,
					assets:
						mode === "absent"
							? []
							: [file.patches[0], file.patches[0].metadata].map(({ name, size, sha256 }) => ({
									name,
									size,
									sha256,
								})),
				});
			}
			if (url === `${baseUrl}/assets/${hash(fixtureMetadata())}`)
				return streamed(
					mode === "badmeta" ? Buffer.alloc(fixtureMetadata().length) : fixtureMetadata(),
				);
			if (url === `${baseUrl}/assets/${hash(fixturePatch())}`) {
				if (mode === "404") return new Response(null, { status: 404 });
				if (mode === "redirect")
					return new Response(null, { status: 302, headers: { location: fullUrl } });
				if (mode === "corrupt") return streamed(Buffer.alloc(patchBytes.length));
				if (mode === "deadline")
					return new Promise((_resolve, reject) =>
						init?.signal?.addEventListener("abort", () => reject(init.signal?.reason), {
							once: true,
						}),
					);
				return streamed(fixturePatch());
			}
			if (url === fullUrl)
				return new Response(null, {
					status: 302,
					headers: { location: "https://release-assets.githubusercontent.com/fixture/full" },
				});
			if (url === "https://release-assets.githubusercontent.com/fixture/full")
				return streamed(binary);
			throw new Error(`Unexpected origin ${url}`);
		},
	};
});
afterEach(async () => {
	settings.update = structuredClone(originalUpdate);
	globalThis.fetch = originalFetch;
	await rm(directory, { recursive: true, force: true });
});

async function run(
	release: ReleaseInfo,
	signal = new AbortController().signal,
	cancelPatch?: () => void,
) {
	return downloadGithubUpdateToFile(
		{ release: { ...release, source: "github" }, currentVersion, basePath, outputPath, signal },
		{
			downloadPatch: (_step, path, options) =>
				downloadMirrorPatch(
					release,
					path,
					{ ...options, onProgress: () => cancelPatch?.() },
					transport,
				),
			downloadFull: (target, path, options) =>
				downloadGithubBinaryToFile(target, path, { ...options, fetcher: transport.fetch }),
			resolveZstd: async () => null,
		},
	);
}
async function checked() {
	const result = await checkMirrorUpdate(identity, currentVersion, transport);
	if (!result.releaseInfo) throw new Error("Missing mirror release");
	return result.releaseInfo;
}
async function clean(output = true) {
	expect((await readdir(directory)).sort()).toEqual(output ? ["current", "output"] : ["current"]);
	if (output) expect(await readFile(outputPath)).toEqual(binary);
}

describe("canonical mirror recovery capabilities", () => {
	test("legacy GitHub sidecars remain compatible without claiming v3", async () => {
		binaryMetadataChanges = { recoveryManifestVersions: undefined };
		expect((await checked()).recoveryManifestVersions).toEqual([1, 2]);
	});
	test("future protocol numbers do not imply v3", async () => {
		binaryMetadataChanges = { recoveryManifestVersions: [1, 2, 4] };
		expect((await checked()).recoveryManifestVersions).toEqual([1, 2, 4]);
	});
	for (const changes of [
		{ recoveryManifestVersions: null },
		{ recoveryManifestVersions: [3, 3] },
		{ recoveryManifestVersions: [] },
		{ recoveryManifestVersions: [1, 2, 256] },
		{ name: "another" },
		{ version: "999.0.0" },
		{ platform: "unknown" },
		{ size: binary.length + 1 },
		{ commit: "b".repeat(40) },
		{ sha256: "b".repeat(64) },
		{ sha512: hash("another", "sha512") },
	]) {
		test(`invalid GitHub sidecar ${JSON.stringify(changes)} fails before patch selection`, async () => {
			binaryMetadataChanges = changes;
			await expect(checked()).rejects.toMatchObject({ code: "INVALID_METADATA" });
			expect(calls).toEqual([`${baseUrl}/index`, `${fullUrl}.metadata.json`]);
		});
	}
	for (const failure of ["digest", "size", "oversize", "json", "unavailable", "redirect"]) {
		test(`GitHub sidecar ${failure} cannot grant mirror v3 capabilities`, async () => {
			const original = transport.fetch;
			transport.fetch = async (url, init) => {
				if (url !== `${fullUrl}.metadata.json`) return original(url, init);
				if (failure === "unavailable") return new Response(null, { status: 404 });
				if (failure === "redirect")
					return new Response(null, {
						status: 302,
						headers: { location: "https://evil.example/sidecar" },
					});
				if (failure === "oversize") return streamed(Buffer.alloc(64 * 1024 + 1));
				if (failure === "json") return new Response("{");
				if (failure === "size") return new Response(`${binarySidecar().toString()} `);
				return new Response(binarySidecar().toString().replace("[1,2,3]", "[1,2,4]"));
			};
			await expect(checked()).rejects.toThrow();
			expect(
				calls.some((url) => url.includes("availability") || url.includes("evil.example")),
			).toBe(false);
		});
	}
	test("same-version changed binary cannot reuse previously checked capabilities", async () => {
		expect((await checked()).recoveryManifestVersions).toContain(3);
		const changedDigest = hash("different target", "sha512");
		binaryMetadataChanges = { sha512: changedDigest, recoveryManifestVersions: undefined };
		const replacement = index();
		replacement.releases[0].files[0].sha512 = changedDigest;
		const original = transport.fetch;
		transport.fetch = async (url, init) =>
			url === `${baseUrl}/index` ? Response.json(replacement) : original(url, init);
		const release = await checked();
		expect(release.sha512).toBe(changedDigest);
		expect(release.recoveryManifestVersions).toEqual([1, 2]);
	});
	test("indexed sidecar request and stream honor the caller deadline", async () => {
		const target = index().releases[0];
		for (const stage of ["headers", "body"]) {
			await expect(
				readIndexedGithubBinaryMetadata(
					{
						repository: identity.repository,
						version,
						commit: target.commit,
						binary: target.files[0],
					},
					{
						signal: AbortSignal.timeout(20),
						fetcher: async () =>
							stage === "headers"
								? new Promise(() => {})
								: new Response(
										new ReadableStream({
											start(controller) {
												controller.enqueue(Buffer.from("{"));
											},
										}),
									),
					},
				),
			).rejects.toThrow();
		}
	});
});

describe("canonical mirror actual file pipeline", () => {
	test("stable channel without a published release reports NO_RELEASE rather than up-to-date", async () => {
		const betaOnly = index();
		betaOnly.releases[0].prerelease = true;
		betaOnly.channels.stable = null;
		transport.fetch = async (url) => {
			calls.push(url);
			expect(url).toBe(`${baseUrl}/index`);
			return Response.json(betaOnly);
		};
		const result = await checkMirrorUpdate(identity, currentVersion, transport);
		expect(result).toMatchObject({
			source: "update-server",
			repository: identity.repository,
			currentVersion,
			updateAvailable: false,
			errorCode: "NO_RELEASE",
		});
		expect(result.error).toBeTruthy();
		expect(result.latestVersion).toBeUndefined();
		expect(result.releaseInfo).toBeUndefined();
		expect(calls).toEqual([`${baseUrl}/index`]);
	});
	test("available direct patch verifies metadata/base/payload/output and requests only the binary sidecar from GitHub", async () => {
		const release = await checked();
		expect(release.recoveryManifestVersions).toEqual([1, 2, 3]);
		expect(await run(release)).toBe("zstd");
		expect(calls.filter((url) => !url.startsWith(identity.serverUrl))).toEqual([
			`${fullUrl}.metadata.json`,
		]);
		await clean();
	});
	test.each([
		"absent",
		"badbase",
		"badbasesize",
		"404",
		"corrupt",
		"badmeta",
		"redirect",
		"rebuild",
		"wrongavailability",
	])("%s cleans owned temps and streams the same canonical GitHub target", async (failure) => {
		mode = failure;
		const release = await checked();
		if (failure === "badbase") await writeFile(basePath, Buffer.alloc(baseBinary.length));
		if (failure === "badbasesize") await writeFile(basePath, "wrong size");
		expect(await run(release)).toBe("full");
		expect(calls.filter((url) => url.startsWith("https://github.com/"))).toEqual([
			`${fullUrl}.metadata.json`,
			fullUrl,
		]);
		expect(calls.some((url) => url.includes("api.github.com") || url.includes("/latest"))).toBe(
			false,
		);
		if (failure.startsWith("badbase"))
			expect(calls).not.toContain(`${baseUrl}/assets/${hash(patchBytes)}`);
		await clean();
	});
	test("zstd missing falls back without changing target", async () => {
		const release = await checked();
		const chain = release._github?.patchChain;
		if (!chain) throw new Error("Missing chain");
		chain[0].meta.mode = "patch-from";
		expect(await run(release)).toBe("full");
		await clean();
	});
	test("cancellation during actual patch body read has zero full requests", async () => {
		const release = await checked();
		const controller = new AbortController();
		await expect(
			run(release, controller.signal, () => controller.abort(new Error("cancel fixture"))),
		).rejects.toThrow("cancel fixture");
		expect(calls).not.toContain(fullUrl);
		await clean(false);
	});
	test("overall deadline cancels patch request and never starts full", async () => {
		const release = await checked();
		mode = "deadline";
		const deadline = createGithubUpdateDeadline(undefined, 20);
		try {
			await expect(run(release, deadline.signal)).rejects.toThrow("overall deadline");
		} finally {
			deadline.dispose();
		}
		expect(calls).not.toContain(fullUrl);
		await clean(false);
	});
	test("optional mirror notes are byte-verified and frozen to the selected repository/version/hash", async () => {
		const release = await checked();
		expect(release.notesAvailable).toBe(true);
		calls = [];
		expect((await getMirrorNotes(identity, version, release.sha512, transport)).notes).toEqual({
			en: "Canonical fixture notes",
			"zh-CN": "Canonical fixture notes",
		});
		mode = "badnotes";
		await expect(getMirrorNotes(identity, version, release.sha512, transport)).rejects.toThrow(
			"digest mismatch",
		);
		await expect(
			getMirrorNotes(identity, version, hash(Buffer.from("another"), "sha512"), transport),
		).rejects.toThrow("artifact changed");
		expect(calls.some((url) => url.includes("github.com"))).toBe(false);
	});

	test("actual main mirror check and full download preserve prepared identity on cancellation", async () => {
		settings.update = {
			...settings.update,
			source: "update-server",
			githubRepository: identity.repository,
			serverUrl: identity.serverUrl,
			product: identity.product,
			channel: "stable",
			checkIntervalMinutes: 60,
			autoDownload: false,
			proxy: { mode: "direct" },
		};
		mode = "absent";
		globalThis.fetch = Object.assign(
			async (...args: Parameters<typeof fetch>) => transport.fetch(String(args[0]), args[1]),
			{ preconnect: originalFetch.preconnect },
		);
		const checked = await checkForUpdate({ force: true });
		if (!checked.releaseInfo) throw new Error("Missing actual mirror update");
		const downloaded = await downloadUpdate(checked.releaseInfo);
		expect(downloaded.success).toBe(true);
		expect(downloaded.preparedIdentity?.sourceIdentity).toEqual(checked.sourceIdentity ?? null);
		expect(downloaded.preparedIdentity?.recoveryManifestVersions).toEqual([1, 2, 3]);
		if (!downloaded.updatePath) throw new Error("Missing verified output");
		expect(await readFile(downloaded.updatePath)).toEqual(binary);
		const controller = new AbortController();
		const cancelled = await downloadUpdate(
			checked.releaseInfo,
			(progress) => {
				if (progress.phase === "downloading" && progress.bytesDownloaded > 0)
					controller.abort(new Error("cancel full fixture"));
			},
			{ signal: controller.signal },
		);
		expect(cancelled.success).toBe(false);
		expect(cancelled.error).toContain("cancel full fixture");
		expect((await getUpdateStatus()).preparedIdentity).toEqual(downloaded.preparedIdentity);
		expect(
			(await readdir(getUpdateDirectory())).filter(
				(path) => path.endsWith(".tmp") || path.startsWith(".github-patch-"),
			),
		).toEqual([]);
		expect(
			calls.filter((url) => url.includes("api.github.com") || url.includes("/latest")),
		).toEqual([]);
	});

	test("legacy mirror protocol and arbitrary full URL descriptors cannot become canonical releases", async () => {
		const unavailable = { ...transport, fetch: async () => new Response(null, { status: 410 }) };
		await expect(checkMirrorUpdate(identity, currentVersion, unavailable)).rejects.toThrow(
			"upgrade it or select GitHub",
		);
		const poisoned = {
			...transport,
			fetch: async () => Response.json({ ...index(), fullUrl: "https://evil.example/binary" }),
		};
		await expect(checkMirrorUpdate(identity, currentVersion, poisoned)).rejects.toThrow(
			"unknown field",
		);
		expect(calls).toEqual([]);
	});

	test("wrong configured repository is refused without an asset request", async () => {
		await expect(
			checkMirrorUpdate({ ...identity, repository: "other/repo" }, currentVersion, transport),
		).rejects.toThrow("repository mismatch");
		expect(calls).toEqual([`${baseUrl}/index`]);
	});
	test.each([
		"repository",
		"serverUrl",
		"product",
		"channel",
	])("saved %s edit invalidates checked download before any new request", async (field) => {
		settings.update = {
			...settings.update,
			source: "update-server",
			githubRepository: identity.repository,
			serverUrl: identity.serverUrl,
			product: identity.product,
			channel: "stable",
			checkIntervalMinutes: 60,
			autoDownload: false,
		};
		mode = "absent";
		const result = await checkForUpdate({ force: true, transport });
		if (!result.releaseInfo) throw new Error("Missing actual checked descriptor");
		const frozen = getCurrentUpdateSourceIdentity();
		if (field === "repository") settings.update.githubRepository = "other/repo";
		else if (field === "serverUrl") settings.update.serverUrl = "https://other.example";
		else if (field === "product") settings.update.product = "other";
		else settings.update.channel = "beta";
		expect(result.sourceIdentity).toEqual(frozen ?? undefined);
		expect(isUpdateSourceCurrent(result.releaseInfo)).toBe(false);
		const before = calls.length;
		expect((await downloadUpdate(result.releaseInfo)).success).toBe(false);
		expect(calls.length).toBe(before);
	});
});
