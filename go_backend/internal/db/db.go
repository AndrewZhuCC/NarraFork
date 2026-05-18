package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	SQL  *sql.DB
	Path string
}

func Open(ctx context.Context) (*Store, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home directory: %w", err)
	}
	dir := filepath.Join(home, ".narrafork")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}
	path := filepath.Join(dir, "narrafork.db")
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("canonical TypeScript database does not exist at %s; start the TypeScript backend once to create and migrate it before using go_backend", path)
		}
		return nil, fmt.Errorf("stat database: %w", err)
	}
	sqlDB, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	sqlDB.SetConnMaxLifetime(0)

	store := &Store{SQL: sqlDB, Path: path}
	if err := store.configure(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	// The Go compatibility backend must use the database schema created by the
	// original TypeScript/Bun backend. This verifier is read-only and intentionally
	// refuses to repair missing schema objects from Go.
	if err := store.VerifyCanonicalSchema(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	// After schema readiness passes, record Go's compatibility view in a
	// namespaced journal table. This does not execute Drizzle SQL or mutate any
	// TS-owned business schema object.
	if err := store.EnsureMigrationCompatibility(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) configure(ctx context.Context) error {
	pragmas := []string{
		"PRAGMA journal_mode = WAL",
		"PRAGMA foreign_keys = ON",
		"PRAGMA busy_timeout = 250",
	}
	for _, stmt := range pragmas {
		if _, err := s.SQL.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("configure sqlite %q: %w", stmt, err)
		}
	}
	return nil
}

func (s *Store) Close() error {
	if s == nil || s.SQL == nil {
		return nil
	}
	return s.SQL.Close()
}

func NowISO() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}
