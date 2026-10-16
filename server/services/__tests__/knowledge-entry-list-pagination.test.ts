/**
 * Keyset pagination of the knowledge entry list (GET /api/knowledge/entries).
 *
 * The list used to read the first 100 rows and THEN drop the unreadable ones, so a reader
 * silently lost everything past row 100 (and fewer than 100 came back once the ACL filter
 * ran). These tests pin the paginated contract:
 *   - pages follow (updatedAt DESC, id DESC) with no duplicates / skips, ties included
 *   - ACL-filtered pages are back-filled from further DB batches
 *   - a reader who can see almost nothing gets bounded (scan-budget) short pages whose
 *     cursor still resumes correctly
 *   - limit bounds / cursor validation at the route, legacy array shape kept for callers
 *     that pass neither `limit` nor `cursor`
 */
import { beforeAll, describe, expect, test } from "bun:test";
import { eq } from "drizzle-orm";
import { Hono } from "hono";
import { db } from "../../db";
import { knowledgeEntries, users } from "../../db/schema";
import { AppError } from "../../lib/errors";
import { generateId } from "../../lib/id";
import { knowledgeRoutes } from "../../routes/knowledge";
import type { Principal } from "../knowledge-acl";
import {
	decodeEntryCursor,
	ENTRY_PAGE_MAX_SCAN_ROWS,
	encodeEntryCursor,
	knowledgeService,
} from "../knowledge-service";

const TAG = Date.now();
let adminId: string;
let lowId: string;

const P = (id: string, role: "admin" | "user" = "user"): Principal => ({ userId: id, role });

function nowIso(): string {
	return new Date().toISOString();
}

async function makeUser(role: "admin" | "user", label: string): Promise<string> {
	const id = generateId();
	await db.insert(users).values({
		id,
		username: `${label}-${TAG}-${generateId(4)}`,
		passwordHash: "x",
		role,
		createdAt: nowIso(),
	});
	return id;
}

function appFor(principal: { userId: string; role: "admin" | "user" }) {
	const app = new Hono();
	app.use("*", async (c, next) => {
		c.set("user", {
			sub: principal.userId,
			role: principal.role,
			iat: 0,
			exp: Number.MAX_SAFE_INTEGER,
		});
		await next();
	});
	app.onError((error) => {
		if (error instanceof AppError) {
			return new Response(JSON.stringify({ error: error.message, code: error.code }), {
				status: error.statusCode,
				headers: { "content-type": "application/json" },
			});
		}
		throw error;
	});
	app.route("/knowledge", knowledgeRoutes);
	return app;
}

/**
 * Seed `spec.length` entries into a fresh collection. `spec[i]` is whether entry i is
 * hidden from a public-clearance reader (classified "confidential"). updatedAt is forced
 * so that several entries share a timestamp (exercises the id tie-break).
 */
async function seedCollection(spec: boolean[], tags: (i: number) => string[] = () => []) {
	const col = await knowledgeService.createCollection({ name: `page-${TAG}-${generateId()}` });
	const ids: string[] = [];
	for (let i = 0; i < spec.length; i++) {
		const e = await knowledgeService.createEntry({
			collectionId: col.id,
			title: `e${i}-${TAG}`,
			content: "body",
			tags: tags(i),
		});
		// Groups of 3 share a timestamp.
		const ts = new Date(Date.UTC(2020, 0, 1, 0, 0, Math.floor(i / 3))).toISOString();
		await db
			.update(knowledgeEntries)
			.set({ updatedAt: ts, ...(spec[i] ? { classificationLevel: "confidential" } : {}) })
			.where(eq(knowledgeEntries.id, e.id));
		ids.push(e.id);
	}
	const rows = await db
		.select({ id: knowledgeEntries.id, updatedAt: knowledgeEntries.updatedAt })
		.from(knowledgeEntries)
		.where(eq(knowledgeEntries.collectionId, col.id));
	const expected = rows
		.sort((a, b) =>
			a.updatedAt === b.updatedAt ? (a.id < b.id ? 1 : -1) : a.updatedAt < b.updatedAt ? 1 : -1,
		)
		.map((r) => r.id);
	const hidden = new Set(ids.filter((_, i) => spec[i]));
	return { collectionId: col.id, expected, hidden };
}

