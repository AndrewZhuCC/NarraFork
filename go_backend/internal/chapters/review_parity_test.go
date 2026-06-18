package chapters

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gitx "narrafork-go/internal/git"
	"narrafork-go/internal/narrators"

	_ "modernc.org/sqlite"
)

func TestCreateReviewIncludesDiffContextAndCopiesDirtyFiles(t *testing.T) {
	ctx := context.Background()
	repo := t.TempDir()
	runGitReview(t, repo, "init", "-b", "main")
	runGitReview(t, repo, "config", "user.email", "test@example.com")
	runGitReview(t, repo, "config", "user.name", "Test User")
	writeFileReview(t, filepath.Join(repo, "tracked.txt"), "base\n")
	runGitReview(t, repo, "add", "tracked.txt")
	runGitReview(t, repo, "commit", "-m", "initial")
	base := strings.TrimSpace(runGitReview(t, repo, "rev-parse", "HEAD"))
	writeFileReview(t, filepath.Join(repo, "committed.txt"), "committed change\n")
	runGitReview(t, repo, "add", "committed.txt")
	runGitReview(t, repo, "commit", "-m", "feature")
	head := strings.TrimSpace(runGitReview(t, repo, "rev-parse", "HEAD"))
	writeFileReview(t, filepath.Join(repo, "tracked.txt"), "base\nuncommitted change\n")
	writeFileReview(t, filepath.Join(repo, "untracked.txt"), "untracked review context\n")
	writeFileReview(t, filepath.Join(repo, "untracked-dir", "nested.txt"), "nested untracked review context\n")
	if err := os.Remove(filepath.Join(repo, "committed.txt")); err != nil {
		t.Fatalf("remove committed dirty file: %v", err)
	}

	sqlDB := openReviewChapterDB(t)
	defer sqlDB.Close()
	createChapterCommitTableForCreateLifecycle(t, sqlDB)
	insertReviewProjectAndSource(t, sqlDB, repo, base, head)

	autoStart := false
	reviewEvents := []map[string]any{}
	service := &Service{
		DB:        sqlDB,
		Git:       gitx.NewService(),
		Narrators: &narrators.Service{DB: sqlDB},
		BroadcastChapterLifecycle: func(payload any) {
			if event, ok := payload.(map[string]any); ok {
				reviewEvents = append(reviewEvents, event)
			}
		},
	}
	review, err := service.CreateReview(ctx, "source", ReviewInput{Locale: "en", AutoStart: &autoStart, UserID: "reviewer"})
	if err != nil {
		t.Fatalf("CreateReview: %v", err)
	}
	if review.WorktreePath == nil || strings.TrimSpace(*review.WorktreePath) == "" {
		t.Fatalf("review worktree missing: %#v", review)
	}
	// Mirror the TS auto title `Review: ${source.title.slice(0,160)}` — for the
	// "Source" fixture title this is exactly "Review: Source".
	if review.Title != "Review: Source" {
		t.Fatalf("review title = %q, want %q", review.Title, "Review: Source")
	}
	if review.HeadCommitSHA == nil || *review.HeadCommitSHA != head || review.CommitCount != 2 {
		t.Fatalf("review head/count = %#v/%d, want %s/2", review.HeadCommitSHA, review.CommitCount, head)
	}
	if !hasReviewLifecycleEvent(reviewEvents, "review:created", review.ID, "source", "p1") {
		t.Fatalf("missing review:created lifecycle event: %#v", reviewEvents)
	}
	var commitRows, manualCommitRows int
	if err := sqlDB.QueryRow(`SELECT COUNT(*), COUNT(CASE WHEN source = 'manual' THEN 1 END) FROM chapter_commits WHERE chapter_id = ?`, review.ID).Scan(&commitRows, &manualCommitRows); err != nil {
		t.Fatalf("query review commit history: %v", err)
	}
	if commitRows != 2 || manualCommitRows != 2 {
		t.Fatalf("review commit rows = %d manual=%d, want 2/2", commitRows, manualCommitRows)
	}
	var reviewHeadMessage string
	if err := sqlDB.QueryRow(`SELECT message FROM chapter_commits WHERE chapter_id = ? AND sha = ?`, review.ID, head).Scan(&reviewHeadMessage); err != nil {
		t.Fatalf("query review head commit message: %v", err)
	}
	if reviewHeadMessage != "feature" {
		t.Fatalf("review head commit message = %q, want feature", reviewHeadMessage)
	}
	worktree := *review.WorktreePath
	if data := readFileReview(t, filepath.Join(worktree, "tracked.txt")); !strings.Contains(data, "uncommitted change") {
		t.Fatalf("tracked dirty file not copied, got %q", data)
	}
	if data := readFileReview(t, filepath.Join(worktree, "untracked.txt")); !strings.Contains(data, "untracked review context") {
		t.Fatalf("untracked file not copied, got %q", data)
	}
	if data := readFileReview(t, filepath.Join(worktree, "untracked-dir", "nested.txt")); !strings.Contains(data, "nested untracked review context") {
		t.Fatalf("nested untracked dir file not copied, got %q", data)
	}
	if _, err := os.Stat(filepath.Join(worktree, "committed.txt")); !os.IsNotExist(err) {
		t.Fatalf("dirty deleted file should be removed from review worktree, err=%v", err)
	}

	var narratorID, systemPrompt, narratorStatus, narratorCWD string
	var messageCount int
	if err := sqlDB.QueryRow(`SELECT id, system_prompt, status, cwd, message_count FROM narrators WHERE chapter_id = ?`, review.ID).Scan(&narratorID, &systemPrompt, &narratorStatus, &narratorCWD, &messageCount); err != nil {
		t.Fatalf("load review narrator: %v", err)
	}
	if narratorCWD != worktree {
		t.Fatalf("review narrator cwd = %q, want %q", narratorCWD, worktree)
	}
	if narratorStatus != "idle" || narratorStatus == "working" || narratorStatus == "running" {
		t.Fatalf("review narrator should be seeded only, got status %q", narratorStatus)
	}
	if messageCount != 1 {
		t.Fatalf("review narrator message_count = %d, want 1", messageCount)
	}
	if !strings.Contains(systemPrompt, "ConcludeReview") || !strings.Contains(systemPrompt, "Code Changes to Review") || !strings.Contains(systemPrompt, "uncommitted change") {
		t.Fatalf("review narrator system prompt missing review context:\n%s", systemPrompt)
	}
	var role, contentText, createdBy string
	var seq int
	if err := sqlDB.QueryRow(`SELECT m.role, m.content_text, m.created_by, r.seq FROM narrator_message_refs r JOIN narrator_messages m ON m.id = r.message_id WHERE r.narrator_id = ? ORDER BY r.seq ASC LIMIT 1`, narratorID).Scan(&role, &contentText, &createdBy, &seq); err != nil {
		t.Fatalf("load seeded review message: %v", err)
	}
	if role != "user" || contentText != reviewStartMessage("en") || createdBy != "reviewer" || seq != 1 {
		t.Fatalf("unexpected seeded review message role=%q text=%q createdBy=%q seq=%d", role, contentText, createdBy, seq)
	}

	if committed, err := service.Git.DiffBetweenRefs(ctx, repo, base, head); err != nil || !strings.Contains(committed, "committed change") {
		t.Fatalf("direct committed diff = %q err=%v", committed, err)
	}
	diffContext := service.buildReviewDiffContext(ctx, repo, mustGetChapter(t, service, "source"), head)
	if !strings.Contains(diffContext, "committed.txt") || !strings.Contains(diffContext, "committed change") {
		t.Fatalf("committed diff missing from context:\n%s", diffContext)
	}
	if !strings.Contains(diffContext, "Uncommitted changes") || !strings.Contains(diffContext, "uncommitted change") || !strings.Contains(diffContext, "untracked.txt") {
		t.Fatalf("uncommitted diff missing from context:\n%s", diffContext)
	}
	prompt := buildReviewSystemPrompt(diffContext, "en")
	if !strings.Contains(prompt, "ConcludeReview") || !strings.Contains(prompt, "Code Changes to Review") {
		t.Fatalf("review prompt missing TS-compatible instructions:\n%s", prompt)
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

func TestCreateReviewRouteAutoStartsNarratorSession(t *testing.T) {
	ctx := context.Background()
	repo := t.TempDir()
	runGitReview(t, repo, "init", "-b", "main")
	runGitReview(t, repo, "config", "user.email", "test@example.com")
	runGitReview(t, repo, "config", "user.name", "Test User")
	writeFileReview(t, filepath.Join(repo, "tracked.txt"), "base\n")
	runGitReview(t, repo, "add", "tracked.txt")
	runGitReview(t, repo, "commit", "-m", "initial")
	base := strings.TrimSpace(runGitReview(t, repo, "rev-parse", "HEAD"))
	writeFileReview(t, filepath.Join(repo, "tracked.txt"), "base\nfeature\n")
	runGitReview(t, repo, "add", "tracked.txt")
	runGitReview(t, repo, "commit", "-m", "feature")
	head := strings.TrimSpace(runGitReview(t, repo, "rev-parse", "HEAD"))

	sqlDB := openReviewChapterDB(t)
	defer sqlDB.Close()
	insertReviewProjectAndSource(t, sqlDB, repo, base, head)
	started := make(chan string, 1)
	service := &Service{
		DB:  sqlDB,
		Git: gitx.NewService(),
		Narrators: &narrators.Service{DB: sqlDB, AgentRunner: func(_ context.Context, narratorID string) {
			started <- narratorID
		}},
	}
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/source/review", strings.NewReader(`{"locale":"en","autoStart":true}`))
	newChapterRouter(NewHandler(service)).ServeHTTP(recorder, req.WithContext(ctx))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("review status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode review response: %v", err)
	}
	runtimePayload, ok := body["reviewRuntime"].(map[string]any)
	if !ok {
		t.Fatalf("reviewRuntime missing from review response: %#v", body)
	}
	if runtimePayload["autoStartRequested"] != true || runtimePayload["autoStartSession"] != true || runtimePayload["startMode"] != "auto-start-background-session" {
		t.Fatalf("unexpected reviewRuntime auto-start payload: %#v", runtimePayload)
	}
	if _, ok := body["fallbackCode"]; ok {
		t.Fatalf("auto-started review should not return fallbackCode: %#v", body)
	}

	var narratorID, status, substatus, cwd string
	var messageCount int
	if err := sqlDB.QueryRow(`SELECT id, status, substatus, cwd, message_count FROM narrators WHERE chapter_id IN (SELECT id FROM chapters WHERE role = 'review')`).Scan(&narratorID, &status, &substatus, &cwd, &messageCount); err != nil {
		t.Fatalf("load review narrator: %v", err)
	}
	if status != "working" || !strings.Contains(substatus, "calling_model") || messageCount != 1 || strings.TrimSpace(cwd) == "" {
		t.Fatalf("review narrator should auto-start working/calling_model, status=%q substatus=%q messages=%d cwd=%q", status, substatus, messageCount, cwd)
	}
	select {
	case got := <-started:
		if got != narratorID {
			t.Fatalf("auto-start runner narrator = %q, want %q", got, narratorID)
		}
	case <-time.After(time.Second):
		t.Fatalf("review narrator auto-start runner was not invoked")
	}
	var role, contentText string
	var createdBy sql.NullString
	if err := sqlDB.QueryRow(`SELECT role, content_text, created_by FROM narrator_messages WHERE narrator_id = ?`, narratorID).Scan(&role, &contentText, &createdBy); err != nil {
		t.Fatalf("load review start message: %v", err)
	}
	if role != "user" || contentText != reviewStartMessage("en") || createdBy.Valid {
		t.Fatalf("auto-start review message = role %q text %q createdBy %#v", role, contentText, createdBy)
	}
	if count := countRowsReview(t, sqlDB, `SELECT COUNT(*) FROM narrator_messages WHERE role = 'assistant'`); count != 0 {
		t.Fatalf("review auto-start proof should not require assistant output in route fixture, count=%d", count)
	}
}

func TestCreateReviewRouteCanExplicitlySeedWithoutAutoStart(t *testing.T) {
	ctx := context.Background()
	repo := t.TempDir()
	runGitReview(t, repo, "init", "-b", "main")
	runGitReview(t, repo, "config", "user.email", "test@example.com")
	runGitReview(t, repo, "config", "user.name", "Test User")
	writeFileReview(t, filepath.Join(repo, "tracked.txt"), "base\n")
	runGitReview(t, repo, "add", "tracked.txt")
	runGitReview(t, repo, "commit", "-m", "initial")
	base := strings.TrimSpace(runGitReview(t, repo, "rev-parse", "HEAD"))
	writeFileReview(t, filepath.Join(repo, "tracked.txt"), "base\nfeature\n")
	runGitReview(t, repo, "add", "tracked.txt")
	runGitReview(t, repo, "commit", "-m", "feature")
	head := strings.TrimSpace(runGitReview(t, repo, "rev-parse", "HEAD"))

	sqlDB := openReviewChapterDB(t)
	defer sqlDB.Close()
	insertReviewProjectAndSource(t, sqlDB, repo, base, head)
	service := &Service{DB: sqlDB, Git: gitx.NewService(), Narrators: &narrators.Service{DB: sqlDB}}
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/source/review", strings.NewReader(`{"locale":"en","autoStart":false}`))
	newChapterRouter(NewHandler(service)).ServeHTTP(recorder, req.WithContext(ctx))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("review status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode review response: %v", err)
	}
	runtimePayload, ok := body["reviewRuntime"].(map[string]any)
	if !ok {
		t.Fatalf("reviewRuntime missing from review response: %#v", body)
	}
	if runtimePayload["autoStartRequested"] != false || runtimePayload["autoStartSession"] != false || runtimePayload["startMode"] != "seed-message-only" {
		t.Fatalf("unexpected reviewRuntime seed-only payload: %#v", runtimePayload)
	}

	var narratorID, status, substatus, cwd string
	var messageCount int
	if err := sqlDB.QueryRow(`SELECT id, status, substatus, cwd, message_count FROM narrators WHERE chapter_id IN (SELECT id FROM chapters WHERE role = 'review')`).Scan(&narratorID, &status, &substatus, &cwd, &messageCount); err != nil {
		t.Fatalf("load review narrator: %v", err)
	}
	if status != "idle" || substatus != "[]" || messageCount != 1 || strings.TrimSpace(cwd) == "" {
		t.Fatalf("review narrator should stay seed-only/idle after explicit opt-out, status=%q substatus=%q messages=%d cwd=%q", status, substatus, messageCount, cwd)
	}
	var role, contentText string
	var createdBy sql.NullString
	if err := sqlDB.QueryRow(`SELECT role, content_text, created_by FROM narrator_messages WHERE narrator_id = ?`, narratorID).Scan(&role, &contentText, &createdBy); err != nil {
		t.Fatalf("load seeded review message: %v", err)
	}
	if role != "user" || contentText != reviewStartMessage("en") || createdBy.Valid {
		t.Fatalf("seeded review message = role %q text %q createdBy %#v", role, contentText, createdBy)
	}
}

