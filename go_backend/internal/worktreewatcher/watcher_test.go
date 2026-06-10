package worktreewatcher

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	gitx "narrafork-go/internal/git"

	_ "modernc.org/sqlite"
)

func TestPollOnceBroadcastsGitStatusAfterWorktreeChange(t *testing.T) {
	requireGit(t)
	database := newWatcherTestDB(t)
	repo := newWatcherRepo(t)
	insertWatcherChapter(t, database, "c1", repo)
	insertWatcherNarrator(t, database, "nar-1", "c1")
	recorder := &watcherBroadcastRecorder{}
	watcher := New(database, gitx.NewService(), recorder)

	if err := watcher.PollOnce(context.Background()); err != nil {
		t.Fatalf("initial poll: %v", err)
	}
	if len(recorder.events) != 0 {
		t.Fatalf("initial poll should not broadcast, got %#v", recorder.events)
	}

	writeWatcherFile(t, repo, "file.txt", "base\nchanged\n")
	if err := watcher.PollOnce(context.Background()); err != nil {
		t.Fatalf("second poll: %v", err)
	}
	if len(recorder.events) != 1 {
		t.Fatalf("expected one git_status broadcast, got %#v", recorder.events)
	}
	event := recorder.events[0]
	if event.narratorID != "nar-1" || event.payload["type"] != "git_status" || event.payload["chapterId"] != "c1" || event.payload["source"] != "worktree-watcher" {
		t.Fatalf("unexpected event: %#v", event)
	}
	status, ok := event.payload["status"].(map[string]any)
	if !ok || status["hasChanges"] != true {
		t.Fatalf("expected compact dirty status, got %#v", event.payload["status"])
	}
	if _, hasFiles := status["files"]; hasFiles {
		t.Fatalf("git_status broadcast should omit files: %#v", status)
	}
}

func TestPollOnceSyncsExternalCommitMetadata(t *testing.T) {
	requireGit(t)
	database := newWatcherTestDB(t)
	repo := newWatcherRepo(t)
	insertWatcherChapter(t, database, "c1", repo)
	insertWatcherNarrator(t, database, "nar-1", "c1")
	recorder := &watcherBroadcastRecorder{}
	watcher := New(database, gitx.NewService(), recorder)

	if err := watcher.PollOnce(context.Background()); err != nil {
		t.Fatalf("initial poll: %v", err)
	}
	writeWatcherFile(t, repo, "new.txt", "new\n")
	runWatcherGit(t, repo, "add", ".")
	runWatcherGit(t, repo, "commit", "-m", "feat: external commit")
	newHead := runWatcherGit(t, repo, "rev-parse", "HEAD")

	if err := watcher.PollOnce(context.Background()); err != nil {
		t.Fatalf("second poll: %v", err)
	}
	var storedHead sql.NullString
	var commitCount int
	if err := database.QueryRow(`SELECT head_commit_sha, commit_count FROM chapters WHERE id = 'c1'`).Scan(&storedHead, &commitCount); err != nil {
		t.Fatalf("select chapter metadata: %v", err)
	}
	if !storedHead.Valid || storedHead.String != newHead || commitCount < 2 {
		t.Fatalf("expected synced head/count, got head=%#v count=%d", storedHead, commitCount)
	}
	var syncedRows int
	if err := database.QueryRow(`SELECT COUNT(*) FROM chapter_commits WHERE chapter_id = 'c1' AND source = 'watcher'`).Scan(&syncedRows); err != nil {
		t.Fatalf("count synced commits: %v", err)
	}
	if syncedRows == 0 {
		t.Fatal("expected watcher to insert recent commit metadata")
	}
	if !recorder.hasEvent("nar-1", "commits_updated") {
		t.Fatalf("expected commits_updated broadcast, got %#v", recorder.events)
	}
}

func TestPollOnceSyncsExternalCommitsWithBoundedRecentLog(t *testing.T) {
	requireGit(t)
	database := newWatcherTestDB(t)
	repo := newWatcherRepo(t)
	insertWatcherChapter(t, database, "c1", repo)
	insertWatcherNarrator(t, database, "nar-1", "c1")
	recorder := &watcherBroadcastRecorder{}
	watcher := New(database, gitx.NewService(), recorder)

	if err := watcher.PollOnce(context.Background()); err != nil {
		t.Fatalf("initial poll: %v", err)
	}
	for i := 0; i < commitSyncLimit+5; i++ {
		writeWatcherFile(t, repo, filepath.Join("external", "file-"+string(rune('a'+i))+".txt"), "change\n")
		runWatcherGit(t, repo, "add", ".")
		runWatcherGit(t, repo, "commit", "-m", "feat: external bounded "+string(rune('a'+i)))
	}
	newHead := runWatcherGit(t, repo, "rev-parse", "HEAD")

	if err := watcher.PollOnce(context.Background()); err != nil {
		t.Fatalf("second poll: %v", err)
	}

	var storedHead sql.NullString
	var commitCount int
	if err := database.QueryRow(`SELECT head_commit_sha, commit_count FROM chapters WHERE id = 'c1'`).Scan(&storedHead, &commitCount); err != nil {
		t.Fatalf("select chapter metadata: %v", err)
	}
	if !storedHead.Valid || storedHead.String != newHead || commitCount != commitSyncLimit+6 {
		t.Fatalf("expected synced live head/count, got head=%#v count=%d", storedHead, commitCount)
	}
	var syncedRows int
	if err := database.QueryRow(`SELECT COUNT(*) FROM chapter_commits WHERE chapter_id = 'c1' AND source = 'watcher'`).Scan(&syncedRows); err != nil {
		t.Fatalf("count synced commits: %v", err)
	}
	if syncedRows != commitSyncLimit {
		t.Fatalf("watcher should cap recent commit inserts to %d, got %d", commitSyncLimit, syncedRows)
	}
	event, ok := recorder.event("nar-1", "commits_updated")
	if !ok || event.payload["newCount"] != commitSyncLimit {
		t.Fatalf("expected bounded commits_updated broadcast, got ok=%v event=%#v all=%#v", ok, event, recorder.events)
	}
}

