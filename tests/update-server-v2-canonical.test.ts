import { expect, spyOn, test } from "bun:test";
import { createHash } from "node:crypto";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { computeBinaryMetadataFromBuffer } from "../scripts/lib/binary-metadata";
import { mirrorAssetsForTarget } from "../shared/independent-update-mirror";
import {
	mergeUpdateIndex,
	UPDATE_INDEX_PLATFORMS,
	type UpdateIndexRelease,
	type UpdateIndexV1,
} from "../shared/update-index";
import { createApp } from "../update-server/app";
import { initConfig } from "../update-server/lib/config";
import { getReleaseByVersion, invalidateProduct } from "../update-server/lib/release-cache";
import { MirrorIngestService } from "../update-server/services/mirror-ingest";
import { V2CanonicalProjection } from "../update-server/services/v2-canonical-projection";
import { LocalStorage } from "../update-server/storage/local";
import historical083 from "./fixtures/v083-update-client-source.json";
import { historicalClient } from "./fixtures/v091-update-client";

const repository = "fixture-owner/narrafork";
const origin = "https://mirror.fixture.invalid";
const sha256 = (bytes: Uint8Array) => createHash("sha256").update(bytes).digest("hex");
const sha512 = (bytes: Uint8Array) => createHash("sha512").update(bytes).digest("base64");
const identity = (name: string, bytes: Buffer) => ({
	name,
	size: bytes.length,
	sha256: sha256(bytes),
});
const latestUrl = (platform = "win-x64", current = "0.9.1", channel = "beta") =>
	`${origin}/api/v2/products/narrafork/releases/latest?platform=${platform}&version=${current}&channel=${channel}`;
