/**
 * What the PostgreSQL read adapter actually SENDS.
 *
 * This suite compiles every read through Drizzle's PostgreSQL dialect and inspects the SQL
 * text, without a server. That is not a substitute for running against PostgreSQL — the
 * parity suite does that — but it catches the class of defect that a SQLite-only test run
 * cannot see at all, and it catches it in a form that names the offending construct.
 *
 * The defect that motivated it: three auxiliary predicates were written as
 * `sql\`${column} = any(${ids})\``. Drizzle renders an array in a template as a
 * parenthesised parameter LIST, so that compiles to `= any(($1, $2, $3))` — and PostgreSQL
 * rejects it outright with "op ANY/ALL (array) requires array on right side". Reading the
 * source, it looks like idiomatic PostgreSQL. Nothing in a SQLite test run touches it, so
 * the first evidence would have been a 500 on a real deployment. (Those auxiliary reads
 * served the story graph and left with it; the guard stays on what remains.)
 *
 * Also pinned here: the user id is a BOUND PARAMETER everywhere. If it were interpolated,
 * the statement text would differ per user (defeating statement caching) and the predicate
 * would be an injection site — and neither shows up in a functional test that happens to use
 * well-behaved ids.
 */

import { describe, expect, test } from "bun:test";
import type { BunSQLDatabase } from "drizzle-orm/bun-sql";
import { drizzle as drizzleProxy } from "drizzle-orm/pg-proxy";
import type { ProjectPrincipal } from "../../project-acl";
import { PostgresProjectReadAdapter } from "../postgres-project-read-adapter";

type Captured = { sql: string; params: unknown[] };

/** A Drizzle handle that records statements instead of executing them. */
function recorder(): { db: BunSQLDatabase; statements: Captured[] } {
	const statements: Captured[] = [];
	const db = drizzleProxy(async (sql, params) => {
		statements.push({ sql, params });
		return { rows: [] };
	}) as unknown as BunSQLDatabase;
	return { db, statements };
}

function principal(userId: string, isAdmin = false): ProjectPrincipal {
	return { userId, isAdmin };
}

/** Run one adapter call and return every statement it compiled. */
async function capture(
	run: (adapter: PostgresProjectReadAdapter) => Promise<unknown>,
): Promise<Captured[]> {
	const { db, statements } = recorder();
	await run(new PostgresProjectReadAdapter(db));
	return statements;
}

const USER = "u_reader";
const PROJECT = "p_1";

/**
 * Every statement the adapter can emit for a non-admin principal, so a broad assertion
 * ("nowhere does `any(` appear") really covers the whole surface rather than one call.
 */
async function allStatements(who: ProjectPrincipal): Promise<Captured[]> {
	const out: Captured[] = [];
	out.push(...(await capture((a) => a.listProjects(who))));
	out.push(...(await capture((a) => a.listProjects(who, { limit: 10 }, "active"))));
	out.push(...(await capture((a) => a.getProject(PROJECT, who))));
	out.push(...(await capture((a) => a.anyProjectExists())));
	return out;
}

describe("id lists compile to real PostgreSQL", () => {
	test("no read emits `= any(...)`, which cannot take a parameter list", () => {
		// The exact defect: `sql`${col} = any(${ids})`` renders `any(($1, $2, $3))` and PostgreSQL
		// answers "op ANY/ALL (array) requires array on right side".
		return allStatements(principal(USER)).then((statements) => {
			expect(statements.length).toBeGreaterThan(0);
			for (const statement of statements) {
				expect(statement.sql.toLowerCase()).not.toContain("any(");
			}
		});
	});
});

describe("ACL predicates reach PostgreSQL intact", () => {
	test("every ACL predicate binds the user id rather than inlining it", async () => {
		const statements = await allStatements(principal(USER));
		const gated = statements.filter((s) => s.sql.includes("acl_grants"));
		expect(gated.length).toBeGreaterThan(0);
		for (const statement of gated) {
			expect(statement.params).toContain(USER);
			expect(statement.sql).not.toContain(USER);
		}
	});

	test("an admin adds no ACL clause, and an absent principal is refused in SQL", async () => {
		const asAdmin = await capture((a) => a.listProjects(principal("u_admin", true)));
		expect(asAdmin[0]?.sql).not.toContain("acl_grants");

		// An empty user id means a route was reached without the auth middleware. It must not
		// degrade to "public", and `isAdmin` must not rescue it — that flag comes from the same
		// unverified place as the id.
		const absent = await capture((a) => a.listProjects(principal("")));
		expect(absent[0]?.sql.toLowerCase()).toContain("false");
		const absentAdmin = await capture((a) => a.listProjects({ userId: "", isAdmin: true }));
		expect(absentAdmin[0]?.sql.toLowerCase()).toContain("false");
	});
});

describe("bounded reads and stable order", () => {
	test("the project list is updatedAt DESC with an id tiebreak, and asks for limit + 1", async () => {
		const statements = await capture((a) => a.listProjects(principal(USER)));
		const sql = statements[0]?.sql.toLowerCase() ?? "";
		// Descending order is the contract every client reads as "most recent first"; the id
		// tiebreak is what makes the cursor safe when rows share `updatedAt`.
		//
		// `collate "c"` on BOTH keys is part of the contract, not an implementation detail:
		// SQLite compares `text` in byte order, PostgreSQL's database collation does not (glibc
		// `en_US.utf8` interleaves case), so without it the two backends disagree about page
		// boundaries. Removing the collation must fail here, loudly.
		expect(sql).toContain(
			'order by "projects"."updated_at" collate "c" desc, "projects"."id" collate "c" asc',
		);
		// 200 + 1: the extra row answers "is there more" without a COUNT over the filtered set.
		expect(statements[0]?.params).toContain(201);
	});

	test("a cursor is compiled as a keyset comparison, not an offset", async () => {
		const cursor = Buffer.from(
			JSON.stringify({ updatedAt: "2026-01-01T00:00:00.000Z", id: "p_9" }),
		).toString("base64url");
		const statements = await capture((a) => a.listProjects(principal(USER), { limit: 5, cursor }));
		const sql = statements[0]?.sql.toLowerCase() ?? "";
		// An OFFSET would skip or repeat rows precisely when the ACL-filtered set changes between
		// pages, which is the situation paging exists for.
		expect(sql).not.toContain("offset");
		// The keyset comparison carries the same `collate "c"` as the ORDER BY. Collating only
		// one of the pair is the failure mode that loses rows outright: the WHERE clause then
		// excludes rows the ORDER BY had not reached yet, and they vanish from the walk.
		expect(sql).toContain('"projects"."updated_at" collate "c" <');
		expect(sql).toContain('"projects"."id" collate "c" >');
		expect(statements[0]?.params).toContain("p_9");
	});
});
