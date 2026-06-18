package git

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestCherryPickCommitsSuccessAppliesCommitsAndReturnsHead exercises the
// frontend-consumed Go-only POST /chapters/:id/cherry-pick git execution path
// (no TypeScript route mirrors it, so this focused test is the only safety net).
func TestCherryPickCommitsSuccessAppliesCommitsAndReturnsHead(t *testing.T) {
	svc := &Service{Timeout: 10 * time.Second}
	ctx := context.Background()

	// Source repo: build a feature commit on a side branch off main.
	repo := t.TempDir()
	runGitTest(t, repo, "init")
	runGitTest(t, repo, "checkout", "-b", "main")
	runGitTest(t, repo, "config", "user.name", "Test User")
	runGitTest(t, repo, "config", "user.email", "test@example.com")
	writeGitTestFile(t, repo, "base.txt", "base\n")
	runGitTest(t, repo, "add", ".")
	runGitTest(t, repo, "commit", "-m", "base")

	runGitTest(t, repo, "checkout", "-b", "feature")
	writeGitTestFile(t, repo, "feature.txt", "feature change\n")
	runGitTest(t, repo, "add", ".")
	runGitTest(t, repo, "commit", "-m", "feature commit")
	featureSHA := strings.TrimSpace(runGitTest(t, repo, "rev-parse", "HEAD"))

	// Back on main, cherry-pick the feature commit.
	runGitTest(t, repo, "checkout", "main")
	result, err := svc.CherryPickCommits(ctx, repo, []string{featureSHA})
	if err != nil {
		t.Fatalf("cherry-pick: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success, got %#v", result)
	}
	if result.CommitSHA == "" {
		t.Fatalf("expected new HEAD commit sha, got empty")
	}
	if len(result.ConflictFiles) != 0 {
		t.Fatalf("expected no conflicts, got %#v", result.ConflictFiles)
	}
	// The cherry-picked file must now exist on main.
	assertGitTestFileContent(t, repo, "feature.txt", "feature change\n")
	// HEAD must point at a real commit reachable on main that carries the change.
	headSHA := strings.TrimSpace(runGitTest(t, repo, "rev-parse", "HEAD"))
	if result.CommitSHA != headSHA {
		t.Fatalf("result CommitSHA %s should match repo HEAD %s after cherry-pick", result.CommitSHA, headSHA)
	}
}

func TestCherryPickCommitsConflictReturnsConflictFilesWithoutError(t *testing.T) {
	svc := &Service{Timeout: 10 * time.Second}
	ctx := context.Background()

	repo := t.TempDir()
	runGitTest(t, repo, "init")
	runGitTest(t, repo, "checkout", "-b", "main")
	runGitTest(t, repo, "config", "user.name", "Test User")
	runGitTest(t, repo, "config", "user.email", "test@example.com")
	writeGitTestFile(t, repo, "shared.txt", "base\n")
	runGitTest(t, repo, "add", ".")
	runGitTest(t, repo, "commit", "-m", "base")

	// Feature branch edits shared.txt one way.
	runGitTest(t, repo, "checkout", "-b", "feature")
	writeGitTestFile(t, repo, "shared.txt", "feature edit\n")
	runGitTest(t, repo, "add", ".")
	runGitTest(t, repo, "commit", "-m", "feature edit")
	featureSHA := strings.TrimSpace(runGitTest(t, repo, "rev-parse", "HEAD"))

	// Main edits shared.txt a conflicting way.
	runGitTest(t, repo, "checkout", "main")
	writeGitTestFile(t, repo, "shared.txt", "main edit\n")
	runGitTest(t, repo, "add", ".")
	runGitTest(t, repo, "commit", "-m", "main edit")

	result, err := svc.CherryPickCommits(ctx, repo, []string{featureSHA})
	// Conflicts are reported via the result, not as a hard error.
	if err != nil {
		t.Fatalf("conflict cherry-pick should not hard-error, got %v", err)
	}
	if result.Success {
		t.Fatalf("expected conflict (success=false), got %#v", result)
	}
	foundShared := false
	for _, f := range result.ConflictFiles {
		if f == "shared.txt" {
			foundShared = true
		}
	}
	if !foundShared {
		t.Fatalf("expected shared.txt in conflict files, got %#v", result.ConflictFiles)
	}
}

func TestCherryPickCommitsEmptyListIsNoop(t *testing.T) {
	svc := &Service{Timeout: 5 * time.Second}
	result, err := svc.CherryPickCommits(context.Background(), t.TempDir(), nil)
	if err != nil {
		t.Fatalf("empty cherry-pick should not error: %v", err)
	}
	if !result.Success {
		t.Fatalf("empty cherry-pick should be success no-op, got %#v", result)
	}
}
