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

func TestFallbackRootTargetIDReturnsQueryErrors(t *testing.T) {
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer database.Close()
	if _, err := database.Exec(`CREATE TABLE chapters (id TEXT PRIMARY KEY, project_id TEXT)`); err != nil {
		t.Fatalf("create chapters: %v", err)
	}
	service := &Service{DB: database}

	_, err = service.fallbackRootTargetID(context.Background(), "project-1")
	if err == nil || !strings.Contains(err.Error(), "is_root") {
		t.Fatalf("expected root target query error, got %v", err)
	}
}

func TestMergeAutoCommitsDirtySourceCleansWorktreeAndRecordsFallbacks(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	ctx := context.Background()
	repo, sourceWT, sourceHead := setupMergeParityRepo(t)
	database := openMergeParityDB(t)
	insertMergeParityRows(t, database, repo, sourceWT, sourceHead, false)

	svc := &Service{DB: database, Git: &gitx.Service{Timeout: 10 * time.Second}}
	result, err := svc.Merge(ctx, "source", "target", "merge", "", "user-1")
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if ok, _ := result["ok"].(bool); !ok {
		t.Fatalf("merge result = %#v, want ok", result)
	}
	if _, err := os.Stat(sourceWT); !os.IsNotExist(err) {
		t.Fatalf("source worktree still exists or unexpected stat error: %v", err)
	}
	var status string
	var worktree sql.NullString
	if err := database.QueryRow(`SELECT status, worktree_path FROM chapters WHERE id = 'source'`).Scan(&status, &worktree); err != nil {
		t.Fatalf("query source: %v", err)
	}
	if status != "merged" || worktree.Valid {
		t.Fatalf("source status/worktree = %s/%v, want merged/null", status, worktree)
	}
	var autoCommits, mergeCommits int
	_ = database.QueryRow(`SELECT COUNT(*) FROM chapter_commits WHERE chapter_id = 'source' AND source = 'auto'`).Scan(&autoCommits)
	_ = database.QueryRow(`SELECT COUNT(*) FROM chapter_commits WHERE chapter_id = 'target' AND source = 'merge'`).Scan(&mergeCommits)
	if autoCommits == 0 || mergeCommits == 0 {
		t.Fatalf("commit sync counts auto=%d merge=%d, want both > 0", autoCommits, mergeCommits)
	}
	var summaries int
	_ = database.QueryRow(`SELECT COUNT(*) FROM narrator_messages WHERE content_json LIKE '%merge_summary%' AND content_json LIKE '%source%'`).Scan(&summaries)
	if summaries == 0 {
		t.Fatalf("expected fallback merge summary message")
	}
}

func TestMergeRejectsStaleApprovedReview(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	ctx := context.Background()
	repo, sourceWT, reviewedHead := setupMergeParityRepo(t)
	runGitChapterTest(t, sourceWT, "add", ".")
	runGitChapterTest(t, sourceWT, "commit", "-m", "after review")
	database := openMergeParityDB(t)
	insertMergeParityRows(t, database, repo, sourceWT, reviewedHead, true)
	if _, err := database.Exec(`INSERT INTO chapters (id, project_id, title, status, role, branch, worktree_path, base_branch, fork_point, head_commit_sha, start_commit_sha, commit_count, is_root, created_at, updated_at)
		VALUES ('review', 'project', 'Review', 'active', 'review', 'review/source', NULL, 'chapter/source', ?, ?, ?, 1, 0, 'now', 'now')`, `{"commitSha":"`+reviewedHead+`"}`, reviewedHead, reviewedHead); err != nil {
		t.Fatalf("insert review chapter: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO review_conclusions (id, review_chapter_id, source_chapter_id, verdict, findings_json, created_at) VALUES ('rc1', 'review', 'source', 'approve', '[]', 'now')`); err != nil {
		t.Fatalf("insert review conclusion: %v", err)
	}

	svc := &Service{DB: database, Git: &gitx.Service{Timeout: 10 * time.Second}}
	_, err := svc.Merge(ctx, "source", "target", "merge", "", "")
	if err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("merge error = %v, want stale review rejection", err)
	}
}

