package ws

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	_ "modernc.org/sqlite"
)

type fakeTerminalRuntime struct {
	mu        sync.Mutex
	ensures   []string
	sizes     []fakeSizeCall
	writes    []fakeWriteCall
	resizes   []fakeSizeCall
	kills     []string
	snapshots map[string]map[string]any
	errors    map[string]error
}

type fakeSizeCall struct {
	terminalID string
	cols       int
	rows       int
}

type fakeWriteCall struct {
	terminalID string
	data       string
}

func (f *fakeTerminalRuntime) Ensure(_ context.Context, terminalID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensures = append(f.ensures, terminalID)
	return f.errLocked("ensure:" + terminalID)
}

func (f *fakeTerminalRuntime) EnsureSize(_ context.Context, terminalID string, cols, rows int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sizes = append(f.sizes, fakeSizeCall{terminalID: terminalID, cols: cols, rows: rows})
	return f.errLocked("ensure:" + terminalID)
}

func (f *fakeTerminalRuntime) Write(_ context.Context, terminalID string, data string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes = append(f.writes, fakeWriteCall{terminalID: terminalID, data: data})
	return f.errLocked("write:" + terminalID)
}

func (f *fakeTerminalRuntime) Resize(_ context.Context, terminalID string, cols, rows int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resizes = append(f.resizes, fakeSizeCall{terminalID: terminalID, cols: cols, rows: rows})
	return f.errLocked("resize:" + terminalID)
}

func (f *fakeTerminalRuntime) Kill(_ context.Context, terminalID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.kills = append(f.kills, terminalID)
	return f.errLocked("kill:" + terminalID)
}

func (f *fakeTerminalRuntime) Snapshot(_ context.Context, terminalID string) (map[string]any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.errLocked("snapshot:" + terminalID); err != nil {
		return nil, err
	}
	snapshot := f.snapshots[terminalID]
	if snapshot == nil {
		return nil, nil
	}
	copy := map[string]any{}
	for key, value := range snapshot {
		copy[key] = value
	}
	return copy, nil
}

func (f *fakeTerminalRuntime) errLocked(key string) error {
	if f.errors == nil {
		return nil
	}
	return f.errors[key]
}

func TestTerminalSubscribeEnsuresRuntimeAndSendsReadySnapshot(t *testing.T) {
	sqlDB := newTerminalTestDB(t)
	insertTerminal(t, sqlDB, "term-running", "running", nil)
	runtime := &fakeTerminalRuntime{snapshots: map[string]map[string]any{
		"term-running": {
			"data":          "hello\n",
			"cols":          120,
			"rows":          40,
			"mouseTracking": true,
			"cursorVisible": false,
		},
	}}
	h, c, wsClient := newTerminalHubClient(t, sqlDB, runtime)
	defer wsClient.Close()

	h.handleMessage(c, map[string]any{"type": "subscribe", "terminalIds": []any{"term-running"}})

	ready := readWSMessage(t, wsClient)
	if ready["type"] != "ready" || ready["terminalId"] != "term-running" || ready["status"] != "running" {
		t.Fatalf("unexpected ready payload: %#v", ready)
	}
	snapshot := readWSMessage(t, wsClient)
	if snapshot["type"] != "snapshot" || snapshot["data"] != "hello\n" || intNumber(snapshot["cols"]) != 120 {
		t.Fatalf("unexpected snapshot payload: %#v", snapshot)
	}
	scrollback := readWSMessage(t, wsClient)
	if scrollback["type"] != "scrollback" || scrollback["data"] != "hello\n" || intNumber(scrollback["rows"]) != 40 {
		t.Fatalf("unexpected scrollback payload: %#v", scrollback)
	}
	bufferState := readWSMessage(t, wsClient)
	if bufferState["type"] != "bufferState" || bufferState["mouseTracking"] != true || bufferState["cursorVisible"] != false {
		t.Fatalf("unexpected bufferState payload: %#v", bufferState)
	}

	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if len(runtime.ensures) != 1 || runtime.ensures[0] != "term-running" {
		t.Fatalf("expected Ensure(term-running), got %#v", runtime.ensures)
	}
}

