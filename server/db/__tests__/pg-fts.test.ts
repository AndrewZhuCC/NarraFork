/**
 * PG FTS shadow tables: trigger-maintained index parity and drift detection/repair,
 * verified against a real PostgreSQL 17 (the full `drizzle-postgres` baseline, applied
 * the way the migrator applies it).
 *
 * WHAT IS BEING PINNED
 * --------------------
 * The shadow tables in `server/db/pg-fts.ts` are the PG stand-in for SQLite's FTS5
 * external-content tables, and the triggers are the writer-blind invariant that keeps
 * them in sync. The behaviours that must NOT drift from the SQLite side (`fts.ts`):
 *
 *   - a base-row write makes the shadow row appear / change / disappear in the same
 *     transaction (tested by reading the shadow table, not by searching);
 *   - narrators index only non-null titles, and an unrelated-column UPDATE does not touch
 *     the shadow (SQLite's `AFTER UPDATE OF title` firing condition);
 *   - knowledge_drafts carry the PARENT ENTRY's title, de-normalized, and an entry rename
 *     cascades to every linked draft's shadow row;
 *   - drift is detected by naming the catalog (a dropped trigger is a durable retry
 *     signal) and by reconciling content counts, and `repairPgFts` heals REAL damage —
 *     rows written while a trigger was missing — without re-seeding anything.
 *
 * The test deliberately damages the database (drops triggers, writes rows while they are
 * gone) and then proves the probe reports exactly that damage and repair returns the
 * reconciliation to zero. Repairing by re-installing the catalog alone would leave the
 * content drifted; the post-repair probe is what proves otherwise.
 *
 * Gated by PG_INTEGRATION=1: when integration is requested, an unavailable database must
 * FAIL the test, never pass as "blocked".
 */
import { describe, expect, test } from "bun:test";
import type { PgRemoteDatabase } from "drizzle-orm/pg-proxy";
import { drizzle as drizzleProxy } from "drizzle-orm/pg-proxy";
import { migrate } from "drizzle-orm/pg-proxy/migrator";
import { migrationScript, psqlProxyCallback } from "../../../tests/db/pg-baseline-model";
import { withPostgres } from "../../../tests/db/pg-test-harness";
import { ensurePgFts, type PgExecutor, probePgFtsDrift, repairPgFts } from "../pg-fts";
import { createPostgresClient } from "../postgres-client";

const MIGRATIONS_FOLDER = "drizzle-postgres";
const RUN_TIMEOUT_MS = 420_000;
const NOW = "2026-09-20T00:00:00.000Z";

type Row = Record<string, unknown>;

async function one(pg: PgExecutor, query: string, params?: unknown[]): Promise<Row> {
	const rows = await pg.unsafe(query, params);
	if (rows.length !== 1) throw new Error(`expected one row, got ${rows.length}: ${query}`);
	return rows[0] as Row;
}

async function shadowRow(pg: PgExecutor, shadow: string, id: string): Promise<Row | null> {
	const rows = await pg.unsafe(`SELECT * FROM "${shadow}" WHERE id = $1`, [id]);
	return (rows[0] as Row) ?? null;
}

/** Seed the rows narrators and drafts point at (user → project). */
async function seedUserProject(pg: PgExecutor): Promise<void> {
	await pg.unsafe(
		`INSERT INTO users (id, username, password_hash, created_at) VALUES ($1, $1, 'x', $2)`,
		["pgfts-user", NOW],
	);
	await pg.unsafe(
		`INSERT INTO projects (id, name, created_at, updated_at) VALUES ($1, $1, $2, $2)`,
		["pgfts-project", NOW],
	);
}

/**
 * The chapter search catalog a release before the chapter removal installed: a shadow table
 * with its GIN index, its trigger function, and a trigger using it. The trigger hangs off a
 * stand-in base table because the real `chapters` table is gone once the migration ran.
 */
async function installRetiredChapterCatalog(pg: PgExecutor): Promise<void> {
	await pg.unsafe("CREATE EXTENSION IF NOT EXISTS pg_trgm");
	await pg.unsafe(
		`CREATE TABLE "search_chapters" (id text PRIMARY KEY, "title" text, "description" text)`,
	);
	await pg.unsafe(
		`CREATE INDEX "idx_search_chapters_title" ON "search_chapters" USING gin ("title" gin_trgm_ops)`,
	);
	await pg.unsafe(`CREATE OR REPLACE FUNCTION "nf_search_chapters_sync"() RETURNS trigger
		LANGUAGE plpgsql AS $fn$ BEGIN RETURN NEW; END $fn$`);
	await pg.unsafe(`CREATE TABLE "pgfts_retired_chapters" (id text PRIMARY KEY)`);
	await pg.unsafe(`CREATE TRIGGER "chapters_fts_insert" AFTER INSERT ON "pgfts_retired_chapters"
		FOR EACH ROW EXECUTE FUNCTION "nf_search_chapters_sync"()`);
}

