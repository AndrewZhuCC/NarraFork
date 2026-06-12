package terminals

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"
)

type capturedNarratorBroadcast struct {
	NarratorID string
	Payload    map[string]any
}

type terminalBroadcastRecorder struct {
	events []capturedNarratorBroadcast
}

type failingTerminalRuntime struct{}

func (failingTerminalRuntime) Ensure(context.Context, string) error {
	return errors.New("runtime start failed")
}

func (failingTerminalRuntime) Kill(context.Context, string) error {
	return nil
}

func (r *terminalBroadcastRecorder) BroadcastNarrator(narratorID string, payload any) {
	if decoded, ok := payload.(map[string]any); ok {
		r.events = append(r.events, capturedNarratorBroadcast{NarratorID: narratorID, Payload: decoded})
	}
}

func TestTerminalCreateDeleteBroadcastsRunningCount(t *testing.T) {
	sqlDB := openTerminalBroadcastDB(t)
	recorder := &terminalBroadcastRecorder{}
	handler := NewHandler(sqlDB, nil, recorder)
	router := chi.NewRouter()
	handler.RegisterRoutes(router)

	body := bytes.NewBufferString(`{"narratorId":"nar-1","name":"Shell"}`)
	createReq := httptest.NewRequest(http.MethodPost, "/", body)
	createRec := httptest.NewRecorder()
	router.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", createRec.Code, createRec.Body.String())
	}
	if len(recorder.events) != 1 || recorder.events[0].NarratorID != "nar-1" {
		t.Fatalf("create broadcasts = %#v", recorder.events)
	}
	if recorder.events[0].Payload["type"] != "terminal_count_changed" || intFromAny(recorder.events[0].Payload["activeTerminalCount"]) != 1 || intFromAny(recorder.events[0].Payload["count"]) != 1 {
		t.Fatalf("unexpected create payload: %#v", recorder.events[0].Payload)
	}

	var created struct {
		ID  string  `json:"id"`
		Cwd *string `json:"cwd"`
	}
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created terminal: %v", err)
	}
	if created.Cwd == nil || *created.Cwd != "/repo/.worktrees/nar-1" {
		t.Fatalf("created terminal cwd = %#v", created.Cwd)
	}
	deleteReq := httptest.NewRequest(http.MethodDelete, "/"+created.ID, nil)
	deleteRec := httptest.NewRecorder()
	router.ServeHTTP(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("delete status = %d body=%s", deleteRec.Code, deleteRec.Body.String())
	}
	if len(recorder.events) != 2 {
		t.Fatalf("delete broadcasts = %#v", recorder.events)
	}
	if recorder.events[1].Payload["type"] != "terminal_count_changed" || intFromAny(recorder.events[1].Payload["activeTerminalCount"]) != 0 || intFromAny(recorder.events[1].Payload["count"]) != 0 {
		t.Fatalf("unexpected delete payload: %#v", recorder.events[1].Payload)
	}
}

func TestTerminalCountBroadcastMarksQueryFailureDegraded(t *testing.T) {
	sqlDB := openTerminalBroadcastDB(t)
	for _, stmt := range []string{
		`ALTER TABLE terminals RENAME TO terminals_real`,
		`CREATE VIEW terminals AS SELECT id, chapter_id, narrator_id, name, cwd, dtach_socket, exit_code, graph_opened, graph_x, graph_y, graph_width, graph_height, created_at FROM terminals_real`,
	} {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("prepare broken terminals view: %v", err)
		}
	}
	recorder := &terminalBroadcastRecorder{}
	handler := NewHandler(sqlDB, nil, recorder)

	handler.broadcastTerminalCountForNarrator(context.Background(), "nar-1")
	if len(recorder.events) != 1 {
		t.Fatalf("expected one broadcast, got %#v", recorder.events)
	}
	payload := recorder.events[0].Payload
	if payload["degraded"] != true || intFromAny(payload["activeTerminalCount"]) != 0 || intFromAny(payload["count"]) != 0 {
		t.Fatalf("expected degraded zero-count payload, got %#v", payload)
	}
	fallbacks, ok := payload["fallbacks"].([]string)
	if !ok || len(fallbacks) != 1 || fallbacks[0] != "terminal_count_query_failed" {
		t.Fatalf("unexpected fallbacks: %#v", payload["fallbacks"])
	}
}

