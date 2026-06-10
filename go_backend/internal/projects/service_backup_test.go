package projects

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestProjectBackupScopeCoversFullTSKeyTables(t *testing.T) {
	required := []string{
		"narrator_messages",
		"narrator_message_refs",
		"narrator_tool_calls",
		"narrator_sidecars",
		"narrator_file_snapshots",
		"narrator_patches",
		"narrator_goals",
		"narrator_buffered_messages",
		"background_tasks",
		"merge_sessions",
		"review_conclusions",
		"terminal_tabs",
		"terminal_view_state",
		"port_allocations",
		"container_instances",
	}
	scope := map[string]bool{}
	for _, table := range projectBackupScope() {
		scope[table] = true
	}
	for _, table := range required {
		if !scope[table] {
			t.Fatalf("project backup scope missing %s", table)
		}
	}
}

func TestProjectImportReturnsExistingProjectQueryErrors(t *testing.T) {
	ctx := context.Background()
	gitPath := t.TempDir()
	source := openProjectBackupTestDB(t)
	defer source.Close()
	seedFullScopeProject(t, source, gitPath)
	if _, err := (&Service{DB: source}).SyncBackup(ctx, "p1"); err != nil {
		t.Fatalf("seed project backup: %v", err)
	}
	target := openProjectBackupTestDB(t)
	defer target.Close()
	for _, stmt := range []string{
		`ALTER TABLE projects RENAME TO projects_real`,
		`CREATE VIEW projects AS SELECT json_extract('not-json', '$') AS id, name, git_path, default_branch, startup_script, copy_files, chapter_settings, created_at, updated_at FROM projects_real`,
	} {
		if _, err := target.Exec(stmt); err != nil {
			t.Fatalf("prepare broken projects view: %v", err)
		}
	}

	_, err := (&Service{DB: target}).ImportBackup(ctx, gitPath)
	if err == nil || !strings.Contains(err.Error(), "query existing project before import") {
		t.Fatalf("expected existing project query error, got %v", err)
	}
}

func TestProjectBackupSyncAndImportFullScope(t *testing.T) {
	ctx := context.Background()
	gitPath := t.TempDir()
	source := openProjectBackupTestDB(t)
	defer source.Close()
	seedFullScopeProject(t, source, gitPath)

	exported, err := (&Service{DB: source}).SyncBackup(ctx, "p1")
	if err != nil {
		t.Fatalf("SyncBackup: %v", err)
	}
	if exported["projectId"] != "p1" {
		t.Fatalf("exported projectId = %v, want p1", exported["projectId"])
	}

	backup, err := sql.Open("sqlite", projectDBPath(gitPath))
	if err != nil {
		t.Fatalf("open backup db: %v", err)
	}
	defer backup.Close()
	for _, table := range projectBackupTables {
		got, err := countRows(ctx, backup, table.name)
		if err != nil {
			t.Fatalf("count backup %s: %v", table.name, err)
		}
		if got != 1 {
			t.Fatalf("backup table %s count = %d, want 1", table.name, got)
		}
	}
	assertColumnValue(t, backup, "narrator_messages", "id", "m1", "command_text", "/ship")
	assertColumnValue(t, backup, "narrator_tool_calls", "id", "tc1", "result_message_id", "m1")
	assertColumnValue(t, backup, "narrator_sidecars", "id", "side1", "content", "sidecar content")
	assertColumnValue(t, backup, "terminal_view_state", "id", "view1", "panel_assignments", `{"left":"tab1"}`)
	assertColumnValue(t, backup, "container_instances", "id", "container1", "proxy_label", "web")

	target := openProjectBackupTestDB(t)
	defer target.Close()
	imported, err := (&Service{DB: target}).ImportBackup(ctx, gitPath)
	if err != nil {
		t.Fatalf("ImportBackup: %v", err)
	}
	if imported["skipped"] != false {
		t.Fatalf("import skipped = %v, want false", imported["skipped"])
	}
	for _, table := range projectBackupTables {
		got, err := countRows(ctx, target, table.name)
		if err != nil {
			t.Fatalf("count imported %s: %v", table.name, err)
		}
		if got != 1 {
			t.Fatalf("imported table %s count = %d, want 1", table.name, got)
		}
	}
	assertColumnValue(t, target, "projects", "id", "p1", "git_path", gitPath)
	assertColumnValue(t, target, "narrator_buffered_messages", "id", "buf1", "text", "queued message")
	assertColumnValue(t, target, "background_tasks", "id", "bg1", "output", "background output")
	assertColumnValue(t, target, "review_conclusions", "id", "review1", "verdict", "approve")
	assertColumnValue(t, target, "port_allocations", "port", 18080, "service_name", "web")
}

