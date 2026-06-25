package narrators

import (
	"context"
	"testing"
)

// TestGitBranchChapterIssues locks the chapter-mode git branch/worktree issue
// detector, mirroring TS detectGitBranchViolations (bash-analyze.ts:1545). It is
// a discriminative unit test (direct boolean assertions on the function).
func TestGitBranchChapterIssues(t *testing.T) {
	issue := []string{
		"git switch main",
		"git checkout other",
		"git checkout -b new",
		"git branch -D old",
		"git branch newbranch",
		"git push --force",
		"git push origin --delete x",
		"git push origin a:b",
		"git merge feature",
		"git rebase main",
		"git reset --hard HEAD~1",
		"git reset --soft HEAD~1",
		"git reset HEAD~2",
		"git stash branch tmp",
		"git worktree add ../wt",
		"git worktree remove ../wt",
		"git filter-branch --tree-filter x",
		"git symbolic-ref HEAD refs/heads/x",
		"git -C /repo switch x", // -C literal tokens[1] -> unknown subcommand -> issue
		"git frobnicate",        // unknown subcommand
		"ls && git switch main", // later sub-command
	}
	for _, c := range issue {
		if !gitBranchChapterIssues(c) {
			t.Fatalf("expected chapter git issue for %q", c)
		}
	}

	clean := []string{
		"git status",
		"git log --oneline",
		"git diff",
		"git add .",
		"git commit -m x",
		"git checkout -- file.txt",
		"git checkout -p",
		"git branch",
		"git branch -a",
		"git push",
		"git push origin main",
		"git merge --abort",
		"git rebase --continue",
		"git reset",
		"git reset -- file.txt",
		"git reset HEAD file.txt",
		"git stash",
		"git stash pop",
		"git symbolic-ref HEAD",
		"git tag v1",
		"git fetch",
		"git pull",
		"git clean -fd",
		"ls -la",
		"",
	}
	for _, c := range clean {
		if gitBranchChapterIssues(c) {
			t.Fatalf("did not expect chapter git issue for %q", c)
		}
	}
}

// TestLooksLikeGitCommitRef locks the commit-ish heuristic mirroring TS
// looksLikeCommitRef (bash-analyze.ts:1815).
func TestLooksLikeGitCommitRef(t *testing.T) {
	yes := []string{"HEAD", "HEAD~3", "HEAD^2", "ORIG_HEAD", "MERGE_HEAD", "@", "a1b2c3d", "0123456789abcdef0123456789abcdef01234567", "v1.0", "v2.3.4"}
	no := []string{"file.txt", "src", "feature-x", "main", "abc", "README.md"}
	for _, a := range yes {
		if !looksLikeGitCommitRef(a) {
			t.Fatalf("looksLikeGitCommitRef(%q)=false want true", a)
		}
	}
	for _, a := range no {
		if looksLikeGitCommitRef(a) {
			t.Fatalf("looksLikeGitCommitRef(%q)=true want false", a)
		}
	}
}

