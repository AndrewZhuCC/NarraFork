package graph

import (
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

	gitx "narrafork-go/internal/git"

	_ "modernc.org/sqlite"
)

func TestGraphRouteRefreshesGitAndReturnsLiveMetadata(t *testing.T) {
	sqlDB := newGraphTestDB(t)
	repoPath := newGraphGitRepo(t)
	head := gitOutput(t, repoPath, "rev-parse", "HEAD")

	_, err := sqlDB.Exec(`INSERT INTO chapters (id, project_id, title, status, branch, role, worktree_path, container_config, is_root, anchor_commit_sha, axis_offset, cross_offset, commit_count, head_commit_sha, panel_expanded, created_at, updated_at)
		VALUES
		('c1', 'p1', 'Chapter 1', 'active', 'main', 'branch', ?, NULL, 1, 'anchor1', 10, 20, 0, 'stale', 1, '2026-01-01', '2026-01-01'),
		('c2', 'p1', 'Chapter 2', 'active', 'feature', 'branch', NULL, NULL, 0, NULL, 0, 0, 4, 'cached', 0, '2026-01-01', '2026-01-01'),
		('c3', 'p1', 'Chapter 3', 'active', 'other', 'branch', NULL, '{"composeFile":"compose.yml"}', 0, NULL, 0, 0, 4, 'cached', 0, '2026-01-01', '2026-01-01'),
		('c4', 'p1', 'Chapter 4', 'active', 'removed', 'branch', NULL, NULL, 0, NULL, 0, 0, 0, NULL, 0, '2026-01-01', '2026-01-01')`, repoPath)
	if err != nil {
		t.Fatalf("insert chapters: %v", err)
	}
	_, err = sqlDB.Exec(`INSERT INTO narrators (id, chapter_id, status, substatus, message_version, created_at) VALUES
		('n1', 'c1', 'running', '["thinking"]', 7, '2026-01-01T00:00:00Z'),
		('n2', 'c1', 'idle', '[]', 3, '2026-01-02T00:00:00Z')`)
	if err != nil {
		t.Fatalf("insert narrators: %v", err)
	}
	_, err = sqlDB.Exec(`INSERT INTO container_instances (id, chapter_id, service_name, status, created_at, updated_at) VALUES
		('ci1', 'c1', 'app', 'running', 'now', 'now'),
		('ci2', 'c2', 'worker', 'paused', 'now', 'now'),
		('ci3', 'c3', 'db', 'stopped', 'now', 'now'),
		('ci4', 'c4', 'cache', 'removed', 'now', 'now')`)
	if err != nil {
		t.Fatalf("insert containers: %v", err)
	}
	_, err = sqlDB.Exec(`INSERT INTO terminals (id, chapter_id, name, status, graph_opened, graph_x, graph_y, graph_width, graph_height, created_at) VALUES
		('t1', 'c1', 'Shell', 'running', 1, 1, 2, 80, 24, 'now'),
		('t2', 'c1', 'Old Shell', 'exited', 1, 3, 4, 80, 24, 'now')`)
	if err != nil {
		t.Fatalf("insert terminals: %v", err)
	}
	_, err = sqlDB.Exec(`INSERT INTO chapter_edges (id, project_id, source_id, target_id, type, metadata, created_at) VALUES ('e1', 'p1', 'c1', 'c2', 'dependency', '{"lastSyncedCommit":"stale"}', 'now')`)
	if err != nil {
		t.Fatalf("insert dependency edge: %v", err)
	}

	body := serveGraphRequest(t, NewHandler(sqlDB, gitx.NewService()), http.MethodGet, "/p1/graph", "")
	if body["degraded"] != false {
		t.Fatalf("complete graph response should not be degraded: %#v", body)
	}
	nodes := body["nodes"].([]any)
	if len(nodes) != 4 {
		t.Fatalf("nodes len = %d, want 4; body=%#v", len(nodes), body)
	}
	byID := map[string]map[string]any{}
	for _, raw := range nodes {
		node := raw.(map[string]any)
		byID[node["id"].(string)] = node
	}
	c1Data := byID["c1"]["data"].(map[string]any)
	if c1Data["headCommitSha"] != head || c1Data["commitCount"] != float64(2) {
		t.Fatalf("expected refreshed git metadata, got head=%#v count=%#v", c1Data["headCommitSha"], c1Data["commitCount"])
	}
	if c1Data["hasContainers"] != true {
		t.Fatalf("running container instance should set hasContainers=true, got %#v", c1Data["hasContainers"])
	}
	if c1Data["narratorCount"] != float64(2) || c1Data["narratorId"] != "n1" || c1Data["narratorStatus"] != "running" {
		t.Fatalf("unexpected narrator summary: %#v", c1Data)
	}
	if c1Data["narratorMessageVersion"] != float64(7) || c1Data["messageVersion"] != float64(7) {
		t.Fatalf("expected narrator message version 7, got %#v", c1Data)
	}
	substatus := c1Data["narratorSubstatus"].([]any)
	if len(substatus) != 1 || substatus[0] != "thinking" {
		t.Fatalf("unexpected narratorSubstatus: %#v", substatus)
	}
	c2Data := byID["c2"]["data"].(map[string]any)
	if c2Data["hasContainers"] != true {
		t.Fatalf("paused container instance should set hasContainers=true, got %#v", c2Data["hasContainers"])
	}
	if c2Data["hasUpstreamUpdates"] != true {
		t.Fatalf("dependency edge should surface upstream updates, got %#v", c2Data["hasUpstreamUpdates"])
	}
	c3Data := byID["c3"]["data"].(map[string]any)
	if c3Data["hasContainers"] != true {
		t.Fatalf("stopped container instance should still count as present, got %#v", c3Data["hasContainers"])
	}
	c4Data := byID["c4"]["data"].(map[string]any)
	if c4Data["hasContainers"] != false {
		t.Fatalf("removed container instance should not count as present, got %#v", c4Data["hasContainers"])
	}
	capabilities := body["capabilities"].(map[string]any)
	split := capabilities["split"].(map[string]any)
	if split["supported"] != true || split["fallback"] != false || split["mode"] != "go-basic-split" {
		t.Fatalf("split capability should expose supported core split, got %#v", split)
	}
	if split["routes"].(map[string]any)["splitAtCommit"] != true {
		t.Fatalf("split capability should expose splitAtCommit route support, got %#v", split)
	}
	partials := split["partials"].(map[string]any)
	compressed := partials["compressedAISummary"].(map[string]any)
	if compressed["supported"] != false || compressed["fallback"] != true || compressed["mode"] != "deterministic-summary-placeholder" || compressed["reason"] != CompressedAISummaryFallbackReason {
		t.Fatalf("compressed split AI summary should be explicit fallback, got %#v", compressed)
	}
	containerAutostart := partials["containerAutostart"].(map[string]any)
	if containerAutostart["supported"] != true || containerAutostart["fallback"] != false || containerAutostart["mode"] != "background-start" {
		t.Fatalf("split container autostart capability mismatch: %#v", containerAutostart)
	}
	commitSync := capabilities["commitSync"].(map[string]any)
	if commitSync["supported"] != true || commitSync["manualRecordingSupported"] != true || commitSync["worktreeWatcher"] != true || commitSync["backgroundSync"] != true || commitSync["graphReadRefresh"] != true || commitSync["fallback"] != false || commitSync["mode"] != "manual-route-lightweight-graph-read-and-worktree-watcher" {
		t.Fatalf("commit-sync capability metadata missing: %#v", commitSync)
	}
	events := commitSync["events"].([]any)
	if len(events) != 3 || events[0] != "git_status" || events[1] != "commits_updated" || events[2] != "commit_sync_error" {
		t.Fatalf("commit-sync events should be explicit and stable, got %#v", events)
	}
	sources := commitSync["sources"].([]any)
	if len(sources) != 2 || sources[0] != "manual" || sources[1] != "watcher" {
		t.Fatalf("commit-sync sources should avoid read-path internal values, got %#v", sources)
	}
	upstream := capabilities["upstreamUpdates"].(map[string]any)
	if upstream["supported"] != true || upstream["fallback"] != false || upstream["field"] != "nodes[].data.hasUpstreamUpdates" {
		t.Fatalf("upstream update capability should be supported, got %#v", upstream)
	}
	edgeCaps := capabilities["chapterEdges"].(map[string]any)
	manualCreateTypes := edgeCaps["manualCreateTypes"].([]any)
	if len(manualCreateTypes) != 1 || manualCreateTypes[0] != "dependency" {
		t.Fatalf("chapter edge manual capability should only expose dependency, got %#v", edgeCaps)
	}

	opened := body["openedTerminals"].([]any)
	if len(opened) != 1 {
		t.Fatalf("opened terminals len = %d, want 1: %#v", len(opened), opened)
	}
	terminal := opened[0].(map[string]any)
	if terminal["id"] != "t1" || terminal["status"] != "running" {
		t.Fatalf("expected running terminal with status, got %#v", terminal)
	}

	var storedHead string
	var storedCount int
	if err := sqlDB.QueryRow(`SELECT head_commit_sha, commit_count FROM chapters WHERE id = 'c1'`).Scan(&storedHead, &storedCount); err != nil {
		t.Fatalf("load stored git metadata: %v", err)
	}
	if storedHead != head || storedCount != 2 {
		t.Fatalf("DB git metadata not refreshed: head=%q count=%d", storedHead, storedCount)
	}
	var syncedCommits int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM chapter_commits WHERE chapter_id = 'c1'`).Scan(&syncedCommits); err != nil {
		t.Fatalf("count graph synced commits: %v", err)
	}
	if syncedCommits != 0 {
		t.Fatalf("graph read refresh should not write commit rows, count=%d", syncedCommits)
	}
}

func TestGraphRouteOnlyUsesLightweightGitMetadataRefresh(t *testing.T) {
	sqlDB := newGraphTestDB(t)
	worktree := t.TempDir()
	if _, err := sqlDB.Exec(`INSERT INTO chapters (id, project_id, title, status, branch, role, worktree_path, is_root, axis_offset, cross_offset, commit_count, head_commit_sha, panel_expanded, created_at, updated_at)
		VALUES ('c1', 'p1', 'Chapter 1', 'active', 'main', 'branch', ?, 1, 1, 2, 0, 'stale', 0, 'now', 'now')`, worktree); err != nil {
		t.Fatalf("insert chapter: %v", err)
	}
	logPath := prependFakeGraphGit(t)

	body := serveGraphRequest(t, NewHandler(sqlDB, gitx.NewService()), http.MethodGet, "/p1/graph", "")
	if body["degraded"] != false {
		t.Fatalf("lightweight graph response should not be degraded: %#v", body)
	}
	nodes := body["nodes"].([]any)
	data := nodes[0].(map[string]any)["data"].(map[string]any)
	if data["headCommitSha"] != "abc123" || data["commitCount"] != float64(12) {
		t.Fatalf("expected fake head/count, got %#v", data)
	}
	commands := readGraphTestFile(t, logPath)
	if !strings.Contains(commands, "rev-parse HEAD") || !strings.Contains(commands, "rev-list --count HEAD") {
		t.Fatalf("graph read did not perform expected lightweight git commands:\n%s", commands)
	}
	for _, forbidden := range []string{"log ", "show ", "diff "} {
		if strings.Contains(commands, forbidden) {
			t.Fatalf("graph read should not run heavy git command %q:\n%s", forbidden, commands)
		}
	}
	var syncedCommits int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM chapter_commits WHERE chapter_id = 'c1'`).Scan(&syncedCommits); err != nil {
		t.Fatalf("count graph commit rows: %v", err)
	}
	if syncedCommits != 0 {
		t.Fatalf("lightweight graph read should not insert chapter_commits, count=%d", syncedCommits)
	}
}