func setupMergeParityRepo(t *testing.T) (repo string, sourceWT string, sourceHead string) {
	t.Helper()
	repo = t.TempDir()
	runGitChapterTest(t, repo, "init")
	runGitChapterTest(t, repo, "checkout", "-b", "main")
	runGitChapterTest(t, repo, "config", "user.name", "Test User")
	runGitChapterTest(t, repo, "config", "user.email", "test@example.com")
	writeChapterTestFile(t, repo, "base.txt", "base\n")
	runGitChapterTest(t, repo, "add", ".")
	runGitChapterTest(t, repo, "commit", "-m", "base")
	runGitChapterTest(t, repo, "branch", "chapter/source")
	writeChapterTestFile(t, repo, ".git/info/exclude", ".worktrees/\n")
	sourceWT = filepath.Join(repo, ".worktrees", "source")
	runGitChapterTest(t, repo, "worktree", "add", sourceWT, "chapter/source")
	writeChapterTestFile(t, sourceWT, "source.txt", "committed\n")
	runGitChapterTest(t, sourceWT, "add", ".")
	runGitChapterTest(t, sourceWT, "commit", "-m", "source committed")
	sourceHead = strings.TrimSpace(runGitChapterTest(t, sourceWT, "rev-parse", "HEAD"))
	writeChapterTestFile(t, sourceWT, "dirty.txt", "dirty\n")
	return repo, sourceWT, sourceHead
}

func TestDeleteChapterReturnsNarratorArchiveErrorWithoutDeletingChapter(t *testing.T) {
	database := openMergeParityDB(t)
	if _, err := database.Exec(`INSERT INTO projects (id, name) VALUES ('project', 'Project')`); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO chapters (id, project_id, title, status, role, branch, base_branch, is_root, commit_count, pinned, axis_offset, cross_offset, panel_expanded, created_at, updated_at) VALUES ('delete-me', 'project', 'Delete Me', 'active', 'branch', 'chapter/delete-me', 'main', 0, 0, 0, 0, 0, 0, 'now', 'now')`); err != nil {
		t.Fatalf("insert chapter: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO narrators (id, chapter_id, variant, model, created_at, updated_at) VALUES ('narrator-delete', 'delete-me', 'primary', 'test-model', 'now', 'now')`); err != nil {
		t.Fatalf("insert narrator: %v", err)
	}
	if _, err := database.Exec(`CREATE TRIGGER fail_narrator_archive BEFORE UPDATE ON narrators WHEN OLD.chapter_id = 'delete-me' BEGIN SELECT RAISE(ABORT, 'archive blocked'); END;`); err != nil {
		t.Fatalf("create archive trigger: %v", err)
	}
	s := &Service{DB: database}

	err := s.Delete(context.Background(), "delete-me")
	if err == nil || !strings.Contains(err.Error(), "archive chapter narrators") {
		t.Fatalf("expected narrator archive error, got %v", err)
	}
	var chapterCount int
	if err := database.QueryRow(`SELECT COUNT(*) FROM chapters WHERE id = 'delete-me'`).Scan(&chapterCount); err != nil {
		t.Fatalf("count chapter: %v", err)
	}
	if chapterCount != 1 {
		t.Fatalf("failed archive should not delete chapter, count=%d", chapterCount)
	}
	var narratorChapterID string
	if err := database.QueryRow(`SELECT chapter_id FROM narrators WHERE id = 'narrator-delete'`).Scan(&narratorChapterID); err != nil {
		t.Fatalf("load narrator chapter: %v", err)
	}
	if narratorChapterID != "delete-me" {
		t.Fatalf("failed archive should keep narrator chapter reference, got %q", narratorChapterID)
	}
}

