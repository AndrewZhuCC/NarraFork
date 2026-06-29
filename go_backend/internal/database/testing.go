package database

import (
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

// TestMain allows tests to run against both SQLite and PostgreSQL.
//
// By default tests run against SQLite (temp file per test).
// To also test PostgreSQL, set NARRAFORK_TEST_PG_DSN, e.g.:
//
//	NARRAFORK_TEST_PG_DSN="postgres://narrafork:narrafork@localhost:5433/narrafork_test?sslmode=disable" go test ./...
//
// The Podman PostgreSQL for local dev:
//
//	podman run -d --name narrafork-pg \
//	  -e POSTGRES_USER=narrafork -e POSTGRES_PASSWORD=narrafork -e POSTGRES_DB=narrafork \
//	  -p 5433:5432 docker.io/library/postgres:17-alpine
//
// Isolation: each OpenTestDB call creates a UNIQUE PostgreSQL schema and
// pins the connection pool's search_path to it. This lets many tests (and
// parallel test packages) share one database without DROP SCHEMA contention.

// TestDialects returns the dialects to test against based on environment.
// SQLite is always included; PostgreSQL is included when NARRAFORK_TEST_PG_DSN is set.
func TestDialects() []DialectConfig {
	dialects := []DialectConfig{
		{Name: "sqlite", DSN: ":memory:"},
	}
	if pgDSN := os.Getenv("NARRAFORK_TEST_PG_DSN"); pgDSN != "" {
		dialects = append(dialects, DialectConfig{Name: "postgres", DSN: pgDSN})
	}
	return dialects
}

// DialectConfig describes a database backend to test against.
type DialectConfig struct {
	Name string
	DSN  string
}

var schemaCounter atomic.Uint64

// OpenTestDB opens a fresh, isolated test database for the given dialect,
// runs all migrations, and returns it. Cleanup is registered automatically.
//
//   - SQLite: a temp file (so multiple pooled connections share one DB).
//   - PostgreSQL: a uniquely-named schema with search_path pinned to it,
//     dropped on cleanup. Safe for parallel tests against one database.
func OpenTestDB(t *testing.T, dc DialectConfig) *DB {
	t.Helper()

	if dc.Name == "postgres" {
		return openTestPostgres(t, dc.DSN)
	}

	// SQLite: temp file shared by the pool
	f := t.TempDir() + "/test.db"
	db, err := Open("sqlite:" + f)
	if err != nil {
		t.Fatalf("open sqlite test db: %v", err)
	}
	if err := Migrate(db); err != nil {
		db.Close()
		t.Fatalf("migrate sqlite test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func openTestPostgres(t *testing.T, baseDSN string) *DB {
	t.Helper()

	// Unique schema name per call. Must be a valid unquoted PG identifier
	// (letters, digits, underscore), so derive it from the counter only.
	schema := fmt.Sprintf("test_%d_%d", os.Getpid(), schemaCounter.Add(1))

	// First connect without a custom search_path to create the schema.
	admin, err := Open(baseDSN)
	if err != nil {
		t.Fatalf("open pg admin: %v", err)
	}
	if _, err := admin.Exec("CREATE SCHEMA " + schema); err != nil {
		admin.Close()
		t.Fatalf("create schema %s: %v", schema, err)
	}
	admin.Close()

	// Reconnect with search_path pinned to the new schema so every pooled
	// connection sees it. pgx accepts search_path as a connection parameter.
	dsn := appendDSNParam(baseDSN, "search_path", schema)
	db, err := Open(dsn)
	if err != nil {
		t.Fatalf("open pg test db: %v", err)
	}

	if err := Migrate(db); err != nil {
		db.Close()
		dropSchema(baseDSN, schema)
		t.Fatalf("migrate pg test db: %v", err)
	}

	t.Cleanup(func() {
		db.Close()
		dropSchema(baseDSN, schema)
	})
	return db
}

// dropSchema removes a test schema using a fresh admin connection.
func dropSchema(baseDSN, schema string) {
	admin, err := Open(baseDSN)
	if err != nil {
		return
	}
	defer admin.Close()
	admin.Exec("DROP SCHEMA IF EXISTS " + schema + " CASCADE")
}

// appendDSNParam appends a query parameter to a postgres URL DSN.
func appendDSNParam(dsn, key, value string) string {
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	return dsn + sep + key + "=" + value
}
