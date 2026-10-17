import { expect, test } from "bun:test";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { join, resolve } from "node:path";
import { generateId } from "@server/lib/id";
import type { TokenRepairJob } from "@shared/usage-token-repair";
import { SQL } from "bun";
import { withPostgres } from "../../tests/db/pg-test-harness";
import { TokenRepairRunner } from "./usage-token-repair-runner";
import { createTokenRepairStore } from "./usage-token-repair-store";

async function settled(runner: TokenRepairRunner, id: string, phase: TokenRepairJob["phase"]) {
	const deadline = Date.now() + 30_000;
	while (Date.now() < deadline) {
		const job = await runner.store.getJob(id);
		if (!job || ["failed", "cancelled", "interrupted"].includes(job.status))
			throw new Error(`Maintenance unexpectedly stopped: ${job?.status}`);
		if (job.phase === phase) return job;
		await Bun.sleep(10);
	}
	throw new Error(`Maintenance did not reach ${phase}`);
}

test("PostgreSQL bulk scan is read-only, includes reviewed rows, and applies a versioned aggregate preview", async () => {
	const root = resolve(import.meta.dir, "../..");
	const directory = mkdtempSync(join(root, ".narrafork", "token-repair-pg-"));
	try {
		const result = await withPostgres(async ({ port, credentials }) => {
			const url = `postgresql://${encodeURIComponent(credentials.user)}:${encodeURIComponent(credentials.password)}@127.0.0.1:${port}/${credentials.database}`;
			const admin = new SQL(url, { max: 1, connectionTimeout: 5 });
			const store = createTokenRepairStore({ backend: "postgres", postgresUrl: url });
			const runner = new TokenRepairRunner(store, join(directory, "request-dumps"));
			try {
				const journal = JSON.parse(
					readFileSync(join(root, "drizzle-postgres/meta/_journal.json"), "utf8"),
				) as { entries: { tag: string }[] };
				for (const entry of journal.entries) {
					const migration = readFileSync(join(root, `drizzle-postgres/${entry.tag}.sql`), "utf8");
					for (const statement of migration
						.split("--> statement-breakpoint")
						.filter((part) => part.trim()))
						await admin.unsafe(statement);
				}
				for (let n = 0; n < 23; n++)
					await admin`INSERT INTO usage_token_repair_jobs (id,status,created_by,created_at,updated_at,include_reviewed) VALUES (${generateId()},'completed','retirement-test','2000-01-01','2000-01-01',true)`;
				const raw = JSON.stringify({
					request: { url: "https://test.invalid/v1/responses" },
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
				await admin.unsafe(
					"INSERT INTO api_requests (id,provider,model,created_at,input_tokens,cached_input_tokens,cache_creation_input_tokens,raw_dump_json,token_accounting_review_version,cost_usd,meter_usage) SELECT 'r-' || n,'p','known','2026-01-01',100,40,20,$1,1,7,9 FROM generate_series(1,1200) n",
					[raw],
				);
				const unresolved = JSON.stringify({
					request: { url: "https://test.invalid/custom" },
					response: {
						status: 200,
						bodyText: JSON.stringify({ usage: { input_tokens: 100, cache_read_input_tokens: 40 } }),
					},
				});
				await admin`INSERT INTO api_requests (id,provider,model,created_at,input_tokens,cached_input_tokens,raw_dump_json) VALUES ('manual','p','unknown','2026-01-01',100,40,${unresolved}),('missing','p','missing','2026-01-01',100,40,NULL)`;
				await admin`INSERT INTO api_requests (id,provider,model,created_at,input_tokens,cached_input_tokens,token_accounting_version) VALUES ('normalized','p','known','2026-01-01',60,40,1)`;
				await admin.unsafe(
					"CREATE FUNCTION forbid_scan_write() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'scan must not write api_requests'; END $$",
				);
				await admin.unsafe(
					"CREATE TRIGGER no_scan_write BEFORE UPDATE ON api_requests FOR EACH ROW EXECUTE FUNCTION forbid_scan_write()",
				);
				const job = await runner.start("admin", true);
				const scan = await settled(runner, job.id, "models");
				expect(scan.scanned).toBe(1203);
				expect(
					(
						await admin`SELECT count(*)::int AS n FROM usage_token_repair_jobs WHERE created_by='retirement-test' AND scratch_retired_at IS NOT NULL`
					)[0]?.n,
				).toBe(20);
				expect(await store.retiredJobs()).toHaveLength(3);
				expect(await runner.models(job.id)).toMatchObject({
					models: [{ provider: "p", model: "unknown", requestCount: 1 }],
				});
				await runner.dryRun(job.id, [{ provider: "p", model: "unknown", protocol: "openai" }]);
				const preview = await settled(runner, job.id, "preview");
				expect(preview.summary).toMatchObject({
					requestCount: 1203,
					repairable: 1201,
					correct: 1,
					skipped: 1,
					unresolved: 0,
					before: { inputTokens: "120260" },
					after: { inputTokens: "48220" },
					delta: { inputTokens: "-72040" },
				});
				expect(
					(
						await admin`SELECT count(*)::int AS n FROM api_requests WHERE token_accounting_review_version=1 AND token_accounting_version IS NULL`
					)[0]?.n,
				).toBe(1200);
				await expect(runner.applyPreview(job.id, "stale-preview", "admin")).rejects.toThrow();
				await admin.unsafe("DROP TRIGGER no_scan_write ON api_requests");
				await admin`UPDATE api_requests SET raw_dump_json=NULL WHERE id='r-1'`;
				const version = preview.summary?.previewVersion;
				if (!version) throw new Error("Missing preview version");
				await runner.applyPreview(job.id, version, "admin");
				const done = await settled(runner, job.id, "done");
				expect(done).toMatchObject({
					status: "completed",
					repaired: 1200,
					conflicts: 1,
					summary: { appliedDelta: { inputTokens: "-71980" } },
				});
				expect(
					(
						await admin`SELECT input_tokens,token_accounting_version FROM api_requests WHERE id='r-1'`
					)[0],
				).toEqual({ input_tokens: 100, token_accounting_version: null });
				expect(
					(
						await admin`SELECT count(*)::int AS n FROM api_requests WHERE cost_usd=7 AND meter_usage=9`
					)[0]?.n,
				).toBe(1200);
				await expect(runner.applyPreview(job.id, version, "admin")).rejects.toThrow();
				const next = await runner.start("admin", true);
				await settled(runner, next.id, "models");
				expect(
					(
						await admin`SELECT count(*)::int AS n FROM usage_token_repair_jobs WHERE created_by='retirement-test' AND scratch_retired_at IS NOT NULL`
					)[0]?.n,
				).toBe(23);
				expect(await store.retiredJobs()).toEqual([]);
				await runner.cancel(next.id);
				return { verified: true };
			} finally {
				await runner.close();
				await admin.close();
			}
		});
		expect(result).toEqual({ verified: true });
	} finally {
		rmSync(directory, { recursive: true, force: true });
	}
}, 120_000);
