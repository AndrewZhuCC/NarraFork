package git

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestLogPagedReturnsCommitsWithPaging covers the git log endpoint read path
// (Service.LogPaged): commits come back newest-first and skip/limit page through
// history. These commit-detail read methods previously had no focused coverage.
func TestLogPagedReturnsCommitsWithPaging(t *testing.T) {
	svc := &Service{Timeout: 10 * time.Second}
	ctx := context.Background()

	repo := initDiscardTestRepo(t) // 1 commit: "base"
	for _, msg := range []string{"second", "third"} {
		writeGitTestFile(t, repo, "tracked.txt", msg+"\n")
		runGitTest(t, repo, "add", ".")
		runGitTest(t, repo, "commit", "-m", msg)
	}

	all, err := svc.LogPaged(ctx, repo, 50, 0)
	if err != nil {
		t.Fatalf("log paged: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 commits, got %d", len(all))
	}
	// Newest first.
	if all[0].Message != "third" || all[2].Message != "base" {
		t.Fatalf("unexpected ordering: %q ... %q", all[0].Message, all[2].Message)
	}
	if all[0].SHA == "" || all[0].ShortSHA == "" || all[0].AuthoredAt == "" {
		t.Fatalf("expected populated sha/shortSha/authoredAt, got %#v", all[0])
	}
	if all[0].AuthorName != "Test User" || all[0].AuthorEmail != "test@example.com" {
		t.Fatalf("unexpected author fields: %#v", all[0])
	}

	// limit=1 returns only the newest; skip=1 returns the next page.
	page1, err := svc.LogPaged(ctx, repo, 1, 0)
	if err != nil || len(page1) != 1 || page1[0].Message != "third" {
		t.Fatalf("page1 unexpected: %v %#v", err, page1)
	}
	page2, err := svc.LogPaged(ctx, repo, 1, 1)
	if err != nil || len(page2) != 1 || page2[0].Message != "second" {
		t.Fatalf("page2 unexpected: %v %#v", err, page2)
	}
}

// TestShowCommitReturnsFullMessageAndAuthor covers the commit-detail header read
// (Service.ShowCommit).
func TestShowCommitReturnsFullMessageAndAuthor(t *testing.T) {
	svc := &Service{Timeout: 10 * time.Second}
	ctx := context.Background()

	repo := initDiscardTestRepo(t)
	writeGitTestFile(t, repo, "tracked.txt", "changed\n")
	runGitTest(t, repo, "add", ".")
	runGitTest(t, repo, "commit", "-m", "subject line"+"\n\nbody paragraph")
	sha := strings.TrimSpace(runGitTest(t, repo, "rev-parse", "HEAD"))

	commit, err := svc.ShowCommit(ctx, repo, sha)
	if err != nil {
		t.Fatalf("show commit: %v", err)
	}
	if commit.SHA != sha {
		t.Fatalf("expected sha %q, got %q", sha, commit.SHA)
	}
	if commit.Message != "subject line" {
		t.Fatalf("expected subject %q, got %q", "subject line", commit.Message)
	}
	if !strings.Contains(commit.FullMessage, "body paragraph") {
		t.Fatalf("expected body in full message, got %q", commit.FullMessage)
	}
	if commit.AuthorName != "Test User" || commit.AuthorEmail != "test@example.com" {
		t.Fatalf("unexpected author: %#v", commit)
	}
}

// TestCommitFilesAndDiffStats covers the per-commit file list, numstat-derived
// line counts, rename detection, and the shortstat summary used by the commit
// detail endpoint (Service.CommitFiles + Service.CommitDiffStats).
func TestCommitFilesAndDiffStats(t *testing.T) {
	svc := &Service{Timeout: 10 * time.Second}
	ctx := context.Background()

	repo := initDiscardTestRepo(t)
	// A commit that adds one file and modifies the base file.
	writeGitTestFile(t, repo, "added.txt", "line1\nline2\n")
	writeGitTestFile(t, repo, "tracked.txt", "base\nextra\n")
	runGitTest(t, repo, "add", ".")
	runGitTest(t, repo, "commit", "-m", "add and modify")
	sha := strings.TrimSpace(runGitTest(t, repo, "rev-parse", "HEAD"))

	files, err := svc.CommitFiles(ctx, repo, sha)
	if err != nil {
		t.Fatalf("commit files: %v", err)
	}
	byPath := map[string]CommitFile{}
	for _, f := range files {
		byPath[f.Path] = f
	}
	added, ok := byPath["added.txt"]
	if !ok || added.Status != "added" || added.LinesAdded != 2 {
		t.Fatalf("unexpected added.txt entry: %#v (all: %#v)", added, files)
	}
	modified, ok := byPath["tracked.txt"]
	if !ok || modified.Status != "modified" || modified.LinesAdded != 1 {
		t.Fatalf("unexpected tracked.txt entry: %#v", modified)
	}

	filesChanged, linesAdded, linesRemoved, err := svc.CommitDiffStats(ctx, repo, sha)
	if err != nil {
		t.Fatalf("commit diff stats: %v", err)
	}
	if filesChanged != 2 {
		t.Fatalf("expected 2 files changed, got %d", filesChanged)
	}
	if linesAdded != 3 {
		t.Fatalf("expected 3 insertions, got %d", linesAdded)
	}
	_ = linesRemoved

	// CommitFileDiff returns a unified diff for a single file with a truncation flag.
	diff, err := svc.CommitFileDiff(ctx, repo, sha, "added.txt")
	if err != nil {
		t.Fatalf("commit file diff: %v", err)
	}
	if trunc, _ := diff["truncated"].(bool); trunc {
		t.Fatalf("did not expect truncation for tiny diff")
	}
	if text, _ := diff["diff"].(string); !strings.Contains(text, "added.txt") {
		t.Fatalf("expected diff to mention added.txt, got %q", text)
	}
}

// TestCommitFilesDetectsRename verifies rename status/oldPath plumbing.
func TestCommitFilesDetectsRename(t *testing.T) {
	svc := &Service{Timeout: 10 * time.Second}
	ctx := context.Background()

	repo := initDiscardTestRepo(t)
	// Seed a file with enough content for git to detect a rename on move.
	writeGitTestFile(t, repo, "original.txt", strings.Repeat("content line\n", 20))
	runGitTest(t, repo, "add", ".")
	runGitTest(t, repo, "commit", "-m", "seed rename source")

	runGitTest(t, repo, "mv", "original.txt", "renamed.txt")
	runGitTest(t, repo, "commit", "-m", "rename file")
	sha := strings.TrimSpace(runGitTest(t, repo, "rev-parse", "HEAD"))

	files, err := svc.CommitFiles(ctx, repo, sha)
	if err != nil {
		t.Fatalf("commit files: %v", err)
	}
	var renamed *CommitFile
	for i := range files {
		if files[i].Path == "renamed.txt" {
			renamed = &files[i]
		}
	}
	if renamed == nil {
		t.Fatalf("expected renamed.txt entry, got %#v", files)
	}
	if renamed.Status != "renamed" || renamed.OldPath != "original.txt" {
		t.Fatalf("expected renamed status with oldPath=original.txt, got %#v", *renamed)
	}
}
