package misccompat

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

// TestRulerProjectChaptersNarratorPrimaryOnlyMatchesTS verifies the narrator
// resolution in rulerProjectChapters mirrors server/routes/ruler.ts, which
// selects narrators with variant = "primary" ONLY (no type='primary' fallback
// and no fallback to an arbitrary narrator). A chapter whose only narrator is a
// non-primary (e.g. a subagent) must therefore resolve to an empty narratorId /
// narratorStatus, matching TS returning null.
func TestRulerProjectChaptersNarratorPrimaryOnlyMatchesTS(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	stmts := []string{
		`CREATE TABLE projects (id TEXT PRIMARY KEY, git_path TEXT, default_branch TEXT)`,
		`CREATE TABLE chapters (
			id TEXT PRIMARY KEY, project_id TEXT, title TEXT, status TEXT, branch TEXT,
			worktree_path TEXT, role TEXT, parent_chapter_id TEXT, start_commit_sha TEXT,
			merge_commit_sha TEXT, head_commit_sha TEXT, anchor_commit_sha TEXT,
			axis_offset REAL, cross_offset REAL, panel_width REAL, panel_height REAL,
			color TEXT, review_source_chapter_id TEXT, review_status TEXT, is_root INTEGER,
			created_at TEXT, updated_at TEXT
		)`,
		`CREATE TABLE narrators (id TEXT PRIMARY KEY, chapter_id TEXT, variant TEXT, type TEXT, status TEXT, substatus TEXT, created_at TEXT)`,
		`INSERT INTO projects (id, git_path, default_branch) VALUES ('p1', NULL, 'main')`,
		`INSERT INTO chapters (id, project_id, title, status, branch, role, is_root, created_at, updated_at) VALUES
			('c-prim', 'p1', 'Primary', 'active', 'feat1', 'branch', 0, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z'),
			('c-subonly', 'p1', 'SubOnly', 'active', 'feat2', 'branch', 0, '2026-01-02T00:00:00Z', '2026-01-02T00:00:00Z'),
			('c-typeprim', 'p1', 'TypePrimaryNoVariant', 'active', 'feat3', 'branch', 0, '2026-01-03T00:00:00Z', '2026-01-03T00:00:00Z')`,
		// c-prim: a real primary narrator (+ an earlier subagent that must NOT win).
		`INSERT INTO narrators (id, chapter_id, variant, type, status, substatus, created_at) VALUES
			('n-sub-early', 'c-prim', 'subagent:review', 'subagent', 'working', '[]', '2026-01-01T00:00:00Z'),
			('n-prim', 'c-prim', 'primary', 'primary', 'idle', '["unread"]', '2026-01-01T00:01:00Z'),
			('n-subonly', 'c-subonly', 'subagent:explore', 'subagent', 'working', '[]', '2026-01-02T00:00:00Z'),
			('n-typeprim', 'c-typeprim', 'subagent:general', 'primary', 'working', '[]', '2026-01-03T00:00:00Z')`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("schema %q: %v", s, err)
		}
	}

	h := NewHandler(db)
	items, err := h.rulerProjectChapters(context.Background(), "p1")
	if err != nil {
		t.Fatalf("rulerProjectChapters: %v", err)
	}
	byID := map[string]map[string]any{}
	for _, it := range items {
		byID[stringMap(it, "id", "")] = it
	}

	// c-prim resolves to the variant=primary narrator even though an earlier
	// subagent exists.
	prim := byID["c-prim"]
	if prim == nil || stringMap(prim, "narratorId", "") != "n-prim" {
		t.Fatalf("c-prim should resolve to n-prim, got %#v", prim)
	}
	if stringMap(prim, "narratorStatus", "") != "unread" {
		t.Fatalf("c-prim narratorStatus = %q, want unread", stringMap(prim, "narratorStatus", ""))
	}

	// c-subonly has only a subagent narrator -> must be empty (TS returns null).
	sub := byID["c-subonly"]
	if sub == nil {
		t.Fatalf("c-subonly missing")
	}
	if got := stringMap(sub, "narratorId", ""); got != "" {
		t.Fatalf("c-subonly narratorId = %q, want empty (primary-only, no any-narrator fallback)", got)
	}
	if got := stringMap(sub, "narratorStatus", ""); got != "" {
		t.Fatalf("c-subonly narratorStatus = %q, want empty", got)
	}

	// c-typeprim has type='primary' but variant != 'primary'. TS filters on
	// variant only, so this must NOT resolve (no type='primary' fallback).
	tp := byID["c-typeprim"]
	if tp == nil {
		t.Fatalf("c-typeprim missing")
	}
	if got := stringMap(tp, "narratorId", ""); got != "" {
		t.Fatalf("c-typeprim narratorId = %q, want empty (type='primary' must NOT be a fallback)", got)
	}
}
