package misccompat

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"
)

func TestRunCompatGitLimitsOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake git script uses POSIX sh")
	}
	binDir := t.TempDir()
	path := filepath.Join(binDir, "git")
	script := "#!/bin/sh\ni=0\nwhile [ $i -lt 140000 ]; do printf x; i=$((i+1)); done\nexit 0\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake git: %v", err)
	}
	t.Setenv("PATH", binDir)

	stdout, stderr, err := runCompatGit(context.Background(), "log")
	if err != nil {
		t.Fatalf("fake git failed: %v stderr=%q", err, stderr)
	}
	if !strings.Contains(stdout, "[truncated]") {
		t.Fatalf("expected truncated stdout, got len=%d", len(stdout))
	}
	if len(stdout) > maxCompatGitOutputBytes+64 {
		t.Fatalf("stdout was not capped: len=%d", len(stdout))
	}
}

func TestRulerDataWithoutGitPathReturnsExplicitFallback(t *testing.T) {
	sqlDB := newRulerParityTestDB(t)
	h := NewHandler(sqlDB)
	body := serveRulerParityJSON(t, h, "/p1/ruler")
	if body["degraded"] != true {
		t.Fatalf("expected degraded ruler payload, got %#v", body)
	}
	if commits, ok := body["commits"].([]any); !ok || len(commits) != 0 {
		t.Fatalf("expected empty commits for missing git path, got %#v", body["commits"])
	}
	fallbacks, ok := body["fallbacks"].([]any)
	if !ok || len(fallbacks) != 1 {
		t.Fatalf("expected one explicit fallback, got %#v", body["fallbacks"])
	}
	fallback := fallbacks[0].(map[string]any)
	if fallback["reason"] != "project_git_path_missing" || fallback["feature"] != "ruler.gitLog" {
		t.Fatalf("unexpected missing-git fallback: %#v", fallback)
	}
	capabilities := body["capabilities"].(map[string]any)
	mutations := capabilities["mutations"].(map[string]any)
	fork := mutations["fork"].(map[string]any)
	if fork["supported"] != false || fork["fallback"] != true || fork["code"] != "FEATURE_DISABLED" {
		t.Fatalf("ruler fork should expose disabled mutation fallback without chapter service, got %#v", fork)
	}
	degradedRead := capabilities["degradedRead"].(map[string]any)
	fallbackReasons := degradedRead["fallbacks"].([]any)
	if len(fallbackReasons) != 3 || fallbackReasons[0] != "project_git_path_missing" {
		t.Fatalf("ruler degraded read reasons should be stable, got %#v", degradedRead)
	}
}

func TestRulerDataGitFailureReportsDegradedFallbacks(t *testing.T) {
	sqlDB := newRulerParityTestDB(t)
	if _, err := sqlDB.Exec(`UPDATE projects SET git_path = ?, default_branch = 'main' WHERE id = 'p1'`, t.TempDir()); err != nil {
		t.Fatalf("update project git path: %v", err)
	}
	h := NewHandler(sqlDB)
	body := serveRulerParityJSON(t, h, "/p1/ruler")
	if body["degraded"] != true {
		t.Fatalf("expected degraded ruler payload on git failure, got %#v", body)
	}
	fallbacks, ok := body["fallbacks"].([]any)
	if !ok || len(fallbacks) < 2 {
		t.Fatalf("expected git count/log fallback metadata, got %#v", body["fallbacks"])
	}
	reasons := map[string]bool{}
	for _, raw := range fallbacks {
		fallback := raw.(map[string]any)
		reasons[fallback["reason"].(string)] = true
		if _, ok := fallback["error"].(string); !ok && fallback["reason"] != "project_git_path_missing" {
			t.Fatalf("fallback should include git error detail: %#v", fallback)
		}
	}
	if !reasons["git_rev_list_failed"] || !reasons["git_log_failed"] {
		t.Fatalf("missing expected fallback reasons: %#v", fallbacks)
	}
}

