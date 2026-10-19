/**
 * Global test preload.
 *
 * Two responsibilities must happen in this order:
 *  1. Isolate every test process from the developer's real ~/.narrafork data.
 *  2. Keep the settings singleton valid when tests mutate its nested objects.
 *
 * Loaded via `bunfig.toml` → `[test].preload` before test modules are imported.
 */

import { afterAll, afterEach, beforeEach } from "bun:test";
import { mkdtempSync, realpathSync, rmSync } from "node:fs";
import { realpath } from "node:fs/promises";
import { homedir, tmpdir } from "node:os";
import { resolve } from "node:path";

function normalizedPath(path: string): string {
	const resolved = resolve(path);
	return process.platform === "win32" ? resolved.toLowerCase() : resolved;
}

function canonicalPath(path: string): string {
	try {
		return normalizedPath(realpathSync(path));
	} catch {
		return normalizedPath(path);
	}
}

const originalHome = homedir();
const realNarraforkHome = canonicalPath(resolve(originalHome, ".narrafork"));
const requestedNarraforkHome = process.env.NARRAFORK_HOME?.trim();
const allowExplicitTestHome = process.env.NARRAFORK_ALLOW_MULTIPLE === "1";
const requestedCanonicalHome = requestedNarraforkHome
	? canonicalPath(requestedNarraforkHome)
	: undefined;

if (requestedCanonicalHome && requestedCanonicalHome === realNarraforkHome) {
	throw new Error(
		`Refusing to run tests against the real NarraFork data directory: ${realNarraforkHome}`,
	);
}
if (requestedNarraforkHome && !allowExplicitTestHome) {
	throw new Error(
		"NARRAFORK_HOME is only accepted by tests with NARRAFORK_ALLOW_MULTIPLE=1 and an isolated path",
	);
}

// Keep HOME isolated for child processes and code that consults the environment.
// Core NarraFork paths use NARRAFORK_HOME explicitly because Bun's os.homedir()
// is resolved from the host account rather than being changed at runtime.
// Windows tmpdir() can contain an 8.3 profile alias (RUNNER~1). Test consumers
// require canonical private roots; resolve only this newly-created, owned directory.
// Use the same async API as the runtime: Bun Windows realpathSync can retain aliases.
const isolatedHome = await realpath(mkdtempSync(resolve(tmpdir(), "narrafork-test-")));
const isolatedNarraforkHome = requestedNarraforkHome
	? resolve(requestedNarraforkHome)
	: resolve(isolatedHome, ".narrafork");

// Keep the original home dir reachable for code that must distinguish the real
// user home from the isolated test home. `os.homedir()` follows HOME/USERPROFILE
// on Bun (as on Node), so after the overrides below it no longer resolves to the
// host account — server/db/connection.ts consults this backup to refuse opening
// the real database from a test process.
process.env.NARRAFORK_ORIGINAL_HOME = originalHome;
process.env.HOME = isolatedHome;
process.env.USERPROFILE = isolatedHome;
process.env.NARRAFORK_HOME = isolatedNarraforkHome;
process.env.NARRAFORK_TEST = "1";

// Seed the isolated database with an already-migrated copy so the ~200-migration replay is not
// repeated by every database-backed test file. Startup still runs its full sequence against it.
// Explicit NARRAFORK_HOME callers own their directory and are left untouched.
let sqliteSeed: import("./sqlite-template").SqliteSeedOutcome | "explicit-home" = "explicit-home";
if (!requestedNarraforkHome) {
	const { seedSqliteTestDatabase } = await import("./sqlite-template");
	sqliteSeed = seedSqliteTestDatabase(isolatedNarraforkHome);
}

export const testEnvironment = Object.freeze({
	originalHome,
	realNarraforkHome,
	isolatedHome,
	narraforkHome: isolatedNarraforkHome,
	/** What the preload did with the pre-migrated SQLite template for this process. */
	sqliteSeed,
});

// This import must remain dynamic and below the environment setup. A static import
// is evaluated before this module body and would load the real settings first.
const { getDefaults, settings } = await import("@server/lib/settings");

// Ordinary agent tests must not spawn a real recording worker for every isolated file.
// Recording tests opt in explicitly; the shipped DEFAULTS remain enabled.
settings.requestEvidence.ringEnabled = false;

// Capture the isolated live settings at preload time. This is the authoritative
// state that other modules (e.g. auth.ts) will read from.
const snapshot = structuredClone(settings);
// getDefaults() deep-clones the entire configuration; its fixed top-level keys
// do not need to be rediscovered for every individual test.
const defaultKeys = Object.keys(getDefaults()) as Array<keyof typeof settings>;

export function repairMissingSettingsBranches(): void {
	// Restore any sub-objects that may have been wiped by a previous test.
	for (const key of defaultKeys) {
		if (settings[key] === undefined || settings[key] === null) {
			// Use snapshot value (preserves runtime-generated secrets like jwtSecret)
			// biome-ignore lint/suspicious/noExplicitAny: restoring dynamic settings keys
			(settings as any)[key] = structuredClone((snapshot as any)[key]);
		}
	}
}

beforeEach(repairMissingSettingsBranches);

afterEach(() => {
	// Full restore from the original snapshot taken at module load time.
	for (const key of Object.keys(snapshot) as Array<keyof typeof snapshot>) {
		// biome-ignore lint/suspicious/noExplicitAny: restoring dynamic settings keys
		(settings as any)[key] = structuredClone((snapshot as any)[key]);
	}
});

afterAll(() => {
	// The temporary HOME is always owned by this preload, even when an explicit
	// isolated NARRAFORK_HOME was supplied for a database-heavy test.
	// On Windows an open bun:sqlite handle can still hold the directory at this
	// point (EBUSY); the OS temp dir reclaims it later, so a failed sweep is only
	// a warning, never a test failure.
	try {
		rmSync(isolatedHome, { recursive: true, force: true, maxRetries: 2 });
	} catch (error) {
		console.warn(`Failed to clean isolated test home: ${String(error)}`);
	}
});
