package git

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestRebaseCleanReplaysCommitsOntoUpstream covers the success path of the
// frontend-consumed ruler rebase operation (POST /projects/:id/ruler/rebase →
// Service.Rebase). The Rebase/RebaseAbort/ConflictFiles methods previously had
// no focused service-level coverage.
func TestRebaseCleanReplaysCommitsOntoUpstream(t *testing.T) {
	svc := &Service{Timeout: 10 * time.Second}
	ctx := context.Background()

	repo := initDiscardTestRepo(t) // main @ tracked.txt = "base\n"

	// main advances with a non-conflicting file.
	writeGitTestFile(t, repo, "main-only.txt", "main work\n")
	runGitTest(t, repo, "add", ".")
	runGitTest(t, repo, "commit", "-m", "main advance")

	// feature branches from the original base and edits a different file.
	runGitTest(t, repo, "checkout", "-b", "feature", "HEAD~1")
	writeGitTestFile(t, repo, "feature-only.txt", "feature work\n")
	runGitTest(t, repo, "add", ".")
	runGitTest(t, repo, "commit", "-m", "feature advance")

	// Rebase feature onto main: clean replay, no conflicts.
	result, err := svc.Rebase(ctx, repo, "main")
	if err != nil {
		t.Fatalf("rebase: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected clean rebase success, got %#v", result)
	}
	if len(result.ConflictFiles) != 0 {
		t.Fatalf("expected no conflicts, got %#v", result.ConflictFiles)
	}
	if result.CommitSHA == "" {
		t.Fatalf("expected rebased HEAD sha, got empty")
	}
	// Both files must now coexist on the rebased feature branch.
	assertGitTestFileContent(t, repo, "main-only.txt", "main work\n")
	assertGitTestFileContent(t, repo, "feature-only.txt", "feature work\n")
}

// TestRebaseConflictReportsConflictFilesThenAbortRestores covers the conflict
// branch of ruler rebase plus the rebase-resolve abort action.
func TestRebaseConflictReportsConflictFilesThenAbortRestores(t *testing.T) {
	svc := &Service{Timeout: 10 * time.Second}
	ctx := context.Background()

	repo := initDiscardTestRepo(t)

	// main edits shared.txt one way.
	writeGitTestFile(t, repo, "shared.txt", "main edit\n")
	runGitTest(t, repo, "add", ".")
	runGitTest(t, repo, "commit", "-m", "main shared edit")

	// feature branches from base and edits shared.txt a conflicting way.
	runGitTest(t, repo, "checkout", "-b", "feature", "HEAD~1")
	writeGitTestFile(t, repo, "shared.txt", "feature edit\n")
	runGitTest(t, repo, "add", ".")
	runGitTest(t, repo, "commit", "-m", "feature shared edit")
	featureHead := strings.TrimSpace(runGitTest(t, repo, "rev-parse", "HEAD"))

	result, err := svc.Rebase(ctx, repo, "main")
	if err != nil {
		t.Fatalf("conflicting rebase should not hard-error, got %v", err)
	}
	if result.Success {
		t.Fatalf("expected conflict (success=false), got %#v", result)
	}
	foundShared := false
	for _, c := range result.ConflictFiles {
		if c.File == "shared.txt" {
			foundShared = true
		}
	}
	if !foundShared {
		t.Fatalf("expected shared.txt in conflict files, got %#v", result.ConflictFiles)
	}

	// ConflictFiles (used by rebase-resolve) reports the same file mid-rebase.
	conflicts, err := svc.ConflictFiles(ctx, repo)
	if err != nil {
		t.Fatalf("conflict files: %v", err)
	}
	if len(conflicts) == 0 || conflicts[0].File != "shared.txt" {
		t.Fatalf("expected shared.txt conflict, got %#v", conflicts)
	}

	// Abort restores the feature branch to its pre-rebase HEAD.
	if err := svc.RebaseAbort(ctx, repo); err != nil {
		t.Fatalf("rebase abort: %v", err)
	}
	head := strings.TrimSpace(runGitTest(t, repo, "rev-parse", "HEAD"))
	if head != featureHead {
		t.Fatalf("expected HEAD restored to %q after abort, got %q", featureHead, head)
	}
	assertGitTestFileContent(t, repo, "shared.txt", "feature edit\n")
}

func TestRebaseRequiresUpstream(t *testing.T) {
	svc := &Service{Timeout: 5 * time.Second}
	if _, err := svc.Rebase(context.Background(), t.TempDir(), "  "); err == nil {
		t.Fatalf("expected error for blank upstream, got nil")
	}
}
