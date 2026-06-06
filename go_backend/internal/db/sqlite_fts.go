package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

const SQLiteCleanShutdownMarker = 0x4e465243 // "NFRC" = NarraFork Clean

type EnsureSQLiteFTSOptions struct {
	SkipUncleanShutdownRebuild bool
}

type EnsureSQLiteFTSResult struct {
	Rebuilt         bool
	RecreatedTables []string
}

type sqliteFTSTableSpec struct {
	Name      string
	CreateSQL string
	Rebuild   string
}

type sqliteFTSTriggerSpec struct {
	Name      string
	CreateSQL string
}

var sqliteFTSTableSpecs = []sqliteFTSTableSpec{
	{
		Name: "chapters_fts",
		CreateSQL: `CREATE VIRTUAL TABLE IF NOT EXISTS chapters_fts USING fts5(
			title, description, content='chapters', content_rowid=rowid, tokenize='trigram'
		)`,
		Rebuild: "INSERT INTO chapters_fts(chapters_fts) VALUES ('rebuild')",
	},
	{
		Name: "narrator_messages_fts",
		CreateSQL: `CREATE VIRTUAL TABLE IF NOT EXISTS narrator_messages_fts USING fts5(
			content_text, content='narrator_messages', content_rowid=rowid, tokenize='trigram'
		)`,
		Rebuild: "INSERT INTO narrator_messages_fts(narrator_messages_fts) VALUES ('rebuild')",
	},
	{
		Name: "narrators_fts",
		CreateSQL: `CREATE VIRTUAL TABLE IF NOT EXISTS narrators_fts USING fts5(
			title, content='narrators', content_rowid=rowid, tokenize='trigram'
		)`,
		Rebuild: "INSERT INTO narrators_fts(narrators_fts) VALUES ('rebuild')",
	},
}

var sqliteFTSTriggerSpecs = []sqliteFTSTriggerSpec{
	{
		Name: "chapters_fts_insert",
		CreateSQL: `CREATE TRIGGER chapters_fts_insert AFTER INSERT ON chapters BEGIN
			INSERT INTO chapters_fts(rowid, title, description)
			VALUES (NEW.rowid, NEW.title, NEW.description);
		END`,
	},
	{
		Name: "chapters_fts_update",
		CreateSQL: `CREATE TRIGGER chapters_fts_update AFTER UPDATE ON chapters BEGIN
			INSERT INTO chapters_fts(chapters_fts, rowid, title, description)
			VALUES ('delete', OLD.rowid, OLD.title, OLD.description);
			INSERT INTO chapters_fts(rowid, title, description)
			VALUES (NEW.rowid, NEW.title, NEW.description);
		END`,
	},
	{
		Name: "chapters_fts_delete",
		CreateSQL: `CREATE TRIGGER chapters_fts_delete AFTER DELETE ON chapters BEGIN
			INSERT INTO chapters_fts(chapters_fts, rowid, title, description)
			VALUES ('delete', OLD.rowid, OLD.title, OLD.description);
		END`,
	},
	{
		Name: "narrator_messages_fts_insert",
		CreateSQL: `CREATE TRIGGER narrator_messages_fts_insert AFTER INSERT ON narrator_messages BEGIN
			INSERT INTO narrator_messages_fts(rowid, content_text)
			VALUES (NEW.rowid, NEW.content_text);
		END`,
	},
	{
		Name: "narrator_messages_fts_update",
		CreateSQL: `CREATE TRIGGER narrator_messages_fts_update AFTER UPDATE ON narrator_messages BEGIN
			INSERT INTO narrator_messages_fts(narrator_messages_fts, rowid, content_text)
			VALUES ('delete', OLD.rowid, OLD.content_text);
			INSERT INTO narrator_messages_fts(rowid, content_text)
			VALUES (NEW.rowid, NEW.content_text);
		END`,
	},
	{
		Name: "narrator_messages_fts_delete",
		CreateSQL: `CREATE TRIGGER narrator_messages_fts_delete AFTER DELETE ON narrator_messages BEGIN
			INSERT INTO narrator_messages_fts(narrator_messages_fts, rowid, content_text)
			VALUES ('delete', OLD.rowid, OLD.content_text);
		END`,
	},
	{
		Name: "narrators_fts_insert",
		CreateSQL: `CREATE TRIGGER narrators_fts_insert AFTER INSERT ON narrators
		WHEN NEW.title IS NOT NULL BEGIN
			INSERT INTO narrators_fts(rowid, title) VALUES (NEW.rowid, NEW.title);
		END`,
	},
	{
		Name: "narrators_fts_update",
		CreateSQL: `CREATE TRIGGER narrators_fts_update AFTER UPDATE OF title ON narrators
		WHEN OLD.title IS NOT NULL OR NEW.title IS NOT NULL BEGIN
			INSERT INTO narrators_fts(narrators_fts, rowid, title)
			SELECT 'delete', OLD.rowid, OLD.title WHERE OLD.title IS NOT NULL;
			INSERT OR IGNORE INTO narrators_fts(rowid, title)
			SELECT NEW.rowid, NEW.title WHERE NEW.title IS NOT NULL;
		END`,
	},
	{
		Name: "narrators_fts_delete",
		CreateSQL: `CREATE TRIGGER narrators_fts_delete AFTER DELETE ON narrators
		WHEN OLD.title IS NOT NULL BEGIN
			INSERT INTO narrators_fts(narrators_fts, rowid, title)
			VALUES ('delete', OLD.rowid, OLD.title);
		END`,
	},
}

