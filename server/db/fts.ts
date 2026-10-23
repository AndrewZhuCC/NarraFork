import type { Database } from "bun:sqlite";
import { logger } from "../lib/logger";

const CLEAN_SHUTDOWN_MARKER = 0x4e465243; // "NFRC" = NarraFork Clean

/**
 * Ensure FTS5 virtual tables and sync triggers exist.
 *
 * Drizzle doesn't support FTS5, so these are managed separately.
 * All statements use IF NOT EXISTS / DROP-then-CREATE to be idempotent.
 * Old non-trigram FTS tables are detected and recreated.
 */
export function markCleanShutdown(sqlite: Database): void {
	// Step 1 (fast, correctness-critical): stamp the clean marker. Writing `application_id`
	// lands the marker in the WAL, which is sufficient — the next startup opens the DB and
	// replays the WAL, so the marker is read back even without a checkpoint into the main
	// file. This single PRAGMA is near-instant.
	sqlite.run(`PRAGMA application_id = ${CLEAN_SHUTDOWN_MARKER}`);
	// Step 2 (best-effort optimization only): truncate the WAL to bound its size. This is
	// NOT required for clean-shutdown detection. A large WAL can make TRUNCATE slow, and on
	// Windows the ~5s console-close (CTRL_CLOSE_EVENT) timeout can force-kill the process
	// mid-checkpoint; guarding it here ensures a stalled/failed checkpoint never invalidates
	// the marker written in step 1.
	try {
		sqlite.run("PRAGMA wal_checkpoint(TRUNCATE)");
	} catch (err) {
		logger.warn("Clean-shutdown WAL checkpoint failed (marker already persisted)", {
			error: String(err),
		});
	}
}

/**
 * Read the clean-shutdown marker WITHOUT resetting it.
 *
 * Must be called before {@link ensureFts}, which resets `application_id` to 0
 * after deciding whether to rebuild the FTS indexes. The value read here reflects
 * how the *previous* process exited: `wasClean === true` means the last run wrote
 * the clean marker on graceful shutdown (so the DB and FTS indexes are trustworthy
 * and the expensive startup integrity check can be skipped).
 */
export function readCleanShutdownState(sqlite: Database): { wasClean: boolean } {
	const appId =
		(sqlite.prepare("PRAGMA application_id").get() as { application_id: number } | undefined)
			?.application_id ?? 0;
	return { wasClean: appId === CLEAN_SHUTDOWN_MARKER };
}

/**
 * Read the clean-shutdown marker AND reset it atomically.
 *
 * This ensures that if the process crashes after this point, the next startup
 * will see `wasClean: false` (the marker is consumed). Call this once at startup
 * before any mutations.
 */
export function consumeCleanShutdownState(sqlite: Database): { wasClean: boolean } {
	const result = readCleanShutdownState(sqlite);
	// Reset immediately so a crash after this point leaves the DB in "unclean" state.
	sqlite.run("PRAGMA application_id = 0");
	return result;
}

/** Env flag: force the old behavior of unconditionally rebuilding every FTS index after an
 * unclean shutdown (bypasses the cheaper 'integrity-check' probe). Escape hatch for the rare
 * case where the probe misses a corruption that the full rebuild would have fixed. */
const FORCE_FULL_FTS_REBUILD =
	process.env.NARRAFORK_DB_FULL_INTEGRITY_CHECK === "1" ||
	process.env.NARRAFORK_FTS_FULL_REBUILD === "1";

/**
 * Probe an external-content FTS5 table for corruption without rebuilding it.
 *
 * `INSERT INTO <t>(<t>, rank) VALUES('integrity-check', 1)` verifies both the index and
 * its external content. Without rank=1, SQLite only checks the index internally and misses
 * rowid drift after a content-table rebuild. This can scan the content, so it is restricted
 * to startup maintenance after an unclean shutdown, never the clean-start fast path.
 *
 * Returns `"ok"` when the probe passes, `"corrupt"` when it throws (SQLITE_CORRUPT_VTAB or a
 * "database disk image is malformed" style error), signalling the caller to rebuild that table.
 *
 * NOTE (safety trade-off): 'integrity-check' catches the vast majority of index/content
 * inconsistencies but is not a 100% guarantee against every edge-case corruption that could
 * later surface as a "malformed" error on UPDATE. The runtime safety net (recoverWithCli in
 * db-resilience.ts) still recovers such cases if they slip through, and its recovery path
 * recreates the FTS tables. Worst case is a delayed fix, not permanent data loss.
 */
