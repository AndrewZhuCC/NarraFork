package chapters

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	gitx "narrafork-go/internal/git"
	"narrafork-go/internal/narrators"

	_ "modernc.org/sqlite"
)

func TestForkParityRestoresContextFilesRuntimeMetadata(t *testing.T) {
	ctx := context.Background()
	sqlDB := newForkParityDB(t)
	repo := newForkParityRepo(t)
	baseSHA := gitOutput(t, repo, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(repo, "keep.txt"), []byte("copy me"), 0o644); err != nil {
		t.Fatalf("write copy source: %v", err)
	}

	copyFiles := `["keep.txt","../outside"]`
	startupScript := `printf startup > startup.out`
	containerConfig := `{"composeFile":"compose.yml","services":["app"]}`
	_, err := sqlDB.Exec(`INSERT INTO projects (id, name, git_path, default_branch, startup_script, copy_files, created_at, updated_at) VALUES ('p1', 'Project', ?, 'master', ?, ?, 'now', 'now')`, repo, startupScript, copyFiles)
	if err != nil {
		t.Fatalf("insert project: %v", err)
	}
	_, err = sqlDB.Exec(`INSERT INTO chapters (id, project_id, title, status, role, branch, worktree_path, base_branch, container_config, is_root, head_commit_sha, commit_count, axis_offset, cross_offset, last_accessed_at, created_at, updated_at) VALUES ('c_parent', 'p1', 'Parent', 'active', 'branch', 'master', ?, 'master', ?, 0, ?, 1, 10, 20, 'now', 'now', 'now')`, repo, containerConfig, baseSHA)
	if err != nil {
		t.Fatalf("insert chapter: %v", err)
	}
	insertForkParityNarratorFixture(t, sqlDB, repo, baseSHA)

	service := &Service{DB: sqlDB, Git: gitx.NewService(), Narrators: &narrators.Service{DB: sqlDB}}
	title := "Fork Parity"
	chapter, err := service.Fork(ctx, "c_parent", ForkInput{Title: &title, InheritMode: "full", ForkAtMessageUUID: "uuid-2"})
	if err != nil {
		t.Fatalf("fork: %v", err)
	}
	if chapter.ParentChapterID == nil || *chapter.ParentChapterID != "c_parent" {
		t.Fatalf("parentChapterId = %v", chapter.ParentChapterID)
	}
	forkPoint, ok := chapter.ForkPoint.(map[string]any)
	if !ok {
		t.Fatalf("forkPoint = %#v", chapter.ForkPoint)
	}
	if forkPoint["commitSha"] != baseSHA || forkPoint["narratorMessageUuid"] != "uuid-2" {
		t.Fatalf("unexpected forkPoint: %#v, base=%s", forkPoint, baseSHA)
	}
	if got, _ := forkPoint["restoredFileSnapshotCount"].(float64); int(got) != 1 {
		t.Fatalf("restoredFileSnapshotCount = %#v", forkPoint["restoredFileSnapshotCount"])
	}
	if chapter.WorktreePath == nil || *chapter.WorktreePath == "" {
		t.Fatalf("expected worktree path")
	}
	worktree := *chapter.WorktreePath
	assertFileContent(t, filepath.Join(worktree, "notes.txt"), "draft at fork")
	assertFileContent(t, filepath.Join(worktree, "keep.txt"), "copy me")
	assertFileContent(t, filepath.Join(worktree, "startup.out"), "startup")
	if _, ok := chapter.ContainerConfig.(map[string]any); !ok {
		t.Fatalf("container config not inherited: %#v", chapter.ContainerConfig)
	}
	if !hasForkFallback(chapter.Fallbacks, "copyFilesPartial") {
		t.Fatalf("expected copyFilesPartial fallback, got %#v", chapter.Fallbacks)
	}
	if hasForkFallback(chapter.Fallbacks, "containerStartup") {
		t.Fatalf("containerStartup should not be reported as fallback after auto-start support, got %#v", chapter.Fallbacks)
	}

	var childNarratorID, parentNarratorID, forkMessageID, inheritMode, cwd string
	var messageCount int
	err = sqlDB.QueryRow(`SELECT id, parent_narrator_id, fork_message_id, inherit_mode, cwd, message_count FROM narrators WHERE chapter_id = ? AND variant = 'primary'`, chapter.ID).Scan(&childNarratorID, &parentNarratorID, &forkMessageID, &inheritMode, &cwd, &messageCount)
	if err != nil {
		t.Fatalf("load child narrator: %v", err)
	}
	if parentNarratorID != "n_parent" || forkMessageID != "m2" || inheritMode != "full" || cwd != worktree || messageCount != 2 {
		t.Fatalf("unexpected child narrator state id=%s parent=%s fork=%s inherit=%s cwd=%s count=%d", childNarratorID, parentNarratorID, forkMessageID, inheritMode, cwd, messageCount)
	}
	refs := []string{}
	rows, err := sqlDB.Query(`SELECT message_id FROM narrator_message_refs WHERE narrator_id = ? ORDER BY seq ASC`, childNarratorID)
	if err != nil {
		t.Fatalf("query refs: %v", err)
	}
	for rows.Next() {
		var messageID string
		if err := rows.Scan(&messageID); err != nil {
			rows.Close()
			t.Fatalf("scan refs: %v", err)
		}
		refs = append(refs, messageID)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close refs: %v", err)
	}
	if strings.Join(refs, ",") != "m1,m2" {
		t.Fatalf("refs = %#v, want m1,m2", refs)
	}

	var edgeMetadata string
	if err := sqlDB.QueryRow(`SELECT metadata FROM chapter_edges WHERE source_id = 'c_parent' AND target_id = ? AND type = 'fork'`, chapter.ID).Scan(&edgeMetadata); err != nil {
		t.Fatalf("load fork edge: %v", err)
	}
	var metadata map[string]any
	if err := json.Unmarshal([]byte(edgeMetadata), &metadata); err != nil {
		t.Fatalf("decode edge metadata: %v", err)
	}
	if metadata["inheritMode"] != "full" || metadata["narratorMessageUuid"] != "uuid-2" {
		t.Fatalf("unexpected edge metadata: %#v", metadata)
	}
	if _, ok := metadata["fallbacks"].([]any); !ok {
		t.Fatalf("expected persisted fallback metadata on edge: %#v", metadata)
	}
}

