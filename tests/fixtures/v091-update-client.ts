import { createHash, randomUUID } from "node:crypto";
import * as fs from "node:fs";
import * as fsp from "node:fs/promises";
import * as path from "node:path";
import * as zlib from "node:zlib";
import historical from "./v091-update-client-source.json";

// Exact v0.9.1 declarations from server/services/update-service.ts (blob
// a1fb1cdbd07fef88f6d68fb79d479915eb218830), server/lib/zstd-patch.ts and
// server/lib/net/update-fetch.ts. The JSON records each source blob and extracted symbol.
// No git/network/history access at test time; works unchanged in a shallow CI checkout.
// Only host integration (settings, helper discovery and prepared-record persistence) is stubbed.
function evaluate(code: string, context: Record<string, unknown>, exports: string[]) {
	const js = new Bun.Transpiler({ loader: "ts", target: "bun" }).transformSync(code);
	const entries = Object.entries(context).filter(([key]) => key !== "default");
	return new Function(...entries.map(([key]) => key), `${js}\nreturn { ${exports.join(", ")} };`)(
		...entries.map(([, value]) => value),
	);
}
export async function historicalClient(options: {
	root: string;
	basePath: string;
	fetch: (url: string, init?: RequestInit) => Promise<Response>;
	/** Defaults to the original v0.9.1 fixture; also accepts the pinned v0.8.3 declarations. */
	source?: typeof historical;
}) {
	const context: Record<string, unknown> = {
		...fs,
		...fsp,
		...path,
		...zlib,
		createHash,
		randomUUID,
		fsConstants: fs.constants,
		updateServerChildEnvironment: () => process.env,
	};
	const source = options.source ?? historical;
	const [serviceSource, zstdSource, networkSource] = source.sections;
	const apply = evaluate(zstdSource.declarations, context, ["applyZstdPatchToFile"]);
	const transport = { fetch: options.fetch, key: "fixture", isCurrent: () => true };
	const network = networkSource
		? evaluate(networkSource.declarations, { createUpdateFetchContext: () => transport }, [
				"fetchUpdateSameOrigin",
				"readUpdateJson",
				"fetchUpdateWithTimeout",
			])
		: { fetchUpdateSameOrigin: options.fetch };
	const client = evaluate(
		serviceSource.declarations,
		{
			...context,
			...apply,
			...network,
			APP_VERSION: source.tag.replace(/^v/, ""),
			fetch: options.fetch,
			UPDATE_DIR: path.join(options.root, "cache"),
			logger: { debug() {}, info() {}, warn() {}, error() {} },
			createUpdateFetchContext: () => transport,
			createDistributionContext: () => ({}),
			getZstdCliPath: async () => Bun.which("zstd"),
			getCurrentExecutablePath: () => options.basePath,
			getServerBaseUrl: () => "https://mirror.fixture.invalid",
			parseUpdateSourceIdentity: (value: unknown) => value,
			isUpdateSourceCurrent: () => true,
			getUpdateCoordinationStatus: () => ({ scheduled: false }),
			resolvePreparedBinaryDestination: async (_base: string, release: { path: string }) => ({
				path: path.join(options.root, release.path),
				fileName: release.path,
				alreadyPresent: false,
			}),
			writePlacedUpdateInfo() {},
			preparedUpdateIdentity: () => ({ id: "fixture-only" }),
		},
		["checkChannel", "downloadUpdate"],
	);
	return {
		check: (platform: string, channel = "beta") =>
			client.checkChannel(
				"https://mirror.fixture.invalid",
				"narrafork",
				channel,
				platform,
				transport,
			),
		download: (release: unknown, signal?: AbortSignal) =>
			client.downloadUpdate(
				{ ...(release as object), sourceIdentity: { source: "update-server" } },
				undefined,
				{ signal },
			),
		fetchSameOrigin: network.fetchUpdateSameOrigin as (url: string) => Promise<Response>,
	};
}