function probeFtsIntegrity(sqlite: Database, table: string): "ok" | "corrupt" {
	try {
		sqlite.run(`INSERT INTO ${table}(${table}, rank) VALUES ('integrity-check', 1)`);
		return "ok";
	} catch (err) {
		logger.warn("FTS integrity probe failed — table will be rebuilt", {
			table,
			error: String(err),
		});
		return "corrupt";
	}
}

/**
 * Install a trigger whose definition carries a version (docs/BLOCK_STORAGE-DESIGN.md §4.6).
 * `CREATE TRIGGER IF NOT EXISTS` never replaces an existing definition, so a changed trigger
 * is recognised by the version comment SQLite keeps in its stored SQL and swapped in one
 * transaction. The name stays the same, so the missing-trigger repair above does not mistake
 * an upgrade for a rebuilt content table.
 */
function installVersionedTrigger(
	sqlite: Database,
	name: string,
	version: number,
	sql: (marker: string) => string,
): void {
	const marker = `/* narrafork:trigger-version ${version} */`;
	const installed = sqlite
		.prepare("SELECT sql FROM sqlite_master WHERE type = 'trigger' AND name = ?")
		.get(name) as { sql: string } | undefined;
	if (installed?.sql.includes(marker)) return;
	sqlite.transaction(() => {
		sqlite.run(`DROP TRIGGER IF EXISTS ${name}`);
		sqlite.run(sql(marker));
	})();
}

/** Bump when the envelope FTS update trigger's definition changes. */
const NARRATOR_MESSAGES_FTS_UPDATE_VERSION = 2;

/** Characters of one block's text the block index keeps (tool results are capped earlier). */
export const BLOCK_INDEX_MAX_CHARS = 256 * 1024;

/**
 * Create the block search index (docs/BLOCK_STORAGE-DESIGN.md §4.6, D6): trigram, keyed by the
 * block row's rowid, contentless — its text is a function of the block and its adopted ledger
 * result that the store computes when it writes the block, so no copy is stored. Rows are
 * removed by rowid, which needs `contentless_delete` (SQLite 3.43+); an older system SQLite
 * gets an ordinary FTS5 table instead, which supports the same statements.
 *
 * Returns whether the table was created by this call.
 */
function createBlockIndex(sqlite: Database): boolean {
	const exists = sqlite
		.prepare("SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'narrator_blocks_fts'")
		.get();
	if (exists) return false;
	try {
		sqlite.run(`
			CREATE VIRTUAL TABLE narrator_blocks_fts USING fts5(
				text, content='', contentless_delete=1, tokenize='trigram'
			)
		`);
	} catch (error) {
		logger.info("SQLite lacks contentless_delete; the block index stores its text", {
			error: String(error),
		});
		sqlite.run("CREATE VIRTUAL TABLE narrator_blocks_fts USING fts5(text, tokenize='trigram')");
	}
	return true;
}

/** Connections known to carry the block index; the helpers below are no-ops elsewhere. */
const blockIndexPresent = new WeakMap<Database, boolean>();

function hasBlockIndex(sqlite: Database): boolean {
	let present = blockIndexPresent.get(sqlite);
	if (present === undefined) {
		present = !!sqlite
			.query("SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'narrator_blocks_fts'")
			.get();
		blockIndexPresent.set(sqlite, present);
	}
	return present;
}

/**
 * Set the searchable text of one block, replacing what it had; empty text leaves it
 * unsearchable. Synchronous and transaction-agnostic: the store calls it inside the
 * transaction that writes the block (a step's seal, a tool's settlement, an edit). A block
 * that does not exist (yet) is not indexed.
 */
export function replaceBlockIndexText(sqlite: Database, blockId: string, text: string): void {
	if (!hasBlockIndex(sqlite)) return;
	removeBlockIndexText(sqlite, blockId);
	if (!text) return;
	sqlite
		.query(
			"INSERT INTO narrator_blocks_fts(rowid, text) SELECT rowid, ? FROM narrator_blocks WHERE id = ?",
		)
		.run(text.slice(0, BLOCK_INDEX_MAX_CHARS), blockId);
}

