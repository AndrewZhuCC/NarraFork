package systemroutes

import (
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

	"narrafork-go/internal/httpx"
)

type fakeRuntimeCleanupTerminalRuntime struct {
	orphans       []string
	killed        []string
	killedOrphans []string
	ensured       []string
}

func (f *fakeRuntimeCleanupTerminalRuntime) ShellPID(string) (int, bool) { return 0, false }

func (f *fakeRuntimeCleanupTerminalRuntime) Kill(_ context.Context, terminalID string) error {
	f.killed = append(f.killed, terminalID)
	return nil
}

func (f *fakeRuntimeCleanupTerminalRuntime) OrphanIDs(context.Context) []string {
	return append([]string{}, f.orphans...)
}

func (f *fakeRuntimeCleanupTerminalRuntime) KillOrphan(_ context.Context, terminalID string) error {
	f.killedOrphans = append(f.killedOrphans, terminalID)
	return nil
}

func (f *fakeRuntimeCleanupTerminalRuntime) Ensure(_ context.Context, terminalID string) error {
	f.ensured = append(f.ensured, terminalID)
	return nil
}

type fakeTerminalRuntimeWithoutDetachedAdmin struct{}

func (f fakeTerminalRuntimeWithoutDetachedAdmin) ShellPID(string) (int, bool)        { return 0, false }
func (f fakeTerminalRuntimeWithoutDetachedAdmin) Kill(context.Context, string) error { return nil }

func TestRuntimeCleanupRejectsOversizedOptionalJSONBody(t *testing.T) {
	h := &Handler{TerminalRuntime: &fakeRuntimeCleanupTerminalRuntime{}}
	req := httptest.NewRequest(http.MethodPost, "/runtime/cleanup", strings.NewReader(strings.Repeat("x", 4*1024*1024+1)))
	w := httptest.NewRecorder()
	err := h.cleanupRuntime(w, req)
	var appErr *httpx.AppError
	if !errors.As(err, &appErr) || appErr.Code != "REQUEST_BODY_TOO_LARGE" {
		t.Fatalf("expected REQUEST_BODY_TOO_LARGE, got %T %v", err, err)
	}
}

func TestRuntimeScanReportsBrowserRuntimeAndPersistedSessions(t *testing.T) {
	sqlDB := newRuntimeCleanupTestDB(t)
	defer sqlDB.Close()
	mustExec(t, sqlDB, `INSERT INTO narrator_browser_sessions (id, status, updated_at, closed_at) VALUES ('browser-active', 'active', 'old', NULL), ('browser-running', 'running', 'old', NULL), ('browser-closed', 'closed', 'old', 'old');`)
	h := &Handler{DB: sqlDB, TerminalRuntime: &fakeRuntimeCleanupTerminalRuntime{}}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/runtime/scan", nil)
	if err := h.scanRuntime(w, req); err != nil {
		t.Fatalf("scan runtime: %v", err)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("runtime scan status = %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode runtime scan: %v", err)
	}
	browsers, _ := body["browsers"].(map[string]any)
	if browsers["supported"] != true || browsers["fallback"] != false || browsers["runtime"] != "go-rod" || browsers["activeSessions"] != float64(2) || browsers["persistedSessions"] != float64(2) || browsers["sessionApiSupported"] != true || browsers["cleanupSupported"] != true || browsers["dbTableAvailable"] != true {
		t.Fatalf("browser runtime payload should expose supported Go Browser runtime and persisted sessions, got %#v", browsers)
	}
}

