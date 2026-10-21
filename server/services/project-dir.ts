/**
 * project-dir.ts — the default project directory is the user's choice, and it must exist.
 *
 * NarraFork used to default `paths.defaultProjectDir` to `~/projects` and `mkdir -p` it on
 * first use. Most machines have no such folder (and people name theirs `Projects`, `code`,
 * `dev`, `src`, ...), so the first session silently landed in a directory the user never
 * picked. Now the setting starts unset, nothing creates it, and every consumer asks this
 * module whether the configured value names an existing directory.
 *
 * Candidate detection only LOOKS: it lists the home directory's direct children and
 * counts git repositories one level below them, with hard caps on both, so a huge home
 * directory costs a bounded number of async stats (never sync work on the main thread).
 */

import { type Dirent, statSync } from "node:fs";
import { readdir, stat } from "node:fs/promises";
import { join, resolve } from "node:path";

export type ProjectDirState = "valid" | "unset" | "missing" | "not_directory";

export interface ProjectDirStatus {
	/** The configured value as stored (may be empty). */
	configured: string;
	/** Absolute, `~`-expanded path; null when unset or unresolvable. */
	path: string | null;
	state: ProjectDirState;
}

/** Expand a bare `~` or `~/` prefix. `~user/x` cannot be resolved here, so it is null. */
export function expandHomePath(value: string, home: string): string | null {
	if (value === "~") return home;
	if (value.startsWith("~/") || value.startsWith("~\\")) return `${home}${value.slice(1)}`;
	if (value.startsWith("~")) return null;
	return value;
}

/** Validity of a configured default project directory. One `stat`, safe on request paths. */
export function getProjectDirStatus(
	configured: string | undefined,
	home: string,
): ProjectDirStatus {
	const value = configured?.trim() ?? "";
	if (!value) return { configured: value, path: null, state: "unset" };
	const expanded = expandHomePath(value, home);
	if (!expanded) return { configured: value, path: null, state: "missing" };
	const path = resolve(expanded);
	try {
		return {
			configured: value,
			path,
			state: statSync(path).isDirectory() ? "valid" : "not_directory",
		};
	} catch {
		return { configured: value, path, state: "missing" };
	}
}

/** The directory to use when a client sends none, or null when the user must choose. */
export function usableProjectDir(configured: string | undefined, home: string): string | null {
	const status = getProjectDirStatus(configured, home);
	return status.state === "valid" ? status.path : null;
}

export interface ProjectDirCandidate {
	path: string;
	/** Git repositories found directly inside (capped at {@link MAX_REPOS_COUNTED}). */
	repoCount: number;
	/** Matched a conventional name such as `projects` or `code` (case-insensitive). */
	conventional: boolean;
}

/** Folder names people commonly keep their checkouts in, compared case-insensitively. */
const CONVENTIONAL_NAMES = new Set([
	"projects",
	"project",
	"code",
	"coding",
	"dev",
	"develop",
	"development",
	"developer",
	"src",
	"source",
	"sources",
	"workspace",
	"workspaces",
	"repos",
	"repositories",
	"git",
	"github",
	"work",
]);

/** Tool defaults one level deeper: GitHub Desktop and Visual Studio. */
const NESTED_CONVENTIONAL = [
	["Documents", "GitHub"],
	["source", "repos"],
];

const MAX_HOME_ENTRIES = 500;
const MAX_DIRS_SCANNED = 60;
const MAX_CHILDREN_PER_DIR = 200;
const MAX_REPOS_COUNTED = 50;
const MAX_CANDIDATES = 8;

async function listDirs(path: string, cap: number): Promise<Dirent[]> {
	try {
		const entries = await readdir(path, { withFileTypes: true });
		return entries.slice(0, cap).filter((entry) => entry.isDirectory() || entry.isSymbolicLink());
	} catch {
		return [];
	}
}

async function isDirectory(path: string): Promise<boolean> {
	try {
		return (await stat(path)).isDirectory();
	} catch {
		return false;
	}
}

async function countRepos(dir: string): Promise<number> {
	let count = 0;
	for (const child of await listDirs(dir, MAX_CHILDREN_PER_DIR)) {
		if (child.name.startsWith(".")) continue;
		if (await isDirectory(join(dir, child.name, ".git"))) count++;
		if (count >= MAX_REPOS_COUNTED) break;
	}
	return count;
}

/**
 * Existing directories under `home` that look like where this user keeps code: conventional
 * names (any case) plus any folder already holding two or more git repositories. Ranked by
 * repository count, conventional names first on ties.
 */
export async function findProjectDirCandidates(home: string): Promise<ProjectDirCandidate[]> {
	const scanned = new Map<string, ProjectDirCandidate>();
	const consider = async (path: string, conventional: boolean) => {
		if (scanned.has(path) || scanned.size >= MAX_DIRS_SCANNED) return;
		if (!(await isDirectory(path))) return;
		scanned.set(path, { path, repoCount: await countRepos(path), conventional });
	};

	const homeDirs = (await listDirs(home, MAX_HOME_ENTRIES)).filter(
		(entry) => !entry.name.startsWith("."),
	);
	// Conventional names first so the scan budget never starves them.
	const ordered = [...homeDirs].sort(
		(a, b) =>
			Number(CONVENTIONAL_NAMES.has(b.name.toLowerCase())) -
			Number(CONVENTIONAL_NAMES.has(a.name.toLowerCase())),
	);
	for (const parts of NESTED_CONVENTIONAL) await consider(join(home, ...parts), true);
	for (const entry of ordered)
		await consider(join(home, entry.name), CONVENTIONAL_NAMES.has(entry.name.toLowerCase()));

	return [...scanned.values()]
		.filter((candidate) => candidate.conventional || candidate.repoCount >= 2)
		.sort(
			(a, b) =>
				b.repoCount - a.repoCount ||
				Number(b.conventional) - Number(a.conventional) ||
				a.path.localeCompare(b.path),
		)
		.slice(0, MAX_CANDIDATES);
}