// EnsureSQLiteFTS is an explicit legacy/future-Go-owned repair helper.
//
// The TS-owned SQLite main database default startup path must not call this:
// canonical verification is read-only and reports FTS drift as
// ErrCanonicalSchemaMismatch instead of creating, dropping, recreating, or
// rebuilding FTS objects.
func EnsureSQLiteFTS(ctx context.Context, sqlDB *sql.DB, opts EnsureSQLiteFTSOptions) (EnsureSQLiteFTSResult, error) {
	if sqlDB == nil {
		return EnsureSQLiteFTSResult{}, errors.New("ensure sqlite fts: database is nil")
	}

	hasBaseTables, err := sqliteFTSHasBaseTables(ctx, sqlDB)
	if err != nil {
		return EnsureSQLiteFTSResult{}, fmt.Errorf("inspect sqlite fts base tables: %w", err)
	}
	if !hasBaseTables {
		return EnsureSQLiteFTSResult{}, nil
	}

	result := EnsureSQLiteFTSResult{}
	createdTables := []string{}
	for _, table := range sqliteFTSTableSpecs {
		sqlText, exists, err := schemaSQL(ctx, sqlDB, "table", table.Name)
		if err != nil {
			return EnsureSQLiteFTSResult{}, fmt.Errorf("inspect sqlite fts table %s: %w", table.Name, err)
		}
		if exists {
			if !strings.Contains(normalizeSQLForCompare(sqlText), "trigram") {
				if _, err := sqlDB.ExecContext(ctx, "DROP TABLE IF EXISTS "+quoteIdentifier(table.Name)); err != nil {
					return EnsureSQLiteFTSResult{}, fmt.Errorf("drop non-trigram sqlite fts table %s: %w", table.Name, err)
				}
				result.RecreatedTables = append(result.RecreatedTables, table.Name)
			}
		} else {
			createdTables = append(createdTables, table.Name)
		}
	}

	for _, table := range sqliteFTSTableSpecs {
		if _, err := sqlDB.ExecContext(ctx, table.CreateSQL); err != nil {
			return EnsureSQLiteFTSResult{}, fmt.Errorf("create sqlite fts table %s: %w", table.Name, err)
		}
	}

	for _, trigger := range sqliteFTSTriggerSpecs {
		if _, err := sqlDB.ExecContext(ctx, "DROP TRIGGER IF EXISTS "+quoteIdentifier(trigger.Name)); err != nil {
			return EnsureSQLiteFTSResult{}, fmt.Errorf("drop sqlite fts trigger %s: %w", trigger.Name, err)
		}
		if _, err := sqlDB.ExecContext(ctx, trigger.CreateSQL); err != nil {
			return EnsureSQLiteFTSResult{}, fmt.Errorf("create sqlite fts trigger %s: %w", trigger.Name, err)
		}
	}

	appID, err := sqliteApplicationID(ctx, sqlDB)
	if err != nil {
		return EnsureSQLiteFTSResult{}, fmt.Errorf("read sqlite clean shutdown marker: %w", err)
	}
	needsRebuild := len(createdTables) > 0 || len(result.RecreatedTables) > 0 || (!opts.SkipUncleanShutdownRebuild && appID != SQLiteCleanShutdownMarker)
	if needsRebuild {
		for _, table := range sqliteFTSTableSpecs {
			if _, err := sqlDB.ExecContext(ctx, table.Rebuild); err != nil {
				return EnsureSQLiteFTSResult{}, fmt.Errorf("rebuild sqlite fts table %s: %w", table.Name, err)
			}
		}
		result.Rebuilt = true
	}

	if _, err := sqlDB.ExecContext(ctx, "PRAGMA application_id = 0"); err != nil {
		return EnsureSQLiteFTSResult{}, fmt.Errorf("clear sqlite clean shutdown marker: %w", err)
	}
	return result, nil
}

func MarkSQLiteCleanShutdown(ctx context.Context, sqlDB *sql.DB) error {
	if sqlDB == nil {
		return errors.New("mark sqlite clean shutdown: database is nil")
	}
	if _, err := sqlDB.ExecContext(ctx, fmt.Sprintf("PRAGMA application_id = %d", SQLiteCleanShutdownMarker)); err != nil {
		return fmt.Errorf("set sqlite clean shutdown marker: %w", err)
	}
	if _, err := sqlDB.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		return fmt.Errorf("checkpoint sqlite wal after clean shutdown marker: %w", err)
	}
	return nil
}

func sqliteFTSHasBaseTables(ctx context.Context, sqlDB *sql.DB) (bool, error) {
	var count int
	if err := sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('chapters', 'narrators', 'narrator_messages')`).Scan(&count); err != nil {
		return false, err
	}
	return count == 3, nil
}

func sqliteApplicationID(ctx context.Context, sqlDB *sql.DB) (int, error) {
	var appID int
	if err := sqlDB.QueryRowContext(ctx, "PRAGMA application_id").Scan(&appID); err != nil {
		return 0, err
	}
	return appID, nil
}
