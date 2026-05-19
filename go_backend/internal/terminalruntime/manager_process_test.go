package terminalruntime

import (
	"bytes"
	"context"
	"io"
	"sync"
	"testing"
	"time"
)

type fakeTerminalProcess struct {
	mu          sync.Mutex
	stdin       bytes.Buffer
	resizeCalls []struct{ cols, rows int }
	pid         int
	closed      bool
	killed      bool
	waitCh      chan struct{}
	exitCode    int
}

func newFakeTerminalProcess(pid int) *fakeTerminalProcess {
	return &fakeTerminalProcess{pid: pid, waitCh: make(chan struct{}), exitCode: 0}
}

func (p *fakeTerminalProcess) Read([]byte) (int, error) { return 0, io.EOF }

func (p *fakeTerminalProcess) Write(buf []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stdin.Write(buf)
}

func (p *fakeTerminalProcess) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	return nil
}

func (p *fakeTerminalProcess) Resize(cols, rows int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.resizeCalls = append(p.resizeCalls, struct{ cols, rows int }{cols: cols, rows: rows})
	return nil
}

func (p *fakeTerminalProcess) PID() int { return p.pid }

func (p *fakeTerminalProcess) Wait() int {
	<-p.waitCh
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.exitCode
}

func (p *fakeTerminalProcess) KillTree(context.Context) {
	p.mu.Lock()
	p.killed = true
	p.mu.Unlock()
	select {
	case <-p.waitCh:
	default:
		close(p.waitCh)
	}
}

func TestManagerUsesInjectedTerminalProcessForCreateResizeInputAndKill(t *testing.T) {
	sqlDB := newTerminalRuntimeTestDB(t)
	if _, err := sqlDB.Exec(`INSERT INTO terminals (id, name, cwd, status, graph_opened, created_at) VALUES ('term-1', 'Terminal', '.', 'running', 0, 'now')`); err != nil {
		t.Fatalf("insert terminal: %v", err)
	}
	manager := NewManager(sqlDB, nil)
	manager.dtachAvailable = func() bool { return false }

	oldStarter := startTerminalProcess
	fake := newFakeTerminalProcess(0)
	startTerminalProcess = func(ctx context.Context, command string, args []string, cwd string, env []string, cols, rows int) (terminalProcess, error) {
		if command == "" || cwd == "" || len(env) == 0 || cols != 120 || rows != 40 {
			t.Fatalf("unexpected terminal start args: command=%q cwd=%q cols=%d rows=%d env=%d args=%v", command, cwd, cols, rows, len(env), args)
		}
		return fake, nil
	}
	t.Cleanup(func() { startTerminalProcess = oldStarter })

	if err := manager.EnsureSize(context.Background(), "term-1", 120, 40); err != nil {
		t.Fatalf("EnsureSize: %v", err)
	}
	if err := manager.Write(context.Background(), "term-1", "echo hello\n"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := manager.Resize(context.Background(), "term-1", 132, 50); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	fake.mu.Lock()
	if got := fake.stdin.String(); got != "echo hello\n" {
		fake.mu.Unlock()
		t.Fatalf("unexpected terminal stdin: %q", got)
	}
	if len(fake.resizeCalls) == 0 || fake.resizeCalls[len(fake.resizeCalls)-1].cols != 132 || fake.resizeCalls[len(fake.resizeCalls)-1].rows != 50 {
		fake.mu.Unlock()
		t.Fatalf("unexpected resize calls: %#v", fake.resizeCalls)
	}
	fake.mu.Unlock()

	if err := manager.Kill(context.Background(), "term-1"); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	select {
	case <-fake.waitCh:
	case <-time.After(2 * time.Second):
		t.Fatal("fake terminal did not exit after kill")
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if !fake.killed || !fake.closed {
		t.Fatalf("expected fake terminal to be killed and closed, got killed=%v closed=%v", fake.killed, fake.closed)
	}
	if session := manager.get("term-1"); session != nil {
		t.Fatalf("expected manager session cleanup, got %#v", session)
	}
}
