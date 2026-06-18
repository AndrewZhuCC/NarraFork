package git

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestCommitCountAndRange covers the rev-list count read methods used by chapter
// graph metadata and merge-summary (CommitCount, CommitCountRef, CountCommitsBetween).
// These previously had no focused service-level coverage.
func TestCommitCountAndRange(t *testing.T) {
	svc := &Service{Timeout: 10 * time.Second}
	ctx := context.Background()

	repo := initDiscardTestRepo(t) // 1 commit on main
	base := strings.TrimSpace(runGitTest(t, repo, "rev-parse", "HEAD"))

	count, err := svc.CommitCount(ctx, repo)
	if err != nil || count != 1 {
		t.Fatalf("expected 1 commit on HEAD, got %d (err %v)", count, err)
	}

	// feature adds two commits.
	runGitTest(t, repo, "checkout", "-b", "feature")
	for _, m := range []string{"f1", "f2"} {
		writeGitTestFile(t, repo, "tracked.txt", m+"\n")
		runGitTest(t, repo, "add", ".")
		runGitTest(t, repo, "commit", "-m", m)
	}

	refCount, err := svc.CommitCountRef(ctx, repo, "feature")
	if err != nil || refCount != 3 {
		t.Fatalf("expected 3 commits on feature, got %d (err %v)", refCount, err)
	}

	// Empty ref falls back to HEAD (== feature, currently checked out).
	headCount, err := svc.CommitCountRef(ctx, repo, "")
	if err != nil || headCount != 3 {
		t.Fatalf("expected empty ref to count HEAD=3, got %d (err %v)", headCount, err)
	}

	// Only the two commits beyond base are between base..feature.
	between, err := svc.CountCommitsBetween(ctx, repo, base, "feature")
	if err != nil || between != 2 {
		t.Fatalf("expected 2 commits base..feature, got %d (err %v)", between, err)
	}
}

// TestCommitSubjectsRangeAndDiffStat covers the merge-summary inputs:
// CommitSubjectsRange lists the branch's exclusive subjects newest-first, and
// DiffStatRange returns a non-empty --stat block for the diverged files.
func TestCommitSubjectsRangeAndDiffStat(t *testing.T) {
	svc := &Service{Timeout: 10 * time.Second}
	ctx := context.Background()

	repo := initDiscardTestRepo(t)
	runGitTest(t, repo, "checkout", "-b", "feature")
	writeGitTestFile(t, repo, "a.txt", "alpha\n")
	runGitTest(t, repo, "add", ".")
	runGitTest(t, repo, "commit", "-m", "add a")
	writeGitTestFile(t, repo, "b.txt", "beta\n")
	runGitTest(t, repo, "add", ".")
	runGitTest(t, repo, "commit", "-m", "add b")

	subjects, err := svc.CommitSubjectsRange(ctx, repo, "main", "feature", 50)
	if err != nil {
		t.Fatalf("commit subjects range: %v", err)
	}
	if len(subjects) != 2 || subjects[0] != "add b" || subjects[1] != "add a" {
		t.Fatalf("expected [add b, add a] newest-first, got %#v", subjects)
	}

	stat, err := svc.DiffStatRange(ctx, repo, "main", "feature", 30)
	if err != nil {
		t.Fatalf("diff stat range: %v", err)
	}
	if !strings.Contains(stat, "a.txt") || !strings.Contains(stat, "b.txt") {
		t.Fatalf("expected diff stat to mention a.txt and b.txt, got %q", stat)
	}

	// Empty required args are rejected.
	if _, err := svc.CommitSubjectsRange(ctx, repo, "", "feature", 50); err == nil {
		t.Fatalf("expected error for empty baseBranch")
	}
	if _, err := svc.DiffStatRange(ctx, repo, "main", "", 30); err == nil {
		t.Fatalf("expected error for empty branch")
	}
}

// TestAncestryAndMergeCommitChecks covers IsAncestor, IsMergeCommit, and
// CurrentBranch used by dependency-sync and chapter graph logic.
func TestAncestryAndMergeCommitChecks(t *testing.T) {
	svc := &Service{Timeout: 10 * time.Second}
	ctx := context.Background()

	repo := initDiscardTestRepo(t)
	base := strings.TrimSpace(runGitTest(t, repo, "rev-parse", "HEAD"))

	if cur, err := svc.CurrentBranch(ctx, repo); err != nil || cur != "main" {
		t.Fatalf("expected current branch main, got %q (err %v)", cur, err)
	}

	// feature branches and merges back with a real merge commit.
	runGitTest(t, repo, "checkout", "-b", "feature")
	writeGitTestFile(t, repo, "feature.txt", "feature\n")
	runGitTest(t, repo, "add", ".")
	runGitTest(t, repo, "commit", "-m", "feature work")
	featureTip := strings.TrimSpace(runGitTest(t, repo, "rev-parse", "HEAD"))

	runGitTest(t, repo, "checkout", "main")
	mergeResult, err := svc.MergeWithStrategy(ctx, repo, "feature", "merge", "merge feature", false)
	if err != nil || !mergeResult.Success {
		t.Fatalf("setup merge failed: %v %#v", err, mergeResult)
	}
	mergeSHA := strings.TrimSpace(runGitTest(t, repo, "rev-parse", "HEAD"))

	// base is an ancestor of the merge; the merge is not an ancestor of base.
	if !svc.IsAncestor(ctx, repo, base, mergeSHA) {
		t.Fatalf("expected base to be ancestor of merge commit")
	}
	if svc.IsAncestor(ctx, repo, mergeSHA, base) {
		t.Fatalf("merge commit should not be ancestor of base")
	}

	// The merge commit is a merge; the feature tip is not.
	isMerge, err := svc.IsMergeCommit(ctx, repo, mergeSHA)
	if err != nil || !isMerge {
		t.Fatalf("expected merge commit to be detected, got %v (err %v)", isMerge, err)
	}
	notMerge, err := svc.IsMergeCommit(ctx, repo, featureTip)
	if err != nil || notMerge {
		t.Fatalf("feature tip should not be a merge commit, got %v (err %v)", notMerge, err)
	}
}
