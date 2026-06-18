package projects

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

// TestServiceListFiltersByStatusAndOrdersByUpdatedAt covers Service.List which
// backs GET /api/projects?status=...: it filters to active/archived when those
// statuses are given, returns all projects otherwise, and orders by updated_at
// DESC. This filtering/ordering logic previously had no focused test.
func TestServiceListFiltersByStatusAndOrdersByUpdatedAt(t *testing.T) {
	ctx := context.Background()
	sqlDB := openProjectListTestDB(t)
	defer sqlDB.Close()

	// Three projects: two active (different updated_at), one archived.
	seed := []struct {
		id, status, updatedAt string
	}{
		{"p-old-active", "active", "2026-01-01T00:00:00Z"},
		{"p-new-active", "active", "2026-03-01T00:00:00Z"},
		{"p-archived", "archived", "2026-02-01T00:00:00Z"},
	}
	for _, s := range seed {
		if _, err := sqlDB.ExecContext(ctx,
			`INSERT INTO projects (id, name, status, flow_mode, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
			s.id, s.id, s.status, "classic", s.updatedAt, s.updatedAt); err != nil {
			t.Fatalf("seed %s: %v", s.id, err)
		}
	}

	svc := &Service{DB: sqlDB}

	ids := func(projects []Project) []string {
		out := make([]string, 0, len(projects))
		for _, p := range projects {
			out = append(out, p.ID)
		}
		return out
	}

	// status="active" returns only active projects, newest first.
	active, err := svc.List(ctx, "active")
	if err != nil {
		t.Fatalf("list active: %v", err)
	}
	if got := ids(active); len(got) != 2 || got[0] != "p-new-active" || got[1] != "p-old-active" {
		t.Fatalf("active filter/order wrong: %#v", got)
	}

	// status="archived" returns only the archived project.
	archived, err := svc.List(ctx, "archived")
	if err != nil {
		t.Fatalf("list archived: %v", err)
	}
	if got := ids(archived); len(got) != 1 || got[0] != "p-archived" {
		t.Fatalf("archived filter wrong: %#v", got)
	}

	// Empty status returns all three, ordered by updated_at DESC.
	all, err := svc.List(ctx, "")
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if got := ids(all); len(got) != 3 || got[0] != "p-new-active" || got[1] != "p-archived" || got[2] != "p-old-active" {
		t.Fatalf("all list/order wrong: %#v", got)
	}

	// An unrecognized status value is treated as "all" (no WHERE clause).
	unknown, err := svc.List(ctx, "bogus")
	if err != nil {
		t.Fatalf("list unknown status: %v", err)
	}
	if len(unknown) != 3 {
		t.Fatalf("unknown status should return all 3, got %d", len(unknown))
	}
}

func openProjectListTestDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if _, err := sqlDB.Exec(`CREATE TABLE projects (id TEXT PRIMARY KEY, name TEXT, description TEXT, status TEXT, flow_mode TEXT, git_path TEXT, remote_url TEXT, default_branch TEXT, startup_script TEXT, copy_files TEXT, chapter_settings TEXT, proxy_domain TEXT, created_at TEXT, updated_at TEXT)`); err != nil {
		sqlDB.Close()
		t.Fatalf("create projects table: %v", err)
	}
	return sqlDB
}
