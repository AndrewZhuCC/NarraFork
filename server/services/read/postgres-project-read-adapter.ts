import { and, asc, eq, or, type SQL, sql } from "drizzle-orm";
import type { BunSQLDatabase } from "drizzle-orm/bun-sql";
import type { PgColumn } from "drizzle-orm/pg-core";
import { projects } from "../../db/postgres-schema";
import type { ProjectPrincipal } from "../project-acl";
import { bindPlaceholders, projectReadableText } from "./acl-sql";
import {
	isReadablePrincipal,
	type ProjectReadAdapter,
	READ_LIMITS,
	type ReadPage,
} from "./project-read-adapter";
import { decodeSortCursor, encodeSortCursor } from "./read-cursor";

/**
 * A text sort/keyset expression pinned to `COLLATE "C"`, i.e. byte order.
 *
 * WHY, and why it is not cosmetic: every sort key and tiebreak in this adapter is a `text`
 * column, and the two backends order text by different rules unless told otherwise.
 * SQLite compares `text` with its BINARY collation (byte order) and has no locale support
 * to change it. PostgreSQL uses the database's collation, which for the official
 * `postgres:17` image is glibc `en_US.utf8` — a locale that ignores case at the primary
 * level and interleaves it. Measured on the two images this suite runs, over ids of the
 * shape nanoid actually produces:
 *
 *   glibc en_US.utf8: bR7Kq2…, Br7Kq2…, p_alpha, p_beta, p_Beta, …, V1stGXR8…, V1StGXR8…
 *   byte order (C)  : Br7Kq2…, V1StGXR8…, V1stGXR8…, bR7Kq2…, p_Beta, …, p_alpha, p_beta
 *
 * `'Zed' < 'abc'` is FALSE under glibc en_US.utf8 and TRUE in byte order. Ids come from
 * `nanoid`, whose alphabet mixes upper and lower case, so real data always lands in that
 * divergence — which means without this the two backends disagree about page boundaries,
 * about WHICH rows a truncated read keeps, and about what a cursor means.
 *
 * The musl-based `postgres:17-alpine` hides all of this: musl's `en_US.utf8` collation
 * degenerates to byte order, so the default test image agrees with SQLite by accident. That
 * is exactly why the parity matrix also runs the glibc image.
 *
 * `COLLATE "C"` is chosen over the alternative — declaring the cursor non-portable — because
 * these keys are opaque machine identifiers and ISO-8601 timestamps, never anything a person
 * reads in sorted order, so no locale-aware ordering is wanted for them. A user-facing
 * ordering (project name, say) would be the opposite case and must NOT be forced to C; see
 * `read-cursor.ts` for the boundary this contract draws.
 *
 * Applied to the ORDER BY and to the keyset comparison TOGETHER, always. Collating only one
 * of them is worse than collating neither: the WHERE clause would then exclude rows the
 * ORDER BY had not yet reached, and those rows are dropped from the walk entirely rather
 * than merely reordered.
 */
function byteOrder(column: PgColumn): SQL {
	return sql`${column} collate "C"`;
}

/**
 * `column COLLATE "C" <op> value`, the keyset half of the pair above.
 *
 * The bound value carries no collation of its own, so the explicit one on the column decides
 * the comparison. Equality is collated too: under a deterministic collation it means the same
 * thing either way, but leaving one comparison uncollated is precisely how the pair drifts
 * apart during a later edit.
 */
function byteOrderCompare(column: PgColumn, op: "<" | ">" | "=", value: string): SQL {
	return sql`${column} collate "C" ${sql.raw(op)} ${value}`;
}

function clampLimit(limit: number | undefined, max: number): number {
	return Math.min(Math.max(limit ?? max, 1), max);
}

/**
 * Read-only PostgreSQL projection. ACL predicates are part of every query, and every one of
 * them is generated from `acl-sql.ts` — the same text the SQLite fragments produce — so a
 * rule cannot hold on one backend and not the other.
 *
 * Nothing in here may fall back to the SQLite handle. When PostgreSQL serves reads, a
 * SQLite second read would answer from a different (and on a real deployment, empty)
 * dataset while looking like a successful check.
 */
export class PostgresProjectReadAdapter implements ProjectReadAdapter {
	constructor(private readonly db: BunSQLDatabase) {}

	/** The project-readability predicate for the `projects` table itself. */
	private projectReadable(principal: ProjectPrincipal) {
		if (!isReadablePrincipal(principal)) return sql`false`;
		if (principal.isAdmin) return undefined;
		return bindPlaceholders(projectReadableText("projects"), principal.userId);
	}

	async listProjects(principal: ProjectPrincipal, page: ReadPage = {}, status?: string) {
		const limit = clampLimit(page.limit, READ_LIMITS.projectPage);
		const cursor = decodeSortCursor("updatedAt", page.cursor);
		const rows = await this.db
			.select({ project: projects })
			.from(projects)
			.where(
				and(
					status ? eq(projects.status, status) : undefined,
					this.projectReadable(principal),
					cursor
						? or(
								byteOrderCompare(projects.updatedAt, "<", cursor.sort),
								and(
									byteOrderCompare(projects.updatedAt, "=", cursor.sort),
									byteOrderCompare(projects.id, ">", cursor.id),
								),
							)
						: undefined,
				),
			)
			// Same order as SQLite: newest first, id as the tiebreak that makes the cursor safe.
			// Both keys are byte-ordered, matching the keyset comparison above exactly — see
			// `byteOrder` for why the pair must move together.
			.orderBy(sql`${byteOrder(projects.updatedAt)} desc`, asc(byteOrder(projects.id)))
			.limit(limit + 1);
		const pageRows = rows.slice(0, limit).map((row) => row.project);
		const last = pageRows.at(-1);
		return {
			rows: pageRows,
			nextCursor:
				rows.length > limit && last ? encodeSortCursor("updatedAt", last.updatedAt, last.id) : null,
		};
	}

	async getProject(id: string, principal: ProjectPrincipal) {
		const rows = await this.db
			.select({ project: projects })
			.from(projects)
			.where(and(eq(projects.id, id), this.projectReadable(principal)))
			.limit(1);
		return rows[0]?.project ?? null;
	}

	async anyProjectExists(): Promise<boolean> {
		const rows = await this.db.select({ id: projects.id }).from(projects).limit(1);
		return rows.length > 0;
	}
}
