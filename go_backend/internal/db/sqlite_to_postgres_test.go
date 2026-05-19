package db

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSQLiteToPostgresDryRunReportsMissingTablesWithoutWriting(t *testing.T) {
	ctx := context.Background()
	sqlitePath := createMigrationSQLiteFixture(t, false)
	dsn := strings.TrimSpace(os.Getenv("NARRAFORK_TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("set NARRAFORK_TEST_POSTGRES_DSN to run sqlite-to-postgres dry-run contract test")
	}
	report, err := RunSQLiteToPostgres(ctx, SQLiteToPostgresOptions{
		SQLitePath:         sqlitePath,
		PostgresDSN:        dsn,
		PostgresSchema:     migrationTestSchemaName(),
		DryRun:             true,
		AllowPartialSource: true,
	})
	if err != nil {
		t.Fatalf("dry-run migration returned error: %v", err)
	}
	if !report.DryRun || report.TargetEngine != EnginePostgres || len(report.PlannedTables) == 0 {
		t.Fatalf("unexpected dry-run report: %#v", report)
	}
	if len(report.MissingSourceTables) == 0 {
		t.Fatalf("partial fixture should report missing planned tables: %#v", report)
	}
}

func TestSQLiteToPostgresCopiesFullCanonicalSetAndOptionalTablesWithLivePostgres(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("NARRAFORK_TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("set NARRAFORK_TEST_POSTGRES_DSN to run sqlite-to-postgres full live copy test")
	}
	ctx := context.Background()
	schema := migrationTestSchemaName()
	defer dropLivePostgresSchema(t, dsn, schema)
	sqlitePath := createFullMigrationSQLiteFixture(t)
	report, err := RunSQLiteToPostgres(ctx, SQLiteToPostgresOptions{
		SQLitePath:     sqlitePath,
		PostgresDSN:    dsn,
		PostgresSchema: schema,
		Verify:         true,
	})
	if err != nil {
		t.Fatalf("full migration returned error: %v", err)
	}
	if len(report.MissingSourceTables) != 0 {
		t.Fatalf("full fixture should not miss source tables: %#v", report.MissingSourceTables)
	}
	for _, table := range report.PlannedTables {
		if _, ok := report.CopiedRows[table]; !ok {
			t.Fatalf("planned table %s was not copied: %#v", table, report.CopiedRows)
		}
	}
	if !report.ConstraintsValidated {
		t.Fatalf("expected postgres constraints to be validated: %#v", report)
	}
	if report.CopiedRows["projects"] != 1 || report.VerifiedRows["projects"] != 1 {
		t.Fatalf("expected one project row in full migration: %#v", report)
	}
	if report.CopiedRows["chapters"] != 1 || report.VerifiedRows["chapters"] != 1 {
		t.Fatalf("expected one FK-backed chapter row in full migration: %#v", report)
	}
	if report.CopiedRows["narrator_browser_sessions"] != 1 || report.VerifiedRows["narrator_browser_sessions"] != 1 {
		t.Fatalf("expected optional browser session row in full migration: %#v", report)
	}
	if report.SourceTableHashes["narrator_browser_sessions"] == "" || report.SourceTableHashes["narrator_browser_sessions"] != report.TargetTableHashes["narrator_browser_sessions"] {
		t.Fatalf("expected matching optional table hashes, got source=%q target=%q", report.SourceTableHashes["narrator_browser_sessions"], report.TargetTableHashes["narrator_browser_sessions"])
	}
	assertLivePostgresProject(t, dsn, schema)
}

func TestSQLiteToPostgresCopiesAndVerifiesProjectRowsWithLivePostgres(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("NARRAFORK_TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("set NARRAFORK_TEST_POSTGRES_DSN to run sqlite-to-postgres live copy test")
	}
	ctx := context.Background()
	schema := migrationTestSchemaName()
	defer dropLivePostgresSchema(t, dsn, schema)
	sqlitePath := createMigrationSQLiteFixture(t, true)
	report, err := RunSQLiteToPostgres(ctx, SQLiteToPostgresOptions{
		SQLitePath:         sqlitePath,
		PostgresDSN:        dsn,
		PostgresSchema:     schema,
		Verify:             true,
		AllowPartialSource: true,
	})
	if err != nil {
		t.Fatalf("migration returned error: %v", err)
	}
	if report.CopiedRows["projects"] != 1 || report.VerifiedRows["projects"] != 1 {
		t.Fatalf("expected copied and verified project row, got %#v", report)
	}
	if report.SourceTableHashes["projects"] == "" || report.SourceTableHashes["projects"] != report.TargetTableHashes["projects"] {
		t.Fatalf("expected matching project hashes, got source=%q target=%q", report.SourceTableHashes["projects"], report.TargetTableHashes["projects"])
	}
	assertLivePostgresProject(t, dsn, schema)
}

func createFullMigrationSQLiteFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "full-source.db")
	sqlDB, err := sql.Open((SQLiteDialect{}).DriverName(), path)
	if err != nil {
		t.Fatalf("open full sqlite fixture: %v", err)
	}
	defer sqlDB.Close()
	for _, table := range postgresBusinessTables() {
		createMigrationFixtureTable(t, sqlDB, table)
	}
	for _, table := range postgresOptionalGoTables() {
		createMigrationFixtureTable(t, sqlDB, table)
	}
	insertProjectFixture(t, sqlDB)
	insertChapterFixture(t, sqlDB)
	insertBrowserSessionFixture(t, sqlDB)
	return path
}

