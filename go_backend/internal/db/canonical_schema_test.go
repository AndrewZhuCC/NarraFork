package db

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestVerifyCanonicalSchemaAcceptsRequiredReadinessObjects(t *testing.T) {
	sqlDB := openCanonicalSchemaTestDB(t)
	defer sqlDB.Close()
	createCanonicalSchemaTables(t, sqlDB)

	if err := VerifyCanonicalSchema(context.Background(), sqlDB); err != nil {
		t.Fatalf("VerifyCanonicalSchema returned error for complete schema: %v", err)
	}
}

func TestVerifyCanonicalSchemaReportsMissingTableAndColumn(t *testing.T) {
	sqlDB := openCanonicalSchemaTestDB(t)
	defer sqlDB.Close()
	createCanonicalSchemaTables(t, sqlDB)

	if _, err := sqlDB.Exec(`DROP TABLE narrators_fts`); err != nil {
		t.Fatalf("drop test table: %v", err)
	}
	if _, err := sqlDB.Exec(`DROP TABLE projects`); err != nil {
		t.Fatalf("drop projects: %v", err)
	}
	if _, err := sqlDB.Exec(`CREATE TABLE projects (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatalf("create incomplete projects: %v", err)
	}

	err := VerifyCanonicalSchema(context.Background(), sqlDB)
	if !errors.Is(err, ErrCanonicalSchemaMismatch) {
		t.Fatalf("expected ErrCanonicalSchemaMismatch, got %v", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "missing tables: narrators_fts") {
		t.Fatalf("expected missing FTS table in error, got %q", msg)
	}
	if !strings.Contains(msg, "missing columns: projects.name") {
		t.Fatalf("expected missing projects.name column in error, got %q", msg)
	}
}

func TestVerifyCanonicalSchemaReportsMissingIndex(t *testing.T) {
	sqlDB := openCanonicalSchemaTestDB(t)
	defer sqlDB.Close()
	createCanonicalSchemaTables(t, sqlDB)

	if _, err := sqlDB.Exec(`DROP INDEX idx_messages_toplevel`); err != nil {
		t.Fatalf("drop index: %v", err)
	}
	err := VerifyCanonicalSchema(context.Background(), sqlDB)
	if !errors.Is(err, ErrCanonicalSchemaMismatch) {
		t.Fatalf("expected ErrCanonicalSchemaMismatch, got %v", err)
	}
	if msg := err.Error(); !strings.Contains(msg, "missing index idx_messages_toplevel") || !strings.Contains(msg, "read-only") {
		t.Fatalf("expected clear missing-index readiness error, got %q", msg)
	}
}

func TestVerifyCanonicalSchemaReportsMissingUniqueConstraint(t *testing.T) {
	sqlDB := openCanonicalSchemaTestDB(t)
	defer sqlDB.Close()
	createCanonicalSchemaTables(t, sqlDB)

	if _, err := sqlDB.Exec(`DROP INDEX idx_chapters_project_branch`); err != nil {
		t.Fatalf("drop unique index: %v", err)
	}
	if _, err := sqlDB.Exec(`CREATE INDEX idx_chapters_project_branch ON chapters(project_id, branch)`); err != nil {
		t.Fatalf("recreate non-unique index: %v", err)
	}
	err := VerifyCanonicalSchema(context.Background(), sqlDB)
	if !errors.Is(err, ErrCanonicalSchemaMismatch) {
		t.Fatalf("expected ErrCanonicalSchemaMismatch, got %v", err)
	}
	if msg := err.Error(); !strings.Contains(msg, "idx_chapters_project_branch") || !strings.Contains(msg, "not unique") {
		t.Fatalf("expected unique-index readiness error, got %q", msg)
	}
}

func TestVerifyCanonicalSchemaReportsMissingForeignKey(t *testing.T) {
	sqlDB := openCanonicalSchemaTestDB(t)
	defer sqlDB.Close()
	createCanonicalSchemaTablesWithOptions(t, sqlDB, false)

	err := VerifyCanonicalSchema(context.Background(), sqlDB)
	if !errors.Is(err, ErrCanonicalSchemaMismatch) {
		t.Fatalf("expected ErrCanonicalSchemaMismatch, got %v", err)
	}
	if msg := err.Error(); !strings.Contains(msg, "missing FK chapters.project_id -> projects.id") {
		t.Fatalf("expected missing FK readiness error, got %q", msg)
	}
}

func TestVerifyCanonicalSchemaReportsNonTrigramFTS(t *testing.T) {
	sqlDB := openCanonicalSchemaTestDB(t)
	defer sqlDB.Close()
	createCanonicalSchemaTables(t, sqlDB)

	for _, trigger := range []string{"chapters_fts_insert", "chapters_fts_update", "chapters_fts_delete"} {
		if _, err := sqlDB.Exec(`DROP TRIGGER ` + quoteIdentifier(trigger)); err != nil {
			t.Fatalf("drop trigger %s: %v", trigger, err)
		}
	}
	if _, err := sqlDB.Exec(`DROP TABLE chapters_fts`); err != nil {
		t.Fatalf("drop FTS table: %v", err)
	}
	if _, err := sqlDB.Exec(`CREATE VIRTUAL TABLE chapters_fts USING fts5(title, description, content='chapters', content_rowid=rowid)`); err != nil {
		t.Fatalf("create non-trigram FTS table: %v", err)
	}
	createChapterFTSTriggers(t, sqlDB)

	err := VerifyCanonicalSchema(context.Background(), sqlDB)
	if !errors.Is(err, ErrCanonicalSchemaMismatch) {
		t.Fatalf("expected ErrCanonicalSchemaMismatch, got %v", err)
	}
	if msg := err.Error(); !strings.Contains(msg, "FTS table chapters_fts") || !strings.Contains(msg, "trigram") {
		t.Fatalf("expected non-trigram FTS readiness error, got %q", msg)
	}
}

func TestVerifyCanonicalSchemaReportsMissingFTSTrigger(t *testing.T) {
	sqlDB := openCanonicalSchemaTestDB(t)
	defer sqlDB.Close()
	createCanonicalSchemaTables(t, sqlDB)

	if _, err := sqlDB.Exec(`DROP TRIGGER narrators_fts_update`); err != nil {
		t.Fatalf("drop FTS trigger: %v", err)
	}
	err := VerifyCanonicalSchema(context.Background(), sqlDB)
	if !errors.Is(err, ErrCanonicalSchemaMismatch) {
		t.Fatalf("expected ErrCanonicalSchemaMismatch, got %v", err)
	}
	if msg := err.Error(); !strings.Contains(msg, "FTS trigger narrators_fts_update") {
		t.Fatalf("expected missing FTS trigger readiness error, got %q", msg)
	}
}

func TestVerifyCanonicalSchemaDoesNotRequireGoOnlyOptionalTables(t *testing.T) {
	goOnlyOptionalTables := []string{
		"custom_subagents",
		"mcp_servers",
		"narrator_browser_sessions",
	}
	for _, table := range goOnlyOptionalTables {
		if _, ok := canonicalMainSchema[table]; ok {
			t.Fatalf("Go-only optional table %q must not be required by the main DB verifier", table)
		}
	}

	sqlDB := openCanonicalSchemaTestDB(t)
	defer sqlDB.Close()
	createCanonicalSchemaTables(t, sqlDB)

	if err := VerifyCanonicalSchema(context.Background(), sqlDB); err != nil {
		t.Fatalf("VerifyCanonicalSchema should pass without Go-only optional tables: %v", err)
	}
	for _, table := range goOnlyOptionalTables {
		exists, _, err := tableColumns(context.Background(), sqlDB, table)
		if err != nil {
			t.Fatalf("inspect %s: %v", table, err)
		}
		if exists {
			t.Fatalf("test fixture unexpectedly created Go-only optional table %q", table)
		}
	}
}

func openCanonicalSchemaTestDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	if _, err := sqlDB.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		t.Fatalf("enable foreign keys: %v", err)
	}
	return sqlDB
}

func createCanonicalSchemaTables(t *testing.T, sqlDB *sql.DB) {
	t.Helper()
	createCanonicalSchemaTablesWithOptions(t, sqlDB, true)
}

func createCanonicalSchemaTablesWithOptions(t *testing.T, sqlDB *sql.DB, includeFKs bool) {
	t.Helper()
	fkByTableColumn := map[string]canonicalForeignKeyRequirement{}
	if includeFKs {
		for _, fk := range canonicalForeignKeys {
			fkByTableColumn[fk.Table+"."+fk.Column] = fk
		}
	}
	for _, table := range sortedCanonicalBaseTables() {
		columns := canonicalMainSchema[table]
		defs := make([]string, 0, len(columns))
		for i, column := range columns {
			definition := quoteIdentifier(column) + " TEXT"
			if i == 0 {
				definition += " PRIMARY KEY"
			}
			if fk, ok := fkByTableColumn[table+"."+column]; ok {
				definition += " REFERENCES " + quoteIdentifier(fk.ReferencedTable) + "(" + quoteIdentifier(fk.ReferencedColumn) + ")"
			}
			defs = append(defs, definition)
		}
		stmt := "CREATE TABLE " + quoteIdentifier(table) + " (" + strings.Join(defs, ", ") + ")"
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("create %s: %v\n%s", table, err, stmt)
		}
	}
	createCanonicalFTSTables(t, sqlDB)
	createCanonicalIndexes(t, sqlDB)
	createCanonicalFTSTriggers(t, sqlDB)
}

func sortedCanonicalBaseTables() []string {
	tables := make([]string, 0, len(canonicalMainSchema))
	for table := range canonicalMainSchema {
		if strings.HasSuffix(table, "_fts") {
			continue
		}
		tables = append(tables, table)
	}
	sort.Strings(tables)
	return tables
}

func createCanonicalIndexes(t *testing.T, sqlDB *sql.DB) {
	t.Helper()
	for _, index := range canonicalMainIndexes {
		unique := ""
		if index.Unique {
			unique = "UNIQUE "
		}
		columns := make([]string, 0, len(index.Columns))
		for _, column := range index.Columns {
			columns = append(columns, quoteIdentifier(column))
		}
		stmt := "CREATE " + unique + "INDEX " + quoteIdentifier(index.Name) + " ON " + quoteIdentifier(index.Table) + "(" + strings.Join(columns, ", ") + ")"
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("create index %s: %v\n%s", index.Name, err, stmt)
		}
	}
}

func createCanonicalFTSTables(t *testing.T, sqlDB *sql.DB) {
	t.Helper()
	statements := []string{
		`CREATE VIRTUAL TABLE chapters_fts USING fts5(title, description, content='chapters', content_rowid=rowid, tokenize='trigram')`,
		`CREATE VIRTUAL TABLE narrator_messages_fts USING fts5(content_text, content='narrator_messages', content_rowid=rowid, tokenize='trigram')`,
		`CREATE VIRTUAL TABLE narrators_fts USING fts5(title, content='narrators', content_rowid=rowid, tokenize='trigram')`,
	}
	for _, stmt := range statements {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("create FTS table: %v\n%s", err, stmt)
		}
	}
}

func createCanonicalFTSTriggers(t *testing.T, sqlDB *sql.DB) {
	t.Helper()
	createChapterFTSTriggers(t, sqlDB)
	statements := []string{
		`CREATE TRIGGER narrator_messages_fts_insert AFTER INSERT ON narrator_messages BEGIN
			INSERT INTO narrator_messages_fts(rowid, content_text)
			VALUES (NEW.rowid, NEW.content_text);
		END`,
		`CREATE TRIGGER narrator_messages_fts_update AFTER UPDATE ON narrator_messages BEGIN
			INSERT INTO narrator_messages_fts(narrator_messages_fts, rowid, content_text)
			VALUES ('delete', OLD.rowid, OLD.content_text);
			INSERT INTO narrator_messages_fts(rowid, content_text)
			VALUES (NEW.rowid, NEW.content_text);
		END`,
		`CREATE TRIGGER narrator_messages_fts_delete AFTER DELETE ON narrator_messages BEGIN
			INSERT INTO narrator_messages_fts(narrator_messages_fts, rowid, content_text)
			VALUES ('delete', OLD.rowid, OLD.content_text);
		END`,
		`CREATE TRIGGER narrators_fts_insert AFTER INSERT ON narrators
			WHEN NEW.title IS NOT NULL BEGIN
			INSERT INTO narrators_fts(rowid, title) VALUES (NEW.rowid, NEW.title);
		END`,
		`CREATE TRIGGER narrators_fts_update AFTER UPDATE OF title ON narrators
			WHEN OLD.title IS NOT NULL OR NEW.title IS NOT NULL BEGIN
			INSERT INTO narrators_fts(narrators_fts, rowid, title)
			SELECT 'delete', OLD.rowid, OLD.title WHERE OLD.title IS NOT NULL;
			INSERT OR IGNORE INTO narrators_fts(rowid, title)
			SELECT NEW.rowid, NEW.title WHERE NEW.title IS NOT NULL;
		END`,
		`CREATE TRIGGER narrators_fts_delete AFTER DELETE ON narrators
			WHEN OLD.title IS NOT NULL BEGIN
			INSERT INTO narrators_fts(narrators_fts, rowid, title)
			VALUES ('delete', OLD.rowid, OLD.title);
		END`,
	}
	for _, stmt := range statements {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("create FTS trigger: %v\n%s", err, stmt)
		}
	}
}

func createChapterFTSTriggers(t *testing.T, sqlDB *sql.DB) {
	t.Helper()
	statements := []string{
		`CREATE TRIGGER chapters_fts_insert AFTER INSERT ON chapters BEGIN
			INSERT INTO chapters_fts(rowid, title, description)
			VALUES (NEW.rowid, NEW.title, NEW.description);
		END`,
		`CREATE TRIGGER chapters_fts_update AFTER UPDATE ON chapters BEGIN
			INSERT INTO chapters_fts(chapters_fts, rowid, title, description)
			VALUES ('delete', OLD.rowid, OLD.title, OLD.description);
			INSERT INTO chapters_fts(rowid, title, description)
			VALUES (NEW.rowid, NEW.title, NEW.description);
		END`,
		`CREATE TRIGGER chapters_fts_delete AFTER DELETE ON chapters BEGIN
			INSERT INTO chapters_fts(chapters_fts, rowid, title, description)
			VALUES ('delete', OLD.rowid, OLD.title, OLD.description);
		END`,
	}
	for _, stmt := range statements {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("create chapter FTS trigger: %v\n%s", err, stmt)
		}
	}
}
