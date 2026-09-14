/**
 * Test setup: in-memory SQLite database with full schema.
 *
 * Instead of maintaining a hand-written DDL (which drifts from the real schema),
 * we read the latest Drizzle migration SQL files and replay them against an
 * in-memory database. This guarantees the test DB always matches production.
 *
 * Usage:
 *   import { getTestDb, cleanDb } from "../setup";
 *   const { db, sqlite } = getTestDb();
 *   afterEach(() => cleanDb(sqlite));
 */

import { Database } from "bun:sqlite";
import { join } from "node:path";
import { drizzle } from "drizzle-orm/bun-sqlite";
import { ensureColumns } from "../server/db/ensure-columns";
import * as relations from "../server/db/relations";
import { applyPendingMigrationsByHash } from "../server/db/run-migrations";
import * as schema from "../server/db/schema";

const DRIZZLE_DIR = join(import.meta.dir, "..", "drizzle");

export function getTestDb() {
	const sqlite = new Database(":memory:");
	sqlite.run("PRAGMA foreign_keys = OFF");
	applyPendingMigrationsByHash(sqlite, DRIZZLE_DIR);
	ensureColumns(sqlite);
	sqlite.run("PRAGMA foreign_keys = ON");
	const db = drizzle({ client: sqlite, schema: { ...schema, ...relations } });
	return { db, sqlite };
}

/**
 * Delete all rows from every ordinary table (FK enforcement is off for the duration).
 *
 * Virtual tables and the shadow tables backing them are skipped. SQLite refuses
 * direct writes to an FTS5 shadow table (`<name>_data`, `_idx`, `_content`, …),
 * so a test that mirrors production by calling `ensureFts` would otherwise fail
 * here. Deleting the base-table rows is enough: the FTS sync triggers keep the
 * index in step.
 */
export function cleanDb(sqlite: Database) {
	sqlite.run("PRAGMA foreign_keys = OFF");
	const allTables = (
		sqlite
			.prepare(
				"SELECT name, sql FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'",
			)
			.all() as Array<{ name: string; sql: string | null }>
	).map((row) => ({
		name: row.name,
		isVirtual: /^\s*CREATE\s+VIRTUAL\s+TABLE/i.test(row.sql ?? ""),
	}));

	const virtualNames = allTables.filter((t) => t.isVirtual).map((t) => t.name);
	const isShadowTable = (name: string) =>
		virtualNames.some((virtualName) => name.startsWith(`${virtualName}_`));

	for (const { name, isVirtual } of allTables) {
		if (isVirtual || isShadowTable(name)) continue;
		sqlite.run(`DELETE FROM "${name}"`);
	}
	sqlite.run("PRAGMA foreign_keys = ON");
}
