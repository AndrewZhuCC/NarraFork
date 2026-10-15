import type { Database } from "bun:sqlite";
import { afterEach, beforeEach, describe, expect, test } from "bun:test";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { getTestDb } from "../../tests/setup";
import { openSchemaCopyAt } from "../../tests/sqlite-schema-image";
import { TokenRepairClient } from "./usage-token-repair-service";
import {
	tokenRepairWorkerEntry,
	tokenRepairWorkerSpecifiers,
} from "./usage-token-repair-worker-paths";

const source = getTestDb().sqlite;
let directory: string;
let path: string;
let db: Database;
let client: TokenRepairClient;
beforeEach(() => {
	directory = mkdtempSync(join(tmpdir(), "token-repair-worker-"));
	path = join(directory, "test.sqlite");
	db = openSchemaCopyAt(source, path);
	client = new TokenRepairClient({ backend: "sqlite", sqlitePath: path }, directory);
});
afterEach(async () => {
	await client.close();
	db.close();
	rmSync(directory, { recursive: true, force: true });
});
function request() {
	const dump = JSON.stringify({
		request: { url: "https://example.invalid/v1/responses" },
		response: {
			status: 200,
			bodyText: JSON.stringify({
				object: "response",
				status: "completed",
				usage: {
					input_tokens: 100,
					output_tokens: 10,
					input_tokens_details: { cached_tokens: 40, cache_write_tokens: 20 },
				},
			}),
		},
	});
	db.query(
		"INSERT INTO api_requests (id,provider,model,created_at,input_tokens,cached_input_tokens,cache_creation_input_tokens,raw_dump_json,cost_usd,meter_usage) VALUES ('request','nug','test','2026-01-01',100,40,20,?,7,9)",
	).run(dump);
}
async function ready(id: string) {
	const deadline = Date.now() + 10_000;
	while (Date.now() < deadline) {
		const job = await client.getJob(id);
		if (job?.status !== "analyzing") {
			expect(job?.status).toBe("ready");
			return job;
		}
		await Bun.sleep(5);
	}
	throw new Error("Analysis did not reach ready before the test deadline");
}

