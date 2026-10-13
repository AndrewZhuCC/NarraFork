export function resolveNarratorSessionCwd(
	narratorCwd: string | null | undefined,
	worktreePath: string | null | undefined,
	projectGitPath: string | null | undefined,
	fallbackCwd: string,
): string {
	return narratorCwd || worktreePath || projectGitPath || fallbackCwd;
}

/** A legacy branch without its directory must never silently execute in main or HOME. */
export function resolveLegacyNarratorCwd(
	narratorCwd: string | null | undefined,
	worktreePath: string | null | undefined,
	projectGitPath: string | null | undefined,
	isRoot: boolean,
): string | null {
	return narratorCwd || worktreePath || (isRoot ? projectGitPath : null) || null;
}
