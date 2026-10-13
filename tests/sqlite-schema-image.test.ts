/**
 * The schema image must be indistinguishable from what fixtures used to build per case: the same
 * sqlite_master definitions, the same header (user_version/application_id), the same PRAGMA
 * behaviour on file-backed WAL copies, and no rows shared between copies.
 */
import { Database } from "bun:sqlite";
import { expect, test } from "bun:test";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { sqlite as isolatedTemplate } from "@server/db";
import { openSchemaCopyAt, openSchemaCopyInMemory } from "./sqlite-schema-image";

const DEFINITIONS_SQL =
	"SELECT sql FROM sqlite_master WHERE type IN ('table','index') AND sql IS NOT NULL AND name NOT LIKE 'sqlite_%' AND name NOT GLOB '*_fts*' ORDER BY type DESC LIMIT 2048";

const schemaOf = (db: Database) =>
	db
		.query<{ type: string; name: string; tbl_name: string; sql: string }, []>(
			"SELECT type,name,tbl_name,sql FROM sqlite_master WHERE sql IS NOT NULL ORDER BY type,name",
		)
		.all();
const header = (db: Database) => ({
	userVersion: db.query<{ user_version: number }, []>("PRAGMA user_version").get()?.user_version,
	applicationId: db.query<{ application_id: number }, []>("PRAGMA application_id").get()
		?.application_id,
});

test("schema image equals the per-case DDL replay and accepts the same PRAGMAs", () => {
	const replay = new Database(":memory:");
	for (const d of isolatedTemplate.query<{ sql: string }, []>(DEFINITIONS_SQL).all())
		replay.exec(d.sql);

	const mem = openSchemaCopyInMemory(isolatedTemplate);
	expect(schemaOf(mem)).toEqual(schemaOf(replay));
	expect(header(mem)).toEqual(header(replay));
	mem.exec("PRAGMA foreign_keys=ON; PRAGMA busy_timeout=0;");
	expect(mem.query("PRAGMA foreign_keys").get()).toEqual({ foreign_keys: 1 });
	expect(mem.query<{ n: number }, []>("SELECT count(*) AS n FROM users").get()?.n).toBe(0);

	const root = mkdtempSync(join(tmpdir(), "img-probe-"));
	try {
		const file = openSchemaCopyAt(isolatedTemplate, join(root, "x.sqlite"));
		file.exec(
			"PRAGMA foreign_keys=ON; PRAGMA busy_timeout=0; PRAGMA journal_mode=WAL; PRAGMA synchronous=NORMAL;",
		);
		expect(file.query("PRAGMA journal_mode").get()).toEqual({ journal_mode: "wal" });
		expect(file.query("PRAGMA synchronous").get()).toEqual({ synchronous: 1 });
		expect(schemaOf(file)).toEqual(schemaOf(replay));
		expect(header(file)).toEqual(header(replay));
		file.exec("INSERT INTO users(id,username,password_hash,created_at) VALUES('a','a','x','t')");
		file.close();
	} finally {
		rmSync(root, { recursive: true, force: true });
	}
	// Guard against a vacuous pass if the source query ever stops matching the schema.
	expect(schemaOf(replay).length).toBeGreaterThan(100);
});

test("each copy is independent: rows written to one never appear in the next", () => {
	const first = openSchemaCopyInMemory(isolatedTemplate);
	first.exec("INSERT INTO users(id,username,password_hash,created_at) VALUES('a','a','x','t')");
	const second = openSchemaCopyInMemory(isolatedTemplate);
	expect(second.query<{ n: number }, []>("SELECT count(*) AS n FROM users").get()?.n).toBe(0);
	first.close();
	second.close();
});
