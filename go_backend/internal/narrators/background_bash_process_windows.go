//go:build windows

package narrators

import (
	"context"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

// configureBackgroundBashCommand mirrors the Unix variant: the kill logic is
// installed as cmd.Cancel (invoked by os/exec on its watch goroutine after
// cmd.Process is set) so reading cmd.Process is race-free. Cancellation flows
// through the command's context.
func configureBackgroundBashCommand(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000200}
	cmd.Cancel = func() error {
		if cmd.Process == nil || cmd.Process.Pid <= 0 {
			return nil
		}
		pid := strconv.Itoa(cmd.Process.Pid)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = exec.CommandContext(ctx, "taskkill", "/T", "/F", "/PID", pid).Run()
		}()
		return cmd.Process.Kill()
	}
	cmd.WaitDelay = 2 * time.Second
}