func TestProcessTreeSurfacesProcessSnapshotErrors(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows returns an explicit empty process tree fallback")
	}
	original := processTreeCommandContext
	processTreeCommandContext = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "__narrafork_missing_ps__")
	}
	t.Cleanup(func() { processTreeCommandContext = original })

	items, err := ProcessTree(context.Background(), 1)
	if err == nil || !strings.Contains(err.Error(), "start process snapshot") {
		t.Fatalf("expected process snapshot error, got items=%#v err=%v", items, err)
	}
}

// Wave 284: the TypeScript getProcessInfoBatch swallows `ps` failures and falls
// back to an empty process list rather than surfacing an error. The Go
// `/api/terminals/{id}/processes` handler must mirror that: a process snapshot
// failure returns an empty 200, not a 500.
func TestProcessesHandlerReturnsEmptyOnSnapshotFailureLikeTypeScript(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows returns an explicit empty process tree fallback")
	}
	sqlDB := openTerminalBroadcastDB(t)
	if _, err := sqlDB.Exec(`INSERT INTO terminals (id, narrator_id, name, status, created_at) VALUES ('term-ps', 'nar-1', 'Shell', 'running', 'now')`); err != nil {
		t.Fatalf("insert terminal: %v", err)
	}
	original := processTreeCommandContext
	processTreeCommandContext = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "__narrafork_missing_ps__")
	}
	t.Cleanup(func() { processTreeCommandContext = original })

	handler := NewHandler(sqlDB, shellPIDOnlyRuntime{pid: 4242})
	router := chi.NewRouter()
	handler.RegisterRoutes(router)

	req := httptest.NewRequest(http.MethodGet, "/term-ps/processes", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("processes status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	if got := strings.TrimSpace(rec.Body.String()); got != "[]" {
		t.Fatalf("processes body = %q, want empty array []", got)
	}
}

type shellPIDOnlyRuntime struct {
	pid int
}

func (r shellPIDOnlyRuntime) Ensure(context.Context, string) error { return nil }
func (r shellPIDOnlyRuntime) Kill(context.Context, string) error   { return nil }
func (r shellPIDOnlyRuntime) ShellPID(string) (int, bool)          { return r.pid, true }

// Wave 285: Go terminal create must mirror the TypeScript createTerminalSchema
// Zod bounds — name max length 100, cols int 10..500, rows int 2..200 — and
// reject out-of-range values with 400 instead of silently clamping.
func TestCreateTerminalValidatesZodBoundsLikeTypeScript(t *testing.T) {
	sqlDB := openTerminalBroadcastDB(t)
	handler := NewHandler(sqlDB, nil)
	router := chi.NewRouter()
	handler.RegisterRoutes(router)

	longName := strings.Repeat("x", 101)
	cases := []struct {
		name   string
		body   string
		status int
	}{
		{"name too long", `{"chapterId":"chapter-1","name":"` + longName + `"}`, http.StatusBadRequest},
		{"cols too small", `{"chapterId":"chapter-1","cols":9}`, http.StatusBadRequest},
		{"cols too large", `{"chapterId":"chapter-1","cols":501}`, http.StatusBadRequest},
		{"rows too small", `{"chapterId":"chapter-1","rows":1}`, http.StatusBadRequest},
		{"rows too large", `{"chapterId":"chapter-1","rows":201}`, http.StatusBadRequest},
		{"in-range bounds", `{"chapterId":"chapter-1","name":"Shell","cols":120,"rows":40}`, http.StatusCreated},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(tc.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("create terminal %q status = %d body=%s, want %d", tc.name, rec.Code, rec.Body.String(), tc.status)
			}
		})
	}
}

