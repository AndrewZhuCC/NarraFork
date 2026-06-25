package narrators

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestConcludeReviewToolPersistsTSConclusionAndInjectsFeedback(t *testing.T) {
	ctx := context.Background()
	sqlDB := openReviewNarratorDB(t)
	defer sqlDB.Close()
	reviewEvents := []map[string]any{}
	service := &Service{
		DB: sqlDB,
		BroadcastProjectEvent: func(payload any) {
			if event, ok := payload.(map[string]any); ok {
				reviewEvents = append(reviewEvents, event)
			}
		},
	}
	insertReviewChapters(t, sqlDB, "reviewing", "")
	sourceID := "source"
	reviewID := "review"
	sourceNarrator, err := service.Create(ctx, CreateInput{ChapterID: &sourceID, Model: "test", PermissionMode: "default"})
	if err != nil {
		t.Fatalf("create source narrator: %v", err)
	}
	reviewNarrator, err := service.Create(ctx, CreateInput{ChapterID: &reviewID, Model: "test", PermissionMode: "default"})
	if err != nil {
		t.Fatalf("create review narrator: %v", err)
	}

	out, err := service.concludeReviewTool(ctx, reviewNarrator.ID, reviewNarrator, map[string]any{
		"verdict": "request_changes",
		"findings": []any{
			map[string]any{"severity": "major", "file": "main.go", "line": float64(42), "message": "handle the error"},
		},
	})
	if err != nil {
		t.Fatalf("concludeReviewTool: %v", err)
	}
	if !strings.Contains(out, "request_changes") || !strings.Contains(out, "1 major") {
		t.Fatalf("unexpected tool output: %s", out)
	}

	var status string
	if err := sqlDB.QueryRow(`SELECT review_status FROM chapters WHERE id = 'review'`).Scan(&status); err != nil {
		t.Fatalf("load review status: %v", err)
	}
	if status != "concluded" {
		t.Fatalf("review_status = %q, want concluded", status)
	}
	if !hasReviewLifecycleEvent(reviewEvents, "review:concluded", "review", "source", "p1") {
		t.Fatalf("missing review:concluded lifecycle event: %#v", reviewEvents)
	}
	var verdict, findings string
	if err := sqlDB.QueryRow(`SELECT verdict, findings_json FROM review_conclusions WHERE review_chapter_id = 'review'`).Scan(&verdict, &findings); err != nil {
		t.Fatalf("load conclusion: %v", err)
	}
	if verdict != "request_changes" || !strings.Contains(findings, "handle the error") || !strings.Contains(findings, "major") {
		t.Fatalf("bad TS conclusion row verdict=%q findings=%s", verdict, findings)
	}
	var feedbackText, feedbackJSON string
	// review_feedback is persisted with role="sys" (model-visible) via
	// narratorService.persistSystemMessage (narrator-persistence.ts:221), so the
	// feedback reaches the source narrator's model. role="system" would be dropped.
	if err := sqlDB.QueryRow(`SELECT content_text, content_json FROM narrator_messages WHERE narrator_id = ? AND role = 'sys' ORDER BY created_at DESC LIMIT 1`, sourceNarrator.ID).Scan(&feedbackText, &feedbackJSON); err != nil {
		t.Fatalf("load feedback message: %v", err)
	}
	if !strings.Contains(feedbackText, "Changes Requested") || !strings.Contains(feedbackText, "main.go:42") || !strings.Contains(feedbackJSON, "review_feedback") {
		t.Fatalf("feedback not injected correctly text=%q json=%s", feedbackText, feedbackJSON)
	}
}

func hasReviewLifecycleEvent(events []map[string]any, eventType, reviewChapterID, sourceChapterID, projectID string) bool {
	for _, event := range events {
		if event["type"] == eventType && event["reviewChapterId"] == reviewChapterID && event["sourceChapterId"] == sourceChapterID && event["projectId"] == projectID {
			return true
		}
	}
	return false
}

