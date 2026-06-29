package database

import (
	"fmt"
	"os"
	"path/filepath"

	sqlite "gosqlite.org"
)

// openSQLite opens a SQLite database at the given path.
func openSQLite(path string) (*DB, error) {
	// Ensure parent directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}

	sqliteDB, err := sqlite.Open(sqlite.Config{
		Path:         path,
		Pragmas:      sqlite.RecommendedPragmas(), // WAL + busy_timeout=5s + foreign_keys=on
		MaxOpenConns: 4,                           // WAL allows concurrent reads
		TxLock:       "immediate",                 // Prevent BUSY_SNAPSHOT on write upgrade
	})
	if err != nil {
		return nil, fmt.Errorf("open sqlite %s: %w", path, err)
	}

	if err := sqliteDB.Ping(); err != nil {
		sqliteDB.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	logOpen(DialectSQLite, path)
	return &DB{DB: sqliteDB.DB, Dialect: SQLiteDialect{}}, nil
}
