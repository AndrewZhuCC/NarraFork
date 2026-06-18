package git

import (
	"context"
	"testing"
	"time"
)

// TestStageCommitResetLifecycle exercises the frontend-consumed git working-tree
// operations (POST /chapters/:id/git/stage|unstage|commit|reset) end to end against
// a real repository. These Service methods are called directly by the git route
// handlers but previously had no focused coverage.
func TestStageCommitResetLifecycle(t *testing.T) {
	svc := &Service{Timeout: 10 * time.Second}
	ctx := context.Background()

	repo := initDiscardTestRepo(t) // commits tracked.txt = "base\n"

	// Two modifications: one tracked edit + one new untracked file.
	writeGitTestFile(t, repo, "tracked.txt", "edited\n")
	writeGitTestFile(t, repo, "added.txt", "brand new\n")

	// StageFiles stages only the named file.
	if err := svc.StageFiles(ctx, repo, []string{"tracked.txt"}); err != nil {
		t.Fatalf("stage files: %v", err)
	}
	st, err := svc.StatusSummary(ctx, repo)
	if err != nil {
		t.Fatalf("status after stage files: %v", err)
	}
	if st.Staged != 1 {
		t.Fatalf("expected 1 staged file, got %d (%#v)", st.Staged, st)
	}
	if st.Untracked != 1 {
		t.Fatalf("expected 1 untracked file (added.txt), got %d", st.Untracked)
	}

	// StageAll stages everything including the untracked file.
	if err := svc.StageAll(ctx, repo); err != nil {
		t.Fatalf("stage all: %v", err)
	}
	st, err = svc.StatusSummary(ctx, repo)
	if err != nil {
		t.Fatalf("status after stage all: %v", err)
	}
	if st.Staged != 2 {
		t.Fatalf("expected 2 staged files after stage all, got %d (%#v)", st.Staged, st)
	}
	if st.Untracked != 0 {
		t.Fatalf("expected 0 untracked after stage all, got %d", st.Untracked)
	}

	// UnstageFiles moves the named file back out of the index.
	if err := svc.UnstageFiles(ctx, repo, []string{"tracked.txt"}); err != nil {
		t.Fatalf("unstage files: %v", err)
	}
	st, err = svc.StatusSummary(ctx, repo)
	if err != nil {
		t.Fatalf("status after unstage files: %v", err)
	}
	if st.Staged != 1 {
		t.Fatalf("expected 1 staged file after unstaging tracked.txt, got %d (%#v)", st.Staged, st)
	}

	// UnstageAll empties the index entirely.
	if err := svc.UnstageAll(ctx, repo); err != nil {
		t.Fatalf("unstage all: %v", err)
	}
	st, err = svc.StatusSummary(ctx, repo)
	if err != nil {
		t.Fatalf("status after unstage all: %v", err)
	}
	if st.Staged != 0 {
		t.Fatalf("expected 0 staged files after unstage all, got %d (%#v)", st.Staged, st)
	}

	// Re-stage everything and commit; the new HEAD sha is returned.
	if err := svc.StageAll(ctx, repo); err != nil {
		t.Fatalf("re-stage all: %v", err)
	}
	headBefore, err := svc.HeadCommit(ctx, repo)
	if err != nil {
		t.Fatalf("head before commit: %v", err)
	}
	sha, err := svc.Commit(ctx, repo, "apply staged changes")
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if sha == "" || sha == headBefore {
		t.Fatalf("commit should return a new HEAD sha, got %q (before %q)", sha, headBefore)
	}
	st, err = svc.StatusSummary(ctx, repo)
	if err != nil {
		t.Fatalf("status after commit: %v", err)
	}
	if st.HasChanges {
		t.Fatalf("expected clean tree after commit, got %#v", st)
	}

	// Reset --hard back to the prior commit drops the committed change from the tree.
	if err := svc.Reset(ctx, repo, headBefore, "hard"); err != nil {
		t.Fatalf("reset hard: %v", err)
	}
	assertGitTestFileContent(t, repo, "tracked.txt", "base\n")
	assertGitTestFileMissing(t, repo, "added.txt")
	headAfter, err := svc.HeadCommit(ctx, repo)
	if err != nil {
		t.Fatalf("head after reset: %v", err)
	}
	if headAfter != headBefore {
		t.Fatalf("expected HEAD reset to %q, got %q", headBefore, headAfter)
	}
}

// TestCommitRequiresMessage verifies the empty-message guard matches the handler
// contract (frontend always sends a message, but the service rejects blanks).
func TestCommitRequiresMessage(t *testing.T) {
	svc := &Service{Timeout: 5 * time.Second}
	repo := initDiscardTestRepo(t)
	if _, err := svc.Commit(context.Background(), repo, "   "); err == nil {
		t.Fatalf("expected error for blank commit message, got nil")
	}
}

// TestResetSoftKeepsWorkingTreeChanges confirms a soft reset moves HEAD but leaves
// the working-tree/index content intact (the default non-hard mode).
func TestResetSoftKeepsWorkingTreeChanges(t *testing.T) {
	svc := &Service{Timeout: 10 * time.Second}
	ctx := context.Background()

	repo := initDiscardTestRepo(t)
	base, err := svc.HeadCommit(ctx, repo)
	if err != nil {
		t.Fatalf("base head: %v", err)
	}

	writeGitTestFile(t, repo, "tracked.txt", "soft change\n")
	runGitTest(t, repo, "add", ".")
	runGitTest(t, repo, "commit", "-m", "soft target")

	// Soft reset back to base: HEAD moves but file content stays changed.
	if err := svc.Reset(ctx, repo, base, "soft"); err != nil {
		t.Fatalf("reset soft: %v", err)
	}
	head, err := svc.HeadCommit(ctx, repo)
	if err != nil {
		t.Fatalf("head after soft reset: %v", err)
	}
	if head != base {
		t.Fatalf("expected HEAD %q after soft reset, got %q", base, head)
	}
	assertGitTestFileContent(t, repo, "tracked.txt", "soft change\n")
}