describe("pg-fts shadow tables on a real PostgreSQL 17", () => {
	test.skipIf(process.env.PG_INTEGRATION !== "1")(
		"triggers maintain the index, drift is detected by name, repair heals content",
		async () => {
			const result = await withPostgres(async ({ port, exec }) => {
				// --- apply the real baseline exactly as the migrator applies it ----------------
				const db = drizzleProxy(psqlProxyCallback(exec)) as unknown as PgRemoteDatabase;
				await migrate(
					db,
					async (queries) => {
						const applied = await exec(migrationScript(queries));
						if (applied.code !== 0) {
							throw new Error(`migration failed (${applied.code}): ${applied.stderr.slice(-400)}`);
						}
					},
					{ migrationsFolder: MIGRATIONS_FOLDER },
				);

				// --- connect over the network, as the production client will -------------------
				const password = "pgfts-test-password";
				const altered = await exec(
					`ALTER ROLE CURRENT_USER PASSWORD '${password.replaceAll("'", "''")}';`,
				);
				expect(altered.code).toBe(0);
				const user = (await exec("SELECT current_user;")).stdout.trim();
				const client = createPostgresClient({
					driver: "bun-sql",
					url: `postgres://${encodeURIComponent(user)}:${encodeURIComponent(password)}@127.0.0.1:${port}/nf_harness`,
					max: 2,
					idleTimeout: 1,
					maxLifetime: 60,
					connectTimeout: 10,
				});
				const pg: PgExecutor = {
					unsafe: (query, params) =>
						client.sql.unsafe(query, params) as Promise<Record<string, unknown>[]>,
				};
				try {
					// --- an upgraded database still carries the retired chapter catalog ---------
					await installRetiredChapterCatalog(pg);

					// --- install the search catalog; the probe must agree it is complete --------
					await ensurePgFts(pg);
					// ...and the retired catalog is gone: table (with its index), function, trigger.
					expect(
						await pg.unsafe(
							`SELECT to_regclass('search_chapters') AS t,
								to_regclass('idx_search_chapters_title') AS i,
								to_regprocedure('nf_search_chapters_sync()') AS f,
								(SELECT count(*)::int FROM pg_trigger WHERE tgname = 'chapters_fts_insert') AS tr`,
						),
					).toEqual([{ t: null, i: null, f: null, tr: 0 }]);
					const clean = await probePgFtsDrift(pg);
					expect(clean).toMatchObject({
						missingExtensions: [],
						missingTables: [],
						missingIndexes: [],
						missingFunctions: [],
						missingTriggers: [],
						drifted: false,
					});
					// Idempotency is the install contract: a second run changes nothing.
					await ensurePgFts(pg);
					expect((await probePgFtsDrift(pg)).drifted).toBe(false);

					await seedUserProject(pg);

					// ── narrators: null-title semantics ────────────────────────────────────
					// Insert with NULL title: the WHEN clause keeps the row out of the index.
					await pg.unsafe(
						`INSERT INTO narrators (id, context_project_id, title, model, created_at, updated_at)
						 VALUES ('pgfts-narr', 'pgfts-project', NULL, 'model-a', $1, $1)`,
						[NOW],
					);
					expect(await shadowRow(pg, "search_narrators", "pgfts-narr")).toBeNull();
					// An unrelated-column update must NOT create an index row (AFTER UPDATE OF
					// title does not fire).
					await pg.unsafe(`UPDATE narrators SET model = 'model-b' WHERE id = 'pgfts-narr'`);
					expect(await shadowRow(pg, "search_narrators", "pgfts-narr")).toBeNull();
					// Setting a title indexes the row.
					await pg.unsafe(`UPDATE narrators SET title = 'titled now' WHERE id = 'pgfts-narr'`);
					expect(await shadowRow(pg, "search_narrators", "pgfts-narr")).toMatchObject({
						title: "titled now",
					});
					// An unrelated-column update must NOT rewrite the index row.
					await pg.unsafe(`UPDATE narrators SET model = 'model-c' WHERE id = 'pgfts-narr'`);
					expect(await shadowRow(pg, "search_narrators", "pgfts-narr")).toMatchObject({
						title: "titled now",
					});
					// Nulling the title removes the row.
					await pg.unsafe(`UPDATE narrators SET title = NULL WHERE id = 'pgfts-narr'`);
					expect(await shadowRow(pg, "search_narrators", "pgfts-narr")).toBeNull();
					// Insert with a title indexes immediately; delete removes.
					await pg.unsafe(`UPDATE narrators SET title = 'final title' WHERE id = 'pgfts-narr'`);
					expect(await shadowRow(pg, "search_narrators", "pgfts-narr")).toMatchObject({
						title: "final title",
					});
					await pg.unsafe(`DELETE FROM narrators WHERE id = 'pgfts-narr'`);
					expect(await shadowRow(pg, "search_narrators", "pgfts-narr")).toBeNull();

					// ── narrator_messages: insert / delete parity ──────────────────────────
					await pg.unsafe(
						`INSERT INTO narrators (id, context_project_id, title, created_at, updated_at)
						 VALUES ('pgfts-narr2', 'pgfts-project', 'session', $1, $1)`,
						[NOW],
					);
					await pg.unsafe(
						`INSERT INTO narrator_messages (id, narrator_id, role, content_json, content_text, created_at)
						 VALUES ('pgfts-msg', 'pgfts-narr2', 'user', '[]', 'message body text', $1)`,
						[NOW],
					);
					expect(await shadowRow(pg, "search_narrator_messages", "pgfts-msg")).toMatchObject({
						content_text: "message body text",
					});
					await pg.unsafe(
						`UPDATE narrator_messages SET content_text = 'edited body' WHERE id = 'pgfts-msg'`,
					);
					expect(await shadowRow(pg, "search_narrator_messages", "pgfts-msg")).toMatchObject({
						content_text: "edited body",
					});
					// An update that touches no indexed column must still leave exactly one,
					// unchanged shadow row (delete+insert semantics, no duplication).
					await pg.unsafe(`UPDATE narrator_messages SET role = 'assistant' WHERE id = 'pgfts-msg'`);
					expect(
						(
							await one(
								pg,
								`SELECT count(*)::int AS c FROM search_narrator_messages WHERE id = 'pgfts-msg'`,
							)
						).c,
					).toBe(1);
					await pg.unsafe(`DELETE FROM narrator_messages WHERE id = 'pgfts-msg'`);
					expect(await shadowRow(pg, "search_narrator_messages", "pgfts-msg")).toBeNull();

					// ── knowledge: entry title de-normalization and rename cascade ──────────
					await pg.unsafe(
						`INSERT INTO knowledge_collections (id, name, slug, created_at, updated_at)
						 VALUES ('pgfts-kc', 'collection', 'kc', $1, $1)`,
						[NOW],
					);
					await pg.unsafe(
						`INSERT INTO knowledge_entries (id, collection_id, title, slug, current_content, current_keywords, created_at, updated_at)
						 VALUES ('pgfts-ke', 'pgfts-kc', 'Entry title v1', 'entry', 'entry body', 'kw-one', $1, $1)`,
						[NOW],
					);
					expect(await shadowRow(pg, "search_knowledge_entries", "pgfts-ke")).toMatchObject({
						title: "Entry title v1",
						current_content: "entry body",
						current_keywords: "kw-one",
					});
					await pg.unsafe(
						`INSERT INTO knowledge_drafts (id, entry_id, author_user_id, content, content_hash, created_at, updated_at)
						 VALUES ('pgfts-kd', 'pgfts-ke', 'pgfts-user', 'draft body', 'h1', $1, $1)`,
						[NOW],
					);
					// The draft's shadow title is the ENTRY's title, not the draft's (NULL here).
					expect(await shadowRow(pg, "search_knowledge_drafts", "pgfts-kd")).toMatchObject({
						title: "Entry title v1",
						content: "draft body",
					});
					// Renaming the entry cascades to the linked draft's shadow row.
					await pg.unsafe(
						`UPDATE knowledge_entries SET title = 'Entry title v2' WHERE id = 'pgfts-ke'`,
					);
					expect(await shadowRow(pg, "search_knowledge_drafts", "pgfts-kd")).toMatchObject({
						title: "Entry title v2",
					});
					expect(await shadowRow(pg, "search_knowledge_entries", "pgfts-ke")).toMatchObject({
						title: "Entry title v2",
					});
					// Editing draft content refreshes its own shadow row only.
					await pg.unsafe(
						`UPDATE knowledge_drafts SET content = 'draft body v2' WHERE id = 'pgfts-kd'`,
					);
					expect(await shadowRow(pg, "search_knowledge_drafts", "pgfts-kd")).toMatchObject({
						title: "Entry title v2",
						content: "draft body v2",
					});
					await pg.unsafe(`DELETE FROM knowledge_drafts WHERE id = 'pgfts-kd'`);
					expect(await shadowRow(pg, "search_knowledge_drafts", "pgfts-kd")).toBeNull();

					// ══ drift: damage the catalog, write while it is damaged, prove repair ══
					await pg.unsafe(`DROP TRIGGER narrators_fts_insert ON narrators`);
					await pg.unsafe(`DROP TRIGGER narrators_fts_update ON narrators`);
					await pg.unsafe(`DROP TRIGGER narrators_fts_delete ON narrators`);
					await pg.unsafe(`DROP INDEX idx_search_knowledge_entries_current_keywords`);

					const drifted = await probePgFtsDrift(pg);
					// A dropped trigger is a durable retry signal: reported by NAME.
					expect(drifted.missingTriggers).toEqual([
						"narrators_fts_insert",
						"narrators_fts_update",
						"narrators_fts_delete",
					]);
					expect(drifted.missingIndexes).toEqual(["idx_search_knowledge_entries_current_keywords"]);
					expect(drifted.drifted).toBe(true);

					// Writes while the triggers are gone: one insert (missing), one title change
					// (mismatched), one delete (stale). None of these reach the shadow.
					await pg.unsafe(
						`INSERT INTO narrators (id, context_project_id, title, created_at, updated_at)
						 VALUES ('pgfts-narr3', 'pgfts-project', 'written while broken', $1, $1)`,
						[NOW],
					);
					await pg.unsafe(
						`UPDATE narrators SET title = 'renamed while broken' WHERE id = 'pgfts-narr2'`,
					);
					await pg.unsafe(
						`INSERT INTO narrators (id, context_project_id, title, created_at, updated_at)
						 VALUES ('pgfts-narr4', 'pgfts-project', 'doomed', $1, $1)`,
						[NOW],
					);
					// narr4's insert DID reach the shadow? No — the insert trigger is dropped,
					// so to create a stale row we index it via repair-free direct insert and
					// then delete the base row.
					await pg.unsafe(
						`INSERT INTO search_narrators (id, title) VALUES ('pgfts-narr4', 'doomed')`,
					);
					await pg.unsafe(`DELETE FROM narrators WHERE id = 'pgfts-narr4'`);

					const damaged = await probePgFtsDrift(pg);
					const narratorsRecon = damaged.reconciliation.find((r) => r.base === "narrators");
					expect(narratorsRecon).toMatchObject({
						missingRows: 1, // narr3 was never indexed
						staleRows: 1, // narr4's shadow row outlived its base row
						mismatchedRows: 1, // narr2's title changed behind the shadow's back
					});

					// Repair: reinstall + batched heal. Nothing is re-seeded — the base rows
					// written above are the ones the shadow must converge to.
					const repaired = await repairPgFts(pg, { batchSize: 2 });
					expect(repaired.report.drifted).toBe(false);
					expect(repaired.report.missingTriggers).toEqual([]);
					expect(repaired.report.missingIndexes).toEqual([]);
					expect(repaired.upserted.search_narrators).toBe(2);
					expect(repaired.purged.search_narrators).toBe(1);
					expect(await shadowRow(pg, "search_narrators", "pgfts-narr2")).toMatchObject({
						title: "renamed while broken",
					});
					expect(await shadowRow(pg, "search_narrators", "pgfts-narr3")).toMatchObject({
						title: "written while broken",
					});
					expect(await shadowRow(pg, "search_narrators", "pgfts-narr4")).toBeNull();
					// And the reinstalled triggers work again: the invariant is restored, not
					// just the data.
					await pg.unsafe(`UPDATE narrators SET title = 'post repair' WHERE id = 'pgfts-narr3'`);
					expect(await shadowRow(pg, "search_narrators", "pgfts-narr3")).toMatchObject({
						title: "post repair",
					});
					return "ok";
				} finally {
					await client.close();
				}
			});
			if (typeof result !== "string") {
				throw new Error(
					`pg-fts integration unavailable: ${result.status === "blocked" || result.status === "failed" ? result.reason : "unexpected harness result"}`,
				);
			}
			expect(result).toBe("ok");
		},
		RUN_TIMEOUT_MS,
	);
});
