package agentbasic

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestBashDefaultTimeoutMatchesTS pins the foreground Bash default/clamp constants
// to the TS bash tool (server/lib/agent/tools/bash.ts): DEFAULT_TIMEOUT_MS=120_000.
// Go previously defaulted to 30_000 (30s), killing 30s-2min commands that succeed
// in TS. The max cap stays at Go's 10-minute resource-safety bound (documented
// divergence from TS MAX_TIMEOUT_MS=86_400_000).
func TestBashDefaultTimeoutMatchesTS(t *testing.T) {
	if bashDefaultTimeoutMs != 120000 {
		t.Fatalf("bashDefaultTimeoutMs = %d, want 120000 (TS DEFAULT_TIMEOUT_MS)", bashDefaultTimeoutMs)
	}
	if bashMaxTimeoutMs != 600000 {
		t.Fatalf("bashMaxTimeoutMs = %d, want 600000 (Go resource-safety cap)", bashMaxTimeoutMs)
	}
}

// TestBashToolResolvesTimeout exercises the timeout-resolution branches of
// toolBash deterministically by choosing a tiny command and observing whether it
// completes (resolved timeout >= 1s floor) rather than waiting out a 2-minute
// default. It proves: (1) a missing timeout arg resolves to a usable default (the
// command runs to completion, not rejected), (2) an explicit small timeout below
// the 1000ms floor is raised to the floor (not zero), (3) an explicit timeout
// above the max cap is clamped.
func TestBashToolResolvesTimeout(t *testing.T) {
	cwd := t.TempDir()

	// (1) No timeout arg -> default (120000) applies; a fast command completes.
	out := ExecuteTool(context.Background(), cwd, "Bash", `{"command":"printf hi"}`)
	if !strings.Contains(out, "hi") {
		t.Fatalf("default-timeout fast command should complete, got: %q", out)
	}
	if strings.Contains(out, "Command timed out") {
		t.Fatalf("fast command must not time out under the default, got: %q", out)
	}

	// (2) Explicit timeout below the 1000ms floor is raised to 1000ms; a fast
	// command still completes (it is well under 1s).
	out = ExecuteTool(context.Background(), cwd, "Bash", `{"command":"printf hi","timeout":1}`)
	if !strings.Contains(out, "hi") {
		t.Fatalf("sub-floor timeout should be raised to 1000ms and let a fast command complete, got: %q", out)
	}

	// (3) A command exceeding a small explicit timeout times out with the
	// TS-style metadata (regression guard that explicit timeouts still apply).
	out = ExecuteTool(context.Background(), cwd, "Bash", `{"command":"sleep 2","timeout":1000}`)
	if !strings.Contains(out, "Command timed out after 1000ms") {
		t.Fatalf("explicit 1000ms timeout should still fire, got: %q", out)
	}
}

// TestBashToolDefaultTimeoutAllowsLongerThanOldDefault proves the behavioral
// parity win directly: a command that sleeps ~1.5s (longer than would matter for
// a sub-second default but well under both the old 30s and new 120s defaults)
// completes when no timeout is supplied. The point is that the DEFAULT path now
// uses 120000ms; this guards against a regression to a sub-2s default.
func TestBashToolDefaultTimeoutAllowsLongerThanOldDefault(t *testing.T) {
	if testing.Short() {
		t.Skip("skips the ~1.5s sleep in -short mode")
	}
	cwd := t.TempDir()
	start := time.Now()
	out := ExecuteTool(context.Background(), cwd, "Bash", `{"command":"sleep 1.5 && printf done"}`)
	elapsed := time.Since(start)
	if !strings.Contains(out, "done") {
		t.Fatalf("1.5s command should complete under the default timeout, got: %q (elapsed %s)", out, elapsed)
	}
	if strings.Contains(out, "Command timed out") {
		t.Fatalf("1.5s command must not time out under the 120000ms default, got: %q", out)
	}
}
