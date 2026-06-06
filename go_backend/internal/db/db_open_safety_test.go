package db

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestOpenReadOnlyModeRefusesMissingDatabaseAndDoesNotCreateIt(t *testing.T) {
	home := t.TempDir()
	narraforkDir := filepath.Join(home, ".narrafork")
	dbPath := filepath.Join(narraforkDir, "narrafork.db")
	cfg, err := NewConfig(ConfigInput{
		SQLitePath:          dbPath,
		SQLiteBootstrapMode: string(SQLiteBootstrapModeReadOnly),
	})
	if err != nil {
		t.Fatalf("NewConfig returned error: %v", err)
	}

	store, err := OpenWithConfig(context.Background(), cfg)
	if err == nil {
		store.Close()
		t.Fatal("OpenWithConfig should fail when the canonical TS database does not exist in read-only mode")
	}
	if !strings.Contains(err.Error(), "canonical TypeScript database does not exist") {
		t.Fatalf("expected missing canonical DB error, got %q", err.Error())
	}
	if _, statErr := os.Stat(dbPath); !os.IsNotExist(statErr) {
		t.Fatalf("OpenWithConfig should not create narrafork.db in read-only mode; stat error = %v", statErr)
	}
	if _, statErr := os.Stat(narraforkDir); !os.IsNotExist(statErr) {
		t.Fatalf("OpenWithConfig should not create .narrafork in read-only mode; stat error = %v", statErr)
	}
}

func TestOpenRefusesMissingSQLiteDatabaseByDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	narraforkDir := filepath.Join(home, ".narrafork")
	dbPath := filepath.Join(narraforkDir, "narrafork.db")

	store, err := Open(context.Background())
	if err == nil {
		store.Close()
		t.Fatal("Open should fail when the default canonical TS database does not exist")
	}
	if !strings.Contains(err.Error(), "canonical TypeScript database does not exist") {
		t.Fatalf("expected missing canonical DB error, got %q", err.Error())
	}
	if _, statErr := os.Stat(dbPath); !os.IsNotExist(statErr) {
		t.Fatalf("Open should not create narrafork.db by default; stat error = %v", statErr)
	}
	if _, statErr := os.Stat(narraforkDir); !os.IsNotExist(statErr) {
		t.Fatalf("Open should not create .narrafork by default; stat error = %v", statErr)
	}
}

func TestOpenWithExplicitLegacyModeBootstrapsMissingSQLiteDatabase(t *testing.T) {
	home := t.TempDir()
	dbPath := filepath.Join(home, ".narrafork", "narrafork.db")
	cfg, err := NewConfig(ConfigInput{
		SQLitePath:          dbPath,
		SQLiteBootstrapMode: string(SQLiteBootstrapModeBunFinalCompatible),
	})
	if err != nil {
		t.Fatalf("NewConfig returned error: %v", err)
	}

	store, err := OpenWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("OpenWithConfig should bootstrap missing SQLite database in explicit legacy mode: %v", err)
	}
	defer store.Close()
	if _, statErr := os.Stat(dbPath); statErr != nil {
		t.Fatalf("bootstrapped database should exist: %v", statErr)
	}
	if err := VerifyCanonicalSchema(context.Background(), store.SQL); err != nil {
		t.Fatalf("bootstrapped database should satisfy canonical schema: %v", err)
	}
	if store.RuntimeSQL == nil || store.RuntimePath != runtimeSidecarPathForSQLite(dbPath) {
		t.Fatalf("bootstrap should open Go runtime sidecar: runtimePath=%q", store.RuntimePath)
	}
	assertTableMissing(t, store.SQL, goMigrationJournalTable)
	journal := readGoMigrationJournalForTest(t, store.RuntimeSQL)
	if journal.Status != "compatible" || journal.ExpectedCount != len(embeddedDrizzleMigrationSnapshot) {
		t.Fatalf("unexpected Go migration journal after bootstrap: %#v", journal)
	}
	for _, expected := range canonicalFTSObjects {
		ok, err := ftsObjectReady(context.Background(), store.SQL, expected)
		if err != nil {
			t.Fatalf("inspect bootstrapped FTS table %s: %v", expected.Table, err)
		}
		if !ok {
			t.Fatalf("bootstrapped FTS table %s is not ready", expected.Table)
		}
	}
}

func TestOpenWithConfigFailsFastWhenPostgresDriverIsUnavailable(t *testing.T) {
	if sqlDriverRegistered((PostgresDialect{}).DriverName()) {
		t.Skip("postgres driver is registered in this build")
	}
	cfg, err := NewConfig(ConfigInput{
		Driver:      "postgres",
		PostgresDSN: "postgres://example/db",
	})
	if err != nil {
		t.Fatalf("NewConfig returned error: %v", err)
	}
	store, err := OpenWithConfig(context.Background(), cfg)
	if err == nil {
		store.Close()
		t.Fatal("OpenWithConfig should fail clearly when pgx is not linked")
	}
	if !errors.Is(err, ErrPostgresDriverUnavailable) {
		t.Fatalf("expected ErrPostgresDriverUnavailable, got %v", err)
	}
}

