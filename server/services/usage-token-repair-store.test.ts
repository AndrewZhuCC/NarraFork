import type { Database } from "bun:sqlite";
import { afterEach, beforeEach, describe, expect, test } from "bun:test";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { TokenRepairEvidence, TokenRepairProtocolOverride } from "@shared/usage-token-repair";
import { getTestDb } from "../../tests/setup";
import { openSchemaCopyAt } from "../../tests/sqlite-schema-image";
import {
	createTokenRepairStore,
	type RepairRequestRow,
	type TokenRepairStore,
} from "./usage-token-repair-store";

const source = getTestDb().sqlite;
let folder: string;
let db: Database;
let store: TokenRepairStore;
let stores: TokenRepairStore[];
function peer() {
	const other = createTokenRepairStore({
		backend: "sqlite",
		sqlitePath: join(folder, "test.sqlite"),
	});
	stores.push(other);
	return other;
}
const at = "2026-01-01T00:00:00.000Z";
function required<T>(value: T | null | undefined): T {
	if (value == null) throw new Error("Missing test fixture");
	return value;
}
beforeEach(() => {
	folder = mkdtempSync(join(tmpdir(), "token-repair-store-"));
	const path = join(folder, "test.sqlite");
	db = openSchemaCopyAt(source, path);
	store = createTokenRepairStore({ backend: "sqlite", sqlitePath: path });
	stores = [store];
});
afterEach(async () => {
	for (const item of stores) await item.close();
	db.close();
	rmSync(folder, { recursive: true, force: true });
});
function request(
	id: string,
	options: {
		reviewed?: number;
		version?: number;
		createdAt?: string;
		userId?: string | null;
		credentialId?: string | null;
		messageId?: string | null;
	} = {},
) {
	db.query(
		`INSERT INTO api_requests (id, user_id, credential_id, message_id, provider, model, created_at, input_tokens, cached_input_tokens, cache_creation_input_tokens, cache_creation_5m_tokens, cache_creation_1h_tokens, cost_usd, meter_usage, token_accounting_version, token_accounting_review_version) VALUES (?, ?, ?, ?, 'openai', 'test', ?, 100, 30, 20, 10, 10, 3.5, 99, ?, ?)`,
	).run(
		id,
		options.userId === undefined ? "u" : options.userId,
		options.credentialId ?? null,
		options.messageId ?? null,
		options.createdAt ?? at,
		options.version ?? null,
		options.reviewed ?? null,
	);
}
function evidence(row: RepairRequestRow): TokenRepairEvidence {
	const before = {
		inputTokens: row.inputTokens,
		cachedInputTokens: row.cachedInputTokens,
		cacheCreationInputTokens: row.cacheCreationInputTokens,
		cacheCreation5mTokens: row.cacheCreation5mTokens,
		cacheCreation1hTokens: row.cacheCreation1hTokens,
	};
	return {
		classification: "repairable",
		protocol: "openai",
		reason: "numeric evidence",
		evidence: "input=100 read=30 write=20",
		before,
		after: { ...before, inputTokens: 50 },
	};
}
async function analyzed(id = "r", startApplying = true) {
	const job = await store.createJob("admin", false);
	const row = required(await store.getRequest(id));
	await store.saveCandidate(job.id, row, evidence(row));
	await store.finishAnalysis(job.id);
	if (startApplying) await store.setJobStatus(job.id, "applying");
	return { job, candidate: required((await store.listCandidates(job.id)).records[0]) };
}
function totals(credential = false) {
	db.exec(
		`INSERT INTO user_usage_totals (user_id, input_tokens, cached_input_tokens, cache_creation_tokens, request_count, cost_usd, first_used_at, last_used_at) VALUES ('u', 1000, 300, 200, 10, 42, '${at}', '${at}')`,
	);
	if (credential)
		db.exec(
			`INSERT INTO credential_usage_totals (id, provider, credential_id, model, input_tokens, cached_input_tokens, cache_creation_tokens, request_count, cost_usd, first_seen_at, last_seen_at) VALUES ('c', 'openai', 'key', 'test', 1000, 300, 200, 10, 42, '${at}', '${at}')`,
		);
}

