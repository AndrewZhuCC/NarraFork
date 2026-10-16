import { Database } from "bun:sqlite";
import { mkdirSync, mkdtempSync, readdirSync, rmSync, statSync } from "node:fs";
import { join, resolve } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { SQL } from "drizzle-orm";
import { getTableConfig, SQLiteSyncDialect } from "drizzle-orm/sqlite-core";
import { apiRequests, usageTokenRepairCandidates, usageTokenRepairJobs } from "../server/db/schema";
import { TokenRepairClient } from "../server/services/usage-token-repair-service";
import type { TokenRepairJob } from "../shared/usage-token-repair";

// Standalone CLI; schema declarations only, never import server/db or tests/setup.
// bun scripts/benchmark-token-repair-bulk.ts --rows=10000
// Defaults exercise both acceptance sizes. --inline uses exclusively small inline JSON.
// --seed-only validates isolated fixture generation without starting maintenance workers.
const args = process.argv.slice(2);
const value = (key: string) => args.find((arg) => arg.startsWith(`--${key}=`))?.split("=")[1];
const sizes = (value("rows") ?? "100000,300000").split(",").map(Number);
if (sizes.some((n) => !Number.isSafeInteger(n) || n < 1 || n > 1_000_000))
	throw new Error("--rows must contain integers between 1 and 1000000");
const timeoutMs = Number(value("timeout-ms") ?? 1_800_000);
if (!Number.isSafeInteger(timeoutMs) || timeoutMs < 1000 || timeoutMs > 7_200_000)
	throw new Error("Invalid --timeout-ms (1000..7200000)");