func TestTerminalSubscribeReportsSnapshotFallbackError(t *testing.T) {
	sqlDB := newTerminalTestDB(t)
	insertTerminal(t, sqlDB, "term-running", "running", nil)
	runtime := &fakeTerminalRuntime{errors: map[string]error{"snapshot:term-running": errors.New("snapshot failed")}}
	h, c, wsClient := newTerminalHubClient(t, sqlDB, runtime)
	defer wsClient.Close()

	h.handleMessage(c, map[string]any{"type": "subscribe", "terminalIds": []any{"term-running"}})

	ready := readWSMessage(t, wsClient)
	if ready["type"] != "ready" || ready["terminalId"] != "term-running" {
		t.Fatalf("unexpected ready payload: %#v", ready)
	}
	fallback := readWSMessage(t, wsClient)
	if fallback["type"] != "error" || fallback["terminalId"] != "term-running" || fallback["code"] != "TERMINAL_SNAPSHOT_UNAVAILABLE" || fallback["fallback"] != true || fallback["fatal"] != false {
		t.Fatalf("unexpected snapshot fallback payload: %#v", fallback)
	}
	if fallback["reason"] != "snapshot failed" || fallback["error"] != "snapshot failed" || fallback["message"] != "snapshot failed" {
		t.Fatalf("snapshot fallback should include reason/error/message detail, got %#v", fallback)
	}
}

func TestTerminalSubscribeExitedSendsCurrentExitWithoutEnsure(t *testing.T) {
	sqlDB := newTerminalTestDB(t)
	exitCode := 7
	insertTerminal(t, sqlDB, "term-exited", "exited", &exitCode)
	runtime := &fakeTerminalRuntime{}
	h, c, wsClient := newTerminalHubClient(t, sqlDB, runtime)
	defer wsClient.Close()

	h.handleMessage(c, map[string]any{"type": "subscribe", "terminalIds": []any{"term-exited"}})

	ready := readWSMessage(t, wsClient)
	if ready["type"] != "ready" || ready["status"] != "exited" || intNumber(ready["exitCode"]) != 7 {
		t.Fatalf("unexpected ready payload: %#v", ready)
	}
	exit := readWSMessage(t, wsClient)
	if exit["type"] != "exit" || exit["terminalId"] != "term-exited" || intNumber(exit["code"]) != 7 {
		t.Fatalf("unexpected exit payload: %#v", exit)
	}

	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if len(runtime.ensures) != 0 || len(runtime.sizes) != 0 {
		t.Fatalf("exited terminal should not be ensured, got ensures=%#v sizes=%#v", runtime.ensures, runtime.sizes)
	}
}

func TestTerminalResizeUsesLastInputClient(t *testing.T) {
	sqlDB := newTerminalTestDB(t)
	insertTerminal(t, sqlDB, "term-shared", "running", nil)
	runtime := &fakeTerminalRuntime{}
	h := NewHub()
	h.SetTerminalDB(sqlDB)
	h.SetTerminalRuntime(runtime)
	c1, ws1 := newTerminalClientForHub(t, h)
	defer ws1.Close()
	c2, ws2 := newTerminalClientForHub(t, h)
	defer ws2.Close()

	h.handleMessage(c1, map[string]any{"type": "subscribe", "terminalIds": []any{"term-shared"}})
	_ = readWSMessage(t, ws1)
	h.handleMessage(c2, map[string]any{"type": "subscribe", "terminalIds": []any{"term-shared"}})
	_ = readWSMessage(t, ws2)

	h.handleMessage(c1, map[string]any{"type": "input", "terminalId": "term-shared", "data": "from-1"})
	requestResize1 := readWSMessage(t, ws1)
	if requestResize1["type"] != "requestResize" || requestResize1["terminalId"] != "term-shared" {
		t.Fatalf("expected requestResize for first active client, got %#v", requestResize1)
	}
	h.handleMessage(c2, map[string]any{"type": "resize", "terminalId": "term-shared", "cols": float64(90), "rows": float64(20)})
	h.handleMessage(c1, map[string]any{"type": "resize", "terminalId": "term-shared", "cols": float64(100), "rows": float64(30)})

	h.handleMessage(c2, map[string]any{"type": "input", "terminalId": "term-shared", "data": "from-2"})
	requestResize2 := readWSMessage(t, ws2)
	if requestResize2["type"] != "requestResize" || requestResize2["terminalId"] != "term-shared" {
		t.Fatalf("expected requestResize for second active client, got %#v", requestResize2)
	}
	h.handleMessage(c1, map[string]any{"type": "resize", "terminalId": "term-shared", "cols": float64(110), "rows": float64(31)})
	h.handleMessage(c2, map[string]any{"type": "resize", "terminalId": "term-shared", "cols": float64(120), "rows": float64(40)})

	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if len(runtime.writes) != 2 || runtime.writes[0].data != "from-1" || runtime.writes[1].data != "from-2" {
		t.Fatalf("expected writes from both clients, got %#v", runtime.writes)
	}
	want := []fakeSizeCall{{terminalID: "term-shared", cols: 100, rows: 30}, {terminalID: "term-shared", cols: 120, rows: 40}}
	if len(runtime.resizes) != len(want) || runtime.resizes[0] != want[0] || runtime.resizes[1] != want[1] {
		t.Fatalf("expected only active-client resizes %#v, got %#v", want, runtime.resizes)
	}
}