func TestGraphRouteReportsFallbackWhenGitMetadataWritebackFails(t *testing.T) {
	sqlDB := newGraphTestDB(t)
	repoPath := newGraphGitRepo(t)
	head := gitOutput(t, repoPath, "rev-parse", "HEAD")
	if _, err := sqlDB.Exec(`INSERT INTO chapters (id, project_id, title, status, branch, role, worktree_path, is_root, axis_offset, cross_offset, commit_count, head_commit_sha, panel_expanded, created_at, updated_at)
		VALUES ('c1', 'p1', 'Chapter 1', 'active', 'main', 'branch', ?, 1, 1, 2, 0, 'stale', 0, 'now', 'now')`, repoPath); err != nil {
		t.Fatalf("insert chapter: %v", err)
	}
	if _, err := sqlDB.Exec(`CREATE TRIGGER fail_graph_git_metadata_update BEFORE UPDATE OF head_commit_sha ON chapters BEGIN SELECT RAISE(ABORT, 'graph git metadata write blocked'); END;`); err != nil {
		t.Fatalf("create metadata trigger: %v", err)
	}

	body := serveGraphRequest(t, NewHandler(sqlDB, gitx.NewService()), http.MethodGet, "/p1/graph", "")
	if body["degraded"] != true {
		t.Fatalf("expected graph response to be degraded: %#v", body)
	}
	nodes := body["nodes"].([]any)
	data := nodes[0].(map[string]any)["data"].(map[string]any)
	if data["headCommitSha"] != head || data["commitCount"] != float64(2) {
		t.Fatalf("response should still include live git metadata, got %#v", data)
	}
	fallbacks := body["fallbacks"].([]any)
	found := false
	for _, raw := range fallbacks {
		fallback := raw.(map[string]any)
		if fallback["reason"] == "git_metadata_refresh_failed" {
			found = true
			if !strings.Contains(fallback["error"].(string), "writeback failed") {
				t.Fatalf("expected writeback error detail, got %#v", fallback)
			}
		}
	}
	if !found {
		t.Fatalf("missing git metadata fallback in %#v", fallbacks)
	}
}

