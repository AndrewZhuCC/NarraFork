package agentbasic

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadToolDetectsBinaryAndNumbersLines covers two Read paths not directly
// asserted elsewhere: a NUL-containing file is rejected as binary rather than
// returning garbled text, and a small text file is returned with cat -n style
// 1-based line numbers.
func TestReadToolDetectsBinaryAndNumbersLines(t *testing.T) {
	cwd := t.TempDir()

	// Binary file (contains a NUL byte) is rejected.
	binPath := filepath.Join(cwd, "image.bin")
	if err := os.WriteFile(binPath, []byte{0x89, 0x50, 0x00, 0x4e, 0x47}, 0o644); err != nil {
		t.Fatalf("write binary fixture: %v", err)
	}
	out := ExecuteTool(context.Background(), cwd, "Read", `{"file_path":"image.bin"}`)
	if !strings.Contains(out, "binary file cannot be read as text") {
		t.Fatalf("binary file should be rejected, got: %s", out)
	}

	// Text file is returned with 1-based line numbers (cat -n style).
	textPath := filepath.Join(cwd, "notes.txt")
	if err := os.WriteFile(textPath, []byte("first\nsecond\nthird\n"), 0o644); err != nil {
		t.Fatalf("write text fixture: %v", err)
	}
	out = ExecuteTool(context.Background(), cwd, "Read", `{"file_path":"notes.txt"}`)
	if !strings.Contains(out, "first") || !strings.Contains(out, "second") || !strings.Contains(out, "third") {
		t.Fatalf("text read should include all lines, got: %s", out)
	}
	// Line numbering: the first line must be tagged with line index 1, and the
	// third line with index 3 (exact prefix format may vary, so check presence
	// of the line numbers alongside their content).
	if !lineNumberedNear(out, "1", "first") {
		t.Fatalf("expected line 1 numbered with 'first', got: %s", out)
	}
	if !lineNumberedNear(out, "3", "third") {
		t.Fatalf("expected line 3 numbered with 'third', got: %s", out)
	}
}

// lineNumberedNear reports whether some output line contains both the line number
// token and the content (in that order), tolerating padding/tab variations.
func lineNumberedNear(out, number, content string) bool {
	for _, line := range strings.Split(out, "\n") {
		numIdx := strings.Index(line, number)
		contentIdx := strings.Index(line, content)
		if numIdx >= 0 && contentIdx > numIdx {
			return true
		}
	}
	return false
}

// TestReadToolTruncatesLongLinesLikeTS pins the Read tool's per-line truncation to
// the TS read tool MAX_LINE_CHARS=2000 (server/lib/agent/tools/read.ts
// truncateDisplayLine): a line longer than 2000 chars is cut to the first 2000
// followed by "… [line truncated, N chars total]". Go previously emitted the full
// line, dumping minified/huge single lines into the model context unbounded.
func TestReadToolTruncatesLongLinesLikeTS(t *testing.T) {
	cwd := t.TempDir()
	longLine := strings.Repeat("x", 5000)
	path := filepath.Join(cwd, "min.js")
	if err := os.WriteFile(path, []byte("short\n"+longLine+"\nend\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	out := ExecuteTool(context.Background(), cwd, "Read", `{"file_path":"min.js"}`)
	if !strings.Contains(out, "… [line truncated, 5000 chars total]") {
		t.Fatalf("long line should be truncated with marker, got len=%d sample=%.120q", len(out), out)
	}
	// The 5000-x run must not appear verbatim (only the first 2000 kept).
	if strings.Contains(out, longLine) {
		t.Fatalf("full 5000-char line must not be emitted verbatim")
	}
	// Short lines are untouched.
	if !strings.Contains(out, "short") || !strings.Contains(out, "end") {
		t.Fatalf("short lines should be preserved, got: %.120q", out)
	}
	// Exactly 2000 'x' chars retained before the marker.
	idx := strings.Index(out, "… [line truncated")
	if idx < 0 {
		t.Fatalf("marker not found")
	}
	head := out[:idx]
	xrun := strings.Count(head, "x")
	if xrun != 2000 {
		t.Fatalf("expected exactly 2000 retained chars before marker, got %d", xrun)
	}
}

// TestTruncateDisplayLineUTF16 confirms truncateDisplayLine counts/slices by
// UTF-16 code units (matching JS .length/.slice): a line of 2001 CJK chars (each 1
// UTF-16 unit) is truncated, while a 2000-char line is not.
func TestTruncateDisplayLineUTF16(t *testing.T) {
	exactly2000 := strings.Repeat("配", 2000)
	if got := truncateDisplayLine(exactly2000); got != exactly2000 {
		t.Fatalf("2000-char CJK line should not be truncated")
	}
	over := strings.Repeat("配", 2001)
	got := truncateDisplayLine(over)
	if !strings.Contains(got, "[line truncated, 2001 chars total]") {
		t.Fatalf("2001-char CJK line should be truncated, got %.60q", got)
	}
}