func TestWebSocketBackpressureClosesClientWhenSendBacklogIsFull(t *testing.T) {
	h := NewHub()
	serverConnCh := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := h.upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		serverConnCh <- conn
	}))
	defer server.Close()

	wsClient, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer wsClient.Close()
	var serverConn *websocket.Conn
	select {
	case serverConn = <-serverConnCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for server websocket")
	}

	c := &client{conn: serverConn, kind: "terminal", subscribers: map[string]struct{}{}, sendSlots: make(chan struct{}, 1)}
	c.sendSlots <- struct{}{}
	c.queueSend([]byte(`{"type":"output"}`))
	_ = wsClient.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err := wsClient.ReadMessage(); err == nil {
		t.Fatal("expected client read to fail after backlog overflow closes websocket")
	}
}

func TestTerminalCreateInputResizeKillPayloads(t *testing.T) {
	sqlDB := newTerminalTestDB(t)
	_, err := sqlDB.Exec(`INSERT INTO chapters (id, worktree_path) VALUES ('chapter-1', '/tmp')`)
	if err != nil {
		t.Fatal(err)
	}
	runtime := &fakeTerminalRuntime{snapshots: map[string]map[string]any{}}
	h, c, wsClient := newTerminalHubClient(t, sqlDB, runtime)
	defer wsClient.Close()

	h.handleMessage(c, map[string]any{
		"type":      "create",
		"requestId": "req-1",
		"chapterId": "chapter-1",
		"name":      "Shell",
		"cols":      float64(132),
		"rows":      float64(43),
	})
	created := readWSMessage(t, wsClient)
	if created["type"] != "created" || created["requestId"] != "req-1" {
		t.Fatalf("unexpected created payload: %#v", created)
	}
	terminal, ok := created["terminal"].(map[string]any)
	if !ok || terminal["id"] == "" || terminal["name"] != "Shell" {
		t.Fatalf("unexpected terminal payload: %#v", created["terminal"])
	}
	terminalID, _ := terminal["id"].(string)
	ready := readWSMessage(t, wsClient)
	if ready["type"] != "ready" || ready["terminalId"] != terminalID {
		t.Fatalf("unexpected ready payload after create: %#v", ready)
	}

	runtime.mu.Lock()
	if len(runtime.sizes) != 1 || runtime.sizes[0] != (fakeSizeCall{terminalID: terminalID, cols: 132, rows: 43}) {
		runtime.mu.Unlock()
		t.Fatalf("expected EnsureSize with create dimensions, got %#v", runtime.sizes)
	}
	runtime.mu.Unlock()

	h.handleMessage(c, map[string]any{"type": "input", "terminalId": terminalID, "data": "ls\n"})
	requestResize := readWSMessage(t, wsClient)
	if requestResize["type"] != "requestResize" || requestResize["terminalId"] != terminalID {
		t.Fatalf("unexpected requestResize payload: %#v", requestResize)
	}
	h.handleMessage(c, map[string]any{"type": "resize", "terminalId": terminalID, "cols": float64(100), "rows": float64(30)})
	h.handleMessage(c, map[string]any{"type": "kill", "terminalId": terminalID})
	killed := readWSMessage(t, wsClient)
	if killed["type"] != "killed" || killed["terminalId"] != terminalID {
		t.Fatalf("unexpected killed payload: %#v", killed)
	}

	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if len(runtime.writes) != 1 || runtime.writes[0] != (fakeWriteCall{terminalID: terminalID, data: "ls\n"}) {
		t.Fatalf("expected Write payload, got %#v", runtime.writes)
	}
	if len(runtime.resizes) != 1 || runtime.resizes[0] != (fakeSizeCall{terminalID: terminalID, cols: 100, rows: 30}) {
		t.Fatalf("expected Resize payload, got %#v", runtime.resizes)
	}
	if len(runtime.kills) != 1 || runtime.kills[0] != terminalID {
		t.Fatalf("expected Kill payload, got %#v", runtime.kills)
	}
}