/**
 * Make one block unsearchable (it reopened, or it is about to be deleted). Deleting a block
 * row removes its index text by itself (`narrator_blocks_fts_delete`), cascades included.
 */
export function removeBlockIndexText(sqlite: Database, blockId: string): void {
	if (!hasBlockIndex(sqlite)) return;
	sqlite
		.query(
			"DELETE FROM narrator_blocks_fts WHERE rowid = (SELECT rowid FROM narrator_blocks WHERE id = ?)",
		)
		.run(blockId);
}

/** The block index writer `narrator-block-sync` registers, bound to one connection. */
export function blockIndexWriter(sqlite: Database): {
	write(blockId: string, text: string): void;
	drop(blockId: string): void;
} {
	return {
		write: (blockId, text) => replaceBlockIndexText(sqlite, blockId, text),
		drop: (blockId) => removeBlockIndexText(sqlite, blockId),
	};
}

/** Refill the rowid-bound draft index without relying on FTS5's external-content rebuild command. */
function repopulateKnowledgeDraftsFts(sqlite: Database, hasKnowledgeEntries: boolean): void {
	const refill = sqlite.transaction(() => {
		sqlite.run("DELETE FROM knowledge_drafts_fts");
		if (hasKnowledgeEntries) {
			sqlite.run(`
				INSERT INTO knowledge_drafts_fts(rowid, title, content)
				SELECT d.rowid, COALESCE(e.title, d.title, ''), d.content
				FROM knowledge_drafts d
				LEFT JOIN knowledge_entries e ON e.id = d.entry_id
			`);
		} else {
			sqlite.run(`
				INSERT INTO knowledge_drafts_fts(rowid, title, content)
				SELECT rowid, COALESCE(title, ''), content
				FROM knowledge_drafts
			`);
		}
	});
	refill();
}

/**
 * FTS objects of retired features, dropped on startup.
 *
 * An upgrade's migration drops the content table (and SQLite drops the table's triggers with
 * it), but the FTS5 virtual table and its shadow tables are runtime objects this module created
 * and no migration knows about. Triggers go first so a trigger can never outlive the index it
 * writes to. A healthy database has none of them: one schema lookup and no writes.
 */
const RETIRED_FTS_TRIGGERS = ["chapters_fts_insert", "chapters_fts_update", "chapters_fts_delete"];
const RETIRED_FTS_TABLES = ["chapters_fts"];

function dropRetiredFtsObjects(sqlite: Database): void {
	const names = [...RETIRED_FTS_TRIGGERS, ...RETIRED_FTS_TABLES];
	const present = sqlite
		.prepare(
			`SELECT name FROM sqlite_master WHERE type IN ('trigger', 'table') AND name IN (${names
				.map(() => "?")
				.join(", ")})`,
		)
		.all(...names) as Array<{ name: string }>;
	if (present.length === 0) return;
	sqlite.transaction(() => {
		for (const name of RETIRED_FTS_TRIGGERS) sqlite.run(`DROP TRIGGER IF EXISTS ${name}`);
		for (const name of RETIRED_FTS_TABLES) sqlite.run(`DROP TABLE IF EXISTS ${name}`);
	})();
	logger.info("Dropped retired FTS objects", { objects: present.map((row) => row.name) });
}

const envelopeIndexRetired = false;

/**
 * Whether `ensureFts` retired the envelope index (`narrator_messages_fts`) on this process's
 * database (docs/BLOCK_STORAGE-DESIGN.md §4.6, P3c): search then reads the block index alone.
 */
export function isEnvelopeIndexRetired(): boolean {
	return envelopeIndexRetired;
}

