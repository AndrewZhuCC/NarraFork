package git

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestMergeNoCommitStagesWithoutCommitting covers the no-commit merge path used by
// the chapter AI-resolve conflict workflow (Service.MergeNoCommit): a successful
// merge leaves changes staged in the index without creating a commit.
func TestMergeNoCommitStagesWithoutCommitting(t *testing.T) {
	svc := &Service{Timeout: 10 * time.Second}
	ctx := context.Background()

	repo := initDiscardTestRepo(t)
	headBefore := strings.TrimSpace(runGitTest(t, repo, "rev-parse", "HEAD"))

	runGitTest(t, repo, "checkout", "-b", "feature")
	writeGitTestFile(t, repo, "feature.txt", "feature\n")
	runGitTest(t, repo, "add", ".")
	runGitTest(t, repo, "commit", "-m", "feature work")

	runGitTest(t, repo, "checkout", "main")
	result, err := svc.MergeNoCommit(ctx, repo, "feature", "merge")
	if err != nil {
		t.Fatalf("merge --no-commit: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success, got %#v", result)
	}
	// File is applied to the working tree...
	assertGitTestFileContent(t, repo, "feature.txt", "feature\n")
	// ...but HEAD has not advanced (no commit was created).
	headAfter := strings.TrimSpace(runGitTest(t, repo, "rev-parse", "HEAD"))
	if headAfter != headBefore {
		t.Fatalf("no-commit merge must not advance HEAD: before %q after %q", headBefore, headAfter)
	}
}

// TestMergeNoCommitConflictReportsConflictFiles covers the conflict branch of the
// AI-resolve workflow: conflicts are surfaced via MergeResult rather than as a
// hard error so the resolver can inspect and fix them.
func TestMergeNoCommitConflictReportsConflictFiles(t *testing.T) {
	svc := &Service{Timeout: 10 * time.Second}
	ctx := context.Background()

	repo := initDiscardTestRepo(t)
	runGitTest(t, repo, "checkout", "-b", "feature")
	writeGitTestFile(t, repo, "tracked.txt", "feature edit\n")
	runGitTest(t, repo, "add", ".")
	runGitTest(t, repo, "commit", "-m", "feature edit")

	runGitTest(t, repo, "checkout", "main")
	writeGitTestFile(t, repo, "tracked.txt", "main edit\n")
	runGitTest(t, repo, "add", ".")
	runGitTest(t, repo, "commit", "-m", "main edit")

	result, err := svc.MergeNoCommit(ctx, repo, "feature", "merge")
	if err != nil {
		t.Fatalf("conflicting no-commit merge should not hard-error, got %v", err)
	}
	if result.Success {
		t.Fatalf("expected conflict (success=false), got %#v", result)
	}
	found := false
	for _, f := range result.ConflictFiles {
		if f == "tracked.txt" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected tracked.txt conflict, got %#v", result.ConflictFiles)
	}
	svc.AbortMerge(ctx, repo)
}

// TestCherryPickBranchRangeReplaysCommits covers the branch-range cherry-pick
// merge strategy (Service.CherryPick) used by chapter merge: every commit in
// baseSHA..sourceBranch is replayed onto the target worktree. This is distinct
// from CherryPickCommits (explicit SHA list) which already has coverage.
func TestCherryPickBranchRangeReplaysCommits(t *testing.T) {
	svc := &Service{Timeout: 10 * time.Second}
	ctx := context.Background()

	repo := initDiscardTestRepo(t)
	baseSHA := strings.TrimSpace(runGitTest(t, repo, "rev-parse", "HEAD"))

	// feature adds two commits on top of base.
	runGitTest(t, repo, "checkout", "-b", "feature")
	writeGitTestFile(t, repo, "one.txt", "first\n")
	runGitTest(t, repo, "add", ".")
	runGitTest(t, repo, "commit", "-m", "commit one")
	writeGitTestFile(t, repo, "two.txt", "second\n")
	runGitTest(t, repo, "add", ".")
	runGitTest(t, repo, "commit", "-m", "commit two")

	// Target stays on main (== base). Cherry-pick the whole feature range.
	runGitTest(t, repo, "checkout", "main")
	result, err := svc.CherryPick(ctx, repo, repo, "feature", baseSHA)
	if err != nil {
		t.Fatalf("cherry-pick range: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success, got %#v", result)
	}
	if result.CommitSHA == "" {
		t.Fatalf("expected new HEAD sha, got empty")
	}
	// Both range commits applied in order.
	assertGitTestFileContent(t, repo, "one.txt", "first\n")
	assertGitTestFileContent(t, repo, "two.txt", "second\n")
}

// TestCherryPickBranchRangeEmptyWhenNoCommits covers the no-op case where the
// source branch has no commits beyond baseSHA.
func TestCherryPickBranchRangeEmptyWhenNoCommits(t *testing.T) {
	svc := &Service{Timeout: 10 * time.Second}
	ctx := context.Background()

	repo := initDiscardTestRepo(t)
	baseSHA := strings.TrimSpace(runGitTest(t, repo, "rev-parse", "HEAD"))

	// feature exists but has no commits beyond base.
	runGitTest(t, repo, "branch", "feature")
	runGitTest(t, repo, "checkout", "main")

	result, err := svc.CherryPick(ctx, repo, repo, "feature", baseSHA)
	if err != nil {
		t.Fatalf("empty-range cherry-pick: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success no-op, got %#v", result)
	}
}
