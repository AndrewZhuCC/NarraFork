package gitroutes

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"narrafork-go/internal/config"
	gitx "narrafork-go/internal/git"

	_ "modernc.org/sqlite"
)

func TestCommitRouteRecordsChapterCommitAndUpdatesChapter(t *testing.T) {
	requireGit(t)
	database := newGitRoutesTestDB(t)
	repo := newGitRoutesRepo(t)
	insertGitRoutesChapter(t, database, "c1", repo)

	writeGitRoutesFile(t, repo, "file.txt", "base\nchanged\n")
	runGitRoutesGit(t, repo, "add", "file.txt")

	body := serveGitRoutesRequest(t, NewHandler(database, gitx.NewService()), http.MethodPost, "/c1/git/commit", `{"message":"feat: update file"}`)
	sha, _ := body["commitSha"].(string)
	if sha == "" {
		t.Fatalf("commitSha missing: %#v", body)
	}
	commitSync := body["commitSync"].(map[string]any)
	if commitSync["recorded"] != true || commitSync["source"] != "manual" || commitSync["tsParity"] != true || commitSync["worktreeWatcher"] != true || commitSync["backgroundSync"] != true || commitSync["graphReadRefresh"] != true || commitSync["fallback"] != false || commitSync["syncEventContract"] != "git_status" {
		t.Fatalf("commit response missing stable commit-sync metadata: %#v", commitSync)
	}

	var storedSHA, message, source string
	var filesChanged, linesAdded int
	if err := database.QueryRow(`SELECT sha, message, source, COALESCE(files_changed, 0), COALESCE(lines_added, 0) FROM chapter_commits WHERE chapter_id = 'c1'`).Scan(&storedSHA, &message, &source, &filesChanged, &linesAdded); err != nil {
		t.Fatalf("select chapter commit: %v", err)
	}
	if storedSHA != sha || message != "feat: update file" || source != "manual" {
		t.Fatalf("unexpected commit row sha=%q message=%q source=%q", storedSHA, message, source)
	}
	if filesChanged != 1 || linesAdded == 0 {
		t.Fatalf("expected diff stats, filesChanged=%d linesAdded=%d", filesChanged, linesAdded)
	}

	var head sql.NullString
	var count int
	if err := database.QueryRow(`SELECT head_commit_sha, commit_count FROM chapters WHERE id = 'c1'`).Scan(&head, &count); err != nil {
		t.Fatalf("select chapter metadata: %v", err)
	}
	if !head.Valid || head.String != sha || count != 2 {
		t.Fatalf("chapter metadata head=%#v count=%d, want %s/2", head, count, sha)
	}
}