func createMigrationSQLiteFixture(t *testing.T, includeProject bool) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "source.db")
	sqlDB, err := sql.Open((SQLiteDialect{}).DriverName(), path)
	if err != nil {
		t.Fatalf("open sqlite fixture: %v", err)
	}
	defer sqlDB.Close()
	createMigrationFixtureTable(t, sqlDB, canonicalSchemaHashTable{Name: "projects", Columns: canonicalMainSchema["projects"]})
	if includeProject {
		insertProjectFixture(t, sqlDB)
	}
	return path
}

func createMigrationFixtureTable(t *testing.T, sqlDB *sql.DB, table canonicalSchemaHashTable) {
	t.Helper()
	columns := []string{}
	for _, column := range table.Columns {
		columnType := "TEXT"
		if table.Name == "port_allocations" && column == "port" {
			columnType = "INTEGER"
		}
		columnDDL := (SQLiteDialect{}).QuoteIdent(column) + " " + columnType
		if postgresPrimaryKeyColumn(table.Name, column) {
			columnDDL += " PRIMARY KEY"
		}
		columns = append(columns, columnDDL)
	}
	if _, err := sqlDB.Exec(`CREATE TABLE ` + (SQLiteDialect{}).QuoteIdent(table.Name) + ` (` + strings.Join(columns, ", ") + `)`); err != nil {
		t.Fatalf("create fixture table %s: %v", table.Name, err)
	}
}

func insertProjectFixture(t *testing.T, sqlDB *sql.DB) {
	t.Helper()
	columns := canonicalMainSchema["projects"]
	quoted := make([]string, len(columns))
	placeholders := make([]string, len(columns))
	values := make([]any, len(columns))
	for i, column := range columns {
		quoted[i] = (SQLiteDialect{}).QuoteIdent(column)
		placeholders[i] = "?"
		switch column {
		case "id":
			values[i] = "project_1"
		case "name":
			values[i] = "Migrated Project"
		case "description":
			values[i] = ""
		case "status":
			values[i] = "active"
		case "flow_mode":
			values[i] = "classic"
		case "chapter_settings":
			values[i] = `{"preserve": true, "order": [1,2,3]}`
		case "created_at", "updated_at":
			values[i] = "2026-05-18T00:00:00Z"
		default:
			values[i] = nil
		}
	}
	query := `INSERT INTO projects (` + strings.Join(quoted, ", ") + `) VALUES (` + strings.Join(placeholders, ", ") + `)`
	if _, err := sqlDB.Exec(query, values...); err != nil {
		t.Fatalf("insert project fixture: %v", err)
	}
}

