/**
 * `api_requests.input_tokens` 的缓存口径回填。
 *
 * OpenAI 系接口的 prompt_tokens 含缓存读取，内部口径要求 input_tokens 只统计未命中缓存的部分。
 * 归一前的写入路径把含缓存的值直接落库，回填负责补正历史记录；它每次启动都会运行，所以必须幂等。
 */
import { Database } from "bun:sqlite";
import { afterEach, describe, expect, test } from "bun:test";
import { applySqliteDataBackfills } from "../data-backfills";
import { runMigrations } from "../run-migrations";

const databases: Database[] = [];

function openDatabase(): Database {
	const database = new Database(":memory:");
	databases.push(database);
	return database;
}

afterEach(() => {
	for (const database of databases.splice(0)) database.close();
});

function insertRequest(
	database: Database,
	id: string,
	inputTokens: number,
	cachedInputTokens: number,
	cacheCreationInputTokens: number,
): void {
	database
		.prepare(
			`INSERT INTO api_requests (id, created_at, input_tokens, cached_input_tokens, cache_creation_input_tokens)
			 VALUES (?, '2026-01-01T00:00:00.000Z', ?, ?, ?)`,
		)
		.run(id, inputTokens, cachedInputTokens, cacheCreationInputTokens);
}

function readInputTokens(database: Database, id: string): number {
	const row = database
		.query("SELECT input_tokens FROM api_requests WHERE id = ?")
		.get(id) as { input_tokens: number };
	return row.input_tokens;
}

describe("api_requests 的 input token 口径回填", () => {
	test("把未归一记录的缓存读取从 input_tokens 中扣除", async () => {
		const database = openDatabase();
		await runMigrations(database);
		insertRequest(database, "raw", 81250, 80256, 0);

		await applySqliteDataBackfills(database);

		expect(readInputTokens(database, "raw")).toBe(994);
	});

	test("已归一、Anthropic 缓存写入、无缓存的记录都不动，且重跑不再变化", async () => {
		const database = openDatabase();
		await runMigrations(database);
		insertRequest(database, "normalized", 994, 80256, 0);
		insertRequest(database, "anthropic", 5000, 3000, 1024);
		insertRequest(database, "nocache", 5000, 0, 0);

		await applySqliteDataBackfills(database);
		await applySqliteDataBackfills(database);

		expect(readInputTokens(database, "normalized")).toBe(994);
		expect(readInputTokens(database, "anthropic")).toBe(5000);
		expect(readInputTokens(database, "nocache")).toBe(5000);
	});
});