// Wave 286: Go terminal PATCH must mirror the TypeScript name validation —
// when `name` is provided it must be a non-empty string, otherwise 400, rather
// than silently ignoring the field.
func TestUpdateTerminalRejectsEmptyOrNonStringNameLikeTypeScript(t *testing.T) {
	sqlDB := openTerminalBroadcastDB(t)
	if _, err := sqlDB.Exec(`INSERT INTO terminals (id, narrator_id, name, status, created_at) VALUES ('term-rename', 'nar-1', 'Old', 'running', 'now')`); err != nil {
		t.Fatalf("insert terminal: %v", err)
	}
	handler := NewHandler(sqlDB, nil)
	router := chi.NewRouter()
	handler.RegisterRoutes(router)

	for _, tc := range []struct {
		name   string
		body   string
		status int
	}{
		{"empty name", `{"name":""}`, http.StatusBadRequest},
		{"whitespace name", `{"name":"   "}`, http.StatusBadRequest},
		{"non-string name", `{"name":123}`, http.StatusBadRequest},
		{"valid name", `{"name":"Renamed"}`, http.StatusOK},
		{"no name field graph only", `{"graphOpened":true}`, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPatch, "/term-rename", bytes.NewBufferString(tc.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("patch terminal %q status = %d body=%s, want %d", tc.name, rec.Code, rec.Body.String(), tc.status)
			}
		})
	}
	// The invalid-name attempts must not have changed the stored name.
	var name string
	if err := sqlDB.QueryRow(`SELECT name FROM terminals WHERE id = 'term-rename'`).Scan(&name); err != nil {
		t.Fatalf("load terminal name: %v", err)
	}
	if name != "Renamed" {
		t.Fatalf("terminal name = %q, want Renamed (only the valid rename should apply)", name)
	}
}

func TestCreateTabDoesNotInsertWhenSortOrderQueryFails(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	for _, stmt := range []string{
		`CREATE TABLE terminal_tabs_store (id TEXT PRIMARY KEY, chapter_id TEXT, narrator_id TEXT, name TEXT, sort_order INTEGER, created_at TEXT)`,
		`CREATE VIEW terminal_tabs AS SELECT id, chapter_id, narrator_id, name, sort_order, created_at FROM terminal_tabs_store WHERE __narrafork_missing_sort_function__()`,
		`CREATE TRIGGER terminal_tabs_insert INSTEAD OF INSERT ON terminal_tabs BEGIN INSERT INTO terminal_tabs_store (id, chapter_id, narrator_id, name, sort_order, created_at) VALUES (NEW.id, NEW.chapter_id, NEW.narrator_id, NEW.name, NEW.sort_order, NEW.created_at); END;`,
	} {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("exec tab fixture: %v\n%s", err, stmt)
		}
	}
	handler := NewHandler(sqlDB, nil)
	req := httptest.NewRequest(http.MethodPost, "/tabs", bytes.NewBufferString(`{"chapterId":"chapter-1","name":"Shell"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	err = handler.createTab(rec, req)
	if err == nil || !strings.Contains(err.Error(), "__narrafork_missing_sort_function__") {
		t.Fatalf("expected sort_order query error, got %v", err)
	}
	var count int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM terminal_tabs_store`).Scan(&count); err != nil {
		t.Fatalf("count terminal tabs: %v", err)
	}
	if count != 0 {
		t.Fatalf("tab should not be inserted when sort_order query fails, count=%d", count)
	}
}

