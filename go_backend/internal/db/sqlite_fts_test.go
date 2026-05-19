package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestEnsureSQLiteFTSCreatesTablesTriggersAndSyncs(t *testing.T) {
	ctx := context.Background()
	sqlDB := openSQLiteFTSTestDB(t)
	defer sqlDB.Close()
	requireSQLiteFTS5Trigram(t, ctx, sqlDB)
	createSQLiteFTSBaseTables(t, ctx, sqlDB)

	result, err := EnsureSQLiteFTS(ctx, sqlDB, EnsureSQLiteFTSOptions{SkipUncleanShutdownRebuild: true})
	if err != nil {
		if isSQLiteFTSUnavailableError(err) {
			t.Skipf("SQLite FTS5/trigram is not available in this modernc sqlite build: %v", err)
		}
		t.Fatalf("EnsureSQLiteFTS: %v", err)
	}
	if !result.Rebuilt {
		t.Fatal("EnsureSQLiteFTS should rebuild newly created FTS indexes")
	}

	for _, expected := range canonicalFTSObjects {
		ok, err := ftsObjectReady(ctx, sqlDB, expected)
		if err != nil {
			t.Fatalf("inspect FTS table %s: %v", expected.Table, err)
		}
		if !ok {
			t.Fatalf("FTS table %s is missing trigram external-content SQL", expected.Table)
		}
	}
	for _, expected := range canonicalFTSTriggers {
		ok, err := triggerReady(ctx, sqlDB, expected)
		if err != nil {
			t.Fatalf("inspect FTS trigger %s: %v", expected.Name, err)
		}
		if !ok {
			t.Fatalf("FTS trigger %s is missing or incompatible", expected.Name)
		}
	}

	mustExecSQLiteFTSTest(t, ctx, sqlDB, `INSERT INTO chapters (id, title, description) VALUES (?, ?, ?)`, "chapter-1", "Phoenix Arc", "initial branch")
	assertSQLiteFTSCount(t, ctx, sqlDB, "chapters_fts", "pho", 1)
	mustExecSQLiteFTSTest(t, ctx, sqlDB, `UPDATE chapters SET title = ?, description = ? WHERE id = ?`, "Orion Arc", "updated branch", "chapter-1")
	assertSQLiteFTSCount(t, ctx, sqlDB, "chapters_fts", "pho", 0)
	assertSQLiteFTSCount(t, ctx, sqlDB, "chapters_fts", "ori", 1)
	mustExecSQLiteFTSTest(t, ctx, sqlDB, `DELETE FROM chapters WHERE id = ?`, "chapter-1")
	assertSQLiteFTSCount(t, ctx, sqlDB, "chapters_fts", "ori", 0)

	mustExecSQLiteFTSTest(t, ctx, sqlDB, `INSERT INTO narrators (id, title) VALUES (?, ?)`, "narrator-1", "Raven Narrator")
	assertSQLiteFTSCount(t, ctx, sqlDB, "narrators_fts", "rav", 1)
	mustExecSQLiteFTSTest(t, ctx, sqlDB, `UPDATE narrators SET title = ? WHERE id = ?`, "Lumen Narrator", "narrator-1")
	assertSQLiteFTSCount(t, ctx, sqlDB, "narrators_fts", "rav", 0)
	assertSQLiteFTSCount(t, ctx, sqlDB, "narrators_fts", "lum", 1)
	mustExecSQLiteFTSTest(t, ctx, sqlDB, `UPDATE narrators SET title = NULL WHERE id = ?`, "narrator-1")
	assertSQLiteFTSCount(t, ctx, sqlDB, "narrators_fts", "lum", 0)

	mustExecSQLiteFTSTest(t, ctx, sqlDB, `INSERT INTO narrator_messages (id, narrator_id, content_text) VALUES (?, ?, ?)`, "message-1", "narrator-1", "Nebula message body")
	assertSQLiteFTSCount(t, ctx, sqlDB, "narrator_messages_fts", "neb", 1)
	mustExecSQLiteFTSTest(t, ctx, sqlDB, `UPDATE narrator_messages SET content_text = ? WHERE id = ?`, "Quasar message body", "message-1")
	assertSQLiteFTSCount(t, ctx, sqlDB, "narrator_messages_fts", "neb", 0)
	assertSQLiteFTSCount(t, ctx, sqlDB, "narrator_messages_fts", "qua", 1)
	mustExecSQLiteFTSTest(t, ctx, sqlDB, `DELETE FROM narrator_messages WHERE id = ?`, "message-1")
	assertSQLiteFTSCount(t, ctx, sqlDB, "narrator_messages_fts", "qua", 0)
}