func TestCreateReviewRollsBackWhenInitializeNarratorFails(t *testing.T) {
	ctx := context.Background()
	repo := t.TempDir()
	runGitReview(t, repo, "init", "-b", "main")
	runGitReview(t, repo, "config", "user.email", "test@example.com")
	runGitReview(t, repo, "config", "user.name", "Test User")
	writeFileReview(t, filepath.Join(repo, "tracked.txt"), "base\n")
	runGitReview(t, repo, "add", "tracked.txt")
	runGitReview(t, repo, "commit", "-m", "initial")
	base := strings.TrimSpace(runGitReview(t, repo, "rev-parse", "HEAD"))
	writeFileReview(t, filepath.Join(repo, "tracked.txt"), "base\nfeature\n")
	runGitReview(t, repo, "add", "tracked.txt")
	runGitReview(t, repo, "commit", "-m", "feature")
	head := strings.TrimSpace(runGitReview(t, repo, "rev-parse", "HEAD"))

	sqlDB := openReviewChapterDB(t)
	defer sqlDB.Close()
	insertReviewProjectAndSource(t, sqlDB, repo, base, head)
	if _, err := sqlDB.Exec(`CREATE TRIGGER seed_review_rollback_child_rows AFTER INSERT ON narrators BEGIN
		INSERT INTO narrator_tool_calls (id, narrator_id) VALUES ('review-tool-call', NEW.id);
		INSERT INTO narrator_sidecars (id, narrator_id) VALUES ('review-sidecar', NEW.id);
		INSERT INTO narrator_goals (id, narrator_id) VALUES ('review-goal', NEW.id);
	END;`); err != nil {
		t.Fatalf("create rollback child-row trigger: %v", err)
	}
	if _, err := sqlDB.Exec(`CREATE TRIGGER fail_review_prompt_update BEFORE UPDATE OF system_prompt ON narrators BEGIN SELECT RAISE(ABORT, 'review prompt update blocked'); END;`); err != nil {
		t.Fatalf("create prompt trigger: %v", err)
	}

	service := &Service{DB: sqlDB, Git: gitx.NewService(), Narrators: &narrators.Service{DB: sqlDB}}
	_, err := service.CreateReview(ctx, "source", ReviewInput{Locale: "en"})
	if err == nil || !strings.Contains(err.Error(), "initialize review narrator system prompt") {
		t.Fatalf("expected initialize error, got %v", err)
	}

	if count := countRowsReview(t, sqlDB, `SELECT COUNT(*) FROM chapters WHERE role = 'review' OR review_source_chapter_id = 'source'`); count != 0 {
		t.Fatalf("review chapter should be rolled back, count=%d", count)
	}
	if count := countRowsReview(t, sqlDB, `SELECT COUNT(*) FROM chapter_edges WHERE type = 'review' OR source_id = 'source'`); count != 0 {
		t.Fatalf("review edge should be rolled back, count=%d", count)
	}
	if count := countRowsReview(t, sqlDB, `SELECT COUNT(*) FROM narrators`); count != 0 {
		t.Fatalf("review narrator should be rolled back, count=%d", count)
	}
	if count := countRowsReview(t, sqlDB, `SELECT COUNT(*) FROM narrator_messages`); count != 0 {
		t.Fatalf("review messages should be rolled back, count=%d", count)
	}
	if count := countRowsReview(t, sqlDB, `SELECT COUNT(*) FROM narrator_message_refs`); count != 0 {
		t.Fatalf("review message refs should be rolled back, count=%d", count)
	}
	for _, table := range []string{"narrator_tool_calls", "narrator_sidecars", "narrator_goals"} {
		if count := countRowsReview(t, sqlDB, `SELECT COUNT(*) FROM `+table); count != 0 {
			t.Fatalf("%s should be rolled back, count=%d", table, count)
		}
	}
	if branches := strings.TrimSpace(runGitReview(t, repo, "branch", "--list", "review/*")); branches != "" {
		t.Fatalf("review branch should be deleted, got %q", branches)
	}
	worktreesDir := filepath.Join(repo, ".worktrees")
	if entries, err := os.ReadDir(worktreesDir); err == nil && len(entries) > 0 {
		t.Fatalf("review worktree should be removed, entries=%d", len(entries))
	} else if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read worktrees dir: %v", err)
	}
}

