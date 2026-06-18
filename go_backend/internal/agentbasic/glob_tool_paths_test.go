package agentbasic

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGlobToolNoMatchesAndNonDirectoryPath covers two Glob paths not asserted by
// the existing recursive/basename/path/escape test: a pattern that matches
// nothing returns "No matches found", and a path that points at a file (not a
// directory) is rejected with "path must be a directory".
func TestGlobToolNoMatchesAndNonDirectoryPath(t *testing.T) {
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "only.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	// No matches.
	out := ExecuteTool(context.Background(), cwd, "Glob", `{"pattern":"**/*.go"}`)
	if !strings.Contains(out, "No matches found") {
		t.Fatalf("expected no-matches result, got: %s", out)
	}

	// Path points at a file -> rejected.
	out = ExecuteTool(context.Background(), cwd, "Glob", `{"pattern":"*","path":"only.txt"}`)
	if !strings.Contains(out, "path must be a directory") {
		t.Fatalf("expected non-directory path rejection, got: %s", out)
	}
}

// TestGlobToolExcludesDotfilesLikeTS pins Glob to Bun.Glob scan({dot:false}) used
// by the TS glob tool: dotfiles and files inside dot-directories (.git, .hidden,
// .env) are excluded, while normal files are matched.
func TestGlobToolExcludesDotfilesLikeTS(t *testing.T) {
	cwd := t.TempDir()
	mustMkdir := func(p string) {
		if err := os.MkdirAll(filepath.Join(cwd, p), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", p, err)
		}
	}
	mustWrite := func(p string) {
		if err := os.WriteFile(filepath.Join(cwd, p), []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
	mustMkdir(".git")
	mustMkdir("src")
	mustMkdir(".hidden")
	mustWrite("a.txt")
	mustWrite(".env")
	mustWrite(filepath.Join(".git", "config"))
	mustWrite(filepath.Join("src", "b.txt"))
	mustWrite(filepath.Join(".hidden", "c.txt"))

	out := ExecuteTool(context.Background(), cwd, "Glob", `{"pattern":"**/*"}`)
	// Visible files matched.
	if !strings.Contains(out, "a.txt") || !strings.Contains(out, "src/b.txt") {
		t.Fatalf("expected visible files matched, got: %s", out)
	}
	// Dotfiles and dot-directory contents excluded.
	for _, hidden := range []string{".env", ".git/config", ".hidden/c.txt"} {
		if strings.Contains(out, hidden) {
			t.Fatalf("dotfile/dot-dir %q should be excluded (dot:false), got: %s", hidden, out)
		}
	}
}
