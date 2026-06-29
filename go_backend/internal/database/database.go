// Package database provides database initialization, connection management,
// and migration execution for NarraFork, supporting both SQLite and PostgreSQL.
package database

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
)

// DB wraps *sql.DB with dialect awareness.
type DB struct {
	*sql.DB
	Dialect Dialect
}

// Open parses the DSN and routes to the appropriate driver.
//
// DSN formats:
//   - "" or file path → SQLite at ~/.narrafork/narrafork.db or given path
//   - "sqlite:/path/to/file.db" → explicit SQLite
//   - "postgres://..." or "postgresql://..." → PostgreSQL
func Open(dsn string) (*DB, error) {
	switch {
	case strings.HasPrefix(dsn, "postgres://"), strings.HasPrefix(dsn, "postgresql://"):
		return openPostgres(dsn)
	default:
		path := strings.TrimPrefix(dsn, "sqlite:")
		return openSQLite(path)
	}
}

// Close closes the database and performs any cleanup.
func (db *DB) Close() error {
	return db.DB.Close()
}

// Ping verifies the connection is alive.
func (db *DB) PingContext(ctx context.Context) error {
	return db.DB.PingContext(ctx)
}

// Exec is a convenience wrapper that uses the dialect's placeholder style.
// For simple queries without dynamic placeholders, use db.DB.ExecContext directly.
func (db *DB) ExecDialect(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return db.DB.ExecContext(ctx, query, args...)
}

// QueryDialect is a convenience wrapper for dialect-aware queries.
func (db *DB) QueryDialect(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return db.DB.QueryContext(ctx, query, args...)
}

// QueryRowDialect is a convenience wrapper for dialect-aware single-row queries.
func (db *DB) QueryRowDialect(ctx context.Context, query string, args ...any) *sql.Row {
	return db.DB.QueryRowContext(ctx, query, args...)
}

// MustOpen opens the database and panics on failure. Useful for tests.
func MustOpen(dsn string) *DB {
	db, err := Open(dsn)
	if err != nil {
		panic(fmt.Sprintf("database.MustOpen(%q): %v", dsn, err))
	}
	return db
}

// logOpen logs a successful database open.
func logOpen(kind DialectKind, info string) {
	slog.Info("database opened", "dialect", string(kind), "info", info)
}
