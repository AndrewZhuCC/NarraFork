package narrators

import "testing"

// TestClassifyGitDangerCoversDestructiveSubcommands locks the git danger
// classification used by the danger-reflection heuristic. Note: this is Go's
// own conservative heuristic, NOT a byte-for-byte port of the TypeScript
// WASM-backed bash analyzer. Where the two differ, Go errs toward flagging MORE
// (e.g. it flags `git reset` with any argument and `git rm`/`git stash drop`),
// which is the safe direction for a confirmation gate — it never flags less than
// the representative destructive set below.
func TestClassifyGitDangerCoversDestructiveSubcommands(t *testing.T) {
	cases := []struct {
		name     string
		command  string
		want     bool
		severity dangerSeverity
	}{
		{"reset hard", "git reset --hard HEAD~1", true, dangerSeverityHigh},
		{"clean fd", "git clean -fd", true, dangerSeverityHigh},
		{"restore", "git restore src/main.go", true, dangerSeverityHigh},
		{"checkout path", "git checkout -- src/main.go", true, dangerSeverityHigh},
		{"branch force delete", "git branch -D feature", true, dangerSeverityHigh},
		{"worktree remove", "git worktree remove ../wt", true, dangerSeverityHigh},
		{"rebase", "git rebase main", true, dangerSeverityHigh},
		{"filter-branch", "git filter-branch --tree-filter x HEAD", true, dangerSeverityHigh},
		{"rm", "git rm tracked.txt", true, dangerSeverityHigh},
		{"reflog expire", "git reflog expire --all", true, dangerSeverityCritical},
		{"stash drop", "git stash drop", true, dangerSeverityHigh},
		{"stash clear", "git stash clear", true, dangerSeverityCritical},
		{"gc prune", "git gc --prune=now", true, dangerSeverityHigh},
		{"push force", "git push --force origin main", true, dangerSeverityCritical},
		{"push delete", "git push --delete origin feature", true, dangerSeverityCritical},
		// Safe / non-destructive git operations must NOT be flagged by classifyGitDanger.
		{"status", "git status", false, ""},
		{"log", "git log --oneline", false, ""},
		{"diff", "git diff HEAD", false, ""},
		{"add", "git add .", false, ""},
		{"commit", "git commit -m msg", false, ""},
		{"branch list", "git branch", false, ""},
		{"push plain", "git push origin main", false, ""},
		// git -C <dir> prefix must be skipped to reach the real subcommand.
		{"reset hard with -C prefix", "git -C /repo reset --hard", true, dangerSeverityHigh},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			info, ok := classifyGitDanger(tc.command)
			if ok != tc.want {
				t.Fatalf("classifyGitDanger(%q) flagged=%v want %v (info=%#v)", tc.command, ok, tc.want, info)
			}
			if tc.want && info.Severity != tc.severity {
				t.Fatalf("classifyGitDanger(%q) severity=%s want %s", tc.command, info.Severity, tc.severity)
			}
		})
	}
}

// TestClassifyGitDangerConservativeResetDivergenceFromTS documents and locks the
// intentional divergence from TypeScript: TS only flags `git reset --hard`,
// whereas Go conservatively flags any `git reset` that carries arguments (e.g.
// `git reset HEAD~1`). This is the safe direction (extra confirmation), so it is
// asserted as intended behavior rather than treated as a parity bug.
func TestClassifyGitDangerConservativeResetDivergenceFromTS(t *testing.T) {
	// reset with an argument -> Go flags (more conservative than TS).
	if _, ok := classifyGitDanger("git reset HEAD~1"); !ok {
		t.Fatal("Go intentionally flags `git reset HEAD~1` (conservative); expected flagged")
	}
	// bare `git reset` (no args) -> not flagged by either backend.
	if _, ok := classifyGitDanger("git reset"); ok {
		t.Fatal("bare `git reset` should not be flagged")
	}
}
