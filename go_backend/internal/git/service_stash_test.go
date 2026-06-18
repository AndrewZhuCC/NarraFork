package git

import (
	"context"
	"testing"
	"time"
)

// TestStashLifecyclePushListApplyDrop exercises the frontend-consumed git stash
// operations (POST /chapters/:id/git/stash, GET .../stash/list, plus apply/pop/drop)
// end to end against a real repository. These Service methods previously had no
// focused coverage even though the git route handlers call them directly.
func TestStashLifecyclePushListApplyDrop(t *testing.T) {
	svc := &Service{Timeout: 10 * time.Second}
	ctx := context.Background()

	repo := initDiscardTestRepo(t) // commits tracked.txt = "base\n"

	// Create a tracked modification plus a brand-new untracked file.
	writeGitTestFile(t, repo, "tracked.txt", "modified\n")
	writeGitTestFile(t, repo, "untracked.txt", "new file\n")

	// Stash with --include-untracked should clean the worktree.
	if err := svc.Stash(ctx, repo, "wip: my changes"); err != nil {
		t.Fatalf("stash push: %v", err)
	}
	assertGitTestFileContent(t, repo, "tracked.txt", "base\n")
	assertGitTestFileMissing(t, repo, "untracked.txt")

	// List should report exactly one stash entry carrying the message.
	list, err := svc.StashList(ctx, repo)
	if err != nil {
		t.Fatalf("stash list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 stash entry, got %d (%#v)", len(list), list)
	}
	if idx, ok := list[0]["index"].(int); !ok || idx != 0 {
		t.Fatalf("expected stash index 0, got %#v", list[0]["index"])
	}
	if msg, _ := list[0]["message"].(string); msg == "" {
		t.Fatalf("expected non-empty stash message, got %#v", list[0])
	}

	// Apply restores the working changes but keeps the stash entry.
	applyResult, err := svc.StashApply(ctx, repo, 0)
	if err != nil {
		t.Fatalf("stash apply: %v", err)
	}
	if applyResult.HasConflicts {
		t.Fatalf("did not expect conflicts on clean apply: %#v", applyResult)
	}
	assertGitTestFileContent(t, repo, "tracked.txt", "modified\n")
	assertGitTestFileContent(t, repo, "untracked.txt", "new file\n")

	stillThere, err := svc.StashList(ctx, repo)
	if err != nil {
		t.Fatalf("stash list after apply: %v", err)
	}
	if len(stillThere) != 1 {
		t.Fatalf("apply must keep the stash entry, got %d", len(stillThere))
	}

	// Drop removes the stash entry; the list becomes empty.
	if err := svc.StashDrop(ctx, repo, 0); err != nil {
		t.Fatalf("stash drop: %v", err)
	}
	afterDrop, err := svc.StashList(ctx, repo)
	if err != nil {
		t.Fatalf("stash list after drop: %v", err)
	}
	if len(afterDrop) != 0 {
		t.Fatalf("expected empty stash list after drop, got %d (%#v)", len(afterDrop), afterDrop)
	}
}

// TestStashPopRestoresAndRemovesEntry confirms pop both restores changes and
// removes the stash in one operation (distinct from apply).
func TestStashPopRestoresAndRemovesEntry(t *testing.T) {
	svc := &Service{Timeout: 10 * time.Second}
	ctx := context.Background()

	repo := initDiscardTestRepo(t)
	writeGitTestFile(t, repo, "tracked.txt", "popme\n")

	if err := svc.Stash(ctx, repo, ""); err != nil {
		t.Fatalf("stash push: %v", err)
	}
	assertGitTestFileContent(t, repo, "tracked.txt", "base\n")

	result, err := svc.StashPop(ctx, repo)
	if err != nil {
		t.Fatalf("stash pop: %v", err)
	}
	if result.HasConflicts {
		t.Fatalf("did not expect conflicts on clean pop: %#v", result)
	}
	assertGitTestFileContent(t, repo, "tracked.txt", "popme\n")

	list, err := svc.StashList(ctx, repo)
	if err != nil {
		t.Fatalf("stash list after pop: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("pop must remove the stash entry, got %d (%#v)", len(list), list)
	}
}

// TestStashPopReportsConflictsWithoutHardError verifies that a conflicting pop
// is surfaced via StashResult.HasConflicts rather than as a hard error, matching
// the handler contract that returns the conflict payload to the frontend.
func TestStashPopReportsConflictsWithoutHardError(t *testing.T) {
	svc := &Service{Timeout: 10 * time.Second}
	ctx := context.Background()

	repo := initDiscardTestRepo(t)

	// Stash one change to tracked.txt.
	writeGitTestFile(t, repo, "tracked.txt", "stashed change\n")
	if err := svc.Stash(ctx, repo, ""); err != nil {
		t.Fatalf("stash push: %v", err)
	}

	// Commit a conflicting change to the same file so pop cannot apply cleanly.
	writeGitTestFile(t, repo, "tracked.txt", "committed conflict\n")
	runGitTest(t, repo, "add", ".")
	runGitTest(t, repo, "commit", "-m", "conflicting change")

	result, err := svc.StashPop(ctx, repo)
	if err != nil {
		t.Fatalf("conflicting stash pop should not hard-error, got %v", err)
	}
	if !result.HasConflicts {
		t.Fatalf("expected HasConflicts=true on conflicting pop, got %#v", result)
	}
}