func TestReviewAgentCompletionResetsMutationAndSchedulesContinuation(t *testing.T) {
	ctx := context.Background()
	repo := t.TempDir()
	runGitNarratorReview(t, repo, "init", "-b", "main")
	runGitNarratorReview(t, repo, "config", "user.email", "test@example.com")
	runGitNarratorReview(t, repo, "config", "user.name", "Test User")
	writeNarratorReviewFile(t, filepath.Join(repo, "main.go"), "package main\n")
	runGitNarratorReview(t, repo, "add", "main.go")
	runGitNarratorReview(t, repo, "commit", "-m", "initial")
	expected := strings.TrimSpace(runGitNarratorReview(t, repo, "rev-parse", "HEAD"))
	writeNarratorReviewFile(t, filepath.Join(repo, "main.go"), "package main\n// bad review mutation\n")
	runGitNarratorReview(t, repo, "add", "main.go")
	runGitNarratorReview(t, repo, "commit", "-m", "bad review commit")
	writeNarratorReviewFile(t, filepath.Join(repo, "scratch.txt"), "reviewer scratch\n")

	sqlDB := openReviewNarratorDB(t)
	defer sqlDB.Close()
	insertReviewChapters(t, sqlDB, "reviewing", repo)
	if _, err := sqlDB.Exec(`UPDATE chapters SET start_commit_sha = ?, head_commit_sha = ? WHERE id = 'review'`, expected, expected); err != nil {
		t.Fatalf("update expected sha: %v", err)
	}
	service := &Service{DB: sqlDB}
	retried := ""
	service.contextOverflowRetry = func(_ context.Context, narratorID string) {
		retried = narratorID
	}
	reviewID := "review"
	reviewNarrator, err := service.Create(ctx, CreateInput{ChapterID: &reviewID, Model: "test", PermissionMode: "default"})
	if err != nil {
		t.Fatalf("create review narrator: %v", err)
	}

	state := &agentRunState{narratorID: reviewNarrator.ID, narrator: reviewNarrator, assistantText: "done"}
	if err := service.finishAgentRunSuccess(ctx, state); err != nil {
		t.Fatalf("finishAgentRunSuccess: %v", err)
	}
	if retried != reviewNarrator.ID {
		t.Fatalf("expected review reset to schedule continuation for %s, got %q", reviewNarrator.ID, retried)
	}
	var status, substatus string
	if err := sqlDB.QueryRow(`SELECT status, substatus FROM narrators WHERE id = ?`, reviewNarrator.ID).Scan(&status, &substatus); err != nil {
		t.Fatalf("load narrator status: %v", err)
	}
	if status != "working" || !strings.Contains(substatus, "calling_model") {
		t.Fatalf("review reset should keep narrator working/calling_model, got %s %s", status, substatus)
	}
	var reviewStatus string
	if err := sqlDB.QueryRow(`SELECT review_status FROM chapters WHERE id = 'review'`).Scan(&reviewStatus); err != nil {
		t.Fatalf("load review status: %v", err)
	}
	if reviewStatus != "reviewing" {
		t.Fatalf("mutated review should not conclude, got %q", reviewStatus)
	}
	var resetMessage string
	if err := sqlDB.QueryRow(`SELECT m.content_text FROM narrator_message_refs r JOIN narrator_messages m ON m.id = r.message_id WHERE r.narrator_id = ? AND m.role = 'user' ORDER BY r.seq DESC LIMIT 1`, reviewNarrator.ID).Scan(&resetMessage); err != nil {
		t.Fatalf("load reset user message: %v", err)
	}
	if !strings.Contains(resetMessage, "working tree has been reset") || !strings.Contains(resetMessage, "HEAD commit was moved") {
		t.Fatalf("missing TS-compatible reset user message: %q", resetMessage)
	}
	if head := strings.TrimSpace(runGitNarratorReview(t, repo, "rev-parse", "HEAD")); head != expected {
		t.Fatalf("HEAD = %s, want %s", head, expected)
	}
	if status := runGitNarratorReview(t, repo, "status", "--porcelain"); strings.TrimSpace(status) != "" {
		t.Fatalf("worktree not clean after agent completion reset: %q", status)
	}
}