interface FixtureOptions {
	platform?: string;
	direct?: boolean;
	realPatches?: boolean;
	versions?: string[];
	unrelated?: boolean;
	largeNotes?: boolean;
	wrongBase?: boolean;
	prunedBase?: boolean;
}
async function withServer(
	run: (ctx: Awaited<ReturnType<typeof fixture>>) => Promise<void>,
	options: FixtureOptions = {},
) {
	invalidateProduct("narrafork");
	const ctx = await fixture(options);
	try {
		await run(ctx);
	} finally {
		invalidateProduct("narrafork");
		await rm(ctx.root, { recursive: true, force: true });
	}
}
async function fixture(options: FixtureOptions) {
	const root = await mkdtemp(join(tmpdir(), "nf-v2-canonical-"));
	const config = join(root, "config.json");
	await writeFile(
		config,
		JSON.stringify({
			mirrorRepositories: { narrafork: repository },
			mirrorLimits: { minFreeBytes: 0 },
			cors: { enabled: false, origins: [] },
		}),
	);
	await initConfig(config);
	const storage = new LocalStorage(join(root, "data"));
	const ingest = new MirrorIngestService(storage);
	const app = createApp(storage, ingest);
	const platform = options.platform ?? "win-x64";
	const versions = options.versions ?? ["0.9.0", "0.9.1", "0.9.2", "0.9.3", "0.9.4", "0.9.5"];
	const raw = new Map<string, Buffer>();
	const binaries = new Map<string, Buffer>();
	const releases: UpdateIndexRelease[] = [];
	let index: UpdateIndexV1 | null = null;
	for (let i = 0; i < versions.length; i++) {
		const version = versions[i];
		const bytes = Buffer.from(`${"fixture binary content\n".repeat(1024)}${version}\n`);
		binaries.set(version, bytes);
		const name = `narrafork-${version}-${UPDATE_INDEX_PLATFORMS[platform]}`;
		const metadata = Buffer.from(
			JSON.stringify(
				computeBinaryMetadataFromBuffer(name, bytes, {
					version,
					platformId: platform,
					target: `bun-${platform.replace(/^win-/, "windows-")}`,
					commit: "a".repeat(40),
					buildDate: "2026-07-01T00:00:00.000Z",
				}),
			),
		);
		raw.set(`${name}.metadata.json`, metadata);
		const patches = [];
		const bases = i > 1 ? [versions[i - 1]] : [];
		if (options.direct && i === versions.length - 1 && !bases.includes(versions[1]))
			bases.push(versions[1]);
		for (const fromVersion of bases) {
			const old = binaries.get(fromVersion) as Buffer;
			const patchName = `${name}.from-${fromVersion}.zstd-patch`;
			let patch = Buffer.from(`synthetic payload ${fromVersion} -> ${version}`);
			if (options.realPatches) {
				const oldPath = join(root, "patch-base");
				const newPath = join(root, "patch-target");
				const patchPath = join(root, "patch");
				await writeFile(oldPath, old);
				await writeFile(newPath, bytes);
				const child = Bun.spawn(
					["zstd", `--patch-from=${oldPath}`, newPath, "-o", patchPath, "-f", "-q"],
					{ stdout: "ignore", stderr: "ignore", timeout: 5000 },
				);
				if ((await child.exited) !== 0) throw new Error("Fixture zstd generation failed");
				patch = await readFile(patchPath);
			}
			const patchMeta = Buffer.from(
				JSON.stringify({
					fromVersion,
					toVersion: version,
					oldFileSize: old.length,
					oldFileSha512:
						options.wrongBase && i === 3 ? sha512(Buffer.from("wrong base")) : sha512(old),
					stableEnd: 0,
					newTailSize: bytes.length,
					patchSize: patch.length,
					newFileSize: bytes.length,
					newFileSha512: sha512(bytes),
					mode: "patch-from",
				}),
			);
			raw.set(patchName, patch);
			raw.set(`${patchName}.meta.json`, patchMeta);
			patches.push({
				...identity(patchName, patch),
				fromVersion,
				metadata: identity(`${patchName}.meta.json`, patchMeta),
			});
		}
		const notes = Buffer.from(
			JSON.stringify({
				schemaVersion: 1,
				repository,
				version,
				notes: {
					en: options.largeNotes ? "x".repeat(100_000) : `Notes ${version}`,
					"zh-CN": options.largeNotes ? "说明".repeat(100_000) : `说明 ${version}`,
				},
			}),
		);
		const notePath = `notes/${version}-${sha256(notes)}.json`;
		raw.set(notePath, notes);
		const release: UpdateIndexRelease = {
			version,
			tag: `v${version}`,
			commit: "a".repeat(40),
			prerelease: i > 1 && i !== 3,
			publishedAt: "2026-07-01T00:00:00.000Z",
			notes: { path: notePath, size: notes.length, sha256: sha256(notes) },
			files: [
				{
					...identity(name, bytes),
					platform,
					sha512: sha512(bytes),
					metadata: identity(`${name}.metadata.json`, metadata),
					patches,
				},
			],
		};
		releases.push(release);
		index = mergeUpdateIndex(index, release, repository);
	}
	if (options.unrelated) {
		const unrelated = structuredClone(releases[2]);
		unrelated.files[0].patches = [];
		// A valid but disconnected release between 0.9.1 and 0.9.2; no need to commit its objects.
		unrelated.version = "0.9.2-beta.1";
		unrelated.tag = `v${unrelated.version}`;
		unrelated.files[0].name = `narrafork-${unrelated.version}-${UPDATE_INDEX_PLATFORMS[platform]}`;
		unrelated.files[0].metadata.name = `${unrelated.files[0].name}.metadata.json`;
		delete unrelated.notes;
		index = mergeUpdateIndex(index, unrelated, repository);
	}
	if (!index) throw new Error("Missing fixture index");
	if (options.prunedBase)
		index.releases = index.releases.filter((r) => !versions.slice(0, 2).includes(r.version));
	const publish = async (
		selected = releases.filter((r) => index.releases.some((entry) => entry.version === r.version)),
	) => {
		for (const release of selected) {
			const assets = mirrorAssetsForTarget(index, release.version);
			for (const asset of assets) {
				await ingest.ingestAsset(
					"narrafork",
					asset,
					new Blob([Uint8Array.from(raw.get(asset.name) as Buffer)]).stream(),
					new AbortController().signal,
				);
			}
			await ingest.commitMain(
				"narrafork",
				{
					schemaVersion: 1,
					repository,
					indexCommit: "b".repeat(40),
					index,
					targetVersion: release.version,
					assets,
				},
				new AbortController().signal,
			);
		}
	};
	const check = (current = "0.9.1", channel = "beta") =>
		app.request(latestUrl(platform, current, channel));
	return { root, storage, ingest, app, platform, releases, index, raw, binaries, publish, check };
}

