//go:build !windows

package narrators

import (
	"os/exec"
	"syscall"
	"time"
)

func configureBackgroundBashCommand(cmd *exec.Cmd) func() {
	if cmd == nil {
		return func() {}
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return func() {
		if cmd.Process == nil || cmd.Process.Pid <= 0 {
			return
		}
		pgid, err := syscall.Getpgid(cmd.Process.Pid)
		if err != nil || pgid <= 0 {
			_ = cmd.Process.Signal(syscall.SIGTERM)
			return
		}
		_ = syscall.Kill(-pgid, syscall.SIGTERM)
		time.AfterFunc(500*time.Millisecond, func() {
			if syscall.Kill(-pgid, 0) == nil {
				_ = syscall.Kill(-pgid, syscall.SIGKILL)
			}
		})
	}
}