func TestProjectBackupSyncPreservesReviewAndMergeLifecycleFields(t *testing.T) {
	ctx := context.Background()
	gitPath := t.TempDir()
	source := openProjectBackupTestDB(t)
	defer source.Close()
	insertProjectBackupTestRow(t, source, "projects", map[string]any{
		"id": "p1", "name": "Project", "status": "active", "flow_mode": "classic",
		"git_path": gitPath, "default_branch": "main", "chapter_settings": `{"commands":[]}`,
	})
	insertProjectBackupTestRow(t, source, "chapters", map[string]any{
		"id": "source", "project_id": "p1", "title": "Source", "status": "merged", "role": "branch",
		"branch": "feature/source", "base_branch": "main", "merged_into_chapter_id": "target",
		"merge_commit_sha": "merge-sha", "merge_strategy": "merge", "pre_merge_target_sha": "pre-target",
		"head_commit_sha": "source-head", "commit_count": 3,
	})
	insertProjectBackupTestRow(t, source, "chapters", map[string]any{
		"id": "target", "project_id": "p1", "title": "Target", "status": "active", "role": "trunk",
		"branch": "main", "base_branch": "main", "head_commit_sha": "target-head", "commit_count": 9,
	})
	insertProjectBackupTestRow(t, source, "chapters", map[string]any{
		"id": "review", "project_id": "p1", "title": "Review: Source", "status": "active", "role": "review",
		"branch": "review/source", "base_branch": "main", "review_source_chapter_id": "source",
		"review_status": "reviewing", "head_commit_sha": "review-head",
	})
	insertProjectBackupTestRow(t, source, "chapter_edges", map[string]any{
		"id": "edge-review", "project_id": "p1", "source_id": "source", "target_id": "review",
		"type": "review", "metadata": `{"commitSha":"source-head"}`,
	})
	insertProjectBackupTestRow(t, source, "chapter_edges", map[string]any{
		"id": "edge-merge", "project_id": "p1", "source_id": "source", "target_id": "target",
		"type": "merge", "metadata": `{"commitSha":"merge-sha","strategy":"merge","preMergeTargetSha":"pre-target","status":"completed"}`,
	})

	if _, err := (&Service{DB: source}).SyncBackup(ctx, "p1"); err != nil {
		t.Fatalf("SyncBackup lifecycle fields: %v", err)
	}
	backup, err := sql.Open("sqlite", projectDBPath(gitPath))
	if err != nil {
		t.Fatalf("open backup db: %v", err)
	}
	defer backup.Close()
	assertLifecycleBackupFields(t, backup)

	target := openProjectBackupTestDB(t)
	defer target.Close()
	if _, err := (&Service{DB: target}).ImportBackup(ctx, gitPath); err != nil {
		t.Fatalf("ImportBackup lifecycle fields: %v", err)
	}
	assertLifecycleBackupFields(t, target)
}

func openProjectBackupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := ensureProjectBackupSchema(context.Background(), db); err != nil {
		db.Close()
		t.Fatalf("ensure schema: %v", err)
	}
	return db
}

