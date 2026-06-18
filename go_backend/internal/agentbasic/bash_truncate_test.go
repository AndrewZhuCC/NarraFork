package agentbasic

import (
	"context"
	"strings"
	"testing"
)

// TestBashToolTruncatesOversizedOutput covers the Bash tool's tail-keeping
// truncation (mirroring TS truncate.ts): a command producing more than the 50KB
// byte cap returns the TAIL of the output (most recent, e.g. trailing errors)
// with an "OUTPUT TRUNCATED" hint and a bounded body, while a small command's
// output is returned untruncated. This guards the bounded-output contract that
// protects the event loop / context AND the parity behaviour that recent output
// is preserved.
func TestBashToolTruncatesOversizedOutput(t *testing.T) {
	cwd := t.TempDir()

	// Produce numbered lines whose total exceeds the 50KB byte cap, so we can tell
	// whether the head or the tail was retained. Each line "L<seq>" is unique.
	out := ExecuteTool(context.Background(), cwd, "Bash",
		`{"command":"for i in $(seq 1 20000); do echo \"L$i marker padding padding padding\"; done","timeout":20000}`)
	if !strings.Contains(out, "OUTPUT TRUNCATED") {
		t.Fatalf("oversized bash output should carry the OUTPUT TRUNCATED hint, got: %.200q", out)
	}
	// Tail retained: the last line must be present, the first line must be gone.
	if !strings.Contains(out, "L20000 ") {
		t.Fatalf("oversized bash output should retain the TAIL (last line L20000), got: %.300q", out[max0(len(out)-300):])
	}
	if strings.Contains(out, "L1 marker") {
		t.Fatalf("oversized bash output should drop the HEAD (first line L1), but it was present")
	}
	// The retained body must be bounded near the 50KB cap (allow header + hint slack).
	if len(out) > toolOutputMaxBytes+2048 {
		t.Fatalf("truncated bash output should be bounded near 50KB, got %d bytes", len(out))
	}

	// A small command is returned untruncated, with no truncation hint.
	small := ExecuteTool(context.Background(), cwd, "Bash", `{"command":"echo small-output"}`)
	if !strings.Contains(small, "small-output") || strings.Contains(small, "OUTPUT TRUNCATED") {
		t.Fatalf("small bash output should not be truncated, got: %s", small)
	}
}

// TestBashToolTruncatesByLineCount covers the 2000-line cap: many short lines that
// stay under the 50KB byte cap are still truncated by line count, keeping the
// last 2000 lines.
func TestBashToolTruncatesByLineCount(t *testing.T) {
	cwd := t.TempDir()
	// 3000 very short lines: well under 50KB total but over the 2000-line cap.
	out := ExecuteTool(context.Background(), cwd, "Bash",
		`{"command":"seq 1 3000","timeout":20000}`)
	if !strings.Contains(out, "OUTPUT TRUNCATED") {
		t.Fatalf("over-2000-line output should be truncated, got: %.200q", out)
	}
	if !strings.Contains(out, "lines truncated") {
		t.Fatalf("line-count truncation should report a lines count, got: %.200q", out)
	}
	// Last line kept, an early (dropped) line gone.
	if !strings.Contains(out, "\n3000") {
		t.Fatalf("line-count truncation should keep the last line 3000, got tail: %.200q", out[max0(len(out)-200):])
	}
	if strings.Contains(out, "\n1\n") {
		t.Fatalf("line-count truncation should drop early line 1")
	}
}

// TestBashToolTruncationPreservesExitCodeAtTail verifies the non-zero exit code
// remains visible after tail-keeping truncation (it is appended after the
// truncated body, matching TS where the trailing exit code survives tail-keep).
func TestBashToolTruncationPreservesExitCodeAtTail(t *testing.T) {
	cwd := t.TempDir()
	out := ExecuteTool(context.Background(), cwd, "Bash",
		`{"command":"for i in $(seq 1 20000); do echo \"L$i padding padding padding\"; done; exit 3","timeout":20000}`)
	if !strings.Contains(out, "OUTPUT TRUNCATED") {
		t.Fatalf("expected truncation, got: %.200q", out)
	}
	if !strings.Contains(out, "[exit code: 3]") {
		t.Fatalf("exit code must remain visible after truncation, got tail: %.200q", out[max0(len(out)-200):])
	}
}

func max0(n int) int {
	if n < 0 {
		return 0
	}
	return n
}
