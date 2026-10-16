/**
 * Schema-only SQLite images for tests that need a fresh, empty, fully-migrated database per case.
 *
 * Several service tests copied the main schema into a new database for every test by replaying
 * every `CREATE TABLE`/`CREATE INDEX` (~550 statements, ~30-35 ms each time). This builds that
 * same schema once per process and serialises it; each fixture then starts from a byte copy.
 *
 * The resulting databases are identical to the per-case DDL replay: same definitions, taken from
 * the same isolated main database, with FTS tables still excluded and no rows. Connection-level
 * PRAGMAs (WAL, synchronous, foreign_keys, busy_timeout) are not part of the file image, so each
 * caller still applies them on its own handle exactly as before.
 */
import { Database } from "bun:sqlite";
import { writeFileSync } from "node:fs";

/** The definitions fixtures copied before; FTS shadow tables are deliberately left out. */
const SCHEMA_SQL =
	"SELECT sql FROM sqlite_master WHERE type IN ('table','index') AND sql IS NOT NULL AND name NOT LIKE 'sqlite_%' AND name NOT GLOB '*_fts*' ORDER BY type DESC LIMIT 2048";

let image: Uint8Array | undefined;
let statementCount = 0;

/** Serialised schema image of `source`, built on first use and reused by this process. */
export function schemaImage(source: Database): Uint8Array {
	if (image) return image;
	const definitions = source.query<{ sql: string }, []>(SCHEMA_SQL).all();
	if (definitions.length >= 2048) throw new Error("schema exceeds the fixture copy bound");
	const builder = new Database(":memory:");
	try {
		builder.transaction(() => {
			for (const row of definitions) builder.exec(row.sql);
		})();
		image = builder.serialize();
		statementCount = definitions.length;
	} finally {
		builder.close();
	}
	return image;
}

/** How many definitions the cached image contains (0 before the first build). */
export function schemaImageStatementCount(): number {
	return statementCount;
}

/** A new in-memory database holding the schema of `source` and no rows. */
export function openSchemaCopyInMemory(source: Database): Database {
	return Database.deserialize(schemaImage(source));
}

/** Write the schema of `source` to `path` (which must not exist yet) and open it. */
export function openSchemaCopyAt(source: Database, path: string): Database {
	writeFileSync(path, schemaImage(source), { flag: "wx" });
	return new Database(path);
}
