package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

var ErrPostgresDriverUnavailable = errors.New("postgres driver unavailable")

type Store struct {
	SQL         *sql.DB
	RuntimeSQL  *sql.DB
	Path        string
	RuntimePath string
	Engine      Engine
	Dialect     Dialect
	Config      Config
}

func Open(ctx context.Context) (*Store, error) {
	cfg, err := NewConfig(ConfigInput{})
	if err != nil {
		return nil, err
	}
	return OpenWithConfig(ctx, cfg)
}

func OpenWithConfig(ctx context.Context, cfg Config) (*Store, error) {
	cfg, err := cfg.normalized()
	if err != nil {
		return nil, err
	}
	dialect, err := NewDialect(cfg.Engine)
	if err != nil {
		return nil, err
	}
	if cfg.Engine == EngineSQLite {
		if err := ensureSQLiteDatabaseFile(cfg.SQLitePath, cfg.SQLiteBootstrapMode); err != nil {
			return nil, err
		}
	}
	if cfg.Engine == EnginePostgres && !sqlDriverRegistered(dialect.DriverName()) {
		return nil, fmt.Errorf("%w: driver %q is not registered; add github.com/jackc/pgx/v5/stdlib when module downloads are available", ErrPostgresDriverUnavailable, dialect.DriverName())
	}
	sqlDB, err := sql.Open(dialect.DriverName(), cfg.DataSourceName())
	if err != nil {
		return nil, fmt.Errorf("open %s database: %w", cfg.Engine, err)
	}
	sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(cfg.ConnMaxLifetime)

	store := &Store{
		SQL:     sqlDB,
		Path:    cfg.DisplayName(),
		Engine:  cfg.Engine,
		Dialect: dialect,
		Config:  cfg,
	}
	if cfg.Engine == EngineSQLite {
		runtimeDB, runtimePath, err := openSQLiteRuntimeSidecar(ctx, cfg.SQLitePath)
		if err != nil {
			_ = sqlDB.Close()
			return nil, err
		}
		store.RuntimeSQL = runtimeDB
		store.RuntimePath = runtimePath
	}
	if err := store.configure(ctx); err != nil {
		_ = store.Close()
		return nil, err
	}
	if cfg.Engine == EngineSQLite && cfg.TSOwnedSchema && cfg.VerifyCanonical {
		if cfg.SQLiteBootstrapMode == SQLiteBootstrapModeBunFinalCompatible {
			if err := store.BootstrapSQLiteBunFinalCompatible(ctx); err != nil {
				_ = store.Close()
				return nil, err
			}
		}
		if err := store.VerifyCanonicalSchema(ctx); err != nil {
			_ = store.Close()
			return nil, err
		}
		if err := store.EnsureMigrationCompatibility(ctx); err != nil {
			_ = store.Close()
			return nil, err
		}
	}
	return store, nil
}

func sqlDriverRegistered(name string) bool {
	for _, registered := range sql.Drivers() {
		if registered == name {
			return true
		}
	}
	return false
}

func ensureSQLiteDatabaseFile(path string, mode SQLiteBootstrapMode) error {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			if mode == SQLiteBootstrapModeReadOnly {
				return fmt.Errorf("canonical TypeScript database does not exist at %s; start the TypeScript backend once to create and migrate it before using go_backend", path)
			}
			dir := filepath.Dir(path)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return fmt.Errorf("create database directory: %w", err)
			}
			return nil
		}
		return fmt.Errorf("stat database: %w", err)
	}
	return nil
}

func (s *Store) configure(ctx context.Context) error {
	if s.Engine == EnginePostgres {
		if err := s.SQL.PingContext(ctx); err != nil {
			return fmt.Errorf("ping postgres database: %w", err)
		}
		if err := SetPostgresSearchPath(ctx, s.SQL, s.Config.PostgresSchema); err != nil {
			return fmt.Errorf("set postgres search_path: %w", err)
		}
		return nil
	}
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
	if s == nil {
		return nil
	}
	var firstErr error
	if s.SQL != nil {
		if err := s.SQL.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if s.RuntimeSQL != nil && s.RuntimeSQL != s.SQL {
		if err := s.RuntimeSQL.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func NowISO() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}
