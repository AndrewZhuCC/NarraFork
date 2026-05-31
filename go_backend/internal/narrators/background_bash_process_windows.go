//go:build windows

package narrators

import (
	"context"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

func configureBackgroundBashCommand(cmd *exec.Cmd) func() {
	if cmd == nil {
		return func() {}
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000200}
	return func() {
		if cmd.Process == nil || cmd.Process.Pid <= 0 {
			return
		}
		pid := strconv.Itoa(cmd.Process.Pid)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = exec.CommandContext(ctx, "taskkill", "/T", "/F", "/PID", pid).Run()
		}()
		_ = cmd.Process.Kill()
	}
}
