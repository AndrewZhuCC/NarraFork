package git

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestInitRepoAndIsRepo covers repository creation/detection used by project
// initialization (InitRepo, IsRepo). These previously had no focused coverage.
func TestInitRepoAndIsRepo(t *testing.T) {
	svc := &Service{Timeout: 10 * time.Second}
	ctx := context.Background()

	dir := filepath.Join(t.TempDir(), "fresh")
	if svc.IsRepo(ctx, dir) {
		t.Fatalf("non-existent dir should not report as repo")
	}
	if err := svc.InitRepo(ctx, dir); err != nil {
		t.Fatalf("init repo: %v", err)
	}
	if !svc.IsRepo(ctx, dir) {
		t.Fatalf("initialized dir should report as repo")
	}
}

// TestStatusAndGetStatusReflectChanges covers the porcelain status read methods
// (Status struct + raw GetStatus string).
func TestStatusAndGetStatusReflectChanges(t *testing.T) {
	svc := &Service{Timeout: 10 * time.Second}
	ctx := context.Background()

	repo := initDiscardTestRepo(t)

	clean, err := svc.Status(ctx, repo)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !clean.Clean || len(clean.Changed) != 0 {
		t.Fatalf("expected clean status, got %#v", clean)
	}
	if clean.Branch != "main" {
		t.Fatalf("expected branch main, got %q", clean.Branch)
	}

	writeGitTestFile(t, repo, "tracked.txt", "dirty\n")
	writeGitTestFile(t, repo, "new.txt", "added\n")

	dirty, err := svc.Status(ctx, repo)
	if err != nil {
		t.Fatalf("status dirty: %v", err)
	}
	if dirty.Clean {
		t.Fatalf("expected dirty status, got clean")
	}
	if len(dirty.Changed) != 2 {
		t.Fatalf("expected 2 changed entries, got %#v", dirty.Changed)
	}

	raw, err := svc.GetStatus(ctx, repo)
	if err != nil {
		t.Fatalf("get status: %v", err)
	}
	if !strings.Contains(raw, "tracked.txt") || !strings.Contains(raw, "new.txt") {
		t.Fatalf("raw status should list both files, got %q", raw)
	}
}

// TestFullDiffAndDiffBetweenRefs covers the working-tree full diff and the
// ref-to-ref diff used by chapter diff views (FullDiff, DiffBetweenRefs).
func TestFullDiffAndDiffBetweenRefs(t *testing.T) {
	svc := &Service{Timeout: 10 * time.Second}
	ctx := context.Background()

	repo := initDiscardTestRepo(t)
	base := strings.TrimSpace(runGitTest(t, repo, "rev-parse", "HEAD"))

	// Uncommitted change shows in FullDiff.
	writeGitTestFile(t, repo, "tracked.txt", "working change\n")
	full, err := svc.FullDiff(ctx, repo)
	if err != nil {
		t.Fatalf("full diff: %v", err)
	}
	if !strings.Contains(full, "tracked.txt") || !strings.Contains(full, "working change") {
		t.Fatalf("expected full diff to show working change, got %q", full)
	}

	// Commit it, then DiffBetweenRefs(base, HEAD) shows the same change.
	runGitTest(t, repo, "add", ".")
	runGitTest(t, repo, "commit", "-m", "commit change")
	head := strings.TrimSpace(runGitTest(t, repo, "rev-parse", "HEAD"))

	between, err := svc.DiffBetweenRefs(ctx, repo, base, head)
	if err != nil {
		t.Fatalf("diff between refs: %v", err)
	}
	if !strings.Contains(between, "working change") {
		t.Fatalf("expected ref diff to show committed change, got %q", between)
	}

	// Identical refs produce an empty diff.
	empty, err := svc.DiffBetweenRefs(ctx, repo, head, head)
	if err != nil {
		t.Fatalf("diff identical refs: %v", err)
	}
	if strings.TrimSpace(empty) != "" {
		t.Fatalf("expected empty diff for identical refs, got %q", empty)
	}
}

// TestAutoCommitOnlyCommitsWhenDirty covers AutoCommit: it stages and commits all
// changes when the tree is dirty, and is a no-op (returns current HEAD) when clean.
func TestAutoCommitOnlyCommitsWhenDirty(t *testing.T) {
	svc := &Service{Timeout: 10 * time.Second}
	ctx := context.Background()

	repo := initDiscardTestRepo(t)
	head0 := strings.TrimSpace(runGitTest(t, repo, "rev-parse", "HEAD"))

	// Clean tree: AutoCommit returns the existing HEAD without a new commit.
	sha, err := svc.AutoCommit(ctx, repo, "noop")
	if err != nil {
		t.Fatalf("auto commit clean: %v", err)
	}
	if sha != head0 {
		t.Fatalf("expected no-op to return current HEAD %q, got %q", head0, sha)
	}

	// Dirty tree: AutoCommit creates a new commit capturing the changes.
	writeGitTestFile(t, repo, "auto.txt", "auto content\n")
	sha2, err := svc.AutoCommit(ctx, repo, "auto commit")
	if err != nil {
		t.Fatalf("auto commit dirty: %v", err)
	}
	if sha2 == "" || sha2 == head0 {
		t.Fatalf("expected new commit sha, got %q (head0 %q)", sha2, head0)
	}
	st, err := svc.Status(ctx, repo)
	if err != nil {
		t.Fatalf("status after auto commit: %v", err)
	}
	if !st.Clean {
		t.Fatalf("expected clean tree after auto commit, got %#v", st)
	}
}

