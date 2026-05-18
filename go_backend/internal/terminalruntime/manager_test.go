package terminalruntime

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	_ "modernc.org/sqlite"
)

func TestRecoverOnStartupMarksExitedOrOrphanAndImportsLiveSockets(t *testing.T) {
	sqlDB := newTerminalRuntimeTestDB(t)
	insertTerminalRuntimeRow(t, sqlDB, "live", "running", "/tmp/live.sock")
	insertTerminalRuntimeRow(t, sqlDB, "dead", "running", "/tmp/dead.sock")
	insertTerminalRuntimeRow(t, sqlDB, "stale-orphan", "orphan", "")

	socketsDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(socketsDir, "terminal-imported.sock"), []byte("socket"), 0o600); err != nil {
		t.Fatalf("write socket marker: %v", err)
	}
	manager := NewManager(sqlDB, nil)
	manager.socketsDir = socketsDir
	manager.dtachAvailable = func() bool { return true }
	manager.socketAlive = func(_ context.Context, terminalID, _ string) bool {
		return terminalID == "live" || terminalID == "imported"
	}

	if err := manager.RecoverOnStartup(context.Background()); err != nil {
		t.Fatalf("RecoverOnStartup: %v", err)
	}

	assertTerminalStatus(t, sqlDB, "live", "orphan", nil)
	assertTerminalStatus(t, sqlDB, "dead", "exited", intPtr(0))
	assertTerminalStatus(t, sqlDB, "stale-orphan", "exited", intPtr(0))
	assertTerminalStatus(t, sqlDB, "imported", "orphan", nil)
}

