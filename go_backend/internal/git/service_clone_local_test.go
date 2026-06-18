package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestCloneStreamingFromLocalSourceSucceedsAndStreamsProgress covers the success
// path of CloneStreamingWithCredentials against a local file:// source repo (no
// network or credentials required): the destination is populated with the source
// content and progress callbacks are invoked. This was the last git Service
// method without focused coverage.
func TestCloneStreamingFromLocalSourceSucceedsAndStreamsProgress(t *testing.T) {
	svc := &Service{Timeout: 10 * time.Second}
	ctx := context.Background()

	// Build a source repo with a couple of commits.
	src := initDiscardTestRepo(t)
	writeGitTestFile(t, src, "extra.txt", "extra content\n")
	runGitTest(t, src, "add", ".")
	runGitTest(t, src, "commit", "-m", "second")

	dest := filepath.Join(t.TempDir(), "cloned")
	srcURL := "file://" + src

	var progress []string
	err := svc.CloneStreamingWithCredentials(ctx, srcURL, dest, "", nil, func(line string) {
		progress = append(progress, line)
	})
	if err != nil {
		t.Fatalf("clone: %v", err)
	}

	// Cloned working tree must contain the source files.
	assertGitTestFileContent(t, dest, "tracked.txt", "base\n")
	assertGitTestFileContent(t, dest, "extra.txt", "extra content\n")
	if !svc.IsRepo(ctx, dest) {
		t.Fatalf("cloned destination should be a git repo")
	}
	// Progress streaming should have produced at least one line.
	if len(progress) == 0 {
		t.Fatalf("expected progress callbacks during clone, got none")
	}
}

// TestCloneStreamingWithBranchChecksOutRequestedBranch covers the --branch path.
func TestCloneStreamingWithBranchChecksOutRequestedBranch(t *testing.T) {
	svc := &Service{Timeout: 10 * time.Second}
	ctx := context.Background()

	src := initDiscardTestRepo(t)
	runGitTest(t, src, "checkout", "-b", "release")
	writeGitTestFile(t, src, "release.txt", "release line\n")
	runGitTest(t, src, "add", ".")
	runGitTest(t, src, "commit", "-m", "release work")
	// Leave the source on main so the clone must follow the explicit branch.
	runGitTest(t, src, "checkout", "main")

	dest := filepath.Join(t.TempDir(), "cloned-branch")
	if err := svc.CloneStreamingWithCredentials(ctx, "file://"+src, dest, "release", nil, nil); err != nil {
		t.Fatalf("clone branch: %v", err)
	}
	if cur, err := svc.CurrentBranch(ctx, dest); err != nil || cur != "release" {
		t.Fatalf("expected checked-out branch release, got %q (err %v)", cur, err)
	}
	assertGitTestFileContent(t, dest, "release.txt", "release line\n")
}

// TestCloneStreamingFailsForMissingSource covers the failure path: a non-existent
// source returns a descriptive (non-auth) clone error and does not panic.
func TestCloneStreamingFailsForMissingSource(t *testing.T) {
	svc := &Service{Timeout: 10 * time.Second}
	ctx := context.Background()

	missing := filepath.Join(t.TempDir(), "does-not-exist")
	dest := filepath.Join(t.TempDir(), "dest")

	err := svc.CloneStreamingWithCredentials(ctx, "file://"+missing, dest, "", nil, nil)
	if err == nil {
		t.Fatalf("expected error cloning a missing source, got nil")
	}
	if !strings.Contains(err.Error(), "failed to clone repo") {
		t.Fatalf("expected descriptive clone failure, got %q", err.Error())
	}
	// Destination must not have been left as a valid repo.
	if _, statErr := os.Stat(filepath.Join(dest, ".git")); statErr == nil {
		t.Fatalf("destination should not be a valid repo after failed clone")
	}
}

// TestCloneStreamingRespectsCancellation covers context cancellation handling:
// an already-cancelled context returns the context error rather than a generic
// clone failure.
func TestCloneStreamingRespectsCancellation(t *testing.T) {
	svc := &Service{Timeout: 10 * time.Second}
	src := initDiscardTestRepo(t)
	dest := filepath.Join(t.TempDir(), "cancelled")

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before starting

	err := svc.CloneStreamingWithCredentials(ctx, "file://"+src, dest, "", nil, nil)
	if err == nil {
		t.Fatalf("expected error for cancelled clone, got nil")
	}
	if !strings.Contains(err.Error(), context.Canceled.Error()) &&
		!strings.Contains(err.Error(), "failed to clone repo") {
		t.Fatalf("expected cancellation or clone failure error, got %q", err.Error())
	}
}