func TestReviewGitStateResetProtectionRestoresHeadAndDirtyFiles(t *testing.T) {
	ctx := context.Background()
	repo := t.TempDir()
	runGitNarratorReview(t, repo, "init", "-b", "main")
	runGitNarratorReview(t, repo, "config", "user.email", "test@example.com")
	runGitNarratorReview(t, repo, "config", "user.name", "Test User")
	writeNarratorReviewFile(t, filepath.Join(repo, "main.go"), "package main\n")
	runGitNarratorReview(t, repo, "add", "main.go")
	runGitNarratorReview(t, repo, "commit", "-m", "initial")
	expected := strings.TrimSpace(runGitNarratorReview(t, repo, "rev-parse", "HEAD"))
	writeNarratorReviewFile(t, filepath.Join(repo, "main.go"), "package main\n// dirty\n")
	writeNarratorReviewFile(t, filepath.Join(repo, "temp.txt"), "dirty\n")
	runGitNarratorReview(t, repo, "add", "main.go")
	runGitNarratorReview(t, repo, "commit", "-m", "bad review commit")

	sqlDB := openReviewNarratorDB(t)
	defer sqlDB.Close()
	insertReviewChapters(t, sqlDB, "reviewing", repo)
	if _, err := sqlDB.Exec(`UPDATE chapters SET start_commit_sha = ?, head_commit_sha = ? WHERE id = 'review'`, expected, expected); err != nil {
		t.Fatalf("update expected sha: %v", err)
	}
	service := &Service{DB: sqlDB}
	isReview, reset, message := service.checkAndResetReviewGitState(ctx, "review")
	if !isReview || !reset || !strings.Contains(message, "HEAD commit was moved") || !strings.Contains(message, "file modifications detected") {
		t.Fatalf("unexpected reset result isReview=%v reset=%v message=%q", isReview, reset, message)
	}
	if head := strings.TrimSpace(runGitNarratorReview(t, repo, "rev-parse", "HEAD")); head != expected {
		t.Fatalf("HEAD = %s, want %s", head, expected)
	}
	if status := runGitNarratorReview(t, repo, "status", "--porcelain"); strings.TrimSpace(status) != "" {
		t.Fatalf("worktree not clean after reset: %q", status)
	}
}

func openReviewNarratorDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := createNarratorTestSchema(sqlDB); err != nil {
		t.Fatalf("create narrator schema: %v", err)
	}
	stmts := []string{
		`CREATE TABLE chapters (id TEXT PRIMARY KEY, project_id TEXT, role TEXT, review_status TEXT, review_source_chapter_id TEXT, worktree_path TEXT, start_commit_sha TEXT, head_commit_sha TEXT, updated_at TEXT)`,
		`CREATE TABLE review_conclusions (id TEXT PRIMARY KEY, review_chapter_id TEXT NOT NULL, source_chapter_id TEXT NOT NULL, verdict TEXT NOT NULL, findings_json TEXT, created_at TEXT NOT NULL)`,
	}
	for _, stmt := range stmts {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("schema: %v\n%s", err, stmt)
		}
	}
	return sqlDB
}

func insertReviewChapters(t *testing.T, sqlDB *sql.DB, reviewStatus, worktree string) {
	t.Helper()
	if _, err := sqlDB.Exec(`INSERT INTO chapters (id, project_id, role, updated_at) VALUES ('source', 'p1', 'branch', 'now')`); err != nil {
		t.Fatalf("insert source chapter: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO chapters (id, project_id, role, review_status, review_source_chapter_id, worktree_path, updated_at) VALUES ('review', 'p1', 'review', ?, 'source', ?, 'now')`, reviewStatus, worktree); err != nil {
		t.Fatalf("insert review chapter: %v", err)
	}
}

func runGitNarratorReview(t *testing.T, cwd string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
	return string(out)
}

func writeNarratorReviewFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