func TestGraphRouteReportsFallbacksForUnavailableOptionalMetadata(t *testing.T) {
	sqlDB := newGraphTestDB(t)
	if _, err := sqlDB.Exec(`INSERT INTO chapters (id, project_id, title, status, branch, role, is_root, axis_offset, cross_offset, commit_count, panel_expanded, created_at, updated_at)
		VALUES ('c1', 'p1', 'Chapter 1', 'active', 'main', 'branch', 1, 1, 2, 0, 0, 'now', 'now')`); err != nil {
		t.Fatalf("insert chapter: %v", err)
	}
	for _, table := range []string{"narrators", "container_instances", "exploration_groups", "terminals"} {
		if _, err := sqlDB.Exec(`DROP TABLE ` + table); err != nil {
			t.Fatalf("drop %s: %v", table, err)
		}
	}

	body := serveGraphRequest(t, NewHandler(sqlDB), http.MethodGet, "/p1/graph", "")
	if body["degraded"] != true {
		t.Fatalf("expected degraded graph response, got %#v", body)
	}
	nodes := body["nodes"].([]any)
	if len(nodes) != 1 {
		t.Fatalf("core graph nodes should still be returned, got %#v", nodes)
	}
	fallbacks := body["fallbacks"].([]any)
	reasons := map[string]bool{}
	for _, raw := range fallbacks {
		fallback := raw.(map[string]any)
		reasons[fallback["reason"].(string)] = true
		if _, ok := fallback["error"].(string); !ok {
			t.Fatalf("fallback should include error detail: %#v", fallback)
		}
	}
	for _, reason := range []string{"narrator_metadata_unavailable", "container_presence_unavailable", "exploration_groups_unavailable", "opened_terminals_unavailable"} {
		if !reasons[reason] {
			t.Fatalf("missing graph fallback reason %q in %#v", reason, fallbacks)
		}
	}
}

