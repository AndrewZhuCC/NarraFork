package git

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
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

func TestGitReadPrependsNoOptionalLocks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake git helper is unix-only")
	}
	dir := t.TempDir()
	logPath := filepath.Join(dir, "args.txt")
	fakeGit := filepath.Join(dir, "git")
	if err := os.WriteFile(fakeGit, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$GIT_ARGS_LOG\"\n"), 0o755); err != nil {
		t.Fatalf("write fake git: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GIT_ARGS_LOG", logPath)

	svc := &Service{Timeout: 5 * time.Second}
	if _, err := svc.gitRead(context.Background(), "", "status", "--porcelain=v1"); err != nil {
		t.Fatalf("gitRead: %v", err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read fake git args: %v", err)
	}
	if got, want := string(data), "--no-optional-locks\nstatus\n--porcelain=v1\n"; got != want {
		t.Fatalf("fake git args = %q, want %q", got, want)
	}
}

func TestStatusSummaryIncludesLineStatsForStagedUnstagedAndUntracked(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := t.TempDir()
	runGitTest(t, repo, "init")
	runGitTest(t, repo, "checkout", "-b", "main")
	runGitTest(t, repo, "config", "user.name", "Test User")
	runGitTest(t, repo, "config", "user.email", "test@example.com")
	writeGitTestFile(t, repo, "tracked.txt", "base\nremove\n")
	runGitTest(t, repo, "add", ".")
	runGitTest(t, repo, "commit", "-m", "base")

	writeGitTestFile(t, repo, "tracked.txt", "base\nstaged\n")
	runGitTest(t, repo, "add", "tracked.txt")
	writeGitTestFile(t, repo, "tracked.txt", "base\nstaged\nunstaged\n")
	writeGitTestFile(t, repo, "new.txt", "one\ntwo")

	svc := &Service{Timeout: 5 * time.Second}
	summary, err := svc.StatusSummary(context.Background(), repo)
	if err != nil {
		t.Fatalf("status summary: %v", err)
	}
	if summary.LinesAdded != 4 || summary.LinesRemoved != 1 {
		t.Fatalf("summary lines = +%d -%d, want +4 -1", summary.LinesAdded, summary.LinesRemoved)
	}
	if summary.Staged != 1 || summary.Unstaged != 1 || summary.Untracked != 1 {
		t.Fatalf("summary counts staged=%d unstaged=%d untracked=%d, want 1/1/1", summary.Staged, summary.Unstaged, summary.Untracked)
	}
	tracked := statusFileByPath(summary.Files, "tracked.txt")
	if tracked == nil {
		t.Fatalf("tracked file missing from status summary: %#v", summary.Files)
	}
	if tracked.LinesAdded != 2 || tracked.LinesRemoved != 1 || tracked.StagedLinesAdded != 1 || tracked.StagedLinesRemoved != 1 || tracked.UnstagedLinesAdded != 1 || tracked.UnstagedLinesRemoved != 0 {
		t.Fatalf("tracked stats = %#v, want staged +1/-1 and unstaged +1/-0", *tracked)
	}
	untracked := statusFileByPath(summary.Files, "new.txt")
	if untracked == nil {
		t.Fatalf("untracked file missing from status summary: %#v", summary.Files)
	}
	if untracked.LinesAdded != 2 || untracked.LinesRemoved != 0 || untracked.UnstagedLinesAdded != 2 || untracked.UnstagedLinesRemoved != 0 {
		t.Fatalf("untracked stats = %#v, want +2/-0", *untracked)
	}
}

func TestFileDiffShowsUntrackedFileAsNewFile(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := initDiscardTestRepo(t)
	writeGitTestFile(t, repo, "new-file.txt", "hello\nworld\n")

	svc := &Service{Timeout: 5 * time.Second}
	result, err := svc.FileDiff(context.Background(), repo, "new-file.txt", false)
	if err != nil {
		t.Fatalf("file diff: %v", err)
	}
	diff, _ := result["diff"].(string)
	if result["truncated"] != false || !strings.Contains(diff, "+hello") || !strings.Contains(diff, "+world") {
		t.Fatalf("unexpected untracked diff result: %#v", result)
	}
}

func TestLogIncludesTSCompatibleFields(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := initDiscardTestRepo(t)

	svc := &Service{Timeout: 5 * time.Second}
	commits, err := svc.Log(context.Background(), repo, 1)
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	if len(commits) != 1 {
		t.Fatalf("expected one commit, got %#v", commits)
	}
	commit := commits[0]
	if commit.SHA == "" || commit.ShortSHA == "" || commit.Message != "base" || commit.Author != "Test User" || commit.Date == "" {
		t.Fatalf("log entry missing TS-compatible fields: %#v", commit)
	}
}

func TestGitWriteMethodsUsePerWorktreeLock(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := initDiscardTestRepo(t)
	writeGitTestFile(t, repo, "queued.txt", "queued\n")

	unlock := acquireWorktreeLock(repo)
	done := make(chan error, 1)
	svc := &Service{Timeout: 5 * time.Second}
	go func() {
		done <- svc.StageAll(context.Background(), repo)
	}()

	select {
	case err := <-done:
		unlock()
		if err != nil {
			t.Fatalf("stage all returned before lock release with error: %v", err)
		}
		t.Fatalf("stage all returned before per-worktree lock was released")
	case <-time.After(50 * time.Millisecond):
	}

	unlock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("stage all after lock release: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("stage all did not finish after per-worktree lock release")
	}

	status := runGitTest(t, repo, "status", "--porcelain")
	if status != "A  queued.txt\n" {
		t.Fatalf("unexpected status after locked stage all: %q", status)
	}
}

func statusFileByPath(files []StatusFile, path string) *StatusFile {
	for i := range files {
		if files[i].Path == path {
			return &files[i]
		}
	}
	return nil
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
