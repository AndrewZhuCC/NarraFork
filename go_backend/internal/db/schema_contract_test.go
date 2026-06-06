package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"narrafork-go/internal/compat/bunfinal"

	_ "modernc.org/sqlite"
)

func TestCanonicalSchemaContractReportFreezesTSOwnedDBMode(t *testing.T) {
	report := CanonicalSchemaContract()
	if report.Mode != "ts-owned-db-compatibility" || report.MainSchemaOwner != "typescript-drizzle" {
		t.Fatalf("unexpected DB ownership contract: %#v", report)
	}
	if report.StrategyStage != "transition" || !report.TransitionalStrategy || report.TargetMode != "pure-go-db-management" {
		t.Fatalf("SQLite contract must identify TS-owned mode as transitional: %#v", report)
	}
	if report.TargetMainSchemaOwner != "go" || report.CurrentRepairOwner != "typescript-drizzle-and-ts-fts-setup" || len(report.FutureGoResponsibilities) == 0 {
		t.Fatalf("contract should expose current TS repair ownership and future Go DB responsibilities: %#v", report)
	}
	if report.GoMainMigrations || report.GoEnsureColumns || report.GoFTSRepair || !report.ReadOnlyVerifier {
		t.Fatalf("Go must remain read-only for the main TS-owned DB during the transition: %#v", report)
	}
	if report.CanonicalSchemaHash == "" || report.ExpectedMigrationHash == "" || report.ExpectedMigrationCount == 0 || report.ExpectedLastMigration == "" {
		t.Fatalf("contract report should expose stable schema/migration hashes: %#v", report)
	}
	if report.GoJournalTable != goMigrationJournalTable || report.DrizzleJournalTable != "__drizzle_migrations" {
		t.Fatalf("unexpected migration journal contract: %#v", report)
	}
	if report.CanonicalTableCount == 0 || report.CanonicalIndexCount == 0 || report.CanonicalForeignKeyCount == 0 || report.CanonicalFTSTableCount != 3 || report.CanonicalFTSTriggerCount == 0 {
		t.Fatalf("contract report should expose canonical object counts: %#v", report)
	}
}

func TestCanonicalSchemaContractArtifactMatchesRuntime(t *testing.T) {
	data, err := bunfinal.ReadCanonicalSchemaContract()
	if err != nil {
		t.Fatalf("read bun-final canonical schema contract: %v", err)
	}
	var artifact CanonicalSchemaContractReport
	if err := json.Unmarshal(data, &artifact); err != nil {
		t.Fatalf("parse bun-final canonical schema contract: %v", err)
	}
	want := CanonicalSchemaContract()
	if !reflect.DeepEqual(artifact, want) {
		t.Fatalf("canonical schema artifact mismatch:\nartifact=%#v\nruntime=%#v", artifact, want)
	}
}

func TestEnsureSchemaDoesNotCreateMainDatabaseObjects(t *testing.T) {
	sqlDB := openCanonicalSchemaTestDB(t)
	defer sqlDB.Close()
	store := &Store{SQL: sqlDB, Path: ":memory:"}

	if err := store.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("EnsureSchema returned error: %v", err)
	}
	assertNoUserSchemaObjects(t, sqlDB)
}

