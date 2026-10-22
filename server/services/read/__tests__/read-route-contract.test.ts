/**
 * What `GET /api/projects` promises its existing clients.
 *
 * The endpoint is consumed as a BARE JSON ARRAY by the dashboard, the project list and the
 * scheduled-task editor. None of them sends a paging parameter, so every property asserted
 * here is something a client already depends on and would lose without any error being raised:
 *
 *  - the project list is most-recently-updated FIRST, and complete;
 *  - a bounded read announces itself (`X-Read-Truncated`) instead of looking like the whole set;
 *  - the response SHAPE stays an array — no pagination envelope, because wrapping it would
 *    break every caller to serve a case none of them have.
 *
 * The adapter is INJECTED here rather than exercised against a database: these are statements
 * about the routes' translation layer (order preserved, headers set, gate applied), and the
 * adapters' own behaviour against real rows is covered by the SQLite matrix and the PostgreSQL
 * parity suite. A recording stub also makes it possible to assert what the route ASKED for,
 * which is where the "default page of 100" regression actually lived.
 */

import { afterAll, afterEach, beforeAll, describe, expect, test } from "bun:test";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { Hono } from "hono";

const previousHome = process.env.NARRAFORK_HOME;
const testHome = mkdtempSync(join(tmpdir(), "narrafork-read-route-"));
process.env.NARRAFORK_HOME = testHome;
process.env.NARRAFORK_ALLOW_MULTIPLE = "1";

const { projectRoutes } = await import("../../../routes/projects");
const { setProjectReadAdapter } = await import("..");
const { AppError } = await import("../../../lib/errors");
const { db } = await import("../../../db");
const { aclGrants, projects, users } = await import("../../../db/schema");
const { generateId } = await import("../../../lib/id");
const { READ_LIMITS } = await import("../project-read-adapter");
const { COLLECT_MAX_ROWS } = await import("../read-collect");

import type { ProjectPrincipal } from "../../project-acl";
import type { ProjectReadAdapter, ReadPage } from "../project-read-adapter";

const OWNER = "read-route-owner";
const READER = "read-route-reader";
const OUTSIDER = "read-route-outsider";

let privateProject: string;

/** One recorded adapter call, so a test can assert what the route asked for. */
type Call = { method: string; page?: ReadPage; status?: string; projectId?: string };

/**
 * An adapter that answers from a fixed row set and records every call.
 *
 * `listProjects` implements real keyset paging over that set, because the
 * routes' full-walk behaviour is exactly what is under test: a stub that ignored the cursor
 * would make `collectAllPages` look correct no matter what it did.
 */
function stubAdapter(options: { projects?: Array<Record<string, unknown>>; pageSize?: number }) {
	const calls: Call[] = [];
	const projectRows = options.projects ?? [];
	const pageSize = options.pageSize ?? READ_LIMITS.projectPage;

	const page = <T extends { id: string }>(
		rows: T[],
		sortKey: string,
		cursorField: string,
		requested: ReadPage | undefined,
	) => {
		const limit = Math.min(Math.max(requested?.limit ?? pageSize, 1), pageSize);
		let start = 0;
		if (requested?.cursor) {
			const decoded = JSON.parse(Buffer.from(requested.cursor, "base64url").toString("utf8")) as
				| Record<string, string>
				| undefined;
			const afterId = decoded?.id;
			// Field name is part of the cursor, so a cursor minted for a differently ordered list
			// does not silently resume in the wrong place.
			expect(decoded?.[cursorField]).toBeString();
			start = rows.findIndex((row) => row.id === afterId) + 1;
		}
		const slice = rows.slice(start, start + limit);
		const last = slice.at(-1) as (T & Record<string, string>) | undefined;
		const more = start + limit < rows.length;
		return {
			rows: slice,
			nextCursor:
				more && last
					? Buffer.from(JSON.stringify({ [cursorField]: last[sortKey], id: last.id })).toString(
							"base64url",
						)
					: null,
		};
	};

	const adapter: ProjectReadAdapter = {
		async listProjects(_principal: ProjectPrincipal, requested?: ReadPage, status?: string) {
			calls.push({ method: "listProjects", page: requested, status });
			const filtered = status ? projectRows.filter((row) => row.status === status) : projectRows;
			return page(filtered as Array<{ id: string }>, "updatedAt", "updatedAt", requested);
		},
		async getProject(id: string) {
			calls.push({ method: "getProject", projectId: id });
			return projectRows.find((row) => row.id === id) ?? null;
		},
		async anyProjectExists() {
			calls.push({ method: "anyProjectExists" });
			return projectRows.length > 0;
		},
	};
	setProjectReadAdapter(adapter);
	return calls;
}

