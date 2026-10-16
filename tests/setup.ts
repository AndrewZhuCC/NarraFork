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
import {
	initializeAbsentReceiptProtectionSource,
	installResourceProtectionTriggers,
} from "../server/db/resource-protection-index";
import { applyPendingMigrationsByHash } from "../server/db/run-migrations";
import * as schema from "../server/db/schema";
import { getNarraforkPath } from "../server/lib/narrafork-home";

const DRIZZLE_DIR = join(import.meta.dir, "..", "drizzle");

export function getTestDb() {
	const sqlite = new Database(":memory:");
	sqlite.run("PRAGMA foreign_keys = OFF");
	applyPendingMigrationsByHash(sqlite, DRIZZLE_DIR);
	ensureColumns(sqlite);
	// PR6's identity tables have no generated migration yet. Only add in-memory
	// fixtures here; production databases and migration files are never changed.
	sqlite.run(`
		CREATE TABLE IF NOT EXISTS user_git_identities (
			id TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			name TEXT NOT NULL, email TEXT NOT NULL, is_default INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_user_git_identities_user ON user_git_identities(user_id, created_at);
		CREATE TABLE IF NOT EXISTS narrator_git_identity_bindings (
			id TEXT PRIMARY KEY, narrator_id TEXT NOT NULL REFERENCES narrators(id) ON DELETE CASCADE,
			user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			identity_id TEXT NOT NULL REFERENCES user_git_identities(id) ON DELETE CASCADE,
			updated_at TEXT NOT NULL
		);
		CREATE UNIQUE INDEX IF NOT EXISTS idx_narrator_git_identity_unique ON narrator_git_identity_bindings(narrator_id, user_id);
		CREATE INDEX IF NOT EXISTS idx_narrator_git_identity_user ON narrator_git_identity_bindings(user_id);
	`);
	sqlite.run("PRAGMA foreign_keys = ON");
	// Match runtime tracking even when a filesystem/service test replaces ../server/db.
	installResourceProtectionTriggers(sqlite);
	initializeAbsentReceiptProtectionSource(sqlite, getNarraforkPath("worktree-requests"));
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
	installResourceProtectionTriggers(sqlite);
	initializeAbsentReceiptProtectionSource(sqlite, getNarraforkPath("worktree-requests"));
}
