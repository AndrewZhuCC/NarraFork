/**
 * Retiring the envelope index (docs/BLOCK_STORAGE-DESIGN.md §4.6, P3c): `ensureFts` drops
 * `narrator_messages_fts` and its triggers once the reindex job has completed, keeps it until
 * then, and brings it back (rebuilt from `content_text`) when the block index is lost and the
 * reindex job has to start over.
 */

import { Database } from "bun:sqlite";
import { describe, expect, test } from "bun:test";
import { ensureFts, isEnvelopeIndexRetired, replaceBlockIndexText } from "./fts";

const ENVELOPE_OBJECTS = [
	"narrator_messages_fts",
	"narrator_messages_fts_insert",
	"narrator_messages_fts_update",
	"narrator_messages_fts_delete",
];

function fixture(): Database {
	const sqlite = new Database(":memory:");
	sqlite.exec(`
		PRAGMA foreign_keys = ON;
		CREATE TABLE narrators(id TEXT PRIMARY KEY, title TEXT);
		CREATE TABLE narrator_messages(
			id TEXT PRIMARY KEY, content_text TEXT, body_revision INTEGER NOT NULL DEFAULT 0
		);
		CREATE TABLE narrator_blocks(
			id TEXT PRIMARY KEY,
			message_id TEXT NOT NULL REFERENCES narrator_messages(id) ON DELETE CASCADE
		);
		CREATE TABLE narrator_block_backfill(
			job TEXT PRIMARY KEY, cursor INTEGER NOT NULL DEFAULT 0,
			complete INTEGER NOT NULL DEFAULT 0, paused INTEGER NOT NULL DEFAULT 0,
			processed INTEGER NOT NULL DEFAULT 0, updated_at TEXT NOT NULL
		);
	`);
	return sqlite;
}

const start = (sqlite: Database) => ensureFts(sqlite, { skipUncleanShutdownRebuild: true });

function present(sqlite: Database): string[] {
	return (
		sqlite
			.query(
				`SELECT name FROM sqlite_master WHERE name IN (${ENVELOPE_OBJECTS.map(() => "?").join(", ")})
				 ORDER BY name`,
			)
			.all(...ENVELOPE_OBJECTS) as Array<{ name: string }>
	).map((row) => row.name);
}

function reindex(sqlite: Database): { complete: number; cursor: number } | null {
	return sqlite
		.query("SELECT complete, cursor FROM narrator_block_backfill WHERE job = 'reindex'")
		.get() as { complete: number; cursor: number } | null;
}

function completeReindex(sqlite: Database): void {
	sqlite.run(
		`INSERT INTO narrator_block_backfill (job, complete, updated_at) VALUES ('reindex', 1, 'now')
		 ON CONFLICT (job) DO UPDATE SET complete = 1`,
	);
}

const envelopeHits = (sqlite: Database, query: string) =>
	(
		sqlite
			.query(
				`SELECT m.id FROM narrator_messages_fts JOIN narrator_messages m ON m.rowid = narrator_messages_fts.rowid
				 WHERE narrator_messages_fts MATCH ? ORDER BY m.id`,
			)
			.all(query) as Array<{ id: string }>
	).map((row) => row.id);

describe("envelope index retirement", () => {
	test("stays until the reindex job has completed", () => {
		const sqlite = fixture();
		start(sqlite);
		expect(present(sqlite)).toEqual([...ENVELOPE_OBJECTS].sort());
		expect(isEnvelopeIndexRetired()).toBe(false);
		sqlite.run("INSERT INTO narrator_block_backfill (job, updated_at) VALUES ('reindex', 'now')");
		start(sqlite);
		expect(present(sqlite)).toHaveLength(4);
	});

	test("retires with its triggers once reindex completes, and stays retired", () => {
		const sqlite = fixture();
		start(sqlite);
		sqlite.run("INSERT INTO narrator_messages (id, content_text) VALUES ('m1', 'marmalade')");
		completeReindex(sqlite);
		start(sqlite);
		expect(present(sqlite)).toEqual([]);
		expect(isEnvelopeIndexRetired()).toBe(true);
		// Writes no longer touch it, and the next start has nothing to do.
		sqlite.run("UPDATE narrator_messages SET content_text = 'quince' WHERE id = 'm1'");
		sqlite.run("DELETE FROM narrator_messages WHERE id = 'm1'");
		expect(start(sqlite)).toEqual({ rebuilt: false });
		expect(present(sqlite)).toEqual([]);
	});

	test("a lost block index restarts reindex and brings the envelope index back, rebuilt", () => {
		const sqlite = fixture();
		start(sqlite);
		sqlite.run(
			"INSERT INTO narrator_messages (id, content_text) VALUES ('m1', 'marmalade on toast')",
		);
		sqlite.run("INSERT INTO narrator_blocks (id, message_id) VALUES ('b1', 'm1')");
		replaceBlockIndexText(sqlite, "b1", "marmalade");
		completeReindex(sqlite);
		start(sqlite);
		expect(present(sqlite)).toEqual([]);
		// A migration rebuilt narrator_blocks: its delete trigger is gone, its rowids may have moved.
		sqlite.run("DROP TRIGGER narrator_blocks_fts_delete");
		const result = start(sqlite);
		expect(result.rebuilt).toBe(true);
		expect(reindex(sqlite)).toEqual({ complete: 0, cursor: 0 });
		expect(sqlite.query("SELECT count(*) AS n FROM narrator_blocks_fts").get()).toEqual({ n: 0 });
		expect(present(sqlite)).toEqual([...ENVELOPE_OBJECTS].sort());
		expect(isEnvelopeIndexRetired()).toBe(false);
		expect(envelopeHits(sqlite, '"marmalade"')).toEqual(["m1"]);
		// Once reindex has completed again, the next start retires it again.
		completeReindex(sqlite);
		start(sqlite);
		expect(present(sqlite)).toEqual([]);
	});

	test("a database without block storage keeps it", () => {
		const sqlite = new Database(":memory:");
		sqlite.exec(`
			CREATE TABLE narrators(id TEXT PRIMARY KEY, title TEXT);
			CREATE TABLE narrator_messages(id TEXT PRIMARY KEY, content_text TEXT);
		`);
		start(sqlite);
		expect(present(sqlite)).toHaveLength(4);
		expect(isEnvelopeIndexRetired()).toBe(false);
	});
});
