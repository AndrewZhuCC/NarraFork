package agentbasic

import (
	"context"
	"strings"
	"testing"
)

// TestBashToolReportsExitErrorWithCapturedOutput covers the non-zero-exit path of
// the Bash tool: a failing command surfaces the numeric "[exit code: N]" marker
// (matching the TS bash tool) while still returning the stdout produced before the
// failure. Existing Bash tests cover empty-command, timeout, workdir, and background
// rejection but not this core failure-reporting contract.
func TestBashToolReportsExitErrorWithCapturedOutput(t *testing.T) {
	cwd := t.TempDir()

	// A command that prints to stdout then exits non-zero.
	out := ExecuteTool(context.Background(), cwd, "Bash",
		`{"command":"echo partial-output; exit 3"}`)
	if !strings.Contains(out, "partial-output") {
		t.Fatalf("bash should capture stdout before failure, got: %s", out)
	}
	// TS surfaces the NUMERIC exit code, e.g. "[exit code: 3]".
	if !strings.Contains(out, "[exit code: 3]") {
		t.Fatalf("bash should surface the numeric exit code on non-zero exit, got: %s", out)
	}

	// A successful command returns its stdout without an exit-code marker.
	ok := ExecuteTool(context.Background(), cwd, "Bash", `{"command":"echo done"}`)
	if !strings.Contains(ok, "done") {
		t.Fatalf("successful bash should return stdout, got: %s", ok)
	}
	if strings.Contains(ok, "[exit code:") {
		t.Fatalf("successful bash should not carry an exit-code marker, got: %s", ok)
	}
}

// TestBashToolEmptyOutputMatchesTS covers the empty-output contract: a successful
// command that prints nothing returns the literal "(no output)" placeholder
// (matching the TS `truncateOutput(output || "(no output)")` fallback) instead of
// an empty string.
func TestBashToolEmptyOutputMatchesTS(t *testing.T) {
	cwd := t.TempDir()
	out := ExecuteTool(context.Background(), cwd, "Bash", `{"command":"true"}`)
	if strings.TrimSpace(out) != "(no output)" {
		t.Fatalf("expected (no output) placeholder for empty stdout, got: %q", out)
	}
}