func TestCreateReviewRollsBackWhenAutoStartFails(t *testing.T) {
	ctx := context.Background()
	repo := t.TempDir()
	runGitReview(t, repo, "init", "-b", "main")
	runGitReview(t, repo, "config", "user.email", "test@example.com")
	runGitReview(t, repo, "config", "user.name", "Test User")
	writeFileReview(t, filepath.Join(repo, "tracked.txt"), "base\n")
	runGitReview(t, repo, "add", "tracked.txt")
	runGitReview(t, repo, "commit", "-m", "initial")
	base := strings.TrimSpace(runGitReview(t, repo, "rev-parse", "HEAD"))
	writeFileReview(t, filepath.Join(repo, "tracked.txt"), "base\nfeature\n")
	runGitReview(t, repo, "add", "tracked.txt")
	runGitReview(t, repo, "commit", "-m", "feature")
	head := strings.TrimSpace(runGitReview(t, repo, "rev-parse", "HEAD"))

	sqlDB := openReviewChapterDB(t)
	defer sqlDB.Close()
	insertReviewProjectAndSource(t, sqlDB, repo, base, head)
	if _, err := sqlDB.Exec(`CREATE TRIGGER seed_review_start_failure_child_rows AFTER INSERT ON narrators BEGIN
		INSERT INTO narrator_tool_calls (id, narrator_id) VALUES ('review-tool-call', NEW.id);
		INSERT INTO narrator_sidecars (id, narrator_id) VALUES ('review-sidecar', NEW.id);
		INSERT INTO narrator_goals (id, narrator_id) VALUES ('review-goal', NEW.id);
	END;`); err != nil {
		t.Fatalf("create start-failure child-row trigger: %v", err)
	}
	if _, err := sqlDB.Exec(`CREATE TRIGGER fail_review_auto_start BEFORE UPDATE OF status ON narrators WHEN NEW.status = 'working' BEGIN SELECT RAISE(ABORT, 'review auto-start blocked'); END;`); err != nil {
		t.Fatalf("create auto-start trigger: %v", err)
	}

	service := &Service{DB: sqlDB, Git: gitx.NewService(), Narrators: &narrators.Service{DB: sqlDB}}
	_, err := service.CreateReview(ctx, "source", ReviewInput{Locale: "en", UserID: "reviewer"})
	if err == nil || !strings.Contains(err.Error(), "initialize review narrator auto-start message") {
		t.Fatalf("expected auto-start error, got %v", err)
	}

	if count := countRowsReview(t, sqlDB, `SELECT COUNT(*) FROM chapters WHERE role = 'review' OR review_source_chapter_id = 'source'`); count != 0 {
		t.Fatalf("review chapter should be rolled back after auto-start failure, count=%d", count)
	}
	if count := countRowsReview(t, sqlDB, `SELECT COUNT(*) FROM chapter_edges WHERE type = 'review' OR source_id = 'source'`); count != 0 {
		t.Fatalf("review edge should be rolled back after auto-start failure, count=%d", count)
	}
	for _, table := range []string{"narrators", "narrator_messages", "narrator_message_refs", "narrator_tool_calls", "narrator_sidecars", "narrator_goals"} {
		if count := countRowsReview(t, sqlDB, `SELECT COUNT(*) FROM `+table); count != 0 {
			t.Fatalf("%s should be rolled back after auto-start failure, count=%d", table, count)
		}
	}
	if branches := strings.TrimSpace(runGitReview(t, repo, "branch", "--list", "review/*")); branches != "" {
		t.Fatalf("review branch should be deleted after auto-start failure, got %q", branches)
	}
	worktreesDir := filepath.Join(repo, ".worktrees")
	if entries, err := os.ReadDir(worktreesDir); err == nil && len(entries) > 0 {
		t.Fatalf("review worktree should be removed after auto-start failure, entries=%d", len(entries))
	} else if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read worktrees dir: %v", err)
	}
}

