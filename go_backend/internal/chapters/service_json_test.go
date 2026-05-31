package chapters

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"narrafork-go/internal/httpx"

	_ "modernc.org/sqlite"
)

func TestChapterJSONFieldsResponseDecodeObjects(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sqlDB.Close()
	_, err = sqlDB.Exec(`CREATE TABLE chapters (
		id TEXT PRIMARY KEY, project_id TEXT, title TEXT, description TEXT, status TEXT, role TEXT,
		branch TEXT, worktree_path TEXT, base_branch TEXT, parent_chapter_id TEXT, fork_point TEXT,
		merged_into_chapter_id TEXT, merge_commit_sha TEXT, merge_strategy TEXT, pre_merge_target_sha TEXT,
		container_config TEXT, exploration_group_id TEXT, is_root INTEGER, head_commit_sha TEXT,
		start_commit_sha TEXT, commit_count INTEGER, color TEXT, group_label TEXT, pinned INTEGER,
		anchor_commit_sha TEXT, axis_offset REAL, cross_offset REAL, panel_expanded INTEGER,
		panel_width REAL, panel_height REAL, review_source_chapter_id TEXT, review_status TEXT,
		last_accessed_at TEXT, created_at TEXT, updated_at TEXT
	)`)
	if err != nil {
		t.Fatalf("create chapters table: %v", err)
	}
	_, err = sqlDB.Exec(`INSERT INTO chapters (
		id, project_id, title, status, role, branch, base_branch, fork_point, container_config,
		is_root, commit_count, pinned, axis_offset, cross_offset, panel_expanded, created_at, updated_at
	) VALUES ('c1', 'p1', 'Chapter', 'active', 'branch', 'chapter/test', 'main', ?, ?, 0, 1, 0, 2, 3, 0, 'now', 'now')`, `{"commitSha":"abc123"}`, `{"composeFile":"compose.yml","services":["app"]}`)
	if err != nil {
		t.Fatalf("insert chapter: %v", err)
	}

	chapter, err := (&Service{DB: sqlDB}).Get(context.Background(), "c1")
	if err != nil {
		t.Fatalf("get chapter: %v", err)
	}
	if _, ok := chapter.ForkPoint.(map[string]any); !ok {
		t.Fatalf("forkPoint = %#v, want object", chapter.ForkPoint)
	}
	if _, ok := chapter.ContainerConfig.(map[string]any); !ok {
		t.Fatalf("containerConfig = %#v, want object", chapter.ContainerConfig)
	}
	encoded, err := json.Marshal(chapter)
	if err != nil {
		t.Fatalf("marshal chapter: %v", err)
	}
	body := string(encoded)
	if strings.Contains(body, `"forkPoint":"`) || strings.Contains(body, `"containerConfig":"`) {
		t.Fatalf("chapter JSON fields encoded as strings: %s", body)
	}
}

func TestLegacyStringForkPointResponseCoercesToObject(t *testing.T) {
	got := decodeForkPoint(sql.NullString{Valid: true, String: "abc123"})
	forkPoint, ok := got.(map[string]any)
	if !ok || forkPoint["commitSha"] != "abc123" {
		t.Fatalf("legacy fork point = %#v, want commitSha object", got)
	}
}

func TestChapterUpdateRejectsRootStatusAndRoleChanges(t *testing.T) {
	sqlDB := openChapterUpdateInvariantDB(t)
	defer sqlDB.Close()
	service := &Service{DB: sqlDB}

	cases := []struct {
		name string
		data map[string]any
		want string
	}{
		{name: "root status", data: map[string]any{"status": "dormant"}, want: "Cannot change root chapter status"},
		{name: "root role", data: map[string]any{"role": "branch"}, want: "Cannot change root chapter role"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := service.Update(context.Background(), "root", tc.data)
			assertChapterValidationError(t, err, tc.want)
		})
	}

	chapter, err := service.Get(context.Background(), "root")
	if err != nil {
		t.Fatalf("get root chapter: %v", err)
	}
	if chapter.Status != "active" || chapter.Role != "trunk" {
		t.Fatalf("root chapter mutated despite validation: status=%s role=%s", chapter.Status, chapter.Role)
	}
}