function appAs(userId: string, role: "admin" | "user" = "user") {
	const app = new Hono();
	app.use("*", async (c, next) => {
		c.set("user", { sub: userId, role, iat: 0, exp: Number.MAX_SAFE_INTEGER });
		await next();
	});
	app.onError((error) => {
		if (error instanceof AppError) {
			return new Response(JSON.stringify({ error: error.message, code: error.code }), {
				status: error.statusCode,
				headers: { "content-type": "application/json" },
			});
		}
		return new Response(JSON.stringify({ error: String(error) }), { status: 500 });
	});
	app.route("/projects", projectRoutes);
	return app;
}

function request(userId: string, path: string, role: "admin" | "user" = "user") {
	return appAs(userId, role).request(`http://localhost${path}`);
}

function projectRow(id: string, updatedAt: string, status = "active") {
	return { id, name: id, status, visibility: "private", ownerUserId: OWNER, updatedAt };
}

beforeAll(async () => {
	const now = new Date().toISOString();
	for (const id of [OWNER, READER, OUTSIDER]) {
		await db
			.insert(users)
			.values({
				id,
				username: `${id}-${Date.now()}`,
				passwordHash: "x",
				role: "user",
				createdAt: now,
			})
			.onConflictDoNothing();
	}
	privateProject = generateId();
	// A real row: project gates load the project from the database rather than through the
	// adapter — the gate is authorization, not a read projection.
	await db.insert(projects).values({
		id: privateProject,
		name: "read route project",
		gitPath: join(testHome, "repo"),
		ownerUserId: OWNER,
		visibility: "private",
		createdAt: now,
		updatedAt: now,
	});
	await db.insert(aclGrants).values({
		id: generateId(),
		scopeType: "project",
		scopeId: privateProject,
		principalType: "user",
		principalId: READER,
		capability: "read",
		createdAt: now,
	});
});

afterEach(() => {
	// Back to the real adapter, so a stub cannot leak into another suite.
	setProjectReadAdapter(undefined);
});

afterAll(async () => {
	setProjectReadAdapter(undefined);
	await db.delete(aclGrants).where(scopedTo(privateProject));
	await db.delete(projects).where(byId(privateProject));
	if (previousHome === undefined) delete process.env.NARRAFORK_HOME;
	else process.env.NARRAFORK_HOME = previousHome;
	rmSync(testHome, { recursive: true, force: true });
});

// Tiny helpers so the cleanup above reads as intent rather than drizzle plumbing.
function byId(id: string) {
	const { eq } = require("drizzle-orm") as typeof import("drizzle-orm");
	return eq(projects.id, id);
}
function scopedTo(id: string) {
	const { eq } = require("drizzle-orm") as typeof import("drizzle-orm");
	return eq(aclGrants.scopeId, id);
}

