package agentbasic

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestExpandGlobBraces pins the brace-expansion helper that brings Go's Glob/Grep
// glob matching in line with TS Bun.Glob / ripgrep --glob. Only brace groups with a
// top-level comma expand; groups without a comma ({1..9}, {foo}, {}) keep their
// literal braces so non-comma patterns behave exactly as before (zero regression).
func TestExpandGlobBraces(t *testing.T) {
	cases := []struct {
		pattern string
		want    []string
	}{
		// Comma groups expand.
		{"*.{ts,tsx}", []string{"*.ts", "*.tsx"}},
		{"**/*.{ts,tsx}", []string{"**/*.ts", "**/*.tsx"}},
		{"{a,b}.{c,d}", []string{"a.c", "a.d", "b.c", "b.d"}},
		{"{a,{b,c}}", []string{"a", "b", "c"}},
		{"{a,}", []string{"a", ""}}, // empty alternative preserved
		// No top-level comma: braces stay literal (matches minimatch/Bun.Glob and
		// Go's prior literal-brace filepath.Match behavior).
		{"file{1..9}.txt", []string{"file{1..9}.txt"}},
		{"x{foo}y", []string{"x{foo}y"}},
		{"a{}b", []string{"a{}b"}},
		{"*.go", []string{"*.go"}},
		{"*.{ts", []string{"*.{ts"}}, // unbalanced -> unchanged
		// Mixed: range group stays literal, comma group expands.
		{"a{1..9}.{ts,tsx}", []string{"a{1..9}.ts", "a{1..9}.tsx"}},
	}
	for _, c := range cases {
		got := expandGlobBraces(c.pattern)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("expandGlobBraces(%q) = %#v, want %#v", c.pattern, got, c.want)
		}
	}
}

// TestExpandGlobBracesBoundedAgainstBlowUp verifies the expansion is bounded so a
// pathological pattern cannot explode memory; it falls back to the literal pattern.
func TestExpandGlobBracesBoundedAgainstBlowUp(t *testing.T) {
	// 7 two-way groups = 128 combinations > globBraceExpansionLimit (64).
	pattern := strings.Repeat("{a,b}", 7)
	got := expandGlobBraces(pattern)
	if len(got) != 1 || got[0] != pattern {
		t.Fatalf("expected bounded fallback to [pattern], got %d results", len(got))
	}
}

// TestGlobMatchBraceExpansion drives globMatch directly to prove comma brace groups
// match the right files (and basename candidates) while literal-brace patterns and
// plain patterns keep their prior behavior.
func TestGlobMatchBraceExpansion(t *testing.T) {
	cases := []struct {
		pattern string
		rel     string
		want    bool
	}{
		{"*.{ts,tsx}", "a.ts", true},
		{"*.{ts,tsx}", "a.tsx", true},
		{"*.{ts,tsx}", "a.js", false},
		{"**/*.{ts,tsx}", "src/a.ts", true},
		{"**/*.{ts,tsx}", "src/a.tsx", true},
		{"**/*.{ts,tsx}", "src/a.js", false},
		{"src/*.{ts,json}", "src/a.json", true},
		{"*.{ts,tsx}", "src/a.ts", true}, // basename candidate (no slash in pattern)
		// Zero-regression controls.
		{"a{1..9}b", "a{1..9}b", true}, // literal brace still matches literal text
		{"*.go", "a.go", true},
		{"*.go", "a.ts", false},
	}
	for _, c := range cases {
		if got := globMatch(c.pattern, c.rel); got != c.want {
			t.Errorf("globMatch(%q, %q) = %v, want %v", c.pattern, c.rel, got, c.want)
		}
	}
}

// TestGlobToolBraceExpansionMatchesFiles is the end-to-end Glob tool proof: a brace
// pattern now lists matching files instead of returning "No matches found".
func TestGlobToolBraceExpansionMatchesFiles(t *testing.T) {
	cwd := t.TempDir()
	for _, name := range []string{"a.ts", "b.tsx", "c.js"} {
		if err := os.WriteFile(filepath.Join(cwd, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	out := ExecuteTool(context.Background(), cwd, "Glob", `{"pattern":"**/*.{ts,tsx}"}`)
	if !strings.Contains(out, "a.ts") || !strings.Contains(out, "b.tsx") {
		t.Fatalf("expected a.ts and b.tsx in brace glob result, got: %s", out)
	}
	if strings.Contains(out, "c.js") {
		t.Fatalf("c.js must not match *.{ts,tsx}, got: %s", out)
	}
}

// TestGrepToolBraceGlobFilterMatchesFiles is the end-to-end Grep tool proof: a brace
// glob filter now keeps matching files instead of rejecting every file.
func TestGrepToolBraceGlobFilterMatchesFiles(t *testing.T) {
	cwd := t.TempDir()
	for _, name := range []string{"a.ts", "b.tsx", "c.js"} {
		if err := os.WriteFile(filepath.Join(cwd, name), []byte("needle here\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	out := ExecuteTool(context.Background(), cwd, "Grep", `{"pattern":"needle","glob":"*.{ts,tsx}","output_mode":"files_with_matches"}`)
	if !strings.Contains(out, "a.ts") || !strings.Contains(out, "b.tsx") {
		t.Fatalf("expected a.ts and b.tsx matched by brace glob filter, got: %s", out)
	}
	if strings.Contains(out, "c.js") {
		t.Fatalf("c.js must be filtered out by *.{ts,tsx}, got: %s", out)
	}
}
