package narrators

import (
	"context"
	"database/sql"
	"testing"
)

func TestCopyBrowserSessionStorageCopiesMainToRuntimeAndPreservesSource(t *testing.T) {
	src := openBrowserSessionSyncTestDB(t)
	defer src.Close()
	dst := openBrowserSessionSyncTestDB(t)
	defer dst.Close()

	createBrowserSessionsTestTable(t, src)
	createBrowserSessionsTestTable(t, dst)
	mustInsertBrowserSessionRow(t, src, "main-session", "active", "main-title")
	mustInsertBrowserSessionRow(t, dst, "runtime-old", "closed", "old-title")

	count, err := CopyBrowserSessionStorage(context.Background(), src, dst)
	if err != nil {
		t.Fatalf("copy browser sessions: %v", err)
	}
	if count != 1 {
		t.Fatalf("copied rows = %d, want 1", count)
	}
	assertBrowserSessionRow(t, src, "main-session", "active", "main-title")
	assertBrowserSessionRowMissing(t, src, "runtime-old")
	assertBrowserSessionRow(t, dst, "main-session", "active", "main-title")
	assertBrowserSessionRowMissing(t, dst, "runtime-old")
}

func TestCopyBrowserSessionStorageCopiesRuntimeToMainForRollback(t *testing.T) {
	runtimeDB := openBrowserSessionSyncTestDB(t)
	defer runtimeDB.Close()
	mainDB := openBrowserSessionSyncTestDB(t)
	defer mainDB.Close()

	createBrowserSessionsTestTable(t, runtimeDB)
	createBrowserSessionsTestTable(t, mainDB)
	mustInsertBrowserSessionRow(t, runtimeDB, "runtime-session", "active", "runtime-title")
	mustInsertBrowserSessionRow(t, mainDB, "main-old", "closed", "old-title")

	count, err := CopyBrowserSessionStorage(context.Background(), runtimeDB, mainDB)
	if err != nil {
		t.Fatalf("rollback browser sessions: %v", err)
	}
	if count != 1 {
		t.Fatalf("copied rows = %d, want 1", count)
	}
	assertBrowserSessionRow(t, runtimeDB, "runtime-session", "active", "runtime-title")
	assertBrowserSessionRowMissing(t, runtimeDB, "main-old")
	assertBrowserSessionRow(t, mainDB, "runtime-session", "active", "runtime-title")
	assertBrowserSessionRowMissing(t, mainDB, "main-old")
}

func openBrowserSessionSyncTestDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	return sqlDB
}

func mustInsertBrowserSessionRow(t *testing.T, sqlDB *sql.DB, id, status, title string) {
	t.Helper()
	if _, err := sqlDB.Exec(`INSERT INTO narrator_browser_sessions (id, narrator_id, status, url, title, ttl_seconds, expires_at, created_at, updated_at) VALUES (?, 'nar-sync', ?, 'https://example.test', ?, 60, '2999-01-01T00:00:00Z', '2026-01-01T00:00:00Z', '2026-01-01T00:00:01Z')`, id, status, title); err != nil {
		t.Fatalf("insert browser session row %s: %v", id, err)
	}
}

func assertBrowserSessionRow(t *testing.T, sqlDB *sql.DB, id, status, title string) {
	t.Helper()
	var gotStatus, gotTitle string
	if err := sqlDB.QueryRow(`SELECT status, title FROM narrator_browser_sessions WHERE id = ?`, id).Scan(&gotStatus, &gotTitle); err != nil {
		t.Fatalf("load browser session row %s: %v", id, err)
	}
	if gotStatus != status || gotTitle != title {
		t.Fatalf("browser session row %s = status=%q title=%q, want status=%q title=%q", id, gotStatus, gotTitle, status, title)
	}
}

func assertBrowserSessionRowMissing(t *testing.T, sqlDB *sql.DB, id string) {
	t.Helper()
	var found string
	err := sqlDB.QueryRow(`SELECT id FROM narrator_browser_sessions WHERE id = ?`, id).Scan(&found)
	if err == nil {
		t.Fatalf("expected browser session row %s to be absent", id)
	}
	if err != sql.ErrNoRows {
		t.Fatalf("query browser session row %s: %v", id, err)
	}
}