describe("historical repair worker integration", () => {
	test("probe never starts analysis; manual preview and apply preserve costs and metering", async () => {
		request();
		expect(await client.probe()).toMatchObject({ hasPending: true, job: null });
		expect(db.query("SELECT input_tokens FROM api_requests").get()).toEqual({ input_tokens: 100 });
		const job = await client.start("admin", false);
		const completedJob = await ready(job.id);
		expect(completedJob?.scanned).toBe(1);
		const page = await client.listCandidates(job.id);
		expect(page.records).toHaveLength(1);
		const candidate = page.records[0];
		if (!candidate) throw new Error("Expected an analyzed candidate");
		expect(candidate).toMatchObject({
			classification: "repairable",
			protocol: "openai",
			before: { inputTokens: 100 },
			after: { inputTokens: 40 },
		});
		expect(await client.apply(job.id, [candidate.id], "admin")).toEqual({
			applied: 1,
			conflicts: 0,
		});
		expect(
			db
				.query(
					"SELECT input_tokens, cached_input_tokens, cache_creation_input_tokens, cost_usd, meter_usage, token_accounting_version FROM api_requests",
				)
				.get(),
		).toEqual({
			input_tokens: 40,
			cached_input_tokens: 40,
			cache_creation_input_tokens: 20,
			cost_usd: 7,
			meter_usage: 9,
			token_accounting_version: 1,
		});
		expect(await client.apply(job.id, [candidate.id], "admin")).toMatchObject({
			applied: 0,
			conflicts: 1,
		});
		expect((await client.listCandidates(job.id)).records[0]).toMatchObject({
			appliedBy: "admin",
			totalsStatus: "pending",
		});
	}, 15_000);

	test("restart does not take over old jobs or replay writes; analysis is explicitly restarted", async () => {
		request();
		const job = await client.start("admin", false);
		await ready(job.id);
		await client.close();
		db.query("UPDATE usage_token_repair_jobs SET status='applying' WHERE id=?").run(job.id);
		client = new TokenRepairClient({ backend: "sqlite", sqlitePath: path }, directory);
		expect((await client.probe()).job?.status).toBe("applying");
		expect(db.query("SELECT input_tokens FROM api_requests").get()).toEqual({ input_tokens: 100 });
		await client.cancel(job.id);
		const restarted = await client.start("admin", true, [], job.id);
		expect(restarted.id).not.toBe(job.id);
		await ready(restarted.id);
		expect(db.query("SELECT input_tokens FROM api_requests").get()).toEqual({ input_tokens: 100 });
	}, 15_000);

	test("stale previews become persistent conflicts rather than overwriting changed records", async () => {
		request();
		const job = await client.start("admin", false);
		await ready(job.id);
		const candidate = (await client.listCandidates(job.id)).records[0];
		if (!candidate) throw new Error("Expected an analyzed candidate");
		db.query("UPDATE api_requests SET input_tokens=111 WHERE id='request'").run();
		expect(await client.apply(job.id, [candidate.id], "admin")).toMatchObject({
			applied: 0,
			conflicts: 1,
		});
		expect((await client.listCandidates(job.id)).records[0]?.classification).toBe("conflict");
		expect((await client.getJob(job.id))?.conflicts).toBe(1);
		expect(db.query("SELECT input_tokens FROM api_requests").get()).toEqual({ input_tokens: 111 });
	}, 15_000);

	test("ready work can be ended before manually starting another bounded analysis", async () => {
		request();
		const job = await client.start("admin", false);
		await ready(job.id);
		expect((await client.cancel(job.id))?.status).toBe("cancelled");
		const next = await client.start("admin", false);
		expect(next.id).not.toBe(job.id);
		await ready(next.id);
	}, 15_000);

	test("cancel stops at a bounded record boundary and a new analysis never applies tokens", async () => {
		request();
		for (let i = 0; i < 10; i++)
			db.query(
				"INSERT INTO api_requests (id,provider,model,created_at,input_tokens,cached_input_tokens,cache_creation_input_tokens,raw_dump_json) SELECT ?,provider,model,created_at,input_tokens,cached_input_tokens,cache_creation_input_tokens,raw_dump_json FROM api_requests WHERE id='request'",
			).run(`copy-${i}`);
		const job = await client.start("admin", false);
		expect((await client.cancel(job.id))?.status).toBe("cancelled");
		expect(
			db.query("SELECT count(*) AS n FROM api_requests WHERE input_tokens != 100").get(),
		).toEqual({ n: 0 });
		const cancelled = await client.getJob(job.id);
		const restarted = await client.start("admin", false);
		expect(restarted.id).not.toBe(job.id);
		expect(((await ready(restarted.id))?.scanned ?? 0) + (cancelled?.scanned ?? 0)).toBe(11);
		expect(
			db.query("SELECT count(*) AS n FROM api_requests WHERE input_tokens != 100").get(),
		).toEqual({ n: 0 });
	}, 15_000);

	test("a second worker probe cannot interrupt a live owner's analysis", async () => {
		request();
		for (let i = 0; i < 5; i++)
			db.query(
				"INSERT INTO api_requests (id,provider,model,created_at,input_tokens,cached_input_tokens,cache_creation_input_tokens,raw_dump_json) SELECT ?,provider,model,created_at,input_tokens,cached_input_tokens,cache_creation_input_tokens,raw_dump_json FROM api_requests WHERE id='request'",
			).run(`copy-${i}`);
		const other = new TokenRepairClient({ backend: "sqlite", sqlitePath: path }, directory);
		try {
			const job = await client.start("admin", false);
			expect(["analyzing", "ready"]).toContain((await other.probe()).job?.status ?? "missing");
			expect((await ready(job.id))?.scanned).toBe(6);
		} finally {
			await other.close();
		}
	}, 15_000);

	test("evidence is rechecked after preview and missing evidence records a conflict", async () => {
		request();
		const job = await client.start("admin", false);
		await ready(job.id);
		const candidate = (await client.listCandidates(job.id)).records[0];
		if (!candidate) throw new Error("Expected a preview candidate");
		db.query("UPDATE api_requests SET raw_dump_json=NULL WHERE id='request'").run();
		expect(await client.apply(job.id, [candidate.id], "admin")).toMatchObject({
			applied: 0,
			conflicts: 1,
		});
		expect((await client.listCandidates(job.id)).records[0]?.classification).toBe("conflict");
		expect(db.query("SELECT input_tokens FROM api_requests").get()).toEqual({ input_tokens: 100 });
	}, 15_000);

	test("manual model protocol choices survive restart and are reused for apply without crossing providers", async () => {
		request();
		const dump = JSON.stringify({
			request: { url: "https://example.invalid/custom-gateway" },
			response: {
				status: 200,
				bodyText: JSON.stringify({
					usage: {
						input_tokens: 100,
						input_tokens_details: { cached_tokens: 40, cache_write_tokens: 20 },
					},
				}),
			},
		});
		db.query("UPDATE api_requests SET raw_dump_json=? WHERE id='request'").run(dump);
		db.query(
			"INSERT INTO api_requests (id,provider,model,created_at,input_tokens,cached_input_tokens,cache_creation_input_tokens,raw_dump_json) SELECT 'other-request','other',model,created_at,input_tokens,cached_input_tokens,cache_creation_input_tokens,raw_dump_json FROM api_requests WHERE id='request'",
		).run();
		db.query(
			"INSERT INTO api_requests (id,provider,model,created_at,input_tokens,cached_input_tokens,cache_creation_input_tokens) SELECT 'missing-dump',provider,model,created_at,input_tokens,cached_input_tokens,cache_creation_input_tokens FROM api_requests WHERE id='request'",
		).run();
		const automatic = await client.start("admin", false);
		await ready(automatic.id);
		expect(
			(await client.listCandidates(automatic.id)).records.every(
				(candidate) => candidate.classification !== "repairable",
			),
		).toBe(true);
		await client.cancel(automatic.id);
		const choices = [{ provider: "nug", model: "test", protocol: "openai" as const }];
		db.query(
			"INSERT INTO api_requests (id,provider,model,created_at,input_tokens,cached_input_tokens,cache_creation_input_tokens,raw_dump_json) SELECT 'outside-source',provider,model,created_at,input_tokens,cached_input_tokens,cache_creation_input_tokens,raw_dump_json FROM api_requests WHERE id='request'",
		).run();
		const manual = await client.start("admin", true, choices, automatic.id);
		await ready(manual.id);
		const page = await client.listCandidates(manual.id);
		expect(page.records).toHaveLength(3);
		expect(page.records.some((candidate) => candidate.requestId === "outside-source")).toBe(false);
		const selected = page.records.find((candidate) => candidate.requestId === "request");
		if (!selected) throw new Error("Expected the manually analyzed request");
		expect(selected).toMatchObject({
			classification: "repairable",
			manualProtocol: "openai",
			after: { inputTokens: 40 },
		});
		expect(
			page.records.find((candidate) => candidate.requestId === "other-request")?.manualProtocol,
		).toBeUndefined();
		expect(
			page.records.find((candidate) => candidate.requestId === "other-request")?.classification,
		).not.toBe("repairable");
		expect(page.records.find((candidate) => candidate.requestId === "missing-dump")).toMatchObject({
			classification: "insufficient",
			manualProtocol: "openai",
			after: null,
		});
		await client.close();
		client = new TokenRepairClient({ backend: "sqlite", sqlitePath: path }, directory);
		expect((await client.getJob(manual.id))?.protocolOverrides).toEqual(choices);
		expect((await client.getJob(manual.id))?.sourceJobId).toBe(automatic.id);
		expect(await client.apply(manual.id, [selected.id], "admin")).toMatchObject({
			applied: 1,
			conflicts: 0,
		});
		expect(db.query("SELECT input_tokens FROM api_requests WHERE id='request'").get()).toEqual({
			input_tokens: 40,
		});
		expect(
			db.query("SELECT input_tokens FROM api_requests WHERE id='other-request'").get(),
		).toEqual({ input_tokens: 100 });
		let rejectedOldPreview = false;
		try {
			await client.apply(automatic.id, [selected.id], "admin");
		} catch {
			rejectedOldPreview = true;
		}
		expect(rejectedOldPreview).toBe(true);
	}, 15_000);

	test("compiled worker paths and Windows virtual paths preserve bundled entry identities", () => {
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
