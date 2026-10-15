import { expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import type { TokenRepairEvidence } from "@shared/usage-token-repair";
import { SQL } from "bun";
import { withPostgres } from "../../tests/db/pg-test-harness";
import { createTokenRepairStore } from "./usage-token-repair-store";

// Real throwaway PostgreSQL only; never imports application database/settings.
test("PostgreSQL detail-only repair preserves unrelated accounting and serializes state changes", async () => {
	const result = await withPostgres(async ({ port, credentials }) => {
		const url = `postgresql://${encodeURIComponent(credentials.user)}:${encodeURIComponent(credentials.password)}@127.0.0.1:${port}/${credentials.database}`;
		const admin = new SQL(url, { max: 1, connectionTimeout: 5 });
		const root = resolve(import.meta.dir, "../..");
		const journal = JSON.parse(
			readFileSync(resolve(root, "drizzle-postgres/meta/_journal.json"), "utf8"),
		) as { entries: { tag: string }[] };
		const store = createTokenRepairStore({ backend: "postgres", postgresUrl: url });
		const other = createTokenRepairStore({ backend: "postgres", postgresUrl: url });
		try {
			for (const entry of journal.entries) {
				const sql = readFileSync(resolve(root, `drizzle-postgres/${entry.tag}.sql`), "utf8");
				for (const statement of sql.split("--> statement-breakpoint").filter((part) => part.trim()))
					await admin.unsafe(statement);
			}
			await admin`INSERT INTO narrators (id, created_at, updated_at) VALUES ('n', '2026-01-01', '2026-01-01')`;
			await admin`INSERT INTO narrator_messages (id, narrator_id, role, content_json, created_at, tokens_in, cached_input_tokens, cache_creation_input_tokens, cache_creation_5m_tokens, cache_creation_1h_tokens) VALUES ('m', 'n', 'assistant', '[]', '2026-01-01', 100, 30, 20, 10, 10)`;
			await admin`INSERT INTO narrator_message_refs (id, narrator_id, message_id, seq) VALUES ('ref', 'n', 'm', 1)`;
			await admin`INSERT INTO api_requests (id, user_id, credential_id, message_id, provider, model, created_at, input_tokens, cached_input_tokens, cache_creation_input_tokens, cache_creation_5m_tokens, cache_creation_1h_tokens, raw_dump_json, cost_usd, meter_usage) VALUES ('r', 'u', 'key', 'm', 'openai', 'test', '2026-01-01', 100, 30, 20, 10, 10, '中中', 9.5, 99)`;
			await admin`INSERT INTO user_usage_totals (user_id, input_tokens, cached_input_tokens, cache_creation_tokens, request_count, cost_usd, first_used_at, last_used_at) VALUES ('u', 1000, 300, 200, 10, 42, '2026-01-01', '2026-01-01')`;
			await admin`INSERT INTO credential_usage_totals (id, provider, credential_id, model, input_tokens, cached_input_tokens, cache_creation_tokens, request_count, cost_usd, first_seen_at, last_seen_at) VALUES ('c', 'openai', 'key', 'test', 1000, 300, 200, 10, 42, '2026-01-01', '2026-01-01')`;
			const protectedRows = new Map<string, unknown[]>();
			await admin.unsafe(
				"CREATE FUNCTION forbid_repair_side_effect() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'forbidden side effect'; END $$",
			);
			for (const table of ["user_usage_totals", "credential_usage_totals", "narrator_messages"]) {
				protectedRows.set(table, Array.from(await admin.unsafe(`SELECT * FROM ${table}`)));
				await admin.unsafe(
					`CREATE TRIGGER no_repair_write BEFORE INSERT OR UPDATE OR DELETE ON ${table} FOR EACH ROW EXECUTE FUNCTION forbid_repair_side_effect()`,
				);
			}
			expect((await store.probe()).hasPending).toBe(true);
			expect(await store.getRawDump("r", 5)).toBeNull();
			expect(await store.getRawDump("r", 6)).toBe("中中");
			const job = await store.createJob("admin", false);
			const row = (await store.scanPage(job.id))[0];
			if (!row) throw new Error("Missing PG fixture");
			const before = {
				inputTokens: 100,
				cachedInputTokens: 30,
				cacheCreationInputTokens: 20,
				cacheCreation5mTokens: 10,
				cacheCreation1hTokens: 10,
			};
			const evidence: TokenRepairEvidence = {
				classification: "repairable",
				protocol: "openai",
				reason: "numeric fixture",
				evidence: "100 - 30 - 20 = 50",
				before,
				after: { ...before, inputTokens: 50 },
			};
			await store.saveCandidate(job.id, row, evidence);
			await store.saveCandidate(job.id, row, evidence);
			expect((await store.getJob(job.id))?.scanned).toBe(1);
			await store.finishAnalysis(job.id);
			const candidate = (await store.listCandidates(job.id)).records[0];
			if (!candidate) throw new Error("Missing PG candidate");
			await expect(store.applyCandidate(job.id, candidate.id, "admin")).rejects.toThrow(
				"not applying",
			);
			const starts = await Promise.allSettled([
				store.setJobStatus(job.id, "applying"),
				other.setJobStatus(job.id, "applying"),
			]);
			expect(starts.filter((item) => item.status === "fulfilled")).toHaveLength(1);
			expect(
				await Promise.all([
					store.applyCandidate(job.id, candidate.id, "admin"),
					other.applyCandidate(job.id, candidate.id, "other"),
				]),
			).toEqual(["applied", "applied"]);
			expect(await store.getRequest("r")).toMatchObject({
				inputTokens: 50,
				tokenAccountingVersion: 1,
			});
			expect(await store.getCandidate(job.id, candidate.id)).toMatchObject({
				totalsStatus: "pending",
			});
			expect((await store.getJob(job.id))?.repaired).toBe(1);
			for (const [table, previous] of protectedRows)
				expect(Array.from(await admin.unsafe(`SELECT * FROM ${table}`))).toEqual(previous);
			const [unchanged] =
				await admin`SELECT cost_usd, meter_usage, output_tokens, reasoning_tokens FROM api_requests WHERE id = 'r'`;
			expect(unchanged).toEqual({
				cost_usd: 9.5,
				meter_usage: 99,
				output_tokens: 0,
				reasoning_tokens: 0,
			});
			await store.setJobStatus(job.id, "ready");
			await expect(store.setJobStatus(job.id, "failed")).rejects.toThrow("transition");
			await other.cancelJob(job.id);
			await expect(store.setJobStatus(job.id, "analyzing")).rejects.toThrow("transition");

			await admin`INSERT INTO api_requests (id, provider, model, created_at, input_tokens, cached_input_tokens, cache_creation_input_tokens, cache_creation_5m_tokens, cache_creation_1h_tokens) VALUES ('manual', 'p', 'test', '2030-01-01', 100, 30, 20, 10, 10)`;
			const manual = await store.createJob("admin", false, [
				{ provider: "p", model: "test", protocol: "anthropic" },
			]);
			const manualRow = (await store.scanPage(manual.id))[0];
			if (!manualRow) throw new Error("Missing PG manual fixture");
			await expect(store.saveCandidate(manual.id, manualRow, evidence)).rejects.toThrow(
				"manual protocol",
			);
			const manualEvidence = { ...evidence, manualProtocol: "anthropic" as const };
			await store.saveCandidate(manual.id, manualRow, manualEvidence);
			await store.finishAnalysis(manual.id);
			await other.setJobStatus(manual.id, "applying");
			const manualCandidate = (await other.listCandidates(manual.id)).records[0];
			if (!manualCandidate) throw new Error("Missing PG manual audit");
			await admin`UPDATE usage_token_repair_candidates SET evidence_json = ${JSON.stringify({ ...manualEvidence, manualProtocol: "gemini" })} WHERE id = ${manualCandidate.id}`;
			expect(await other.applyCandidate(manual.id, manualCandidate.id, "admin")).toBe("conflict");
			await store.cancelJob(manual.id);
			await expect(other.applyCandidate(manual.id, manualCandidate.id, "admin")).rejects.toThrow(
				"not applying",
			);
			await expect(other.setJobStatus(manual.id, "ready")).rejects.toThrow("transition");
			await expect(other.setJobStatus(manual.id, "failed")).rejects.toThrow("transition");

			await admin`INSERT INTO api_requests (id, provider, model, created_at, input_tokens, cached_input_tokens, token_accounting_review_version) SELECT 'prior-' || n, 'p', 'test', '2010-01-01', 100, 30, 1 FROM generate_series(1, 10001) n`;
			await admin`INSERT INTO api_requests (id, provider, model, created_at, input_tokens, cached_input_tokens, token_accounting_review_version) VALUES ('outside-scope', 'p', 'test', '2029-01-01', 100, 30, 1)`;
			await expect(store.createJob("admin", true, [], "missing-source")).rejects.toThrow(
				"Source job",
			);
			await expect(store.createJob("admin", false, [], manual.id)).rejects.toThrow(
				"includeReviewed",
			);
			const scoped = await store.createJob("admin", true, manual.protocolOverrides, manual.id);
			expect(scoped.sourceJobId).toBe(manual.id);
			expect(scoped.protocolOverrides).toEqual(manual.protocolOverrides);
			const scopedRows = await store.scanPage(scoped.id);
			expect(scopedRows.map((item) => item.id)).toEqual(["manual"]);
			const scopedRow = scopedRows[0];
			if (!scopedRow) throw new Error("Missing scoped PG fixture");
			const outsider = await store.getRequest("outside-scope");
			if (!outsider) throw new Error("Missing outside-scope PG fixture");
			await expect(store.saveCandidate(scoped.id, outsider, manualEvidence)).rejects.toThrow(
				"source scope",
			);
			await store.saveCandidate(scoped.id, scopedRow, manualEvidence);
			expect(await store.scanPage(scoped.id)).toEqual([]);
			await store.finishAnalysis(scoped.id);
			await store.setJobStatus(scoped.id, "applying");
			const scopedCandidate = (await store.listCandidates(scoped.id)).records[0];
			if (!scopedCandidate) throw new Error("Missing scoped PG audit");
			await admin`DELETE FROM api_requests WHERE id = 'manual'`;
			expect(await store.applyCandidate(scoped.id, scopedCandidate.id, "admin")).toBe("conflict");
			expect((await store.getJob(scoped.id))?.conflicts).toBe(1);
			await store.setJobStatus(scoped.id, "ready");
			await store.close();
			expect((await other.getJob(scoped.id))?.status).toBe("ready");
			await other.cancelJob(scoped.id);
			return "passed";
		} finally {
			await store.close();
			await other.close();
			await admin.close({ timeout: 1 });
		}
	});
	expect(result).toBe("passed");
}, 120_000);