// TestCommitGitignoreIfDirtyCommitsOnlyGitignore covers the .gitignore helper:
// it commits a dirty .gitignore and is a no-op when .gitignore is unchanged.
func TestCommitGitignoreIfDirtyCommitsOnlyGitignore(t *testing.T) {
	svc := &Service{Timeout: 10 * time.Second}
	ctx := context.Background()

	repo := initDiscardTestRepo(t)
	head0 := strings.TrimSpace(runGitTest(t, repo, "rev-parse", "HEAD"))

	// No .gitignore changes: no-op, HEAD unchanged.
	if err := svc.CommitGitignoreIfDirty(ctx, repo); err != nil {
		t.Fatalf("gitignore no-op: %v", err)
	}
	if h := strings.TrimSpace(runGitTest(t, repo, "rev-parse", "HEAD")); h != head0 {
		t.Fatalf("expected HEAD unchanged on no-op, got %q", h)
	}

	// Add a .gitignore: it should be committed.
	writeGitTestFile(t, repo, ".gitignore", "node_modules/\n")
	if err := svc.CommitGitignoreIfDirty(ctx, repo); err != nil {
		t.Fatalf("commit gitignore: %v", err)
	}
	headAfter := strings.TrimSpace(runGitTest(t, repo, "rev-parse", "HEAD"))
	if headAfter == head0 {
		t.Fatalf("expected a new commit for .gitignore, HEAD unchanged")
	}
	// Tree must be clean afterward (only .gitignore was committed).
	st, err := svc.Status(ctx, repo)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !st.Clean {
		t.Fatalf("expected clean tree after gitignore commit, got %#v", st)
	}
}

// TestLogRefReturnsCommitsForBranch covers LogRef which logs a specific ref.
func TestLogRefReturnsCommitsForBranch(t *testing.T) {
	svc := &Service{Timeout: 10 * time.Second}
	ctx := context.Background()

	repo := initDiscardTestRepo(t)
	runGitTest(t, repo, "checkout", "-b", "feature")
	writeGitTestFile(t, repo, "f.txt", "feature\n")
	runGitTest(t, repo, "add", ".")
	runGitTest(t, repo, "commit", "-m", "feature commit")

	// main only has the base commit.
	mainCommits, err := svc.LogRef(ctx, repo, "main", 50)
	if err != nil {
		t.Fatalf("log ref main: %v", err)
	}
	if len(mainCommits) != 1 || mainCommits[0].Message != "base" {
		t.Fatalf("expected only base on main, got %#v", mainCommits)
	}

	// feature has base + feature commit, newest first.
	featCommits, err := svc.LogRef(ctx, repo, "feature", 50)
	if err != nil {
		t.Fatalf("log ref feature: %v", err)
	}
	if len(featCommits) != 2 || featCommits[0].Message != "feature commit" {
		t.Fatalf("expected feature commit newest-first, got %#v", featCommits)
	}
}

// TestDiffTruncationUsesUTF16Boundary verifies FullDiff and DiffBetweenRefs cap
// the diff at 100000 UTF-16 code units (matching the TS getFullDiff/getDiffBetweenRefs
// `maxBytes` budget, which is actually measured via String.length/slice) and never
// emit invalid UTF-8 by cutting a multi-byte rune mid-sequence. A large CJK file
// (>100000 UTF-16 units, ~3x bytes) would, under a byte slice, be cut mid-rune.
func TestDiffTruncationUsesUTF16Boundary(t *testing.T) {
	svc := &Service{Timeout: 30 * time.Second}
	ctx := context.Background()
	repo := initDiscardTestRepo(t)
	base := strings.TrimSpace(runGitTest(t, repo, "rev-parse", "HEAD"))

	// 120000 CJK chars = 120000 UTF-16 units (360000 bytes), comfortably over 100000.
	big := strings.Repeat("章", 120000) + "\n"
	writeGitTestFile(t, repo, "big.txt", big)

	full, err := svc.FullDiff(ctx, repo)
	if err != nil {
		t.Fatalf("full diff: %v", err)
	}
	if !strings.Contains(full, "[diff truncated — exceeded size limit]") {
		t.Fatalf("expected FullDiff to be truncated")
	}
	if strings.ContainsRune(full, 0xFFFD) {
		t.Fatalf("FullDiff truncation produced invalid UTF-8 (U+FFFD)")
	}
	if fullUnits := len([]rune(full)); fullUnits < 90000 {
		t.Fatalf("FullDiff kept only %d runes — looks byte-capped, want UTF-16 cap (~100000)", fullUnits)
	}

	runGitTest(t, repo, "add", ".")
	runGitTest(t, repo, "commit", "-m", "big cjk file")
	head := strings.TrimSpace(runGitTest(t, repo, "rev-parse", "HEAD"))

	between, err := svc.DiffBetweenRefs(ctx, repo, base, head)
	if err != nil {
		t.Fatalf("diff between refs: %v", err)
	}
	if !strings.Contains(between, "[diff truncated — exceeded size limit]") {
		t.Fatalf("expected DiffBetweenRefs to be truncated")
	}
	if strings.ContainsRune(between, 0xFFFD) {
		t.Fatalf("DiffBetweenRefs truncation produced invalid UTF-8 (U+FFFD)")
	}
	// The kept content must be capped by UTF-16 code units (~100000), not by bytes.
	// A byte-length cap on a CJK diff (3 bytes/rune) would keep only ~33334 units,
	// so a kept length well above that proves the UTF-16 boundary is used.
	betweenUnits := len([]rune(between))
	if betweenUnits < 90000 {
		t.Fatalf("DiffBetweenRefs kept only %d runes — looks byte-capped, want UTF-16 cap (~100000)", betweenUnits)
	}
}