func TestChapterUpdateRejectsNonRootTrunkRole(t *testing.T) {
	sqlDB := openChapterUpdateInvariantDB(t)
	defer sqlDB.Close()
	service := &Service{DB: sqlDB}

	_, err := service.Update(context.Background(), "branch", map[string]any{"role": "trunk"})
	assertChapterValidationError(t, err, "Trunk role is reserved for the root chapter")

	chapter, err := service.Get(context.Background(), "branch")
	if err != nil {
		t.Fatalf("get branch chapter: %v", err)
	}
	if chapter.Role != "branch" {
		t.Fatalf("branch role mutated despite validation: %s", chapter.Role)
	}
}

func TestChapterUpdateAllowsRootTitleAndBranchRoleChanges(t *testing.T) {
	sqlDB := openChapterUpdateInvariantDB(t)
	defer sqlDB.Close()
	service := &Service{DB: sqlDB}

	root, err := service.Update(context.Background(), "root", map[string]any{"title": "Renamed root"})
	if err != nil {
		t.Fatalf("update root title: %v", err)
	}
	if root.Title != "Renamed root" || root.Status != "active" || root.Role != "trunk" {
		t.Fatalf("unexpected root update result: %#v", root)
	}

	branch, err := service.Update(context.Background(), "branch", map[string]any{"role": "exploration"})
	if err != nil {
		t.Fatalf("update branch role: %v", err)
	}
	if branch.Role != "exploration" {
		t.Fatalf("branch role = %s, want exploration", branch.Role)
	}
}

func openChapterUpdateInvariantDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	_, err = sqlDB.Exec(`CREATE TABLE chapters (
		id TEXT PRIMARY KEY, project_id TEXT, title TEXT, description TEXT, status TEXT, role TEXT,
		branch TEXT, worktree_path TEXT, base_branch TEXT, parent_chapter_id TEXT, fork_point TEXT,
		merged_into_chapter_id TEXT, merge_commit_sha TEXT, merge_strategy TEXT, pre_merge_target_sha TEXT,
		container_config TEXT, exploration_group_id TEXT, is_root INTEGER, head_commit_sha TEXT,
		start_commit_sha TEXT, commit_count INTEGER, color TEXT, group_label TEXT, pinned INTEGER,
		anchor_commit_sha TEXT, axis_offset REAL, cross_offset REAL, panel_expanded INTEGER,
		panel_width REAL, panel_height REAL, review_source_chapter_id TEXT, review_status TEXT,
		last_accessed_at TEXT, created_at TEXT, updated_at TEXT
	);
	INSERT INTO chapters (
		id, project_id, title, status, role, branch, base_branch, is_root, commit_count,
		pinned, axis_offset, cross_offset, panel_expanded, created_at, updated_at
	) VALUES
		('root', 'p1', 'Root', 'active', 'trunk', 'main', 'main', 1, 1, 0, 0, 0, 0, 'now', 'now'),
		('branch', 'p1', 'Branch', 'active', 'branch', 'chapter/branch', 'main', 0, 1, 0, 0, 0, 0, 'now', 'now');`)
	if err != nil {
		sqlDB.Close()
		t.Fatalf("create chapter update invariant db: %v", err)
	}
	return sqlDB
}

func assertChapterValidationError(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected validation error %q", want)
	}
	var appErr *httpx.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("expected AppError, got %T %v", err, err)
	}
	if appErr.Code != "VALIDATION_ERROR" || appErr.Message != want {
		t.Fatalf("unexpected validation error code/message: %s %q", appErr.Code, appErr.Message)
	}
}