func TestForkAutoStartsInheritedContainers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake podman script uses POSIX sh")
	}
	ctx := context.Background()
	sqlDB := newForkParityDB(t)
	repo := newForkParityRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "compose.yml"), []byte("services:\n  web:\n    image: test\n"), 0o644); err != nil {
		t.Fatalf("write compose: %v", err)
	}
	runGit(t, repo, "add", "compose.yml")
	runGit(t, repo, "commit", "-m", "add compose")
	baseSHA := gitOutput(t, repo, "rev-parse", "HEAD")
	containerConfig := `{"ports":[{"containerPort":8080,"serviceName":"web"}]}`
	if _, err := sqlDB.Exec(`INSERT INTO projects (id, name, git_path, default_branch, created_at, updated_at) VALUES ('p1', 'Project', ?, 'master', 'now', 'now')`, repo); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO chapters (id, project_id, title, status, role, branch, worktree_path, base_branch, container_config, is_root, head_commit_sha, commit_count, axis_offset, cross_offset, last_accessed_at, created_at, updated_at) VALUES ('c_parent', 'p1', 'Parent', 'active', 'branch', 'master', ?, 'master', ?, 0, ?, 1, 10, 20, 'now', 'now', 'now')`, repo, containerConfig, baseSHA); err != nil {
		t.Fatalf("insert chapter: %v", err)
	}
	prependFakePodman(t, fakePodmanScript())
	recorder := &chapterBroadcastRecorder{}
	service := &Service{DB: sqlDB, Git: gitx.NewService(), BroadcastMergeProgress: recorder.BroadcastMergeProgress}
	title := "Container Fork"
	chapter, err := service.Fork(ctx, "c_parent", ForkInput{Title: &title, InheritMode: "full"})
	if err != nil {
		t.Fatalf("fork: %v", err)
	}
	eventually(t, func() bool { return recorder.hasGlobalEvent("container:started") }, "fork auto-start container:started")
	eventually(t, func() bool {
		var count int
		return sqlDB.QueryRow(`SELECT COUNT(*) FROM container_instances WHERE chapter_id = ?`, chapter.ID).Scan(&count) == nil && count > 0
	}, "fork auto-start persisted container instance")
}

func TestForkParityFreshInheritSkipsMessageRefs(t *testing.T) {
	ctx := context.Background()
	sqlDB := newForkParityDB(t)
	repo := newForkParityRepo(t)
	baseSHA := gitOutput(t, repo, "rev-parse", "HEAD")
	_, err := sqlDB.Exec(`INSERT INTO projects (id, name, git_path, default_branch, created_at, updated_at) VALUES ('p1', 'Project', ?, 'master', 'now', 'now')`, repo)
	if err != nil {
		t.Fatalf("insert project: %v", err)
	}
	_, err = sqlDB.Exec(`INSERT INTO chapters (id, project_id, title, status, role, branch, worktree_path, base_branch, is_root, head_commit_sha, commit_count, axis_offset, cross_offset, created_at, updated_at) VALUES ('c_parent', 'p1', 'Parent', 'active', 'branch', 'master', ?, 'master', 0, ?, 1, 0, 0, 'now', 'now')`, repo, baseSHA)
	if err != nil {
		t.Fatalf("insert chapter: %v", err)
	}
	insertForkParityNarratorFixture(t, sqlDB, repo, baseSHA)
	service := &Service{DB: sqlDB, Git: gitx.NewService(), Narrators: &narrators.Service{DB: sqlDB}}
	title := "Fresh Fork"
	chapter, err := service.Fork(ctx, "c_parent", ForkInput{Title: &title, InheritMode: "fresh", ForkAtMessageUUID: "uuid-2"})
	if err != nil {
		t.Fatalf("fork fresh: %v", err)
	}
	var childNarratorID, inheritMode string
	var messageCount int
	if err := sqlDB.QueryRow(`SELECT id, inherit_mode, message_count FROM narrators WHERE chapter_id = ?`, chapter.ID).Scan(&childNarratorID, &inheritMode, &messageCount); err != nil {
		t.Fatalf("load child narrator: %v", err)
	}
	if inheritMode != "fresh" || messageCount != 0 {
		t.Fatalf("fresh narrator inherit/count = %s/%d", inheritMode, messageCount)
	}
	var refCount int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM narrator_message_refs WHERE narrator_id = ?`, childNarratorID).Scan(&refCount); err != nil {
		t.Fatalf("count fresh refs: %v", err)
	}
	if refCount != 0 {
		t.Fatalf("fresh fork copied %d refs", refCount)
	}
}

