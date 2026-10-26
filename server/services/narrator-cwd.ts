import { parseTraits } from "../lib/narrator-utils";

export function resolveNarratorSessionCwd(
	narratorCwd: string | null | undefined,
	projectGitPath: string | null | undefined,
	fallbackCwd: string,
): string {
	return narratorCwd || projectGitPath || fallbackCwd;
}

/**
 * A session created inside a chapter, before chapters were removed, that never stored its
 * own directory.
 *
 * Its directory used to come from the chapter (the chapter worktree, or the project
 * directory for a root chapter), and that is gone. It must never silently run in the home
 * directory or in some project's main checkout instead: the caller asks the user to choose
 * a directory (`WORKSPACE_DIRECTORY_REQUIRED`).
 *
 * Chapter sessions are the only ones created without the `standalone` trait (every
 * creation path sets it, and releases with chapters backfilled it onto every session
 * outside a chapter), so the missing trait is what identifies them once the chapter link
 * itself is gone. Nothing may backfill `standalone` onto all sessions any more.
 */
export function isFormerChapterSessionWithoutDirectory(row: {
	cwd: string | null;
	traits: unknown;
}): boolean {
	return !row.cwd && !parseTraits(row.traits).includes("standalone");
}