func TestCommitRouteReturnsErrorWhenMetadataUpdateFails(t *testing.T) {
	requireGit(t)
	database := newGitRoutesTestDB(t)
	repo := newGitRoutesRepo(t)
	insertGitRoutesChapter(t, database, "c1", repo)
	if _, err := database.Exec(`CREATE TRIGGER fail_commit_metadata_update BEFORE UPDATE OF head_commit_sha ON chapters BEGIN SELECT RAISE(ABORT, 'chapter metadata update blocked'); END;`); err != nil {
		t.Fatalf("create metadata trigger: %v", err)
	}
	writeGitRoutesFile(t, repo, "file.txt", "base\nchanged\n")
	runGitRoutesGit(t, repo, "add", "file.txt")

	w := serveGitRoutesRecorder(t, NewHandler(database, gitx.NewService()), http.MethodPost, "/c1/git/commit", `{"message":"feat: update file"}`)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected metadata update failure to return 500, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestResetRouteRefreshesChapterHeadMetadata(t *testing.T) {
	requireGit(t)
	database := newGitRoutesTestDB(t)
	repo := newGitRoutesRepo(t)
	baseSHA := runGitRoutesGit(t, repo, "rev-parse", "HEAD")
	writeGitRoutesFile(t, repo, "file.txt", "second\n")
	runGitRoutesGit(t, repo, "commit", "-am", "second change")
	secondSHA := runGitRoutesGit(t, repo, "rev-parse", "HEAD")
	if secondSHA == baseSHA {
		t.Fatalf("test setup did not create a second commit")
	}
	insertGitRoutesChapter(t, database, "c1", repo)
	if _, err := database.Exec(`UPDATE chapters SET commit_count = 2 WHERE id = 'c1'`); err != nil {
		t.Fatalf("seed commit count: %v", err)
	}

	body := serveGitRoutesRequest(t, NewHandler(database, gitx.NewService()), http.MethodPost, "/c1/git/reset", `{"target":"`+baseSHA+`","mode":"hard"}`)
	if body["hasChanges"] != false {
		t.Fatalf("reset response should include clean status summary: %#v", body)
	}

	var head sql.NullString
	var count int
	if err := database.QueryRow(`SELECT head_commit_sha, commit_count FROM chapters WHERE id = 'c1'`).Scan(&head, &count); err != nil {
		t.Fatalf("select chapter metadata: %v", err)
	}
	if !head.Valid || head.String != baseSHA || count != 1 {
		t.Fatalf("expected reset to refresh chapter metadata to %s/1, got head=%#v count=%d", baseSHA, head, count)
	}
}

func TestStatusDiffAndLogRoutesReturnFrontendShapes(t *testing.T) {
	requireGit(t)
	database := newGitRoutesTestDB(t)
	repo := newGitRoutesRepo(t)
	insertGitRoutesChapter(t, database, "c1", repo)
	writeGitRoutesFile(t, repo, "file.txt", "base\nchanged\n")

	status := serveGitRoutesRequest(t, NewHandler(database, gitx.NewService()), http.MethodGet, "/c1/git/status", "")
	if status["hasChanges"] != true {
		t.Fatalf("status should report dirty worktree: %#v", status)
	}
	if files, _ := status["files"].([]any); len(files) == 0 {
		t.Fatalf("status response should include files: %#v", status)
	}

	diff := serveGitRoutesRequest(t, NewHandler(database, gitx.NewService()), http.MethodGet, "/c1/git/diff?file=file.txt", "")
	if diff["diff"] == "" || diff["truncated"] != false {
		t.Fatalf("diff response missing diff/truncated contract: %#v", diff)
	}
	writeGitRoutesFile(t, repo, "new-file.txt", "new\n")
	untrackedDiff := serveGitRoutesRequest(t, NewHandler(database, gitx.NewService()), http.MethodGet, "/c1/git/diff?file=new-file.txt", "")
	if text, _ := untrackedDiff["diff"].(string); !strings.Contains(text, "+new") || untrackedDiff["truncated"] != false {
		t.Fatalf("untracked diff should match TS new-file shape: %#v", untrackedDiff)
	}

	log := serveGitRoutesArrayRequest(t, NewHandler(database, gitx.NewService()), http.MethodGet, "/c1/git/log?limit=5")
	if len(log) == 0 {
		t.Fatalf("log response should be a commit array: %#v", log)
	}
	entry := log[0].(map[string]any)
	if entry["sha"] == "" || entry["shortSha"] == "" || entry["author"] == "" || entry["date"] == "" {
		t.Fatalf("log response missing TS-compatible fields: %#v", entry)
	}
}

func TestMutatingGitRouteBroadcastsNarratorGitStatus(t *testing.T) {
	requireGit(t)
	database := newGitRoutesTestDB(t)
	repo := newGitRoutesRepo(t)
	insertGitRoutesChapter(t, database, "c1", repo)
	if _, err := database.Exec(`INSERT INTO narrators (id, chapter_id) VALUES ('nar-1', 'c1')`); err != nil {
		t.Fatalf("insert narrator: %v", err)
	}
	writeGitRoutesFile(t, repo, "file.txt", "base\nchanged\n")

	recorder := &gitRoutesBroadcastRecorder{}
	handler := NewHandler(database, gitx.NewService())
	handler.Broadcaster = recorder
	body := serveGitRoutesRequest(t, handler, http.MethodPost, "/c1/git/stage", `{"all":true}`)
	if body["hasChanges"] != true {
		t.Fatalf("stage response should include git status: %#v", body)
	}
	if len(recorder.events) != 1 {
		t.Fatalf("expected one broadcast, got %#v", recorder.events)
	}
	event := recorder.events[0]
	if event.narratorID != "nar-1" || event.payload["type"] != "git_status" || event.payload["chapterId"] != "c1" {
		t.Fatalf("unexpected broadcast: %#v", event)
	}
	if event.payload["baseBranch"] != "main" || event.payload["commitsAhead"] != 0 {
		t.Fatalf("unexpected chapter git fields: %#v", event.payload)
	}
	if _, ok := event.payload["linesAdded"].(int); !ok {
		t.Fatalf("expected line stats field in git_status event: %#v", event.payload)
	}
	status, ok := event.payload["status"].(map[string]any)
	if !ok || status["hasChanges"] != true {
		t.Fatalf("expected compact status object, got %#v", event.payload["status"])
	}
	if _, hasFiles := status["files"]; hasFiles {
		t.Fatalf("git_status event should omit files list, got %#v", status)
	}
}

func TestGitStatusBroadcastMarksBaseBranchQueryFallback(t *testing.T) {
	requireGit(t)
	database := newGitRoutesTestDB(t)
	repo := newGitRoutesRepo(t)
	insertGitRoutesChapter(t, database, "c1", repo)
	if _, err := database.Exec(`INSERT INTO narrators (id, chapter_id) VALUES ('nar-1', 'c1')`); err != nil {
		t.Fatalf("insert narrator: %v", err)
	}
	stmts := []string{
		`ALTER TABLE chapters RENAME TO chapters_real`,
		`CREATE VIEW chapters AS SELECT id, project_id, title, status, role, branch, worktree_path, head_commit_sha, commit_count, created_at, updated_at FROM chapters_real`,
	}
	for _, stmt := range stmts {
		if _, err := database.Exec(stmt); err != nil {
			t.Fatalf("replace chapters view: %v", err)
		}
	}
	recorder := &gitRoutesBroadcastRecorder{}
	handler := NewHandler(database, gitx.NewService())
	handler.Broadcaster = recorder

	handler.broadcastGitStatus(context.Background(), "c1", repo, "test")
	if len(recorder.events) != 1 {
		t.Fatalf("expected one broadcast, got %#v", recorder.events)
	}
	event := recorder.events[0]
	if event.payload["degraded"] != true {
		t.Fatalf("expected degraded git_status payload, got %#v", event.payload)
	}
	fallbacks, ok := event.payload["fallbacks"].([]string)
	if !ok || len(fallbacks) != 1 || fallbacks[0] != "chapter_base_branch_query_failed" {
		t.Fatalf("unexpected fallbacks: %#v", event.payload["fallbacks"])
	}
	if event.payload["baseBranch"] != "" || event.payload["commitsAhead"] != 0 {
		t.Fatalf("expected safe base branch fallback values, got %#v", event.payload)
	}
}

func TestStashApplyAndPopReturnConflictStatus(t *testing.T) {
	requireGit(t)
	database := newGitRoutesTestDB(t)
	repo := newGitRoutesRepo(t)
	insertGitRoutesChapter(t, database, "c1", repo)

	writeGitRoutesFile(t, repo, "file.txt", "stashed\n")
	pushBody := serveGitRoutesRequest(t, NewHandler(database, gitx.NewService()), http.MethodPost, "/c1/git/stash", `{"action":"push","message":"stash change"}`)
	if pushBody["hasConflicts"] != false {
		t.Fatalf("stash push should not conflict: %#v", pushBody)
	}

	applyBody := serveGitRoutesRequest(t, NewHandler(database, gitx.NewService()), http.MethodPost, "/c1/git/stash", `{"action":"apply","index":0}`)
	if applyBody["hasConflicts"] != false {
		t.Fatalf("stash apply should not conflict: %#v", applyBody)
	}
	status := applyBody["status"].(map[string]any)
	if status["hasChanges"] != true {
		t.Fatalf("apply response should include dirty status: %#v", applyBody)
	}

	runGitRoutesGit(t, repo, "restore", "file.txt")
	writeGitRoutesFile(t, repo, "file.txt", "local\n")
	runGitRoutesGit(t, repo, "commit", "-am", "local change")

	popBody := serveGitRoutesRequest(t, NewHandler(database, gitx.NewService()), http.MethodPost, "/c1/git/stash", `{"action":"pop"}`)
	if popBody["hasConflicts"] != true {
		t.Fatalf("stash pop should report conflict: %#v", popBody)
	}
	status = popBody["status"].(map[string]any)
	files := status["files"].([]any)
	if len(files) == 0 || files[0].(map[string]any)["status"] != "UU" {
		t.Fatalf("conflict status missing UU file: %#v", status)
	}
}

func TestAICommitMessageFallbackAndGeneratorPath(t *testing.T) {
	requireGit(t)
	database := newGitRoutesTestDB(t)
	repo := newGitRoutesRepo(t)
	insertGitRoutesChapter(t, database, "c1", repo)
	writeGitRoutesFile(t, repo, "file.txt", "base\nchanged\n")

	called := false
	fallbackBody := serveGitRoutesRequest(t, NewHandler(database, gitx.NewService()), http.MethodPost, "/c1/git/ai-commit-message", `{}`)
	if fallbackBody["message"] != "chore: update files" || called {
		t.Fatalf("fallback body=%#v called=%v", fallbackBody, called)
	}

	oldGenerate := generateCommitMessage
	t.Cleanup(func() { generateCommitMessage = oldGenerate })

	t.Setenv("HOME", t.TempDir())
	settings, err := config.Load()
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	if _, err := settings.Patch(map[string]any{"agent": map[string]any{"defaultModel": "openai:default-model", "summaryModel": "openai:summary-model"}}); err != nil {
		t.Fatalf("patch settings: %v", err)
	}
	generateCommitMessage = func(_ context.Context, gotSettings *config.Store, model, _, prompt, cwd string) (string, error) {
		called = true
		if gotSettings != settings || model != "openai:summary-model" || prompt == "" || cwd != repo {
			t.Fatalf("unexpected generator args settings=%p model=%q promptEmpty=%v cwd=%q", gotSettings, model, prompt == "", cwd)
		}
		return "\"fix: update file\"\nextra", nil
	}

	realBody := serveGitRoutesRequest(t, NewHandler(database, gitx.NewService(), settings), http.MethodPost, "/c1/git/ai-commit-message", `{}`)
	if !called || realBody["message"] != "fix: update file" {
		t.Fatalf("real provider body=%#v called=%v", realBody, called)
	}

	called = false
	generateCommitMessage = func(context.Context, *config.Store, string, string, string, string) (string, error) {
		called = true
		return "", errors.New("provider unavailable")
	}
	w := serveGitRoutesRecorder(t, NewHandler(database, gitx.NewService(), settings), http.MethodPost, "/c1/git/ai-commit-message", `{}`)
	if !called || w.Code != http.StatusInternalServerError {
		t.Fatalf("provider error should propagate with 500, called=%v status=%d body=%s", called, w.Code, w.Body.String())
	}
}

func newGitRoutesTestDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	stmts := []string{
		`CREATE TABLE chapters (id TEXT PRIMARY KEY, project_id TEXT, title TEXT, status TEXT, role TEXT, branch TEXT, worktree_path TEXT, base_branch TEXT, head_commit_sha TEXT, commit_count INTEGER, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE chapter_commits (id TEXT PRIMARY KEY, chapter_id TEXT, sha TEXT, message TEXT, full_message TEXT, author_name TEXT, author_email TEXT, authored_at TEXT, source TEXT, narrator_id TEXT, narrator_message_id TEXT, files_changed INTEGER, lines_added INTEGER, lines_removed INTEGER, created_at TEXT)`,
		`CREATE TABLE narrators (id TEXT PRIMARY KEY, chapter_id TEXT)`,
	}
	for _, stmt := range stmts {
		if _, err := database.Exec(stmt); err != nil {
			t.Fatalf("exec schema: %v", err)
		}
	}
	return database
}

