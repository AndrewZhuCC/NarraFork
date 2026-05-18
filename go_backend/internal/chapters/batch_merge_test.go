package chapters

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gitx "narrafork-go/internal/git"

	_ "modernc.org/sqlite"
)

func TestBatchMergeRunsInBackgroundAndPersistsProgress(t *testing.T) {
	ctx := context.Background()
	sqlDB := newBatchMergeTestDB(t)
	repo, targetWorktree := setupBatchMergeRepo(t, false)
	insertBatchMergeProjectAndChapters(t, sqlDB, repo, targetWorktree)

	events := make(chan map[string]any, 8)
	service := &Service{DB: sqlDB, Git: gitx.NewService(), BroadcastMergeProgress: func(payload any) {
		if event, ok := payload.(map[string]any); ok {
			events <- event
		}
	}}

	result, err := service.BatchMerge(ctx, BatchMergeInput{
		BaseChapterID:    "target",
		TargetChapterID:  "target",
		SourceChapterIDs: []string{"source1", "source2"},
		Strategy:         "merge",
	})
	if err != nil {
		t.Fatalf("BatchMerge: %v", err)
	}
	if result.Status != "running" || result.MergeSessionID == "" || result.TargetChapterID != "target" {
		t.Fatalf("unexpected immediate result: %#v", result)
	}

	session := waitForMergeSessionStatus(t, sqlDB, result.MergeSessionID, "completed", 3*time.Second)
	if session.currentIndex != 2 || session.mergedCount != 2 || session.error.Valid {
		t.Fatalf("session progress = %#v, want currentIndex=2 mergedCount=2 no error", session)
	}

	seen := drainMergeEventTypes(events)
	for _, want := range []string{"merge:started", "merge:step_ok", "merge:completed"} {
		if !seen[want] {
			t.Fatalf("missing WS progress event %q in %#v", want, seen)
		}
	}
}

func TestBatchMergeConflictDecisionTimeoutFallsBackToError(t *testing.T) {
	ctx := context.Background()
	sqlDB := newBatchMergeTestDB(t)
	repo, targetWorktree := setupBatchMergeRepo(t, true)
	insertBatchMergeProjectAndChapters(t, sqlDB, repo, targetWorktree)

	events := make(chan map[string]any, 8)
	service := &Service{
		DB:                    sqlDB,
		Git:                   gitx.NewService(),
		MergeDecisionTimeout:  80 * time.Millisecond,
		MergeDecisionInterval: 10 * time.Millisecond,
		BroadcastMergeProgress: func(payload any) {
			if event, ok := payload.(map[string]any); ok {
				events <- event
			}
		},
	}

	result, err := service.BatchMerge(ctx, BatchMergeInput{
		BaseChapterID:    "target",
		TargetChapterID:  "target",
		SourceChapterIDs: []string{"source1"},
		Strategy:         "merge",
	})
	if err != nil {
		t.Fatalf("BatchMerge: %v", err)
	}

	session := waitForMergeSessionStatus(t, sqlDB, result.MergeSessionID, "error", 3*time.Second)
	if session.currentIndex != 0 || session.mergedCount != 0 {
		t.Fatalf("session progress = %#v, want stopped at first item", session)
	}
	if !session.error.Valid || !strings.Contains(session.error.String, "Decision timeout") {
		t.Fatalf("session error = %#v, want Decision timeout", session.error)
	}
	if !strings.Contains(session.conflictFiles.String, "shared.txt") {
		t.Fatalf("conflict_files = %q, want shared.txt", session.conflictFiles.String)
	}

	seen := drainMergeEventTypes(events)
	for _, want := range []string{"merge:conflict", "merge:cancelled"} {
		if !seen[want] {
			t.Fatalf("missing WS progress event %q in %#v", want, seen)
		}
	}
}

type mergeSessionSnapshot struct {
	status        string
	currentIndex  int
	mergedCount   int
	conflictFiles sql.NullString
	error         sql.NullString
}