func seedFullScopeProject(t *testing.T, db *sql.DB, gitPath string) {
	t.Helper()
	overrides := map[string]map[string]any{
		"projects": {
			"id": "p1", "name": "Project", "status": "active", "flow_mode": "classic",
			"git_path": gitPath, "default_branch": "main", "chapter_settings": `{"commands":[]}`,
		},
		"exploration_groups": {"id": "eg1", "project_id": "p1", "title": "Exploration", "base_chapter_id": "c1"},
		"chapters": {
			"id": "c1", "project_id": "p1", "title": "Chapter", "status": "active", "role": "branch",
			"branch": "nf/c1", "base_branch": "main", "exploration_group_id": "eg1",
		},
		"chapter_edges":   {"id": "edge1", "project_id": "p1", "source_id": "c1", "target_id": "c1", "type": "fork"},
		"chapter_commits": {"id": "commit1", "chapter_id": "c1", "sha": "abc123", "message": "initial", "authored_at": "2026-01-01T00:00:00Z", "narrator_id": "n1", "narrator_message_id": "m1"},
		"narrators": {
			"id": "n1", "chapter_id": "c1", "type": "primary", "inherit_mode": "fresh", "title": "Narrator",
			"status": "idle", "substatus": "[]", "traits": "[]", "variant": "primary",
		},
		"narrator_goals":               {"id": "goal1", "narrator_id": "n1", "objective": "finish", "status": "active"},
		"narrator_messages":            {"id": "m1", "narrator_id": "n1", "role": "user", "content_json": `[{"type":"text","text":"hi"}]`, "content_text": "hi", "command_text": "/ship", "created_by": "user1"},
		"narrator_message_refs":        {"id": "ref1", "narrator_id": "n1", "message_id": "m1", "seq": 1},
		"narrator_sidecars":            {"id": "side1", "narrator_id": "n1", "message_id": "m1", "tool_use_id": "tool1", "target": "tool_result", "source": "bash", "content": "sidecar content"},
		"narrator_tool_calls":          {"id": "tc1", "narrator_id": "n1", "message_id": "m1", "tool_use_id": "tool1", "tool_name": "Bash", "status": "success", "result_message_id": "m1"},
		"narrator_file_snapshots":      {"id": "snap1", "narrator_id": "n1", "file_path": "README.md", "original_content": "before"},
		"narrator_patches":             {"id": "patch1", "narrator_id": "n1", "message_id": "m1", "tool_use_id": "tool1", "before_hash": "before", "after_hash": "after", "files_json": `["README.md"]`},
		"narrator_buffered_messages":   {"id": "buf1", "narrator_id": "n1", "text": "queued message", "seq": 1, "images_json": "[]", "creator_json": `{}`},
		"background_tasks":             {"id": "bg1", "parent_narrator_id": "n1", "type": "bash", "status": "completed", "command": "pwd", "tool_use_id": "tool1", "output": "background output"},
		"merge_sessions":               {"id": "merge1", "target_chapter_id": "c1", "source_chapter_ids": `["c1"]`, "status": "completed"},
		"review_conclusions":           {"id": "review1", "review_chapter_id": "c1", "source_chapter_id": "c1", "verdict": "approve", "findings_json": "[]"},
		"terminals":                    {"id": "term1", "chapter_id": "c1", "narrator_id": "n1", "name": "Terminal", "status": "exited"},
		"terminal_tabs":                {"id": "tab1", "chapter_id": "c1", "narrator_id": "n1", "name": "Tab", "sort_order": 1},
		"terminal_view_state":          {"id": "view1", "user_id": "user1", "chapter_id": "c1", "narrator_id": "n1", "layout": "split-h", "active_tab_id": "tab1", "panel_assignments": `{"left":"tab1"}`},
		"container_instances":          {"id": "container1", "chapter_id": "c1", "service_name": "web", "status": "running", "host_port": 18080, "container_port": 3000, "proxy_label": "web"},
		"port_allocations":             {"port": 18080, "chapter_id": "c1", "service_name": "web"},
		"volume_snapshots":             {"id": "volume1", "project_id": "p1", "name": "Volume", "source_chapter_id": "c1", "service_name": "db", "container_path": "/data"},
		"volume_snapshot_applications": {"id": "volume-app1", "snapshot_id": "volume1", "chapter_id": "c1"},
	}
	for _, table := range projectBackupTables {
		insertProjectBackupTestRow(t, db, table.name, overrides[table.name])
	}
}

