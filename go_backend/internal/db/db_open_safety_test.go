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

func TestOpenRefusesMissingDatabaseAndDoesNotCreateIt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	store, err := Open(context.Background())
	if err == nil {
		store.Close()
		t.Fatal("Open should fail when the canonical TS database does not exist")
	}
	if !strings.Contains(err.Error(), "canonical TypeScript database does not exist") {
		t.Fatalf("expected missing canonical DB error, got %q", err.Error())
	}
	if _, statErr := os.Stat(filepath.Join(home, ".narrafork", "narrafork.db")); !os.IsNotExist(statErr) {
		t.Fatalf("Open should not create narrafork.db; stat error = %v", statErr)
	}
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

func TestOpenWithCompatibleDBOnlyAddsGoJournal(t *testing.T) {
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
	if strings.Join(newObjects, ",") != goMigrationJournalTable {
		t.Fatalf("Open should only add %s, got %#v", goMigrationJournalTable, newObjects)
	}
	assertStringSlicesEqual(t, projectColumnsBefore, tableColumnNamesForTest(t, store.SQL, "projects"))
	assertStringSlicesEqual(t, narratorColumnsBefore, tableColumnNamesForTest(t, store.SQL, "narrators"))
	journal := readGoMigrationJournalForTest(t, store.SQL)
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