func TestEnsureSQLiteFTSRecreatesNonTrigramTables(t *testing.T) {
	ctx := context.Background()
	sqlDB := openSQLiteFTSTestDB(t)
	defer sqlDB.Close()
	requireSQLiteFTS5Trigram(t, ctx, sqlDB)
	createSQLiteFTSBaseTables(t, ctx, sqlDB)

	oldTables := []string{
		`CREATE VIRTUAL TABLE chapters_fts USING fts5(title, description, content='chapters', content_rowid=rowid)`,
		`CREATE VIRTUAL TABLE narrator_messages_fts USING fts5(content_text, content='narrator_messages', content_rowid=rowid)`,
		`CREATE VIRTUAL TABLE narrators_fts USING fts5(title, content='narrators', content_rowid=rowid)`,
	}
	for _, stmt := range oldTables {
		mustExecSQLiteFTSTest(t, ctx, sqlDB, stmt)
	}

	result, err := EnsureSQLiteFTS(ctx, sqlDB, EnsureSQLiteFTSOptions{SkipUncleanShutdownRebuild: true})
	if err != nil {
		if isSQLiteFTSUnavailableError(err) {
			t.Skipf("SQLite FTS5/trigram is not available in this modernc sqlite build: %v", err)
		}
		t.Fatalf("EnsureSQLiteFTS: %v", err)
	}
	if !result.Rebuilt {
		t.Fatal("EnsureSQLiteFTS should rebuild after recreating non-trigram FTS tables")
	}
	if got, want := len(result.RecreatedTables), len(sqliteFTSTableSpecs); got != want {
		t.Fatalf("recreated tables = %v, want %d tables", result.RecreatedTables, want)
	}
	for _, expected := range canonicalFTSObjects {
		ok, err := ftsObjectReady(ctx, sqlDB, expected)
		if err != nil {
			t.Fatalf("inspect recreated FTS table %s: %v", expected.Table, err)
		}
		if !ok {
			t.Fatalf("recreated FTS table %s is missing trigram external-content SQL", expected.Table)
		}
	}
}

func TestEnsureSQLiteFTSSkipsWhenBaseTablesAreMissing(t *testing.T) {
	ctx := context.Background()
	sqlDB := openSQLiteFTSTestDB(t)
	defer sqlDB.Close()

	result, err := EnsureSQLiteFTS(ctx, sqlDB, EnsureSQLiteFTSOptions{})
	if err != nil {
		t.Fatalf("EnsureSQLiteFTS without base tables: %v", err)
	}
	if result.Rebuilt || len(result.RecreatedTables) != 0 {
		t.Fatalf("EnsureSQLiteFTS result = %#v, want zero result when base tables are missing", result)
	}
}

