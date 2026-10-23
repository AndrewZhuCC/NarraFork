import { Database } from "bun:sqlite";
import { afterAll, afterEach, beforeEach, describe, expect, mock, spyOn, test } from "bun:test";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { supported } from "@server/db/backend";
import * as connection from "@server/db/connection";
import * as pool from "@server/lib/db-worker/pool";
import {
	assertSessionPreviewBudget,
	SESSION_PREVIEW_MAX_ID_ENTRIES,
	SESSION_PREVIEW_MAX_ID_LENGTH,
	SESSION_PREVIEW_MAX_RELATION_FIELD_LENGTH,
	SESSION_PREVIEW_MAX_RELATIONS,
	type StorageScanSessionPreviewParams,
} from "@server/lib/db-worker/protocol";
import * as scanRunner from "@server/lib/db-worker/storage-scan-runner";
import { narratorService } from "@server/services/narrator-service";

// A dedicated file is visible to real read workers; neither main-thread nor worker queries use
// the user's database. Keep raw_dump_json in this legacy fixture to pin honest byte accounting.
const home = mkdtempSync(join(tmpdir(), "narrafork-cleanup-workers-"));
const dbPath = join(home, "fixture.db");
const sqlite = new Database(dbPath);
sqlite.run("PRAGMA journal_mode = WAL");
sqlite.exec(`
	CREATE TABLE narrators (
		id TEXT PRIMARY KEY, parent_narrator_id TEXT, type TEXT DEFAULT 'primary',
		variant TEXT DEFAULT 'primary', traits TEXT, title TEXT, status TEXT,
		message_count INTEGER DEFAULT 0, created_at TEXT, updated_at TEXT,
		last_message_at TEXT, is_background INTEGER DEFAULT 0, background_status TEXT
	);
	CREATE TABLE terminals (id TEXT PRIMARY KEY, narrator_id TEXT, status TEXT);
	CREATE TABLE narrator_messages (id TEXT PRIMARY KEY, narrator_id TEXT, content_json TEXT);
	CREATE TABLE narrator_message_refs (narrator_id TEXT, message_id TEXT);
	CREATE TABLE narrator_tool_calls (
		id TEXT PRIMARY KEY, narrator_id TEXT, input_json TEXT, output_json TEXT,
		created_at TEXT, is_file_history_checkpoint INTEGER DEFAULT 0
	);
	CREATE TABLE api_requests (id TEXT PRIMARY KEY, narrator_id TEXT, raw_dump_json TEXT);
`);
const realDbModule = { ...(await import("../../db")) };
mock.module("../../db", () => ({ ...realDbModule, sqlite }));
const { databaseCleanupService } = await import("../database-cleanup-service");
const old = "2020-01-01T00:00:00.000Z";
const savedWorkerSetting = process.env.NARRAFORK_DB_WORKER;

function seedNarrator(id: string, status = "archived", parentId: string | null = null): void {
	sqlite
		.prepare(`INSERT INTO narrators
		(id, parent_narrator_id, title, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`)
		.run(id, parentId, id, status, old, old);
}

/** The service may read plan metadata, but no payload aggregate or sample on this connection. */
function forbidMainThreadPayloadReads() {
	const prepare = sqlite.prepare.bind(sqlite);
	return spyOn(sqlite, "prepare").mockImplementation((sql, ...args) => {
		if (/\b(SUM|length|raw_dump_json|content_json|input_json|output_json)\b/i.test(sql)) {
			throw new Error("Forbidden main-thread payload scan");
		}
		return prepare(sql, ...args);
	});
}

beforeEach(() => {
	mock.restore();
	pool.resetDbWorkerPoolForTest();
	delete process.env.NARRAFORK_DB_WORKER;
	spyOn(connection, "getDbPath").mockReturnValue(dbPath);
	for (const table of [
		"narrators",
		"terminals",
		"narrator_messages",
		"narrator_message_refs",
		"narrator_tool_calls",
		"api_requests",
	]) {
		sqlite.exec(`DELETE FROM ${table}`);
	}
});
afterEach(() => {
	mock.restore();
	pool.shutdownDbWorkerPool();
	if (savedWorkerSetting === undefined) delete process.env.NARRAFORK_DB_WORKER;
	else process.env.NARRAFORK_DB_WORKER = savedWorkerSetting;
});
afterAll(() => {
	mock.module("../../db", () => realDbModule);
	sqlite.close();
	rmSync(home, { recursive: true, force: true });
});