export function ensureFts(
	sqlite: Database,
	options: {
		skipUncleanShutdownRebuild?: boolean;
		/**
		 * How the previous process exited, as read by {@link consumeCleanShutdownState}.
		 *
		 * MUST be passed by the startup path. `consumeCleanShutdownState` resets `application_id`
		 * to 0 before this function runs, so re-reading the pragma here would report EVERY startup
		 * as unclean and pay the probe cost (seconds of blocked main thread) even after a clean
		 * shutdown. Omitted only by callers with no marker to consult (tests, tooling).
		 */
		wasClean?: boolean;
	} = {},
): { rebuilt: boolean } {
	// Guard: skip if base tables don't exist yet (fresh DB before first migration)
	const hasBaseTables = sqlite
		.prepare(
			"SELECT COUNT(*) as c FROM sqlite_master WHERE type='table' AND name IN ('narrators','narrator_messages')",
		)
		.get() as { c: number };

	if (hasBaseTables.c !== 2) {
		return { rebuilt: false };
	}

	dropRetiredFtsObjects(sqlite);

	// Detect and drop old non-trigram FTS tables so they get recreated correctly
	const ftsTablesRecreated: string[] = [];
	for (const table of ["narrator_messages_fts", "narrators_fts", "knowledge_drafts_fts"]) {
		const info = sqlite
			.prepare("SELECT sql FROM sqlite_master WHERE type='table' AND name=?")
			.get(table) as { sql: string } | undefined;
		if (info && !info.sql.includes("trigram")) {
			sqlite.run(`DROP TABLE IF EXISTS ${table}`);
			ftsTablesRecreated.push(table);
		}
	}

	// --- Virtual tables ---
	sqlite.run(`
		CREATE VIRTUAL TABLE IF NOT EXISTS narrator_messages_fts USING fts5(
			content_text, content='narrator_messages', content_rowid=rowid, tokenize='trigram'
		)
	`);
	sqlite.run(`
		CREATE VIRTUAL TABLE IF NOT EXISTS narrators_fts USING fts5(
			title, content='narrators', content_rowid=rowid, tokenize='trigram'
		)
	`);

	// The block index exists once block storage does (migration 0213).
	const hasNarratorBlocks =
		(
			sqlite
				.prepare(
					"SELECT COUNT(*) as c FROM sqlite_master WHERE type='table' AND name='narrator_blocks'",
				)
				.get() as { c: number }
		).c === 1;
	const blockIndexCreated = hasNarratorBlocks && createBlockIndex(sqlite);
	if (hasNarratorBlocks) blockIndexPresent.set(sqlite, true);

	// knowledge_entries_fts is created conditionally — the table may not exist yet on
	// older databases that haven't run the knowledge-base migration. Guarded below.
	const hasKnowledgeEntries =
		(
			sqlite
				.prepare(
					"SELECT COUNT(*) as c FROM sqlite_master WHERE type='table' AND name='knowledge_entries'",
				)
				.get() as { c: number }
		).c === 1;
	if (hasKnowledgeEntries) {
		// Column-set migration: an older DB has knowledge_entries_fts with only
		// (title, current_content). The `current_keywords` column was added for passive
		// auto-injection — detect the stale shape and drop it so it is recreated with the
		// new column (and rebuilt below via needsRebuild). The FTS column name must match
		// the base-table column name (external-content tables resolve columns by name on
		// 'rebuild'), hence `current_keywords` rather than `keywords`.
		const keInfo = sqlite
			.prepare("SELECT sql FROM sqlite_master WHERE type='table' AND name='knowledge_entries_fts'")
			.get() as { sql: string } | undefined;
		if (keInfo && !keInfo.sql.includes("current_keywords")) {
			sqlite.run("DROP TABLE IF EXISTS knowledge_entries_fts");
			// The sync triggers reference the FTS column list, so drop them too — they are
			// recreated (with the keywords column) by the CREATE TRIGGER calls below.
			sqlite.run("DROP TRIGGER IF EXISTS knowledge_entries_fts_insert");
			sqlite.run("DROP TRIGGER IF EXISTS knowledge_entries_fts_update");
			sqlite.run("DROP TRIGGER IF EXISTS knowledge_entries_fts_delete");
			ftsTablesRecreated.push("knowledge_entries_fts");
		}
		sqlite.run(`
			CREATE VIRTUAL TABLE IF NOT EXISTS knowledge_entries_fts USING fts5(
				title, current_content, current_keywords, content='knowledge_entries', content_rowid=rowid, tokenize='trigram'
			)
		`);
	}

	// knowledge_drafts_fts indexes each author's personal draft (title + content) so the
	// agent's KnowledgeSearch can surface a user's own uncommitted edits ("working copy").
	// NOT an external-content table: the FTS rowid is bound to knowledge_drafts.rowid so all
	// trigger maintenance is by rowid (O(log n)), never a scan over UNINDEXED columns.
	// `title` is de-normalized from the parent entry (drafts only edit content, not title)
	// and kept in sync by an entries-title trigger below.
	const hasKnowledgeDrafts =
		(
			sqlite
				.prepare(
					"SELECT COUNT(*) as c FROM sqlite_master WHERE type='table' AND name='knowledge_drafts'",
				)
				.get() as { c: number }
		).c === 1;
	// Track whether the drafts FTS table is being created for the first time so it gets
	// populated even on a clean startup (a brand-new empty FTS table won't otherwise be
	// caught by the unclean-shutdown rebuild path).
	let draftsFtsFreshlyCreated = false;
	let draftsFtsPopulated = false;
	if (hasKnowledgeDrafts) {
		const draftsFtsExists =
			(
				sqlite
					.prepare(
						"SELECT COUNT(*) as c FROM sqlite_master WHERE type='table' AND name='knowledge_drafts_fts'",
					)
					.get() as { c: number }
			).c === 1;
		draftsFtsFreshlyCreated = !draftsFtsExists;
		sqlite.run(`
			CREATE VIRTUAL TABLE IF NOT EXISTS knowledge_drafts_fts USING fts5(
				title, content, tokenize='trigram'
			)
		`);
	}

	// A Drizzle content-table rebuild drops its triggers and can renumber rowids while
	// leaving the external-content FTS index untouched (for example migration 0170).
	// Check BEFORE reinstalling triggers, even on a clean startup. Missing triggers are
	// also a durable retry signal: rebuild must succeed before that signal is removed.
	// Only inspect the fixed set of schema objects; healthy clean starts scan no content.
	const repairedContentIndexes = new Set<string>();
	const contentIndexTriggerSets = [
		{
			table: "narrator_messages_fts",
			rebuild: true,
			triggers: [
				"narrator_messages_fts_insert",
				"narrator_messages_fts_update",
				"narrator_messages_fts_delete",
			],
		},
		{
			table: "narrators_fts",
			rebuild: true,
			triggers: ["narrators_fts_insert", "narrators_fts_update", "narrators_fts_delete"],
		},
		...(hasKnowledgeEntries
			? [
					{
						table: "knowledge_entries_fts",
						rebuild: true,
						triggers: [
							"knowledge_entries_fts_insert",
							"knowledge_entries_fts_update",
							"knowledge_entries_fts_delete",
						],
					},
				]
			: []),
		...(hasKnowledgeDrafts
			? [
					{
						table: "knowledge_drafts_fts",
						rebuild: false,
						triggers: [
							"knowledge_drafts_fts_insert",
							"knowledge_drafts_fts_update",
							"knowledge_drafts_fts_delete",
							"knowledge_drafts_fts_entry_title",
						],
					},
				]
			: []),
		// A new block index has nothing to repair; an existing one whose delete trigger is gone
		// may point at renumbered rowids.
		...(hasNarratorBlocks && !blockIndexCreated
			? [
					{
						table: "narrator_blocks_fts",
						rebuild: false,
						triggers: ["narrator_blocks_fts_delete"],
					},
				]
			: []),
	] as const;
	for (const { table, rebuild, triggers: requiredTriggers } of contentIndexTriggerSets) {
		const placeholders = requiredTriggers.map(() => "?").join(", ");
		const triggers = sqlite
			.prepare(
				`SELECT name FROM sqlite_master WHERE type = 'trigger' AND name IN (${placeholders})`,
			)
			.all(...requiredTriggers);
		if (triggers.length === requiredTriggers.length) continue;
		const startedAt = Date.now();
		try {
			if (table === "knowledge_drafts_fts") {
				// This is a plain FTS table: refill it from the source rows before restoring any
				// trigger, so a failed refill leaves both the old index and the missing-trigger
				// retry signal intact.
				repopulateKnowledgeDraftsFts(sqlite, hasKnowledgeEntries);
				draftsFtsPopulated = true;
			} else if (table === "narrator_blocks_fts") {
				// Its text is computed by the store, not derivable here: drop what may now be
				// attached to the wrong blocks. Envelopes stay searchable through their own index.
				sqlite.run("DELETE FROM narrator_blocks_fts");
			} else if (rebuild) {
				sqlite.run(`INSERT INTO ${table}(${table}) VALUES ('rebuild')`);
			}
			repairedContentIndexes.add(table);
			logger.info(
				table === "knowledge_drafts_fts"
					? "FTS index repopulated before restoring missing sync triggers"
					: table === "narrator_blocks_fts"
						? "Block FTS index cleared before restoring its missing delete trigger"
						: rebuild
							? "FTS index rebuilt before restoring missing sync triggers"
							: "FTS index marked for repopulation before restoring missing sync triggers",
				{
					table,
					durationMs: Date.now() - startedAt,
				},
			);
		} catch (error) {
			logger.error("FTS content-table recovery failed; startup cannot continue", {
				table,
				durationMs: Date.now() - startedAt,
				error: String(error),
			});
			throw error;
		}
	}

	// --- Sync triggers: narrator_messages ---
	sqlite.run(`
		CREATE TRIGGER IF NOT EXISTS narrator_messages_fts_insert AFTER INSERT ON narrator_messages BEGIN
			INSERT INTO narrator_messages_fts(rowid, content_text)
			VALUES (NEW.rowid, NEW.content_text);
		END
	`);
	// v2: only a changed content_text re-indexes the row. v1 fired on every column, so each
	// revision bump, usage write or flag flip deleted and re-inserted the row's index.
	installVersionedTrigger(
		sqlite,
		"narrator_messages_fts_update",
		NARRATOR_MESSAGES_FTS_UPDATE_VERSION,
		(marker) => `
		CREATE TRIGGER narrator_messages_fts_update AFTER UPDATE OF content_text ON narrator_messages
		WHEN OLD.content_text IS NOT NEW.content_text BEGIN
			${marker}
			INSERT INTO narrator_messages_fts(narrator_messages_fts, rowid, content_text)
			VALUES ('delete', OLD.rowid, OLD.content_text);
			INSERT INTO narrator_messages_fts(rowid, content_text)
			VALUES (NEW.rowid, NEW.content_text);
		END
	`,
	);
	sqlite.run(`
		CREATE TRIGGER IF NOT EXISTS narrator_messages_fts_delete AFTER DELETE ON narrator_messages BEGIN
			INSERT INTO narrator_messages_fts(narrator_messages_fts, rowid, content_text)
			VALUES ('delete', OLD.rowid, OLD.content_text);
		END
	`);

	// --- Sync triggers: narrators ---
	sqlite.run(`
		CREATE TRIGGER IF NOT EXISTS narrators_fts_insert AFTER INSERT ON narrators
		WHEN NEW.title IS NOT NULL BEGIN
			INSERT INTO narrators_fts(rowid, title) VALUES (NEW.rowid, NEW.title);
		END
	`);
	// narrators_fts_update needs DROP+CREATE to handle the complex WHEN clause update
	sqlite.run("DROP TRIGGER IF EXISTS narrators_fts_update");
	sqlite.run(`
		CREATE TRIGGER narrators_fts_update AFTER UPDATE OF title ON narrators
		WHEN OLD.title IS NOT NULL OR NEW.title IS NOT NULL BEGIN
			INSERT INTO narrators_fts(narrators_fts, rowid, title)
			SELECT 'delete', OLD.rowid, OLD.title WHERE OLD.title IS NOT NULL;
			INSERT OR IGNORE INTO narrators_fts(rowid, title)
			SELECT NEW.rowid, NEW.title WHERE NEW.title IS NOT NULL;
		END
	`);
	sqlite.run(`
		CREATE TRIGGER IF NOT EXISTS narrators_fts_delete AFTER DELETE ON narrators
		WHEN OLD.title IS NOT NULL BEGIN
			INSERT INTO narrators_fts(narrators_fts, rowid, title)
			VALUES ('delete', OLD.rowid, OLD.title);
		END
	`);

	// --- Sync trigger: narrator_blocks (the store writes the text; deletes clean up here) ---
	// A deleted block's rowid can be reused by the next insert, so its text must leave the
	// index with it, including blocks removed by the envelope's ON DELETE CASCADE.
	if (hasNarratorBlocks) {
		sqlite.run(`
			CREATE TRIGGER IF NOT EXISTS narrator_blocks_fts_delete AFTER DELETE ON narrator_blocks BEGIN
				DELETE FROM narrator_blocks_fts WHERE rowid = OLD.rowid;
			END
		`);
	}

	// --- Sync triggers: knowledge_entries (title + current_content + current_keywords) ---
	if (hasKnowledgeEntries) {
		sqlite.run(`
			CREATE TRIGGER IF NOT EXISTS knowledge_entries_fts_insert AFTER INSERT ON knowledge_entries BEGIN
				INSERT INTO knowledge_entries_fts(rowid, title, current_content, current_keywords)
				VALUES (NEW.rowid, NEW.title, NEW.current_content, NEW.current_keywords);
			END
		`);
		sqlite.run(`
			CREATE TRIGGER IF NOT EXISTS knowledge_entries_fts_update AFTER UPDATE ON knowledge_entries BEGIN
				INSERT INTO knowledge_entries_fts(knowledge_entries_fts, rowid, title, current_content, current_keywords)
				VALUES ('delete', OLD.rowid, OLD.title, OLD.current_content, OLD.current_keywords);
				INSERT INTO knowledge_entries_fts(rowid, title, current_content, current_keywords)
				VALUES (NEW.rowid, NEW.title, NEW.current_content, NEW.current_keywords);
			END
		`);
		sqlite.run(`
			CREATE TRIGGER IF NOT EXISTS knowledge_entries_fts_delete AFTER DELETE ON knowledge_entries BEGIN
				INSERT INTO knowledge_entries_fts(knowledge_entries_fts, rowid, title, current_content, current_keywords)
				VALUES ('delete', OLD.rowid, OLD.title, OLD.current_content, OLD.current_keywords);
			END
		`);
	}

	// --- Sync triggers: knowledge_drafts (rowid-bound; title de-normalized from entry) ---
	if (hasKnowledgeDrafts) {
		// INSERT: index the new personal entry. Title comes from its own `title` (standalone
		// entry) or, when linked (entry_id set), the parent global entry's title.
		sqlite.run(`
			CREATE TRIGGER IF NOT EXISTS knowledge_drafts_fts_insert AFTER INSERT ON knowledge_drafts BEGIN
				INSERT INTO knowledge_drafts_fts(rowid, title, content)
				VALUES (
					NEW.rowid,
					COALESCE((SELECT title FROM knowledge_entries WHERE id = NEW.entry_id), NEW.title, ''),
					NEW.content
				);
			END
		`);
		// UPDATE: delete+reinsert by rowid (content may change; title re-resolved).
		sqlite.run(`
			CREATE TRIGGER IF NOT EXISTS knowledge_drafts_fts_update AFTER UPDATE ON knowledge_drafts BEGIN
				DELETE FROM knowledge_drafts_fts WHERE rowid = OLD.rowid;
				INSERT INTO knowledge_drafts_fts(rowid, title, content)
				VALUES (
					NEW.rowid,
					COALESCE((SELECT title FROM knowledge_entries WHERE id = NEW.entry_id), NEW.title, ''),
					NEW.content
				);
			END
		`);
		// DELETE: drop the indexed row by rowid.
		sqlite.run(`
			CREATE TRIGGER IF NOT EXISTS knowledge_drafts_fts_delete AFTER DELETE ON knowledge_drafts BEGIN
				DELETE FROM knowledge_drafts_fts WHERE rowid = OLD.rowid;
			END
		`);
		// entry title change → refresh de-normalized title on all its drafts. The subquery
		// resolves rowids via idx_kd_entry (entry_id B-tree), then updates by rowid.
		if (hasKnowledgeEntries) {
			sqlite.run(`
				CREATE TRIGGER IF NOT EXISTS knowledge_drafts_fts_entry_title AFTER UPDATE OF title ON knowledge_entries BEGIN
					UPDATE knowledge_drafts_fts
					SET title = NEW.title
					WHERE rowid IN (SELECT rowid FROM knowledge_drafts WHERE entry_id = NEW.id);
				END
			`);
		}
	}

	// --- Rebuild FTS indexes if needed ---
	// Two independent triggers, handled differently:
	//   1) Migration (tokenizer change / dropped-and-recreated table): the index is empty or
	//      built with the wrong tokenizer, so an UNCONDITIONAL full 'rebuild' is mandatory.
	//   2) Unclean shutdown: trigram indexes *can* silently corrupt on crash and later throw
	//      "malformed" on UPDATE, but usually they are fine. Instead of unconditionally
	//      rebuilding every table (the previous behavior — slow: full re-tokenization of the
	//      whole corpus, ~1 min on large narrator_messages), probe each table cheaply with
	//      'integrity-check' and rebuild ONLY the tables that actually fail. Set
	//      NARRAFORK_FTS_FULL_REBUILD=1 (or NARRAFORK_DB_FULL_INTEGRITY_CHECK=1) to force the
	//      old unconditional-rebuild behavior.
	// Prefer the caller-supplied marker state. Falling back to the live pragma is only correct for
	// callers that never consumed the marker; the startup path always passes `wasClean` because it
	// already reset `application_id` to 0 (see the option docs above).
	const wasClean =
		options.wasClean ??
		((sqlite.prepare("PRAGMA application_id").get() as { application_id: number } | undefined)
			?.application_id ?? 0) === CLEAN_SHUTDOWN_MARKER;
	const migrationForcedRebuild = ftsTablesRecreated.length > 0;
	const uncleanShutdown = !options.skipUncleanShutdownRebuild && !wasClean;
	const needsRebuild = migrationForcedRebuild || uncleanShutdown;

	if (needsRebuild) {
		const startedAt = Date.now();
		// Migration always rebuilds unconditionally. Unclean shutdown only rebuilds tables that
		// fail the cheap integrity probe (unless the force flag is set).
		const probeThenRebuild = uncleanShutdown && !migrationForcedRebuild && !FORCE_FULL_FTS_REBUILD;
		const ftsTables = [
			"narrators_fts",
			"narrator_messages_fts",
			...(hasKnowledgeEntries ? ["knowledge_entries_fts"] : []),
		];
		const rebuilt: string[] = [];
		const healthy: string[] = [];
		try {
			for (const table of ftsTables) {
				if (repairedContentIndexes.has(table)) continue;
				if (probeThenRebuild && probeFtsIntegrity(sqlite, table) === "ok") {
					healthy.push(table);
					continue;
				}
				sqlite.run(`INSERT INTO ${table}(${table}) VALUES ('rebuild')`);
				rebuilt.push(table);
			}
			logger.info("FTS indexes checked on startup", {
				reason: migrationForcedRebuild ? "migration" : "unclean_shutdown",
				mode: probeThenRebuild ? "probe_then_rebuild" : "full_rebuild",
				rebuilt,
				healthy,
				durationMs: Date.now() - startedAt,
			});
		} catch (err) {
			logger.warn("FTS rebuild failed on startup", {
				error: String(err),
				durationMs: Date.now() - startedAt,
			});
		}
	}

	// The block index cannot be rebuilt from SQL. After an unclean shutdown it is probed like the
	// others, and a corrupt one is recreated empty: envelopes stay searchable through their own
	// index, and a block regains its text when the store next writes it.
	if (
		uncleanShutdown &&
		hasNarratorBlocks &&
		!blockIndexCreated &&
		!repairedContentIndexes.has("narrator_blocks_fts") &&
		probeFtsIntegrity(sqlite, "narrator_blocks_fts") === "corrupt"
	) {
		try {
			sqlite.run("DROP TABLE narrator_blocks_fts");
			createBlockIndex(sqlite);
			logger.warn("Corrupt block FTS index recreated empty");
		} catch (err) {
			logger.warn("Block FTS index recreation failed on startup", { error: String(err) });
		}
	}

	// Populate knowledge_drafts_fts when rebuilding OR when the table was just created
	// (a brand-new empty table on a clean startup isn't covered by needsRebuild). It is a
	// plain (non external-content) FTS table, so 'rebuild' is unavailable — refill manually
	// with rowid bound to knowledge_drafts.rowid and title de-normalized from the entry. A
	// missing-trigger repair already performed this refill before trigger installation.
	if (
		hasKnowledgeDrafts &&
		!draftsFtsPopulated &&
		(needsRebuild || draftsFtsFreshlyCreated || repairedContentIndexes.has("knowledge_drafts_fts"))
	) {
		repopulateKnowledgeDraftsFts(sqlite, hasKnowledgeEntries);
		draftsFtsPopulated = true;
	}

	// Clear the clean shutdown marker — it will be set again on clean exit.
	// During Bun --hot reloads, skipUncleanShutdownRebuild prevents this running
	// process from being misclassified as a crashed previous process.
	sqlite.run("PRAGMA application_id = 0");

	return { rebuilt: needsRebuild || repairedContentIndexes.size > 0 };
}