test("v2 uses canonical pointers and complete cross-channel graph without legacy cache, skipping unrelated nodes", async () => {
	await withServer(
		async (ctx) => {
			await ctx.publish();
			expect(await ctx.storage.listFiles("products")).toEqual([]);
			const response = await ctx.check();
			expect(response.status).toBe(200);
			const data = await response.json();
			expect(data.version).toBe("0.9.5");
			expect(data.file.filename).toBe("narrafork-0.9.5-windows-x64.exe");
			expect(data.file.sha512).toBe(sha512(ctx.binaries.get("0.9.5") as Buffer));
			expect(data.patchChain.map((s: { toVersion: string }) => s.toVersion)).toEqual([
				"0.9.2",
				"0.9.3",
				"0.9.4",
				"0.9.5",
			]);
			expect(data.releaseNotesPerVersion.map((s: { version: string }) => s.version)).toEqual([
				"0.9.2",
				"0.9.3",
				"0.9.4",
				"0.9.5",
			]);
			expect(data.releaseNotes).toEqual({ en: "Notes 0.9.5", "zh-CN": "说明 0.9.5" });
			for (const step of data.patchChain) {
				for (const url of [step.url, step.metaUrl]) {
					expect(new URL(url, origin).origin).toBe(origin);
					const download = await ctx.app.request(new URL(url, origin));
					expect(download.status).toBe(200);
					expect(download.headers.get("location")).toBeNull();
					await download.arrayBuffer();
				}
				const meta = await (await ctx.app.request(new URL(step.metaUrl, origin))).json();
				expect(meta.oldFileSha512).toBe(sha512(ctx.binaries.get(step.fromVersion) as Buffer));
				expect(meta.newFileSha512).toBe(sha512(ctx.binaries.get(step.toVersion) as Buffer));
			}
			expect((await (await ctx.check("0.9.1", "stable")).json()).version).toBe("0.9.3");
			expect((await (await ctx.check("0.9.5")).json()).updateAvailable).toBe(false);
			const metadata = await (
				await ctx.app.request(`${origin}/api/v2/products/narrafork/releases/0.9.5/metadata`)
			).json();
			expect(metadata.platforms[ctx.platform].filename).toBe(data.file.filename);
			expect(metadata.releaseDate).toBe("2026-07-01T00:00:00.000Z");
			await ctx.storage.saveFile(
				"products/narrafork/releases/999.0.0/meta.json",
				Buffer.from(JSON.stringify({ version: "999.0.0", channel: "beta", platforms: {} })),
			);
			expect((await (await ctx.check()).json()).version).toBe("0.9.5");
		},
		{ unrelated: true },
	);
});

test("verified direct patch wins over chain and baseline Windows identity is exact", async () => {
	await withServer(
		async (ctx) => {
			await ctx.publish();
			const data = await (await ctx.check()).json();
			expect(data.zstdPatch.fromVersion).toBe("0.9.1");
			expect(data.patchChain).toBeUndefined();
			expect(data.file.filename).toBe("narrafork-0.9.5-windows-x64-baseline.exe");
			expect((await ctx.app.request(latestUrl("win-x64"))).status).toBe(503);
		},
		{ direct: true, platform: "win-x64-baseline" },
	);
});