func TestRulerProjectChaptersResolvesNarratorFromNarratorsTable(t *testing.T) {
	sqlDB := newRulerParityTestDB(t)
	h := NewHandler(sqlDB)

	items, err := h.rulerProjectChapters(context.Background(), "p1")
	if err != nil {
		t.Fatalf("rulerProjectChapters returned error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 non-root project chapters, got %d: %#v", len(items), items)
	}

	first := items[0]
	if got := stringMap(first, "id", ""); got != "c1" {
		t.Fatalf("first chapter id = %q, want c1", got)
	}
	if got := stringMap(first, "narratorId", ""); got != "n-primary" {
		t.Fatalf("narratorId = %q, want n-primary", got)
	}
	if got := stringMap(first, "narratorStatus", ""); got != "working" {
		t.Fatalf("narratorStatus = %q, want working", got)
	}

	second := items[1]
	if got := stringMap(second, "id", ""); got != "c2" {
		t.Fatalf("second chapter id = %q, want c2", got)
	}
	if got := stringMap(second, "narratorId", ""); got != "" {
		t.Fatalf("chapter without narrator should have empty narratorId, got %q", got)
	}
	if got := stringMap(second, "narratorStatus", ""); got != "" {
		t.Fatalf("chapter without narrator should have empty narratorStatus, got %q", got)
	}
}

func serveRulerParityJSON(t *testing.T, h *Handler, path string) map[string]any {
	t.Helper()
	router := chi.NewRouter()
	h.RegisterRuler(router)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ruler request status = %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode ruler body: %v", err)
	}
	return body
}

func newRulerParityTestDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	stmts := []string{
		`CREATE TABLE projects (
			id TEXT PRIMARY KEY,
			git_path TEXT,
			default_branch TEXT
		)`,
		`CREATE TABLE chapters (
			id TEXT PRIMARY KEY,
			project_id TEXT,
			title TEXT,
			status TEXT,
			branch TEXT,
			role TEXT,
			parent_chapter_id TEXT,
			start_commit_sha TEXT,
			merge_commit_sha TEXT,
			head_commit_sha TEXT,
			anchor_commit_sha TEXT,
			axis_offset REAL,
			cross_offset REAL,
			panel_width REAL,
			panel_height REAL,
			color TEXT,
			review_source_chapter_id TEXT,
			review_status TEXT,
			is_root INTEGER,
			created_at TEXT
		)`,
		`CREATE TABLE narrators (
			id TEXT PRIMARY KEY,
			chapter_id TEXT,
			variant TEXT,
			type TEXT,
			status TEXT,
			created_at TEXT
		)`,
		`INSERT INTO projects (id, git_path, default_branch)
			VALUES ('p1', NULL, 'main'), ('p2', NULL, 'main')`,
		`INSERT INTO chapters (id, project_id, title, status, branch, role, is_root, created_at)
			VALUES
			('root', 'p1', 'Root', 'active', 'main', 'trunk', 1, '2026-01-01T00:00:00Z'),
			('c1', 'p1', 'Branch', 'active', 'feature', 'branch', 0, '2026-01-02T00:00:00Z'),
			('c2', 'p1', 'No Narrator', 'active', 'feature-2', 'branch', 0, '2026-01-03T00:00:00Z'),
			('other', 'p2', 'Other Project', 'active', 'other', 'branch', 0, '2026-01-04T00:00:00Z')`,
		`INSERT INTO narrators (id, chapter_id, variant, type, status, created_at)
			VALUES
			('n-sub', 'c1', 'subagent:review', 'subagent', 'idle', '2026-01-01T00:00:00Z'),
			('n-primary', 'c1', 'primary', 'primary', 'working', '2026-01-02T00:00:00Z')`,
	}
	for _, stmt := range stmts {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("exec test schema: %v\n%s", err, stmt)
		}
	}
	return sqlDB
}