func TestKillAndDeleteChapterTerminalsReturnsDeleteErrors(t *testing.T) {
	sqlDB := newForkParityDB(t)
	for _, stmt := range []string{
		`CREATE TABLE terminal_tabs (id TEXT PRIMARY KEY, chapter_id TEXT)`,
		`CREATE TABLE terminal_view_state (id TEXT PRIMARY KEY, chapter_id TEXT)`,
		`CREATE TABLE terminals (id TEXT PRIMARY KEY, chapter_id TEXT)`,
		`INSERT INTO terminal_tabs (id, chapter_id) VALUES ('tab1', 'c1')`,
		`INSERT INTO terminal_view_state (id, chapter_id) VALUES ('view1', 'c1')`,
		`INSERT INTO terminals (id, chapter_id) VALUES ('term1', 'c1')`,
		`CREATE TRIGGER fail_terminal_tab_delete BEFORE DELETE ON terminal_tabs BEGIN SELECT RAISE(ABORT, 'terminal tab delete blocked'); END;`,
	} {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("exec terminal fixture: %v\n%s", err, stmt)
		}
	}
	service := &Service{DB: sqlDB}
	if err := service.killAndDeleteChapterTerminals(context.Background(), "c1"); err == nil {
		t.Fatalf("expected terminal tab delete failure")
	}
	var tabCount int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM terminal_tabs WHERE chapter_id = 'c1'`).Scan(&tabCount); err != nil {
		t.Fatalf("count terminal tabs: %v", err)
	}
	if tabCount != 1 {
		t.Fatalf("terminal tab should remain after failed cleanup, count=%d", tabCount)
	}
}

func TestWakeMergedChapterReturnsErrorWhenMergeEdgeDeleteFails(t *testing.T) {
	repo := newForkParityRepo(t)
	sqlDB := newForkParityDB(t)
	if _, err := sqlDB.Exec(`INSERT INTO projects (id, name, git_path, default_branch, created_at, updated_at) VALUES ('p1', 'Project', ?, 'master', 'now', 'now')`, repo); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO chapters (id, project_id, title, status, role, branch, worktree_path, base_branch, is_root, commit_count, axis_offset, cross_offset, created_at, updated_at) VALUES ('merged', 'p1', 'Merged', 'merged', 'branch', 'master', NULL, 'master', 0, 1, 0, 0, 'now', 'now')`); err != nil {
		t.Fatalf("insert merged chapter: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO chapter_edges (id, project_id, source_id, target_id, type, metadata, created_at) VALUES ('edge_merge', 'p1', 'merged', 'target', 'merge', '{}', 'now')`); err != nil {
		t.Fatalf("insert merge edge: %v", err)
	}
	if _, err := sqlDB.Exec(`CREATE TRIGGER fail_merge_edge_delete BEFORE DELETE ON chapter_edges BEGIN SELECT RAISE(ABORT, 'merge edge delete blocked'); END;`); err != nil {
		t.Fatalf("create delete trigger: %v", err)
	}
	service := &Service{DB: sqlDB, Git: gitx.NewService()}
	if err := service.Wake(context.Background(), "merged"); err == nil {
		t.Fatalf("expected wake to fail when merge edge delete fails")
	}
	var status string
	if err := sqlDB.QueryRow(`SELECT status FROM chapters WHERE id = 'merged'`).Scan(&status); err != nil {
		t.Fatalf("load merged status: %v", err)
	}
	if status != "merged" {
		t.Fatalf("failed wake should roll back chapter status, got %s", status)
	}
}