func dropLivePostgresSchema(t *testing.T, dsn string, schema string) {
	t.Helper()
	sqlDB, err := sql.Open((PostgresDialect{}).DriverName(), dsn)
	if err != nil {
		return
	}
	defer sqlDB.Close()
	_, _ = sqlDB.ExecContext(context.Background(), "DROP SCHEMA IF EXISTS "+(PostgresDialect{}).QuoteIdent(schema)+" CASCADE")
}

func insertChapterFixture(t *testing.T, sqlDB *sql.DB) {
	t.Helper()
	columns := canonicalMainSchema["chapters"]
	quoted := make([]string, len(columns))
	placeholders := make([]string, len(columns))
	values := make([]any, len(columns))
	for i, column := range columns {
		quoted[i] = (SQLiteDialect{}).QuoteIdent(column)
		placeholders[i] = "?"
		switch column {
		case "id":
			values[i] = "chapter_1"
		case "project_id":
			values[i] = "project_1"
		case "title":
			values[i] = "Migrated Chapter"
		case "description":
			values[i] = ""
		case "status":
			values[i] = "active"
		case "role":
			values[i] = "branch"
		case "branch":
			values[i] = "main"
		case "is_root", "pinned":
			values[i] = 0
		case "commit_count":
			values[i] = 1
		case "container_config":
			values[i] = `{"services": []}`
		case "created_at", "updated_at":
			values[i] = "2026-05-18T00:00:00Z"
		default:
			values[i] = nil
		}
	}
	query := `INSERT INTO chapters (` + strings.Join(quoted, ", ") + `) VALUES (` + strings.Join(placeholders, ", ") + `)`
	if _, err := sqlDB.Exec(query, values...); err != nil {
		t.Fatalf("insert chapter fixture: %v", err)
	}
}

func insertBrowserSessionFixture(t *testing.T, sqlDB *sql.DB) {
	t.Helper()
	columns := postgresOptionalGoTables()[0].Columns
	quoted := make([]string, len(columns))
	placeholders := make([]string, len(columns))
	values := make([]any, len(columns))
	for i, column := range columns {
		quoted[i] = (SQLiteDialect{}).QuoteIdent(column)
		placeholders[i] = "?"
		switch column {
		case "id":
			values[i] = "browser_1"
		case "narrator_id":
			values[i] = "narrator_1"
		case "status":
			values[i] = "active"
		case "metadata_json":
			values[i] = `{"source":"fixture"}`
		case "created_at", "updated_at":
			values[i] = "2026-05-18T00:00:00Z"
		default:
			values[i] = nil
		}
	}
	query := `INSERT INTO narrator_browser_sessions (` + strings.Join(quoted, ", ") + `) VALUES (` + strings.Join(placeholders, ", ") + `)`
	if _, err := sqlDB.Exec(query, values...); err != nil {
		t.Fatalf("insert browser session fixture: %v", err)
	}
}

func assertLivePostgresProject(t *testing.T, dsn string, schema string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	sqlDB, err := sql.Open((PostgresDialect{}).DriverName(), dsn)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	defer sqlDB.Close()
	if _, err := sqlDB.ExecContext(ctx, "SET search_path TO "+(PostgresDialect{}).QuoteIdent(schema)); err != nil {
		t.Fatalf("set search_path: %v", err)
	}
	var name string
	if err := sqlDB.QueryRowContext(ctx, `SELECT name FROM projects WHERE id = $1`, "project_1").Scan(&name); err != nil {
		t.Fatalf("query migrated project: %v", err)
	}
	if name != "Migrated Project" {
		t.Fatalf("migrated project name = %q", name)
	}
}

func migrationTestSchemaName() string {
	return "narrafork_migration_" + strings.ReplaceAll(time.Now().UTC().Format("20060102150405.000000000"), ".", "")
}