test("canonical latest preserves exact legacy metadata outside its catalog and missing history stays 404", async () => {
	await withServer(async (ctx) => {
		await ctx.publish();
		const legacy = {
			version: "0.8.3",
			channel: "beta",
			releaseDate: "2026-07-01T00:00:00.000Z",
			platforms: {
				[ctx.platform]: {
					filename: "legacy-0.8.3.exe",
					size: 10,
					sha512: "legacy",
					hasZstdPatch: false,
				},
			},
		};
		await ctx.storage.saveFile(
			"products/narrafork/releases/0.8.3/meta.json",
			Buffer.from(JSON.stringify(legacy)),
		);
		const historical = await ctx.app.request(
			`${origin}/api/v2/products/narrafork/releases/0.8.3/metadata`,
		);
		expect(historical.status).toBe(200);
		expect(await historical.json()).toEqual(legacy);
		expect(
			(await ctx.app.request(`${origin}/api/v2/products/narrafork/releases/0.8.4/metadata`)).status,
		).toBe(404);
		expect((await (await ctx.check()).json()).version).toBe("0.9.5");
	});
});

test("same-version canonical metadata wins and broken committed objects never fall back to warm legacy cache", async () => {
	await withServer(async (ctx) => {
		await ctx.publish();
		const legacy = {
			version: "0.9.5",
			channel: "beta" as const,
			releaseDate: "2026-07-01T00:00:00.000Z",
			platforms: {
				[ctx.platform]: {
					filename: "legacy-shadow.exe",
					size: 10,
					sha512: "legacy",
					hasZstdPatch: false,
				},
			},
		};
		await ctx.storage.saveFile(
			"products/narrafork/releases/0.9.5/meta.json",
			Buffer.from(JSON.stringify(legacy)),
		);
		expect(await getReleaseByVersion(ctx.storage, "narrafork", "0.9.5")).toEqual(legacy);
		const url = `${origin}/api/v2/products/narrafork/releases/0.9.5/metadata`;
		const canonical = await (await ctx.app.request(url)).json();
		expect(canonical.platforms[ctx.platform].filename).toBe("narrafork-0.9.5-windows-x64.exe");
		const notes = ctx.releases.at(-1)?.notes;
		if (!notes) throw new Error("Missing notes fixture");
		await ctx.storage.deleteFile(`mirror-objects/${notes.sha256}`);
		expect((await ctx.app.request(url)).status).toBe(503);
		await ctx.storage.saveFile(`mirror-objects/${notes.sha256}`, Buffer.alloc(notes.size, 32));
		const broken = await ctx.app.request(url);
		expect(broken.status).toBe(503);
		expect((await broken.json()).code).toBe("CANONICAL_UPDATE_UNAVAILABLE");
	});
});

test("canonical semver prerelease to stable is an update (not legacy parseInt equality)", async () => {
	await withServer(
		async (ctx) => {
			await ctx.publish();
			const response = await ctx.check("0.9.5-beta.1");
			expect(response.status).toBe(200);
			const data = await response.json();
			expect(data.updateAvailable).toBe(true);
			expect(data.zstdPatch.fromVersion).toBe("0.9.5-beta.1");
		},
		{ versions: ["0.9.0", "0.9.5-beta.1", "0.9.5"] },
	);
});

for (const problem of [
	"missing-patch",
	"missing-meta",
	"uncommitted",
	"broken-catalog",
	"bad-meta-hash",
	"bad-notes-hash",
	"wrong-base",
] as const) {
	test(`canonical ${problem} never silently falls back to a legacy newer release`, async () => {
		await withServer(
			async (ctx) => {
				await ctx.publish(
					problem === "uncommitted" ? ctx.releases.filter((r) => r.version !== "0.9.3") : undefined,
				);
				await ctx.storage.saveFile(
					"products/narrafork/releases/999.0.0/meta.json",
					Buffer.from(
						JSON.stringify({
							version: "999.0.0",
							channel: "beta",
							platforms: {
								[ctx.platform]: {
									filename: "legacy.exe",
									size: 10,
									sha512: "old",
									hasZstdPatch: true,
								},
							},
						}),
					),
				);
				const patch = ctx.releases[3].files[0].patches[0];
				if (problem === "missing-patch")
					await ctx.storage.deleteFile(`mirror-objects/${patch.sha256}`);
				if (problem === "missing-meta")
					await ctx.storage.deleteFile(`mirror-objects/${patch.metadata.sha256}`);
				if (problem === "bad-meta-hash")
					await ctx.storage.saveFile(
						`mirror-objects/${patch.metadata.sha256}`,
						Buffer.alloc(patch.metadata.size, 32),
					);
				if (problem === "bad-notes-hash") {
					const notes = ctx.releases.at(-1)?.notes;
					if (!notes) throw new Error("Missing notes");
					await ctx.storage.saveFile(
						`mirror-objects/${notes.sha256}`,
						Buffer.alloc(notes.size, 32),
					);
				}
				if (problem === "broken-catalog")
					await ctx.storage.saveFile("mirror-snapshots/narrafork.json", Buffer.from("{}"));
				const response = await ctx.check();
				expect(response.status).toBe(503);
				expect((await response.json()).error).toContain("GitHub");
			},
			{ wrongBase: problem === "wrong-base" },
		);
	});
}

