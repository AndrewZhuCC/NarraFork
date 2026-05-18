package chapters

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

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
