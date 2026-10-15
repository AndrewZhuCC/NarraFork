import { describe, expect, test } from "bun:test";
import { createHash } from "node:crypto";
import { existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { Worker } from "node:worker_threads";
import {
	computeBinaryMetadataFromBuffer,
	formatMetadataJson,
} from "../../scripts/lib/binary-metadata";
import { validatePublishedPatchSources } from "../../scripts/lib/local-github-patch-source";
import {
	computeFileIdentity,
	findPreviousVersionBinary,
	getBaselineMismatch,
	requirePublishedBaseline,
	selectPublishedBaseline,
} from "../../server/lib/release-baseline";
import { applyZstdPatch, generateZstdPatch } from "../../server/lib/zstd-patch";
import { createHelperDistributionDependencies } from "../../shared/helper-distribution";

describe("release baseline integrity", () => {
	test("selects the newest lower platform-specific version", () => {
		const directory = mkdtempSync(join(tmpdir(), "nf-release-baseline-"));
		try {
			for (const version of ["0.5.8", "0.5.9", "0.5.10"]) {
				writeFileSync(join(directory, `narrafork-${version}-linux-x64`), version);
			}
			writeFileSync(join(directory, "narrafork-0.5.10-linux-x64-baseline"), "other");

			const previous = findPreviousVersionBinary(directory, "narrafork-0.5.11-linux-x64", "0.5.11");
			expect(previous?.version).toBe("0.5.10");
			expect(previous?.path.endsWith("narrafork-0.5.10-linux-x64")).toBe(true);
		} finally {
			rmSync(directory, { recursive: true, force: true });
		}
	});

	test("returns null when the dist path is missing or is not a directory", () => {
		const directory = mkdtempSync(join(tmpdir(), "nf-release-baseline-missing-"));
		try {
			const missing = join(directory, "missing");
			const filePath = join(directory, "not-a-directory");
			writeFileSync(filePath, "binary");

			expect(findPreviousVersionBinary(missing, "narrafork-0.5.11-linux-x64", "0.5.11")).toBeNull();
			expect(
				findPreviousVersionBinary(filePath, "narrafork-0.5.11-linux-x64", "0.5.11"),
			).toBeNull();
		} finally {
			rmSync(directory, { recursive: true, force: true });
		}
	});

	test("selects the highest stable or beta baseline strictly below the target", () => {
		const selected = selectPublishedBaseline("0.5.11", [
			{
				version: "0.5.9",
				channel: "beta",
				file: { filename: "narrafork-0.5.9-linux-x64", size: 9, sha512: "beta" },
			},
			{
				version: "0.5.10",
				channel: "stable",
				file: { filename: "narrafork-0.5.10-linux-x64", size: 10, sha512: "stable" },
			},
			{
				version: "0.5.11",
				channel: "beta",
				file: { filename: "narrafork-0.5.11-linux-x64", size: 11, sha512: "target" },
			},
		]);
		expect(selected?.version).toBe("0.5.10");
	});

	test("rejects a local older guess when the exact published baseline is missing", () => {
		const published = [
			{
				version: "0.5.10",
				channel: "stable" as const,
				file: { filename: "narrafork-0.5.10-linux-x64", size: 10, sha512: "published" },
			},
		];
		expect(() =>
			requirePublishedBaseline("0.5.11", "narrafork-0.5.11-linux-x64", published, [
				"narrafork-0.5.9-linux-x64",
			]),
		).toThrow(/0\.5\.10.*missing from dist/i);
		expect(
			requirePublishedBaseline("0.5.11", "narrafork-0.5.11-linux-x64", published, [
				"narrafork-0.5.10-linux-x64",
			]),
		).toEqual(published[0]);
	});

	test("allows a fresh checkout when neither online channel has a platform baseline", () => {
		expect(requirePublishedBaseline("0.5.11", "narrafork-0.5.11-linux-x64", [], [])).toBeNull();
	});

	test("reports when a local baseline differs from the published identity", () => {
		const directory = mkdtempSync(join(tmpdir(), "nf-release-identity-"));
		try {
			const filePath = join(directory, "baseline");
			writeFileSync(filePath, "overwritten");
			const actual = computeFileIdentity(filePath);
			const mismatch = getBaselineMismatch(actual, {
				filename: "narrafork-0.5.10-linux-x64",
				size: 8,
				sha512: createHash("sha512").update("published").digest("base64"),
			});
			expect(mismatch).toContain("expected size");
			expect(mismatch).toContain("actual sha512");
		} finally {
			rmSync(directory, { recursive: true, force: true });
		}
	});

	test("records the source identity in new patches and replays successfully", () => {
		const oldBuffer = Buffer.from("published-source-binary".repeat(4096));
		const newBuffer = Buffer.from("new-target-binary".repeat(4096));
		const { patch, meta } = generateZstdPatch(oldBuffer, newBuffer, {
			fromVersion: "0.5.10",
			toVersion: "0.5.11",
		});

		expect(meta.oldFileSize).toBe(oldBuffer.length);
		expect(meta.oldFileSha512).toBe(createHash("sha512").update(oldBuffer).digest("base64"));
		expect(applyZstdPatch(oldBuffer, patch, meta)).toEqual(newBuffer);
	});
});

describe("production canonical GitHub patch sources", () => {
	function fixture() {
		const directory = mkdtempSync(join(tmpdir(), "nf-public-patch-base-"));
		const source = Buffer.from("original public GitHub binary");
		const commit = "a".repeat(40);
		const repository = "sourceowner/sourcefork";
		const name = "narrafork-1.2.0-linux-x64";
		const metadata = computeBinaryMetadataFromBuffer(name, source, {
			version: "1.2.0",
			platformId: "linux-x64",
			target: "bun-linux-x64",
			commit,
			repository,
			buildDate: "2026-10-08T00:00:00Z",
		});
		const sidecar = formatMetadataJson(metadata);
		const patchName = "narrafork-1.2.1-linux-x64.from-1.2.0.zstd-patch";
		const patchMeta = {
			fromVersion: "1.2.0",
			toVersion: "1.2.1",
			oldFileSize: source.length,
			oldFileSha512: metadata.sha512,
			stableEnd: 0,
			newTailSize: 20,
			patchSize: 10,
			newFileSize: 20,
			newFileSha512: createHash("sha512").update("target").digest("base64"),
		};
		const save = () =>
			writeFileSync(join(directory, `${patchName}.meta.json`), JSON.stringify(patchMeta));
		save();
		const calls: string[][] = [];
		const state = { draft: false, badDigest: false, missingPublic: false };
		const run = async (args: string[]) => {
			calls.push(args);
			if (
				args[0] !== "api" ||
				!args[1]?.startsWith(`repos/${repository}/`) ||
				args.some((arg) => ["--method", "-X", "-F", "-f", "--input"].includes(arg))
			)
				throw new Error("Forbidden server/write/full request");
			const path = args[1].slice(`repos/${repository}/`.length);
			if (path === "releases/tags/v1.2.0" && state.missingPublic)
				throw new Error("HTTP 404: public tag missing");
			if (path === "git/ref/tags/v1.2.0")
				return JSON.stringify({ ref: "refs/tags/v1.2.0", object: { type: "commit", sha: commit } });
			if (path === "releases/tags/v1.2.0")
				return JSON.stringify({
					id: 1,
					tag_name: "v1.2.0",
					draft: state.draft,
					prerelease: false,
					published_at: "2026-10-08T00:00:00Z",
				});
			if (path === "releases/1/assets?per_page=100&page=1")
				return JSON.stringify([
					{
						id: 10,
						name,
						size: source.length,
						state: "uploaded",
						digest: `sha256:${state.badDigest ? "b".repeat(64) : metadata.sha256}`,
					},
					{
						id: 11,
						name: `${name}.metadata.json`,
						size: Buffer.byteLength(sidecar),
						state: "uploaded",
						digest: `sha256:${createHash("sha256").update(sidecar).digest("hex")}`,
					},
				]);
			if (path === "releases/assets/11" && args.includes("Accept: application/octet-stream"))
				return sidecar;
			throw new Error(`Forbidden historical server/binary request: ${path}`);
		};
		const options = {
			distDir: directory,
			version: "1.2.1",
			repository: "SourceOwner/SourceFork",
			platformSuffixes: new Map([["linux-x64", "linux-x64"]]),
			run,
		};
		return { directory, options, calls, state, patchMeta, save };
	}
	test("accepts exact public original platform/oldsize/oldSHA without local base or server", async () => {
		const f = fixture();
		try {
			await validatePublishedPatchSources(f.options);
			expect(f.calls.length).toBeGreaterThan(0);
			expect(f.calls.some((args) => args[1].includes("releases/assets/10"))).toBe(false);
		} finally {
			rmSync(f.directory, { recursive: true, force: true });
		}
	});
	for (const mutation of [
		"oldFileSize",
		"oldFileSha512",
		"draft",
		"badDigest",
		"missingPublic",
	] as const) {
		test(`rejects ${mutation} instead of trusting server/local dist guesses`, async () => {
			const f = fixture();
			try {
				if (mutation === "oldFileSize") f.patchMeta.oldFileSize++;
				else if (mutation === "oldFileSha512")
					f.patchMeta.oldFileSha512 = createHash("sha512").update("different").digest("base64");
				else f.state[mutation] = true;
				f.save();
				await expect(validatePublishedPatchSources(f.options)).rejects.toThrow();
			} finally {
				rmSync(f.directory, { recursive: true, force: true });
			}
		});
	}
	test("offline GitHub patch dry-run refuses unverifiable source with zero GitHub calls", async () => {
		const f = fixture();
		try {
			await expect(validatePublishedPatchSources({ ...f.options, dryRun: true })).rejects.toThrow(
				"Offline GitHub dry-run",
			);
			expect(f.calls).toHaveLength(0);
		} finally {
			rmSync(f.directory, { recursive: true, force: true });
		}
	});
	test("parent cancellation prevents all GitHub source queries", async () => {
		const f = fixture();
		try {
			const controller = new AbortController();
			controller.abort();
			await expect(
				validatePublishedPatchSources({ ...f.options, signal: controller.signal }),
			).rejects.toThrow();
			expect(f.calls).toHaveLength(0);
		} finally {
			rmSync(f.directory, { recursive: true, force: true });
		}
	});
	test("oversized local patch sidecar is bounded before GitHub reads", async () => {
		const f = fixture();
		try {
			writeFileSync(
				join(f.directory, "narrafork-1.2.1-linux-x64.from-1.2.0.zstd-patch.meta.json"),
				Buffer.alloc(64 * 1024 + 1),
			);
			await expect(validatePublishedPatchSources(f.options)).rejects.toThrow("metadata size limit");
			expect(f.calls).toHaveLength(0);
		} finally {
			rmSync(f.directory, { recursive: true, force: true });
		}
	});
	test("local post-process is full-only even when dist contains a plausible older binary", async () => {
		const directory = mkdtempSync(join(tmpdir(), "nf-full-only-worker-"));
		const name = "narrafork-1.2.1-linux-x64";
		const baseline = join(directory, "narrafork-1.2.0-linux-x64");
		writeFileSync(baseline, "unverified dist guess");
		writeFileSync(join(directory, name), "new binary");
		const worker = new Worker(resolve(import.meta.dir, "../../scripts/post-process-worker.ts"), {
			workerData: {
				platform: { target: "bun-linux-x64", platformId: "linux-x64", name },
				distDir: directory,
				root: directory,
				version: "1.2.1",
				commit: "a".repeat(40),
				repository: "SourceOwner/SourceFork",
				helperDistribution: createHelperDistributionDependencies(),
			},
		});
		try {
			await new Promise<void>((resolve, reject) => {
				const timer = setTimeout(() => reject(new Error("Worker timeout")), 10_000);
				worker.on("error", (error) => {
					clearTimeout(timer);
					reject(error);
				});
				worker.on("exit", (code) => {
					clearTimeout(timer);
					code === 0 ? resolve() : reject(new Error(`Worker exit ${code}`));
				});
			});
			expect(existsSync(join(directory, `${name}.metadata.json`))).toBe(true);
			expect(existsSync(join(directory, `${name}.zstd-patch`))).toBe(false);
			expect(readFileSync(baseline, "utf8")).toBe("unverified dist guess");
			const workerSource = readFileSync(
				resolve(import.meta.dir, "../../scripts/post-process-worker.ts"),
				"utf8",
			);
			expect(workerSource).not.toContain("findPreviousVersionBinary");
			expect(workerSource).not.toContain("generateZstdPatch");
		} finally {
			await worker.terminate();
			rmSync(directory, { recursive: true, force: true });
		}
	});
});