func TestRecoverOnStartupReturnsRecoveredSocketInsertErrors(t *testing.T) {
	sqlDB := newTerminalRuntimeTestDB(t)
	socketsDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(socketsDir, "terminal-imported.sock"), []byte("socket"), 0o600); err != nil {
		t.Fatalf("write socket marker: %v", err)
	}
	if _, err := sqlDB.Exec(`CREATE TRIGGER fail_recovered_terminal_insert BEFORE INSERT ON terminals WHEN NEW.id = 'imported' BEGIN SELECT RAISE(ABORT, 'recovered terminal insert blocked'); END;`); err != nil {
		t.Fatalf("create recovery insert trigger: %v", err)
	}
	manager := NewManager(sqlDB, nil)
	manager.socketsDir = socketsDir
	manager.dtachAvailable = func() bool { return true }
	manager.socketAlive = func(_ context.Context, terminalID, _ string) bool { return terminalID == "imported" }

	err := manager.RecoverOnStartup(context.Background())
	if err == nil || !strings.Contains(err.Error(), "import recovered terminal") {
		t.Fatalf("expected recovered terminal insert error, got %v", err)
	}
	var count int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM terminals WHERE id = 'imported'`).Scan(&count); err != nil {
		t.Fatalf("count imported terminal: %v", err)
	}
	if count != 0 {
		t.Fatalf("failed recovered insert should not leave imported row, count=%d", count)
	}
}

func TestRecoverOnStartupMarksRunningExitedWhenDtachUnavailable(t *testing.T) {
	sqlDB := newTerminalRuntimeTestDB(t)
	insertTerminalRuntimeRow(t, sqlDB, "running", "running", "")
	insertTerminalRuntimeRow(t, sqlDB, "orphan", "orphan", "/tmp/orphan.sock")
	manager := NewManager(sqlDB, nil)
	manager.dtachAvailable = func() bool { return false }

	if err := manager.RecoverOnStartup(context.Background()); err != nil {
		t.Fatalf("RecoverOnStartup: %v", err)
	}

	assertTerminalStatus(t, sqlDB, "running", "exited", intPtr(0))
	assertTerminalStatus(t, sqlDB, "orphan", "exited", intPtr(0))
}

func TestEnsureFallsBackToDirectWhenDtachCreateFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("direct pty fallback test uses POSIX shell")
	}
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("/bin/sh is required for direct pty fallback test")
	}
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("PATH", t.TempDir())

	sqlDB := newTerminalRuntimeTestDB(t)
	cwd := t.TempDir()
	_, err := sqlDB.Exec(`INSERT INTO terminals (id, name, cwd, status, graph_opened, created_at) VALUES ('fallback', 'Terminal', ?, 'running', 0, 'now')`, cwd)
	if err != nil {
		t.Fatalf("insert terminal: %v", err)
	}

	var mu sync.Mutex
	events := []map[string]any{}
	manager := NewManager(sqlDB, func(_ string, payload any) {
		if event, ok := payload.(map[string]any); ok {
			mu.Lock()
			events = append(events, event)
			mu.Unlock()
		}
	})
	manager.socketsDir = t.TempDir()
	manager.dtachAvailable = func() bool { return true }

	if err := manager.EnsureSize(context.Background(), "fallback", 100, 30); err != nil {
		t.Fatalf("EnsureSize: %v", err)
	}
	defer manager.Kill(context.Background(), "fallback")

	mu.Lock()
	defer mu.Unlock()
	foundFallback := false
	for _, event := range events {
		if event["type"] == "dtachFallback" && event["mode"] == "direct" {
			foundFallback = true
			break
		}
	}
	if !foundFallback {
		t.Fatalf("expected dtachFallback event, got %#v", events)
	}

	var status string
	var dtachSocket sql.NullString
	if err := sqlDB.QueryRow(`SELECT status, dtach_socket FROM terminals WHERE id = 'fallback'`).Scan(&status, &dtachSocket); err != nil {
		t.Fatalf("query fallback terminal: %v", err)
	}
	if status != "running" || dtachSocket.Valid {
		t.Fatalf("fallback terminal status/socket = %s/%v, want running/null", status, dtachSocket)
	}
}

func TestEnsureSizeReturnsErrorWhenRunningStatusUpdateFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("direct pty status update test uses POSIX shell")
	}
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("/bin/sh is required for direct pty status update test")
	}
	t.Setenv("SHELL", "/bin/sh")

	sqlDB := newTerminalRuntimeTestDB(t)
	cwd := t.TempDir()
	if _, err := sqlDB.Exec(`INSERT INTO terminals (id, name, cwd, status, graph_opened, created_at) VALUES ('db-fail', 'Terminal', ?, 'running', 0, 'now')`, cwd); err != nil {
		t.Fatalf("insert terminal: %v", err)
	}
	if _, err := sqlDB.Exec(`CREATE TRIGGER fail_terminal_running_update BEFORE UPDATE OF status ON terminals BEGIN SELECT RAISE(ABORT, 'terminal status update blocked'); END;`); err != nil {
		t.Fatalf("create status trigger: %v", err)
	}
	manager := NewManager(sqlDB, nil)
	manager.dtachAvailable = func() bool { return false }

	err := manager.EnsureSize(context.Background(), "db-fail", 80, 24)
	if err == nil || !strings.Contains(err.Error(), "terminal status update blocked") {
		t.Fatalf("expected status update error, got %v", err)
	}
	manager.mu.Lock()
	_, stillStored := manager.sessions["db-fail"]
	manager.mu.Unlock()
	if stillStored {
		t.Fatalf("session should be removed when DB running status update fails")
	}
}

func TestPersistDtachSocketReturnsUpdateErrors(t *testing.T) {
	sqlDB := newTerminalRuntimeTestDB(t)
	insertTerminalRuntimeRow(t, sqlDB, "dtach-fail", "running", "")
	if _, err := sqlDB.Exec(`CREATE TRIGGER fail_dtach_socket_update BEFORE UPDATE OF dtach_socket ON terminals BEGIN SELECT RAISE(ABORT, 'dtach socket update blocked'); END;`); err != nil {
		t.Fatalf("create dtach socket trigger: %v", err)
	}
	manager := NewManager(sqlDB, nil)

	err := manager.persistDtachSocket(context.Background(), "dtach-fail", "/tmp/dtach-fail.sock")
	if err == nil || !strings.Contains(err.Error(), "dtach socket update blocked") {
		t.Fatalf("expected dtach socket update error, got %v", err)
	}
}

func TestShellPIDFindsDetachedDtachShellFromDBSocket(t *testing.T) {
	sqlDB := newTerminalRuntimeTestDB(t)
	insertTerminalRuntimeRow(t, sqlDB, "detached", "orphan", "/tmp/detached.sock")
	manager := NewManager(sqlDB, nil)
	manager.dtachAvailable = func() bool { return true }
	manager.socketAlive = func(_ context.Context, terminalID, socketPath string) bool {
		return terminalID == "detached" && socketPath == "/tmp/detached.sock"
	}
	manager.processSnapshotProvider = func(context.Context) processSnapshot {
		return processSnapshot{
			items: map[int]processInfo{
				100: {pid: 100, ppid: 1, args: "dtach -n /tmp/detached.sock -z /bin/sh -li"},
				101: {pid: 101, ppid: 100, args: "/bin/sh -li"},
			},
			children: map[int][]int{100: {101}},
		}
	}

	pid, ok := manager.ShellPID("detached")
	if !ok || pid != 101 {
		t.Fatalf("ShellPID = %d/%v, want 101/true", pid, ok)
	}
}

func TestMarkStatusEmitsErrorWhenDatabaseUpdateFails(t *testing.T) {
	sqlDB := newTerminalRuntimeTestDB(t)
	insertTerminalRuntimeRow(t, sqlDB, "persist-fail", "running", "")
	if _, err := sqlDB.Exec(`CREATE TRIGGER fail_terminal_status_update BEFORE UPDATE OF status ON terminals BEGIN SELECT RAISE(ABORT, 'status persist blocked'); END;`); err != nil {
		t.Fatalf("create status trigger: %v", err)
	}
	events := []map[string]any{}
	manager := NewManager(sqlDB, func(_ string, payload any) {
		if event, ok := payload.(map[string]any); ok {
			events = append(events, event)
		}
	})

	manager.markExited(context.Background(), "persist-fail", 0)

	if len(events) != 1 {
		t.Fatalf("expected one status persist error event, got %#v", events)
	}
	event := events[0]
	if event["type"] != "error" || event["code"] != "TERMINAL_STATUS_PERSIST_FAILED" || event["status"] != "exited" || event["fallback"] != true || event["fatal"] != false {
		t.Fatalf("unexpected status persist event: %#v", event)
	}
}

func TestCapabilitiesExposePlatformAndReplayContract(t *testing.T) {
	manager := NewManager(nil, nil)
	manager.dtachAvailable = func() bool { return runtime.GOOS != "windows" }
	caps := manager.Capabilities()
	if caps["multiClientResizeMode"] != "last-input-client" || caps["scrollbackReplay"] != true || caps["bufferStateReplay"] != true {
		t.Fatalf("unexpected terminal capability contract: %#v", caps)
	}
	if runtime.GOOS == "windows" {
		if caps["supported"] != false || caps["directPty"] != false || caps["dtachSupported"] != false || caps["reason"] == "" {
			t.Fatalf("expected explicit Windows unsupported capability, got %#v", caps)
		}
		return
	}
	if caps["supported"] != true || caps["directPty"] != true || caps["dtachSupported"] != true {
		t.Fatalf("expected Unix-like terminal support capabilities, got %#v", caps)
	}
}

func TestSessionBufferIsCapped(t *testing.T) {
	session := &Session{}
	input := "prefix-" + strings.Repeat("x", maxSnapshotBytes+64) + "-suffix"
	session.appendBufferLocked(input)
	if session.buf.Len() != maxSnapshotBytes {
		t.Fatalf("buffer length = %d, want %d", session.buf.Len(), maxSnapshotBytes)
	}
	if !strings.HasSuffix(session.buf.String(), "-suffix") {
		t.Fatalf("buffer did not keep newest output suffix")
	}
	if strings.Contains(session.buf.String(), "prefix-") {
		t.Fatalf("buffer retained oldest output after cap")
	}
}

func newTerminalRuntimeTestDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", "file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	_, err = sqlDB.Exec(`CREATE TABLE terminals (
		id TEXT PRIMARY KEY,
		chapter_id TEXT,
		narrator_id TEXT,
		name TEXT,
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
		t.Fatal(err)
	}
	return sqlDB
}