describe("standalone token repair store", () => {
	test("source-scoped second batch bypasses 10000 earlier reviewed rows and never admits outside requests", async () => {
		db.transaction(() => {
			for (let i = 0; i < 10001; i++) request(`early-${i}`, { reviewed: 1 });
		})();
		request("late-source", { createdAt: "2030-01-01" });
		const source = await store.createJob("admin", false);
		const original = required((await store.scanPage(source.id))[0]);
		expect(original.id).toBe("late-source");
		await store.saveCandidate(source.id, original, evidence(original));
		await store.finishAnalysis(source.id);
		await store.cancelJob(source.id);
		request("outside-before", { createdAt: "2029-01-01", reviewed: 1 });
		request("outside-after", { createdAt: "2031-01-01", reviewed: 1 });
		const job = await store.createJob("admin", true, [], source.id);
		expect(job.sourceJobId).toBe(source.id);
		expect(job.upperId).toBe("late-source");
		const rows = await store.scanPage(job.id);
		expect(rows.map((row) => row.id)).toEqual(["late-source"]);
		const plan = db
			.query(
				"EXPLAIN QUERY PLAN SELECT a.id FROM (SELECT request_id FROM usage_token_repair_candidates WHERE job_id = ? ORDER BY request_id LIMIT 10000) source CROSS JOIN api_requests a WHERE a.id = source.request_id AND a.token_accounting_version IS NULL AND a.cached_input_tokens > 0 ORDER BY a.created_at, a.id LIMIT 50",
			)
			.all(source.id);
		expect(JSON.stringify(plan)).toContain("idx_token_repair_candidate_request");
		expect(JSON.stringify(plan)).toContain("SEARCH a USING INDEX");
		expect(JSON.stringify(plan)).not.toContain("SCAN a");
		const outside = required(await store.getRequest("outside-before"));
		await expect(store.saveCandidate(job.id, outside, evidence(outside))).rejects.toThrow(
			"source scope",
		);
		await store.saveCandidate(job.id, required(rows[0]), evidence(required(rows[0])));
		expect(await store.scanPage(job.id)).toEqual([]);
		await store.finishAnalysis(job.id);
		expect((await store.getJob(job.id))?.scanned).toBe(1);
	});
	test("source lookup rejects more than 10000 candidates with a bounded cardinality check", async () => {
		db.query(
			"INSERT INTO usage_token_repair_jobs (id,status,created_by,created_at,updated_at,include_reviewed) VALUES ('oversized','completed','admin',?,?,0)",
		).run(at, at);
		const insert = db.query(
			"INSERT INTO usage_token_repair_candidates (id,job_id,request_id,created_at,snapshot_json,evidence_json) VALUES (?, 'oversized', ?, ?, '{}', '{}')",
		);
		db.transaction(() => {
			for (let i = 0; i < 10001; i++) insert.run(`candidate${i}`, `missing${i}`, at);
		})();
		await expect(store.createJob("admin", true, [], "oversized")).rejects.toThrow(
			"candidate limit",
		);
		db.exec("DELETE FROM usage_token_repair_candidates WHERE id = 'candidate10000'");
		expect((await store.createJob("admin", true, [], "oversized")).sourceJobId).toBe("oversized");
	});
	test("source identity must exist, be ended, and use reviewed scanning", async () => {
		await expect(store.createJob("admin", true, [], "missing")).rejects.toThrow("Source job");
		request("r");
		const source = await store.createJob("admin", false);
		await expect(store.createJob("admin", true, [], source.id)).rejects.toThrow("Source job");
		await store.finishAnalysis(source.id);
		await expect(store.createJob("admin", true, [], source.id)).rejects.toThrow("Source job");
		await store.cancelJob(source.id);
		await expect(store.createJob("admin", false, [], source.id)).rejects.toThrow("includeReviewed");
		const scoped = await store.createJob("admin", true, [], source.id);
		expect(scoped.sourceJobId).toBe(source.id);
		expect(await store.scanPage(scoped.id)).toEqual([]);
	});
	test("source-scoped pages retain cursor, scope and overrides; repaired/zero/deleted rows are excluded", async () => {
		for (let i = 0; i < 68; i++) request(`s${i.toString().padStart(3, "0")}`);
		const source = await store.createJob("admin", false);
		for (
			let page = await store.scanPage(source.id);
			page.length;
			page = await store.scanPage(source.id)
		) {
			for (const row of page) await store.saveCandidate(source.id, row, evidence(row));
		}
		await store.finishAnalysis(source.id);
		await store.cancelJob(source.id);
		db.exec(
			"UPDATE api_requests SET token_accounting_version = 1 WHERE id = 's000'; UPDATE api_requests SET cached_input_tokens = 0, cache_creation_input_tokens = 0, cache_creation_5m_tokens = 0, cache_creation_1h_tokens = 0 WHERE id = 's001'; DELETE FROM api_requests WHERE id = 's002'",
		);
		const choices: TokenRepairProtocolOverride[] = [
			{ provider: "openai", model: "test", protocol: "gemini" },
		];
		const job = await store.createJob("admin", true, choices, source.id);
		const first = await store.scanPage(job.id);
		expect(first).toHaveLength(50);
		for (const row of first)
			await store.saveCandidate(job.id, row, { ...evidence(row), manualProtocol: "gemini" });
		const other = store;
		expect((await other.getJob(job.id))?.sourceJobId).toBe(source.id);
		expect((await other.getJob(job.id))?.protocolOverrides).toEqual(choices);
		const last = await other.scanPage(job.id);
		expect(last).toHaveLength(15);
		for (const row of last)
			await other.saveCandidate(job.id, row, { ...evidence(row), manualProtocol: "gemini" });
		expect(await other.scanPage(job.id)).toEqual([]);
		expect((await other.getJob(job.id))?.scanned).toBe(65);
	});
	test("source scope is enforced again before applying a persisted candidate", async () => {
		request("a");
		const source = await store.createJob("admin", false);
		const original = required((await store.scanPage(source.id))[0]);
		await store.saveCandidate(source.id, original, evidence(original));
		await store.finishAnalysis(source.id);
		await store.cancelJob(source.id);
		const job = await store.createJob("admin", true, [], source.id);
		const row = required((await store.scanPage(job.id))[0]);
		await store.saveCandidate(job.id, row, evidence(row));
		await store.finishAnalysis(job.id);
		await store.setJobStatus(job.id, "applying");
		const candidate = required((await store.listCandidates(job.id)).records[0]);
		db.query("DELETE FROM usage_token_repair_candidates WHERE job_id = ?").run(source.id);
		expect(await store.applyCandidate(job.id, candidate.id, "admin")).toBe("conflict");
		expect((await store.getRequest("a"))?.inputTokens).toBe(100);
	});
	test("historical jobs default to empty overrides and never expose raw config JSON", async () => {
		db.query(
			"INSERT INTO usage_token_repair_jobs (id,status,created_by,created_at,updated_at,include_reviewed) VALUES ('legacy','completed','admin',?,?,0)",
		).run(at, at);
		const job = await store.getJob("legacy");
		expect(job?.protocolOverrides).toEqual([]);
		expect(job).not.toHaveProperty("protocolOverridesJson");
	});
	test("canonical protocol choices persist across clients without caller mutation", async () => {
		request("r");
		const choices: TokenRepairProtocolOverride[] = [
			{ provider: "z", model: "same", protocol: "gemini" },
			{ provider: null, model: "same", protocol: "openai" },
		];
		const job = await store.createJob("admin", false, choices);
		const canonical = required(await store.getJob(job.id)).protocolOverrides;
		required(choices[0]).protocol = "anthropic";
		job.protocolOverrides?.pop();
		expect((await store.getJob(job.id))?.protocolOverrides).toEqual(canonical);
		const raw = db
			.query<{ protocol_overrides_json: string }, [string]>(
				"SELECT protocol_overrides_json FROM usage_token_repair_jobs WHERE id = ?",
			)
			.get(job.id);
		expect(raw?.protocol_overrides_json).toBe(JSON.stringify(canonical));
		await store.close();
		const other = peer();
		expect((await other.getJob(job.id))?.status).toBe("analyzing");
		expect((await other.getJob(job.id))?.protocolOverrides).toEqual(canonical);
	});
	for (const [label, choices] of Object.entries({
		"too many": Array.from({ length: 33 }, (_, i) => ({
			provider: null,
			model: `m${i}`,
			protocol: "openai",
		})),
		"provider too long": [{ provider: "p".repeat(129), model: "m", protocol: "openai" }],
		"missing provider": [{ model: "m", protocol: "openai" }],
		"empty model": [{ provider: null, model: "", protocol: "openai" }],
		"long model": [{ provider: null, model: "m".repeat(257), protocol: "openai" }],
		"unknown protocol": [{ provider: null, model: "m", protocol: "unknown" }],
		"duplicate identity": [
			{ provider: null, model: "m", protocol: "openai" },
			{ provider: null, model: "m", protocol: "gemini" },
		],
		"UTF8 byte overflow": Array.from({ length: 32 }, (_, i) => ({
			provider: "中".repeat(128),
			model: `${i}${"文".repeat(252)}`,
			protocol: "openai",
		})),
	})) {
		test(`store rejects invalid protocol choices: ${label}`, async () => {
			await expect(
				store.createJob("admin", false, choices as TokenRepairProtocolOverride[]),
			).rejects.toThrow();
			expect(await store.latestJob()).toBeNull();
		});
	}
	test("even idempotent acknowledgements reject a tampered protocol binding without repeating writes", async () => {
		request("r");
		const { job, candidate } = await analyzed();
		expect(await store.applyCandidate(job.id, candidate.id, "admin")).toBe("applied");
		db.query("UPDATE usage_token_repair_jobs SET protocol_overrides_json = ? WHERE id = ?").run(
			JSON.stringify([{ provider: "openai", model: "test", protocol: "openai" }]),
			job.id,
		);
		expect(await store.applyCandidate(job.id, candidate.id, "admin")).toBe("conflict");
		expect((await store.getRequest("r"))?.inputTokens).toBe(50);
		expect((await store.getJob(job.id))?.repaired).toBe(1);
	});
	test("manual evidence is bound to exact provider and model; unselected identities reject manual markers", async () => {
		request("a");
		request("b");
		db.exec("UPDATE api_requests SET provider = 'other' WHERE id = 'b'");
		const job = await store.createJob("admin", false, [
			{ provider: "openai", model: "test", protocol: "anthropic" },
		]);
		const [a, b] = await store.scanPage(job.id);
		const first = required(a);
		const second = required(b);
		await expect(store.saveCandidate(job.id, first, evidence(first))).rejects.toThrow(
			"manual protocol",
		);
		await expect(
			store.saveCandidate(job.id, first, { ...evidence(first), manualProtocol: "gemini" }),
		).rejects.toThrow("manual protocol");
		expect((await store.getJob(job.id))?.scanned).toBe(0);
		await store.saveCandidate(job.id, first, { ...evidence(first), manualProtocol: "anthropic" });
		await expect(
			store.saveCandidate(job.id, second, { ...evidence(second), manualProtocol: "anthropic" }),
		).rejects.toThrow("manual protocol");
		await store.saveCandidate(job.id, second, evidence(second));
		await store.finishAnalysis(job.id);
		await store.setJobStatus(job.id, "applying");
		const page = await store.listCandidates(job.id);
		const candidate = required(page.records.find((item) => item.requestId === "a"));
		expect(candidate.manualProtocol).toBe("anthropic");
		expect(await store.applyCandidate(job.id, candidate.id, "admin")).toBe("applied");
	});
	for (const target of ["configuration", "evidence"]) {
		test(`apply rejects tampered persisted manual ${target}`, async () => {
			request("r");
			const job = await store.createJob("admin", false, [
				{ provider: "openai", model: "test", protocol: "anthropic" },
			]);
			const row = required(await store.getRequest("r"));
			const audit = { ...evidence(row), manualProtocol: "anthropic" as const };
			await store.saveCandidate(job.id, row, audit);
			await store.finishAnalysis(job.id);
			await store.setJobStatus(job.id, "applying");
			const candidate = required((await store.listCandidates(job.id)).records[0]);
			if (target === "configuration")
				db.query(
					"UPDATE usage_token_repair_jobs SET protocol_overrides_json = '[]' WHERE id = ?",
				).run(job.id);
			else
				db.query("UPDATE usage_token_repair_candidates SET evidence_json = ? WHERE id = ?").run(
					JSON.stringify({ ...audit, manualProtocol: "gemini" }),
					candidate.id,
				);
			expect(await store.applyCandidate(job.id, candidate.id, "admin")).toBe("conflict");
			expect((await store.getRequest("r"))?.inputTokens).toBe(100);
			expect((await store.getCandidate(job.id, candidate.id))?.classification).toBe("conflict");
		});
	}
	test("partial index probe and reviewed scan use indexed created/id cursors", async () => {
		request("r");
		request("reviewed", { reviewed: 1 });
		request("new", { version: 1 });
		expect((await store.probe()).hasPending).toBe(true);
		for (const reviewed of [false, true]) {
			const plan = db
				.query(
					`EXPLAIN QUERY PLAN SELECT id FROM api_requests INDEXED BY idx_api_requests_token_repair_${reviewed ? "reviewed" : "pending"} WHERE token_accounting_version IS NULL ${reviewed ? "" : "AND token_accounting_review_version IS NULL"} AND (cached_input_tokens > 0 OR cache_creation_input_tokens > 0 OR cache_creation_5m_tokens > 0 OR cache_creation_1h_tokens > 0) AND (created_at, id) > ('', '') ORDER BY created_at, id LIMIT 50`,
				)
				.all();
			expect(JSON.stringify(plan)).toContain(
				reviewed
					? "idx_api_requests_token_repair_reviewed"
					: "idx_api_requests_token_repair_pending",
			);
		}
		const job = await store.createJob("admin", false);
		expect((await store.scanPage(job.id)).map((row) => row.id)).toEqual(["r"]);
		await store.cancelJob(job.id);
		const again = await store.createJob("admin", true);
		expect((await store.scanPage(again.id)).map((row) => row.id)).toEqual(["r", "reviewed"]);
	});
	test("single active job, fixed upper, metadata-only paging, monotonic scan and idempotent save", async () => {
		for (let i = 0; i < 65; i++) request(`r${i.toString().padStart(3, "0")}`);
		const job = await store.createJob("admin", false);
		await expect(store.createJob("other", true)).rejects.toThrow("already active");
		request("zzz", { createdAt: "2027-01-01" });
		const page = await store.scanPage(job.id);
		expect(page).toHaveLength(50);
		expect(page[0]).not.toHaveProperty("rawDumpJson");
		for (const row of page) await store.saveCandidate(job.id, row, evidence(row));
		await store.saveCandidate(job.id, required(page[0]), evidence(required(page[0])));
		expect((await store.getJob(job.id))?.scanned).toBe(50);
		const last = await store.scanPage(job.id);
		expect(last).toHaveLength(15);
		for (const row of last) await store.saveCandidate(job.id, row, evidence(row));
		expect(await store.scanPage(job.id)).toEqual([]);
		const first = await store.listCandidates(job.id);
		expect(first.records).toHaveLength(50);
		const second = await store.listCandidates(job.id, required(first.nextCursor));
		expect(second.records).toHaveLength(15);
		expect(second.nextCursor).toBeNull();
		expect(new Set([...first.records, ...second.records].map((row) => row.id)).size).toBe(65);
	});
	test("ready holds the single slot until explicit cancellation releases it", async () => {
		request("r");
		const { job } = await analyzed("r", false);
		expect(job.id).toHaveLength(21);
		await expect(store.createJob("other", true)).rejects.toThrow("already active");
		await store.cancelJob(job.id);
		const next = await store.createJob("other", true);
		expect(next.id).not.toBe(job.id);
		expect((await store.scanPage(next.id)).map((row) => row.id)).toEqual(["r"]);
	});
	test("bounded byte dump read, even for multibyte content", async () => {
		request("r");
		db.query("UPDATE api_requests SET raw_dump_json = ? WHERE id = 'r'").run("中中");
		expect(await store.getRawDump("r", 5)).toBeNull();
		expect(await store.getRawDump("r", 6)).toBe("中中");
		expect(await store.getRawDump("absent", 100)).toBeNull();
		expect(await store.getRawDump("r", Number.NaN)).toBeNull();
	});
	test("apply detail only leaves lifetime totals pending and is idempotent", async () => {
		request("r");
		totals();
		const { job, candidate } = await analyzed();
		expect((await store.getRequest("r"))?.inputTokens).toBe(100);
		expect(await store.applyCandidate(job.id, candidate.id, "admin")).toBe("applied");
		expect(await store.applyCandidate(job.id, candidate.id, "admin")).toBe("applied");
		expect(await store.getRequest("r")).toMatchObject({
			inputTokens: 50,
			tokenAccountingVersion: 1,
		});
		expect(db.query("SELECT input_tokens FROM user_usage_totals").get()).toEqual({
			input_tokens: 1000,
		});
		expect((await store.getJob(job.id))?.repaired).toBe(1);
	});
	test("apply never writes lifetime totals, linked messages, cost, output or meter fields", async () => {
		db.exec("PRAGMA foreign_keys = OFF");
		db.exec(
			`INSERT INTO narrator_messages (id, narrator_id, role, content_json, created_at, tokens_in, cached_input_tokens, cache_creation_input_tokens, cache_creation_5m_tokens, cache_creation_1h_tokens, cost_usd, meter_usage) VALUES ('m', 'n', 'assistant', '[]', '${at}', 100, 30, 20, 10, 10, 9, 7); INSERT INTO narrator_message_refs (id, narrator_id, message_id, seq) VALUES ('ref', 'n', 'm', 1)`,
		);
		db.exec("PRAGMA foreign_keys = ON");
		request("r", { credentialId: "key", messageId: "m" });
		totals(true);
		const message = db.query("SELECT * FROM narrator_messages WHERE id = 'm'").get();
		for (const table of ["user_usage_totals", "credential_usage_totals", "narrator_messages"]) {
			for (const operation of ["INSERT", "UPDATE", "DELETE"])
				db.exec(
					`CREATE TRIGGER forbid_${table}_${operation} BEFORE ${operation} ON ${table} BEGIN SELECT RAISE(ABORT, 'forbidden side effect'); END`,
				);
		}
		const { job, candidate } = await analyzed();
		expect(await store.applyCandidate(job.id, candidate.id, "admin")).toBe("applied");
		expect(await store.applyCandidate(job.id, candidate.id, "admin")).toBe("applied");
		for (const table of ["user_usage_totals", "credential_usage_totals"])
			expect(
				db
					.query(
						`SELECT input_tokens, cached_input_tokens, cache_creation_tokens, cost_usd, request_count FROM ${table}`,
					)
					.get(),
			).toEqual({
				input_tokens: 1000,
				cached_input_tokens: 300,
				cache_creation_tokens: 200,
				cost_usd: 42,
				request_count: 10,
			});
		expect(db.query("SELECT * FROM narrator_messages WHERE id = 'm'").get()).toEqual(message);
		expect(
			db
				.query("SELECT cost_usd, meter_usage, output_tokens, reasoning_tokens FROM api_requests")
				.get(),
		).toEqual({ cost_usd: 3.5, meter_usage: 99, output_tokens: 0, reasoning_tokens: 0 });
		expect(await store.getCandidate(job.id, candidate.id)).toMatchObject({
			totalsStatus: "pending",
			appliedBy: "admin",
		});
	});
	for (const mutation of [
		"DELETE FROM api_requests",
		"UPDATE api_requests SET input_tokens = 101",
		"UPDATE api_requests SET token_accounting_version = 1",
		"UPDATE api_requests SET user_id = 'other'",
	]) {
		test(`CAS rejects ${mutation}`, async () => {
			request("r");
			const { job, candidate } = await analyzed();
			db.exec(mutation);
			expect(await store.applyCandidate(job.id, candidate.id, "admin")).toBe("conflict");
			expect((await store.getJob(job.id))?.repaired).toBe(0);
			expect((await store.getCandidate(job.id, candidate.id))?.classification).toBe("conflict");
		});
	}
	test("stale analysis records conflict without stamping a changed request as reviewed", async () => {
		request("r");
		const job = await store.createJob("admin", false);
		const row = required((await store.scanPage(job.id))[0]);
		db.exec("UPDATE api_requests SET cached_input_tokens = 31 WHERE id = 'r'");
		await store.saveCandidate(job.id, row, evidence(row));
		expect((await store.listCandidates(job.id)).records[0]?.classification).toBe("conflict");
		expect((await store.getRequest("r"))?.tokenAccountingReviewVersion).toBeNull();
	});
	test("explicit external conflict is audited once and never changes request tokens", async () => {
		request("r");
		const { job, candidate } = await analyzed();
		await store.markCandidateConflict(job.id, candidate.id, "evidence disappeared");
		await store.markCandidateConflict(job.id, candidate.id, "evidence disappeared");
		expect((await store.getCandidate(job.id, candidate.id))?.classification).toBe("conflict");
		expect((await store.getJob(job.id))?.conflicts).toBe(1);
		expect((await store.getRequest("r"))?.inputTokens).toBe(100);
	});
	test("unknown attribution and missing lifetime buckets do not block detail-only repair", async () => {
		request("r", { userId: null, credentialId: "missing" });
		const { job, candidate } = await analyzed();
		expect(await store.applyCandidate(job.id, candidate.id, "admin")).toBe("applied");
		expect((await store.getRequest("r"))?.tokenAccountingVersion).toBe(1);
		expect(db.query("SELECT user_id FROM user_usage_totals").all()).toEqual([]);
		expect(db.query("SELECT id FROM credential_usage_totals").all()).toEqual([]);
	});
	test("ready-to-applying CAS admits exactly one competing worker", async () => {
		request("r");
		const { job } = await analyzed("r", false);
		const other = peer();
		const results = await Promise.allSettled([
			store.setJobStatus(job.id, "applying"),
			other.setJobStatus(job.id, "applying"),
		]);
		expect(results.filter((result) => result.status === "fulfilled")).toHaveLength(1);
		expect((await other.getJob(job.id))?.status).toBe("applying");
		await store.setJobStatus(job.id, "ready");
		await expect(store.setJobStatus(job.id, "ready")).rejects.toThrow("transition");
		await expect(store.setJobStatus(job.id, "failed")).rejects.toThrow("transition");
	});
	test("cancellation blocks all stale writes and reanalysis always creates a new scoped job", async () => {
		request("r");
		const job = await store.createJob("admin", false);
		const row = required((await store.scanPage(job.id))[0]);
		await store.saveCandidate(job.id, row, evidence(row));
		await peer().cancelJob(job.id);
		await expect(store.scanPage(job.id)).rejects.toThrow("not analyzing");
		await expect(store.saveCandidate(job.id, row, evidence(row))).rejects.toThrow("not analyzing");
		await expect(store.finishAnalysis(job.id)).rejects.toThrow("transition");
		for (const status of ["analyzing", "applying", "ready", "failed", "completed"] as const)
			await expect(store.setJobStatus(job.id, status)).rejects.toThrow("transition");
		const candidate = required((await store.listCandidates(job.id)).records[0]);
		await expect(store.applyCandidate(job.id, candidate.id, "admin")).rejects.toThrow(
			"not applying",
		);
		await expect(store.markCandidateConflict(job.id, candidate.id, "stale")).rejects.toThrow(
			"not applying",
		);
		const next = await store.createJob("admin", true, [], job.id);
		expect(next.id).not.toBe(job.id);
		expect((await store.scanPage(next.id)).map((item) => item.id)).toEqual(["r"]);
		expect((await store.getRequest("r"))?.inputTokens).toBe(100);
	});
	test("cancelling an applying job prevents late application and failure cleanup", async () => {
		request("r");
		const { job, candidate } = await analyzed();
		await peer().cancelJob(job.id);
		await expect(store.applyCandidate(job.id, candidate.id, "admin")).rejects.toThrow(
			"not applying",
		);
		await expect(store.setJobStatus(job.id, "ready")).rejects.toThrow("transition");
		await expect(store.setJobStatus(job.id, "failed")).rejects.toThrow("transition");
		expect((await store.getJob(job.id))?.status).toBe("cancelled");
	});
	test("close and read-only clients never alter job state or resume it", async () => {
		request("r");
		const job = await store.createJob("admin", false);
		const other = peer();
		await store.close();
		expect((await other.getJob(job.id))?.status).toBe("analyzing");
		expect((await other.probe()).job?.status).toBe("analyzing");
		await expect(other.setJobStatus(job.id, "analyzing")).rejects.toThrow("transition");
		await other.setJobStatus(job.id, "failed", "bounded failure");
		expect((await other.getJob(job.id))?.status).toBe("failed");
	});
	test("unrepairable classifications cannot be applied", async () => {
		request("r");
		const job = await store.createJob("admin", false);
		const row = required(await store.getRequest("r"));
		await store.saveCandidate(job.id, row, { ...evidence(row), classification: "ambiguous" });
		await store.finishAnalysis(job.id);
		await store.setJobStatus(job.id, "applying");
		const candidate = required((await store.listCandidates(job.id)).records[0]);
		expect(await store.applyCandidate(job.id, candidate.id, "admin")).toBe("conflict");
	});
	test("busy writer fails within short timeout rather than blocking for seconds", async () => {
		request("r");
		db.exec("BEGIN IMMEDIATE");
		const start = performance.now();
		try {
			await expect(store.createJob("admin", false)).rejects.toThrow();
		} finally {
			db.exec("ROLLBACK");
		}
		expect(performance.now() - start).toBeLessThan(1000);
	});
});