func TestCreateTerminalReportsRuntimeRollbackErrors(t *testing.T) {
	sqlDB := openTerminalBroadcastDB(t)
	if _, err := sqlDB.Exec(`CREATE TRIGGER terminal_delete_fail BEFORE DELETE ON terminals BEGIN SELECT RAISE(ABORT, 'delete blocked'); END;`); err != nil {
		t.Fatalf("create delete trigger: %v", err)
	}
	handler := NewHandler(sqlDB, failingTerminalRuntime{})
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{"narratorId":"nar-1","name":"Shell"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	err := handler.create(rec, req)
	if err == nil || !strings.Contains(err.Error(), "rollback terminal row") {
		t.Fatalf("expected rollback error, got %v", err)
	}
	var count int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM terminals WHERE narrator_id = 'nar-1'`).Scan(&count); err != nil {
		t.Fatalf("count terminal rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("failed rollback should leave row for diagnosis, count=%d", count)
	}
}

func TestUpdateTabDoesNotHideDatabaseErrors(t *testing.T) {
	sqlDB := openTerminalBroadcastDB(t)
	if _, err := sqlDB.Exec(`INSERT INTO terminal_tabs (id, chapter_id, name, sort_order, created_at) VALUES ('tab-1', 'chapter-1', 'Old', 0, 'now')`); err != nil {
		t.Fatalf("insert terminal tab: %v", err)
	}
	if _, err := sqlDB.Exec(`CREATE TRIGGER terminal_tab_update_fail BEFORE UPDATE ON terminal_tabs BEGIN SELECT RAISE(ABORT, 'update blocked'); END;`); err != nil {
		t.Fatalf("create failing trigger: %v", err)
	}
	router := chi.NewRouter()
	NewHandler(sqlDB, nil).RegisterRoutes(router)

	req := httptest.NewRequest(http.MethodPatch, "/tabs/tab-1", bytes.NewBufferString(`{"name":"New"}`))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("update tab should surface DB error, got status %d body=%s", rec.Code, rec.Body.String())
	}
	var name string
	if err := sqlDB.QueryRow(`SELECT name FROM terminal_tabs WHERE id = 'tab-1'`).Scan(&name); err != nil {
		t.Fatalf("query tab name: %v", err)
	}
	if name != "Old" {
		t.Fatalf("failed update should not look successful, stored name=%q", name)
	}
}

func TestDeleteMissingTabReturnsNotFound(t *testing.T) {
	sqlDB := openTerminalBroadcastDB(t)
	router := chi.NewRouter()
	NewHandler(sqlDB, nil).RegisterRoutes(router)

	req := httptest.NewRequest(http.MethodDelete, "/tabs/missing", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("delete missing tab status = %d body=%s, want 404", rec.Code, rec.Body.String())
	}
}

func TestChapterTerminalCreateDeleteBroadcastsToChapterNarrators(t *testing.T) {
	sqlDB := openTerminalBroadcastDB(t)
	recorder := &terminalBroadcastRecorder{}
	handler := NewHandler(sqlDB, nil, recorder)
	router := chi.NewRouter()
	handler.RegisterRoutes(router)

	body := bytes.NewBufferString(`{"chapterId":"chapter-1","name":"Chapter Shell"}`)
	createReq := httptest.NewRequest(http.MethodPost, "/", body)
	createRec := httptest.NewRecorder()
	router.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", createRec.Code, createRec.Body.String())
	}
	if len(recorder.events) != 1 || recorder.events[0].NarratorID != "nar-1" {
		t.Fatalf("create broadcasts = %#v", recorder.events)
	}
	if recorder.events[0].Payload["type"] != "terminal_count_changed" || intFromAny(recorder.events[0].Payload["activeTerminalCount"]) != 0 {
		t.Fatalf("unexpected chapter create payload: %#v", recorder.events[0].Payload)
	}

	var created struct {
		ID        string  `json:"id"`
		ChapterID *string `json:"chapterId"`
		Cwd       *string `json:"cwd"`
	}
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created terminal: %v", err)
	}
	if created.ChapterID == nil || *created.ChapterID != "chapter-1" || created.Cwd == nil || *created.Cwd != "/repo/.worktrees/nar-1" {
		t.Fatalf("unexpected created terminal: %#v", created)
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/"+created.ID, nil)
	deleteRec := httptest.NewRecorder()
	router.ServeHTTP(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("delete status = %d body=%s", deleteRec.Code, deleteRec.Body.String())
	}
	if len(recorder.events) != 2 || recorder.events[1].NarratorID != "nar-1" {
		t.Fatalf("delete broadcasts = %#v", recorder.events)
	}
	if recorder.events[1].Payload["type"] != "terminal_count_changed" || intFromAny(recorder.events[1].Payload["activeTerminalCount"]) != 0 {
		t.Fatalf("unexpected chapter delete payload: %#v", recorder.events[1].Payload)
	}
}

func openTerminalBroadcastDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	stmts := []string{
		`CREATE TABLE chapters (id TEXT PRIMARY KEY, worktree_path TEXT)`,
		`CREATE TABLE narrators (id TEXT PRIMARY KEY, chapter_id TEXT, cwd TEXT)`,
		`CREATE TABLE terminals (
			id TEXT PRIMARY KEY,
			chapter_id TEXT,
			narrator_id TEXT,
			name TEXT NOT NULL,
			cwd TEXT,
			dtach_socket TEXT,
			status TEXT NOT NULL,
			exit_code INTEGER,
			graph_opened INTEGER DEFAULT 0,
			graph_x REAL,
			graph_y REAL,
			graph_width REAL,
			graph_height REAL,
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE terminal_tabs (
			id TEXT PRIMARY KEY,
			chapter_id TEXT,
			narrator_id TEXT,
			name TEXT NOT NULL,
			sort_order INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL
		)`,
		`INSERT INTO chapters (id, worktree_path) VALUES ('chapter-1', '/repo/.worktrees/nar-1')`,
		`INSERT INTO narrators (id, chapter_id, cwd) VALUES ('nar-1', 'chapter-1', '/ignored/narrator/cwd')`,
	}
	for _, stmt := range stmts {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	return sqlDB
}

func intFromAny(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	default:
		return -1
	}
}