func TestCleanupMergeSummaryReturnsNarratorCountUpdateErrors(t *testing.T) {
	database := openMergeParityDB(t)
	if _, err := database.Exec(`INSERT INTO narrators (id, chapter_id, variant, model, message_count, created_at, updated_at) VALUES ('narrator-target', 'target', 'primary', 'test-model', 1, 'now', 'now')`); err != nil {
		t.Fatalf("insert narrator: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO narrator_messages (id, narrator_id, role, content_json, content_text, created_at) VALUES ('summary-1', 'narrator-target', 'user', '[{"type":"merge_summary","sourceChapterId":"source","commitSha":"abc123","isLatest":true}]', 'summary', 'now')`); err != nil {
		t.Fatalf("insert summary message: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO narrator_message_refs (id, narrator_id, message_id, seq, is_compact) VALUES ('ref-1', 'narrator-target', 'summary-1', 1, 0)`); err != nil {
		t.Fatalf("insert summary ref: %v", err)
	}
	if _, err := database.Exec(`CREATE TRIGGER fail_merge_summary_count_update BEFORE UPDATE OF message_count ON narrators BEGIN SELECT RAISE(ABORT, 'message count update blocked'); END;`); err != nil {
		t.Fatalf("create message count trigger: %v", err)
	}
	service := &Service{DB: database}

	err := service.cleanupMergeSummaryForMerge(context.Background(), "source", "abc123")
	if err == nil || !strings.Contains(err.Error(), "update merge summary narrator count") {
		t.Fatalf("expected narrator count update error, got %v", err)
	}
	var messageCount, refCount int
	if err := database.QueryRow(`SELECT COUNT(*) FROM narrator_messages WHERE id = 'summary-1'`).Scan(&messageCount); err != nil {
		t.Fatalf("count summary messages: %v", err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM narrator_message_refs WHERE id = 'ref-1'`).Scan(&refCount); err != nil {
		t.Fatalf("count summary refs: %v", err)
	}
	if messageCount != 1 || refCount != 1 {
		t.Fatalf("failed count update should roll back summary cleanup, messages=%d refs=%d", messageCount, refCount)
	}
}

func TestInjectMergeSummaryFallbackReturnsHistoricalUpdateErrors(t *testing.T) {
	database := openMergeParityDB(t)
	if _, err := database.Exec(`INSERT INTO chapters (id, project_id, title, status, role, branch, base_branch, is_root, commit_count, pinned, axis_offset, cross_offset, panel_expanded, created_at, updated_at) VALUES ('target', 'project', 'Target', 'active', 'trunk', 'main', 'main', 1, 0, 0, 0, 0, 0, 'now', 'now')`); err != nil {
		t.Fatalf("insert target chapter: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO narrators (id, chapter_id, variant, model, created_at, updated_at) VALUES ('narrator-target', 'target', 'primary', 'test-model', 'now', 'now')`); err != nil {
		t.Fatalf("insert target narrator: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO narrator_messages (id, narrator_id, role, content_json, content_text, created_at) VALUES ('old-summary', 'narrator-target', 'user', '[{"type":"merge_summary","sourceChapterId":"source","isLatest":true}]', 'old summary', 'now')`); err != nil {
		t.Fatalf("insert old summary: %v", err)
	}
	if _, err := database.Exec(`CREATE TRIGGER fail_merge_summary_historical BEFORE UPDATE OF content_json ON narrator_messages WHEN OLD.id = 'old-summary' BEGIN SELECT RAISE(ABORT, 'merge summary historical update blocked'); END;`); err != nil {
		t.Fatalf("create historical trigger: %v", err)
	}
	service := &Service{DB: database}

	err := service.injectMergeSummaryFallback(context.Background(), Chapter{ID: "source", Branch: "feature"}, Chapter{ID: "target", Branch: "main"}, "merge", "abc123", "merged", "user-1")
	if err == nil || !strings.Contains(err.Error(), "mark merge summary historical") {
		t.Fatalf("expected historical update error, got %v", err)
	}
	var messageCount int
	if err := database.QueryRow(`SELECT COUNT(*) FROM narrator_messages WHERE narrator_id = 'narrator-target' AND content_json LIKE '%merge_summary%'`).Scan(&messageCount); err != nil {
		t.Fatalf("count merge summary messages: %v", err)
	}
	if messageCount != 1 {
		t.Fatalf("historical update failure should not insert new summary, count=%d", messageCount)
	}
}

func TestInjectMergeSummaryFallbackReturnsSequenceQueryErrors(t *testing.T) {
	database := openMergeParityDB(t)
	if _, err := database.Exec(`INSERT INTO chapters (id, project_id, title, status, role, branch, base_branch, is_root, commit_count, pinned, axis_offset, cross_offset, panel_expanded, created_at, updated_at) VALUES ('target', 'project', 'Target', 'active', 'trunk', 'main', 'main', 1, 0, 0, 0, 0, 0, 'now', 'now')`); err != nil {
		t.Fatalf("insert target chapter: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO narrators (id, chapter_id, variant, model, created_at, updated_at) VALUES ('narrator-target', 'target', 'primary', 'test-model', 'now', 'now')`); err != nil {
		t.Fatalf("insert target narrator: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO narrator_messages (id, narrator_id, role, content_json, content_text, created_at) VALUES ('existing-message', 'narrator-target', 'user', '[{"type":"text","text":"existing"}]', 'existing', 'now')`); err != nil {
		t.Fatalf("insert existing message: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO narrator_message_refs (id, narrator_id, message_id, seq, is_compact) VALUES ('existing-ref', 'narrator-target', 'existing-message', 0, 0)`); err != nil {
		t.Fatalf("insert existing ref: %v", err)
	}
	for _, stmt := range []string{
		`ALTER TABLE narrator_message_refs RENAME TO narrator_message_refs_real`,
		`CREATE VIEW narrator_message_refs AS SELECT id, narrator_id, message_id, json_extract('not-json', '$') AS seq, is_compact FROM narrator_message_refs_real`,
		`CREATE TRIGGER narrator_message_refs_insert INSTEAD OF INSERT ON narrator_message_refs BEGIN INSERT INTO narrator_message_refs_real (id, narrator_id, message_id, seq, is_compact) VALUES (NEW.id, NEW.narrator_id, NEW.message_id, NEW.seq, NEW.is_compact); END;`,
	} {
		if _, err := database.Exec(stmt); err != nil {
			t.Fatalf("prepare broken ref view: %v", err)
		}
	}
	service := &Service{DB: database}

	err := service.injectMergeSummaryFallback(context.Background(), Chapter{ID: "source", Branch: "feature"}, Chapter{ID: "target", Branch: "main"}, "merge", "abc123", "merged", "user-1")
	if err == nil || !strings.Contains(err.Error(), "query merge summary sequence") {
		t.Fatalf("expected merge summary sequence error, got %v", err)
	}
	var messageCount int
	if err := database.QueryRow(`SELECT COUNT(*) FROM narrator_messages WHERE narrator_id = 'narrator-target' AND content_json LIKE '%merge_summary%'`).Scan(&messageCount); err != nil {
		t.Fatalf("count merge summary messages: %v", err)
	}
	if messageCount != 0 {
		t.Fatalf("sequence query failure should roll back merge summary message, count=%d", messageCount)
	}
	var refCount int
	if err := database.QueryRow(`SELECT COUNT(*) FROM narrator_message_refs_real WHERE narrator_id = 'narrator-target'`).Scan(&refCount); err != nil {
		t.Fatalf("count backing refs: %v", err)
	}
	if refCount != 1 {
		t.Fatalf("sequence query failure should not insert fallback ref, count=%d", refCount)
	}
}

func TestCleanupMergedSourceWorktreeReturnsTerminalDeleteErrors(t *testing.T) {
	database := openMergeParityDB(t)
	if _, err := database.Exec(`INSERT INTO projects (id, name, git_path) VALUES ('project', 'Project', '/repo')`); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	for _, stmt := range []string{
		`INSERT INTO terminal_tabs (id, chapter_id) VALUES ('tab-source', 'source')`,
		`CREATE TRIGGER fail_merged_source_tab_cleanup BEFORE DELETE ON terminal_tabs WHEN OLD.chapter_id = 'source' BEGIN SELECT RAISE(ABORT, 'merged source tab cleanup blocked'); END;`,
	} {
		if _, err := database.Exec(stmt); err != nil {
			t.Fatalf("prepare terminal cleanup fixture: %v", err)
		}
	}
	worktree := "/outside/source"
	s := &Service{DB: database}

	err := s.cleanupMergedSourceWorktree(context.Background(), Chapter{ID: "source", ProjectID: "project", WorktreePath: &worktree})
	if err == nil || !strings.Contains(err.Error(), "delete merged source terminal tabs") {
		t.Fatalf("expected terminal tab cleanup error, got %v", err)
	}
	var tabCount int
	if err := database.QueryRow(`SELECT COUNT(*) FROM terminal_tabs WHERE chapter_id = 'source'`).Scan(&tabCount); err != nil {
		t.Fatalf("count source tabs: %v", err)
	}
	if tabCount != 1 {
		t.Fatalf("failed cleanup should leave terminal tab for retry, count=%d", tabCount)
	}
}

func TestRecordChapterCommitReturnsInsertErrors(t *testing.T) {
	database := openMergeParityDB(t)
	if _, err := database.Exec(`INSERT INTO chapters (id, project_id, title, status, role, branch, base_branch, is_root, commit_count, pinned, axis_offset, cross_offset, panel_expanded, created_at, updated_at) VALUES ('commit-target', 'project', 'Commit Target', 'active', 'branch', 'main', 'main', 0, 0, 0, 0, 0, 0, 'now', 'now')`); err != nil {
		t.Fatalf("insert commit target: %v", err)
	}
	if _, err := database.Exec(`CREATE TRIGGER fail_chapter_commit_insert BEFORE INSERT ON chapter_commits BEGIN SELECT RAISE(ABORT, 'chapter commit insert blocked'); END;`); err != nil {
		t.Fatalf("create commit trigger: %v", err)
	}
	service := &Service{DB: database}
	if err := service.recordChapterCommit(context.Background(), "commit-target", "abc123", "message", "manual"); err == nil {
		t.Fatalf("expected chapter commit insert failure")
	}
}

func TestRefreshChapterGitMetadataReturnsUpdateErrors(t *testing.T) {
	repo := newForkParityRepo(t)
	database := openMergeParityDB(t)
	if _, err := database.Exec(`INSERT INTO chapters (id, project_id, title, status, role, branch, worktree_path, base_branch, is_root, commit_count, pinned, axis_offset, cross_offset, panel_expanded, created_at, updated_at) VALUES ('refresh', 'project', 'Refresh', 'active', 'branch', 'main', ?, 'main', 0, 0, 0, 0, 0, 0, 'now', 'now')`, repo); err != nil {
		t.Fatalf("insert refresh chapter: %v", err)
	}
	if _, err := database.Exec(`CREATE TRIGGER fail_refresh_metadata BEFORE UPDATE OF head_commit_sha ON chapters BEGIN SELECT RAISE(ABORT, 'refresh metadata update blocked'); END;`); err != nil {
		t.Fatalf("create refresh trigger: %v", err)
	}
	h := &Handler{Service: &Service{DB: database, Git: gitx.NewService()}}
	if _, err := h.refreshChapterGitMetadata(context.Background(), "refresh", repo); err == nil {
		t.Fatalf("expected refresh metadata update failure")
	}
}

func openMergeParityDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	stmts := []string{
		`CREATE TABLE projects (id TEXT PRIMARY KEY, name TEXT, git_path TEXT, default_branch TEXT, startup_script TEXT, copy_files TEXT, chapter_settings TEXT)`,
		`CREATE TABLE chapters (id TEXT PRIMARY KEY, project_id TEXT, title TEXT, description TEXT, status TEXT, role TEXT, branch TEXT, worktree_path TEXT, base_branch TEXT, parent_chapter_id TEXT, fork_point TEXT, merged_into_chapter_id TEXT, merge_commit_sha TEXT, merge_strategy TEXT, pre_merge_target_sha TEXT, container_config TEXT, exploration_group_id TEXT, is_root INTEGER, head_commit_sha TEXT, start_commit_sha TEXT, commit_count INTEGER, color TEXT, group_label TEXT, pinned INTEGER, anchor_commit_sha TEXT, axis_offset REAL, cross_offset REAL, panel_expanded INTEGER, panel_width REAL, panel_height REAL, review_source_chapter_id TEXT, review_status TEXT, last_accessed_at TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE chapter_edges (id TEXT PRIMARY KEY, project_id TEXT, source_id TEXT, target_id TEXT, type TEXT, metadata TEXT, created_at TEXT)`,
		`CREATE TABLE chapter_commits (id TEXT PRIMARY KEY, chapter_id TEXT, sha TEXT, message TEXT, full_message TEXT, author_name TEXT, author_email TEXT, authored_at TEXT, source TEXT, narrator_id TEXT, narrator_message_id TEXT, files_changed INTEGER, lines_added INTEGER, lines_removed INTEGER, created_at TEXT)`,
		`CREATE TABLE narrators (id TEXT PRIMARY KEY, chapter_id TEXT, variant TEXT, model TEXT, message_count INTEGER DEFAULT 0, message_version INTEGER DEFAULT 0, last_message_at TEXT, updated_at TEXT, created_at TEXT)`,
		`CREATE TABLE narrator_messages (id TEXT PRIMARY KEY, narrator_id TEXT, role TEXT, content_json TEXT, content_text TEXT, created_by TEXT, created_at TEXT)`,
		`CREATE TABLE narrator_message_refs (id TEXT PRIMARY KEY, narrator_id TEXT, message_id TEXT, seq INTEGER, is_compact INTEGER)`,
		`CREATE TABLE review_conclusions (id TEXT PRIMARY KEY, review_chapter_id TEXT, source_chapter_id TEXT, verdict TEXT, findings_json TEXT, created_at TEXT)`,
		`CREATE TABLE terminals (id TEXT PRIMARY KEY, chapter_id TEXT)`,
		`CREATE TABLE terminal_tabs (id TEXT PRIMARY KEY, chapter_id TEXT)`,
		`CREATE TABLE terminal_view_state (id TEXT PRIMARY KEY, chapter_id TEXT)`,
	}
	for _, stmt := range stmts {
		if _, err := database.Exec(stmt); err != nil {
			t.Fatalf("schema: %v\n%s", err, stmt)
		}
	}
	return database
}

func insertMergeParityRows(t *testing.T, database *sql.DB, repo, sourceWT, sourceHead string, requireReview bool) {
	t.Helper()
	settings := `{}`
	if requireReview {
		settings = `{"requireReviewBeforeMerge":true}`
	}
	if _, err := database.Exec(`INSERT INTO projects (id, name, git_path, default_branch, chapter_settings) VALUES ('project', 'Project', ?, 'main', ?)`, repo, settings); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	mainHead := strings.TrimSpace(runGitChapterTest(t, repo, "rev-parse", "main"))
	if _, err := database.Exec(`INSERT INTO chapters (id, project_id, title, status, role, branch, worktree_path, base_branch, head_commit_sha, start_commit_sha, commit_count, is_root, created_at, updated_at)
		VALUES ('target', 'project', 'Target', 'active', 'trunk', 'main', ?, 'main', ?, ?, 1, 1, 'now', 'now')`, repo, mainHead, mainHead); err != nil {
		t.Fatalf("insert target: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO chapters (id, project_id, title, status, role, branch, worktree_path, base_branch, parent_chapter_id, fork_point, head_commit_sha, start_commit_sha, commit_count, is_root, created_at, updated_at)
		VALUES ('source', 'project', 'Source', 'active', 'branch', 'chapter/source', ?, 'main', 'target', ?, ?, ?, 2, 0, 'now', 'now')`, sourceWT, `{"commitSha":"`+sourceHead+`"}`, sourceHead, sourceHead); err != nil {
		t.Fatalf("insert source: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO narrators (id, chapter_id, variant, model, created_at, updated_at) VALUES ('narrator-target', 'target', 'primary', 'test-model', 'now', 'now')`); err != nil {
		t.Fatalf("insert narrator: %v", err)
	}
}

func runGitChapterTest(t *testing.T, cwd string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
	return string(out)
}

func writeChapterTestFile(t *testing.T, repo, name, content string) {
	t.Helper()
	path := filepath.Join(repo, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}
