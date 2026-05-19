package misccompat

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"narrafork-go/internal/chapters"
	gitx "narrafork-go/internal/git"

	_ "modernc.org/sqlite"
)

func TestRulerForkSplitsHistoricalCommitAndReturnsNewForkChapter(t *testing.T) {
	repo, baseSHA, splitSHA, headSHA := newRulerSplitRepo(t)
	sqlDB := newRulerSplitDB(t)
	insertRulerSplitRoot(t, sqlDB, repo, baseSHA, headSHA)
	gitService := gitx.NewService()
	chapterService := &chapters.Service{DB: sqlDB, Git: gitService}
	handler := NewHandler(sqlDB, gitService, chapterService)

	created := serveRulerFork(t, handler, `/p1/ruler/fork`, `{"startCommitSha":"`+splitSHA+`"}`)
	if created.ID == "" || created.ID == "root" {
		t.Fatalf("ruler split should return the new fork chapter, got %#v", created)
	}
	if created.ParentChapterID == nil || strings.TrimSpace(*created.ParentChapterID) == "" {
		t.Fatalf("new fork parent missing: %#v", created)
	}
	prefixID := *created.ParentChapterID
	var rootParent sql.NullString
	var rootIsRoot int
	if err := sqlDB.QueryRow(`SELECT parent_chapter_id, is_root FROM chapters WHERE id = 'root'`).Scan(&rootParent, &rootIsRoot); err != nil {
		t.Fatalf("query root continuation: %v", err)
	}
	if !rootParent.Valid || rootParent.String != prefixID || rootIsRoot != 1 {
		t.Fatalf("root continuation parent/is_root = %#v/%d, want prefix/1", rootParent, rootIsRoot)
	}
	var prefixIsRoot int
	if err := sqlDB.QueryRow(`SELECT is_root FROM chapters WHERE id = ?`, prefixID).Scan(&prefixIsRoot); err != nil {
		t.Fatalf("query prefix: %v", err)
	}
	if prefixIsRoot != 0 {
		t.Fatalf("prefix is_root = %d, want 0", prefixIsRoot)
	}
	if got := rulerSplitEdgeCount(t, sqlDB, prefixID, "root"); got != 1 {
		t.Fatalf("prefix->root continuation edge count = %d, want 1", got)
	}
	if got := rulerSplitEdgeCount(t, sqlDB, prefixID, created.ID); got != 1 {
		t.Fatalf("prefix->new fork edge count = %d, want 1", got)
	}
	var chapterCount int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM chapters`).Scan(&chapterCount); err != nil {
		t.Fatalf("count chapters: %v", err)
	}
	if chapterCount != 3 {
		t.Fatalf("historical ruler fork should create prefix + new fork, chapter count=%d", chapterCount)
	}
}

func TestRulerForkAtHeadKeepsOrdinaryForkSemantics(t *testing.T) {
	repo, baseSHA, _, headSHA := newRulerSplitRepo(t)
	sqlDB := newRulerSplitDB(t)
	insertRulerSplitRoot(t, sqlDB, repo, baseSHA, headSHA)
	gitService := gitx.NewService()
	chapterService := &chapters.Service{DB: sqlDB, Git: gitService}
	handler := NewHandler(sqlDB, gitService, chapterService)

	created := serveRulerFork(t, handler, `/p1/ruler/fork`, `{"startCommitSha":"`+headSHA+`","title":"Head fork"}`)
	if created.ID == "" || created.ID == "root" {
		t.Fatalf("ordinary head fork should return created chapter, got %#v", created)
	}
	if created.ParentChapterID == nil || *created.ParentChapterID != "root" {
		t.Fatalf("head fork parent = %#v, want root", created.ParentChapterID)
	}
	var rootParent sql.NullString
	if err := sqlDB.QueryRow(`SELECT parent_chapter_id FROM chapters WHERE id = 'root'`).Scan(&rootParent); err != nil {
		t.Fatalf("query root parent: %v", err)
	}
	if rootParent.Valid {
		t.Fatalf("ordinary head fork should not rewrite root parent, got %#v", rootParent)
	}
	var chapterCount int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM chapters`).Scan(&chapterCount); err != nil {
		t.Fatalf("count chapters: %v", err)
	}
	if chapterCount != 2 {
		t.Fatalf("head ruler fork should create only one fork chapter, count=%d", chapterCount)
	}
}