func TestAdminTerminalsReportsRuntimeOrphanSockets(t *testing.T) {
	sqlDB := newRuntimeCleanupTestDB(t)
	mustExec(t, sqlDB, `INSERT INTO terminals (id, dtach_socket, status, exit_code) VALUES ('socket-a', '/tmp/narrafork/socket-a.dtach', 'orphan', NULL), ('running-a', NULL, 'running', NULL);`)
	runtime := &fakeRuntimeCleanupTerminalRuntime{orphans: []string{"socket-a", "runtime-only", "socket-a", ""}}
	h := &Handler{DB: sqlDB, TerminalRuntime: runtime}

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/admin/terminals", nil)
	if err := h.adminTerminals(w, req); err != nil {
		t.Fatalf("list admin terminals: %v", err)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("admin terminals status = %d body=%s", w.Code, w.Body.String())
	}
	var body struct {
		OrphanSockets []struct {
			TerminalID string `json:"terminalId"`
			SocketPath string `json:"socketPath"`
		} `json:"orphanSockets"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode admin terminals: %v", err)
	}
	if len(body.OrphanSockets) != 2 {
		t.Fatalf("orphan socket count = %d body=%s", len(body.OrphanSockets), w.Body.String())
	}
	if body.OrphanSockets[0].TerminalID != "socket-a" || body.OrphanSockets[0].SocketPath != "/tmp/narrafork/socket-a.dtach" {
		t.Fatalf("first orphan socket mismatch: %#v", body.OrphanSockets[0])
	}
	if body.OrphanSockets[1].TerminalID != "runtime-only" || body.OrphanSockets[1].SocketPath != "" {
		t.Fatalf("runtime-only orphan socket mismatch: %#v", body.OrphanSockets[1])
	}
}

func TestAdminTerminalDetachedActionsUseRuntimeInsteadOfFakeSuccess(t *testing.T) {
	sqlDB := newRuntimeCleanupTestDB(t)
	mustExec(t, sqlDB, `INSERT INTO terminals (id, status, exit_code) VALUES ('orphan-term', 'orphan', NULL);`)
	runtime := &fakeRuntimeCleanupTerminalRuntime{}
	h := &Handler{DB: sqlDB, TerminalRuntime: runtime}

	reconnectReq := httptest.NewRequest(http.MethodPost, "/api/admin/terminals/orphan-term/reattach", nil)
	reconnectReq = withChiParam(reconnectReq, "id", "orphan-term")
	reconnect := httptest.NewRecorder()
	if err := h.reattachTerminal(reconnect, reconnectReq); err != nil {
		t.Fatalf("reattach terminal: %v", err)
	}
	if reconnect.Code != http.StatusOK || strings.Join(runtime.ensured, ",") != "orphan-term" {
		t.Fatalf("reattach did not call runtime: status=%d body=%s ensured=%#v", reconnect.Code, reconnect.Body.String(), runtime.ensured)
	}

	reconnectOrphanReq := httptest.NewRequest(http.MethodPost, "/api/admin/terminals/reattach-orphan", strings.NewReader(`{"terminalId":"socket-only"}`))
	reconnectOrphanReq.Header.Set("Content-Type", "application/json")
	reconnectOrphan := httptest.NewRecorder()
	if err := h.reattachOrphanTerminal(reconnectOrphan, reconnectOrphanReq); err != nil {
		t.Fatalf("reattach orphan terminal: %v", err)
	}
	if reconnectOrphan.Code != http.StatusOK || strings.Join(runtime.ensured, ",") != "orphan-term,socket-only" {
		t.Fatalf("reattach orphan did not call runtime: status=%d body=%s ensured=%#v", reconnectOrphan.Code, reconnectOrphan.Body.String(), runtime.ensured)
	}

	killReq := httptest.NewRequest(http.MethodPost, "/api/admin/terminals/kill-orphan", strings.NewReader(`{"terminalId":"orphan-term"}`))
	killReq.Header.Set("Content-Type", "application/json")
	kill := httptest.NewRecorder()
	if err := h.killOrphanTerminal(kill, killReq); err != nil {
		t.Fatalf("kill orphan terminal: %v", err)
	}
	if kill.Code != http.StatusOK || strings.Join(runtime.killedOrphans, ",") != "orphan-term" {
		t.Fatalf("kill orphan did not call runtime: status=%d body=%s killed=%#v", kill.Code, kill.Body.String(), runtime.killedOrphans)
	}
	if got := scalarString(t, sqlDB, `SELECT status FROM terminals WHERE id = 'orphan-term'`); got != "exited" {
		t.Fatalf("orphan terminal status = %q", got)
	}
}

func TestAdminTerminalDetachedActionsReturnUnsupportedWhenRuntimeCannotAct(t *testing.T) {
	h := &Handler{TerminalRuntime: fakeTerminalRuntimeWithoutDetachedAdmin{}}

	reconnectReq := httptest.NewRequest(http.MethodPost, "/api/admin/terminals/orphan-term/reattach", nil)
	reconnectReq = withChiParam(reconnectReq, "id", "orphan-term")
	reconnect := httptest.NewRecorder()
	if err := h.reattachTerminal(reconnect, reconnectReq); err != nil {
		t.Fatalf("reattach terminal: %v", err)
	}
	assertTerminalUnsupported(t, reconnect, "admin.terminal.reattach")

	killReq := httptest.NewRequest(http.MethodPost, "/api/admin/terminals/kill-orphan", strings.NewReader(`{"terminalId":"orphan-term"}`))
	killReq.Header.Set("Content-Type", "application/json")
	kill := httptest.NewRecorder()
	if err := h.killOrphanTerminal(kill, killReq); err != nil {
		t.Fatalf("kill orphan terminal: %v", err)
	}
	assertTerminalUnsupported(t, kill, "admin.terminal.killOrphan")
}

func TestRuntimeCleanupDryRunReportsScopedRemovedWithoutSideEffects(t *testing.T) {
	sqlDB := newRuntimeCleanupTestDB(t)
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".worktrees", "dormant"), 0o755); err != nil {
		t.Fatalf("mkdir dormant worktree: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".worktrees", "orphan"), 0o755); err != nil {
		t.Fatalf("mkdir orphan worktree: %v", err)
	}
	mustExec(t, sqlDB, `INSERT INTO projects (id, git_path) VALUES ('p1', ?);`, repo)
	mustExec(t, sqlDB, `INSERT INTO chapters (id, project_id, status, worktree_path) VALUES ('active-ch', 'p1', 'active', ?), ('dormant-ch', 'p1', 'dormant', ?);`, filepath.Join(repo, ".worktrees", "active"), filepath.Join(repo, ".worktrees", "dormant"))
	mustExec(t, sqlDB, `INSERT INTO terminals (id, status, exit_code) VALUES ('running-term', 'running', NULL), ('exited-term', 'exited', 0), ('orphan-term', 'orphan', NULL);`)
	mustExec(t, sqlDB, `INSERT INTO container_instances (id, container_id, status, updated_at) VALUES ('cont-running', '', 'running', 'old'), ('cont-stopped', '', 'stopped', 'old');`)
	mustExec(t, sqlDB, `INSERT INTO narrator_browser_sessions (id, status, updated_at, closed_at) VALUES ('browser-active', 'active', 'old', NULL), ('browser-closed', 'closed', 'old', 'old');`)

	runtime := &fakeRuntimeCleanupTerminalRuntime{orphans: []string{"socket-only"}}
	h := &Handler{DB: sqlDB, TerminalRuntime: runtime}
	result := callRuntimeCleanup(t, h, `{"target":"all","dryRun":true}`)

	if !result.OK || !result.DryRun || result.Target != "all" || len(result.Errors) != 0 {
		t.Fatalf("unexpected result: %#v", result)
	}
	assertRemoved(t, result, "terminals", "exited-term", "orphan-term", "socket-only")
	assertNotRemoved(t, result, "terminals", "running-term")
	assertRemoved(t, result, "containers", "cont-running")
	assertNotRemoved(t, result, "containers", "cont-stopped")
	assertRemoved(t, result, "browsers", "browser-active")
	assertRemoved(t, result, "worktrees", "dormant-ch", "p1:orphan")
	assertNotRemoved(t, result, "worktrees", "active-ch")

	if got := scalarString(t, sqlDB, `SELECT status FROM terminals WHERE id = 'orphan-term'`); got != "orphan" {
		t.Fatalf("dry-run changed terminal status to %q", got)
	}
	if got := scalarString(t, sqlDB, `SELECT status FROM container_instances WHERE id = 'cont-running'`); got != "running" {
		t.Fatalf("dry-run changed container status to %q", got)
	}
	if got := scalarString(t, sqlDB, `SELECT status FROM narrator_browser_sessions WHERE id = 'browser-active'`); got != "active" {
		t.Fatalf("dry-run changed browser status to %q", got)
	}
	if runtimeCache != nil {
		t.Fatalf("cleanup should invalidate runtime cache")
	}
}

func TestRuntimeCleanupDefaultExecutesFrontendContract(t *testing.T) {
	sqlDB := newRuntimeCleanupTestDB(t)
	mustExec(t, sqlDB, `INSERT INTO terminals (id, status, exit_code) VALUES ('orphan-term', 'orphan', NULL);`)
	runtime := &fakeRuntimeCleanupTerminalRuntime{}
	h := &Handler{DB: sqlDB, TerminalRuntime: runtime}

	result := callRuntimeCleanup(t, h, `{"target":"terminals"}`)
	if !result.OK || result.DryRun || result.Killed == nil || *result.Killed != 1 {
		t.Fatalf("frontend default cleanup should execute, got %#v", result)
	}
	if strings.Join(runtime.killedOrphans, ",") != "orphan-term" {
		t.Fatalf("default cleanup did not call runtime orphan kill: %#v", runtime.killedOrphans)
	}
	if got := scalarString(t, sqlDB, `SELECT status FROM terminals WHERE id = 'orphan-term'`); got != "exited" {
		t.Fatalf("terminal status = %q", got)
	}
}

func TestRuntimeCleanupExecuteTerminalsContainersAndBrowsers(t *testing.T) {
	sqlDB := newRuntimeCleanupTestDB(t)
	mustExec(t, sqlDB, `INSERT INTO terminals (id, status, exit_code) VALUES ('running-term', 'running', NULL), ('exited-term', 'exited', 0), ('orphan-term', 'orphan', NULL);`)
	mustExec(t, sqlDB, `INSERT INTO container_instances (id, container_id, status, updated_at) VALUES ('db-only-running', '', 'running', 'old');`)
	mustExec(t, sqlDB, `INSERT INTO narrator_browser_sessions (id, status, updated_at, closed_at) VALUES ('browser-active', 'running', 'old', NULL);`)
	runtime := &fakeRuntimeCleanupTerminalRuntime{orphans: []string{"socket-only"}}
	h := &Handler{DB: sqlDB, TerminalRuntime: runtime}

	terminals := callRuntimeCleanup(t, h, `{"target":"terminals","execute":true}`)
	if !terminals.OK || terminals.DryRun || terminals.Killed == nil || *terminals.Killed != 3 {
		t.Fatalf("unexpected terminal cleanup result: %#v", terminals)
	}
	if strings.Join(runtime.killed, ",") != "exited-term" {
		t.Fatalf("killed terminals = %#v", runtime.killed)
	}
	if strings.Join(runtime.killedOrphans, ",") != "orphan-term,socket-only" {
		t.Fatalf("killed orphan terminals = %#v", runtime.killedOrphans)
	}
	if got := scalarString(t, sqlDB, `SELECT status FROM terminals WHERE id = 'running-term'`); got != "running" {
		t.Fatalf("running terminal should be untouched, got %q", got)
	}
	if got := scalarString(t, sqlDB, `SELECT status FROM terminals WHERE id = 'orphan-term'`); got != "exited" {
		t.Fatalf("orphan terminal status = %q", got)
	}

	containers := callRuntimeCleanup(t, h, `{"target":"containers","mode":"execute"}`)
	if !containers.OK || containers.Stopped == nil || *containers.Stopped != 1 {
		t.Fatalf("unexpected container cleanup result: %#v", containers)
	}
	if got := scalarString(t, sqlDB, `SELECT status FROM container_instances WHERE id = 'db-only-running'`); got != "stopped" {
		t.Fatalf("container status = %q", got)
	}

	browsers := callRuntimeCleanup(t, h, `{"target":"browsers","dryRun":false}`)
	if !browsers.OK || browsers.ClosedSessions == nil || *browsers.ClosedSessions != 1 || browsers.BrowserClosed == nil || !*browsers.BrowserClosed {
		t.Fatalf("unexpected browser cleanup result: %#v", browsers)
	}
	if got := scalarString(t, sqlDB, `SELECT status FROM narrator_browser_sessions WHERE id = 'browser-active'`); got != "closed" {
		t.Fatalf("browser status = %q", got)
	}
}

func TestRuntimeCleanupExecuteRemovesDormantGitWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}
	sqlDB := newRuntimeCleanupTestDB(t)
	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("root\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	runGit(t, repo, "add", "README.md")
	runGit(t, repo, "commit", "-m", "initial")
	worktreePath := filepath.Join(repo, ".worktrees", "dormant")
	runGit(t, repo, "worktree", "add", "-b", "dormant-test", worktreePath, "HEAD")

	mustExec(t, sqlDB, `INSERT INTO projects (id, git_path) VALUES ('p1', ?);`, repo)
	mustExec(t, sqlDB, `INSERT INTO chapters (id, project_id, status, worktree_path) VALUES ('dormant-ch', 'p1', 'dormant', ?);`, worktreePath)

	h := &Handler{DB: sqlDB}
	result := callRuntimeCleanup(t, h, `{"target":"worktrees","execute":true}`)
	if !result.OK || result.RemovedCounts["worktrees"] != 1 || len(result.Errors) != 0 {
		t.Fatalf("unexpected worktree cleanup result: %#v", result)
	}
	assertRemoved(t, result, "worktrees", "dormant-ch")
	if _, err := os.Stat(worktreePath); !os.IsNotExist(err) {
		t.Fatalf("worktree path still exists or stat failed unexpectedly: %v", err)
	}
	var worktree sql.NullString
	if err := sqlDB.QueryRow(`SELECT worktree_path FROM chapters WHERE id = 'dormant-ch'`).Scan(&worktree); err != nil {
		t.Fatalf("scan worktree path: %v", err)
	}
	if worktree.Valid {
		t.Fatalf("worktree_path should be NULL after cleanup, got %q", worktree.String)
	}
}

func assertTerminalUnsupported(t *testing.T, recorder *httptest.ResponseRecorder, feature string) {
	t.Helper()
	if recorder.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode unsupported response: %v", err)
	}
	if body["ok"] != false || body["code"] != "TERMINAL_RUNTIME_UNSUPPORTED" || body["feature"] != feature || body["supported"] != false || body["fallback"] != true {
		t.Fatalf("unexpected unsupported body: %#v", body)
	}
}

func newRuntimeCleanupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	mustExec(t, sqlDB, `
CREATE TABLE projects (id TEXT PRIMARY KEY, git_path TEXT);
CREATE TABLE chapters (id TEXT PRIMARY KEY, project_id TEXT, status TEXT, worktree_path TEXT);
CREATE TABLE terminals (id TEXT PRIMARY KEY, chapter_id TEXT, narrator_id TEXT, name TEXT DEFAULT 'Terminal', cwd TEXT, dtach_socket TEXT, status TEXT, exit_code INTEGER, created_at TEXT DEFAULT 'now');
CREATE TABLE container_instances (id TEXT PRIMARY KEY, container_id TEXT, status TEXT, updated_at TEXT);
CREATE TABLE narrator_browser_sessions (id TEXT PRIMARY KEY, status TEXT, updated_at TEXT, closed_at TEXT);
`)
	return sqlDB
}

func mustExec(t *testing.T, sqlDB *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := sqlDB.Exec(query, args...); err != nil {
		t.Fatalf("exec failed: %v\n%s", err, query)
	}
}

func callRuntimeCleanup(t *testing.T, h *Handler, payload string) runtimeCleanupResult {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/runtime/cleanup", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	if err := h.cleanupRuntime(w, req); err != nil {
		t.Fatalf("cleanupRuntime: %v", err)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var result runtimeCleanupResult
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return result
}

func assertRemoved(t *testing.T, result runtimeCleanupResult, scope string, ids ...string) {
	t.Helper()
	present := map[string]bool{}
	for _, id := range result.Removed[scope] {
		present[id] = true
	}
	for _, id := range ids {
		if !present[id] {
			t.Fatalf("removed[%s] missing %q in %#v", scope, id, result.Removed[scope])
		}
	}
}

func assertNotRemoved(t *testing.T, result runtimeCleanupResult, scope string, ids ...string) {
	t.Helper()
	present := map[string]bool{}
	for _, id := range result.Removed[scope] {
		present[id] = true
	}
	for _, id := range ids {
		if present[id] {
			t.Fatalf("removed[%s] unexpectedly includes %q in %#v", scope, id, result.Removed[scope])
		}
	}
}

func scalarString(t *testing.T, sqlDB *sql.DB, query string, args ...any) string {
	t.Helper()
	var value string
	if err := sqlDB.QueryRow(query, args...).Scan(&value); err != nil {
		t.Fatalf("query scalar %q: %v", query, err)
	}
	return value
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, string(out))
	}
}
