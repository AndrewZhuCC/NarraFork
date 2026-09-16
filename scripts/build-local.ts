/**
 * Selfuse macOS build: reuse the complete build pipeline, with a stable signing identity.
 * Requires the existing local identity/keychain; never creates certificates or changes trust.
 * Usage: bun scripts/build-local.ts
 */
import { execFileSync, spawnSync } from "node:child_process";
import { existsSync, readFileSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";

if (process.platform !== "darwin" || process.argv.length > 2) {
	throw new Error("Run on macOS: bun scripts/build-local.ts (full frontend build required)");
}
const root = join(import.meta.dir, "..");
const signingDir = join(homedir(), ".narrafork", "code-signing");
const identity = JSON.parse(readFileSync(join(signingDir, "identity.json"), "utf8"));
if (
	typeof identity.fingerprint !== "string" ||
	!/^[a-f\d]{40}$/i.test(identity.fingerprint) ||
	typeof identity.keychain !== "string" ||
	/[\r\n\0]/.test(identity.keychain) ||
	!existsSync(identity.keychain)
) {
	throw new Error("Invalid or missing selfuse signing identity/keychain");
}
const password = readFileSync(join(signingDir, "keychain-password"), "utf8").trim();
if (!/^[a-f\d]{64}$/i.test(password)) {
	throw new Error("Invalid local keychain password file");
}
const commandOptions = { encoding: "utf8" as const, timeout: 30_000, maxBuffer: 64 * 1024 };
// Feed the password over stdin, never argv, environment, or build output.
try {
	const unlocked = spawnSync("/usr/bin/security", ["-i"], {
		...commandOptions,
		input: `unlock-keychain -p ${password} ${JSON.stringify(identity.keychain)}\n`,
		stdio: ["pipe", "pipe", "pipe"],
	});
	if (
		unlocked.status !== 0 ||
		unlocked.error ||
		`${unlocked.stdout}${unlocked.stderr}`.includes("security:")
	) {
		throw new Error("unlock failed");
	}
} catch {
	throw new Error("Could not unlock the selfuse signing keychain; build stopped");
}
function keychains(): string[] {
	return execFileSync("/usr/bin/security", ["list-keychains", "-d", "user"], commandOptions)
		.trim()
		.split("\n")
		.filter((line) => line.trim())
		.map((line) => JSON.parse(line.trim()));
}
function setKeychains(paths: string[]) {
	execFileSync(
		"/usr/bin/security",
		["list-keychains", "-d", "user", "-s", ...paths],
		commandOptions,
	);
}
const before = keychains();
const added = !before.includes(identity.keychain);
// ponytail: local builds are serial; add a cross-process lock before supporting concurrent builds.
try {
	// codesign --keychain still needs the search list to resolve the certificate chain.
	if (added) setKeychains([...before, identity.keychain]);
	const build = Bun.spawnSync(
		[process.execPath, "scripts/build-cross-platform.ts", `--platform=darwin-${process.arch}`],
		{
			cwd: root,
			stdio: ["inherit", "inherit", "inherit"],
			timeout: 30 * 60_000,
			env: {
				...process.env,
				NF_CODESIGN_IDENTITY: identity.fingerprint,
				NF_CODESIGN_KEYCHAIN: identity.keychain,
				NF_CODESIGN_IDENTIFIER: "app.narrafork.selfuse",
			},
		},
	);
	if (build.exitCode !== 0) throw new Error("Selfuse build failed; do not install its output");
} finally {
	// Preserve other entries, including concurrent user changes; remove only our addition.
	if (added) setKeychains(keychains().filter((path) => path !== identity.keychain));
}
