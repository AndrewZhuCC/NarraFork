package narrators

import "testing"

// TestTSGlobMatchMatchesTypeScriptSemantics verifies the command-pattern glob
// matcher mirrors the TypeScript narrator-permission globMatch exactly: only '*'
// is a wildcard (matching any run of characters including '/'), and '?', '[',
// ']' are literals. This locks the parity fix that replaced filepath.Match
// (which diverged on '/'-crossing and char-class metacharacters).
func TestTSGlobMatchMatchesTypeScriptSemantics(t *testing.T) {
	cases := []struct {
		text    string
		pattern string
		want    bool
	}{
		{"anything", "*", true},
		{"", "*", true},
		{"rm", "rm", true},
		{"rm", "rf", false},
		{"git", "git*", true},
		{"github", "git*", true},
		// '*' must cross '/' like the TS regex .* (filepath.Match would not).
		{"origin/sub/path", "origin/*", true},
		{"origin/main", "origin/*", true},
		// '[' ']' are literals, NOT a char class (filepath.Match would treat as class).
		{"a", "[abc]", false},
		{"[abc]", "[abc]", true},
		{"file[1]", "file[1]", true},
		// '?' is a literal, not single-char wildcard.
		{"a", "?", false},
		{"?", "?", true},
		// mid-pattern star.
		{"rm-rf", "rm*rf", true},
		{"rmXrf", "rm*rf", true},
		{"rmrf", "rm*rf", true},
		{"rm-rX", "rm*rf", false},
	}
	for _, tc := range cases {
		if got := tsGlobMatch(tc.text, tc.pattern); got != tc.want {
			t.Fatalf("tsGlobMatch(%q,%q)=%v want %v", tc.text, tc.pattern, got, tc.want)
		}
	}
}

// TestMatchCommandPatternParity verifies prefix-token command matching mirrors
// the TS matchCommandPattern: a pattern matches when its space-separated parts
// glob-match the leading tokens; longer patterns than tokens never match; the
// blacklist-style "rm *" pattern blocks any rm-with-arg command.
func TestMatchCommandPatternParity(t *testing.T) {
	cases := []struct {
		tokens  []string
		pattern string
		want    bool
	}{
		{[]string{"rm", "-rf", "x"}, "rm *", true},
		{[]string{"rm"}, "rm *", false}, // pattern longer than tokens
		{[]string{"rm"}, "rm", true},
		{[]string{"git", "push", "origin/main"}, "git push origin/*", true},
		{[]string{"git", "push", "origin/sub/main"}, "git push origin/*", true}, // '*' crosses '/'
		{[]string{"ls", "-la"}, "rm *", false},
		{[]string{"echo", "hi"}, "*", true},
		{[]string{}, "rm", false},      // no tokens
		{[]string{"rm"}, "   ", false}, // empty pattern after trim
	}
	for _, tc := range cases {
		if got := matchCommandPattern(tc.tokens, tc.pattern); got != tc.want {
			t.Fatalf("matchCommandPattern(%v,%q)=%v want %v", tc.tokens, tc.pattern, got, tc.want)
		}
	}
}

// TestSplitShellCommandsSplitsOnOperators verifies command splitting on
// &&, ||, ;, | so blacklist matching inspects each sub-command (mirrors TS
// bash-analysis command splitting used by the permission blacklist).
func TestSplitShellCommandsSplitsOnOperators(t *testing.T) {
	got := splitShellCommands("ls -la && rm -rf x ; echo done | grep d")
	want := [][]string{
		{"ls", "-la"},
		{"rm", "-rf", "x"},
		{"echo", "done"},
		{"grep", "d"},
	}
	if len(got) != len(want) {
		t.Fatalf("split count=%d want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if len(got[i]) != len(want[i]) {
			t.Fatalf("sub[%d]=%v want %v", i, got[i], want[i])
		}
		for j := range want[i] {
			if got[i][j] != want[i][j] {
				t.Fatalf("sub[%d][%d]=%q want %q", i, j, got[i][j], want[i][j])
			}
		}
	}
}
