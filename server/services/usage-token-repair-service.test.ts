import type { Database } from "bun:sqlite";
import { afterEach, beforeEach, describe, expect, test } from "bun:test";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { TokenRepairJob } from "@shared/usage-token-repair";
import { getTestDb } from "../../tests/setup";
import { openSchemaCopyAt } from "../../tests/sqlite-schema-image";
import { TokenRepairClient } from "./usage-token-repair-service";
import {
	tokenRepairWorkerEntry,
	tokenRepairWorkerSpecifiers,
} from "./usage-token-repair-worker-paths";

const source = getTestDb().sqlite;
let directory: string, path: string, db: Database, client: TokenRepairClient;
beforeEach(() => {
	directory = mkdtempSync(join(tmpdir(), "token-repair-v2-"));
	path = join(directory, "source.sqlite");
	db = openSchemaCopyAt(source, path);
	db.exec("PRAGMA journal_mode=WAL");
	client = new TokenRepairClient(
		{ backend: "sqlite", sqlitePath: path },
		join(directory, "request-dumps"),
	);
});
afterEach(async () => {
	await client.close();
	db.close();
	rmSync(directory, { recursive: true, force: true });
});
function request(id = "request", url = "https://example.invalid/v1/responses") {
	const dump = JSON.stringify({
		request: { url },
		response: {
			status: 200,
			bodyText: JSON.stringify({
				...(url.endsWith("/responses") ? { object: "response", status: "completed" } : {}),
				usage: {
					input_tokens: 100,
					output_tokens: 10,
					input_tokens_details: { cached_tokens: 40, cache_write_tokens: 20 },
				},
			}),
		},
	});
	db.query(
		"INSERT INTO api_requests (id,provider,model,created_at,input_tokens,cached_input_tokens,cache_creation_input_tokens,raw_dump_json,cost_usd,meter_usage) VALUES (?,'nug','test','2026-01-01',100,40,20,?,7,9)",
	).run(id, dump);
}
async function settled(id: string, phase = "models"): Promise<TokenRepairJob> {
	const deadline = Date.now() + 15000;
	while (Date.now() < deadline) {
		const job = await client.getJob(id);
		if (job?.phase === phase && ["ready", "completed"].includes(job.status)) return job;
		if (job && ["failed", "cancelled"].includes(job.status)) throw new Error(JSON.stringify(job));
		await Bun.sleep(5);
	}
	throw new Error("Maintenance deadline exceeded");
}
async function rejects(call: Promise<unknown>) {
	let rejected = false;
	try {
		await call;
	} catch {
		rejected = true;
	}
	expect(rejected).toBe(true);
}
async function preview(id: string) {
	await client.dryRun(id, []);
	return settled(id, "preview");
}
describe("aggregate historical repair worker", () => {
	test("scan and dry-run are read-only, aggregate the whole range, and apply preserves costs", async () => {
		request();
		db.query(
			"INSERT INTO api_requests (id,created_at,input_tokens,token_accounting_review_version) VALUES ('no-cache','2026-01-01',7,1)",
		).run();
		const before = db.query("SELECT * FROM api_requests ORDER BY id").all();
		expect(await client.probe()).toMatchObject({ hasPending: true, job: null });
		const job = await client.start("admin", false);
		await settled(job.id);
		expect(db.query("SELECT * FROM api_requests ORDER BY id").all()).toEqual(before);
		expect(db.query("SELECT * FROM usage_token_repair_candidates").all()).toEqual([]);
		await rejects(client.listCandidates(job.id));
		const ready = await preview(job.id);
		expect(ready.summary).toMatchObject({
			requestCount: 2,
			repairable: 1,
			before: { inputTokens: "107" },
			after: { inputTokens: "47" },
			delta: { inputTokens: "-60" },
		});
		expect(db.query("SELECT * FROM api_requests ORDER BY id").all()).toEqual(before);
		const version = ready.summary?.previewVersion;
		if (!version) throw new Error("Missing preview");
		await client.applyPreview(job.id, version, "admin");
		const done = await settled(job.id, "done");
		expect(done.repaired).toBe(1);
		expect(done.summary?.appliedDelta.inputTokens).toBe("-60");
		expect(done.summary?.writeTransactions?.count).toBe(1);
		expect(done.summary?.writeTransactions?.maxMs).toBeGreaterThan(0);
		expect(
			db
				.query(
					"SELECT input_tokens,cost_usd,meter_usage,token_accounting_version FROM api_requests WHERE id='request'",
				)
				.get(),
		).toEqual({ input_tokens: 40, cost_usd: 7, meter_usage: 9, token_accounting_version: 1 });
		await rejects(client.applyPreview(job.id, version, "admin"));
		expect(
			db
				.query(
					"SELECT COUNT(*) AS n FROM usage_token_repair_candidates WHERE applied_at IS NOT NULL",
				)
				.get(),
		).toEqual({ n: 1 });
	}, 20000);
	test("changed counters and missing dump are conflicts, not silent overwrites", async () => {
		request("a");
		request("b");
		const job = await client.start("admin", false);
		await settled(job.id);
		const p = await preview(job.id);
		db.query("UPDATE api_requests SET input_tokens=111 WHERE id='a'").run();
		db.query("UPDATE api_requests SET raw_dump_json=NULL WHERE id='b'").run();
		await client.applyPreview(job.id, p.summary?.previewVersion ?? "", "admin");
		const done = await settled(job.id, "done");
		expect(done.repaired).toBe(0);
		expect(done.conflicts).toBe(2);
		expect(done.summary?.appliedDelta.inputTokens).toBe("0");
		expect(db.query("SELECT input_tokens FROM api_requests WHERE id='a'").get()).toEqual({
			input_tokens: 111,
		});
	}, 20000);
	test("manual protocols are model scoped, missing dump never becomes a model, and old preview is invalidated", async () => {
		request("a", "https://example.invalid/custom");
		request("b", "https://example.invalid/custom");
		const unresolvedDump = JSON.stringify({
			request: { url: "https://example.invalid/custom" },
			response: {
				status: 200,
				bodyText: JSON.stringify({ usage: { input_tokens: 100, cache_read_input_tokens: 40 } }),
			},
		});
		db.query("UPDATE api_requests SET raw_dump_json=?,cache_creation_input_tokens=0").run(
			unresolvedDump,
		);
		db.query("UPDATE api_requests SET provider='other' WHERE id='b'").run();
		db.query(
			"INSERT INTO api_requests (id,provider,model,created_at,input_tokens,cached_input_tokens) VALUES ('missing','nug','missing-dump','2026-01-01',100,40)",
		).run();
		const job = await client.start("admin", false);
		await settled(job.id);
		const models = await client.models(job.id);
		expect(models.models).toHaveLength(2);
		expect(models.models.some((m) => m.model === "missing-dump")).toBe(false);
		const first = await preview(job.id);
		expect(first.summary?.unresolved).toBe(2);
		await client.dryRun(job.id, [{ provider: "nug", model: "test", protocol: "openai" }]);
		const p = await settled(job.id, "preview");
		expect(p.summary).toMatchObject({ repairable: 1, unresolved: 1, skipped: 1 });
		await rejects(client.applyPreview(job.id, first.summary?.previewVersion ?? "", "admin"));
		await client.close();
		client = new TokenRepairClient(
			{ backend: "sqlite", sqlitePath: path },
			join(directory, "request-dumps"),
		);
		await client.applyPreview(job.id, p.summary?.previewVersion ?? "", "admin");
		expect((await settled(job.id, "done")).repaired).toBe(1);
	}, 20000);
	test("fixed upper bound excludes inserts after start and cancellation permits a full fresh scan", async () => {
		request();
		const job = await client.start("admin", false);
		db.query("INSERT INTO api_requests (id,created_at) VALUES ('later','2099-01-01')").run();
		expect((await settled(job.id)).scanned).toBe(1);
		await client.cancel(job.id);
		const next = await client.start("admin", false);
		expect((await settled(next.id)).scanned).toBe(2);
	}, 20000);
	test("scan has no ten-thousand truncation and includes old reviewed rows", async () => {
		db.transaction(() => {
			const insert = db.query(
				"INSERT INTO api_requests (id,created_at,input_tokens,token_accounting_review_version) VALUES (?,'2026-01-01',1,1)",
			);
			for (let i = 0; i < 10001; i++) insert.run(`r-${i}`);
		})();
		const job = await client.start("admin", false);
		expect((await settled(job.id)).scanned).toBe(10001);
		expect((await preview(job.id)).summary?.before.inputTokens).toBe("10001");
		expect(
			db
				.query("SELECT COUNT(*) AS n FROM api_requests WHERE token_accounting_review_version!=1")
				.get(),
		).toEqual({ n: 0 });
	}, 30000);
	test("probe from another worker never takes over the live owner", async () => {
		request();
		const job = await client.start("admin", false);
		const other = new TokenRepairClient(
			{ backend: "sqlite", sqlitePath: path },
			join(directory, "request-dumps"),
		);
		try {
			expect(["analyzing", "ready"]).toContain((await other.probe()).job?.status ?? "missing");
			await rejects(other.start("admin", false));
			await settled(job.id);
		} finally {
			await other.close();
		}
	}, 20000);
	test("compiled paths retain Windows and bundled identities", () => {
		expect(
			tokenRepairWorkerSpecifiers(
				"usage-token-repair-worker",
				true,
				"file:///$bunfs/root/server/index.js",
			)[0],
		).toBe("file:///$bunfs/root/server/services/usage-token-repair-worker.js");
		expect(
			tokenRepairWorkerEntry("file:///C:/%7EBUN/root/server/services/usage-token-repair-worker.js"),
		).toBe("C:/~BUN/root/server/services/usage-token-repair-worker.js");
	});
});
