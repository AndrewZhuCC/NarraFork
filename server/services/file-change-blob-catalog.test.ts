import { Database } from "bun:sqlite";
import { afterEach, beforeEach, describe, expect, test } from "bun:test";
import { createHash } from "node:crypto";
import { mkdtemp, readFile, realpath, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import {
	fileChangeBlobs as blobs,
	fileChangeStorageBudgets as budgets,
	fileChangeBlobReservations as reservations,
} from "@server/db/schema";
import type { FileChangeBlobRef } from "@shared/file-change-protocol";
import { eq } from "drizzle-orm";
import { drizzle } from "drizzle-orm/bun-sqlite";
import {
	FILE_CHANGE_BLOB_BUDGET_ID,
	FileChangeBlobCatalog,
	type FileChangeBlobCatalogErrorCode,
} from "./file-change-blob-catalog";
import { type FileChangeBlobRelease, FileChangeBlobStore } from "./file-change-blob-store";

const DDL = `
CREATE TABLE file_change_blobs (
 id TEXT PRIMARY KEY NOT NULL, digest TEXT NOT NULL UNIQUE, size_bytes INTEGER NOT NULL,
 storage_key TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'staging', lease_until TEXT,
 gc_generation INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE TABLE file_change_storage_budgets (
 id TEXT PRIMARY KEY NOT NULL, namespace_key TEXT NOT NULL UNIQUE,
 status TEXT NOT NULL DEFAULT 'unverified', used_bytes INTEGER NOT NULL DEFAULT 0,
 reserved_bytes INTEGER NOT NULL DEFAULT 0, quota_bytes INTEGER NOT NULL,
 generation INTEGER NOT NULL DEFAULT 0, reconciled_at TEXT, updated_at TEXT NOT NULL
);
CREATE TABLE file_change_blob_reservations (
 id TEXT PRIMARY KEY NOT NULL, budget_id TEXT NOT NULL REFERENCES file_change_storage_budgets(id),
 owner_epoch TEXT NOT NULL, generation INTEGER NOT NULL DEFAULT 0, expected_size INTEGER NOT NULL,
 status TEXT NOT NULL DEFAULT 'reserved', blob_digest TEXT REFERENCES file_change_blobs(digest),
 published INTEGER, created_at TEXT NOT NULL, settled_at TEXT
);
CREATE INDEX idx_fc_reservation_budget ON file_change_blob_reservations(budget_id,status,created_at,id);
`;
const empty: FileChangeBlobRelease = { ref: null, published: false };
let root: string;
let sqlite: Database;
let db: ReturnType<typeof drizzle>;
let catalog: FileChangeBlobCatalog;
let queries: string[];
beforeEach(async () => {
	expect(process.env.NARRAFORK_TEST).toBe("1");
	root = await mkdtemp(join(await realpath(tmpdir()), "blob-catalog-v3-"));
	sqlite = new Database(":memory:");
	sqlite.exec("PRAGMA foreign_keys = ON; PRAGMA busy_timeout = 0;");
	sqlite.exec(DDL);
	queries = [];
	db = drizzle(sqlite, { logger: { logQuery: (query) => queries.push(query) } });
	catalog = new FileChangeBlobCatalog({ db, namespaceKey: root });
});
afterEach(async () => {
	sqlite.close();
	await rm(root, { recursive: true, force: true });
});
function refFor(text = "abc"): FileChangeBlobRef {
	return {
		algorithm: "sha256",
		digest: createHash("sha256").update(text).digest("hex"),
		sizeBytes: Buffer.byteLength(text),
	};
}
function reserve(size = 3, source = catalog) {
	return source.reserve({
		expectedGeneration: 0,
		ownerEpoch: "writer",
		expectedSize: size,
		signal: new AbortController().signal,
	});
}
function expectCode(run: () => unknown, code: FileChangeBlobCatalogErrorCode) {
	expect(run).toThrow(expect.objectContaining({ name: "FileChangeBlobCatalogError", code }));
}
function store() {
	return new FileChangeBlobStore({
		root: join(root, "objects"),
		minimumFreeBytes: 0,
		admission: catalog.admission({ ownerEpoch: "writer" }),
	});
}
function blobPath(ref: FileChangeBlobRef) {
	return join(root, "objects", "sha256", ref.digest.slice(0, 2), ref.digest);
}

describe("independent retryable blob history", () => {
	test("initialization is idempotent and needs no ready-state transition", () => {
		expect(catalog.getBudget()).toBeNull();
		const initialized = catalog.initializeNamespace({ quotaBytes: 100 });
		expect(catalog.initializeNamespace({ expectedGeneration: 999 })).toEqual(initialized);
		const lease = reserve();
		lease.release({ ref: refFor(), published: true });
		expect(catalog.getMetadata({ ref: refFor() })?.status).toBe("ready");
		expect(catalog.getBudget()?.usedBytes).toBe(3);
		expect(db.select().from(reservations).all()).toEqual([]);
	});
	test.each([
		"unverified",
		"reconciling",
		"ready",
	] as const)("old %s status and abandoned reservations remain archival", (status) => {
		catalog.initializeNamespace({ quotaBytes: 100 });
		db.update(budgets)
			.set({ status, reservedBytes: Number.MAX_SAFE_INTEGER, generation: 91 })
			.where(eq(budgets.id, FILE_CHANGE_BLOB_BUDGET_ID))
			.run();
		db.insert(reservations)
			.values({
				id: "abandoned",
				budgetId: FILE_CHANGE_BLOB_BUDGET_ID,
				ownerEpoch: "dead-owner",
				generation: 3,
				expectedSize: 100,
				status: "reconcile_required",
				createdAt: "2026-01-01T00:00:00.000Z",
			})
			.run();
		const before = db.select().from(reservations).all();
		const budget = catalog.getBudget();
		if (!budget) throw new Error("Missing budget fixture");
		expect(catalog.initializeNamespace()).toEqual(budget);
		reserve().release({ ref: refFor(), published: true });
		expect(catalog.getBudget()).toMatchObject({
			status,
			generation: 91,
			reservedBytes: Number.MAX_SAFE_INTEGER,
			usedBytes: 3,
		});
		expect(db.select().from(reservations).all()).toEqual(before);
	});
	test("live byte quota is shared across handles without serializing file IO", () => {
		catalog.initializeNamespace({ quotaBytes: 5 });
		const other = new FileChangeBlobCatalog({ db, namespaceKey: root });
		const first = reserve(3);
		const second = reserve(2, other);
		expectCode(() => reserve(1, other), "quota_exceeded");
		first.release(empty);
		const third = reserve(3, other);
		second.release(empty);
		third.release(empty);
		reserve(5).release(empty);
		expect(catalog.getBudget()).toMatchObject({ usedBytes: 0, reservedBytes: 0 });
		expect(queries.some((query) => /SUM\s*\(|COUNT\s*\(/i.test(query))).toBe(false);
	});
	test("verified publication is atomic, deduplicated, and enforces stored byte quota", () => {
		catalog.initializeNamespace({ quotaBytes: 6 });
		const first = reserve();
		const result = { ref: refFor(), published: true };
		first.release(result);
		first.release(result);
		reserve().release({ ref: refFor(), published: false });
		expect(catalog.getBudget()?.usedBytes).toBe(3);
		reserve().release({ ref: refFor("def"), published: true });
		expect(catalog.getBudget()?.usedBytes).toBe(6);
		expectCode(() => reserve(1), "quota_exceeded");
		expectCode(() => first.release(empty), "release_conflict");
	});
	test("accounting failure releases in-memory quota and never damages published bytes", async () => {
		catalog.initializeNamespace({ quotaBytes: 8 });
		const objects = store();
		sqlite.exec(
			"CREATE TRIGGER fail_accounting BEFORE UPDATE OF used_bytes ON file_change_storage_budgets BEGIN SELECT RAISE(ABORT, 'accounting-fault'); END;",
		);
		await expect(objects.putBytes(Buffer.from("abc"), { expectedSize: 3 })).rejects.toThrow(
			"admission release failed",
		);
		expect(await readFile(blobPath(refFor()), "utf8")).toBe("abc");
		expect(catalog.getMetadata({ ref: refFor() })).toBeNull();
		sqlite.exec("DROP TRIGGER fail_accounting;");
		const second = await objects.putBytes(Buffer.from("def"), { expectedSize: 3 });
		expect(await objects.readBytes(second)).toEqual(Buffer.from("def"));
		const retry = await objects.putBytes(Buffer.from("abc"), { expectedSize: 3 });
		expect(await objects.readBytes(retry)).toEqual(Buffer.from("abc"));
		expect(catalog.getBudget()?.usedBytes).toBe(6);
		expect(await readFile(blobPath(refFor()), "utf8")).toBe("abc");
	});
	test("original publication callback is retryable after a transient DB failure", () => {
		catalog.initializeNamespace({ quotaBytes: 3 });
		const lease = reserve();
		const result = { ref: refFor(), published: true };
		sqlite.exec(
			"CREATE TRIGGER fail_blob BEFORE INSERT ON file_change_blobs BEGIN SELECT RAISE(ABORT, 'blob-fault'); END;",
		);
		expect(() => lease.release(result)).toThrow("blob-fault");
		reserve().release(empty);
		sqlite.exec("DROP TRIGGER fail_blob;");
		lease.release(result);
		lease.release(result);
		expect(catalog.getBudget()?.usedBytes).toBe(3);
	});
	test("a corrupt object only fails that digest and remains intact", async () => {
		catalog.initializeNamespace({ quotaBytes: 100 });
		const objects = store();
		const original = await objects.putBytes(Buffer.from("abc"), { expectedSize: 3 });
		await writeFile(blobPath(original), "bad");
		await expect(objects.readBytes(original)).rejects.toThrow();
		await expect(objects.putBytes(Buffer.from("abc"), { expectedSize: 3 })).rejects.toThrow();
		const second = await objects.putBytes(Buffer.from("xyz"), { expectedSize: 3 });
		expect(await objects.readBytes(second)).toEqual(Buffer.from("xyz"));
		expect(await readFile(blobPath(original), "utf8")).toBe("bad");
		expect(catalog.getMetadata({ ref: original })?.digest).toBe(original.digest);
	});
	test("stream failure returns only active quota and preserves older history", async () => {
		catalog.initializeNamespace({ quotaBytes: 6 });
		const objects = store();
		const prior = await objects.putBytes(Buffer.from("abc"), { expectedSize: 3 });
		async function* failed() {
			yield Buffer.from("d");
			throw new Error("producer-fault");
		}
		await expect(objects.putStream(failed(), { expectedSize: 3 })).rejects.toThrow(
			"producer-fault",
		);
		const later = await objects.putBytes(Buffer.from("def"), { expectedSize: 3 });
		expect(await objects.readBytes(prior)).toEqual(Buffer.from("abc"));
		expect(await objects.readBytes(later)).toEqual(Buffer.from("def"));
	});
	test("cancellation and invalid publication metadata release only their own quota", () => {
		catalog.initializeNamespace({ quotaBytes: 3 });
		const signal = AbortSignal.abort();
		expectCode(() => catalog.reserve({ ownerEpoch: "writer", expectedSize: 3, signal }), "aborted");
		const lease = reserve();
		expectCode(
			() => lease.release({ ref: refFor("wrong-size"), published: true }),
			"size_mismatch",
		);
		reserve().release(empty);
		expect(catalog.getBudget()?.usedBytes).toBe(0);
	});
	test("a reservation snapshots its declared size before the caller can mutate it", () => {
		catalog.initializeNamespace({ quotaBytes: 10 });
		const request = { ownerEpoch: "writer", expectedSize: 3, signal: new AbortController().signal };
		const lease = catalog.reserve(request);
		request.expectedSize = 6;
		expectCode(() => lease.release({ ref: refFor("abcdef"), published: true }), "size_mismatch");
		expect(catalog.getBudget()?.usedBytes).toBe(0);
		reserve(10).release(empty);
	});

	test("namespace identity, catalog metadata and unsafe SQLite settings stay enforced", () => {
		catalog.initializeNamespace({ quotaBytes: 100 });
		const other = new FileChangeBlobCatalog({ db, namespaceKey: "different" });
		expectCode(() => other.initializeNamespace(), "namespace_mismatch");
		expectCode(() => other.getMetadata({ ref: refFor() }), "namespace_mismatch");
		reserve().release({ ref: refFor(), published: true });
		db.update(blobs).set({ sizeBytes: 99 }).where(eq(blobs.digest, refFor().digest)).run();
		expectCode(() => reserve().release({ ref: refFor(), published: false }), "catalog_mismatch");
		sqlite.exec("PRAGMA busy_timeout = 5000;");
		expectCode(() => new FileChangeBlobCatalog({ db, namespaceKey: root }), "invalid_input");
	});
	test("legacy reservation inventory remains bounded and read-only", () => {
		catalog.initializeNamespace();
		for (let index = 0; index < 5; index++)
			db.insert(reservations)
				.values({
					id: `old-${index}`,
					budgetId: FILE_CHANGE_BLOB_BUDGET_ID,
					ownerEpoch: "old",
					expectedSize: 1,
					createdAt: "2026-01-01T00:00:00.000Z",
				})
				.run();
		const first = catalog.listReservations({ status: "reserved", limit: 2 });
		const second = catalog.listReservations({
			status: "reserved",
			limit: 2,
			after: first.nextCursor ?? undefined,
		});
		const last = catalog.listReservations({
			status: "reserved",
			limit: 2,
			after: second.nextCursor ?? undefined,
		});
		expect([...first.items, ...second.items, ...last.items].map((row) => row.id)).toEqual([
			"old-0",
			"old-1",
			"old-2",
			"old-3",
			"old-4",
		]);
		expect(last.nextCursor).toBeNull();
	});
});