func insertTerminalRuntimeRow(t *testing.T, sqlDB *sql.DB, terminalID, status, socketPath string) {
	t.Helper()
	var socket any
	if strings.TrimSpace(socketPath) != "" {
		socket = socketPath
	}
	_, err := sqlDB.Exec(`INSERT INTO terminals (id, name, dtach_socket, status, graph_opened, created_at) VALUES (?, 'Terminal', ?, ?, 0, 'now')`, terminalID, socket, status)
	if err != nil {
		t.Fatalf("insert terminal %s: %v", terminalID, err)
	}
}

func assertTerminalStatus(t *testing.T, sqlDB *sql.DB, terminalID, wantStatus string, wantExit *int) {
	t.Helper()
	var status string
	var exitCode sql.NullInt64
	if err := sqlDB.QueryRow(`SELECT status, exit_code FROM terminals WHERE id = ?`, terminalID).Scan(&status, &exitCode); err != nil {
		t.Fatalf("query terminal %s: %v", terminalID, err)
	}
	if status != wantStatus {
		t.Fatalf("terminal %s status = %s, want %s", terminalID, status, wantStatus)
	}
	if wantExit == nil {
		if exitCode.Valid {
			t.Fatalf("terminal %s exit code = %d, want null", terminalID, exitCode.Int64)
		}
		return
	}
	if !exitCode.Valid || int(exitCode.Int64) != *wantExit {
		t.Fatalf("terminal %s exit code = %v, want %d", terminalID, exitCode, *wantExit)
	}
}

func intPtr(value int) *int {
	return &value
}
