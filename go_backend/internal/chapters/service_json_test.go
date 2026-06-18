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

func TestChapterUpdateValidatesZodBoundsLikeTypeScript(t *testing.T) {
	sqlDB := openChapterUpdateInvariantDB(t)
	defer sqlDB.Close()
	service := &Service{DB: sqlDB}

	cases := []struct {
		name string
		data map[string]any
		want string
	}{
		{name: "empty title", data: map[string]any{"title": "  "}, want: "title must be a non-empty string"},
		{name: "overlong title", data: map[string]any{"title": strings.Repeat("x", 201)}, want: "title must be at most 200 characters"},
		{name: "overlong description", data: map[string]any{"description": strings.Repeat("d", 2001)}, want: "description must be at most 2000 characters"},
		{name: "invalid status", data: map[string]any{"status": "archived"}, want: "status must be one of active, dormant, merged, abandoned, frozen"},
		{name: "invalid role", data: map[string]any{"role": "reviewer"}, want: "role must be one of trunk, branch, exploration, review"},
		{name: "overlong color", data: map[string]any{"color": strings.Repeat("c", 21)}, want: "color must be at most 20 characters"},
		{name: "overlong groupLabel", data: map[string]any{"groupLabel": strings.Repeat("g", 101)}, want: "groupLabel must be at most 100 characters"},
		{name: "container composeFile too long", data: map[string]any{"containerConfig": map[string]any{"composeFile": strings.Repeat("f", 501)}}, want: "containerConfig.composeFile must be at most 500 characters"},
		{name: "container empty service", data: map[string]any{"containerConfig": map[string]any{"services": []any{""}}}, want: "containerConfig.services[0] must be a non-empty string"},
		{name: "container port out of range", data: map[string]any{"containerConfig": map[string]any{"ports": []any{map[string]any{"containerPort": float64(70000), "serviceName": "web"}}}}, want: "containerConfig.ports[0].containerPort must be between 1 and 65535"},
		{name: "container port missing service", data: map[string]any{"containerConfig": map[string]any{"ports": []any{map[string]any{"containerPort": float64(8080), "serviceName": ""}}}}, want: "containerConfig.ports[0].serviceName must be a non-empty string"},
		{name: "container env non-string", data: map[string]any{"containerConfig": map[string]any{"env": map[string]any{"KEY": float64(1)}}}, want: "containerConfig.env.KEY must be a string"},
		// TS updateChapterSchema: title/description/status/role are .optional() but
		// NOT .nullable(), so an explicit null is rejected with 400 rather than
		// writing NULL to the column (color/groupLabel/containerConfig ARE nullable).
		{name: "null title", data: map[string]any{"title": nil}, want: "title must be a non-empty string"},
		{name: "null description", data: map[string]any{"description": nil}, want: "description must be at most 2000 characters"},
		{name: "null status", data: map[string]any{"status": nil}, want: "status must be one of active, dormant, merged, abandoned, frozen"},
		{name: "null role", data: map[string]any{"role": nil}, want: "role must be one of trunk, branch, exploration, review"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := service.Update(context.Background(), "branch", tc.data)
			assertChapterValidationError(t, err, tc.want)
		})
	}

	// A valid update with the full containerConfig shape and enum values passes.
	updated, err := service.Update(context.Background(), "branch", map[string]any{
		"title":       "Renamed branch",
		"description": "ok",
		"status":      "dormant",
		"role":        "exploration",
		"color":       "#abcdef",
		"groupLabel":  "Group A",
		"containerConfig": map[string]any{
			"composeFile": "docker-compose.yml",
			"services":    []any{"web", "db"},
			"ports":       []any{map[string]any{"containerPort": float64(8080), "serviceName": "web"}},
			"env":         map[string]any{"NODE_ENV": "production"},
		},
	})
	if err != nil {
		t.Fatalf("valid chapter update should pass, got %v", err)
	}
	if updated.Title != "Renamed branch" || updated.Status != "dormant" || updated.Role != "exploration" {
		t.Fatalf("unexpected valid update result: %#v", updated)
	}
}

// TestChapterUpdateNullableFieldsAcceptNullToClear confirms the nullable-vs-optional
// distinction: color/groupLabel/containerConfig are .nullable().optional() in TS,
// so an explicit null clears them (does NOT 400), while title/description/status/
// role reject null (covered by the validation table above).
func TestChapterUpdateNullableFieldsAcceptNullToClear(t *testing.T) {
	sqlDB := openChapterUpdateInvariantDB(t)
	defer sqlDB.Close()
	service := &Service{DB: sqlDB}

	// Seed non-null values, then clear them with null.
	if _, err := sqlDB.Exec(`UPDATE chapters SET color = '#abcdef', group_label = 'G', container_config = '{"composeFile":"c.yml"}' WHERE id = 'branch'`); err != nil {
		t.Fatalf("seed nullable fields: %v", err)
	}
	if _, err := service.Update(context.Background(), "branch", map[string]any{"color": nil, "groupLabel": nil, "containerConfig": nil}); err != nil {
		t.Fatalf("null should be accepted to clear nullable fields, got %v", err)
	}
	var color, groupLabel, containerConfig sql.NullString
	if err := sqlDB.QueryRow(`SELECT color, group_label, container_config FROM chapters WHERE id = 'branch'`).Scan(&color, &groupLabel, &containerConfig); err != nil {
		t.Fatalf("read cleared fields: %v", err)
	}
	if color.Valid || groupLabel.Valid || containerConfig.Valid {
		t.Fatalf("nullable fields should be cleared to NULL, got color=%#v group=%#v container=%#v", color, groupLabel, containerConfig)
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
