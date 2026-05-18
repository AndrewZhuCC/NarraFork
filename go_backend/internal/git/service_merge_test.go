package git

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestCountConflictMarkersLimitedBoundsRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conflict.txt")
	content := append([]byte("<<<<<<< HEAD\n"), bytes.Repeat([]byte("x"), maxConflictScanBytes+1024)...)
	content = append(content, []byte("\n<<<<<<< beyond-limit\n")...)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write conflict file: %v", err)
	}
	if got := countConflictMarkersLimited(path); got != 1 {
		t.Fatalf("conflict marker count = %d, want bounded first marker only", got)
	}
}

func TestMergeTreeReportsParityFields(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := t.TempDir()
	runGitTest(t, repo, "init")
	runGitTest(t, repo, "checkout", "-b", "main")
	runGitTest(t, repo, "config", "user.name", "Test User")
	runGitTest(t, repo, "config", "user.email", "test@example.com")
	writeGitTestFile(t, repo, "shared.txt", "base\n")
	runGitTest(t, repo, "add", ".")
	runGitTest(t, repo, "commit", "-m", "base")
	runGitTest(t, repo, "branch", "source")
	writeGitTestFile(t, repo, "shared.txt", "target\n")
	runGitTest(t, repo, "commit", "-am", "target")
	runGitTest(t, repo, "checkout", "source")
	writeGitTestFile(t, repo, "shared.txt", "source\n")
	runGitTest(t, repo, "commit", "-am", "source")
	runGitTest(t, repo, "checkout", "main")

	svc := &Service{Timeout: 5 * time.Second}
	base, err := svc.GetMergeBase(context.Background(), repo, "main", "source")
	if err != nil {
		t.Fatalf("merge base: %v", err)
	}
	hasConflicts, files, output, err := svc.MergeTree(context.Background(), repo, base, "main", "source")
	if err != nil {
		t.Fatalf("merge tree: %v", err)
	}
	if !hasConflicts {
		t.Fatalf("expected conflict, output:\n%s", output)
	}
	if len(files) != 1 || files[0] != "shared.txt" {
		t.Fatalf("conflict files = %#v, want shared.txt", files)
	}
}

func runGitTest(t *testing.T, cwd string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
	return string(out)
}

func writeGitTestFile(t *testing.T, repo, name, content string) {
	t.Helper()
	path := filepath.Join(repo, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}