func TestTerminalWSCreateResizeRenameBoundsRejectOutOfRange(t *testing.T) {
	sqlDB := newTerminalTestDB(t)
	if _, err := sqlDB.Exec(`INSERT INTO chapters (id, worktree_path) VALUES ('chapter-1', '/tmp')`); err != nil {
		t.Fatal(err)
	}
	runtime := &fakeTerminalRuntime{snapshots: map[string]map[string]any{}}
	h, c, wsClient := newTerminalHubClient(t, sqlDB, runtime)
	defer wsClient.Close()

	// create with cols above 500 should error before persisting a terminal row.
	h.handleMessage(c, map[string]any{
		"type":      "create",
		"requestId": "req-bad",
		"chapterId": "chapter-1",
		"cols":      float64(900),
		"rows":      float64(40),
	})
	msg := readWSMessage(t, wsClient)
	if msg["type"] != "error" || msg["message"] != "Invalid terminal create request" {
		t.Fatalf("expected create bounds error, got %#v", msg)
	}
	var count int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM terminals`).Scan(&count); err != nil {
		t.Fatalf("count terminals: %v", err)
	}
	if count != 0 {
		t.Fatalf("invalid create should not persist a terminal row, count=%d", count)
	}

	// Create a valid terminal to exercise resize/rename bounds.
	h.handleMessage(c, map[string]any{
		"type":      "create",
		"requestId": "req-ok",
		"chapterId": "chapter-1",
		"name":      "Shell",
	})
	created := readWSMessage(t, wsClient)
	terminal, _ := created["terminal"].(map[string]any)
	terminalID, _ := terminal["id"].(string)
	if terminalID == "" {
		t.Fatalf("expected created terminal id, got %#v", created)
	}
	ready := readWSMessage(t, wsClient)
	if ready["type"] != "ready" {
		t.Fatalf("expected ready after create, got %#v", ready)
	}

	// resize with rows below 2 should error.
	h.handleMessage(c, map[string]any{"type": "resize", "terminalId": terminalID, "cols": float64(100), "rows": float64(1)})
	msg = readWSMessage(t, wsClient)
	if msg["type"] != "error" || msg["message"] != "Invalid terminal resize request" {
		t.Fatalf("expected resize bounds error, got %#v", msg)
	}

	// rename with a name longer than 100 chars should error.
	longName := make([]byte, 101)
	for i := range longName {
		longName[i] = 'n'
	}
	h.handleMessage(c, map[string]any{"type": "rename", "terminalId": terminalID, "name": string(longName)})
	msg = readWSMessage(t, wsClient)
	if msg["type"] != "error" || msg["message"] != "name must be at most 100 characters" {
		t.Fatalf("expected rename bounds error, got %#v", msg)
	}
}

func TestTerminalCreateWithNarratorUsesChapterWorktreeCwd(t *testing.T) {
	sqlDB := newTerminalTestDB(t)
	if _, err := sqlDB.Exec(`INSERT INTO chapters (id, worktree_path) VALUES ('chapter-1', '/repo/.worktrees/chapter-1')`); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO narrators (id, chapter_id, cwd) VALUES ('nar-1', 'chapter-1', '/ignored/narrator/cwd')`); err != nil {
		t.Fatal(err)
	}
	h := NewHub()
	h.SetTerminalDB(sqlDB)

	terminal, err := h.createTerminal(context.Background(), map[string]any{"type": "create", "narratorId": "nar-1"})
	if err != nil {
		t.Fatalf("createTerminal: %v", err)
	}
	if terminal["cwd"] != "/repo/.worktrees/chapter-1" {
		t.Fatalf("terminal cwd = %#v", terminal["cwd"])
	}
}

func newTerminalHubClient(t *testing.T, sqlDB *sql.DB, runtime *fakeTerminalRuntime) (*Hub, *client, *websocket.Conn) {
	t.Helper()
	h := NewHub()
	h.SetTerminalDB(sqlDB)
	h.SetTerminalRuntime(runtime)
	c, wsClient := newTerminalClientForHub(t, h)
	return h, c, wsClient
}

func newTerminalClientForHub(t *testing.T, h *Hub) (*client, *websocket.Conn) {
	t.Helper()
	serverConnCh := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := h.upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		serverConnCh <- conn
	}))
	t.Cleanup(server.Close)
	url := "ws" + strings.TrimPrefix(server.URL, "http")
	wsClient, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	var serverConn *websocket.Conn
	select {
	case serverConn = <-serverConnCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for server websocket")
	}
	c := &client{conn: serverConn, kind: "terminal", userID: "user-1", subscribers: map[string]struct{}{}}
	h.add(c)
	t.Cleanup(func() { h.remove(c) })
	return c, wsClient
}

func readWSMessage(t *testing.T, conn *websocket.Conn) map[string]any {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read websocket message: %v", err)
	}
	var msg map[string]any
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("decode websocket message %q: %v", string(data), err)
	}
	return msg
}

func newTerminalTestDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", "file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
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
	}
	for _, stmt := range stmts {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	return sqlDB
}

func insertTerminal(t *testing.T, sqlDB *sql.DB, terminalID, status string, exitCode *int) {
	t.Helper()
	var exitValue any
	if exitCode != nil {
		exitValue = *exitCode
	}
	_, err := sqlDB.Exec(`INSERT INTO terminals (id, name, status, exit_code, graph_opened, created_at) VALUES (?, 'Terminal', ?, ?, 0, '2026-05-14T00:00:00Z')`, terminalID, status, exitValue)
	if err != nil {
		t.Fatal(err)
	}
}