func TestInitializeReviewNarratorReturnsPromptUpdateError(t *testing.T) {
	sqlDB := openReviewNarratorInitDB(t)
	defer sqlDB.Close()
	if _, err := sqlDB.Exec(`CREATE TRIGGER fail_review_prompt_update BEFORE UPDATE OF system_prompt ON narrators BEGIN SELECT RAISE(ABORT, 'review prompt update blocked'); END;`); err != nil {
		t.Fatalf("create prompt trigger: %v", err)
	}
	worktree := t.TempDir()
	service := &Service{DB: sqlDB, Git: gitx.NewService()}

	err := service.initializeReviewNarrator(context.Background(), "review-narrator", worktree, Chapter{WorktreePath: &worktree}, "", ReviewInput{Locale: "en"})
	if err == nil || !strings.Contains(err.Error(), "initialize review narrator system prompt") {
		t.Fatalf("expected prompt update error, got %v", err)
	}
}

func TestInitializeReviewNarratorReturnsStartMessageError(t *testing.T) {
	sqlDB := openReviewNarratorInitDB(t)
	defer sqlDB.Close()
	if _, err := sqlDB.Exec(`CREATE TRIGGER fail_review_start_message BEFORE INSERT ON narrator_messages WHEN NEW.role = 'user' BEGIN SELECT RAISE(ABORT, 'review start message blocked'); END;`); err != nil {
		t.Fatalf("create start message trigger: %v", err)
	}
	worktree := t.TempDir()
	service := &Service{DB: sqlDB, Git: gitx.NewService(), Narrators: &narrators.Service{DB: sqlDB}}
	autoStart := false

	err := service.initializeReviewNarrator(context.Background(), "review-narrator", worktree, Chapter{WorktreePath: &worktree}, "", ReviewInput{Locale: "en", AutoStart: &autoStart, UserID: "reviewer"})
	if err == nil || !strings.Contains(err.Error(), "initialize review narrator start message") {
		t.Fatalf("expected start message error, got %v", err)
	}
}

func openReviewChapterDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	stmts := []string{
		`CREATE TABLE projects (id TEXT PRIMARY KEY, name TEXT, git_path TEXT, default_branch TEXT, startup_script TEXT, copy_files TEXT, chapter_settings TEXT)`,
		`CREATE TABLE chapter_edges (id TEXT PRIMARY KEY, project_id TEXT, source_id TEXT, target_id TEXT, type TEXT, metadata TEXT, created_at TEXT)`,
		`CREATE TABLE chapters (
			id TEXT PRIMARY KEY, project_id TEXT, title TEXT, description TEXT, status TEXT, role TEXT,
			branch TEXT, worktree_path TEXT, base_branch TEXT, parent_chapter_id TEXT, fork_point TEXT,
			merged_into_chapter_id TEXT, merge_commit_sha TEXT, merge_strategy TEXT, pre_merge_target_sha TEXT,
			container_config TEXT, exploration_group_id TEXT, is_root INTEGER DEFAULT 0, head_commit_sha TEXT,
			start_commit_sha TEXT, commit_count INTEGER DEFAULT 0, color TEXT, group_label TEXT, pinned INTEGER DEFAULT 0,
			anchor_commit_sha TEXT, axis_offset REAL DEFAULT 0, cross_offset REAL DEFAULT 0, panel_expanded INTEGER DEFAULT 0,
			panel_width REAL, panel_height REAL, review_source_chapter_id TEXT, review_status TEXT,
			last_accessed_at TEXT, created_at TEXT, updated_at TEXT
		)`,
		`CREATE TABLE narrators (
			id TEXT PRIMARY KEY,
			chapter_id TEXT,
			api_conversation_id TEXT,
			fork_message_id TEXT,
			type TEXT,
			subagent_type TEXT,
			title TEXT,
			inherit_mode TEXT,
			parent_narrator_id TEXT,
			context_summary TEXT,
			model TEXT,
			system_prompt TEXT,
			permission_mode TEXT,
			previous_permission_mode TEXT,
			plan_file_id TEXT,
			reasoning_effort TEXT,
			fast_mode INTEGER DEFAULT 0,
			relaxed_plan INTEGER DEFAULT 0,
			plan_reflection_auto_approve_override TEXT DEFAULT 'inherit',
			danger_reflection_override TEXT DEFAULT 'inherit',
			message_count INTEGER DEFAULT 0,
			total_cost_usd REAL DEFAULT 0,
			last_message_at TEXT,
			status TEXT DEFAULT 'idle',
			substatus TEXT DEFAULT '[]',
			plan_mode INTEGER DEFAULT 0,
			cwd TEXT,
			prune_boundary_message_id TEXT,
			pruned_percent INTEGER,
			prune_enabled INTEGER DEFAULT 1,
			enabled_tools TEXT,
			error_message TEXT,
			todos_json TEXT,
			todos_tool_use_id TEXT,
			variant TEXT DEFAULT 'primary',
			traits TEXT DEFAULT '[]',
			is_background INTEGER DEFAULT 0,
			background_status TEXT,
			background_result TEXT,
			background_completed_at TEXT,
			is_ask_in_passing INTEGER DEFAULT 0,
			message_version INTEGER DEFAULT 0,
			created_at TEXT,
			updated_at TEXT
		)`,
		`CREATE TABLE narrator_messages (
			id TEXT PRIMARY KEY,
			narrator_id TEXT,
			parent_tool_use_id TEXT,
			role TEXT,
			content_json TEXT,
			content_text TEXT,
			command_text TEXT,
			created_by TEXT,
			created_at TEXT
		)`,
		`CREATE TABLE narrator_message_refs (
			id TEXT PRIMARY KEY,
			narrator_id TEXT,
			message_id TEXT,
			seq INTEGER,
			is_compact INTEGER DEFAULT 0,
			segment_compact_id TEXT
		)`,
		`CREATE TABLE narrator_tool_calls (id TEXT PRIMARY KEY, narrator_id TEXT)`,
		`CREATE TABLE narrator_sidecars (id TEXT PRIMARY KEY, narrator_id TEXT)`,
		`CREATE TABLE narrator_goals (id TEXT PRIMARY KEY, narrator_id TEXT)`,
	}
	for _, stmt := range stmts {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("schema: %v\n%s", err, stmt)
		}
	}
	return sqlDB
}

func openReviewNarratorInitDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	stmts := []string{
		`CREATE TABLE narrators (
			id TEXT PRIMARY KEY,
			chapter_id TEXT,
			api_conversation_id TEXT,
			fork_message_id TEXT,
			type TEXT,
			subagent_type TEXT,
			title TEXT,
			inherit_mode TEXT,
			parent_narrator_id TEXT,
			context_summary TEXT,
			model TEXT,
			system_prompt TEXT,
			permission_mode TEXT,
			previous_permission_mode TEXT,
			plan_file_id TEXT,
			reasoning_effort TEXT,
			fast_mode INTEGER DEFAULT 0,
			relaxed_plan INTEGER DEFAULT 0,
			plan_reflection_auto_approve_override TEXT DEFAULT 'inherit',
			danger_reflection_override TEXT DEFAULT 'inherit',
			message_count INTEGER DEFAULT 0,
			total_cost_usd REAL DEFAULT 0,
			last_message_at TEXT,
			status TEXT DEFAULT 'idle',
			substatus TEXT DEFAULT '[]',
			plan_mode INTEGER DEFAULT 0,
			cwd TEXT,
			prune_boundary_message_id TEXT,
			pruned_percent INTEGER,
			prune_enabled INTEGER DEFAULT 1,
			enabled_tools TEXT,
			error_message TEXT,
			todos_json TEXT,
			todos_tool_use_id TEXT,
			variant TEXT DEFAULT 'primary',
			traits TEXT DEFAULT '[]',
			is_background INTEGER DEFAULT 0,
			background_status TEXT,
			background_result TEXT,
			background_completed_at TEXT,
			is_ask_in_passing INTEGER DEFAULT 0,
			message_version INTEGER DEFAULT 0,
			created_at TEXT,
			updated_at TEXT
		)`,
		`CREATE TABLE narrator_messages (
			id TEXT PRIMARY KEY,
			narrator_id TEXT,
			parent_tool_use_id TEXT,
			role TEXT,
			content_json TEXT,
			content_text TEXT,
			command_text TEXT,
			created_by TEXT,
			created_at TEXT
		)`,
		`CREATE TABLE narrator_message_refs (
			id TEXT PRIMARY KEY,
			narrator_id TEXT,
			message_id TEXT,
			seq INTEGER,
			is_compact INTEGER DEFAULT 0,
			segment_compact_id TEXT
		)`,
		`INSERT INTO narrators (
			id, type, title, inherit_mode, model, permission_mode, status, substatus, plan_mode,
			cwd, variant, traits, is_background, is_ask_in_passing, message_version, message_count,
			total_cost_usd, fast_mode, relaxed_plan, plan_reflection_auto_approve_override,
			danger_reflection_override, created_at, updated_at
		) VALUES ('review-narrator', 'primary', 'Review', 'fresh', 'openai:gpt-test', 'default', 'idle', '[]', 0, '.', 'primary', '[]', 0, 0, 0, 0, 0, 0, 0, 'inherit', 'inherit', 'now', 'now')`,
	}
	for _, stmt := range stmts {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("schema: %v\n%s", err, stmt)
		}
	}
	return sqlDB
}

