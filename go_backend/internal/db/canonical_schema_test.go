package db

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strconv"
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

func TestVerifyCanonicalSchemaReportsColumnMetadataDrift(t *testing.T) {
	sqlDB := openCanonicalSchemaTestDB(t)
	defer sqlDB.Close()
	mustExecCanonical(t, sqlDB, `CREATE TABLE projects (id INTEGER PRIMARY KEY, name TEXT, flow_mode INTEGER NOT NULL DEFAULT 'ruler')`)

	failures, err := verifyCanonicalColumnMetadata(context.Background(), sqlDB)
	if err != nil {
		t.Fatalf("verify column metadata: %v", err)
	}
	assertCanonicalFailureContains(t, failures, "column projects.id type = INTEGER, want TEXT")
	assertCanonicalFailureContains(t, failures, "column projects.name notNull = false, want true")
	assertCanonicalFailureContains(t, failures, "column projects.flow_mode type = INTEGER, want TEXT")
	assertCanonicalFailureContains(t, failures, "column projects.flow_mode default = 'ruler', want 'classic'")
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

func TestVerifyCanonicalSchemaLeavesFTSDriftReadOnly(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name      string
		mutate    func(t *testing.T, sqlDB *sql.DB)
		assertion func(t *testing.T, sqlDB *sql.DB)
	}{
		{
			name: "missing fts table",
			mutate: func(t *testing.T, sqlDB *sql.DB) {
				dropChapterFTSObjectsForTest(t, sqlDB)
			},
			assertion: func(t *testing.T, sqlDB *sql.DB) {
				assertSchemaObjectMissingForTest(t, sqlDB, "table", "chapters_fts")
			},
		},
		{
			name: "non-trigram fts table",
			mutate: func(t *testing.T, sqlDB *sql.DB) {
				dropChapterFTSObjectsForTest(t, sqlDB)
				mustExecCanonical(t, sqlDB, `CREATE VIRTUAL TABLE chapters_fts USING fts5(title, description, content='chapters', content_rowid=rowid)`)
				createChapterFTSTriggers(t, sqlDB)
			},
			assertion: func(t *testing.T, sqlDB *sql.DB) {
				sqlText := schemaSQLForTest(t, ctx, sqlDB, "table", "chapters_fts")
				if strings.Contains(normalizeSQLForCompare(sqlText), "trigram") {
					t.Fatalf("VerifyCanonicalSchema should not recreate chapters_fts as trigram, SQL = %q", sqlText)
				}
			},
		},
		{
			name: "missing fts trigger",
			mutate: func(t *testing.T, sqlDB *sql.DB) {
				mustExecCanonical(t, sqlDB, `DROP TRIGGER chapters_fts_insert`)
			},
			assertion: func(t *testing.T, sqlDB *sql.DB) {
				assertSchemaObjectMissingForTest(t, sqlDB, "trigger", "chapters_fts_insert")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sqlDB := openCanonicalSchemaTestDB(t)
			defer sqlDB.Close()
			createCanonicalSchemaTables(t, sqlDB)
			setApplicationIDForTest(t, sqlDB, SQLiteCleanShutdownMarker)
			tt.mutate(t, sqlDB)
			beforeObjects := schemaObjectSet(t, sqlDB)

			err := VerifyCanonicalSchema(ctx, sqlDB)
			if !errors.Is(err, ErrCanonicalSchemaMismatch) {
				t.Fatalf("expected ErrCanonicalSchemaMismatch, got %v", err)
			}
			assertSchemaObjectSetUnchanged(t, beforeObjects, schemaObjectSet(t, sqlDB))
			tt.assertion(t, sqlDB)
			assertApplicationIDForTest(t, sqlDB, SQLiteCleanShutdownMarker)
		})
	}
}