func TestMarkSQLiteCleanShutdownSetsMarker(t *testing.T) {
	ctx := context.Background()
	sqlDB := openSQLiteFTSTestDB(t)
	defer sqlDB.Close()
	mustExecSQLiteFTSTest(t, ctx, sqlDB, `PRAGMA journal_mode = WAL`)

	if err := MarkSQLiteCleanShutdown(ctx, sqlDB); err != nil {
		t.Fatalf("MarkSQLiteCleanShutdown: %v", err)
	}
	if got := sqliteFTSTestApplicationID(t, ctx, sqlDB); got != SQLiteCleanShutdownMarker {
		t.Fatalf("application_id = %#x, want %#x", got, SQLiteCleanShutdownMarker)
	}

	createSQLiteFTSBaseTables(t, ctx, sqlDB)
	requireSQLiteFTS5Trigram(t, ctx, sqlDB)
	if _, err := EnsureSQLiteFTS(ctx, sqlDB, EnsureSQLiteFTSOptions{SkipUncleanShutdownRebuild: true}); err != nil {
		if isSQLiteFTSUnavailableError(err) {
			t.Skipf("SQLite FTS5/trigram is not available in this modernc sqlite build: %v", err)
		}
		t.Fatalf("EnsureSQLiteFTS: %v", err)
	}
	if got := sqliteFTSTestApplicationID(t, ctx, sqlDB); got != 0 {
		t.Fatalf("application_id after EnsureSQLiteFTS = %#x, want 0", got)
	}
}

func openSQLiteFTSTestDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	return sqlDB
}

func requireSQLiteFTS5Trigram(t *testing.T, ctx context.Context, sqlDB *sql.DB) {
	t.Helper()
	_, err := sqlDB.ExecContext(ctx, `CREATE VIRTUAL TABLE __narrafork_fts_probe USING fts5(body, tokenize='trigram')`)
	if err != nil {
		if isSQLiteFTSUnavailableError(err) {
			t.Skipf("SQLite FTS5/trigram is not available in this modernc sqlite build: %v", err)
		}
		t.Fatalf("probe SQLite FTS5/trigram support: %v", err)
	}
	if _, err := sqlDB.ExecContext(ctx, `DROP TABLE __narrafork_fts_probe`); err != nil {
		t.Fatalf("drop SQLite FTS5/trigram probe table: %v", err)
	}
}

func createSQLiteFTSBaseTables(t *testing.T, ctx context.Context, sqlDB *sql.DB) {
	t.Helper()
	statements := []string{
		`CREATE TABLE chapters (id TEXT PRIMARY KEY, title TEXT, description TEXT)`,
		`CREATE TABLE narrators (id TEXT PRIMARY KEY, title TEXT)`,
		`CREATE TABLE narrator_messages (id TEXT PRIMARY KEY, narrator_id TEXT, content_text TEXT)`,
	}
	for _, stmt := range statements {
		mustExecSQLiteFTSTest(t, ctx, sqlDB, stmt)
	}
}

func assertSQLiteFTSCount(t *testing.T, ctx context.Context, sqlDB *sql.DB, table string, query string, want int) {
	t.Helper()
	var got int
	stmt := fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE %s MATCH ?", quoteIdentifier(table), quoteIdentifier(table))
	if err := sqlDB.QueryRowContext(ctx, stmt, query).Scan(&got); err != nil {
		t.Fatalf("query %s MATCH %q: %v", table, query, err)
	}
	if got != want {
		t.Fatalf("%s MATCH %q count = %d, want %d", table, query, got, want)
	}
}

func mustExecSQLiteFTSTest(t *testing.T, ctx context.Context, sqlDB *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := sqlDB.ExecContext(ctx, query, args...); err != nil {
		t.Fatalf("exec sqlite statement: %v\n%s", err, query)
	}
}

func sqliteFTSTestApplicationID(t *testing.T, ctx context.Context, sqlDB *sql.DB) int {
	t.Helper()
	appID, err := sqliteApplicationID(ctx, sqlDB)
	if err != nil {
		t.Fatalf("read application_id: %v", err)
	}
	return appID
}

func isSQLiteFTSUnavailableError(err error) bool {
	if err == nil {
		return false
	}
	for err != nil {
		message := strings.ToLower(err.Error())
		if strings.Contains(message, "no such module: fts5") ||
			strings.Contains(message, "no such tokenizer") ||
			strings.Contains(message, "unknown tokenizer") ||
			strings.Contains(message, "parse error in tokenize directive") {
			return true
		}
		err = errors.Unwrap(err)
	}
	return false
}
