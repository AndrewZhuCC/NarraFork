package narrators

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"unicode/utf16"
)

// setupTerminalReadTest creates a service + narrator + one terminal and returns
// the service, narrator, terminal id, and runtime so a test can inject snapshot
// output and exercise the read tool.
func setupTerminalReadTest(t *testing.T) (*Service, Narrator, string, *fakeAgentTerminalRuntime) {
	t.Helper()
	sqlDB := openNarratorHandlerTestDB(t)
	createTerminalAgentToolSchema(t, sqlDB)
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	runtime := newFakeAgentTerminalRuntime()
	service := &Service{DB: sqlDB, TerminalRuntime: runtime}
	narrator, err := service.Get(context.Background(), narratorID)
	if err != nil {
		t.Fatalf("load narrator: %v", err)
	}
	if _, _, err := service.executeTerminalAgentTool(context.Background(), narratorID, narrator, map[string]any{"action": "create", "name": "Dev"}); err != nil {
		t.Fatalf("create terminal: %v", err)
	}
	terminalID := terminalToolOnlyID(t, sqlDB)
	return service, narrator, terminalID, runtime
}

// TestTerminalToolReadKeepsTailOnLargeIncrementalOutput verifies that incremental
// terminal reads keep the most recent output (tail), mirroring the TS tailSlice
// behaviour, rather than the previous head-keeping truncation.
func TestTerminalToolReadKeepsTailOnLargeIncrementalOutput(t *testing.T) {
	service, narrator, terminalID, runtime := setupTerminalReadTest(t)

	// Produce many numbered lines exceeding the 200-line / 40000-char incremental
	// caps so we can tell whether the head or the tail was retained.
	var b strings.Builder
	for i := 1; i <= 5000; i++ {
		fmt.Fprintf(&b, "L%d marker padding padding padding\n", i)
	}
	runtime.snapshots[terminalID] = b.String()

	out, status, err := service.executeTerminalAgentTool(context.Background(), narrator.ID, narrator, map[string]any{"action": "read", "terminal_id": "Dev"})
	if err != nil || status != "" {
		t.Fatalf("read err=%v status=%q", err, status)
	}
	if !strings.Contains(out, "L5000 ") {
		t.Fatalf("incremental read should retain the TAIL (last line L5000), got tail: %.200q", out[max0i(len(out)-200):])
	}
	if strings.Contains(out, "L1 marker") {
		t.Fatalf("incremental read should drop the HEAD (first line L1), but it was present")
	}
	// Bounded near the 40000 UTF-16 char incremental cap (allow slack).
	if units := utf16.Encode([]rune(out)); len(units) > terminalReadIncrementalMaxChars+200 {
		t.Fatalf("incremental read should be bounded near %d chars, got %d", terminalReadIncrementalMaxChars, len(units))
	}
}

// TestTerminalToolReadLastNLinesReturnsLastLines verifies the explicit
// last_n_lines branch returns the last N lines.
func TestTerminalToolReadLastNLinesReturnsLastLines(t *testing.T) {
	service, narrator, terminalID, runtime := setupTerminalReadTest(t)
	runtime.snapshots[terminalID] = "alpha\nbeta\ngamma\ndelta\nepsilon\n"

	out, status, err := service.executeTerminalAgentTool(context.Background(), narrator.ID, narrator, map[string]any{"action": "read", "terminal_id": "Dev", "last_n_lines": float64(2)})
	if err != nil || status != "" {
		t.Fatalf("read err=%v status=%q", err, status)
	}
	trimmed := strings.TrimSpace(out)
	if trimmed != "delta\nepsilon" {
		t.Fatalf("last_n_lines=2 should return the last two lines, got %q", trimmed)
	}
}

func TestTailSliceTerminalOutput(t *testing.T) {
	// Line cap: keep only the last maxLines lines.
	in := "a\nb\nc\nd\ne"
	if got := tailSliceTerminalOutput(in, 2, 1000); got != "d\ne" {
		t.Fatalf("line cap = %q, want d\\ne", got)
	}
	// Char cap: trim to last maxChars then drop the leading partial line.
	long := strings.Repeat("x", 30) + "\n" + strings.Repeat("y", 30)
	got := tailSliceTerminalOutput(long, 100, 40)
	if strings.Contains(got, "x") {
		t.Fatalf("char cap should drop the leading partial x-line, got %q", got)
	}
	if !strings.Contains(got, "y") {
		t.Fatalf("char cap should retain the trailing y-line, got %q", got)
	}
	// Small input returned unchanged.
	if got := tailSliceTerminalOutput("hi\nthere", 100, 1000); got != "hi\nthere" {
		t.Fatalf("small input should be unchanged, got %q", got)
	}
}

func max0i(n int) int {
	if n < 0 {
		return 0
	}
	return n
}
