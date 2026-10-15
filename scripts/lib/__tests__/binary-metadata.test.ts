import { describe, expect, test } from "bun:test";
import { createHash } from "node:crypto";
import { createHelperDistributionDependencies } from "../../../shared/helper-distribution";
import { CURRENT_RECOVERY_MANIFEST_VERSIONS } from "../../../shared/update-identity";
import {
	type BinaryMetadata,
	computeBinaryMetadataFromBuffer,
	formatChecksumsReport,
	formatMetadataJson,
	formatSha256Sums,
} from "../binary-metadata";

const INPUT = {
	version: "0.5.0",
	platformId: "linux-x64",
	target: "bun-linux-x64",
	commit: "abc1234",
	buildDate: "2026-01-02T03:04:05.000Z",
};

describe("computeBinaryMetadataFromBuffer", () => {
	test("computes correct size and both digests", () => {
		const buf = Buffer.from("narrafork binary contents");
		const meta = computeBinaryMetadataFromBuffer("narrafork-0.5.0-linux-x64", buf, INPUT);

		expect(meta.name).toBe("narrafork-0.5.0-linux-x64");
		expect(meta.platform).toBe("linux-x64");
		expect(meta.target).toBe("bun-linux-x64");
		expect(meta.version).toBe("0.5.0");
		expect(meta.commit).toBe("abc1234");
		expect(meta.buildDate).toBe("2026-01-02T03:04:05.000Z");
		expect(meta.size).toBe(buf.length);
		expect(meta.sha256).toBe(createHash("sha256").update(buf).digest("hex"));
		expect(meta.sha512).toBe(createHash("sha512").update(buf).digest("base64"));
	});

	test("application upgrades retain the independently pinned component identity", () => {
		const helperDistribution = createHelperDistributionDependencies();
		for (const version of ["0.9.2", "0.9.3", "1.0.0"]) {
			const meta = computeBinaryMetadataFromBuffer(
				`narrafork-${version}-linux-x64`,
				Buffer.from(`application ${version}`),
				{ ...INPUT, version, helperDistribution },
			);
			expect(meta.version).toBe(version);
			expect(JSON.parse(formatMetadataJson(meta)).helperDistribution).toEqual(helperDistribution);
			expect(meta.helperDistribution?.executor.version).toBe("0.9.1");
			expect(meta.helperDistribution?.executor.tag).toBe("executor-v0.9.1");
		}
	});

	test("legacy sidecars stay undeclared and malformed declarations cannot become legacy", () => {
		const legacy = computeBinaryMetadataFromBuffer("legacy", Buffer.from("old"), INPUT);
		expect(Object.hasOwn(legacy, "helperDistribution")).toBe(false);
		const declared = createHelperDistributionDependencies();
		expect(() =>
			computeBinaryMetadataFromBuffer("invalid", Buffer.from("bad"), {
				...INPUT,
				helperDistribution: {
					...declared,
					executor: { ...declared.executor, tag: "executor-latest" },
				},
			}),
		).toThrow();
	});

	test("only explicit main-program declarations serialize recovery capabilities", () => {
		const helperDistribution = createHelperDistributionDependencies();
		const meta = computeBinaryMetadataFromBuffer("main", Buffer.from("binary"), {
			...INPUT,
			helperDistribution,
			recoveryManifestVersions: CURRENT_RECOVERY_MANIFEST_VERSIONS,
		});
		expect(JSON.parse(formatMetadataJson(meta)).recoveryManifestVersions).toEqual([1, 2, 3]);
		expect(meta.helperDistribution).toEqual(helperDistribution);
		for (const name of ["legacy-main", "narrafork-executor", "helper"]) {
			const legacy = computeBinaryMetadataFromBuffer(name, Buffer.from("binary"), INPUT);
			expect(
				Object.hasOwn(JSON.parse(formatMetadataJson(legacy)), "recoveryManifestVersions"),
			).toBe(false);
		}
		for (const versions of [[], [1, 2, 3, 3], [0], [256], [1.5]]) {
			expect(() =>
				computeBinaryMetadataFromBuffer("bad", Buffer.from("binary"), {
					...INPUT,
					recoveryManifestVersions: versions,
				}),
			).toThrow("recoveryManifestVersions");
			expect(() => formatMetadataJson({ ...meta, recoveryManifestVersions: versions })).toThrow(
				"recoveryManifestVersions",
			);
		}
		expect(
			JSON.parse(formatMetadataJson({ ...meta, recoveryManifestVersions: [3, 1, 2] }))
				.recoveryManifestVersions,
		).toEqual([1, 2, 3]);
	});

	test("sha256 is lowercase 64-char hex", () => {
		const meta = computeBinaryMetadataFromBuffer("x", Buffer.from("abc"), INPUT);
		expect(meta.sha256).toMatch(/^[0-9a-f]{64}$/);
	});
});

describe("formatMetadataJson", () => {
	test("is valid JSON round-trippable and newline-terminated", () => {
		const meta = computeBinaryMetadataFromBuffer("bin", Buffer.from("data"), INPUT);
		const json = formatMetadataJson(meta);
		expect(json.endsWith("\n")).toBe(true);
		expect(JSON.parse(json)).toEqual(meta);
	});
});

describe("formatSha256Sums", () => {
	function meta(name: string, content: string): BinaryMetadata {
		return computeBinaryMetadataFromBuffer(name, Buffer.from(content), INPUT);
	}

	test("produces `sha256sum -c` compatible lines (hex, two spaces, name)", () => {
		const entries = [meta("narrafork-0.5.0-linux-x64", "one")];
		const out = formatSha256Sums(entries);
		const expectedHex = createHash("sha256").update(Buffer.from("one")).digest("hex");
		expect(out).toBe(`${expectedHex}  narrafork-0.5.0-linux-x64\n`);
		// Exactly two spaces between hash and filename (binary marker).
		expect(out).toMatch(/^[0-9a-f]{64} {2}\S/);
	});

	test("sorts entries by filename for stable output", () => {
		const out = formatSha256Sums([
			meta("narrafork-0.5.0-windows-x64.exe", "w"),
			meta("narrafork-0.5.0-linux-x64", "l"),
			meta("narrafork-0.5.0-macos-arm64", "m"),
		]);
		const names = out
			.trim()
			.split("\n")
			.map((line) => line.split("  ")[1]);
		expect(names).toEqual([
			"narrafork-0.5.0-linux-x64",
			"narrafork-0.5.0-macos-arm64",
			"narrafork-0.5.0-windows-x64.exe",
		]);
	});
});

describe("formatChecksumsReport", () => {
	test("includes version, commit, build date, verify hint and per-binary fields", () => {
		const entries = [
			computeBinaryMetadataFromBuffer("narrafork-0.5.0-linux-x64", Buffer.from("l"), INPUT),
			computeBinaryMetadataFromBuffer("narrafork-0.5.0-macos-arm64", Buffer.from("m"), {
				...INPUT,
				platformId: "darwin-arm64",
				target: "bun-darwin-arm64",
			}),
		];
		const report = formatChecksumsReport("0.5.0", entries);

		expect(report).toContain("NarraFork v0.5.0");
		expect(report).toContain("Commit:     abc1234");
		expect(report).toContain("Build date: 2026-01-02T03:04:05.000Z");
		expect(report).toContain("sha256sum -c narrafork-0.5.0-SHA256SUMS");
		expect(report).toContain("narrafork-0.5.0-linux-x64");
		expect(report).toContain("platform : darwin-arm64");
		expect(report).toContain("sha256   :");
		expect(report).toContain("sha512   :");
		expect(report.endsWith("\n")).toBe(true);
	});
});
