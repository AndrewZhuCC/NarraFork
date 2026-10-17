/**
 * Worker thread for post-processing a single platform binary.
 * Runs signing and SHA-512/full metadata generation in an isolated thread
 * so multiple platforms can be processed in true parallelism.
 *
 * Communication: parentPort.postMessage({ type: "log" | "done", ... })
 */
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { existsSync, readFileSync, statSync, writeFileSync } from "node:fs";
import { homedir } from "node:os";
import { join, relative } from "node:path";
import { parentPort, workerData } from "node:worker_threads";
import {
	type HelperDistributionDependencies,
	parseHelperDistributionDependencies,
} from "../shared/helper-distribution";
import {
	type BinaryMetadata,
	computeBinaryMetadata,
	formatMetadataJson,
} from "./lib/binary-metadata";
import { computeCiBinaryMetadata, runCiBuildCommand, signCiBinary } from "./lib/ci-build-strict";

interface WorkerInput {
	platform: { target: string; platformId: string; name: string };
	distDir: string;
	root: string;
	version: string;
	commit: string;
	repository: string;
	helperDistribution: HelperDistributionDependencies;
	recoveryManifestVersions?: readonly number[];
	releaseCi?: boolean;
}

const {
	platform,
	distDir,
	root,
	version,
	commit,
	repository,
	helperDistribution,
	recoveryManifestVersions,
	releaseCi = false,
} = workerData as WorkerInput;

if (!parentPort) throw new Error("Post-processing must run inside a worker");
const port = parentPort;
function log(message: string) {
	port.postMessage({ type: "log", message });
}

// ---------------------------------------------------------------------------
// Ad-hoc codesign (macOS)
// ---------------------------------------------------------------------------

function findRcodesign(): string | null {
	try {
		const check = Bun.spawnSync(["rcodesign", "--version"], {
			stdout: "pipe",
			stderr: "pipe",
		});
		if (check.exitCode === 0) return "rcodesign";
	} catch {}

	const localPath = join(homedir(), ".narrafork", "bin", "rcodesign");
	if (existsSync(localPath)) {
		try {
			const localCheck = Bun.spawnSync([localPath, "--version"], {
				stdout: "pipe",
				stderr: "pipe",
			});
			if (localCheck.exitCode === 0) return localPath;
		} catch {}
	}

	return null;
}

function adHocSign(filePath: string): boolean {
	const rcodesign = findRcodesign();
	if (rcodesign) {
		const result = Bun.spawnSync([rcodesign, "sign", filePath], {
			stdout: "pipe",
			stderr: "pipe",
		});
		if (result.exitCode === 0) {
			log(`✓ Ad-hoc signed (rcodesign): ${relative(root, filePath)}`);
			return true;
		}
		const stderr = new TextDecoder().decode(result.stderr);
		log(`⚠ rcodesign failed: ${stderr.trim()}`);
	}

	if (process.platform === "darwin") {
		const result = Bun.spawnSync(["codesign", "--force", "--sign", "-", filePath], {
			stdout: "pipe",
			stderr: "pipe",
		});
		if (result.exitCode === 0) {
			log(`✓ Ad-hoc signed (codesign): ${relative(root, filePath)}`);
			return true;
		}
	}

	return false;
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

const outfile = join(distDir, platform.name);
const buildDate = new Date().toISOString();

// 1. macOS signing (must happen before hashing so digests match the final file)
if (releaseCi) {
	await signCiBinary(platform.target, process.platform, outfile, runCiBuildCommand);
} else if (platform.target.includes("darwin")) {
	const identity = process.env.NF_CODESIGN_IDENTITY;
	const keychain = process.env.NF_CODESIGN_KEYCHAIN;
	const identifier = process.env.NF_CODESIGN_IDENTIFIER;
	const configured = [identity, keychain, identifier].some((value) => value !== undefined);
	if (configured) {
		if (
			process.platform !== "darwin" ||
			!identity ||
			!/^[a-f\d]{40}$/i.test(identity) ||
			!keychain ||
			!existsSync(keychain) ||
			!identifier ||
			!/^[a-z\d.-]+$/i.test(identifier)
		) {
			throw new Error("Invalid NF_CODESIGN configuration; refusing ad-hoc fallback");
		}
		const options = { timeout: 120_000, maxBuffer: 128 * 1024 };
		execFileSync(
			"/usr/bin/codesign",
			[
				"--force",
				"--sign",
				identity,
				"--keychain",
				keychain,
				"--identifier",
				identifier,
				"--timestamp=none",
				outfile,
			],
			options,
		);
		execFileSync(
			"/usr/bin/codesign",
			[
				"--verify",
				"--strict",
				"-R",
				`=identifier "${identifier}" and certificate leaf = H"${identity}"`,
				outfile,
			],
			options,
		);
		log(`✓ Identity signed and verified: ${identifier} (${relative(root, outfile)})`);
	} else if (!adHocSign(outfile)) {
		log(
			`⚠ Ad-hoc signing failed — users may need to run: codesign --force --sign - ${relative(root, outfile)}`,
		);
	}
}

// 2. SHA-512 (kept for latest*.yml) + verifiable sidecar metadata
const metadataInput = {
	version,
	platformId: platform.platformId,
	target: platform.target,
	commit,
	repository,
	helperDistribution: parseHelperDistributionDependencies(helperDistribution),
	recoveryManifestVersions,
	buildDate,
};
const strictMetadata = releaseCi
	? await computeCiBinaryMetadata(outfile, metadataInput)
	: undefined;
const fileSha512 =
	strictMetadata?.sha512 ?? createHash("sha512").update(readFileSync(outfile)).digest("base64");
const fileSize = strictMetadata?.size ?? statSync(outfile).size;

let metadata: BinaryMetadata | undefined;
try {
	metadata = strictMetadata ?? computeBinaryMetadata(outfile, metadataInput);
	const metadataPath = `${outfile}.metadata.json`;
	writeFileSync(metadataPath, formatMetadataJson(metadata));
	log(`✓ Metadata: ${relative(root, metadataPath)} (sha256 ${metadata.sha256.slice(0, 12)}…)`);
} catch (err) {
	if (releaseCi) throw err;
	const message = err instanceof Error ? err.message : String(err);
	log(`⚠ Metadata generation failed for ${platform.platformId}: ${message}`);
}

// 3. Full-only local build. Release CI assembles patches separately, once,
// using verified original binaries from public GitHub Releases. A dist filename
// is never evidence of a published base, and mirrors never generate patches.
log("Full-only post-processing; canonical patches are generated by GitHub Release CI");
// 4. Done — send latestYml back
function getLatestYmlName(target: string): string {
	if (target.includes("darwin")) return "latest-mac.yml";
	if (target.includes("windows")) return "latest.yml";
	return "latest-linux.yml";
}

const latestYml = {
	name: getLatestYmlName(platform.target),
	version,
	releaseDate: buildDate,
	file: { url: platform.name, size: fileSize, sha512: fileSha512 },
};

port.postMessage({ type: "done", latestYml, metadata });
