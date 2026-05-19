package db

import (
	"path/filepath"
	"testing"
	"time"
)

func TestNewConfigDefaultsToSQLiteTSOwnedDatabase(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg, err := NewConfig(ConfigInput{})
	if err != nil {
		t.Fatalf("NewConfig returned error: %v", err)
	}
	wantPath := filepath.Join(home, ".narrafork", defaultSQLiteFileName)
	if cfg.Engine != EngineSQLite || cfg.SQLitePath != wantPath {
		t.Fatalf("unexpected sqlite default config: %#v want path %q", cfg, wantPath)
	}
	if !cfg.VerifyCanonical || !cfg.TSOwnedSchema || cfg.SQLiteBootstrapMode != SQLiteBootstrapModeBunFinalCompatible {
		t.Fatalf("sqlite default should bootstrap and verify Bun-final-compatible TS-owned schema: %#v", cfg)
	}
	if cfg.MaxOpenConns != 1 || cfg.MaxIdleConns != 1 || cfg.ConnMaxLifetime != 0 {
		t.Fatalf("unexpected sqlite pool defaults: %#v", cfg)
	}
}

func TestNewConfigUsesInputAndEnvOverrides(t *testing.T) {
	t.Setenv("NARRAFORK_DB_DRIVER", "postgres")
	t.Setenv("NARRAFORK_DATABASE_URL", "postgres://example/db")
	t.Setenv("NARRAFORK_POSTGRES_SCHEMA", "narrafork_runtime")
	t.Setenv("NARRAFORK_DB_MAX_OPEN_CONNS", "17")
	t.Setenv("NARRAFORK_DB_MAX_IDLE_CONNS", "9")
	t.Setenv("NARRAFORK_DB_CONN_MAX_LIFETIME_SECONDS", "45")
	t.Setenv("NARRAFORK_DB_VERIFY_CANONICAL", "true")
	t.Setenv("NARRAFORK_DB_TS_OWNED_SCHEMA", "true")

	cfg, err := NewConfig(ConfigInput{Driver: "sqlite"})
	if err != nil {
		t.Fatalf("NewConfig returned error: %v", err)
	}
	if cfg.Engine != EnginePostgres || cfg.PostgresDSN != "postgres://example/db" || cfg.PostgresSchema != "narrafork_runtime" {
		t.Fatalf("env should override database engine, DSN, and schema: %#v", cfg)
	}
	if cfg.MaxOpenConns != 17 || cfg.MaxIdleConns != 9 || cfg.ConnMaxLifetime != 45*time.Second {
		t.Fatalf("env pool overrides not applied: %#v", cfg)
	}
	if cfg.VerifyCanonical || cfg.TSOwnedSchema {
		t.Fatalf("postgres config must force Go-owned non-canonical mode: %#v", cfg)
	}
}

func TestNewConfigRejectsInvalidDriver(t *testing.T) {
	_, err := NewConfig(ConfigInput{Driver: "mysql"})
	if err == nil {
		t.Fatal("NewConfig should reject unsupported database drivers")
	}
}

func TestNewConfigRejectsInvalidSQLiteBootstrapMode(t *testing.T) {
	_, err := NewConfig(ConfigInput{SQLiteBootstrapMode: "native-now"})
	if err == nil {
		t.Fatal("NewConfig should reject unsupported SQLite bootstrap mode")
	}
}

func TestNewConfigRequiresPostgresDSN(t *testing.T) {
	_, err := NewConfig(ConfigInput{Driver: "postgres"})
	if err == nil {
		t.Fatal("NewConfig should require postgres DSN")
	}
}

func TestPostgresDataSourceNameCarriesQuotedSearchPath(t *testing.T) {
	cfg, err := NewConfig(ConfigInput{
		Driver:         "postgres",
		PostgresDSN:    "postgres://example/db",
		PostgresSchema: `Team Schema`,
	})
	if err != nil {
		t.Fatalf("NewConfig returned error: %v", err)
	}
	dsn, searchPath := splitPostgresDataSourceName(cfg.DataSourceName())
	if dsn != "postgres://example/db" || searchPath != `"Team Schema"` {
		t.Fatalf("unexpected encoded Postgres search_path: dsn=%q searchPath=%q", dsn, searchPath)
	}
}
