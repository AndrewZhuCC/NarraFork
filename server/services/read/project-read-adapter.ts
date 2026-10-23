import type { ProjectPrincipal } from "../project-acl";

export type ReadPage = { limit?: number; cursor?: string };

export type ReadPageResult<T> = { rows: T[]; nextCursor: string | null };

/** Read adapters reject absent/invalid principals rather than treating them as public. */
export function isReadablePrincipal(principal: ProjectPrincipal): boolean {
	return Boolean(principal && typeof principal.userId === "string" && principal.userId.length > 0);
}

/**
 * The row ceilings every read adapter obeys, so PostgreSQL and SQLite bound a result set
 * at the same place.
 *
 * They exist because these queries run on the HTTP thread and the project count is
 * user-driven: an unbounded `findMany` is the "all requests hang" failure mode the backend
 * rules warn about. What matters as much as the numbers is that hitting one is REPORTED —
 * a bounded read that looks like a complete set makes missing projects read as deleted.
 *
 * Each list therefore fetches `limit + 1` rows: the extra row is never returned, it only
 * answers "is there more". That is cheaper and more honest than a `COUNT(*)` over the
 * whole ACL-filtered set.
 */
export const READ_LIMITS = {
	/** Projects per page, and the default when a caller names no limit. */
	projectPage: 200,
} as const;

export interface ProjectReadAdapter<TProject = unknown> {
	listProjects(
		principal: ProjectPrincipal,
		page?: ReadPage,
		status?: string,
	): Promise<ReadPageResult<TProject>>;
	getProject(id: string, principal: ProjectPrincipal): Promise<TProject | null>;
	/**
	 * Whether ANY project exists, ignoring access — the second half of the
	 * "nothing exists yet" versus "none of them are yours" question.
	 *
	 * On the adapter because the answer must come from the backend actually serving reads;
	 * asking SQLite while PostgreSQL serves the list produces a confident wrong answer.
	 * Discloses only existence, never a name, id or count.
	 */
	anyProjectExists(): Promise<boolean>;
}
