package agentbasic

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadArgIntRoundsLikeJSMathRound pins the Read offset/limit numeric coercion
// to JavaScript Math.round semantics (TS read.ts:111-119 does Math.round on the
// raw offset/limit before clamping). Go previously truncated floats via intValue
// (int(2.7)==2), so a float offset/limit read from a different line than TS.
func TestReadArgIntRoundsLikeJSMathRound(t *testing.T) {
	cases := []struct {
		in       any
		fallback int
		want     int
	}{
		{2.7, 1, 3}, // Math.round(2.7) == 3 (Go int(2.7) was 2)
		{2.4, 1, 2}, // Math.round(2.4) == 2
		{2.5, 1, 3}, // Math.round(2.5) == 3 (half up toward +Inf)
		{3.0, 1, 3}, // whole float
		{float64(10), 1, 10},
		{nil, 1, 1},   // missing -> fallback
		{nil, 0, 0},   // missing -> fallback
		{"x", 5, 5},   // non-numeric -> fallback
		{7, 1, 7},     // already int
		{-1.0, 0, -1}, // -1 (readAll sentinel) preserved
		{-2.5, 0, -2}, // Math.round(-2.5) == -2 (half toward +Inf, NOT -3)
	}
	for _, tc := range cases {
		if got := readArgInt(tc.in, tc.fallback); got != tc.want {
			t.Fatalf("readArgInt(%v, %d) = %d, want %d", tc.in, tc.fallback, got, tc.want)
		}
	}
}

// TestJSRoundHalfTowardPositiveInfinity locks the half-rounding direction to JS
// Math.round (half toward +Infinity), distinct from Go math.Round (half away
// from zero): Math.round(-0.5) == 0 (not -1), Math.round(-1.5) == -1 (not -2).
func TestJSRoundHalfTowardPositiveInfinity(t *testing.T) {
	cases := []struct {
		in   float64
		want int
	}{
		{0.5, 1}, {1.5, 2}, {2.5, 3},
		{-0.5, 0}, {-1.5, -1}, {-2.5, -2},
		{2.49, 2}, {2.51, 3},
	}
	for _, tc := range cases {
		if got := jsRound(tc.in); got != tc.want {
			t.Fatalf("jsRound(%v) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

// TestReadToolFloatOffsetRoundsToTSLine is the end-to-end proof: a float offset
// of 2.7 must read starting from line 3 (Math.round(2.7)==3), matching TS, not
// line 2 (the old Go truncation). A float limit of 1.6 reads Math.round(1.6)==2
// lines.
func TestReadToolFloatOffsetRoundsToTSLine(t *testing.T) {
	cwd := t.TempDir()
	path := filepath.Join(cwd, "lines.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\nfour\nfive\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	// offset 2.7 -> Math.round == 3 -> start at line 3 ("three"); limit 1.6 ->
	// Math.round == 2 -> two lines ("three","four").
	out := ExecuteTool(context.Background(), cwd, "Read", `{"file_path":"lines.txt","offset":2.7,"limit":1.6}`)
	if !strings.Contains(out, "three") || !strings.Contains(out, "four") {
		t.Fatalf("offset 2.7/limit 1.6 should read lines 3-4, got: %q", out)
	}
	// Must NOT include line 2 ("two", from the old truncated offset==2) nor line 5.
	if lineNumberedNear(out, "2", "two") {
		t.Fatalf("offset 2.7 must round to line 3, not truncate to line 2: %q", out)
	}
	if strings.Contains(out, "five") {
		t.Fatalf("limit 1.6 should round to 2 lines, line 5 must not appear: %q", out)
	}
	// Line 3 must carry the cat -n line number 3.
	if !lineNumberedNear(out, "3", "three") {
		t.Fatalf("expected line numbered 3 with 'three', got: %q", out)
	}
}
