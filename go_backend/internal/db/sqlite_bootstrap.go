package db

import (
	"context"
	"database/sql"
	"fmt"
)

type SQLiteBootstrapResult struct {
	RanMigrations bool
	EnsuredFTS    bool
	FTSRebuilt    bool
}

// BootstrapSQLiteBunFinalCompatible is an explicit legacy/test bootstrap helper
// for Bun-final-compatible fixtures and future Go-owned databases. It is
// intentionally not part of the read-only TS-owned transitional startup path.
func (s *Store) BootstrapSQLiteBunFinalCompatible(ctx context.Context) error {
	if s == nil || s.SQL == nil {
		return fmt.Errorf("bootstrap SQLite Bun-final-compatible database: database is not open")
	}
	_, err := BootstrapSQLiteBunFinalCompatible(ctx, s.SQL)
	return err
}

func BootstrapSQLiteBunFinalCompatible(ctx context.Context, sqlDB *sql.DB) (SQLiteBootstrapResult, error) {
	if sqlDB == nil {
		return SQLiteBootstrapResult{}, fmt.Errorf("bootstrap SQLite Bun-final-compatible database: database is nil")
	}
	result := SQLiteBootstrapResult{}

	empty, err := sqliteHasNoUserObjects(ctx, sqlDB)
	if err != nil {
		return SQLiteBootstrapResult{}, fmt.Errorf("inspect SQLite schema before bootstrap: %w", err)
	}
	if empty {
		if err := RunBunCompatibleDrizzleMigrations(ctx, sqlDB, embeddedDrizzleMigrations); err != nil {
			return SQLiteBootstrapResult{}, fmt.Errorf("run Bun-final Drizzle migrations: %w", err)
		}
		result.RanMigrations = true
	}

	ftsResult, err := EnsureSQLiteFTS(ctx, sqlDB, EnsureSQLiteFTSOptions{})
	if err != nil {
		return SQLiteBootstrapResult{}, fmt.Errorf("ensure Bun-final SQLite FTS: %w", err)
	}
	result.EnsuredFTS = true
	result.FTSRebuilt = ftsResult.Rebuilt
	return result, nil
}

func sqliteHasNoUserObjects(ctx context.Context, sqlDB *sql.DB) (bool, error) {
	var count int
	if err := sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE name NOT LIKE 'sqlite_%'`).Scan(&count); err != nil {
		return false, err
	}
	return count == 0, nil
}