func insertGitRoutesChapter(t *testing.T, database *sql.DB, id, repo string) {
	t.Helper()
	head := runGitRoutesGit(t, repo, "rev-parse", "HEAD")
	_, err := database.Exec(`INSERT INTO chapters (id, project_id, title, status, role, branch, worktree_path, base_branch, head_commit_sha, commit_count, created_at, updated_at) VALUES (?, 'p1', 'Chapter', 'active', 'branch', 'main', ?, 'main', ?, 1, 'now', 'now')`, id, repo, head)
	if err != nil {
		t.Fatalf("insert chapter: %v", err)
	}
}

type gitRoutesBroadcastEvent struct {
	narratorID string
	payload    map[string]any
}

type gitRoutesBroadcastRecorder struct {
	events []gitRoutesBroadcastEvent
}

func (r *gitRoutesBroadcastRecorder) BroadcastNarrator(narratorID string, payload any) {
	payloadMap, _ := payload.(map[string]any)
	r.events = append(r.events, gitRoutesBroadcastEvent{narratorID: narratorID, payload: payloadMap})
}

func serveGitRoutesRequest(t *testing.T, handler *Handler, method, target, body string) map[string]any {
	t.Helper()
	rec := serveGitRoutesRecorder(t, handler, method, target, body)
	if rec.Code < 200 || rec.Code >= 300 {
		t.Fatalf("%s %s returned %d: %s", method, target, rec.Code, rec.Body.String())
	}
	var decoded map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode response: %v\n%s", err, rec.Body.String())
	}
	return decoded
}

