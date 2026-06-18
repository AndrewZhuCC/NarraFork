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
	"time"

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

// TestRuntimeCleanupUnknownTargetMatchesTSMessage locks the invalid-target error
// string against the TypeScript runtime cleanup route (server/routes/runtime.ts),
// which returns 400 "Invalid target". Go keeps its documented capability extension
// (empty/omitted target -> "all"; Go-only "worktrees" target), so only a genuinely
// unrecognized non-empty target reaches the 400 path; the message is aligned to TS.
func TestRuntimeCleanupUnknownTargetMatchesTSMessage(t *testing.T) {
	h := &Handler{TerminalRuntime: &fakeRuntimeCleanupTerminalRuntime{}}
	for _, body := range []string{`{"target":"bogus"}`, `{"target":"sessions"}`} {
		req := httptest.NewRequest(http.MethodPost, "/runtime/cleanup", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		err := h.cleanupRuntime(w, req)
		var appErr *httpx.AppError
		if !errors.As(err, &appErr) || appErr.Status != http.StatusBadRequest {
			t.Fatalf("expected 400 AppError for %s, got %T %v", body, err, err)
		}
		if appErr.Message != "Invalid target" {
			t.Fatalf("expected message \"Invalid target\" for %s, got %q", body, appErr.Message)
		}
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

func TestCachedRuntimeExpiresAfterTTLLikeTypeScript(t *testing.T) {
	previous := runtimeCache
	t.Cleanup(func() { runtimeCache = previous })
	h := &Handler{TerminalRuntime: &fakeRuntimeCleanupTerminalRuntime{}}

	// Fresh cache within the 30s TTL window is served as cached=true.
	runtimeCache = map[string]any{
		"terminals": map[string]any{},
		"scannedAt": time.Now().Add(-5 * time.Second).UnixMilli(),
	}
	fresh := callCachedRuntime(t, h)
	if fresh["cached"] != true {
		t.Fatalf("fresh runtime cache should report cached=true, got %#v", fresh)
	}

	// Cache older than the TTL window is treated as expired, cleared, and not served.
	runtimeCache = map[string]any{
		"terminals": map[string]any{},
		"scannedAt": time.Now().Add(-(runtimeCacheTTL + time.Second)).UnixMilli(),
	}
	stale := callCachedRuntime(t, h)
	if stale["cached"] != false {
		t.Fatalf("stale runtime cache should report cached=false, got %#v", stale)
	}
	if _, hasData := stale["data"]; hasData {
		t.Fatalf("expired runtime cache response must omit data, got %#v", stale)
	}
	if runtimeCache != nil {
		t.Fatalf("expired runtime cache must be cleared, got %#v", runtimeCache)
	}
}

func callCachedRuntime(t *testing.T, h *Handler) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/runtime/cached", nil)
	w := httptest.NewRecorder()
	if err := h.cachedRuntime(w, req); err != nil {
		t.Fatalf("cached runtime: %v", err)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("cached runtime status = %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode cached runtime: %v", err)
	}
	return body
}

// Wave 280: the TypeScript runtime-service cleanup emits no event-bus frames and
// acquires no global lock; it simply runs per-target cleanup and logs. Go must
// match that no-event/no-lock contract, so the cleanup response must not carry
// any event/lock/broadcast fields that would imply a serialized or event-driven
// cleanup the TypeScript backend does not perform.
func TestRuntimeCleanupEmitsNoEventOrGlobalLockLikeTypeScript(t *testing.T) {
	previous := runtimeCache
	t.Cleanup(func() { runtimeCache = previous })
	sqlDB := newRuntimeCleanupTestDB(t)
	defer sqlDB.Close()
	mustExec(t, sqlDB, `INSERT INTO terminals (id, status, exit_code) VALUES ('exited-term', 'exited', 0);`)
	h := &Handler{DB: sqlDB, TerminalRuntime: &fakeRuntimeCleanupTerminalRuntime{}}

	req := httptest.NewRequest(http.MethodPost, "/api/runtime/cleanup", strings.NewReader(`{"target":"terminals","execute":true}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	if err := h.cleanupRuntime(w, req); err != nil {
		t.Fatalf("cleanupRuntime: %v", err)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var raw map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	for _, forbidden := range []string{"event", "events", "eventId", "lock", "lockId", "globalLock", "locked", "broadcast", "broadcasts"} {
		if _, present := raw[forbidden]; present {
			t.Fatalf("runtime cleanup response must not expose %q (TS emits no events/lock): %#v", forbidden, raw)
		}
	}
	if raw["ok"] != true {
		t.Fatalf("expected cleanup ok=true, got %#v", raw)
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

// TestOrphanTerminalRoutesRequireStringTerminalIdLikeTS locks the kill-orphan and
// reattach-orphan validation against TS (server/routes/admin.ts): `if (!terminalId
// || typeof terminalId !== "string") throw new AppError("terminalId is required",
// 400, "VALIDATION_ERROR")`. A missing/empty/non-string terminalId must return 400
// with code VALIDATION_ERROR. Go previously decoded into a typed string (a
// non-string value gave a generic "invalid JSON") and used BadRequest (code
// BAD_REQUEST) instead of VALIDATION_ERROR.
func TestOrphanTerminalRoutesRequireStringTerminalIdLikeTS(t *testing.T) {
	h := &Handler{TerminalRuntime: &fakeRuntimeCleanupTerminalRuntime{}}
	handlers := map[string]func(http.ResponseWriter, *http.Request) error{
		"kill-orphan":     h.killOrphanTerminal,
		"reattach-orphan": h.reattachOrphanTerminal,
	}
	for route, handler := range handlers {
		for _, tc := range []struct{ name, body string }{
			{"missing terminalId", `{}`},
			{"empty terminalId", `{"terminalId":""}`},
			{"terminalId number", `{"terminalId":123}`},
			{"terminalId object", `{"terminalId":{"a":1}}`},
		} {
			t.Run(route+"/"+tc.name, func(t *testing.T) {
				req := httptest.NewRequest(http.MethodPost, "/api/admin/terminals/"+route, strings.NewReader(tc.body))
				req.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				err := handler(w, req)
				var appErr *httpx.AppError
				if !errors.As(err, &appErr) || appErr.Status != http.StatusBadRequest {
					t.Fatalf("expected 400 AppError for %s/%s, got %T %v", route, tc.name, err, err)
				}
				if appErr.Code != "VALIDATION_ERROR" || appErr.Message != "terminalId is required" {
					t.Fatalf("expected VALIDATION_ERROR \"terminalId is required\" for %s/%s, got code=%q msg=%q", route, tc.name, appErr.Code, appErr.Message)
				}
			})
		}
	}
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

func TestRuntimeCleanupEncodesEmptyErrorsArray(t *testing.T) {
	sqlDB := newRuntimeCleanupTestDB(t)
	h := &Handler{DB: sqlDB, TerminalRuntime: &fakeRuntimeCleanupTerminalRuntime{}}
	req := httptest.NewRequest(http.MethodPost, "/api/runtime/cleanup", strings.NewReader(`{"target":"terminals","execute":true}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	if err := h.cleanupRuntime(w, req); err != nil {
		t.Fatalf("cleanupRuntime: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode cleanup: %v", err)
	}
	errorsList, ok := body["errors"].([]any)
	if !ok || len(errorsList) != 0 {
		t.Fatalf("runtime cleanup should encode empty errors as [], got %#v", body["errors"])
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
	reason, _ := body["reason"].(string)
	if reason == "" || body["error"] != reason || body["message"] != reason {
		t.Fatalf("terminal unsupported fallback should expose reason/error/message, got %#v", body)
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