func serveRulerFork(t *testing.T, h *Handler, path, body string) chapters.Chapter {
	t.Helper()
	router := chi.NewRouter()
	h.RegisterRuler(router)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("ruler fork status = %d body=%s", w.Code, w.Body.String())
	}
	var created chapters.Chapter
	if err := httpDecodeJSON(w, &created); err != nil {
		t.Fatalf("decode ruler fork: %v", err)
	}
	return created
}

func httpDecodeJSON(w *httptest.ResponseRecorder, dst any) error {
	return jsonDecoder(w.Body.Bytes(), dst)
}

func jsonDecoder(data []byte, dst any) error {
	return json.Unmarshal(data, dst)
}

func newRulerSplitRepo(t *testing.T) (repo, baseSHA, splitSHA, headSHA string) {
	t.Helper()
	repo = t.TempDir()
	runRulerSplitGit(t, repo, "init")
	runRulerSplitGit(t, repo, "config", "user.email", "test@example.com")
	runRulerSplitGit(t, repo, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(repo, "story.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatalf("write base: %v", err)
	}
	runRulerSplitGit(t, repo, "add", "story.txt")
	runRulerSplitGit(t, repo, "commit", "--date", "2026-02-01T00:00:00Z", "-m", "base")
	baseSHA = rulerSplitGitOutput(t, repo, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(repo, "story.txt"), []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatalf("write split: %v", err)
	}
	runRulerSplitGit(t, repo, "add", "story.txt")
	runRulerSplitGit(t, repo, "commit", "--date", "2026-02-02T00:00:00Z", "-m", "split")
	splitSHA = rulerSplitGitOutput(t, repo, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(repo, "story.txt"), []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatalf("write head: %v", err)
	}
	runRulerSplitGit(t, repo, "add", "story.txt")
	runRulerSplitGit(t, repo, "commit", "--date", "2026-02-03T00:00:00Z", "-m", "head")
	headSHA = rulerSplitGitOutput(t, repo, "rev-parse", "HEAD")
	return repo, baseSHA, splitSHA, headSHA
}

func newRulerSplitDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	stmts := []string{
		`CREATE TABLE projects (id TEXT PRIMARY KEY, name TEXT, git_path TEXT, default_branch TEXT, startup_script TEXT, copy_files TEXT, chapter_settings TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE chapters (id TEXT PRIMARY KEY, project_id TEXT, title TEXT, description TEXT, status TEXT, role TEXT, branch TEXT, worktree_path TEXT, base_branch TEXT, parent_chapter_id TEXT, fork_point TEXT, merged_into_chapter_id TEXT, merge_commit_sha TEXT, merge_strategy TEXT, pre_merge_target_sha TEXT, container_config TEXT, exploration_group_id TEXT, is_root INTEGER, head_commit_sha TEXT, start_commit_sha TEXT, commit_count INTEGER, color TEXT, group_label TEXT, pinned INTEGER, anchor_commit_sha TEXT, axis_offset REAL, cross_offset REAL, panel_expanded INTEGER, panel_width REAL, panel_height REAL, review_source_chapter_id TEXT, review_status TEXT, last_accessed_at TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE chapter_edges (id TEXT PRIMARY KEY, project_id TEXT, source_id TEXT, target_id TEXT, type TEXT, metadata TEXT, created_at TEXT)`,
		`CREATE TABLE narrators (id TEXT PRIMARY KEY, chapter_id TEXT, api_conversation_id TEXT, fork_message_id TEXT, type TEXT, subagent_type TEXT, title TEXT, inherit_mode TEXT, parent_narrator_id TEXT, context_summary TEXT, model TEXT, system_prompt TEXT, permission_mode TEXT, previous_permission_mode TEXT, plan_file_id TEXT, reasoning_effort TEXT, fast_mode INTEGER DEFAULT 0, relaxed_plan INTEGER DEFAULT 0, plan_reflection_auto_approve_override TEXT DEFAULT 'inherit', danger_reflection_override TEXT DEFAULT 'inherit', message_count INTEGER DEFAULT 0, total_cost_usd REAL DEFAULT 0, last_message_at TEXT, status TEXT DEFAULT 'idle', substatus TEXT DEFAULT '[]', plan_mode INTEGER DEFAULT 0, cwd TEXT, error_message TEXT, todos_json TEXT, todos_tool_use_id TEXT, prune_enabled INTEGER DEFAULT 1, enabled_tools TEXT, variant TEXT DEFAULT 'primary', traits TEXT DEFAULT '[]', is_background INTEGER DEFAULT 0, background_status TEXT, background_result TEXT, background_completed_at TEXT, is_ask_in_passing INTEGER DEFAULT 0, message_version INTEGER DEFAULT 0, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE narrator_messages (id TEXT PRIMARY KEY, narrator_id TEXT, sdk_message_uuid TEXT, parent_tool_use_id TEXT, role TEXT, content_json TEXT, content_text TEXT, model TEXT, commit_sha TEXT, command_text TEXT, created_by TEXT, created_at TEXT)`,
		`CREATE TABLE narrator_message_refs (id TEXT PRIMARY KEY, narrator_id TEXT, message_id TEXT, seq INTEGER, is_compact INTEGER DEFAULT 0, segment_compact_id TEXT)`,
	}
	for _, stmt := range stmts {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("schema: %v\n%s", err, stmt)
		}
	}
	return sqlDB
}