func TestCanonicalDefaultOpenLeavesFTSDriftReadOnly(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name      string
		mutate    func(t *testing.T, sqlDB *sql.DB)
		assertion func(t *testing.T, sqlDB *sql.DB)
	}{
		{
			name: "missing fts table",
			mutate: func(t *testing.T, sqlDB *sql.DB) {
				dropChapterFTSObjectsForTest(t, sqlDB)
			},
			assertion: func(t *testing.T, sqlDB *sql.DB) {
				assertSchemaObjectMissingForTest(t, sqlDB, "table", "chapters_fts")
			},
		},
		{
			name: "non-trigram fts table",
			mutate: func(t *testing.T, sqlDB *sql.DB) {
				dropChapterFTSObjectsForTest(t, sqlDB)
				mustExecCanonical(t, sqlDB, `CREATE VIRTUAL TABLE chapters_fts USING fts5(title, description, content='chapters', content_rowid=rowid)`)
				createChapterFTSTriggers(t, sqlDB)
			},
			assertion: func(t *testing.T, sqlDB *sql.DB) {
				sqlText := schemaSQLForTest(t, ctx, sqlDB, "table", "chapters_fts")
				if strings.Contains(normalizeSQLForCompare(sqlText), "trigram") {
					t.Fatalf("Open should not recreate chapters_fts as trigram, SQL = %q", sqlText)
				}
			},
		},
		{
			name: "missing fts trigger",
			mutate: func(t *testing.T, sqlDB *sql.DB) {
				mustExecCanonical(t, sqlDB, `DROP TRIGGER chapters_fts_insert`)
			},
			assertion: func(t *testing.T, sqlDB *sql.DB) {
				assertSchemaObjectMissingForTest(t, sqlDB, "trigger", "chapters_fts_insert")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			dbPath := prepareHomeDatabase(t, home)
			sqlDB := openSQLiteFileForTest(t, dbPath)
			createCanonicalSchemaTables(t, sqlDB)
			setApplicationIDForTest(t, sqlDB, SQLiteCleanShutdownMarker)
			tt.mutate(t, sqlDB)
			beforeObjects := schemaObjectSet(t, sqlDB)
			mustCloseTestDB(t, sqlDB)
			t.Setenv("HOME", home)

			store, err := Open(ctx)
			if err == nil {
				store.Close()
				t.Fatal("Open should report canonical FTS drift instead of repairing it")
			}
			if !errors.Is(err, ErrCanonicalSchemaMismatch) {
				t.Fatalf("expected ErrCanonicalSchemaMismatch, got %v", err)
			}

			verifyDB := openSQLiteFileForTest(t, dbPath)
			defer verifyDB.Close()
			assertSchemaObjectSetUnchanged(t, beforeObjects, schemaObjectSet(t, verifyDB))
			tt.assertion(t, verifyDB)
			assertApplicationIDForTest(t, verifyDB, SQLiteCleanShutdownMarker)
		})
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

func dropChapterFTSObjectsForTest(t *testing.T, sqlDB *sql.DB) {
	t.Helper()
	for _, trigger := range []string{"chapters_fts_insert", "chapters_fts_update", "chapters_fts_delete"} {
		mustExecCanonical(t, sqlDB, `DROP TRIGGER IF EXISTS `+quoteIdentifier(trigger))
	}
	mustExecCanonical(t, sqlDB, `DROP TABLE IF EXISTS chapters_fts`)
}

func schemaSQLForTest(t *testing.T, ctx context.Context, sqlDB *sql.DB, objectType string, name string) string {
	t.Helper()
	sqlText, exists, err := schemaSQL(ctx, sqlDB, objectType, name)
	if err != nil {
		t.Fatalf("inspect %s %s: %v", objectType, name, err)
	}
	if !exists {
		t.Fatalf("expected %s %s to exist", objectType, name)
	}
	return sqlText
}

func assertSchemaObjectMissingForTest(t *testing.T, sqlDB *sql.DB, objectType string, name string) {
	t.Helper()
	_, exists, err := schemaSQL(context.Background(), sqlDB, objectType, name)
	if err != nil {
		t.Fatalf("inspect %s %s: %v", objectType, name, err)
	}
	if exists {
		t.Fatalf("Verify/Open should not create missing %s %s", objectType, name)
	}
}

func assertSchemaObjectSetUnchanged(t *testing.T, before map[string]struct{}, after map[string]struct{}) {
	t.Helper()
	if len(after) != len(before) {
		t.Fatalf("schema object count changed: before=%#v after=%#v", before, after)
	}
	for name := range before {
		if _, ok := after[name]; !ok {
			t.Fatalf("schema object %s was removed: before=%#v after=%#v", name, before, after)
		}
	}
	for name := range after {
		if _, ok := before[name]; !ok {
			t.Fatalf("schema object %s was added: before=%#v after=%#v", name, before, after)
		}
	}
}

func setApplicationIDForTest(t *testing.T, sqlDB *sql.DB, appID int) {
	t.Helper()
	if _, err := sqlDB.Exec(`PRAGMA application_id = ` + strconv.Itoa(appID)); err != nil {
		t.Fatalf("set application_id: %v", err)
	}
}

func assertApplicationIDForTest(t *testing.T, sqlDB *sql.DB, want int) {
	t.Helper()
	var got int
	if err := sqlDB.QueryRow(`PRAGMA application_id`).Scan(&got); err != nil {
		t.Fatalf("read application_id: %v", err)
	}
	if got != want {
		t.Fatalf("application_id = %#x, want %#x", got, want)
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
	metadataByTableColumn := canonicalColumnMetadataByTableColumnForTest()
	for _, table := range sortedCanonicalBaseTables() {
		columns := canonicalMainSchema[table]
		defs := make([]string, 0, len(columns))
		for i, column := range columns {
			metadata, hasMetadata := metadataByTableColumn[table+"."+column]
			columnType := "TEXT"
			if hasMetadata && metadata.Type != "" {
				columnType = metadata.Type
			}
			definition := quoteIdentifier(column) + " " + columnType
			if hasMetadata && metadata.NotNull != nil && *metadata.NotNull {
				definition += " NOT NULL"
			}
			if i == 0 {
				definition += " PRIMARY KEY"
			}
			if hasMetadata && metadata.DefaultValue != nil {
				definition += " DEFAULT " + *metadata.DefaultValue
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

func canonicalColumnMetadataByTableColumnForTest() map[string]canonicalColumnMetadataRequirement {
	metadataByColumn := map[string]canonicalColumnMetadataRequirement{}
	for _, metadata := range canonicalColumnMetadata {
		metadataByColumn[metadata.Table+"."+metadata.Column] = metadata
	}
	return metadataByColumn
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
