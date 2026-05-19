package narrators

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"testing"
)

type fakeAgentTerminalRuntime struct {
	mu        sync.Mutex
	ensured   []string
	writes    map[string][]string
	snapshots map[string]string
}

func newFakeAgentTerminalRuntime() *fakeAgentTerminalRuntime {
	return &fakeAgentTerminalRuntime{writes: map[string][]string{}, snapshots: map[string]string{}}
}

func (f *fakeAgentTerminalRuntime) Ensure(_ context.Context, terminalID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensured = append(f.ensured, terminalID)
	if _, ok := f.snapshots[terminalID]; !ok {
		f.snapshots[terminalID] = ""
	}
	return nil
}

func (f *fakeAgentTerminalRuntime) Write(_ context.Context, terminalID string, data string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes[terminalID] = append(f.writes[terminalID], data)
	f.snapshots[terminalID] += data
	return nil
}

func (f *fakeAgentTerminalRuntime) Snapshot(_ context.Context, terminalID string) (map[string]any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return map[string]any{"data": f.snapshots[terminalID], "status": "running"}, nil
}

func TestTerminalAgentToolCreateListReadWriteRename(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	createTerminalAgentToolSchema(t, sqlDB)
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	runtime := newFakeAgentTerminalRuntime()
	service := &Service{DB: sqlDB, TerminalRuntime: runtime}
	narrator, err := service.Get(context.Background(), narratorID)
	if err != nil {
		t.Fatalf("load narrator: %v", err)
	}

	out, status, err := service.executeTerminalAgentTool(context.Background(), narratorID, narrator, map[string]any{"action": "create", "name": "Dev"})
	if err != nil || status != "" || !strings.Contains(out, "Terminal created successfully") {
		t.Fatalf("create output=%q status=%q err=%v", out, status, err)
	}
	terminalID := terminalToolOnlyID(t, sqlDB)
	if len(runtime.ensured) != 1 || runtime.ensured[0] != terminalID {
		t.Fatalf("terminal runtime ensured = %#v, want %s", runtime.ensured, terminalID)
	}
	runtime.snapshots[terminalID] = "hello\nworld\n"

	out, status, err = service.executeTerminalAgentTool(context.Background(), narratorID, narrator, map[string]any{"action": "read", "terminal_id": "Dev", "last_n_lines": float64(1)})
	if err != nil || status != "" || strings.TrimSpace(out) != "world" {
		t.Fatalf("read output=%q status=%q err=%v", out, status, err)
	}

	out, status, err = service.executeTerminalAgentTool(context.Background(), narratorID, narrator, map[string]any{"action": "write", "terminal_id": terminalID, "input": "echo ok\n"})
	if err != nil || status != "" || !strings.Contains(out, "Wrote") {
		t.Fatalf("write output=%q status=%q err=%v", out, status, err)
	}
	if writes := runtime.writes[terminalID]; len(writes) != 1 || writes[0] != "echo ok\n" {
		t.Fatalf("writes = %#v", writes)
	}

	out, status, err = service.executeTerminalAgentTool(context.Background(), narratorID, narrator, map[string]any{"action": "rename", "terminal_id": "Dev", "name": "Server"})
	if err != nil || status != "" || !strings.Contains(out, "Server") {
		t.Fatalf("rename output=%q status=%q err=%v", out, status, err)
	}
	out, status, err = service.executeTerminalAgentTool(context.Background(), narratorID, narrator, map[string]any{"action": "list"})
	if err != nil || status != "" || !strings.Contains(out, "Server") || strings.Contains(out, "Dev |") {
		t.Fatalf("list output=%q status=%q err=%v", out, status, err)
	}
}

func TestTerminalAgentToolDeniesForeignTerminal(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	createTerminalAgentToolSchema(t, sqlDB)
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	service := &Service{DB: sqlDB, TerminalRuntime: newFakeAgentTerminalRuntime()}
	narrator, err := service.Get(context.Background(), narratorID)
	if err != nil {
		t.Fatalf("load narrator: %v", err)
	}
	_, err = sqlDB.Exec(`INSERT INTO terminals (id, narrator_id, name, status, created_at) VALUES ('foreign', 'other', 'Foreign', 'running', 'now')`)
	if err != nil {
		t.Fatalf("insert terminal: %v", err)
	}
	out, status, err := service.executeTerminalAgentTool(context.Background(), narratorID, narrator, map[string]any{"action": "read", "terminal_id": "foreign"})
	if err != nil || status != "error" || !strings.Contains(out, "access denied") {
		t.Fatalf("foreign read output=%q status=%q err=%v", out, status, err)
	}
}

func createTerminalAgentToolSchema(t *testing.T, sqlDB *sql.DB) {
	t.Helper()
	_, err := sqlDB.Exec(`CREATE TABLE terminals (
		id TEXT PRIMARY KEY,
		chapter_id TEXT,
		narrator_id TEXT,
		name TEXT NOT NULL,
		cwd TEXT,
		dtach_socket TEXT,
		status TEXT,
		exit_code INTEGER,
		graph_opened INTEGER DEFAULT 0,
		graph_x REAL,
		graph_y REAL,
		graph_width REAL,
		graph_height REAL,
		created_at TEXT
	)`)
	if err != nil {
		t.Fatalf("create terminals schema: %v", err)
	}
}

func terminalToolOnlyID(t *testing.T, sqlDB *sql.DB) string {
	t.Helper()
	var terminalID string
	if err := sqlDB.QueryRow(`SELECT id FROM terminals`).Scan(&terminalID); err != nil {
		t.Fatalf("load terminal id: %v", err)
	}
	return terminalID
}
