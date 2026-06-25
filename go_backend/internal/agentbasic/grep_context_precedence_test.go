package agentbasic

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGrepToolContextPrecedenceEndToEnd is an end-to-end safety net over the
// grep context-line precedence fix (grepContextBounds, unit-tested in
// grep_context_bounds_test.go::TestGrepContextBoundsPrecedence): it drives the
// Grep tool through ExecuteTool to confirm that when both `context` and `-A`
// are provided in content mode, the symmetric context wins (前后各 N 行),
// matching the TS grep tool which passes only -C to rg (tools/grep.ts:216-222).
// The pre-fix Go behavior reset to before=0/after=0 then applied -A, dropping
// the before line.
func TestGrepToolContextPrecedenceEndToEnd(t *testing.T) {
	cwd := t.TempDir()
	// 5 lines; match the middle line ("match") so symmetric context can show
	// both directions.
	if err := os.WriteFile(filepath.Join(cwd, "f.txt"), []byte("l1\nl2\nmatch\nl4\nl5\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	// context:1 + -A:0: TS -> -C 1 -> shows l2 (before) and l4 (after). The old
	// Go behavior would reset to before=0/after=0 then apply -A:0 => only the
	// match line (no l2, no l4).
	out := ExecuteTool(context.Background(), cwd, "Grep", `{"pattern":"match","output_mode":"content","context":1,"-A":0,"-n":false}`)
	if !strings.Contains(out, "l2") {
		t.Fatalf("symmetric context should win over -A and show the before line l2, got: %s", out)
	}
	if !strings.Contains(out, "l4") {
		t.Fatalf("symmetric context should win over -A and show the after line l4, got: %s", out)
	}
}
