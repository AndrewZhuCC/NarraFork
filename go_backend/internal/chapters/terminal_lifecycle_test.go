package chapters

import (
	"context"
	"database/sql"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	_ "modernc.org/sqlite"
)

type recordingTerminalKiller struct {
	mu     sync.Mutex
	killed []string
}

func (r *recordingTerminalKiller) Kill(_ context.Context, terminalID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.killed = append(r.killed, terminalID)
	return nil
}

func TestKillAndDeleteChapterTerminalsCleansRunningOrphanAndExitedRows(t *testing.T) {
	sqlDB := newChapterTerminalLifecycleDB(t)
	insertChapterTerminalLifecycleRow(t, sqlDB, "term-running", "chapter-1", "running")
	insertChapterTerminalLifecycleRow(t, sqlDB, "term-orphan", "chapter-1", "orphan")
	insertChapterTerminalLifecycleRow(t, sqlDB, "term-exited", "chapter-1", "exited")
	insertChapterTerminalLifecycleRow(t, sqlDB, "term-other", "chapter-2", "running")
	_, _ = sqlDB.Exec(`INSERT INTO terminal_tabs (id, chapter_id) VALUES ('tab-1', 'chapter-1'), ('tab-2', 'chapter-2')`)
	_, _ = sqlDB.Exec(`INSERT INTO terminal_view_state (id, chapter_id) VALUES ('view-1', 'chapter-1'), ('view-2', 'chapter-2')`)

	killer := &recordingTerminalKiller{}
	service := &Service{DB: sqlDB, TerminalRuntime: killer}
	if err := service.killAndDeleteChapterTerminals(context.Background(), "chapter-1"); err != nil {
		t.Fatalf("killAndDeleteChapterTerminals: %v", err)
	}

	killer.mu.Lock()
	killed := append([]string(nil), killer.killed...)
	killer.mu.Unlock()
	sort.Strings(killed)
	wantKilled := []string{"term-exited", "term-orphan", "term-running"}
	if !reflect.DeepEqual(killed, wantKilled) {
		t.Fatalf("killed terminals = %#v, want %#v", killed, wantKilled)
	}
	assertChapterTerminalCount(t, sqlDB, `SELECT COUNT(*) FROM terminals WHERE chapter_id = 'chapter-1'`, 0)
	assertChapterTerminalCount(t, sqlDB, `SELECT COUNT(*) FROM terminal_tabs WHERE chapter_id = 'chapter-1'`, 0)
	assertChapterTerminalCount(t, sqlDB, `SELECT COUNT(*) FROM terminal_view_state WHERE chapter_id = 'chapter-1'`, 0)
	assertChapterTerminalCount(t, sqlDB, `SELECT COUNT(*) FROM terminals WHERE chapter_id = 'chapter-2'`, 1)
	assertChapterTerminalCount(t, sqlDB, `SELECT COUNT(*) FROM terminal_tabs WHERE chapter_id = 'chapter-2'`, 1)
	assertChapterTerminalCount(t, sqlDB, `SELECT COUNT(*) FROM terminal_view_state WHERE chapter_id = 'chapter-2'`, 1)
}

func newChapterTerminalLifecycleDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", "file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	stmts := []string{
		`CREATE TABLE terminals (id TEXT PRIMARY KEY, chapter_id TEXT, status TEXT)`,
		`CREATE TABLE terminal_tabs (id TEXT PRIMARY KEY, chapter_id TEXT)`,
		`CREATE TABLE terminal_view_state (id TEXT PRIMARY KEY, chapter_id TEXT)`,
	}
	for _, stmt := range stmts {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	return sqlDB
}

func insertChapterTerminalLifecycleRow(t *testing.T, sqlDB *sql.DB, terminalID, chapterID, status string) {
	t.Helper()
	if _, err := sqlDB.Exec(`INSERT INTO terminals (id, chapter_id, status) VALUES (?, ?, ?)`, terminalID, chapterID, status); err != nil {
		t.Fatalf("insert terminal %s: %v", terminalID, err)
	}
}

func assertChapterTerminalCount(t *testing.T, sqlDB *sql.DB, query string, want int) {
	t.Helper()
	var count int
	if err := sqlDB.QueryRow(query).Scan(&count); err != nil {
		t.Fatalf("count query failed: %v", err)
	}
	if count != want {
		t.Fatalf("count for %q = %d, want %d", query, count, want)
	}
}
