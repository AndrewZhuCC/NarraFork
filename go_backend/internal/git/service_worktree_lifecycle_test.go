package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestBranchAndWorktreeLifecycle exercises the branch/worktree git operations that
// back chapter create -> dormant -> wake -> cleanup (CreateBranch, BranchExists,
// CreateWorktree, RemoveWorktree, PruneWorktrees, DeleteBranch). These Service
// methods are called by the chapter service but previously had no focused coverage.
func TestBranchAndWorktreeLifecycle(t *testing.T) {
	svc := &Service{Timeout: 10 * time.Second}
	ctx := context.Background()

	repo := initDiscardTestRepo(t) // main @ tracked.txt = "base\n"

	// A fresh feature branch should not exist yet, then exist after creation.
	if svc.BranchExists(ctx, repo, "feature") {
		t.Fatalf("feature branch should not exist before creation")
	}
	if err := svc.CreateBranch(ctx, repo, "feature", "main"); err != nil {
		t.Fatalf("create branch: %v", err)
	}
	if !svc.BranchExists(ctx, repo, "feature") {
		t.Fatalf("feature branch should exist after creation")
	}

	// Create a worktree checked out to the feature branch (chapter activation).
	worktree := filepath.Join(t.TempDir(), "wt-feature")
	if err := svc.CreateWorktree(ctx, repo, worktree, "feature"); err != nil {
		t.Fatalf("create worktree: %v", err)
	}
	if _, err := os.Stat(filepath.Join(worktree, "tracked.txt")); err != nil {
		t.Fatalf("worktree should contain tracked.txt: %v", err)
	}
	listed := runGitTest(t, repo, "worktree", "list")
	if !strings.Contains(listed, worktree) {
		t.Fatalf("worktree list should include %q, got:\n%s", worktree, listed)
	}

	// Remove the worktree (chapter dormant) but keep the branch.
	if err := svc.RemoveWorktree(ctx, repo, worktree); err != nil {
		t.Fatalf("remove worktree: %v", err)
	}
	if _, err := os.Stat(worktree); !os.IsNotExist(err) {
		t.Fatalf("worktree dir should be gone after remove, stat err=%v", err)
	}
	if !svc.BranchExists(ctx, repo, "feature") {
		t.Fatalf("branch must survive worktree removal (dormant keeps branch)")
	}

	// Prune dangling worktree metadata (cleanup) — must not error on a clean repo.
	if err := svc.PruneWorktrees(ctx, repo); err != nil {
		t.Fatalf("prune worktrees: %v", err)
	}

	// Delete the branch (chapter abandon/cleanup).
	if err := svc.DeleteBranch(ctx, repo, "feature"); err != nil {
		t.Fatalf("delete branch: %v", err)
	}
	if svc.BranchExists(ctx, repo, "feature") {
		t.Fatalf("feature branch should be gone after delete")
	}
}

// TestCreateBranchRejectsInvalidNames confirms the ValidateBranch guard blocks
// names that could be unsafe to interpolate into git commands.
func TestCreateBranchRejectsInvalidNames(t *testing.T) {
	svc := &Service{Timeout: 5 * time.Second}
	ctx := context.Background()
	repo := initDiscardTestRepo(t)

	for _, bad := range []string{"", "bad name", "has..dots"} {
		if err := svc.CreateBranch(ctx, repo, bad, "main"); err == nil {
			t.Fatalf("expected error for invalid branch %q, got nil", bad)
		}
	}
	// A valid name still succeeds.
	if err := svc.CreateBranch(ctx, repo, "valid-branch", "main"); err != nil {
		t.Fatalf("valid branch name should succeed: %v", err)
	}
}

// TestWakeAfterDormantRecreatesWorktreeWithBranchState models the wake flow:
// after a dormant remove, re-adding a worktree restores the branch's committed
// state on disk.
func TestWakeAfterDormantRecreatesWorktreeWithBranchState(t *testing.T) {
	svc := &Service{Timeout: 10 * time.Second}
	ctx := context.Background()

	repo := initDiscardTestRepo(t)
	if err := svc.CreateBranch(ctx, repo, "feature", "main"); err != nil {
		t.Fatalf("create branch: %v", err)
	}

	wt1 := filepath.Join(t.TempDir(), "wt1")
	if err := svc.CreateWorktree(ctx, repo, wt1, "feature"); err != nil {
		t.Fatalf("create worktree: %v", err)
	}
	// Commit work on the branch via its worktree.
	writeGitTestFile(t, wt1, "feature.txt", "wake state\n")
	runGitTest(t, wt1, "add", ".")
	runGitTest(t, wt1, "commit", "-m", "feature work")

	// Dormant: remove the worktree.
	if err := svc.RemoveWorktree(ctx, repo, wt1); err != nil {
		t.Fatalf("remove worktree: %v", err)
	}

	// Wake: recreate the worktree; the committed file must be present again.
	wt2 := filepath.Join(t.TempDir(), "wt2")
	if err := svc.CreateWorktree(ctx, repo, wt2, "feature"); err != nil {
		t.Fatalf("recreate worktree: %v", err)
	}
	assertGitTestFileContent(t, wt2, "feature.txt", "wake state\n")
}