func TestPollOnceBroadcastsCommitSyncErrorWhenMetadataUpdateFails(t *testing.T) {
	requireGit(t)
	database := newWatcherTestDB(t)
	repo := newWatcherRepo(t)
	insertWatcherChapter(t, database, "c1", repo)
	insertWatcherNarrator(t, database, "nar-1", "c1")
	recorder := &watcherBroadcastRecorder{}
	watcher := New(database, gitx.NewService(), recorder)

	if err := watcher.PollOnce(context.Background()); err != nil {
		t.Fatalf("initial poll: %v", err)
	}
	writeWatcherFile(t, repo, "new.txt", "new\n")
	runWatcherGit(t, repo, "add", ".")
	runWatcherGit(t, repo, "commit", "-m", "feat: external commit")
	if _, err := database.Exec(`CREATE TRIGGER fail_watcher_chapter_metadata BEFORE UPDATE OF head_commit_sha ON chapters BEGIN SELECT RAISE(ABORT, 'watcher metadata update blocked'); END;`); err != nil {
		t.Fatalf("create metadata trigger: %v", err)
	}

	if err := watcher.PollOnce(context.Background()); err != nil {
		t.Fatalf("second poll: %v", err)
	}

	event, ok := recorder.event("nar-1", "commit_sync_error")
	if !ok {
		t.Fatalf("expected commit_sync_error broadcast, got %#v", recorder.events)
	}
	if event.payload["code"] != "WORKTREE_WATCHER_COMMIT_SYNC_FAILED" || event.payload["backgroundSync"] != true || event.payload["fallback"] != true || event.payload["fatal"] != false {
		t.Fatalf("unexpected commit sync error payload: %#v", event.payload)
	}
	reason, _ := event.payload["reason"].(string)
	if reason == "" || event.payload["error"] != reason || event.payload["message"] != reason {
		t.Fatalf("commit sync error should expose reason/error/message, got %#v", event.payload)
	}
}

type watcherBroadcastEvent struct {
	narratorID string
	payload    map[string]any
}

type watcherBroadcastRecorder struct {
	events []watcherBroadcastEvent
}

func (r *watcherBroadcastRecorder) BroadcastNarrator(narratorID string, payload any) {
	payloadMap, _ := payload.(map[string]any)
	r.events = append(r.events, watcherBroadcastEvent{narratorID: narratorID, payload: payloadMap})
}

func (r *watcherBroadcastRecorder) hasEvent(narratorID, eventType string) bool {
	_, ok := r.event(narratorID, eventType)
	return ok
}

func (r *watcherBroadcastRecorder) event(narratorID, eventType string) (watcherBroadcastEvent, bool) {
	for _, event := range r.events {
		if event.narratorID == narratorID && event.payload["type"] == eventType {
			return event, true
		}
	}
	return watcherBroadcastEvent{}, false
}

func newWatcherTestDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	stmts := []string{
		`CREATE TABLE chapters (id TEXT PRIMARY KEY, status TEXT, worktree_path TEXT, base_branch TEXT, head_commit_sha TEXT, commit_count INTEGER, updated_at TEXT)`,
		`CREATE TABLE narrators (id TEXT PRIMARY KEY, chapter_id TEXT)`,
		`CREATE TABLE chapter_commits (id TEXT PRIMARY KEY, chapter_id TEXT, sha TEXT, message TEXT, full_message TEXT, author_name TEXT, author_email TEXT, authored_at TEXT, source TEXT, files_changed INTEGER, lines_added INTEGER, lines_removed INTEGER, created_at TEXT)`,
	}
	for _, stmt := range stmts {
		if _, err := database.Exec(stmt); err != nil {
			t.Fatalf("exec schema: %v", err)
		}
	}
	return database
}

func insertWatcherChapter(t *testing.T, database *sql.DB, id, repo string) {
	t.Helper()
	head := runWatcherGit(t, repo, "rev-parse", "HEAD")
	if _, err := database.Exec(`INSERT INTO chapters (id, status, worktree_path, base_branch, head_commit_sha, commit_count, updated_at) VALUES (?, 'active', ?, 'main', ?, 1, 'now')`, id, repo, head); err != nil {
		t.Fatalf("insert chapter: %v", err)
	}
}

func insertWatcherNarrator(t *testing.T, database *sql.DB, narratorID, chapterID string) {
	t.Helper()
	if _, err := database.Exec(`INSERT INTO narrators (id, chapter_id) VALUES (?, ?)`, narratorID, chapterID); err != nil {
		t.Fatalf("insert narrator: %v", err)
	}
}

func newWatcherRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	runWatcherGit(t, repo, "init")
	runWatcherGit(t, repo, "checkout", "-b", "main")
	runWatcherGit(t, repo, "config", "user.name", "Test User")
	runWatcherGit(t, repo, "config", "user.email", "test@example.com")
	writeWatcherFile(t, repo, "file.txt", "base\n")
	runWatcherGit(t, repo, "add", ".")
	runWatcherGit(t, repo, "commit", "-m", "base")
	return repo
}

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
}

func runWatcherGit(t *testing.T, cwd string, args ...string) string {
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

func writeWatcherFile(t *testing.T, repo, name, content string) {
	t.Helper()
	path := filepath.Join(repo, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}