func serveGitRoutesRecorder(t *testing.T, handler *Handler, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	router := chi.NewRouter()
	handler.RegisterRoutes(router)
	req := httptest.NewRequest(method, target, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func serveGitRoutesArrayRequest(t *testing.T, handler *Handler, method, target string) []any {
	t.Helper()
	router := chi.NewRouter()
	handler.RegisterRoutes(router)
	req := httptest.NewRequest(method, target, nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code < 200 || rec.Code >= 300 {
		t.Fatalf("%s %s returned %d: %s", method, target, rec.Code, rec.Body.String())
	}
	var decoded []any
	if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode array response: %v\n%s", err, rec.Body.String())
	}
	return decoded
}

func newGitRoutesRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	runGitRoutesGit(t, repo, "init")
	runGitRoutesGit(t, repo, "checkout", "-b", "main")
	runGitRoutesGit(t, repo, "config", "user.name", "Test User")
	runGitRoutesGit(t, repo, "config", "user.email", "test@example.com")
	writeGitRoutesFile(t, repo, "file.txt", "base\n")
	runGitRoutesGit(t, repo, "add", ".")
	runGitRoutesGit(t, repo, "commit", "-m", "base")
	return repo
}

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
}

func runGitRoutesGit(t *testing.T, cwd string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE=2026-01-01T00:00:00Z", "GIT_COMMITTER_DATE=2026-01-01T00:00:00Z")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
	return string(bytes.TrimSpace(out))
}