const inline = args.includes("--inline");
const seedOnly = args.includes("--seed-only");
const keep = args.includes("--keep");
const root = resolve(import.meta.dir, "../.narrafork");
mkdirSync(root, { recursive: true, mode: 0o700 });
const folder = mkdtempSync(join(root, "benchmark-token-repair-"));
const watchdog = setTimeout(
	() => {
		console.error(JSON.stringify({ error: "Benchmark hard timeout", folder }));
		process.exit(124);
	},
	timeoutMs * sizes.length + 60_000,
);
const quote = (name: string) => `"${name.replaceAll('"', '""')}"`;
function schema(db: Database) {
	const dialect = new SQLiteSyncDialect();
	for (const table of [apiRequests, usageTokenRepairJobs, usageTokenRepairCandidates]) {
		const config = getTableConfig(table);
		const columns = config.columns.map((column) => {
			let field = `${quote(column.name)} ${column.getSQLType()}`;
			if (column.primary) field += " PRIMARY KEY";
			if (column.notNull) field += " NOT NULL";
			if (column.default !== undefined) {
				const v = column.default;
				field += ` DEFAULT ${v instanceof SQL ? dialect.sqlToQuery(v).sql : typeof v === "string" ? `'${v.replaceAll("'", "''")}'` : Number(v)}`;
			}
			return field;
		});
		db.exec(`CREATE TABLE ${quote(config.name)} (${columns.join(",")})`);
	}
	db.exec(`
		CREATE INDEX idx_api_requests_created ON api_requests(created_at,id);
		CREATE INDEX idx_token_repair_jobs_created ON usage_token_repair_jobs(created_at,id);
		CREATE UNIQUE INDEX idx_token_repair_single_active ON usage_token_repair_jobs((1)) WHERE status IN ('analyzing','ready','applying');
		CREATE UNIQUE INDEX idx_token_repair_candidate_request ON usage_token_repair_candidates(job_id,request_id);
		CREATE INDEX idx_token_repair_candidates_page ON usage_token_repair_candidates(job_id,id);
		CREATE TABLE benchmark_source_updates(n INTEGER NOT NULL);
		INSERT INTO benchmark_source_updates VALUES(0);
		CREATE TRIGGER benchmark_detect_source_update AFTER UPDATE ON api_requests BEGIN UPDATE benchmark_source_updates SET n=n+1; END;
		CREATE TABLE benchmark_foreground(id INTEGER PRIMARY KEY, value INTEGER NOT NULL);
	`);
}
const usage = { input_tokens: 100, output_tokens: 10, input_tokens_details: { cached_tokens: 40 } };
const response = { object: "response", status: "completed", usage };
const jsonDump = {
	request: { url: "https://benchmark.invalid/v1/responses" },
	response: { status: 200, bodyText: JSON.stringify(response) },
};
const sseDump = {
	request: jsonDump.request,
	response: {
		status: 200,
		bodyText: `data: ${JSON.stringify({ type: "response.completed", response })}\n\n`,
	},
};
function directoryBytes(path: string): number {
	let total = 0;
	try {
		for (const entry of readdirSync(path, { withFileTypes: true })) {
			const child = join(path, entry.name);
			total += entry.isDirectory() ? directoryBytes(child) : statSync(child).size;
		}
	} catch (error) {
		if ((error as NodeJS.ErrnoException).code !== "ENOENT") throw error;
	}
	return total;
}
function percentile(samples: number[], fraction: number) {
	if (!samples.length) return null;
	const sorted = [...samples].sort((a, b) => a - b);
	return sorted[Math.min(sorted.length - 1, Math.ceil(sorted.length * fraction) - 1)];
}
const stats = (samples: number[]) => ({
	samples: samples.length,
	p95Ms: percentile(samples, 0.95),
	p99Ms: percentile(samples, 0.99),
	maxMs: samples.length ? samples.reduce((max, sample) => Math.max(max, sample), 0) : null,
});
function expectEqual(actual: unknown, expected: unknown, label: string) {
	if (actual !== expected) throw new Error(`${label}: actual=${actual} expected=${expected}`);
}
async function benchmark(rows: number) {
	const directory = join(folder, `${rows}-${inline ? "inline" : "mixed"}`);
	const dumps = join(directory, "request-dumps");
	mkdirSync(dumps, { recursive: true, mode: 0o700 });
	const dbPath = join(directory, "source.sqlite");
	const db = new Database(dbPath, { create: true, strict: true });
	db.exec("PRAGMA journal_mode=WAL; PRAGMA synchronous=NORMAL; PRAGMA busy_timeout=0");
	schema(db);
	const insert = db.query(
		"INSERT INTO api_requests(id,user_id,provider,model,created_at,input_tokens,cached_input_tokens,raw_dump_json) VALUES(?, 'admin', 'benchmark', 'model', '2026-01-01T00:00:00.000Z',100,40,?)",
	);
	const distribution = { json: 0, missing: 0, sse: 0, spill: 0 };
	// Seed bounded batches, with unique ownership-verified spill files.
	for (let start = 0; start < rows; start += 1000) {
		const batch: [string, string | null][] = [];
		for (let i = start; i < Math.min(start + 1000, rows); i++) {
			const id = `request-${i.toString().padStart(9, "0")}`;
			const kind = inline ? 0 : i % 10;
			let raw: string | null;
			if (kind === 0 && !inline) {
				distribution.missing++;
				raw = null;
			} else if (kind === 1 && !inline) {
				distribution.spill++;
				const path = join(dumps, `${id}.json`);
				const data = JSON.stringify({
					schema: "narrafork.api-request-dump.v1",
					requestId: id,
					dump: jsonDump,
				});
				await Bun.write(path, data, { mode: 0o600 });
				raw = JSON.stringify({
					spill: {
						schema: "narrafork.api-request-dump-spill.v1",
						filePath: path,
						bytes: Buffer.byteLength(data),
						inlineTruncated: true,
						writtenForRequestId: id,
					},
				});
			} else if (kind === 2 && !inline) {
				distribution.sse++;
				raw = JSON.stringify(sseDump);
			} else {
				distribution.json++;
				raw = JSON.stringify(jsonDump);
			}
			batch.push([id, raw]);
		}
		db.transaction(() => {
			for (const [id, raw] of batch) insert.run(id, raw);
		})();
	}
	const updates = () =>
		(db.query("SELECT n FROM benchmark_source_updates").get() as { n: number }).n;
	expectEqual(
		(db.query("SELECT COUNT(*) AS n FROM api_requests").get() as { n: number }).n,
		rows,
		"seed count",
	);
	if (seedOnly) {
		db.close();
		return { rows, distribution, seedValidated: true, sourceUpdates: 0 };
	}
	const client = new TokenRepairClient({ backend: "sqlite", sqlitePath: dbPath }, dumps);
	const server = Bun.serve({ hostname: "127.0.0.1", port: 0, fetch: () => new Response("ok") });
	let phase = "scan",
		active = true,
		peakRss = process.memoryUsage().rss;
	const measurements: Record<
		string,
		{ crud: number[]; heartbeat: number[]; busy: number; httpErrors: number }
	> = {};
	const measure = () =>
		(measurements[phase] ??= { crud: [], heartbeat: [], busy: 0, httpErrors: 0 });
	const sideInsert = db.query("INSERT INTO benchmark_foreground VALUES(1,0)");
	const sideUpdate = db.query("UPDATE benchmark_foreground SET value=value+1 WHERE id=1");
	const sideSelect = db.query("SELECT value FROM benchmark_foreground WHERE id=1");
	const sideDelete = db.query("DELETE FROM benchmark_foreground WHERE id=1");
	const foreground = (async () => {
		while (active) {
			const m = measure();
			let started = performance.now();
			try {
				db.transaction(() => {
					sideInsert.run();
					sideUpdate.run();
					sideSelect.get();
					sideDelete.run();
				})();
			} catch (error) {
				if (/busy|locked/i.test(String(error))) m.busy++;
				else throw error;
			}
			m.crud.push(performance.now() - started);
			started = performance.now();
			try {
				const response = await fetch(`http://127.0.0.1:${server.port}`, {
					signal: AbortSignal.timeout(2000),
				});
				await response.text();
				if (!response.ok) m.httpErrors++;
			} catch {
				m.httpErrors++;
			}
			m.heartbeat.push(performance.now() - started);
			peakRss = Math.max(peakRss, process.memoryUsage().rss);
			await delay(20);
		}
	})();
	const deadline = Date.now() + timeoutMs;
	async function wait(id: string, target: TokenRepairJob["phase"]): Promise<TokenRepairJob> {
		while (Date.now() < deadline) {
			const job = await client.getJob(id);
			if (!job || job.status === "failed" || job.status === "cancelled")
				throw new Error(JSON.stringify(job));
			if (job.phase === target) return job;
			await delay(100);
		}
		const last = await client.getJob(id);
		throw new Error(`Timeout waiting for ${target}: ${JSON.stringify(last)}`);
	}
	try {
		let started = performance.now();
		let job = await client.start("admin", true);
		job = await wait(job.id, "models");
		const scanMs = performance.now() - started;
		expectEqual(job.scanned, rows, "scan row count");
		expectEqual(updates(), 0, "scan source writes");
		const scanScratchBytes = directoryBytes(join(directory, "token-repair-jobs"));
		console.log(
			JSON.stringify({
				rows,
				phase: "scan",
				scanMs,
				rowsPerSecond: rows / (scanMs / 1000),
				sourceUpdates: updates(),
				scanScratchBytes,
			}),
		);
		phase = "dryRun";
		started = performance.now();
		await client.dryRun(job.id, []);
		job = await wait(job.id, "preview");
		const dryRunMs = performance.now() - started;
		expectEqual(updates(), 0, "dry-run source writes");
		const summary = job.summary;
		if (!summary?.previewVersion) throw new Error("Missing preview");
		const repaired = rows - distribution.missing;
		expectEqual(summary.requestCount, rows, "summary request count");
		expectEqual(summary.repairable, repaired, "summary repairable");
		expectEqual(summary.before.inputTokens, String(rows * 100), "summary before input");
		expectEqual(
			summary.after.inputTokens,
			String(rows * 100 - repaired * 40),
			"summary after input",
		);
		const scratchBytes = directoryBytes(join(directory, "token-repair-jobs"));
		console.log(
			JSON.stringify({
				rows,
				phase: "dryRun",
				dryRunMs,
				rowsPerSecond: rows / (dryRunMs / 1000),
				sourceUpdates: updates(),
				scratchBytes,
				previewValidated: true,
			}),
		);
		phase = "apply";
		started = performance.now();
		await client.applyPreview(job.id, summary.previewVersion, "admin");
		job = await wait(job.id, "done");
		const applyMs = performance.now() - started;
		expectEqual(job.status, "completed", "final status");
		expectEqual(job.repaired, repaired, "apply count");
		expectEqual(updates(), repaired, "apply source update count");
		return {
			rows,
			distribution,
			scanMs,
			scanRowsPerSecond: rows / (scanMs / 1000),
			dryRunMs,
			dryRunRowsPerSecond: rows / (dryRunMs / 1000),
			applyMs,
			peakRssBytes: peakRss,
			scanScratchBytes,
			scratchBytes,
			sourceUpdatesDuringScan: 0,
			sourceUpdatesDuringDryRun: 0,
			sourceUpdatesAfterApply: updates(),
			repaired: job.repaired,
			conflicts: job.conflicts,
			foreground: Object.fromEntries(
				Object.entries(measurements).map(([key, m]) => [
					key,
					{
						crud: stats(m.crud),
						heartbeat: stats(m.heartbeat),
						busy: m.busy,
						httpErrors: m.httpErrors,
					},
				]),
			),
			applyWriteTransactions: job.summary?.writeTransactions ?? null,
			applyLockHoldP99Below50Ms: job.summary?.writeTransactions
				? job.summary.writeTransactions.p99Ms < 50
				: null,
			applyLockHoldMaxBelow50Ms: job.summary?.writeTransactions
				? job.summary.writeTransactions.maxMs < 50
				: null,
			applyLockHoldNote:
				"SQLite BEGIN success through COMMIT/ROLLBACK; p99 is a fixed 1ms histogram upper bound; foreground CRUD latency is separate",
			inlineThroughputTargetMet: inline ? rows / (scanMs / 1000) >= 1000 : null,
		};
	} finally {
		active = false;
		await foreground;
		await client.close();
		await server.stop(true);
		db.close();
	}
}
try {
	console.log(
		JSON.stringify({
			benchmark: "token-repair-bulk",
			folder,
			inline,
			timeoutMs,
			rssScope: "whole process including worker threads",
			foregroundIntervalMs: 20,
			fixtureSchema:
				"current schema columns/defaults; minimal maintenance indexes, no unrelated tables/FKs",
			sqliteBusyTimeoutMs: 0,
		}),
	);
	for (const size of sizes) console.log(JSON.stringify(await benchmark(size)));
} finally {
	clearTimeout(watchdog);
	if (!keep) rmSync(folder, { recursive: true, force: true });
}