describe("session cleanup read workers", () => {
	test("worker measures counts, legacy dump bytes and samples without main-thread payload reads", async () => {
		seedNarrator("root");
		seedNarrator("child", "archived", "root");
		seedNarrator("survivor", "running");
		sqlite.exec(`INSERT INTO narrator_messages VALUES ('owned', 'root', 'hello'), ('shared', 'root', 'shared');
			INSERT INTO narrator_message_refs VALUES ('root', 'owned'), ('root', 'shared'), ('survivor', 'shared');
			INSERT INTO narrator_tool_calls VALUES ('tc', 'child', '{}', 'output', '${old}', 0)`);
		const raw = "x".repeat(256 * 1024);
		sqlite.prepare("INSERT INTO api_requests VALUES ('request', 'root', ?)").run(raw);
		forbidMainThreadPayloadReads();
		const preview = await databaseCleanupService.previewCleanup("archivedSessions");
		expect(preview.counts).toEqual({
			sessions: 1,
			narrators: 2,
			descendantNarrators: 1,
			messages: 1,
			toolCalls: 1,
			apiRequests: 1,
		});
		expect(preview.approxBytes).toBeGreaterThan(raw.length);
		expect(preview.samples).toHaveLength(1);
		expect(preview.samples[0].approxBytes).toBe(preview.approxBytes);
		expect(preview.warningCodes).toEqual(["deletesUsageHistory"]);
	}, 30_000);

	test("stale preview retains total counts but caps worker samples at ten", async () => {
		for (let i = 0; i < 12; i++) seedNarrator(`stale-${i}`, "idle");
		forbidMainThreadPayloadReads();
		const preview = await databaseCleanupService.previewCleanup("staleSessions", {
			sampleLimit: 100,
		});
		expect(preview.counts.sessions).toBe(12);
		expect(preview.counts.narrators).toBe(12);
		expect(preview.samples).toHaveLength(10);
		expect(preview.samples.every((sample) => sample.approxBytes > 0)).toBe(true);
	}, 30_000);

	test("disabled shared pool still measures on an ephemeral worker", async () => {
		seedNarrator("archived");
		process.env.NARRAFORK_DB_WORKER = "off";
		forbidMainThreadPayloadReads();
		const preview = await databaseCleanupService.previewCleanup("archivedSessions");
		expect(preview.counts.narrators).toBe(1);
		expect(preview.approxBytes).toBeGreaterThan(0);
	}, 30_000);

	test("worker rejects more than ten sample groups instead of expanding scan work", async () => {
		await expect(
			pool.runReadTask(dbPath, {
				kind: "storageScanSessionPreview",
				narratorIds: [],
				sampleNarratorIds: Array.from({ length: 11 }, () => []),
				relations: [],
			}),
		).rejects.toThrow("sample limit exceeded");
	}, 30_000);

	test("budget accepts exact bounds without mutating or truncating input", () => {
		const params: StorageScanSessionPreviewParams = {
			kind: "storageScanSessionPreview",
			narratorIds: Array.from({ length: SESSION_PREVIEW_MAX_ID_ENTRIES / 2 }, () =>
				"n".repeat(SESSION_PREVIEW_MAX_ID_LENGTH),
			),
			sampleNarratorIds: [
				Array.from({ length: SESSION_PREVIEW_MAX_ID_ENTRIES / 2 }, () => "sample"),
			],
			relations: Array.from({ length: SESSION_PREVIEW_MAX_RELATIONS }, () => ({
				tableName: "t".repeat(SESSION_PREVIEW_MAX_RELATION_FIELD_LENGTH),
				alias: "a".repeat(SESSION_PREVIEW_MAX_RELATION_FIELD_LENGTH),
				narratorColumn: "c".repeat(SESSION_PREVIEW_MAX_RELATION_FIELD_LENGTH),
			})),
		};
		const before = JSON.stringify(params);
		expect(() => assertSessionPreviewBudget(params)).not.toThrow();
		expect(JSON.stringify(params)).toBe(before);
	});

	const overBudgetCases: Array<{
		name: string;
		partial: Partial<StorageScanSessionPreviewParams>;
		error: string;
	}> = [
		{
			name: "total ids",
			partial: { narratorIds: Array(SESSION_PREVIEW_MAX_ID_ENTRIES + 1).fill("n") },
			error: "id entry limit exceeded",
		},
		{
			name: "one sample id list",
			partial: { sampleNarratorIds: [Array(SESSION_PREVIEW_MAX_ID_ENTRIES + 1).fill("n")] },
			error: "id entry limit exceeded",
		},
		{
			name: "combined total and sample ids",
			partial: {
				narratorIds: Array(SESSION_PREVIEW_MAX_ID_ENTRIES).fill("n"),
				sampleNarratorIds: [["n"]],
			},
			error: "id entry limit exceeded",
		},
		{
			name: "id length",
			partial: { narratorIds: ["n".repeat(SESSION_PREVIEW_MAX_ID_LENGTH + 1)] },
			error: "id length limit exceeded",
		},
		{
			name: "sample id length",
			partial: { sampleNarratorIds: [["n".repeat(SESSION_PREVIEW_MAX_ID_LENGTH + 1)]] },
			error: "id length limit exceeded",
		},
		{
			name: "relation count",
			partial: {
				relations: Array.from({ length: SESSION_PREVIEW_MAX_RELATIONS + 1 }, () => ({
					tableName: "narrators",
					alias: "n",
					narratorColumn: "id",
				})),
			},
			error: "relation or id list limit exceeded",
		},
		...["tableName", "alias", "narratorColumn"].map((field) => ({
			name: `relation ${field} length`,
			partial: {
				relations: [
					{
						tableName: "narrators",
						alias: "n",
						narratorColumn: "id",
						[field]: "x".repeat(SESSION_PREVIEW_MAX_RELATION_FIELD_LENGTH + 1),
					},
				],
			},
			error: "relation field length limit exceeded",
		})),
	];
	for (const { name, partial, error } of overBudgetCases) {
		test(`worker rejects oversized ${name} before opening the database`, async () => {
			const params: StorageScanSessionPreviewParams = {
				kind: "storageScanSessionPreview",
				narratorIds: [],
				sampleNarratorIds: [],
				relations: [],
				...partial,
			};
			expect(() => assertSessionPreviewBudget(params)).toThrow(error);
			await expect(pool.runReadTask(join(home, "must-not-open.db"), params)).rejects.toMatchObject({
				code: "DATABASE_CLEANUP_PREVIEW_BUDGET_EXCEEDED",
				message: `Session cleanup preview ${error}`,
			});
		}, 30_000);
	}

	test("over-budget plan fails before sending either worker task or deleting any root", async () => {
		seedNarrator("n".repeat(SESSION_PREVIEW_MAX_ID_LENGTH + 1));
		forbidMainThreadPayloadReads();
		const shared = spyOn(pool, "runReadTask");
		const ephemeral = spyOn(pool, "runEphemeralReadTask");
		const remove = spyOn(narratorService, "remove").mockResolvedValue(undefined);
		for (const request of [
			() => databaseCleanupService.previewCleanup("archivedSessions"),
			() => databaseCleanupService.executeCleanup("archivedSessions"),
		]) {
			await expect(request()).rejects.toMatchObject({
				statusCode: 413,
				code: "DATABASE_CLEANUP_PREVIEW_BUDGET_EXCEEDED",
			});
		}
		expect(shared).not.toHaveBeenCalled();
		expect(ephemeral).not.toHaveBeenCalled();
		expect(remove).not.toHaveBeenCalled();
	});

	test("both workers unavailable rejects preview with 503 and never scans synchronously", async () => {
		seedNarrator("archived");
		const mainReads = forbidMainThreadPayloadReads();
		spyOn(pool, "runReadTask").mockRejectedValue(new pool.WorkersUnavailableError("disabled"));
		const ephemeral = spyOn(pool, "runEphemeralReadTask").mockRejectedValue(
			new Error("spawn failed"),
		);
		await expect(databaseCleanupService.previewCleanup("archivedSessions")).rejects.toMatchObject({
			statusCode: 503,
			code: "DATABASE_CLEANUP_WORKERS_UNAVAILABLE",
		});
		expect(ephemeral).toHaveBeenCalledTimes(1);
		expect(mainReads.mock.calls.every(([sql]) => !/\bSUM\b/i.test(sql))).toBe(true);
	});

	test("storage candidate failure is explicit even when the object scan succeeds", async () => {
		seedNarrator("archived");
		forbidMainThreadPayloadReads();
		spyOn(pool, "runReadTask").mockRejectedValue(new pool.WorkersUnavailableError("disabled"));
		spyOn(pool, "runEphemeralReadTask").mockRejectedValue(new Error("spawn failed"));
		const objects = spyOn(scanRunner, "runParallelObjectStorageScan").mockResolvedValue({
			executedOn: "workers",
			workerCount: 1,
			durationMs: 0,
			pageSize: 4096,
			pageCount: 1,
			freelistBytes: 0,
			objectBytes: 4096,
			scanMode: "approximate",
			categories: [],
			topTables: [],
			readFailures: { tableCount: 0, tableNames: [] },
		});
		await expect(databaseCleanupService.scanDatabaseBreakdown()).rejects.toMatchObject({
			statusCode: 503,
			code: "DATABASE_CLEANUP_WORKERS_UNAVAILABLE",
		});
		expect(objects).toHaveBeenCalledTimes(1);
	});

	test("cleanup waits for the worker re-preview before removing a root", async () => {
		seedNarrator("archived");
		forbidMainThreadPayloadReads();
		const remove = spyOn(narratorService, "remove").mockResolvedValue(undefined);
		spyOn(realDbModule.databaseMaintenance, "checkpoint").mockReturnValue(supported({ ok: true }));
		spyOn(realDbModule.databaseMaintenance, "refreshPlannerStatistics").mockReturnValue(
			supported({ ok: true }),
		);
		const run = pool.runReadTask;
		let release!: () => void;
		let started!: () => void;
		const gate = new Promise<void>((resolve) => {
			release = resolve;
		});
		const ready = new Promise<void>((resolve) => {
			started = resolve;
		});
		spyOn(pool, "runReadTask").mockImplementation(async (...args) => {
			started();
			await gate;
			return run(...args);
		});
		const cleanup = databaseCleanupService.executeCleanup("archivedSessions");
		await ready;
		expect(remove).not.toHaveBeenCalled();
		release();
		const result = await cleanup;
		expect(result.counts.narrators).toBe(1);
		expect(result.changed).toBe(true);
		expect(result.samples).toEqual([]);
		expect(remove).toHaveBeenCalledWith("archived");
	}, 30_000);

	for (const change of ["resumed", "new-descendant", "running-terminal", "recent-activity"]) {
		test(`cleanup revalidates eligibility after worker measurement: ${change}`, async () => {
			const target = change === "recent-activity" ? "staleSessions" : "archivedSessions";
			seedNarrator("root", target === "staleSessions" ? "idle" : "archived");
			forbidMainThreadPayloadReads();
			const remove = spyOn(narratorService, "remove").mockResolvedValue(undefined);
			const run = pool.runReadTask;
			spyOn(pool, "runReadTask").mockImplementation(
				async <T>(...args: Parameters<typeof run>): Promise<T> => {
					const result = await run<T>(...args);
					// The scan observed the old plan, but the main thread stayed available for changes.
					if (change === "resumed") {
						sqlite.run("UPDATE narrators SET status='running' WHERE id='root'");
					} else if (change === "new-descendant") {
						seedNarrator("new-child", "archived", "root");
					} else if (change === "running-terminal") {
						sqlite.run("INSERT INTO terminals VALUES ('terminal', 'root', 'running')");
					} else {
						sqlite
							.prepare("UPDATE narrators SET last_message_at=? WHERE id='root'")
							.run(new Date().toISOString());
					}
					return result;
				},
			);
			await expect(databaseCleanupService.executeCleanup(target)).rejects.toMatchObject({
				statusCode: 409,
				code: "DATABASE_CLEANUP_PLAN_CHANGED",
			});
			expect(remove).not.toHaveBeenCalled();
			expect(sqlite.prepare("SELECT id FROM narrators WHERE id='root'").get()).toEqual({
				id: "root",
			});
		}, 30_000);
	}

	test("cleanup refuses deletion when re-preview workers fail", async () => {
		seedNarrator("archived");
		forbidMainThreadPayloadReads();
		const remove = spyOn(narratorService, "remove").mockResolvedValue(undefined);
		spyOn(pool, "runReadTask").mockRejectedValue(new pool.WorkersUnavailableError("disabled"));
		spyOn(pool, "runEphemeralReadTask").mockRejectedValue(new Error("spawn failed"));
		await expect(databaseCleanupService.executeCleanup("archivedSessions")).rejects.toMatchObject({
			statusCode: 503,
		});
		expect(remove).not.toHaveBeenCalled();
	});

	test("pool shutdown does not start an ephemeral reader during maintenance", async () => {
		seedNarrator("archived");
		forbidMainThreadPayloadReads();
		spyOn(pool, "runReadTask").mockRejectedValue(new pool.WorkersUnavailableError("pool_shutdown"));
		const ephemeral = spyOn(pool, "runEphemeralReadTask");
		await expect(databaseCleanupService.previewCleanup("archivedSessions")).rejects.toThrow(
			"pool_shutdown",
		);
		expect(ephemeral).not.toHaveBeenCalled();
	});
});
