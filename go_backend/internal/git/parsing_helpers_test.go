package git

import (
	"reflect"
	"testing"
)

// TestSplitLines covers the line splitter used across git output parsing: it
// trims a trailing newline, drops empty lines, strips CR, and returns nil for
// empty/whitespace-only input.
func TestSplitLines(t *testing.T) {
	if got := splitLines(""); got != nil {
		t.Fatalf("empty input should yield nil, got %#v", got)
	}
	if got := splitLines("   "); got != nil {
		t.Fatalf("whitespace-only should yield nil, got %#v", got)
	}
	if got := splitLines("a\r\nb\n\nc\n"); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Fatalf("splitLines = %#v", got)
	}
}

// TestParseGitStatInt covers the numstat integer parser: "-" (binary) and
// unparsable values yield 0; numbers (with surrounding space) parse normally.
func TestParseGitStatInt(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"-", 0},
		{"", 0},
		{"xyz", 0},
		{"5", 5},
		{"  42 ", 42},
	}
	for _, tc := range cases {
		if got := parseGitStatInt(tc.in); got != tc.want {
			t.Fatalf("parseGitStatInt(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

// TestCommitStatusFromCode covers the git name-status code to frontend status
// mapping (A/D/R/C/T/default and the empty code default).
func TestCommitStatusFromCode(t *testing.T) {
	cases := map[string]string{
		"":     "modified",
		"A":    "added",
		"D":    "deleted",
		"R100": "renamed",
		"C75":  "copied",
		"T":    "typechange",
		"M":    "modified",
		"X":    "modified",
	}
	for code, want := range cases {
		if got := commitStatusFromCode(code); got != want {
			t.Fatalf("commitStatusFromCode(%q) = %q, want %q", code, got, want)
		}
	}
}

// TestParseMergeTreeConflictFiles covers conflict-file extraction from git
// merge-tree output: +++ b/<file> lines, "CONFLICT (...) in <file>" lines, and
// our/their sections are all collected and de-duplicated; b/ prefix is stripped.
func TestParseMergeTreeConflictFiles(t *testing.T) {
	out := `changed in both
  base   100644 abc src/app.go
  our    100644 def src/app.go
  their  100644 ghi src/app.go
@@ -1,3 +1,5 @@
CONFLICT (content): Merge conflict in src/app.go
CONFLICT (add/add): Merge conflict in README.md
+++ b/src/util.go`

	got := parseMergeTreeConflictFiles(out)
	// src/app.go appears via our/their and CONFLICT line but must be de-duplicated.
	seen := map[string]int{}
	for _, f := range got {
		seen[f]++
	}
	for _, want := range []string{"src/app.go", "README.md", "src/util.go"} {
		if seen[want] == 0 {
			t.Fatalf("expected conflict file %q in %#v", want, got)
		}
		if seen[want] > 1 {
			t.Fatalf("conflict file %q should be de-duplicated, count=%d", want, seen[want])
		}
	}

	if got := parseMergeTreeConflictFiles(""); len(got) != 0 {
		t.Fatalf("empty merge-tree output should yield no conflicts, got %#v", got)
	}
}
