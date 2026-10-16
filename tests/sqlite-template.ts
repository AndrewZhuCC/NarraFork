/**
 * Pre-migrated SQLite template for test processes.
 *
 * Every test file that imports `@server/db` used to replay the full migration history (~200 SQL
 * files, ~0.6-0.8 s of CPU) into its own empty database. With `--isolate` that cost is paid once
 * per file, and on the 4-vCPU CI runners it is a large share of the whole suite.
 *
 * The preload now seeds the isolated `narrafork.db` with a byte copy of a database that has already
 * gone through the real startup sequence once. Nothing about the code under test changes: module
 * evaluation of `@server/db` still runs the full lifecycle (lock, marker, migrations by hash,
 * ensureColumns, backfills, FTS), it simply finds every migration already applied.
 *
 * Correctness of the cache key is what matters here. The key hashes the CONTENT of every input
 * that decides the resulting schema: migration SQL, the journal, and the database startup
 * sources. Any edit produces a new template; a stale template cannot be reused.
 *
 * The template is created by a child process (the same Bun binary, same preload-free startup),
 * written to a temporary name and atomically renamed, so concurrent test workers either see a
 * complete template or build their own. A failure falls back to the old behaviour: the test
 * process simply migrates from scratch.
 */
import { spawnSync } from "node:child_process";
import {
	copyFileSync,
	existsSync,
	mkdirSync,
	mkdtempSync,
	readdirSync,
	readFileSync,
	renameSync,
	rmSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";

const REPO_ROOT = resolve(import.meta.dir, "..");
/** Directories whose files decide the migrated schema. Tests are excluded. */
const SCHEMA_SOURCE_DIRS = ["server/db", "server/db/backend"];
/**
 * Single files that also shape the template: this module (it holds the build recipe, so editing
 * how the template is produced invalidates old ones) and the dependency lock (Drizzle, the
 * migrator and bun:sqlite behaviour come from there).
 */
const RECIPE_FILES = ["tests/sqlite-template.ts", "bun.lock"];
const BUILD_TIMEOUT_MS = 60_000;

/**
 * Content hash of every input that shapes a freshly started database. Hashing ~1 MiB costs ~10 ms;
 * `--isolate` resets globals and the environment between files, so it is simply recomputed.
 */
export function sqliteTemplateKey(root = REPO_ROOT): string {
	const hash = new Bun.CryptoHasher("sha256");
	const add = (relative: string) => {
		hash.update(relative);
		hash.update("\0");
		hash.update(readFileSync(join(root, relative)));
		hash.update("\0");
	};
	for (const name of readdirSync(join(root, "drizzle")).sort()) {
		if (name.endsWith(".sql")) add(`drizzle/${name}`);
	}
	add("drizzle/meta/_journal.json");
	for (const dir of SCHEMA_SOURCE_DIRS) {
		for (const name of readdirSync(join(root, dir)).sort()) {
			if (name.endsWith(".ts") && !name.includes(".test.")) add(`${dir}/${name}`);
		}
	}
	for (const file of RECIPE_FILES) add(file);
	hash.update(Bun.version);
	return hash.digest("hex").slice(0, 24);
}

/** Where the template for the current sources lives (it may not exist yet). */
export function sqliteTemplatePath(): string {
	return join(templateDir(), `narrafork-${sqliteTemplateKey()}.db`);
}

function templateDir(): string {
	return resolve(
		process.env.NF_TEST_SQLITE_TEMPLATE_DIR?.trim() || join(tmpdir(), "narrafork-test-templates"),
	);
}

/**
 * The child runs the real database startup against a private home and checkpoints the WAL into the
 * main file so a single-file copy is complete. It deliberately does NOT write the clean-shutdown
 * marker: every test database must keep starting on the unclean path it always used.
 */
const BUILD_SCRIPT = `
const { db, sqlite, releaseDatabaseInstanceLockOnly } = await import("@server/db");
if (!db) throw new Error("database did not start");
sqlite.run("PRAGMA wal_checkpoint(TRUNCATE)");
sqlite.run("PRAGMA journal_mode = DELETE");
releaseDatabaseInstanceLockOnly();
sqlite.close();
process.exit(0);
`;

function buildTemplate(target: string, script: string): boolean {
	const dir = dirname(target);
	mkdirSync(dir, { recursive: true, mode: 0o700 });
	const home = mkdtempSync(join(dir, "build-"));
	try {
		const result = spawnSync(process.execPath, ["--eval", script], {
			cwd: REPO_ROOT,
			env: {
				...process.env,
				HOME: home,
				USERPROFILE: home,
				NARRAFORK_HOME: join(home, ".narrafork"),
				NARRAFORK_ALLOW_MULTIPLE: "1",
				NARRAFORK_TEST: "1",
				NODE_ENV: "test",
			},
			stdio: ["ignore", "ignore", "pipe"],
			timeout: BUILD_TIMEOUT_MS,
			maxBuffer: 1024 * 1024,
		});
		const built = join(home, ".narrafork", "narrafork.db");
		if (result.status !== 0 || !existsSync(built)) {
			console.warn(
				`[test preload] SQLite template build failed (status ${result.status}); migrating per file instead. ${String(result.stderr ?? "").slice(-2000)}`,
			);
			return false;
		}
		// Atomic publish: concurrent workers either see a complete file or none.
		const staged = `${target}.${process.pid}.${Date.now()}.tmp`;
		copyFileSync(built, staged);
		renameSync(staged, target);
		return true;
	} finally {
		rmSync(home, { recursive: true, force: true });
	}
}

/** Why seeding did or did not happen; recorded by the preload so a guard can tell. */
export type SqliteSeedOutcome = "seeded" | "disabled" | "exists" | "unavailable";

/** Test seams only: the guard test drives a private template path and a failing build. */
export interface SqliteSeedOptions {
	templatePath?: string;
	buildScript?: string;
	env?: Record<string, string | undefined>;
}

/**
 * Seed `narraforkHome/narrafork.db` from the template. Anything but "seeded" leaves the
 * directory untouched, so the database startup simply migrates from scratch as before.
 */
export function seedSqliteTestDatabase(
	narraforkHome: string,
	options: SqliteSeedOptions = {},
): SqliteSeedOutcome {
	const env = options.env ?? process.env;
	if (env.NF_TEST_SQLITE_TEMPLATE === "0") return "disabled";
	const destination = join(narraforkHome, "narrafork.db");
	if (existsSync(destination)) return "exists";
	try {
		const target = options.templatePath ?? sqliteTemplatePath();
		if (!existsSync(target) && !buildTemplate(target, options.buildScript ?? BUILD_SCRIPT))
			return "unavailable";
		mkdirSync(narraforkHome, { recursive: true, mode: 0o700 });
		copyFileSync(target, destination);
		return "seeded";
	} catch (error) {
		console.warn(`[test preload] SQLite template unavailable: ${String(error)}`);
		return "unavailable";
	}
}