func TestVerifyCanonicalSchemaIsReadOnlyOnMismatch(t *testing.T) {
	sqlDB := openCanonicalSchemaTestDB(t)
	defer sqlDB.Close()
	if _, err := sqlDB.Exec(`CREATE TABLE projects (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatalf("create incomplete projects: %v", err)
	}

	err := VerifyCanonicalSchema(context.Background(), sqlDB)
	if !errors.Is(err, ErrCanonicalSchemaMismatch) {
		t.Fatalf("expected ErrCanonicalSchemaMismatch, got %v", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "Start the TypeScript backend once") {
		t.Fatalf("expected remediation guidance, got %q", msg)
	}
	var projectTables int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'projects'`).Scan(&projectTables); err != nil {
		t.Fatalf("count projects table: %v", err)
	}
	if projectTables != 1 {
		t.Fatalf("verifier should not create or repair tables, projects table count = %d", projectTables)
	}
	var columnCount int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('projects')`).Scan(&columnCount); err != nil {
		t.Fatalf("count projects columns: %v", err)
	}
	if columnCount != 1 {
		t.Fatalf("verifier should not add missing columns, projects column count = %d", columnCount)
	}
}

func TestCanonicalReadinessDetectsIndexForeignKeyFTSAndTriggerDrift(t *testing.T) {
	ctx := context.Background()
	t.Run("missing index", func(t *testing.T) {
		sqlDB := openCanonicalSchemaTestDB(t)
		defer sqlDB.Close()
		mustExecCanonical(t, sqlDB, `CREATE TABLE chapters (id TEXT PRIMARY KEY, project_id TEXT, status TEXT)`)
		failures, err := verifyCanonicalReadinessObjects(ctx, sqlDB)
		if err != nil {
			t.Fatalf("verify readiness: %v", err)
		}
		assertCanonicalFailureContains(t, failures, "missing index idx_chapters_project")
	})
	t.Run("missing foreign key", func(t *testing.T) {
		sqlDB := openCanonicalSchemaTestDB(t)
		defer sqlDB.Close()
		mustExecCanonical(t, sqlDB, `CREATE TABLE exploration_groups (id TEXT PRIMARY KEY)`)
		mustExecCanonical(t, sqlDB, `CREATE TABLE chapters (id TEXT PRIMARY KEY, exploration_group_id TEXT)`)
		failures, err := verifyCanonicalReadinessObjects(ctx, sqlDB)
		if err != nil {
			t.Fatalf("verify readiness: %v", err)
		}
		assertCanonicalFailureContains(t, failures, "missing FK chapters.exploration_group_id")
	})
	t.Run("incompatible fts table", func(t *testing.T) {
		sqlDB := openCanonicalSchemaTestDB(t)
		defer sqlDB.Close()
		mustExecCanonical(t, sqlDB, `CREATE TABLE chapters (id TEXT PRIMARY KEY, title TEXT, description TEXT)`)
		mustExecCanonical(t, sqlDB, `CREATE VIRTUAL TABLE chapters_fts USING fts5(title, description)`) // missing TS external-content trigram contract
		failures, err := verifyCanonicalReadinessObjects(ctx, sqlDB)
		if err != nil {
			t.Fatalf("verify readiness: %v", err)
		}
		assertCanonicalFailureContains(t, failures, "FTS table chapters_fts")
	})
	t.Run("missing fts trigger", func(t *testing.T) {
		sqlDB := openCanonicalSchemaTestDB(t)
		defer sqlDB.Close()
		failures, err := verifyCanonicalReadinessObjects(ctx, sqlDB)
		if err != nil {
			t.Fatalf("verify readiness: %v", err)
		}
		assertCanonicalFailureContains(t, failures, "FTS trigger chapters_fts_insert")
	})
}

func mustExecCanonical(t *testing.T, sqlDB *sql.DB, stmt string) {
	t.Helper()
	if _, err := sqlDB.Exec(stmt); err != nil {
		t.Fatalf("exec %s: %v", stmt, err)
	}
}

func assertCanonicalFailureContains(t *testing.T, failures []string, needle string) {
	t.Helper()
	for _, failure := range failures {
		if strings.Contains(failure, needle) {
			return
		}
	}
	t.Fatalf("expected readiness failure containing %q, got %#v", needle, failures)
}

func assertNoUserSchemaObjects(t *testing.T, sqlDB *sql.DB) {
	t.Helper()
	var count int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name NOT LIKE 'sqlite_%'`).Scan(&count); err != nil {
		t.Fatalf("count schema objects: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected no schema objects, found %d", count)
	}
}