func writeGitRoutesFile(t *testing.T, repo, name, content string) {
	t.Helper()
	path := filepath.Join(repo, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// Wave 299: Go git commit/reset/stash/stage validation must mirror the TypeScript
// gitCommitSchema/gitResetSchema/gitStashSchema/gitStageSchema Zod bounds and reject
// out-of-range values with 400.
func TestGitMutationRoutesValidateZodBoundsLikeTypeScript(t *testing.T) {
	requireGit(t)
	database := newGitRoutesTestDB(t)
	repo := newGitRoutesRepo(t)
	insertGitRoutesChapter(t, database, "c1", repo)
	handler := NewHandler(database, gitx.NewService())
	longMessage := strings.Repeat("m", 501)
	longTarget := strings.Repeat("t", 101)
	longStash := strings.Repeat("s", 201)
	for _, tc := range []struct {
		name   string
		target string
		body   string
	}{
		{"commit empty message", "/c1/git/commit", `{"message":"  "}`},
		{"commit overlong message", "/c1/git/commit", `{"message":"` + longMessage + `"}`},
		{"reset overlong target", "/c1/git/reset", `{"target":"` + longTarget + `","mode":"hard"}`},
		{"reset invalid mode", "/c1/git/reset", `{"target":"HEAD","mode":"mixed"}`},
		{"stash overlong message", "/c1/git/stash", `{"action":"push","message":"` + longStash + `"}`},
		{"stage neither files nor all", "/c1/git/stage", `{}`},
		{"discard neither files nor all", "/c1/git/discard", `{"files":[]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := serveGitRoutesRecorder(t, handler, http.MethodPost, tc.target, tc.body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("%q status = %d body=%s, want 400", tc.name, w.Code, w.Body.String())
			}
		})
	}
}

// Wave 305: Go git log route must mirror the TypeScript gitLogQuerySchema pagination
// (limit 1..200 default 50, skip >= 0 default 0) and apply the skip offset.
func TestGitLogRouteSupportsSkipPaginationAndValidatesBounds(t *testing.T) {
	requireGit(t)
	database := newGitRoutesTestDB(t)
	repo := newGitRoutesRepo(t)
	insertGitRoutesChapter(t, database, "c1", repo)
	// newGitRoutesRepo creates the "base" commit; add three more so we have four total.
	for _, subject := range []string{"second", "third", "fourth"} {
		writeGitRoutesFile(t, repo, "file.txt", subject+"\n")
		runGitRoutesGit(t, repo, "add", ".")
		runGitRoutesGit(t, repo, "commit", "-m", subject)
	}
	handler := NewHandler(database, gitx.NewService())

	all := serveGitRoutesArrayRequest(t, handler, http.MethodGet, "/c1/git/log?limit=10")
	if len(all) != 4 {
		t.Fatalf("expected 4 commits, got %d: %#v", len(all), all)
	}
	firstSubject, _ := all[0].(map[string]any)["message"].(string)
	if firstSubject != "fourth" {
		t.Fatalf("expected newest commit first, got %q", firstSubject)
	}

	// skip=1 should drop the newest commit; the new head becomes "third".
	skipped := serveGitRoutesArrayRequest(t, handler, http.MethodGet, "/c1/git/log?limit=10&skip=1")
	if len(skipped) != 3 {
		t.Fatalf("expected 3 commits after skip=1, got %d: %#v", len(skipped), skipped)
	}
	if subject, _ := skipped[0].(map[string]any)["message"].(string); subject != "third" {
		t.Fatalf("expected skip=1 to start at \"third\", got %q", subject)
	}

	// limit caps the returned count.
	limited := serveGitRoutesArrayRequest(t, handler, http.MethodGet, "/c1/git/log?limit=2")
	if len(limited) != 2 {
		t.Fatalf("expected 2 commits with limit=2, got %d", len(limited))
	}

	for _, tc := range []struct {
		name   string
		target string
	}{
		{"limit zero", "/c1/git/log?limit=0"},
		{"limit too large", "/c1/git/log?limit=201"},
		{"limit non-numeric", "/c1/git/log?limit=abc"},
		{"skip negative", "/c1/git/log?skip=-1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.target, nil)
			rec := httptest.NewRecorder()
			router := chi.NewRouter()
			handler.RegisterRoutes(router)
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("%q status = %d body=%s, want 400", tc.name, rec.Code, rec.Body.String())
			}
		})
	}
}