func insertRulerSplitRoot(t *testing.T, sqlDB *sql.DB, repo, baseSHA, headSHA string) {
	t.Helper()
	if _, err := sqlDB.Exec(`INSERT INTO projects (id, name, git_path, default_branch, created_at, updated_at) VALUES ('p1', 'Project', ?, 'master', 'now', 'now')`, repo); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO chapters (id, project_id, title, status, role, branch, worktree_path, base_branch, is_root, head_commit_sha, start_commit_sha, commit_count, axis_offset, cross_offset, panel_expanded, created_at, updated_at) VALUES ('root', 'p1', 'Root', 'active', 'trunk', 'master', ?, 'master', 1, ?, ?, 3, 0, 0, 0, 'now', 'now')`, repo, headSHA, baseSHA); err != nil {
		t.Fatalf("insert root chapter: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO narrators (id, chapter_id, type, title, inherit_mode, model, permission_mode, status, substatus, plan_mode, cwd, prune_enabled, variant, traits, is_background, is_ask_in_passing, message_version, created_at, updated_at) VALUES ('n_root', 'root', 'primary', 'Root narrator', 'fresh', 'test-model', 'default', 'idle', '[]', 0, ?, 1, 'primary', '[]', 0, 0, 0, '2026-02-01T00:00:00Z', '2026-02-01T00:00:00Z')`, repo); err != nil {
		t.Fatalf("insert root narrator: %v", err)
	}
}

func rulerSplitEdgeCount(t *testing.T, sqlDB *sql.DB, sourceID, targetID string) int {
	t.Helper()
	var count int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM chapter_edges WHERE source_id = ? AND target_id = ? AND type = 'fork'`, sourceID, targetID).Scan(&count); err != nil {
		t.Fatalf("count ruler split edge: %v", err)
	}
	return count
}

func runRulerSplitGit(t *testing.T, cwd string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, string(out))
	}
}

func rulerSplitGitOutput(t *testing.T, cwd string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, string(out))
	}
	return strings.TrimSpace(string(out))
}
