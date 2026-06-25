//go:build !windows

package narrators

import (
	"os/exec"
	"syscall"
	"time"
)

// configureBackgroundBashCommand sets up process-group isolation and a
// context-driven cancellation that terminates the whole group. The kill logic
// is installed as cmd.Cancel instead of being returned as a closure invoked from
// another goroutine: os/exec runs cmd.Cancel from the watch goroutine it starts
// inside Start() AFTER cmd.Process is assigned, so reading cmd.Process there is
// free of the data race that a directly-invoked closure would have against
// cmd.Run()/Start(). Cancellation therefore flows solely through the command's
// context (cancel() -> ctx.Done() -> cmd.Cancel()).
func configureBackgroundBashCommand(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil || cmd.Process.Pid <= 0 {
			return nil
		}
		pgid, err := syscall.Getpgid(cmd.Process.Pid)
		if err != nil || pgid <= 0 {
			return cmd.Process.Signal(syscall.SIGTERM)
		}
		_ = syscall.Kill(-pgid, syscall.SIGTERM)
		time.AfterFunc(500*time.Millisecond, func() {
			if syscall.Kill(-pgid, 0) == nil {
				_ = syscall.Kill(-pgid, syscall.SIGKILL)
			}
		})
		return nil
	}
	// Safety net: if a surviving grandchild keeps the output pipe open after the
	// group is signalled, force Wait() to return rather than blocking the
	// background task goroutine indefinitely.
	cmd.WaitDelay = 2 * time.Second
}