async function walk(
	principal: Principal,
	opts: { collectionId: string; limit: number; tag?: string },
	maxPages = 100,
) {
	const pages: string[][] = [];
	let cursor: string | undefined;
	for (let n = 0; n < maxPages; n++) {
		const page = await knowledgeService.listEntriesPage({ principal, ...opts, cursor });
		pages.push(page.items.map((r) => r.id));
		if (!page.nextCursor) return pages;
		cursor = page.nextCursor;
	}
	throw new Error("pagination did not terminate");
}

beforeAll(async () => {
	adminId = await makeUser("admin", "padmin");
	lowId = await makeUser("user", "plow");
});

describe("listEntriesPage", () => {
	test("admin walks every entry in (updatedAt DESC, id DESC) order, no dupes/skips", async () => {
		const { collectionId, expected } = await seedCollection(Array(13).fill(false));
		const pages = await walk(P(adminId, "admin"), { collectionId, limit: 5 });
		expect(pages.map((p) => p.length)).toEqual([5, 5, 3]);
		expect(pages.flat()).toEqual(expected);
	});

	test("exact multiple of limit ends with nextCursor null (no phantom empty page)", async () => {
		const { collectionId, expected } = await seedCollection(Array(6).fill(false));
		const pages = await walk(P(adminId, "admin"), { collectionId, limit: 3 });
		expect(pages.map((p) => p.length)).toEqual([3, 3]);
		expect(pages.flat()).toEqual(expected);
	});

	test("ACL-filtered pages are back-filled and resume without leaking hidden rows", async () => {
		// Alternate visible/hidden, with a long hidden run in the middle.
		const spec = [false, true, false, true, true, true, true, false, false, true, false, true];
		const { collectionId, expected, hidden } = await seedCollection(spec);
		const visible = expected.filter((id) => !hidden.has(id));
		const pages = await walk(P(lowId), { collectionId, limit: 2 });
		const flat = pages.flat();
		expect(flat).toEqual(visible);
		expect(flat.some((id) => hidden.has(id))).toBe(false);
		// Every page except the last is full: back-fill pulled from later DB batches.
		for (const p of pages.slice(0, -1)) expect(p.length).toBe(2);
	});

	test("tag filter composes with pagination", async () => {
		const spec = Array(10).fill(false);
		const { collectionId, expected } = await seedCollection(spec, (i) =>
			i % 3 === 0 ? ["pick"] : ["other"],
		);
		const pages = await walk(P(adminId, "admin"), { collectionId, limit: 2, tag: "pick" });
		const flat = pages.flat();
		expect(flat.length).toBe(4);
		expect(flat).toEqual(expected.filter((id) => flat.includes(id)));
	});

	test("scan budget bounds one call; the cursor still resumes after scanned rows", async () => {
		// More hidden rows than one call may scan, then one visible row at the end.
		const col = await knowledgeService.createCollection({ name: `budget-${TAG}-${generateId()}` });
		const hiddenCount = ENTRY_PAGE_MAX_SCAN_ROWS + 20;
		const rows = Array.from({ length: hiddenCount }, (_, i) => ({
			id: generateId(),
			collectionId: col.id,
			title: `h${i}`,
			slug: `h-${i}-${generateId(6)}`,
			classificationLevel: "confidential",
			status: "active" as const,
			createdAt: "2021-01-01T00:00:00.000Z",
			updatedAt: new Date(Date.UTC(2021, 0, 2, 0, 0, 0, i)).toISOString(),
		}));
		for (let i = 0; i < rows.length; i += 200) {
			await db.insert(knowledgeEntries).values(rows.slice(i, i + 200));
		}
		const visible = await knowledgeService.createEntry({
			collectionId: col.id,
			title: `visible-${TAG}`,
			content: "body",
		});
		await db
			.update(knowledgeEntries)
			.set({ updatedAt: "2021-01-01T00:00:00.000Z" })
			.where(eq(knowledgeEntries.id, visible.id));

		const first = await knowledgeService.listEntriesPage({
			principal: P(lowId),
			collectionId: col.id,
			limit: 10,
		});
		expect(first.items).toEqual([]);
		expect(first.nextCursor).not.toBeNull();
		const second = await knowledgeService.listEntriesPage({
			principal: P(lowId),
			collectionId: col.id,
			limit: 10,
			cursor: first.nextCursor ?? undefined,
		});
		expect(second.items.map((r) => r.id)).toEqual([visible.id]);
		expect(second.nextCursor).toBeNull();
	});

	test("limit is clamped to [1, 200]", async () => {
		const { collectionId } = await seedCollection(Array(3).fill(false));
		const p0 = await knowledgeService.listEntriesPage({
			principal: P(adminId, "admin"),
			collectionId,
			limit: 0,
		});
		expect(p0.items.length).toBe(1);
		expect(p0.nextCursor).not.toBeNull();
		const big = await knowledgeService.listEntriesPage({
			principal: P(adminId, "admin"),
			collectionId,
			limit: 10_000,
		});
		expect(big.items.length).toBe(3);
		expect(big.nextCursor).toBeNull();
	});

	test("cursor round-trips and rejects garbage", () => {
		const pos = { updatedAt: "2020-01-01T00:00:00.000Z", id: "abc" };
		expect(decodeEntryCursor(encodeEntryCursor(pos))).toEqual(pos);
		expect(() => decodeEntryCursor("not-a-cursor")).toThrow();
		expect(() => decodeEntryCursor(Buffer.from('{"a":1}').toString("base64url"))).toThrow();
	});
});

