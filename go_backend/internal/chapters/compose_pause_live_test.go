package chapters

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// composePsState runs `compose ps --format json` and returns the normalized
// status of the first parsed instance, exercising the same parse + normalize
// path the runtime uses.
func composePsState(ctx context.Context, t *testing.T, cmd []string) string {
	t.Helper()
	psArgs := append(append([]string{}, cmd[1:]...), "ps", "--format", "json")
	out, err := exec.CommandContext(ctx, cmd[0], psArgs...).Output()
	if err != nil {
		t.Fatalf("compose ps failed: %v", err)
	}
	raw := parseContainerJSONList(string(out))
	if len(raw) == 0 {
		return ""
	}
	return normalizeContainerInstance("chap-pause-live", raw[0])["status"].(string)
}

// TestComposePauseUnpauseStateTransitionsLikeRealPodman runs a REAL compose
// up → pause → unpause → down cycle and verifies that normalizeContainerStatus
// maps podman's real State values across the transition (running → paused →
// running). This closes the fixture-only gap for the dormant/wake (pause/unpause)
// container state machine: previously only fake-Podman covered it, so the
// running→paused→running mapping was never proven against real podman output.
func TestComposePauseUnpauseStateTransitionsLikeRealPodman(t *testing.T) {
	if !realPodmanForInspect(t) {
		t.Skip("real podman not available; skipping live pause/unpause parity test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()

	if len(composeVersion(ctx).command) == 0 {
		t.Skip("no podman compose provider available; skipping")
	}

	work, err := os.MkdirTemp(".", "pause-live-")
	if err != nil {
		t.Fatalf("mkdir temp work: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(work) })
	composeFile := filepath.Join(work, "compose.yml")
	composeYML := "services:\n  app:\n    image: docker.io/library/golang:1.26-alpine\n    entrypoint: [\"sleep\", \"150\"]\n"
	if err := os.WriteFile(composeFile, []byte(composeYML), 0o644); err != nil {
		t.Fatalf("write compose file: %v", err)
	}

	cmd := composeCommandForFile(ctx, composeFile)
	if len(cmd) == 0 {
		t.Skip("composeCommandForFile resolved empty; skipping")
	}
	run := func(action string) error {
		args := append(append([]string{}, cmd[1:]...), action)
		if action == "up" {
			args = append(args, "-d")
		}
		return exec.CommandContext(ctx, cmd[0], args...).Run()
	}

	if err := run("up"); err != nil {
		t.Skipf("compose up failed (registry/storage unavailable?): %v", err)
	}
	t.Cleanup(func() {
		downArgs := append(append([]string{}, cmd[1:]...), "down")
		_ = exec.Command(cmd[0], downArgs...).Run()
	})

	if got := composePsState(ctx, t, cmd); got != "running" {
		t.Fatalf("after up, normalized status = %q, want running", got)
	}

	if err := run("pause"); err != nil {
		t.Fatalf("compose pause failed: %v", err)
	}
	if got := composePsState(ctx, t, cmd); got != "paused" {
		t.Fatalf("after pause, normalized status = %q, want paused", got)
	}

	if err := run("unpause"); err != nil {
		t.Fatalf("compose unpause failed: %v", err)
	}
	if got := composePsState(ctx, t, cmd); got != "running" {
		t.Fatalf("after unpause, normalized status = %q, want running", got)
	}
}
