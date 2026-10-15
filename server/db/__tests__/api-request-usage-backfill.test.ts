/** Historical token corrections require administrator evidence review, never startup guesses. */
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
	const row = database.query("SELECT input_tokens FROM api_requests WHERE id = ?").get(id) as {
		input_tokens: number;
	};
	return row.input_tokens;
}

describe("历史 token 数据不在启动时自动修正", () => {
	test("原始包含缓存的输入保留原值，等待人工核对", async () => {
		const database = openDatabase();
		await runMigrations(database);
		insertRequest(database, "raw", 81250, 80256, 0);

		await applySqliteDataBackfills(database);

		expect(readInputTokens(database, "raw")).toBe(81250);
		expect(
			database
				.query(
					"SELECT token_accounting_version, token_accounting_review_version FROM api_requests WHERE id='raw'",
				)
				.get(),
		).toEqual({ token_accounting_version: null, token_accounting_review_version: null });
	});

	test("已归一、Anthropic 缓存写入、无缓存的记录都不动，且重跑不再变化", async () => {
		const database = openDatabase();
		await runMigrations(database);
		insertRequest(database, "normalized", 994, 80256, 0);
		insertRequest(database, "anthropic", 5000, 3000, 1024);
		insertRequest(database, "nocache", 5000, 0, 0);
		insertRequest(database, "uncached-dominates", 1000, 100, 0);
		insertRequest(database, "full-cache", 100, 100, 0);

		await applySqliteDataBackfills(database);
		await applySqliteDataBackfills(database);

		expect(readInputTokens(database, "normalized")).toBe(994);
		expect(readInputTokens(database, "anthropic")).toBe(5000);
		expect(readInputTokens(database, "nocache")).toBe(5000);
		expect(readInputTokens(database, "uncached-dominates")).toBe(1000);
		expect(readInputTokens(database, "full-cache")).toBe(100);
	});
});