// TestResolveToolPermissionChapterModeGitBranchGate verifies the chapter-mode
// git branch gate at the permission layer. The assertions are split into
// DISCRIMINATIVE cases (whose result changes when the gate is removed, so they
// are genuine parity evidence) and PARITY-LOCK cases (documented as
// non-discriminative).
func TestResolveToolPermissionChapterModeGitBranchGate(t *testing.T) {
	chapterID := "chap-1"
	withChapter := func(mode string) Narrator {
		n := permissionTestNarrator("", mode)
		n.ChapterID = &chapterID
		return n
	}

	// --- Discriminative (a): command whitelist `git *` + acceptEdits ---
	// Without the gate, commandWhitelistDecision(acceptEdits) -> allow; with the
	// gate, the branch-changing command must ask.
	t.Run("commandWhitelistAcceptEditsAsks", func(t *testing.T) {
		service := newPermissionTestService(t)
		cwd := t.TempDir()
		narrator := withChapter("acceptEdits")
		narrator.Cwd = &cwd
		if _, err := service.DB.Exec(`INSERT INTO narrator_whitelist_cmds (id, narrator_id, pattern, enabled, created_at) VALUES ('cw', ?, 'git *', 1, 'now')`, narrator.ID); err != nil {
			t.Fatalf("insert command whitelist: %v", err)
		}
		got, err := service.resolveToolPermission(context.Background(), narrator, cwd, permissionToolCall("Bash", `{"command":"git switch main"}`))
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if got.Action != "ask" {
			t.Fatalf("chapter git switch (whitelist+acceptEdits) = %s (%s), want ask", got.Action, got.Reason)
		}
	})

	// --- Discriminative (b): command whitelist `git *` + readOnly ---
	// Without the gate, commandWhitelistDecision(readOnly) -> allow (not a write
	// op); with the gate -> deny.
	t.Run("commandWhitelistReadOnlyDenies", func(t *testing.T) {
		service := newPermissionTestService(t)
		cwd := t.TempDir()
		narrator := withChapter("readOnly")
		narrator.Cwd = &cwd
		if _, err := service.DB.Exec(`INSERT INTO narrator_whitelist_cmds (id, narrator_id, pattern, enabled, created_at) VALUES ('cw', ?, 'git *', 1, 'now')`, narrator.ID); err != nil {
			t.Fatalf("insert command whitelist: %v", err)
		}
		got, err := service.resolveToolPermission(context.Background(), narrator, cwd, permissionToolCall("Bash", `{"command":"git switch main"}`))
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if got.Action != "deny" {
			t.Fatalf("chapter git switch (whitelist+readOnly) = %s (%s), want deny", got.Action, got.Reason)
		}
	})

	// --- Discriminative (c): cwd path whitelist + default (closest to real exploit) ---
	// Without the gate, the path-whitelist auto-allow allows `git switch main`
	// (paths=["."] -> cwd); with the gate -> ask. Control: `git status` (no
	// issue) still auto-allows.
	t.Run("pathWhitelistDefaultAsks", func(t *testing.T) {
		service := newPermissionTestService(t)
		cwd := t.TempDir()
		narrator := withChapter("default")
		narrator.Cwd = &cwd
		if _, err := service.DB.Exec(`INSERT INTO narrator_whitelist_dirs (id, narrator_id, path, access_level, enabled, created_at) VALUES ('wl', ?, ?, 'readWrite', 1, 'now')`, narrator.ID, cwd); err != nil {
			t.Fatalf("insert whitelist dir: %v", err)
		}
		gated, err := service.resolveToolPermission(context.Background(), narrator, cwd, permissionToolCall("Bash", `{"command":"git switch main"}`))
		if err != nil {
			t.Fatalf("resolve gated: %v", err)
		}
		if gated.Action != "ask" {
			t.Fatalf("chapter git switch (cwd whitelist+default) = %s (%s), want ask", gated.Action, gated.Reason)
		}
		// Control: a safe git command with no branch issue is still auto-allowed.
		control, err := service.resolveToolPermission(context.Background(), narrator, cwd, permissionToolCall("Bash", `{"command":"git status"}`))
		if err != nil {
			t.Fatalf("resolve control: %v", err)
		}
		if control.Action != "allow" {
			t.Fatalf("chapter git status (cwd whitelist+default) = %s (%s), want allow", control.Action, control.Reason)
		}
	})

	// --- Parity-lock (non-discriminative on its own; forms a chapter/non-chapter
	// contrast with case (a)): a non-chapter narrator does NOT trigger the gate,
	// so the command whitelist auto-allows in acceptEdits. ---
	t.Run("nonChapterDoesNotGate", func(t *testing.T) {
		service := newPermissionTestService(t)
		cwd := t.TempDir()
		narrator := permissionTestNarrator(cwd, "acceptEdits") // ChapterID nil
		if _, err := service.DB.Exec(`INSERT INTO narrator_whitelist_cmds (id, narrator_id, pattern, enabled, created_at) VALUES ('cw', ?, 'git *', 1, 'now')`, narrator.ID); err != nil {
			t.Fatalf("insert command whitelist: %v", err)
		}
		got, err := service.resolveToolPermission(context.Background(), narrator, cwd, permissionToolCall("Bash", `{"command":"git switch main"}`))
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if got.Action != "allow" {
			t.Fatalf("non-chapter git switch (whitelist+acceptEdits) = %s (%s), want allow", got.Action, got.Reason)
		}
	})
}