func insertReviewProjectAndSource(t *testing.T, sqlDB *sql.DB, repo, base, head string) {
	t.Helper()
	if _, err := sqlDB.Exec(`INSERT INTO projects (id, name, git_path, default_branch) VALUES ('p1', 'Project', ?, 'main')`, repo); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	_, err := sqlDB.Exec(`INSERT INTO chapters (
		id, project_id, title, status, role, branch, worktree_path, base_branch,
		head_commit_sha, start_commit_sha, commit_count, axis_offset, cross_offset,
		panel_expanded, created_at, updated_at
	) VALUES ('source', 'p1', 'Source', 'active', 'branch', 'main', ?, 'main', ?, ?, 1, 0, 0, 0, 'now', 'now')`, repo, head, base)
	if err != nil {
		t.Fatalf("insert source: %v", err)
	}
}

func mustGetChapter(t *testing.T, service *Service, id string) Chapter {
	t.Helper()
	chapter, err := service.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("get chapter: %v", err)
	}
	return chapter
}

func runGitReview(t *testing.T, cwd string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
	return string(out)
}

func writeFileReview(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readFileReview(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func countRowsReview(t *testing.T, sqlDB *sql.DB, query string) int {
	t.Helper()
	var count int
	if err := sqlDB.QueryRow(query).Scan(&count); err != nil {
		t.Fatalf("count rows: %v\n%s", err, query)
	}
	return count
}
