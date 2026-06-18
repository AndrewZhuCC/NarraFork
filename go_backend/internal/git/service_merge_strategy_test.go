package git

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestMergeWithStrategyNoFFCreatesMergeCommit covers the default no-ff merge path
// used by chapter merge (Service.MergeWithStrategy). These merge/revert methods
// previously had no focused service-level coverage.
func TestMergeWithStrategyNoFFCreatesMergeCommit(t *testing.T) {
	svc := &Service{Timeout: 10 * time.Second}
	ctx := context.Background()

	repo := initDiscardTestRepo(t) // main @ tracked.txt = "base\n"

	// feature branch adds a new file.
	runGitTest(t, repo, "checkout", "-b", "feature")
	writeGitTestFile(t, repo, "feature.txt", "feature\n")
	runGitTest(t, repo, "add", ".")
	runGitTest(t, repo, "commit", "-m", "feature work")

	// Back on main, merge feature with an explicit no-ff merge commit.
	runGitTest(t, repo, "checkout", "main")
	result, err := svc.MergeWithStrategy(ctx, repo, "feature", "merge", "merge feature", false)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected merge success, got %#v", result)
	}
	if result.IsFastForward {
		t.Fatalf("expected non-fast-forward merge commit, got fast-forward")
	}
	if result.CommitSHA == "" {
		t.Fatalf("expected merge commit sha, got empty")
	}
	assertGitTestFileContent(t, repo, "feature.txt", "feature\n")
	// The merge commit must have two parents.
	parents := strings.Fields(runGitTest(t, repo, "rev-list", "--parents", "-n", "1", "HEAD"))
	if len(parents) != 3 {
		t.Fatalf("expected merge commit with 2 parents, got rev-list %v", parents)
	}
}

// TestMergeWithStrategySquashCollapsesHistory covers the squash strategy which
// stages the diff and creates a single new commit (no merge parent).
func TestMergeWithStrategySquashCollapsesHistory(t *testing.T) {
	svc := &Service{Timeout: 10 * time.Second}
	ctx := context.Background()

	repo := initDiscardTestRepo(t)
	runGitTest(t, repo, "checkout", "-b", "feature")
	writeGitTestFile(t, repo, "squash.txt", "squashed\n")
	runGitTest(t, repo, "add", ".")
	runGitTest(t, repo, "commit", "-m", "squash work")

	runGitTest(t, repo, "checkout", "main")
	result, err := svc.MergeWithStrategy(ctx, repo, "feature", "squash", "squash merge", false)
	if err != nil {
		t.Fatalf("squash merge: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected squash success, got %#v", result)
	}
	assertGitTestFileContent(t, repo, "squash.txt", "squashed\n")
	// Squash produces an ordinary single-parent commit.
	parents := strings.Fields(runGitTest(t, repo, "rev-list", "--parents", "-n", "1", "HEAD"))
	if len(parents) != 2 {
		t.Fatalf("expected single-parent squash commit, got rev-list %v", parents)
	}
}

// TestMergeWithStrategyConflictThenAbort covers the conflict branch plus AbortMerge.
func TestMergeWithStrategyConflictThenAbort(t *testing.T) {
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
	mainHead := strings.TrimSpace(runGitTest(t, repo, "rev-parse", "HEAD"))

	result, err := svc.MergeWithStrategy(ctx, repo, "feature", "merge", "conflicting merge", false)
	if err != nil {
		t.Fatalf("conflicting merge should not hard-error, got %v", err)
	}
	if result.Success {
		t.Fatalf("expected conflict (success=false), got %#v", result)
	}
	foundConflict := false
	for _, f := range result.ConflictFiles {
		if f == "tracked.txt" {
			foundConflict = true
		}
	}
	if !foundConflict {
		t.Fatalf("expected tracked.txt conflict, got %#v", result.ConflictFiles)
	}

	// AbortMerge clears the in-progress merge, restoring HEAD/content.
	svc.AbortMerge(ctx, repo)
	head := strings.TrimSpace(runGitTest(t, repo, "rev-parse", "HEAD"))
	if head != mainHead {
		t.Fatalf("expected HEAD %q after abort, got %q", mainHead, head)
	}
	assertGitTestFileContent(t, repo, "tracked.txt", "main edit\n")
}

// TestRevertCommitCreatesInverseCommit covers narrator/chapter revert of an
// ordinary commit (Service.RevertCommit).
func TestRevertCommitCreatesInverseCommit(t *testing.T) {
	svc := &Service{Timeout: 10 * time.Second}
	ctx := context.Background()

	repo := initDiscardTestRepo(t)
	writeGitTestFile(t, repo, "feature.txt", "added\n")
	runGitTest(t, repo, "add", ".")
	runGitTest(t, repo, "commit", "-m", "add feature.txt")
	target := strings.TrimSpace(runGitTest(t, repo, "rev-parse", "HEAD"))

	sha, err := svc.RevertCommit(ctx, repo, target)
	if err != nil {
		t.Fatalf("revert: %v", err)
	}
	if sha == "" || sha == target {
		t.Fatalf("expected new revert commit sha, got %q (target %q)", sha, target)
	}
	// The reverted file addition is undone.
	assertGitTestFileMissing(t, repo, "feature.txt")
}

// TestRevertMergeCommitUsesMainlineParent covers reverting a merge commit, which
// requires the -m 1 mainline flag (Service.RevertMergeCommit).
func TestRevertMergeCommitUsesMainlineParent(t *testing.T) {
	svc := &Service{Timeout: 10 * time.Second}
	ctx := context.Background()

	repo := initDiscardTestRepo(t)
	runGitTest(t, repo, "checkout", "-b", "feature")
	writeGitTestFile(t, repo, "feature.txt", "feature\n")
	runGitTest(t, repo, "add", ".")
	runGitTest(t, repo, "commit", "-m", "feature work")

	runGitTest(t, repo, "checkout", "main")
	mergeResult, err := svc.MergeWithStrategy(ctx, repo, "feature", "merge", "merge feature", false)
	if err != nil || !mergeResult.Success {
		t.Fatalf("setup merge failed: %v %#v", err, mergeResult)
	}
	mergeSHA := strings.TrimSpace(runGitTest(t, repo, "rev-parse", "HEAD"))

	sha, err := svc.RevertMergeCommit(ctx, repo, mergeSHA)
	if err != nil {
		t.Fatalf("revert merge: %v", err)
	}
	if sha == "" || sha == mergeSHA {
		t.Fatalf("expected new revert commit sha, got %q (merge %q)", sha, mergeSHA)
	}
	// Reverting the merge removes the feature file again.
	assertGitTestFileMissing(t, repo, "feature.txt")
}
