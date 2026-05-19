//go:build !windows

package terminalruntime

import (
	"context"
	"os"
	"os/exec"
	"syscall"

	"github.com/creack/pty"
)

type osTerminalProcess struct {
	cmd  *exec.Cmd
	file *os.File
}

func defaultTerminalCommand() (string, []string) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	return shell, []string{"-l"}
}

func startPlatformTerminal(ctx context.Context, command string, args []string, cwd string, env []string, cols, rows int) (terminalProcess, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = cwd
	cmd.Env = env
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		return nil, err
	}
	return &osTerminalProcess{cmd: cmd, file: ptmx}, nil
}

func (p *osTerminalProcess) Read(buf []byte) (int, error) {
	return p.file.Read(buf)
}

func (p *osTerminalProcess) Write(buf []byte) (int, error) {
	return p.file.Write(buf)
}

func (p *osTerminalProcess) Close() error {
	return p.file.Close()
}

func (p *osTerminalProcess) Resize(cols, rows int) error {
	return pty.Setsize(p.file, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
}

func (p *osTerminalProcess) PID() int {
	if p.cmd == nil || p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

func (p *osTerminalProcess) Wait() int {
	err := p.cmd.Wait()
	if err == nil {
		return 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode()
	}
	return 1
}

func (p *osTerminalProcess) KillTree(context.Context) {
	if pid := p.PID(); pid > 0 {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}

func (m *Manager) killProcessTree(ctx context.Context, pid int) {
	killProcessTree(ctx, pid, m.processSnapshot(ctx))
}

func killProcessTree(_ context.Context, pid int, snapshot processSnapshot) {
	if pid <= 0 {
		return
	}
	descendants := snapshot.descendants(pid)
	for i := len(descendants) - 1; i >= 0; i-- {
		_ = syscall.Kill(descendants[i], syscall.SIGKILL)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
}

func signalTerminalResize(pid int) {
	if pid > 0 {
		_ = syscall.Kill(pid, syscall.SIGWINCH)
	}
}

func platformTerminalCapabilities() map[string]any {
	return map[string]any{
		"supported":      true,
		"directPty":      true,
		"windowsPty":     false,
		"runtime":        "unix-pty",
		"fallback":       false,
		"dtachSupported": true,
		"orphanRecovery": true,
	}
}