func TestOpenWithConfigCanSkipCanonicalChecksForSQLite(t *testing.T) {
	home := t.TempDir()
	dbPath := prepareHomeDatabase(t, home)
	sqlDB := openSQLiteFileForTest(t, dbPath)
	mustCloseTestDB(t, sqlDB)

	cfg, err := NewConfig(ConfigInput{
		SQLitePath:      dbPath,
		VerifyCanonical: Bool(false),
		TSOwnedSchema:   Bool(true),
	})
	if err != nil {
		t.Fatalf("NewConfig returned error: %v", err)
	}
	store, err := OpenWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("OpenWithConfig should skip canonical checks when disabled: %v", err)
	}
	defer store.Close()
	if store.Engine != EngineSQLite || store.Dialect.Name() != EngineSQLite || store.Path != dbPath {
		t.Fatalf("unexpected store metadata: %#v", store)
	}
	assertTableMissing(t, store.SQL, goMigrationJournalTable)
}

func TestOpenDoesNotWriteJournalWhenCanonicalSchemaFails(t *testing.T) {
	home := t.TempDir()
	dbPath := prepareHomeDatabase(t, home)
	sqlDB := openSQLiteFileForTest(t, dbPath)
	if _, err := sqlDB.Exec(`CREATE TABLE projects (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatalf("create incomplete projects table: %v", err)
	}
	createDrizzleMigrationsTable(t, sqlDB, embeddedDrizzleMigrationSnapshot, nil)
	mustCloseTestDB(t, sqlDB)
	t.Setenv("HOME", home)

	store, err := Open(context.Background())
	if err == nil {
		store.Close()
		t.Fatal("Open should fail when canonical schema is incomplete")
	}
	if !errors.Is(err, ErrCanonicalSchemaMismatch) {
		t.Fatalf("expected ErrCanonicalSchemaMismatch, got %v", err)
	}
	assertFileDBTableMissing(t, dbPath, goMigrationJournalTable)
}

func TestOpenDoesNotWriteJournalWhenDrizzleJournalFails(t *testing.T) {
	home := t.TempDir()
	dbPath := prepareHomeDatabase(t, home)
	sqlDB := openSQLiteFileForTest(t, dbPath)
	createCanonicalSchemaTables(t, sqlDB)
	entries := embeddedDrizzleMigrationSnapshot[:len(embeddedDrizzleMigrationSnapshot)-1]
	createDrizzleMigrationsTable(t, sqlDB, entries, nil)
	mustCloseTestDB(t, sqlDB)
	t.Setenv("HOME", home)

	store, err := Open(context.Background())
	if err == nil {
		store.Close()
		t.Fatal("Open should fail when expected Drizzle migrations are missing")
	}
	if !errors.Is(err, ErrMigrationCompatibilityMismatch) {
		t.Fatalf("expected ErrMigrationCompatibilityMismatch, got %v", err)
	}
	assertFileDBTableMissing(t, dbPath, goMigrationJournalTable)
}

func TestOpenWithCompatibleDBWritesGoJournalOnlyToRuntimeSidecar(t *testing.T) {
	home := t.TempDir()
	dbPath := prepareHomeDatabase(t, home)
	sqlDB := openSQLiteFileForTest(t, dbPath)
	createCanonicalSchemaTables(t, sqlDB)
	createDrizzleMigrationsTable(t, sqlDB, embeddedDrizzleMigrationSnapshot, nil)
	beforeObjects := schemaObjectSet(t, sqlDB)
	projectColumnsBefore := tableColumnNamesForTest(t, sqlDB, "projects")
	narratorColumnsBefore := tableColumnNamesForTest(t, sqlDB, "narrators")
	mustCloseTestDB(t, sqlDB)
	t.Setenv("HOME", home)

	store, err := Open(context.Background())
	if err != nil {
		t.Fatalf("Open returned error for compatible database: %v", err)
	}
	defer store.Close()

	afterObjects := schemaObjectSet(t, store.SQL)
	newObjects := []string{}
	for name := range afterObjects {
		if _, ok := beforeObjects[name]; !ok {
			newObjects = append(newObjects, name)
		}
	}
	sort.Strings(newObjects)
	if len(newObjects) != 0 {
		t.Fatalf("Open should not add Go-only objects to the shared DB, got %#v", newObjects)
	}
	assertStringSlicesEqual(t, projectColumnsBefore, tableColumnNamesForTest(t, store.SQL, "projects"))
	assertStringSlicesEqual(t, narratorColumnsBefore, tableColumnNamesForTest(t, store.SQL, "narrators"))
	journal := readGoMigrationJournalForTest(t, store.RuntimeSQL)
	if journal.Status != "compatible" {
		t.Fatalf("journal status = %q, want compatible", journal.Status)
	}
}

func prepareHomeDatabase(t *testing.T, home string) string {
	t.Helper()
	dir := filepath.Join(home, ".narrafork")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create temp NarraFork dir: %v", err)
	}
	return filepath.Join(dir, "narrafork.db")
}

func openSQLiteFileForTest(t *testing.T, path string) *sql.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open sqlite test file: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	if _, err := sqlDB.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		_ = sqlDB.Close()
		t.Fatalf("enable foreign keys: %v", err)
	}
	return sqlDB
}

func mustCloseTestDB(t *testing.T, sqlDB *sql.DB) {
	t.Helper()
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close sqlite test DB: %v", err)
	}
}

func assertFileDBTableMissing(t *testing.T, dbPath string, table string) {
	t.Helper()
	sqlDB := openSQLiteFileForTest(t, dbPath)
	defer sqlDB.Close()
	assertTableMissing(t, sqlDB, table)
}