test("canonical missing snapshot preserves legacy directory fallback", async () => {
	await withServer(async (ctx) => {
		await ctx.storage.saveFile(
			"products/narrafork/releases/0.8.3/meta.json",
			Buffer.from(
				JSON.stringify({
					version: "0.8.3",
					channel: "beta",
					releaseDate: "2026-07-01",
					platforms: {
						[ctx.platform]: {
							filename: "legacy.exe",
							size: 10,
							sha512: "legacy",
							hasZstdPatch: false,
						},
					},
				}),
			),
		);
		const data = await (await ctx.check("0.8.0")).json();
		expect(data.version).toBe("0.8.3");
	});
});

test("notes excerpts keep complete v2 JSON below the real historical client's 1 MiB ceiling", async () => {
	await withServer(
		async (ctx) => {
			await ctx.publish();
			const response = await ctx.check();
			expect(response.status).toBe(200);
			const bytes = await response.text();
			expect(Buffer.byteLength(bytes)).toBeLessThan(1024 * 1024);
			const data = JSON.parse(bytes);
			expect(data.releaseNotes.en.length).toBeLessThan(2100);
			expect(data.releaseNotes["zh-CN"].length).toBeLessThan(2100);
			expect(data.releaseNotesPerVersion).toHaveLength(4);
		},
		{ largeNotes: true },
	);
});

test("no path over sixteen steps is advertised to legacy clients", async () => {
	await withServer(
		async (ctx) => {
			await ctx.publish();
			expect((await ctx.check()).status).toBe(503);
		},
		{ versions: Array.from({ length: 20 }, (_, i) => `0.9.${i}`) },
	);
});

test("projection cancellation and oversized storage metadata cancel streams without buffering payloads", async () => {
	await withServer(async (ctx) => {
		await ctx.publish();
		const snapshot = await ctx.ingest.readMain("narrafork");
		if (!snapshot) throw new Error("Missing snapshot");
		const controller = new AbortController();
		controller.abort();
		await expect(
			new V2CanonicalProjection(ctx.storage, "narrafork", snapshot, controller.signal).latest(
				"beta",
				ctx.platform,
				"0.9.1",
			),
		).rejects.toThrow();
		const original = ctx.storage.getFileStream.bind(ctx.storage);
		const meta = ctx.releases[2].files[0].patches[0].metadata;
		let cancelled = false;
		const streams = spyOn(ctx.storage, "getFileStream").mockImplementation(async (path) =>
			path === `mirror-objects/${meta.sha256}`
				? new ReadableStream({
						start(c) {
							c.enqueue(Buffer.alloc(meta.size + 1));
						},
						cancel() {
							cancelled = true;
						},
					})
				: original(path),
		);
		try {
			expect((await ctx.check()).status).toBe(503);
			expect(cancelled).toBe(true);
		} finally {
			streams.mockRestore();
		}
	});
});

test("historical fixture checks work with all git/subprocess access unavailable", async () => {
	const spawn = spyOn(Bun, "spawn").mockImplementation(() => {
		throw new Error("Subprocess unavailable in shallow-checkout fixture");
	});
	try {
		const client = await historicalClient({
			root: "/unused-fixture-root",
			basePath: "/unused-fixture-base",
			fetch: async () => Response.json({ updateAvailable: false, version: "0.9.1" }),
		});
		expect((await client.check("win-x64")).updateAvailable).toBe(false);
		expect(spawn).not.toHaveBeenCalled();
	} finally {
		spawn.mockRestore();
	}
});