func insertProjectBackupTestRow(t *testing.T, db *sql.DB, table string, overrides map[string]any) {
	t.Helper()
	columns := projectBackupColumnNames(table)
	values := make([]any, 0, len(columns))
	for _, column := range projectBackupSchemas[table] {
		if value, ok := overrides[column.name]; ok {
			values = append(values, value)
			continue
		}
		values = append(values, defaultProjectBackupTestValue(table, column))
	}
	query := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", table, strings.Join(columns, ","), placeholders(len(columns)))
	if _, err := db.Exec(query, values...); err != nil {
		t.Fatalf("insert %s: %v", table, err)
	}
}

func defaultProjectBackupTestValue(table string, column projectBackupColumn) any {
	name := column.name
	if name == "id" {
		return table + "1"
	}
	if strings.HasSuffix(name, "_at") || name == "created_at" || name == "updated_at" || name == "started_at" || name == "buffered_at" || name == "allocated_at" || name == "applied_at" {
		return "2026-01-01T00:00:00Z"
	}
	upper := strings.ToUpper(column.typ)
	if strings.Contains(upper, "INTEGER") {
		return 1
	}
	if strings.Contains(upper, "REAL") {
		return 1.25
	}
	if strings.Contains(name, "json") || strings.Contains(name, "assignments") || name == "source_chapter_ids" || name == "conflict_files" || name == "traits" || name == "substatus" || name == "enabled_tools" || name == "chapter_settings" || name == "todos_json" || name == "content_json" || name == "turn_usage_json" || name == "permission_suggestions" {
		return "[]"
	}
	return "test-" + name
}

func assertLifecycleBackupFields(t *testing.T, db *sql.DB) {
	t.Helper()
	assertColumnValue(t, db, "chapters", "id", "source", "status", "merged")
	assertColumnValue(t, db, "chapters", "id", "source", "merged_into_chapter_id", "target")
	assertColumnValue(t, db, "chapters", "id", "source", "merge_commit_sha", "merge-sha")
	assertColumnValue(t, db, "chapters", "id", "source", "merge_strategy", "merge")
	assertColumnValue(t, db, "chapters", "id", "source", "pre_merge_target_sha", "pre-target")
	assertColumnValue(t, db, "chapters", "id", "review", "role", "review")
	assertColumnValue(t, db, "chapters", "id", "review", "review_source_chapter_id", "source")
	assertColumnValue(t, db, "chapters", "id", "review", "review_status", "reviewing")
	assertColumnValue(t, db, "chapter_edges", "id", "edge-review", "type", "review")
	assertColumnValue(t, db, "chapter_edges", "id", "edge-review", "metadata", `{"commitSha":"source-head"}`)
	assertColumnValue(t, db, "chapter_edges", "id", "edge-merge", "type", "merge")
	assertColumnValue(t, db, "chapter_edges", "id", "edge-merge", "metadata", `{"commitSha":"merge-sha","strategy":"merge","preMergeTargetSha":"pre-target","status":"completed"}`)
}

func assertColumnValue(t *testing.T, db *sql.DB, table, keyColumn string, keyValue any, column string, want any) {
	t.Helper()
	var got any
	query := fmt.Sprintf("SELECT %s FROM %s WHERE %s = ?", column, table, keyColumn)
	if err := db.QueryRow(query, keyValue).Scan(&got); err != nil {
		t.Fatalf("query %s.%s: %v", table, column, err)
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("%s.%s = %v, want %v", table, column, got, want)
	}
}
