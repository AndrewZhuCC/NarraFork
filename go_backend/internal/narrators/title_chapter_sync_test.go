package narrators

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

// TestSyncTitleToChapter verifies the narrator->chapter title propagation
// (mirroring TS syncTitleToChapter): a chapter-bound primary narrator's title
// change updates the bound chapter's title, but only for type="primary" and only
// when the chapter title differs.
func TestSyncTitleToChapter(t *testing.T) {
	sqlDB := openTitleSyncTestDB(t)
	defer sqlDB.Close()
	mustExecTitleSync(t, sqlDB, `INSERT INTO chapters (id, title) VALUES ('c1', 'Old chapter')`)
	mustExecTitleSync(t, sqlDB, `INSERT INTO narrators (id, chapter_id, type, variant, title) VALUES ('n1', 'c1', 'primary', 'primary', 'New')`)
	service := &Service{DB: sqlDB}

	service.syncTitleToChapter(context.Background(), "n1", "New")
	if got := titleSyncChapterTitle(t, sqlDB, "c1"); got != "New" {
		t.Fatalf("chapter title should sync to narrator title, got %q", got)
	}

	// A subagent (type != primary) must NOT propagate.
	mustExecTitleSync(t, sqlDB, `INSERT INTO chapters (id, title) VALUES ('c2', 'Untouched')`)
	mustExecTitleSync(t, sqlDB, `INSERT INTO narrators (id, chapter_id, type, variant, title) VALUES ('n2', 'c2', 'subagent', 'subagent', 'Sub')`)
	service.syncTitleToChapter(context.Background(), "n2", "Sub")
	if got := titleSyncChapterTitle(t, sqlDB, "c2"); got != "Untouched" {
		t.Fatalf("subagent title must not propagate to chapter, got %q", got)
	}
}

// TestSyncTitleToNarrator verifies the chapter->narrator title propagation
// (mirroring TS syncTitleToNarrator): a chapter title change updates the
// chapter's primary-variant narrator title, skips when already in sync, and does
// nothing for a chapter with no primary narrator.
func TestSyncTitleToNarrator(t *testing.T) {
	sqlDB := openTitleSyncTestDB(t)
	defer sqlDB.Close()
	mustExecTitleSync(t, sqlDB, `INSERT INTO chapters (id, title) VALUES ('c1', 'Renamed')`)
	mustExecTitleSync(t, sqlDB, `INSERT INTO narrators (id, chapter_id, type, variant, title) VALUES ('n1', 'c1', 'primary', 'primary', 'Old narrator')`)
	// A non-primary narrator on the same chapter must not be chosen.
	mustExecTitleSync(t, sqlDB, `INSERT INTO narrators (id, chapter_id, type, variant, title) VALUES ('n1sub', 'c1', 'subagent', 'subagent', 'Sub')`)
	service := &Service{DB: sqlDB}

	service.SyncTitleToNarrator(context.Background(), "c1", "Renamed")
	if got := titleSyncNarratorTitle(t, sqlDB, "n1"); got != "Renamed" {
		t.Fatalf("primary narrator title should sync to chapter title, got %q", got)
	}
	if got := titleSyncNarratorTitle(t, sqlDB, "n1sub"); got != "Sub" {
		t.Fatalf("subagent narrator title must not change, got %q", got)
	}

	// Chapter with no primary narrator: no-op, no panic.
	mustExecTitleSync(t, sqlDB, `INSERT INTO chapters (id, title) VALUES ('c_empty', 'X')`)
	service.SyncTitleToNarrator(context.Background(), "c_empty", "X")
}

func openTitleSyncTestDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	for _, stmt := range []string{
		`CREATE TABLE narrators (id TEXT PRIMARY KEY, chapter_id TEXT, type TEXT, variant TEXT, title TEXT, traits TEXT NOT NULL DEFAULT '[]', updated_at TEXT)`,
		`CREATE TABLE chapters (id TEXT PRIMARY KEY, title TEXT, updated_at TEXT)`,
	} {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("create title-sync schema: %v", err)
		}
	}
	return sqlDB
}

func mustExecTitleSync(t *testing.T, sqlDB *sql.DB, stmt string) {
	t.Helper()
	if _, err := sqlDB.Exec(stmt); err != nil {
		t.Fatalf("exec %q: %v", stmt, err)
	}
}

func titleSyncChapterTitle(t *testing.T, sqlDB *sql.DB, chapterID string) string {
	t.Helper()
	var title sql.NullString
	if err := sqlDB.QueryRow(`SELECT title FROM chapters WHERE id = ?`, chapterID).Scan(&title); err != nil {
		t.Fatalf("read chapter title: %v", err)
	}
	return title.String
}

func titleSyncNarratorTitle(t *testing.T, sqlDB *sql.DB, narratorID string) string {
	t.Helper()
	var title sql.NullString
	if err := sqlDB.QueryRow(`SELECT title FROM narrators WHERE id = ?`, narratorID).Scan(&title); err != nil {
		t.Fatalf("read narrator title: %v", err)
	}
	return title.String
}