func TestForkReturnsErrorWhenStoryEdgeInsertFails(t *testing.T) {
	repo := newForkParityRepo(t)
	baseSHA := gitOutput(t, repo, "rev-parse", "HEAD")
	sqlDB := newForkParityDB(t)
	if _, err := sqlDB.Exec(`INSERT INTO projects (id, name, git_path, default_branch, created_at, updated_at) VALUES ('p1', 'Project', ?, 'master', 'now', 'now')`, repo); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO chapters (id, project_id, title, status, role, branch, worktree_path, base_branch, is_root, head_commit_sha, commit_count, axis_offset, cross_offset, created_at, updated_at) VALUES ('c_parent', 'p1', 'Parent', 'active', 'branch', 'master', ?, 'master', 0, ?, 1, 0, 0, 'now', 'now')`, repo, baseSHA); err != nil {
		t.Fatalf("insert parent chapter: %v", err)
	}
	if _, err := sqlDB.Exec(`CREATE TRIGGER fail_fork_edge_insert BEFORE INSERT ON chapter_edges BEGIN SELECT RAISE(ABORT, 'fork edge insert blocked'); END;`); err != nil {
		t.Fatalf("create edge trigger: %v", err)
	}
	service := &Service{DB: sqlDB, Git: gitx.NewService()}
	title := "Broken Edge Fork"
	if _, err := service.Fork(context.Background(), "c_parent", ForkInput{Title: &title}); err == nil {
		t.Fatalf("expected fork edge insert failure")
	}
	var chapterCount int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM chapters`).Scan(&chapterCount); err != nil {
		t.Fatalf("count chapters: %v", err)
	}
	if chapterCount != 1 {
		t.Fatalf("failed fork should clean up created chapter, count=%d", chapterCount)
	}
}

func newForkParityDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	stmts := []string{
		`CREATE TABLE projects (id TEXT PRIMARY KEY, name TEXT, git_path TEXT, default_branch TEXT, startup_script TEXT, copy_files TEXT, chapter_settings TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE chapters (id TEXT PRIMARY KEY, project_id TEXT, title TEXT, description TEXT, status TEXT, role TEXT, branch TEXT, worktree_path TEXT, base_branch TEXT, parent_chapter_id TEXT, fork_point TEXT, merged_into_chapter_id TEXT, merge_commit_sha TEXT, merge_strategy TEXT, pre_merge_target_sha TEXT, container_config TEXT, exploration_group_id TEXT, is_root INTEGER, head_commit_sha TEXT, start_commit_sha TEXT, commit_count INTEGER, color TEXT, group_label TEXT, pinned INTEGER, anchor_commit_sha TEXT, axis_offset REAL, cross_offset REAL, panel_expanded INTEGER, panel_width REAL, panel_height REAL, review_source_chapter_id TEXT, review_status TEXT, last_accessed_at TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE chapter_edges (id TEXT PRIMARY KEY, project_id TEXT, source_id TEXT, target_id TEXT, type TEXT, metadata TEXT, created_at TEXT)`,
		`CREATE TABLE narrators (id TEXT PRIMARY KEY, chapter_id TEXT, api_conversation_id TEXT, fork_message_id TEXT, type TEXT, subagent_type TEXT, title TEXT, inherit_mode TEXT, parent_narrator_id TEXT, context_summary TEXT, model TEXT, system_prompt TEXT, permission_mode TEXT, previous_permission_mode TEXT, plan_file_id TEXT, reasoning_effort TEXT, fast_mode INTEGER DEFAULT 0, relaxed_plan INTEGER DEFAULT 0, plan_reflection_auto_approve_override TEXT DEFAULT 'inherit', danger_reflection_override TEXT DEFAULT 'inherit', message_count INTEGER DEFAULT 0, total_cost_usd REAL DEFAULT 0, last_message_at TEXT, status TEXT DEFAULT 'idle', substatus TEXT DEFAULT '[]', plan_mode INTEGER DEFAULT 0, cwd TEXT, error_message TEXT, todos_json TEXT, todos_tool_use_id TEXT, prune_enabled INTEGER DEFAULT 1, enabled_tools TEXT, variant TEXT DEFAULT 'primary', traits TEXT DEFAULT '[]', is_background INTEGER DEFAULT 0, background_status TEXT, background_result TEXT, background_completed_at TEXT, is_ask_in_passing INTEGER DEFAULT 0, message_version INTEGER DEFAULT 0, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE narrator_messages (id TEXT PRIMARY KEY, narrator_id TEXT, sdk_message_uuid TEXT, parent_tool_use_id TEXT, role TEXT, content_json TEXT, content_text TEXT, model TEXT, commit_sha TEXT, command_text TEXT, created_by TEXT, created_at TEXT)`,
		`CREATE TABLE narrator_message_refs (id TEXT PRIMARY KEY, narrator_id TEXT, message_id TEXT, seq INTEGER, is_compact INTEGER DEFAULT 0, segment_compact_id TEXT)`,
		`CREATE TABLE narrator_tool_calls (id TEXT PRIMARY KEY, narrator_id TEXT, message_id TEXT, tool_use_id TEXT, tool_name TEXT, input_json TEXT, output_json TEXT, status TEXT, created_at TEXT)`,
		`CREATE TABLE narrator_file_snapshots (id TEXT PRIMARY KEY, narrator_id TEXT, file_path TEXT, original_content TEXT, created_at TEXT)`,
		`CREATE TABLE container_instances (id TEXT PRIMARY KEY, chapter_id TEXT, container_id TEXT, service_name TEXT, status TEXT, host_port INTEGER, container_port INTEGER, proxy_label TEXT, container_ip TEXT, volume_name TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE port_allocations (port INTEGER PRIMARY KEY, chapter_id TEXT, service_name TEXT, allocated_at TEXT)`,
	}
	for _, stmt := range stmts {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("create schema: %v\n%s", err, stmt)
		}
	}
	return sqlDB
}

func newForkParityRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("base"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	runGit(t, repo, "add", "README.md")
	runGit(t, repo, "commit", "-m", "base")
	return repo
}

func insertForkParityNarratorFixture(t *testing.T, sqlDB *sql.DB, repo, baseSHA string) {
	t.Helper()
	_, err := sqlDB.Exec(`INSERT INTO narrators (id, chapter_id, type, title, inherit_mode, model, permission_mode, status, substatus, plan_mode, cwd, prune_enabled, variant, traits, is_background, is_ask_in_passing, message_version, created_at, updated_at) VALUES ('n_parent', 'c_parent', 'primary', 'Parent narrator', 'fresh', 'test-model', 'default', 'idle', '[]', 0, ?, 1, 'primary', '[]', 0, 0, 0, 'now', 'now')`, repo)
	if err != nil {
		t.Fatalf("insert narrator: %v", err)
	}
	messages := []struct {
		id, uuid, role, text string
		commit               any
		seq                  int
	}{
		{"m1", "uuid-1", "user", "first", baseSHA, 1},
		{"m2", "uuid-2", "assistant", "write notes", nil, 2},
		{"m3", "uuid-3", "assistant", "after fork point", nil, 3},
	}
	for _, msg := range messages {
		if _, err := sqlDB.Exec(`INSERT INTO narrator_messages (id, narrator_id, sdk_message_uuid, role, content_json, content_text, commit_sha, created_at) VALUES (?, 'n_parent', ?, ?, '[]', ?, ?, ?)`, msg.id, msg.uuid, msg.role, msg.text, msg.commit, msg.id); err != nil {
			t.Fatalf("insert message %s: %v", msg.id, err)
		}
		if _, err := sqlDB.Exec(`INSERT INTO narrator_message_refs (id, narrator_id, message_id, seq, is_compact) VALUES (?, 'n_parent', ?, ?, 0)`, "ref_"+msg.id, msg.id, msg.seq); err != nil {
			t.Fatalf("insert ref %s: %v", msg.id, err)
		}
	}
	notesPath := filepath.Join(repo, "notes.txt")
	input, _ := json.Marshal(map[string]any{"file_path": notesPath, "content": "draft at fork"})
	if _, err := sqlDB.Exec(`INSERT INTO narrator_file_snapshots (id, narrator_id, file_path, original_content, created_at) VALUES ('snap_notes', 'n_parent', ?, NULL, 'now')`, notesPath); err != nil {
		t.Fatalf("insert snapshot: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO narrator_tool_calls (id, narrator_id, message_id, tool_use_id, tool_name, input_json, output_json, status, created_at) VALUES ('tc_write', 'n_parent', 'm2', 'toolu_write', 'Write', ?, '{}', 'success', 'now')`, string(input)); err != nil {
		t.Fatalf("insert tool call: %v", err)
	}
}

func runGit(t *testing.T, cwd string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, string(out))
	}
}

func gitOutput(t *testing.T, cwd string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, string(out))
	}
	return strings.TrimSpace(string(out))
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(data) != want {
		t.Fatalf("%s = %q, want %q", path, string(data), want)
	}
}

func hasForkFallback(fallbacks []ForkFallback, feature string) bool {
	for _, fallback := range fallbacks {
		if fallback.Feature == feature && fallback.Fallback {
			return true
		}
	}
	return false
}
