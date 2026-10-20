import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { readdirSync, readFileSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";
import { gunzipSync } from "node:zlib";
import { type RawCatalog, rawCatalogFromModelFiles } from "@shared/model-catalog/card";
import bundledCatalogJSON from "@shared/model-catalog/dist/catalog.json";
import { validateRawCatalog } from "@shared/model-catalog/src/validation";
import { legacyCatalogToRaw } from "./legacy-v1";

// Commit details include file patches; branch metadata keeps checks independent of diff size.
export const CATALOG_REVISION_URL =
	"https://api.github.com/repos/NarraFork/narrafork-model-catalog/branches/main";
export const CATALOG_ARCHIVE_BASE =
	"https://codeload.github.com/NarraFork/narrafork-model-catalog/tar.gz/";
export const MAX_CATALOG_REVISION_BYTES = 1024 * 1024;
export const MAX_CATALOG_BYTES = 16 * 1024 * 1024;

export function parseCatalogRevision(value: unknown): { version: string; publishedAt: string } {
	const branch = value as {
		commit?: { sha?: string; commit?: { committer?: { date?: string } } };
	} | null;
	const version = branch?.commit?.sha;
	const publishedAt = branch?.commit?.commit?.committer?.date;
	if (
		typeof version !== "string" ||
		!/^[a-f0-9]{40}$/.test(version) ||
		typeof publishedAt !== "string" ||
		!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$/.test(publishedAt) ||
		!Number.isFinite(Date.parse(publishedAt))
	)
		throw new Error("Invalid catalog Git revision");
	return { version, publishedAt };
}

export function catalogSourceVersion(version: string): string {
	return version.startsWith("v2:") ? version.slice(3) : version;
}

/** A stored or downloaded catalog. Snapshots cached by an older binary are imported once. */
export function decodeCatalog(value: unknown): RawCatalog {
	if (value && typeof value === "object" && "schemaVersion" in value && value.schemaVersion === 1)
		return legacyCatalogToRaw(value);
	return validateRawCatalog(value);
}

let bundledDecoded: RawCatalog | undefined;
/**
 * The catalog shipped in the binary, decoded once per process.
 *
 * Both the model catalog and the legacy built-in card table derive from this same document, and
 * each used to decode and fully validate it at import time (~35-45 ms apiece) in every process,
 * including every isolated test file. Only the bundled module object is memoised: catalogs read
 * from disk or the network keep going through `decodeCatalog` and are validated every time.
 * Callers must treat the result as read-only, as they already treat any decoded document.
 */
export function decodeBundledCatalog(): RawCatalog {
	bundledDecoded ??= decodeCatalog(bundledCatalogJSON);
	return bundledDecoded;
}

export async function catalogFromArchive(
	bytes: Uint8Array,
	version: string,
	publishedAt: string,
): Promise<RawCatalog> {
	if (bytes.length > MAX_CATALOG_BYTES) throw new Error("Catalog exceeds size limit");
	const tar = gunzipSync(bytes, { maxOutputLength: MAX_CATALOG_BYTES });
	const entries = await new Bun.Archive(tar).files();
	const files = new Map<string, string>();
	for (const [path, file] of entries) {
		const match = /^[^/]+\/models\/([a-z0-9][a-z0-9._-]*\.json)$/.exec(path);
		if (!match) continue;
		if (files.has(match[1]!)) throw new Error("Duplicate model file");
		files.set(match[1]!, await file.text());
	}
	return rawCatalogFromModelFiles(files, catalogSourceVersion(version), publishedAt);
}

// Refresh this application's offline snapshot from a catalog checkout, without a public SDK/build.
// bun server/lib/model-catalog/source.ts ../narrafork-model-catalog/models
if (import.meta.main) {
	const directory = process.argv[2];
	if (!directory) throw new Error("Usage: source.ts <catalog models directory>");
	const files = new Map(
		readdirSync(directory)
			.filter((name) => name.endsWith(".json"))
			.sort()
			.map((name) => [name, readFileSync(resolve(directory, name), "utf8")]),
	);
	const hash = createHash("sha256");
	for (const [name, text] of files) hash.update(name).update("\0").update(text).update("\0");
	const publishedAt = execFileSync(
		"git",
		["-C", directory, "log", "-1", "--format=%cI", "--", "."],
		{ encoding: "utf8", windowsHide: true },
	).trim();
	const catalog = rawCatalogFromModelFiles(files, `bundled-${hash.digest("hex")}`, publishedAt);
	writeFileSync(
		new URL("../../../shared/model-catalog/dist/catalog.json", import.meta.url),
		`${JSON.stringify(catalog, null, 2)}\n`,
	);
	console.log(`${catalog.models.length} models, ${catalog.variants.length} variants`);
}