func TestUpdatePositionsRejectsChaptersOutsideProject(t *testing.T) {
	sqlDB := newGraphTestDB(t)
	_, err := sqlDB.Exec(`INSERT INTO chapters (id, project_id, title, status, branch, role, is_root, axis_offset, cross_offset, commit_count, panel_expanded, created_at, updated_at)
		VALUES
		('c1', 'p1', 'Chapter 1', 'active', 'main', 'branch', 1, 1, 2, 0, 0, 'now', 'now'),
		('c2', 'p2', 'Chapter 2', 'active', 'main', 'branch', 0, 3, 4, 0, 0, 'now', 'now')`)
	if err != nil {
		t.Fatalf("insert chapters: %v", err)
	}

	body := serveGraphRaw(t, NewHandler(sqlDB), http.MethodPatch, "/p1/graph/positions", `{"positions":[{"chapterId":"c1","axisOffset":10,"crossOffset":20},{"chapterId":"c2","axisOffset":30,"crossOffset":40}]}`)
	if body.Code != http.StatusBadRequest {
		t.Fatalf("PATCH should reject cross-project chapter, got status %d body %s", body.Code, body.Body.String())
	}
	if !strings.Contains(body.Body.String(), "Chapters not in project: c2") {
		t.Fatalf("expected ownership error, got %s", body.Body.String())
	}

	var axis, cross float64
	if err := sqlDB.QueryRow(`SELECT axis_offset, cross_offset FROM chapters WHERE id = 'c1'`).Scan(&axis, &cross); err != nil {
		t.Fatalf("load c1 position: %v", err)
	}
	if axis != 1 || cross != 2 {
		t.Fatalf("valid chapter should not be partially updated after validation failure, got axis=%v cross=%v", axis, cross)
	}
}

func newGraphTestDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	stmts := []string{
		`CREATE TABLE chapters (
			id TEXT PRIMARY KEY, project_id TEXT, title TEXT, status TEXT, branch TEXT, role TEXT,
			color TEXT, group_label TEXT, exploration_group_id TEXT, is_root INTEGER,
			anchor_commit_sha TEXT, axis_offset REAL, cross_offset REAL, commit_count INTEGER,
			head_commit_sha TEXT, panel_expanded INTEGER, panel_width REAL, panel_height REAL,
			worktree_path TEXT, container_config TEXT, review_source_chapter_id TEXT, review_status TEXT,
			created_at TEXT, updated_at TEXT
		)`,
		`CREATE TABLE narrators (
			id TEXT PRIMARY KEY, chapter_id TEXT, status TEXT, substatus TEXT, message_version INTEGER,
			created_at TEXT
		)`,
		`CREATE TABLE container_instances (
			id TEXT PRIMARY KEY, chapter_id TEXT, service_name TEXT, status TEXT, created_at TEXT, updated_at TEXT
		)`,
		`CREATE TABLE chapter_edges (id TEXT PRIMARY KEY, project_id TEXT, source_id TEXT, target_id TEXT, type TEXT, metadata TEXT, created_at TEXT)`,
		`CREATE TABLE chapter_commits (id TEXT PRIMARY KEY, chapter_id TEXT, sha TEXT, message TEXT, full_message TEXT, author_name TEXT, author_email TEXT, authored_at TEXT, source TEXT, narrator_id TEXT, narrator_message_id TEXT, files_changed INTEGER, lines_added INTEGER, lines_removed INTEGER, created_at TEXT)`,
		`CREATE TABLE exploration_groups (id TEXT PRIMARY KEY, project_id TEXT, title TEXT, description TEXT, base_chapter_id TEXT, status TEXT, decided_chapter_id TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE terminals (id TEXT PRIMARY KEY, chapter_id TEXT, name TEXT, status TEXT, graph_opened INTEGER, graph_x REAL, graph_y REAL, graph_width REAL, graph_height REAL, created_at TEXT)`,
	}
	for _, stmt := range stmts {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("create test schema: %v", err)
		}
	}
	return sqlDB
}

func newGraphGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatalf("write first file: %v", err)
	}
	runGit(t, dir, "add", "file.txt")
	runGit(t, dir, "commit", "-m", "first")
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("two\n"), 0o644); err != nil {
		t.Fatalf("write second file: %v", err)
	}
	runGit(t, dir, "add", "file.txt")
	runGit(t, dir, "commit", "-m", "second")
	return dir
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func prependFakeGraphGit(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "git.log")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "` + logPath + `"
if [ "$1" = "rev-parse" ] && [ "$2" = "HEAD" ]; then echo "abc123"; exit 0; fi
if [ "$1" = "rev-list" ] && [ "$2" = "--count" ] && [ "$3" = "HEAD" ]; then echo "12"; exit 0; fi
if [ "$1" = "log" ] || [ "$1" = "show" ] || [ "$1" = "diff" ]; then echo "heavy git command blocked" >&2; exit 97; fi
echo "unexpected git command: $*" >&2
exit 1
`
	path := filepath.Join(dir, "git")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake git: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

func readGraphTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func serveGraphRequest(t *testing.T, h *Handler, method, path, requestBody string) map[string]any {
	t.Helper()
	w := serveGraphRaw(t, h, method, path, requestBody)
	if w.Code != http.StatusOK {
		t.Fatalf("%s %s status = %d body = %s", method, path, w.Code, w.Body.String())
	}
	var decoded map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode response: %v\n%s", err, w.Body.String())
	}
	return decoded
}

func serveGraphRaw(t *testing.T, h *Handler, method, path, requestBody string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	h.RegisterRoutes(r)
	req := httptest.NewRequest(method, path, strings.NewReader(requestBody))
	if requestBody != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}