test("v0.9.1 original checkChannel/downloadUpdate reconstruct .1→.2→.3→.4→.5 with original zstd/net code", async () => {
	await withServer(
		async (ctx) => {
			await ctx.publish();
			const basePath = join(ctx.root, "running-0.9.1.exe");
			await writeFile(basePath, ctx.binaries.get("0.9.1") as Buffer);
			const requests: string[] = [];
			const client = await historicalClient({
				root: ctx.root,
				basePath,
				fetch: async (url, init) => {
					expect(new URL(url).origin).toBe(origin);
					requests.push(url);
					return ctx.app.request(url, init);
				},
			});
			const check = await client.check(ctx.platform);
			expect(check.updateAvailable).toBe(true);
			expect(check.patchChain).toHaveLength(4);
			// Same version is not enough: a different actual base must fail, without full fallback.
			await writeFile(basePath, Buffer.alloc((ctx.binaries.get("0.9.1") as Buffer).length));
			expect((await client.download(check.releaseInfo)).success).toBe(false);
			await writeFile(basePath, ctx.binaries.get("0.9.1") as Buffer);
			requests.length = 0;
			const downloaded = await client.download(check.releaseInfo);
			expect(downloaded.success, downloaded.error).toBe(true);
			expect(
				(await readFile(downloaded.newBinaryPath)).equals(ctx.binaries.get("0.9.5") as Buffer),
			).toBe(true);
			expect(sha512(await readFile(downloaded.newBinaryPath))).toBe(check.releaseInfo.sha512);
			expect(requests.filter((url) => url.includes("/mirror/assets/"))).toHaveLength(8);
			expect((await readFile(basePath)).equals(ctx.binaries.get("0.9.1") as Buffer)).toBe(true);
			const cancelled = new AbortController();
			cancelled.abort();
			expect((await client.download(check.releaseInfo, cancelled.signal)).success).toBe(false);
			const redirectClient = await historicalClient({
				root: ctx.root,
				basePath,
				fetch: async () =>
					new Response(null, {
						status: 302,
						headers: { location: "https://github.com/fixture/release" },
					}),
			});
			await expect(redirectClient.fetchSameOrigin(`${origin}/redirect`)).rejects.toThrow(
				"Untrusted update request redirect",
			);
		},
		{ realPatches: true },
	);
});

const prunedVersions = ["0.8.2", "0.8.3", "0.9.0", "0.9.1", "0.9.2", "0.9.3"];
for (const platform of ["win-x64", "win-x64-baseline"]) {
	for (const channel of ["stable", "beta"]) {
		test(`0.8.3 outside the catalog can upgrade through a verified ${platform} ${channel} chain`, async () => {
			await withServer(
				async (ctx) => {
					await ctx.publish();
					expect(ctx.index.releases.some((r) => r.version === "0.8.3")).toBe(false);
					const response = await ctx.check("0.8.3", channel);
					expect(response.status).toBe(200);
					const data = await response.json();
					expect(data.updateAvailable).toBe(true);
					expect(data.version).toBe(channel === "stable" ? "0.9.1" : "0.9.3");
					expect(data.patchChain[0].fromVersion).toBe("0.8.3");
					expect(data.patchChain[0].toVersion).toBe("0.9.0");
					expect(data.patchChain.at(-1).toVersion).toBe(data.version);
					for (const step of data.patchChain) {
						const metaResponse = await ctx.app.request(new URL(step.metaUrl, origin));
						expect(metaResponse.status).toBe(200);
						const meta = await metaResponse.json();
						expect(meta.oldFileSha512).toBe(sha512(ctx.binaries.get(step.fromVersion) as Buffer));
						expect(meta.newFileSha512).toBe(sha512(ctx.binaries.get(step.toVersion) as Buffer));
					}
				},
				{ versions: prunedVersions, prunedBase: true, platform },
			);
		});
	}
}

