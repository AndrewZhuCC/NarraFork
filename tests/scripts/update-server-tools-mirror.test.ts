import { describe, expect, test } from "bun:test";
import { UpdateServerMirrorHttp } from "../../scripts/lib/update-server-bridge-http";
import { prepareUpdateServerToolsMirror } from "../../scripts/lib/update-server-tools-mirror";
import { mirrorPublishedGitHubRelease } from "../../scripts/mirror-update-server";
import {
	getHelperAssetName,
	HELPER_MANIFEST_FILENAME,
	HELPER_PLATFORMS,
	HELPER_RELEASE_TAG,
	HELPER_TOOL_VERSIONS,
	HELPER_TOOLS,
} from "../../shared/helper-distribution";
import {
	EXECUTOR_MANIFEST_FILENAME,
	EXECUTOR_PLATFORMS,
	executorPublishedFilename,
} from "../../shared/remote-executor";
import { WebDavClient } from "../../shared/webdav-transport";
import { createWebDavFixture } from "../fixtures/webdav-fixture";
import { commit, config, repository, sha } from "./mirror-fixture";

function fixture(kind: "helpers" | "executor") {
	const files = new Map<string, Buffer>();
	const artifact = (name: string) => {
		const content = Buffer.from(`${name}\n`);
		files.set(name, content);
		return { name, size: content.length, sha256: sha(content) };
	};
	const licenses = [artifact("LICENSE-test.txt")];
	const tag = kind === "helpers" ? HELPER_RELEASE_TAG : "executor-v1.2.0";
	const manifest =
		kind === "helpers"
			? {
					schemaVersion: 1,
					repository,
					tag,
					commit,
					catalogVersion: "1.0.0",
					files: HELPER_TOOLS.flatMap((tool) =>
						HELPER_PLATFORMS.map((platform) => ({
							tool,
							platform,
							toolVersion: HELPER_TOOL_VERSIONS[tool],
							...artifact(getHelperAssetName(tool, platform)),
						})),
					),
					licenses,
				}
			: {
					schemaVersion: 1,
					repository,
					tag,
					commit,
					manifest: {
						version: "1.2.0",
						protocolVersion: 1,
						releasedAt: "2026-07-01T00:00:00Z",
						platforms: Object.fromEntries(
							EXECUTOR_PLATFORMS.map((platform) => {
								const a = artifact(executorPublishedFilename("1.2.0", platform));
								return [platform, { filename: a.name, size: a.size, sha256: a.sha256 }];
							}),
						),
					},
					licenses,
				};
	const manifestName = kind === "helpers" ? HELPER_MANIFEST_FILENAME : EXECUTOR_MANIFEST_FILENAME;
	files.set(manifestName, Buffer.from(`${JSON.stringify(manifest, null, 2)}\n`));
	const assets = [...files].map(([name, bytes], i) => ({
		id: i + 1,
		name,
		size: bytes.length,
		digest: `sha256:${sha(bytes)}`,
		state: "uploaded",
	}));
	const downloaded: string[] = [];
	const run = async (args: string[]) => {
		const path = args[1];
		if (!path.startsWith(`repos/${repository}/`)) throw new Error("Official fallback forbidden");
		if (path.includes("git/ref/tags/"))
			return JSON.stringify({ ref: `refs/tags/${tag}`, object: { type: "commit", sha: commit } });
		if (path.includes("releases/tags/"))
			return JSON.stringify({
				id: 9,
				tag_name: tag,
				draft: false,
				prerelease: false,
				published_at: "2026-07-01T00:00:00Z",
			});
		if (path.includes("releases/9/assets?")) return JSON.stringify(assets);
		if (path.includes("releases/assets/")) {
			const asset = assets.find((a) => a.id === Number(path.split("/").at(-1)));
			if (!asset || ![manifestName, "LICENSE-test.txt"].includes(asset.name))
				throw new Error("Full helper/executor download forbidden");
			downloaded.push(asset.name);
			return files.get(asset.name)?.toString("utf8") ?? "";
		}
		throw new Error("Unrecognized read-only request");
	};
	let stored: unknown;
	const requests: string[] = [];
	const http = new UpdateServerMirrorHttp(config, {
		fetchImpl: (async (url: unknown, init?: RequestInit) => {
			const path = new URL(String(url)).pathname;
			requests.push(path);
			if (path !== `/api/v3/products/narrafork/mirror/tools/${kind}/${tag}`)
				throw new Error("Old tools aliases/binary upload forbidden");
			if (init?.method !== "POST") throw new Error("Producer GET is forbidden");
			stored = JSON.parse(String(init.body));
			const projection = stored as { assets: { name: string; size: number; sha256: string }[] };
			return Response.json({
				schemaVersion: 1,
				verification: "server-sha256",
				repository,
				kind,
				tag,
				verified: projection.assets.map(({ name, size, sha256 }) => ({ name, size, sha256 })),
			});
		}) as unknown as typeof fetch,
	});
	return {
		options: { kind, repository, tag, config, run, http },
		assets,
		files,
		downloaded,
		requests,
		get stored() {
			return stored;
		},
	};
}
describe("canonical auxiliary metadata mirror", () => {
	for (const kind of ["helpers", "executor"] as const)
		test(`${kind} mirrors only raw manifest and authorized license text`, async () => {
			const f = fixture(kind);
			const result = await mirrorPublishedGitHubRelease(f.options);
			expect(result.status).toBe("METADATA_ONLY");
			expect(result.snapshotCommitted).toBe(true);
			expect(f.downloaded.sort()).toEqual(
				[
					kind === "helpers" ? HELPER_MANIFEST_FILENAME : EXECUTOR_MANIFEST_FILENAME,
					"LICENSE-test.txt",
				].sort(),
			);
			expect(f.requests).toHaveLength(1);
			const stored = f.stored as { assets: { name: string; content: string }[] };
			expect(stored.assets).toHaveLength(2);
			for (const asset of stored.assets) {
				const bytes = f.files.get(asset.name);
				if (!bytes) throw new Error("Missing expected metadata fixture");
				expect(asset.content).toBe(bytes.toString("utf8"));
			}
		});
	for (const kind of ["helpers", "executor"] as const) {
		test(`${kind} WebDAV staging is accepted bytes only, no server requests`, async () => {
			const f = fixture(kind);
			const dav = createWebDavFixture();
			const davConfig = {
				url: dav.baseUrl,
				feed: "default",
				auth: { bearerToken: "fixture-token" },
			};
			const result = await mirrorPublishedGitHubRelease({
				...f.options,
				config: undefined,
				transport: "webdav",
				relay: {
					config: davConfig,
					signingKey: Buffer.alloc(32, 1).toString("base64"),
					client: new WebDavClient(davConfig, { fetchImpl: dav.fetchImpl }),
				},
			});
			expect(result.status).toBe("STAGED_FOR_PULL");
			expect(result.snapshotCommitted).toBe(false);
			expect(result.uploaded).toHaveLength(2);
			expect(result.verified).toEqual([]);
			expect(f.requests).toHaveLength(0);
			expect(dav.requests.some((r) => r.method === "GET")).toBe(false);
			expect(dav.requests.filter((r) => r.method === "PUT").at(-1)?.path).toContain(
				`/ready/narrafork/${kind}/`,
			);
		});
		for (const result of [
			{ ok: true },
			{
				schemaVersion: 1,
				verification: "server-sha256",
				repository,
				kind,
				tag: "wrong",
				verified: [],
			},
		])
			test(`${kind} legacy/forged commit receipt never triggers GET fallback`, async () => {
				const f = fixture(kind);
				f.options.http.postJson = async () => result;
				await expect(mirrorPublishedGitHubRelease(f.options)).rejects.toThrow(
					"upgrade update server",
				);
				expect(f.requests).toHaveLength(0);
			});
	}
	test("missing trusted public binary digest rejects metadata projection without downloading it", async () => {
		const f = fixture("executor");
		f.assets[0].digest = "";
		await expect(mirrorPublishedGitHubRelease(f.options)).rejects.toThrow();
		expect(f.requests).toHaveLength(0);
	});
	test("wrong declared license hash rejects before mirror writes", async () => {
		const f = fixture("helpers");
		f.files.set("LICENSE-test.txt", Buffer.from("corrupt"));
		await expect(mirrorPublishedGitHubRelease(f.options)).rejects.toThrow();
		expect(f.requests).toHaveLength(0);
	});
	test("online preview is read-only and uses no mirror config", async () => {
		const f = fixture("executor");
		expect(
			(await mirrorPublishedGitHubRelease({ ...f.options, config: undefined, dryRun: true }))
				.status,
		).toBe("DRY_RUN");
		expect(f.requests).toHaveLength(0);
	});
	test("pre-publication bundle sealing cannot revive retired tool aliases", async () => {
		await expect(prepareUpdateServerToolsMirror({ bridgeDir: "untrusted" })).rejects.toThrow(
			"retired",
		);
	});
});
