import { Database } from "bun:sqlite";
import { afterAll, beforeAll, describe, expect, test } from "bun:test";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { eq, sql } from "drizzle-orm";
import { drizzle } from "drizzle-orm/bun-sqlite";
import { integer, sqliteTable, text } from "drizzle-orm/sqlite-core";
import {
	getDbWorkerPoolStats,
	resetDbWorkerPoolForTest,
	shutdownDbWorkerPool,
} from "../lib/db-worker/pool";
import { readQuery } from "./worker-read";

const owners = sqliteTable("owners", {
	id: text("id").primaryKey(),
	name: text("name").notNull(),
});
const items = sqliteTable("items", {
	id: text("id").primaryKey(),
	ownerId: text("owner_id"),
	meta: text("meta", { mode: "json" }).$type<{ tags: string[] }>(),
	done: integer("done", { mode: "boolean" }).notNull(),
	createdAt: integer("created_at", { mode: "timestamp_ms" }).notNull(),
});

let home: string;
let sqlite: Database;
let db: ReturnType<typeof drizzle>;

beforeAll(() => {
	home = mkdtempSync(join(tmpdir(), "narrafork-worker-read-"));
	sqlite = new Database(join(home, "fixture.db"));
	sqlite.run("PRAGMA journal_mode = WAL");
	sqlite.run("CREATE TABLE owners (id TEXT PRIMARY KEY, name TEXT NOT NULL)");
	sqlite.run(
		"CREATE TABLE items (id TEXT PRIMARY KEY, owner_id TEXT, meta TEXT, done INTEGER NOT NULL, created_at INTEGER NOT NULL)",
	);
	db = drizzle({ client: sqlite });
	db.insert(owners).values({ id: "o1", name: "alice" }).run();
	db.insert(items)
		.values([
			{ id: "i1", ownerId: "o1", meta: { tags: ["a"] }, done: true, createdAt: new Date(1000) },
			{ id: "i2", ownerId: null, meta: null, done: false, createdAt: new Date(2000) },
		])
		.run();
	resetDbWorkerPoolForTest();
});

afterAll(() => {
	shutdownDbWorkerPool();
	sqlite.close();
	rmSync(home, { recursive: true, force: true });
});

describe("readQuery", () => {
	test("decodes columns, joins and left-join nulls exactly like await query", async () => {
		const all = () => db.select().from(items).orderBy(items.id);
		const joined = () =>
			db
				.select({ item: items, owner: owners })
				.from(items)
				.leftJoin(owners, eq(owners.id, items.ownerId))
				.orderBy(items.id);
		const duplicateIds = () =>
			db
				.select({ itemId: items.id, ownerId: owners.id, total: sql<number>`count(*)` })
				.from(items)
				.leftJoin(owners, eq(owners.id, items.ownerId))
				.groupBy(items.id)
				.orderBy(items.id);
		expect(await readQuery(all())).toEqual(await all());
		expect(await readQuery(joined())).toEqual(await joined());
		expect(await readQuery(duplicateIds())).toEqual(await duplicateIds());
		const [first] = await readQuery(db.select().from(items).orderBy(items.id));
		expect(first?.createdAt).toBeInstanceOf(Date);
		expect(first?.meta).toEqual({ tags: ["a"] });
		expect(getDbWorkerPoolStats().workers).toBeGreaterThan(0);
	}, 30_000);

	test("fails instead of truncating when the row budget is exceeded", async () => {
		await expect(readQuery(db.select().from(items), { maxRows: 1 })).rejects.toThrow("row budget");
	}, 30_000);

	test("runs inline inside a transaction so it sees uncommitted writes", async () => {
		sqlite.run("BEGIN");
		try {
			db.insert(owners).values({ id: "o2", name: "bob" }).run();
			expect(await readQuery(db.select().from(owners))).toHaveLength(2);
		} finally {
			sqlite.run("ROLLBACK");
		}
	});

	test("runs inline for in-memory databases", async () => {
		const memory = drizzle({ client: new Database(":memory:") });
		expect(await readQuery(memory.select({ one: sql<number>`1` }).from(sql`(SELECT 1)`))).toEqual([
			{ one: 1 },
		]);
	});

	test("falls back inline when workers are unavailable unless told not to", async () => {
		const original = process.env.NARRAFORK_DB_WORKER;
		process.env.NARRAFORK_DB_WORKER = "off";
		try {
			expect(await readQuery(db.select().from(owners))).toEqual([{ id: "o1", name: "alice" }]);
			await expect(readQuery(db.select().from(owners), { inlineFallback: false })).rejects.toThrow(
				"unavailable",
			);
		} finally {
			if (original === undefined) delete process.env.NARRAFORK_DB_WORKER;
			else process.env.NARRAFORK_DB_WORKER = original;
			resetDbWorkerPoolForTest();
		}
	});
});