func waitForMergeSessionStatus(t *testing.T, sqlDB *sql.DB, id string, want string, timeout time.Duration) mergeSessionSnapshot {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var snap mergeSessionSnapshot
		err := sqlDB.QueryRow(`SELECT status, current_index, merged_count, conflict_files, error FROM merge_sessions WHERE id = ?`, id).Scan(&snap.status, &snap.currentIndex, &snap.mergedCount, &snap.conflictFiles, &snap.error)
		if err != nil {
			t.Fatalf("query merge session: %v", err)
		}
		if snap.status == want {
			return snap
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("merge session %s did not reach status %q", id, want)
	return mergeSessionSnapshot{}
}

func drainMergeEventTypes(events <-chan map[string]any) map[string]bool {
	seen := map[string]bool{}
	for {
		select {
		case event := <-events:
			if typ, _ := event["type"].(string); typ != "" {
				seen[typ] = true
			}
		default:
			return seen
		}
	}
}

func newBatchMergeTestDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	_, err = sqlDB.Exec(`
CREATE TABLE projects (
	id TEXT PRIMARY KEY, name TEXT, git_path TEXT, default_branch TEXT, startup_script TEXT,
	copy_files TEXT, chapter_settings TEXT
);
CREATE TABLE chapters (
	id TEXT PRIMARY KEY, project_id TEXT, title TEXT, description TEXT, status TEXT, role TEXT,
	branch TEXT, worktree_path TEXT, base_branch TEXT, parent_chapter_id TEXT, fork_point TEXT,
	merged_into_chapter_id TEXT, merge_commit_sha TEXT, merge_strategy TEXT, pre_merge_target_sha TEXT,
	container_config TEXT, exploration_group_id TEXT, is_root INTEGER, head_commit_sha TEXT,
	start_commit_sha TEXT, commit_count INTEGER, color TEXT, group_label TEXT, pinned INTEGER,
	anchor_commit_sha TEXT, axis_offset REAL, cross_offset REAL, panel_expanded INTEGER,
	panel_width REAL, panel_height REAL, review_source_chapter_id TEXT, review_status TEXT,
	last_accessed_at TEXT, created_at TEXT, updated_at TEXT
);
CREATE TABLE chapter_edges (id TEXT PRIMARY KEY, project_id TEXT, source_id TEXT, target_id TEXT, type TEXT, metadata TEXT, created_at TEXT);
CREATE TABLE chapter_commits (id TEXT PRIMARY KEY, chapter_id TEXT, sha TEXT, message TEXT, full_message TEXT, author_name TEXT, author_email TEXT, authored_at TEXT, source TEXT, narrator_id TEXT, narrator_message_id TEXT, files_changed INTEGER, lines_added INTEGER, lines_removed INTEGER, created_at TEXT);
CREATE TABLE merge_sessions (
	id TEXT PRIMARY KEY, target_chapter_id TEXT NOT NULL, source_chapter_ids TEXT NOT NULL,
	strategy TEXT NOT NULL DEFAULT 'merge', status TEXT NOT NULL, current_index INTEGER NOT NULL DEFAULT 0,
	merged_count INTEGER NOT NULL DEFAULT 0, current_source_chapter_id TEXT, conflict_files TEXT,
	error TEXT, locale TEXT, created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
`)
	if err != nil {
		t.Fatalf("create schema: %v", err)
	}
	return sqlDB
}

func setupBatchMergeRepo(t *testing.T, conflict bool) (string, string) {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	runGitBatch(t, repo, "init", "-b", "main")
	runGitBatch(t, repo, "config", "user.email", "test@example.com")
	runGitBatch(t, repo, "config", "user.name", "Test User")
	writeFileBatch(t, filepath.Join(repo, "shared.txt"), "base\n")
	runGitBatch(t, repo, "add", ".")
	runGitBatch(t, repo, "commit", "-m", "base")

	runGitBatch(t, repo, "checkout", "-b", "source1")
	if conflict {
		writeFileBatch(t, filepath.Join(repo, "shared.txt"), "source\n")
	} else {
		writeFileBatch(t, filepath.Join(repo, "source1.txt"), "one\n")
	}
	runGitBatch(t, repo, "add", ".")
	runGitBatch(t, repo, "commit", "-m", "source1")

	runGitBatch(t, repo, "checkout", "main")
	runGitBatch(t, repo, "checkout", "-b", "source2")
	writeFileBatch(t, filepath.Join(repo, "source2.txt"), "two\n")
	runGitBatch(t, repo, "add", ".")
	runGitBatch(t, repo, "commit", "-m", "source2")

	runGitBatch(t, repo, "checkout", "main")
	runGitBatch(t, repo, "branch", "target")
	targetWorktree := filepath.Join(repo, ".worktrees", "target")
	runGitBatch(t, repo, "worktree", "add", targetWorktree, "target")
	runGitBatch(t, targetWorktree, "config", "user.email", "test@example.com")
	runGitBatch(t, targetWorktree, "config", "user.name", "Test User")
	if conflict {
		writeFileBatch(t, filepath.Join(targetWorktree, "shared.txt"), "target\n")
		runGitBatch(t, targetWorktree, "add", ".")
		runGitBatch(t, targetWorktree, "commit", "-m", "target")
	}
	return repo, targetWorktree
}

func insertBatchMergeProjectAndChapters(t *testing.T, sqlDB *sql.DB, repo, targetWorktree string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := sqlDB.Exec(`INSERT INTO projects (id, name, git_path, default_branch, chapter_settings) VALUES ('project', 'Project', ?, 'main', '{}')`, repo)
	if err != nil {
		t.Fatalf("insert project: %v", err)
	}
	insertChapter := func(id, title, branch string, worktree *string) {
		var worktreeValue any
		if worktree != nil {
			worktreeValue = *worktree
		}
		_, err := sqlDB.Exec(`INSERT INTO chapters (
			id, project_id, title, status, role, branch, worktree_path, base_branch,
			is_root, commit_count, pinned, axis_offset, cross_offset, panel_expanded, created_at, updated_at
		) VALUES (?, 'project', ?, 'active', 'branch', ?, ?, 'main', 0, 1, 0, 0, 0, 0, ?, ?)`, id, title, branch, worktreeValue, now, now)
		if err != nil {
			t.Fatalf("insert chapter %s: %v", id, err)
		}
	}
	insertChapter("target", "Target", "target", &targetWorktree)
	insertChapter("source1", "Source 1", "source1", nil)
	insertChapter("source2", "Source 2", "source2", nil)
}

func runGitBatch(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, string(out))
	}
}

func writeFileBatch(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
