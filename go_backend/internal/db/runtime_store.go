package db

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
)

const goRuntimeDBFileName = "go-runtime.db"

func runtimeSidecarPathForSQLite(sqlitePath string) string {
	return filepath.Join(filepath.Dir(sqlitePath), goRuntimeDBFileName)
}

func openSQLiteRuntimeSidecar(ctx context.Context, sqlitePath string) (*sql.DB, string, error) {
	runtimePath := runtimeSidecarPathForSQLite(sqlitePath)
	sqlDB, err := sql.Open((SQLiteDialect{}).DriverName(), runtimePath)
	if err != nil {
		return nil, "", fmt.Errorf("open Go runtime sidecar database: %w", err)
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	pragmas := []string{
		"PRAGMA journal_mode = WAL",
		"PRAGMA foreign_keys = ON",
		"PRAGMA busy_timeout = 250",
	}
	for _, stmt := range pragmas {
		if _, err := sqlDB.ExecContext(ctx, stmt); err != nil {
			_ = sqlDB.Close()
			return nil, "", fmt.Errorf("configure Go runtime sidecar %q: %w", stmt, err)
		}
	}
	return sqlDB, runtimePath, nil
}