describe("GET /knowledge/entries", () => {
	test("limit/cursor opt in to the { items, nextCursor } envelope", async () => {
		const { collectionId, expected } = await seedCollection(Array(5).fill(false));
		const app = appFor({ userId: adminId, role: "admin" });
		const r1 = await app.request(`/knowledge/entries?collectionId=${collectionId}&limit=3`);
		expect(r1.status).toBe(200);
		const b1 = (await r1.json()) as { items: { id: string }[]; nextCursor: string | null };
		expect(b1.items.map((e) => e.id)).toEqual(expected.slice(0, 3));
		expect(typeof b1.nextCursor).toBe("string");
		const r2 = await app.request(
			`/knowledge/entries?collectionId=${collectionId}&cursor=${encodeURIComponent(b1.nextCursor ?? "")}`,
		);
		const b2 = (await r2.json()) as { items: { id: string }[]; nextCursor: string | null };
		expect(b2.items.map((e) => e.id)).toEqual(expected.slice(3));
		expect(b2.nextCursor).toBeNull();
	});

	test("without limit/cursor the legacy bare array is returned", async () => {
		const { collectionId, expected } = await seedCollection(Array(4).fill(false));
		const res = await appFor({ userId: adminId, role: "admin" }).request(
			`/knowledge/entries?collectionId=${collectionId}`,
		);
		const body = (await res.json()) as { id: string }[];
		expect(Array.isArray(body)).toBe(true);
		expect(body.map((e) => e.id)).toEqual(expected);
		expect(res.headers.get("X-Next-Cursor")).toBeNull();
	});

	test("invalid limit / cursor → 400", async () => {
		const app = appFor({ userId: adminId, role: "admin" });
		expect((await app.request("/knowledge/entries?limit=0")).status).toBe(400);
		expect((await app.request("/knowledge/entries?limit=201")).status).toBe(400);
		expect((await app.request("/knowledge/entries?limit=abc")).status).toBe(400);
		expect((await app.request("/knowledge/entries?cursor=%%%")).status).toBe(400);
	});

	test("paged envelope with q carries nextCursor null", async () => {
		const res = await appFor({ userId: adminId, role: "admin" }).request(
			`/knowledge/entries?q=${encodeURIComponent(`e0-${TAG}`)}&limit=10`,
		);
		expect(res.status).toBe(200);
		const body = (await res.json()) as { items: unknown[]; nextCursor: string | null };
		expect(Array.isArray(body.items)).toBe(true);
		expect(body.nextCursor).toBeNull();
	});
});
