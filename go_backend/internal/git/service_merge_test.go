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

func TestDiscardFilesRemovesSelectedTrackedAndUntrackedOnly(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := initDiscardTestRepo(t)
	writeGitTestFile(t, repo, "tracked.txt", "changed\n")
	writeGitTestFile(t, repo, "untracked.txt", "temporary\n")
	writeGitTestFile(t, repo, "keep-untracked.txt", "keep\n")

	svc := &Service{Timeout: 5 * time.Second}
	if err := svc.DiscardFiles(context.Background(), repo, []string{"tracked.txt", "untracked.txt"}); err != nil {
		t.Fatalf("discard selected files: %v", err)
	}

	assertGitTestFileContent(t, repo, "tracked.txt", "base\n")
	assertGitTestFileMissing(t, repo, "untracked.txt")
	assertGitTestFileContent(t, repo, "keep-untracked.txt", "keep\n")
	status := runGitTest(t, repo, "status", "--porcelain")
	if status != "?? keep-untracked.txt\n" {
		t.Fatalf("unexpected status after selected discard: %q", status)
	}
}

func TestDiscardAllRemovesTrackedChangesAndUntrackedDirectories(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := initDiscardTestRepo(t)
	writeGitTestFile(t, repo, "tracked.txt", "changed\n")
	writeGitTestFile(t, repo, "scratch/nested.txt", "temporary\n")

	svc := &Service{Timeout: 5 * time.Second}
	if err := svc.DiscardAll(context.Background(), repo); err != nil {
		t.Fatalf("discard all: %v", err)
	}

	assertGitTestFileContent(t, repo, "tracked.txt", "base\n")
	assertGitTestFileMissing(t, repo, "scratch")
	status := runGitTest(t, repo, "status", "--porcelain")
	if status != "" {
		t.Fatalf("expected clean worktree after discard all, got %q", status)
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

func initDiscardTestRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	runGitTest(t, repo, "init")
	runGitTest(t, repo, "checkout", "-b", "main")
	runGitTest(t, repo, "config", "user.name", "Test User")
	runGitTest(t, repo, "config", "user.email", "test@example.com")
	writeGitTestFile(t, repo, "tracked.txt", "base\n")
	runGitTest(t, repo, "add", ".")
	runGitTest(t, repo, "commit", "-m", "base")
	return repo
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

func assertGitTestFileContent(t *testing.T, repo, name, want string) {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(repo, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	if string(content) != want {
		t.Fatalf("%s content = %q, want %q", name, string(content), want)
	}
}

func assertGitTestFileMissing(t *testing.T, repo, name string) {
	t.Helper()
	_, err := os.Stat(filepath.Join(repo, name))
	if err == nil {
		t.Fatalf("expected %s to be removed", name)
	}
	if !os.IsNotExist(err) {
		t.Fatalf("stat %s: %v", name, err)
	}
}