test("an out-of-catalog current version can also use a verified direct patch", async () => {
	await withServer(
		async (ctx) => {
			await ctx.publish();
			const response = await ctx.check("0.8.3");
			expect(response.status).toBe(200);
			const data = await response.json();
			expect(data.zstdPatch.fromVersion).toBe("0.8.3");
			expect(data.patchChain).toBeUndefined();
		},
		{ versions: prunedVersions, prunedBase: true, direct: true },
	);
});

test("out-of-catalog entry does not weaken verification of subsequent patch bases", async () => {
	await withServer(
		async (ctx) => {
			await ctx.publish();
			expect((await ctx.check("0.8.3")).status).toBe(503);
		},
		{ versions: prunedVersions, prunedBase: true, wrongBase: true },
	);
});

for (const problem of ["missing-meta", "bad-meta", "missing-patch"] as const) {
	test(`out-of-catalog entry still rejects ${problem}`, async () => {
		await withServer(
			async (ctx) => {
				await ctx.publish();
				const patch = ctx.releases[2].files[0].patches[0];
				if (problem === "missing-patch")
					await ctx.storage.deleteFile(`mirror-objects/${patch.sha256}`);
				else if (problem === "missing-meta")
					await ctx.storage.deleteFile(`mirror-objects/${patch.metadata.sha256}`);
				else
					await ctx.storage.saveFile(
						`mirror-objects/${patch.metadata.sha256}`,
						Buffer.alloc(patch.metadata.size, 32),
					);
				expect((await ctx.check("0.8.3")).status).toBe(503);
			},
			{ versions: prunedVersions, prunedBase: true },
		);
	});
}

test("actual v0.8.3 check/download reconstructs a pruned-base chain and refuses the wrong executable", async () => {
	await withServer(
		async (ctx) => {
			await ctx.publish();
			const base = ctx.binaries.get("0.8.3") as Buffer;
			const basePath = join(ctx.root, "running-0.8.3.exe");
			await writeFile(basePath, base);
			const client = await historicalClient({
				root: ctx.root,
				basePath,
				source: historical083,
				fetch: async (url, init) => {
					expect(new URL(url).origin).toBe(origin);
					return ctx.app.request(url, init);
				},
			});
			const check = await client.check(ctx.platform, "stable");
			expect(check.updateAvailable).toBe(true);
			expect(check.patchChain).toHaveLength(2);
			expect(check.patchChain[0].fromVersion).toBe("0.8.3");
			await writeFile(basePath, Buffer.alloc(base.length));
			expect((await client.download(check.releaseInfo)).success).toBe(false);
			await writeFile(basePath, base);
			const result = await client.download(check.releaseInfo);
			expect(result.success, result.error).toBe(true);
			expect(
				(await readFile(result.newBinaryPath)).equals(ctx.binaries.get("0.9.1") as Buffer),
			).toBe(true);
			expect(sha512(await readFile(result.newBinaryPath))).toBe(check.releaseInfo.sha512);
			expect((await readFile(basePath)).equals(base)).toBe(true);
		},
		{ versions: prunedVersions, prunedBase: true, realPatches: true },
	);
});

for (const problem of ["platform", "size", "hash"] as const) {
	test(`retained initial release ${problem} mismatch cannot use the external-base exception`, async () => {
		await withServer(
			async (ctx) => {
				await ctx.publish();
				const snapshot = await ctx.ingest.readMain("narrafork");
				if (!snapshot) throw new Error("Missing snapshot");
				const source = snapshot.index.releases.find((r) => r.version === "0.9.1");
				if (!source) throw new Error("Missing source release");
				if (problem === "platform") source.files[0].platform = "win-arm64";
				if (problem === "size") source.files[0].size++;
				if (problem === "hash") source.files[0].sha512 = sha512(Buffer.from("wrong"));
				await expect(
					new V2CanonicalProjection(
						ctx.storage,
						"narrafork",
						snapshot,
						new AbortController().signal,
					).latest("beta", ctx.platform, "0.9.1"),
				).rejects.toThrow();
			},
			{ direct: true },
		);
	});
}