describe("GET /projects ordering and completeness", () => {
	test("serves most-recently-updated first, as it always has", async () => {
		// The dashboard reads the head of this list as "what I worked on last". Ascending order
		// silently inverted that; nothing errored, the list was simply wrong.
		stubAdapter({
			projects: [
				projectRow("p_new", "2026-03-01T00:00:00.000Z"),
				projectRow("p_mid", "2026-02-01T00:00:00.000Z"),
				projectRow("p_old", "2026-01-01T00:00:00.000Z"),
			],
		});
		const res = await request(OWNER, "/projects");
		expect(res.status).toBe(200);
		const body = (await res.json()) as Array<{ id: string }>;
		expect(body.map((row) => row.id)).toEqual(["p_new", "p_mid", "p_old"]);
	});

	test("returns a bare array, not a pagination envelope", async () => {
		stubAdapter({ projects: [projectRow("p_1", "2026-01-01T00:00:00.000Z")] });
		const body = await (await request(OWNER, "/projects")).json();
		expect(Array.isArray(body)).toBe(true);
	});

	test("a request with no paging parameters asks for no limit and walks every page", async () => {
		// The regression: the route sent `limit: 100`, so a user with more projects than that
		// received their first hundred and no indication the rest existed.
		const rows = Array.from({ length: 250 }, (_, index) =>
			projectRow(`p_${String(index).padStart(3, "0")}`, "2026-01-01T00:00:00.000Z"),
		);
		const calls = stubAdapter({ projects: rows, pageSize: 100 });
		const res = await request(OWNER, "/projects");
		const body = (await res.json()) as Array<{ id: string }>;
		// All 250 rows, assembled from bounded pages.
		expect(body).toHaveLength(250);
		expect(body[0]?.id).toBe("p_000");
		expect(body.at(-1)?.id).toBe("p_249");
		// The route named no limit; the adapter chose its own maximum.
		expect(calls[0]?.page?.limit).toBeUndefined();
		expect(calls.filter((call) => call.method === "listProjects").length).toBe(3);
		// A complete answer carries no truncation signal.
		expect(res.headers.get("X-Read-Truncated")).toBeNull();
		expect(res.headers.get("X-Next-Cursor")).toBeNull();
	});

	test("honours an explicit ?limit and reports the continuation", async () => {
		const rows = Array.from({ length: 30 }, (_, index) =>
			projectRow(`p_${String(index).padStart(2, "0")}`, "2026-01-01T00:00:00.000Z"),
		);
		const calls = stubAdapter({ projects: rows });
		const res = await request(OWNER, "/projects?limit=10");
		const body = (await res.json()) as Array<{ id: string }>;
		expect(body).toHaveLength(10);
		expect(calls[0]?.page?.limit).toBe(10);
		// One page was asked for and more remain: say so, and hand back a usable cursor.
		expect(res.headers.get("X-Read-Truncated")).toBe("1");
		const cursor = res.headers.get("X-Next-Cursor");
		expect(cursor).toBeString();

		const next = await request(
			OWNER,
			`/projects?limit=10&cursor=${encodeURIComponent(cursor ?? "")}`,
		);
		const nextBody = (await next.json()) as Array<{ id: string }>;
		// The continuation resumes where the first page stopped — no repeat, no gap.
		expect(nextBody.map((row) => row.id)).toEqual(
			rows.slice(10, 20).map((row) => row.id as string),
		);
	});

	test("a walk that hits the response ceiling is reported, never silently cut", async () => {
		const rows = Array.from({ length: COLLECT_MAX_ROWS + 25 }, (_, index) =>
			projectRow(`p_${String(index).padStart(5, "0")}`, "2026-01-01T00:00:00.000Z"),
		);
		stubAdapter({ projects: rows, pageSize: 200 });
		const res = await request(OWNER, "/projects");
		const body = (await res.json()) as unknown[];
		expect(body).toHaveLength(COLLECT_MAX_ROWS);
		// The whole point: a bounded response must not look like a complete set.
		expect(res.headers.get("X-Read-Truncated")).toBe("1");
		expect(res.headers.get("X-Next-Cursor")).toBeString();
	});

	test("passes a valid status filter through and ignores an unknown one", async () => {
		const calls = stubAdapter({
			projects: [
				projectRow("p_active", "2026-02-01T00:00:00.000Z", "active"),
				projectRow("p_archived", "2026-01-01T00:00:00.000Z", "archived"),
			],
		});
		const archived = (await (await request(OWNER, "/projects?status=archived")).json()) as Array<{
			id: string;
		}>;
		expect(archived.map((row) => row.id)).toEqual(["p_archived"]);
		expect(calls.at(-1)?.status).toBe("archived");

		// An unrecognized status must not become a filter that answers a different question.
		await request(OWNER, "/projects?status=frozen");
		expect(calls.at(-1)?.status).toBeUndefined();
	});
});

describe("GET /projects/hidden-existence uses the serving backend", () => {
	test("asks the adapter for both halves rather than querying SQLite directly", async () => {
		// Asking SQLite while PostgreSQL serves reads would answer "no projects exist" with full
		// confidence, and this endpoint exists precisely to disambiguate an empty list.
		const calls = stubAdapter({ projects: [] });
		const res = await request(OUTSIDER, "/projects/hidden-existence");
		expect(await res.json()).toEqual({ hasHidden: false });
		expect(calls.map((call) => call.method)).toEqual(["listProjects", "anyProjectExists"]);
		// A bounded probe: one row, never the whole list.
		expect(calls[0]?.page?.limit).toBe(1);
	});

	test("reports hidden projects when the caller sees none but some exist", async () => {
		const adapterCalls = stubAdapter({ projects: [] });
		// `listProjects` returns nothing for this principal, `anyProjectExists` says otherwise.
		setProjectReadAdapter({
			async listProjects() {
				adapterCalls.push({ method: "listProjects" });
				return { rows: [], nextCursor: null };
			},
			async getProject() {
				return null;
			},
			async anyProjectExists() {
				return true;
			},
		});
		expect(await (await request(OUTSIDER, "/projects/hidden-existence")).json()).toEqual({
			hasHidden: true,
		});
	});

	test("nothing is hidden from an admin, without touching the adapter", async () => {
		const calls = stubAdapter({ projects: [projectRow("p_1", "2026-01-01T00:00:00.000Z")] });
		expect(
			await (await request("read-route-admin", "/projects/hidden-existence", "admin")).json(),
		).toEqual({ hasHidden: false });
		expect(calls).toEqual([]);
	});
});
