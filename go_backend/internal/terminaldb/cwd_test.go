package terminaldb

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	_ "modernc.org/sqlite"
)

func TestResolveCwdMatchesTSTerminalFallbackOrder(t *testing.T) {
	sqlDB := newResolveCwdTestDB(t)

	chapterCwd, err := ResolveCwd(context.Background(), sqlDB, "chapter-worktree", "")
	if err != nil {
		t.Fatalf("chapter cwd: %v", err)
	}
	if !chapterCwd.Valid || chapterCwd.String != "/repo/.worktrees/chapter" {
		t.Fatalf("chapter cwd = %#v", chapterCwd)
	}

	narratorChapterCwd, err := ResolveCwd(context.Background(), sqlDB, "", "narrator-with-chapter")
	if err != nil {
		t.Fatalf("narrator chapter cwd: %v", err)
	}
	if !narratorChapterCwd.Valid || narratorChapterCwd.String != "/repo/.worktrees/chapter" {
		t.Fatalf("narrator with chapter cwd = %#v", narratorChapterCwd)
	}

	narratorOwnCwd, err := ResolveCwd(context.Background(), sqlDB, "", "narrator-standalone")
	if err != nil {
		t.Fatalf("standalone narrator cwd: %v", err)
	}
	if !narratorOwnCwd.Valid || narratorOwnCwd.String != "/standalone/cwd" {
		t.Fatalf("standalone narrator cwd = %#v", narratorOwnCwd)
	}

	narratorFallbackCwd, err := ResolveCwd(context.Background(), sqlDB, "", "narrator-dormant")
	if err != nil {
		t.Fatalf("dormant narrator fallback cwd: %v", err)
	}
	if !narratorFallbackCwd.Valid || narratorFallbackCwd.String != "/fallback/cwd" {
		t.Fatalf("dormant narrator fallback cwd = %#v", narratorFallbackCwd)
	}
}

func TestResolveCwdValidatesTerminalTarget(t *testing.T) {
	sqlDB := newResolveCwdTestDB(t)

	if _, err := ResolveCwd(context.Background(), sqlDB, "", ""); !errors.Is(err, ErrTargetRequired) {
		t.Fatalf("empty target err = %v", err)
	}
	if _, err := ResolveCwd(context.Background(), sqlDB, "chapter-worktree", "narrator-with-chapter"); !errors.Is(err, ErrTargetAmbiguous) {
		t.Fatalf("ambiguous target err = %v", err)
	}
	if _, err := ResolveCwd(context.Background(), sqlDB, "missing", ""); !errors.Is(err, ErrChapterNotFound) {
		t.Fatalf("missing chapter err = %v", err)
	}
	if _, err := ResolveCwd(context.Background(), sqlDB, "chapter-dormant", ""); !errors.Is(err, ErrChapterDormant) {
		t.Fatalf("dormant chapter err = %v", err)
	}
	if _, err := ResolveCwd(context.Background(), sqlDB, "", "missing"); !errors.Is(err, ErrNarratorNotFound) {
		t.Fatalf("missing narrator err = %v", err)
	}
}

func newResolveCwdTestDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	stmts := []string{
		`CREATE TABLE chapters (id TEXT PRIMARY KEY, worktree_path TEXT)`,
		`CREATE TABLE narrators (id TEXT PRIMARY KEY, chapter_id TEXT, cwd TEXT)`,
		`INSERT INTO chapters (id, worktree_path) VALUES
			('chapter-worktree', '/repo/.worktrees/chapter'),
			('chapter-dormant', NULL)`,
		`INSERT INTO narrators (id, chapter_id, cwd) VALUES
			('narrator-with-chapter', 'chapter-worktree', '/ignored/cwd'),
			('narrator-standalone', NULL, '/standalone/cwd'),
			('narrator-dormant', 'chapter-dormant', '/fallback/cwd')`,
	}
	for _, stmt := range stmts {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("exec test schema: %v\n%s", err, stmt)
		}
	}
	return sqlDB
}
