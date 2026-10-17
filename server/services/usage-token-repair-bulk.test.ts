import type { Database } from "bun:sqlite";
import { afterEach, beforeEach, expect, test } from "bun:test";
import { existsSync, mkdtempSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { generateId } from "@server/lib/id";
import { getTestDb } from "../../tests/setup";
import { openSchemaCopyAt } from "../../tests/sqlite-schema-image";
import {
	addTotals,
	emptySummary,
	retireRepairScratch,
	TokenRepairScratch,
	zeroTotals,
} from "./usage-token-repair-scratch";
import {
	createTokenRepairStore,
	type RepairRequestRow,
	type TokenRepairStore,
} from "./usage-token-repair-store";

let root: string, db: Database, store: TokenRepairStore, other: TokenRepairStore;
beforeEach(() => {
	root = mkdtempSync(join(tmpdir(), "repair-bulk-core-"));
	const path = join(root, "source.sqlite");
	db = openSchemaCopyAt(getTestDb().sqlite, path);
	db.exec("PRAGMA journal_mode=WAL");
	store = createTokenRepairStore({ backend: "sqlite", sqlitePath: path });
	other = createTokenRepairStore({ backend: "sqlite", sqlitePath: path });
	db.exec(
		"INSERT INTO api_requests (id,created_at,input_tokens,cached_input_tokens,raw_dump_json) VALUES ('r','2026-01-01',100,40,'original')",
	);
});
afterEach(async () => {
	await store.close();
	await other.close();
	db.close();
	rmSync(root, { recursive: true, force: true });
});
async function preview() {
	const job = await store.createBulkJob("admin");
	const summary = emptySummary();
	summary.previewVersion = "v";
	await store.bulkState(job.id, "preview", "ready", summary, 1);
	return job;
}
async function entry() {
	const row = await store.getRequest("r");
	if (!row) throw new Error("missing");
	return {
		row,
		evidence: {
			classification: "repairable" as const,
			protocol: "openai" as const,
			reason: "inclusive",
			evidence: "",
			before: row,
			after: { ...row, inputTokens: 60 },
		},
		valid: true,
		expectedRaw: "original",
	};
}
test("claims serialize across worker instances and stale worker progress cannot overwrite the new owner", async () => {
	const job = await preview();
	const outcomes = await Promise.allSettled([
		other.bulkClaim(job.id, "applying", undefined, "v"),
		store.bulkClaim(job.id, "applying", undefined, "v"),
	]);
	expect(outcomes.filter((o) => o.status === "fulfilled")).toHaveLength(1);
	const fresh = await store.getJob(job.id);
	expect(fresh?.phase).toBe("applying");
	if (outcomes[0]?.status === "fulfilled") {
		let rejected = false;
		try {
			await store.bulkState(job.id, "models", "ready", null, 999);
		} catch {
			rejected = true;
		}
		expect(rejected).toBe(true);
		await store.bulkFail(job.id);
		expect((await other.getJob(job.id))?.scanned).toBe(1);
		expect((await other.getJob(job.id))?.status).toBe("applying");
	}
});
test("raw dump cleanup between verification and transaction fails the CAS", async () => {
	const job = await preview();
	await store.bulkClaim(job.id, "applying", undefined, "v");
	const e = await entry();
	db.exec("UPDATE api_requests SET raw_dump_json=NULL WHERE id='r'");
	expect(await store.applyBulk(job.id, [e], "admin")).toEqual(["conflict"]);
	expect(
		db.query("SELECT input_tokens,token_accounting_version FROM api_requests WHERE id='r'").get(),
	).toEqual({ input_tokens: 100, token_accounting_version: null });
	expect((await store.getJob(job.id))?.summary?.appliedDelta.inputTokens).toBe("0");
});
test("counter update, audit and decimal applied totals roll back atomically if audit insertion fails", async () => {
	const job = await preview();
	await store.bulkClaim(job.id, "applying", undefined, "v");
	db.exec(
		"CREATE TRIGGER reject_repair BEFORE INSERT ON usage_token_repair_candidates BEGIN SELECT RAISE(ABORT,'audit unavailable'); END",
	);
	let failed = false;
	try {
		await store.applyBulk(job.id, [await entry()], "admin");
	} catch {
		failed = true;
	}
	expect(failed).toBe(true);
	expect(store.writeMetrics().count).toBe(1);
	expect(db.query("SELECT input_tokens FROM api_requests WHERE id='r'").get()).toEqual({
		input_tokens: 100,
	});
	expect((await store.getJob(job.id))?.repaired).toBe(0);
	db.exec("DROP TRIGGER reject_repair");
	expect(await store.applyBulk(job.id, [await entry()], "admin")).toEqual(["applied"]);
	expect((await store.getJob(job.id))?.summary?.appliedDelta.inputTokens).toBe("-40");
	expect(await store.applyBulk(job.id, [await entry()], "admin")).toEqual(["already"]);
	expect((await store.getJob(job.id))?.repaired).toBe(1);
	expect((await store.getJob(job.id))?.summary?.appliedDelta.inputTokens).toBe("-40");
});
test("scratch model keyset pagination is exact and identities cannot redirect its file", () => {
	const id = generateId();
	const scratch = new TokenRepairScratch(join(root, "request-dumps"), id, true);
	try {
		const row = {
			id: "r",
			createdAt: "2026-01-01",
			provider: null,
			model: "m",
			userId: null,
			credentialId: null,
			messageId: null,
			tokenAccountingVersion: null,
			tokenAccountingReviewVersion: null,
			inputTokens: 1,
			cachedInputTokens: 0,
			cacheCreationInputTokens: 0,
			cacheCreation5mTokens: 0,
			cacheCreation1hTokens: 0,
		} satisfies RepairRequestRow;
		for (let i = 0; i < 235; i++) {
			const r = { ...row, id: `r${i}`, model: `model${i}` };
			scratch.append([
				{
					row: r,
					evidence: {
						classification: "insufficient",
						protocol: "unknown",
						reason: "needs_protocol",
						evidence: "",
						before: r,
						after: null,
					},
					hash: "h",
				},
			]);
		}
		const one = scratch.models(),
			two = scratch.models(one.nextCursor ?? undefined),
			three = scratch.models(two.nextCursor ?? undefined);
		expect(one.models).toHaveLength(100);
		expect(two.models).toHaveLength(100);
		expect(three.models).toHaveLength(35);
		expect(new Set([...one.models, ...two.models, ...three.models].map((m) => m.model)).size).toBe(
			235,
		);
		expect(three.nextCursor).toBeNull();
	} finally {
		scratch.close();
	}
	const linkId = generateId();
	symlinkSync(join(root, "source.sqlite"), join(root, "token-repair-jobs", `${linkId}.sqlite`));
	expect(() => new TokenRepairScratch(join(root, "request-dumps"), linkId, true)).toThrow();
	expect(() => new TokenRepairScratch(join(root, "request-dumps"), linkId)).toThrow();
	expect(() => new TokenRepairScratch(join(root, "request-dumps"), "../source", true)).toThrow();
});
test("scratch enforces a bounded disk budget and rolls back a full batch on exhaustion", async () => {
	const scratch = new TokenRepairScratch(join(root, "request-dumps"), generateId(), true);
	try {
		const cap = scratch.db.query("PRAGMA max_page_count").get() as { max_page_count: number };
		const size = scratch.db.query("PRAGMA page_size").get() as { page_size: number };
		expect(cap.max_page_count * size.page_size * 2).toBeLessThan(2 * 1024 ** 3);
		const pages = scratch.db.query("PRAGMA page_count").get() as { page_count: number };
		scratch.db.exec(`PRAGMA max_page_count=${pages.page_count}`);
		const fixture = await entry();
		expect(() =>
			scratch.append(
				Array.from({ length: 100 }, (_, n) => ({
					row: { ...fixture.row, id: `large-${n}` },
					evidence: { ...fixture.evidence, evidence: "x".repeat(8192) },
					hash: "hash",
				})),
			),
		).toThrow();
		expect(scratch.db.query("SELECT count(*) AS n FROM entries").get()).toEqual({ n: 0 });
	} finally {
		scratch.close();
	}
});
test("retirement selects only old terminal jobs, is bounded and never follows symlinks", async () => {
	const directory = join(root, "request-dumps");
	const old = "2000-01-01T00:00:00.000Z";
	const ids = Array.from({ length: 23 }, () => generateId());
	const insert = db.query(
		"INSERT INTO usage_token_repair_jobs (id,status,created_by,created_at,updated_at,include_reviewed) VALUES (?,'completed','admin',?,?,1)",
	);
	for (const id of ids) {
		insert.run(id, old, old);
		new TokenRepairScratch(directory, id, true).close();
	}
	const active = await store.createBulkJob("admin");
	const recent = generateId();
	insert.run(recent, new Date().toISOString(), new Date().toISOString());
	new TokenRepairScratch(directory, recent, true).close();
	const retired = await store.retiredJobs();
	expect(retired).toHaveLength(20);
	expect(retired).not.toContain(active.id);
	expect(retired).not.toContain(recent);
	const target = join(root, "private-source");
	writeFileSync(target, "must survive");
	const linkId = retired[0];
	if (!linkId) throw new Error("Missing retirement fixture");
	const link = join(root, "token-repair-jobs", `${linkId}.sqlite-wal`);
	symlinkSync(target, link);
	const completed = retireRepairScratch(directory, retired);
	expect(completed).toHaveLength(19);
	expect(completed).not.toContain(linkId);
	await store.markScratchRetired(completed);
	expect(await store.retiredJobs()).toHaveLength(4);
	for (const id of retired)
		expect(existsSync(join(root, "token-repair-jobs", `${id}.sqlite`))).toBe(false);
	expect(existsSync(target)).toBe(true);
	expect(existsSync(link)).toBe(true);
	expect(existsSync(join(root, "token-repair-jobs", `${recent}.sqlite`))).toBe(true);
	expect(
		ids.filter((id) => existsSync(join(root, "token-repair-jobs", `${id}.sqlite`))),
	).toHaveLength(3);
});
test("durable retirement passes legacy jobs without files and advances across batches and restarts", async () => {
	const directory = join(root, "request-dumps");
	const legacy = Array.from({ length: 20 }, () => generateId());
	const modern = Array.from({ length: 23 }, () => generateId());
	const insert = db.query(
		"INSERT INTO usage_token_repair_jobs (id,status,created_by,created_at,updated_at,include_reviewed,workflow_version) VALUES (?,'completed','admin',?,?,1,?)",
	);
	for (const id of legacy) insert.run(id, "2000-01-01", "2000-01-01", null);
	for (const id of modern) {
		insert.run(id, "2001-01-01", "2001-01-01", 2);
		new TokenRepairScratch(directory, id, true).close();
	}
	const recent = generateId();
	insert.run(recent, new Date().toISOString(), new Date().toISOString(), 2);
	new TokenRepairScratch(directory, recent, true).close();
	const active = await store.createBulkJob("admin");
	const first = await store.retiredJobs();
	expect(new Set(first)).toEqual(new Set(legacy));
	const firstCompleted = retireRepairScratch(directory, first);
	expect(firstCompleted).toHaveLength(20);
	expect(await store.markScratchRetired(firstCompleted)).toHaveLength(20);
	// A crash after deleting files but before acknowledgement must also be retryable.
	expect(retireRepairScratch(directory, first)).toHaveLength(20);
	await store.close();
	store = createTokenRepairStore({ backend: "sqlite", sqlitePath: join(root, "source.sqlite") });
	const second = await store.retiredJobs();
	expect(second).toHaveLength(20);
	expect(second.every((id) => modern.includes(id))).toBe(true);
	await store.markScratchRetired(retireRepairScratch(directory, second));
	const third = await store.retiredJobs();
	expect(third).toHaveLength(3);
	expect(third.some((id) => second.includes(id))).toBe(false);
	await store.markScratchRetired(retireRepairScratch(directory, third));
	expect(await store.retiredJobs()).toEqual([]);
	expect(await store.markScratchRetired([recent, active.id])).toEqual([]);
	for (const id of modern)
		expect(existsSync(join(root, "token-repair-jobs", `${id}.sqlite`))).toBe(false);
	expect(existsSync(join(root, "token-repair-jobs", `${recent}.sqlite`))).toBe(true);
	expect(db.query("SELECT count(*) AS n FROM usage_token_repair_jobs").get()).toEqual({ n: 45 });
	expect(
		db
			.query(
				"SELECT count(*) AS n FROM usage_token_repair_jobs WHERE scratch_retired_at IS NOT NULL",
			)
			.get(),
	).toEqual({ n: 43 });
	expect((await store.getJob(legacy[0] ?? "missing"))?.status).toBe("completed");
});
test("missing scratch directory can be acknowledged without touching unrelated paths", () => {
	const id = generateId();
	expect(retireRepairScratch(join(root, "request-dumps"), [id, "../source"])).toEqual([id]);
	expect(existsSync(join(root, "source.sqlite"))).toBe(true);
});
test("transaction timing is bounded, includes rolled-back writes and can reset per job", async () => {
	const job = await preview();
	await store.bulkClaim(job.id, "applying", undefined, "v");
	store.resetWriteMetrics();
	expect(store.writeMetrics()).toEqual({ count: 0, maxMs: 0, p99Ms: 0 });
	await store.applyBulk(job.id, [await entry()], "admin");
	const metrics = store.writeMetrics();
	expect(metrics.count).toBe(1);
	expect(metrics.maxMs).toBeGreaterThan(0);
	expect(metrics.p99Ms).toBeGreaterThanOrEqual(metrics.maxMs);
	store.resetWriteMetrics();
	expect(store.writeMetrics().count).toBe(0);
});
test("aggregate decimal totals retain precision beyond safe integer sums", () => {
	const totals = zeroTotals();
	const row = {
		inputTokens: Number.MAX_SAFE_INTEGER,
		cachedInputTokens: 0,
		cacheCreationInputTokens: 0,
		cacheCreation5mTokens: 0,
		cacheCreation1hTokens: 0,
	};
	addTotals(totals, row);
	addTotals(totals, row);
	expect(totals.inputTokens).toBe("18014398509481982");
});
