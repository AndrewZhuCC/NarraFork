package database

import (
	"log/slog"
)

const cleanShutdownMarker = 0x4e465243 // "NFRC"

// MarkCleanShutdown sets the application_id marker and checkpoints WAL.
// Only meaningful for SQLite. Call on graceful shutdown.
func MarkCleanShutdown(db *DB) {
	if db.Dialect.Kind() != DialectSQLite {
		return
	}
	db.Exec("PRAGMA application_id = 0x4e465243")
	db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
}

// ClearShutdownMarker resets the application_id so next startup can detect crashes.
func ClearShutdownMarker(db *DB) {
	if db.Dialect.Kind() != DialectSQLite {
		return
	}
	db.Exec("PRAGMA application_id = 0")
}

// RebuildFTS rebuilds all FTS5 indexes. Call after unclean shutdown or migration.
// Only meaningful for SQLite.
func RebuildFTS(db *DB) error {
	if db.Dialect.Kind() != DialectSQLite {
		return nil
	}
	stmts := []string{
		"INSERT INTO chapters_fts(chapters_fts) VALUES ('rebuild')",
		"INSERT INTO narrator_messages_fts(narrator_messages_fts) VALUES ('rebuild')",
		"INSERT INTO narrators_fts(narrators_fts) VALUES ('rebuild')",
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	slog.Info("FTS indexes rebuilt")
	return nil
}

// EnsureFTSHealth checks if FTS needs rebuilding (unclean shutdown detection)
// and rebuilds if necessary. Only meaningful for SQLite.
func EnsureFTSHealth(db *DB) {
	if db.Dialect.Kind() != DialectSQLite {
		return
	}

	var appID int
	row := db.QueryRow("PRAGMA application_id")
	if err := row.Scan(&appID); err != nil {
		return
	}

	if appID != cleanShutdownMarker {
		if err := RebuildFTS(db); err != nil {
			slog.Warn("FTS rebuild failed on startup", "error", err)
		} else {
			slog.Info("FTS rebuilt due to unclean shutdown")
		}
	}

	ClearShutdownMarker(db)
}
