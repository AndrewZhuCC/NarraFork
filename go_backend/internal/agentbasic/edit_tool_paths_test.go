package agentbasic

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEditToolSingleMatchReplaceAndNotFound covers two Edit paths not asserted by
// the existing unique-match/replace-all/identical/size tests: the common
// single-unique-match successful replacement (with on-disk verification) and the
// "old_string not found" error contract.
func TestEditToolSingleMatchReplaceAndNotFound(t *testing.T) {
	cwd := t.TempDir()
	path := filepath.Join(cwd, "edit.txt")
	if err := os.WriteFile(path, []byte("alpha\nbeta\ngamma\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	// Single unique match replaces successfully and persists to disk.
	out := ExecuteTool(context.Background(), cwd, "Edit",
		`{"file_path":"edit.txt","old_string":"beta","new_string":"BETA"}`)
	if strings.Contains(out, "not found") || strings.Contains(out, "multiple matches") {
		t.Fatalf("single-match edit should succeed, got: %s", out)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read edited file: %v", err)
	}
	if string(data) != "alpha\nBETA\ngamma\n" {
		t.Fatalf("unexpected edited content: %q", string(data))
	}

	// A missing old_string returns the not-found error and does not mutate the file.
	out = ExecuteTool(context.Background(), cwd, "Edit",
		`{"file_path":"edit.txt","old_string":"does-not-exist","new_string":"x"}`)
	if !strings.Contains(out, "old_string not found") {
		t.Fatalf("expected old_string not found, got: %s", out)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file after failed edit: %v", err)
	}
	if string(after) != "alpha\nBETA\ngamma\n" {
		t.Fatalf("failed edit must not mutate the file, got: %q", string(after))
	}
}

// TestEditToolMissingFileReturnsCleanError verifies editing a non-existent file
// (with a non-empty old_string) returns the TS-compatible "File not found: <path>"
// message instead of leaking the raw OS stat error.
func TestEditToolMissingFileReturnsCleanError(t *testing.T) {
	cwd := t.TempDir()
	out := ExecuteTool(context.Background(), cwd, "Edit",
		`{"file_path":"missing.txt","old_string":"foo","new_string":"bar"}`)
	if out != "File not found: missing.txt" {
		t.Fatalf("expected clean File not found message, got: %q", out)
	}
	if strings.Contains(out, "no such file") || strings.Contains(out, "stat ") {
		t.Fatalf("missing-file edit must not leak the raw OS error, got: %q", out)
	}
}

// TestEditToolLineTrimmedMatchLikeTS pins the cascading replacer chain ported
// from TS: an old_string whose lines differ only by leading/trailing whitespace
// from the file (a very common model output) still matches via the
// LineTrimmedReplacer instead of failing exact match. The replacement preserves
// the file's original indentation (the matched original substring is replaced).
func TestEditToolLineTrimmedMatchLikeTS(t *testing.T) {
	cwd := t.TempDir()
	path := filepath.Join(cwd, "code.go")
	// File has tab indentation on a two-line block.
	if err := os.WriteFile(path, []byte("func f() {\n\tif x {\n\t\treturn 1\n\t}\n}\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	// old_string is the same two lines but with SPACE indentation (different
	// leading whitespace) — exact substring match fails; LineTrimmedReplacer
	// matches and the original (tab-indented) text is replaced.
	out := ExecuteTool(context.Background(), cwd, "Edit",
		`{"file_path":"code.go","old_string":"  if x {\n    return 1","new_string":"\tif y {\n\t\treturn 2"}`)
	if strings.Contains(out, "not found") || strings.Contains(out, "multiple matches") {
		t.Fatalf("line-trimmed edit should succeed, got: %s", out)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "func f() {\n\tif y {\n\t\treturn 2\n\t}\n}\n" {
		t.Fatalf("unexpected edited content: %q", string(data))
	}
}

// TestEditToolNormalizesCRLFLikeTS pins the CRLF->LF normalization: a CRLF file
// edited with an LF old_string matches (TS normalizes both before matching) and
// the written file is normalized to LF.
func TestEditToolNormalizesCRLFLikeTS(t *testing.T) {
	cwd := t.TempDir()
	path := filepath.Join(cwd, "win.txt")
	if err := os.WriteFile(path, []byte("one\r\ntwo\r\nthree\r\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	out := ExecuteTool(context.Background(), cwd, "Edit",
		`{"file_path":"win.txt","old_string":"two","new_string":"TWO"}`)
	if strings.Contains(out, "not found") {
		t.Fatalf("CRLF edit should match LF old_string, got: %s", out)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "\r\n") {
		t.Fatalf("edited file should be normalized to LF, got: %q", string(data))
	}
	if string(data) != "one\nTWO\nthree\n" {
		t.Fatalf("unexpected edited content: %q", string(data))
	}
}

// TestEditToolBlockAnchorMatchLikeTS pins the BlockAnchorReplacer: a 3+-line
// old_string whose first and last lines match anchors in the file but whose
// middle line differs (within Levenshtein similarity) still matches and the
// original block is replaced.
func TestEditToolBlockAnchorMatchLikeTS(t *testing.T) {
	cwd := t.TempDir()
	path := filepath.Join(cwd, "block.txt")
	// Middle line in the file is "middle content here".
	if err := os.WriteFile(path, []byte("START\nmiddle content here\nEND\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	// old_string anchors on START/END but the middle line differs slightly.
	out := ExecuteTool(context.Background(), cwd, "Edit",
		`{"file_path":"block.txt","old_string":"START\nmiddle content HER\nEND","new_string":"START\nreplaced\nEND"}`)
	if strings.Contains(out, "not found") || strings.Contains(out, "multiple matches") {
		t.Fatalf("block-anchor edit should succeed, got: %s", out)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "START\nreplaced\nEND\n" {
		t.Fatalf("unexpected edited content: %q", string(data))
	}
}

// TestEditToolEscapeNormalizedMatchLikeTS pins the EscapeNormalizedReplacer: an
// old_string containing literal escape sequences (\n, \t) matches the real
// newline/tab content in the file.
func TestEditToolEscapeNormalizedMatchLikeTS(t *testing.T) {
	cwd := t.TempDir()
	path := filepath.Join(cwd, "esc.txt")
	if err := os.WriteFile(path, []byte("line one\nline two\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	// old_string uses a literal backslash-n instead of a real newline.
	out := ExecuteTool(context.Background(), cwd, "Edit",
		`{"file_path":"esc.txt","old_string":"line one\\nline two","new_string":"replaced"}`)
	if strings.Contains(out, "not found") {
		t.Fatalf("escape-normalized edit should match, got: %s", out)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "replaced\n" {
		t.Fatalf("unexpected edited content: %q", string(data))
	}
}

// TestEditWhitespaceNormalizedSingleLinePartialMatch drives the replacer directly
// to pin the TS edit.ts:151-166 else-branch: when the normalized find is a
// substring within a single line whose internal whitespace differs, the replacer
// yields the matched ORIGINAL substring (not the whole line) via a words-joined-
// by-\s+ regex over the original line.
func TestEditWhitespaceNormalizedSingleLinePartialMatch(t *testing.T) {
	// Partial match within a line: file has two spaces, find has one.
	got := editWhitespaceNormalizedReplacer("  result = call(a,  b, c)", "call(a, b, c)")
	foundPartial := false
	for _, s := range got {
		if s == "call(a,  b, c)" {
			foundPartial = true
		}
		if s == "  result = call(a,  b, c)" {
			t.Fatalf("expected the partial substring, not the whole line: %q", s)
		}
	}
	if !foundPartial {
		t.Fatalf("expected partial substring `call(a,  b, c)` in candidates, got: %#v", got)
	}

	// Whole-line equality still goes through branch (a) and yields the full line.
	got = editWhitespaceNormalizedReplacer("call(a,  b, c)", "call(a, b, c)")
	if len(got) == 0 || got[0] != "call(a,  b, c)" {
		t.Fatalf("whole-line normalized match should yield the line, got: %#v", got)
	}

	// A find not present in the line yields nothing (no false match).
	got = editWhitespaceNormalizedReplacer("totally unrelated text", "call(a, b, c)")
	if len(got) != 0 {
		t.Fatalf("non-matching find must yield no candidates, got: %#v", got)
	}
}

// TestEditToolWhitespaceNormalizedPartialMatchLikeTS is the end-to-end proof: an
// old_string that is a single-line substring whose internal whitespace differs
// from the file matches and replaces just that substring, mirroring TS. Without
// the WhitespaceNormalized single-line partial branch this returns "not found".
func TestEditToolWhitespaceNormalizedPartialMatchLikeTS(t *testing.T) {
	cwd := t.TempDir()
	path := filepath.Join(cwd, "ws.txt")
	// File uses two spaces inside the call; the surrounding line has a prefix.
	if err := os.WriteFile(path, []byte("  result = compute(x,  y)\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	out := ExecuteTool(context.Background(), cwd, "Edit",
		`{"file_path":"ws.txt","old_string":"compute(x, y)","new_string":"compute(x, z)"}`)
	if strings.Contains(out, "not found") || strings.Contains(out, "multiple matches") {
		t.Fatalf("whitespace-normalized partial edit should succeed, got: %s", out)
	}
	data, _ := os.ReadFile(path)
	// Only the matched substring is replaced; the prefix and trailing newline stay.
	if string(data) != "  result = compute(x, z)\n" {
		t.Fatalf("unexpected edited content: %q", string(data))
	}
}
