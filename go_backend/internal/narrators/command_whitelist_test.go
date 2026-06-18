package narrators

import "testing"

func cmdRule(pattern string) permissionCommandRule { return permissionCommandRule{Pattern: pattern} }

// TestCommandWhitelistCoveredRequiresEverySubcommand locks the whitelist
// coverage set-semantics: a chained command is covered only when EVERY
// sub-command (split on &&/||/;/|) matches some whitelist pattern; if any
// sub-command is unmatched, the whole command is not covered. Empty
// command/rules are not covered.
func TestCommandWhitelistCoveredRequiresEverySubcommand(t *testing.T) {
	rules := []permissionCommandRule{cmdRule("ls"), cmdRule("git status"), cmdRule("echo *")}

	covered := []string{
		"ls",
		"ls -la",     // prefix-token match
		"git status", // multi-token pattern
		"echo hi",    // glob pattern
		"ls && git status",
		"ls | echo piped",
	}
	for _, c := range covered {
		if !commandWhitelistCovered(c, rules) {
			t.Fatalf("expected %q to be whitelist-covered", c)
		}
	}

	notCovered := []string{
		"rm -rf x",       // no rule
		"ls && rm -rf x", // one sub-command unmatched -> whole not covered
		"git push",       // git status rule does not cover git push
		"",               // empty
	}
	for _, c := range notCovered {
		if commandWhitelistCovered(c, rules) {
			t.Fatalf("did not expect %q to be whitelist-covered", c)
		}
	}
	// Empty rules never cover.
	if commandWhitelistCovered("ls", nil) {
		t.Fatal("empty rules must not cover any command")
	}
}

// TestCommandBlacklistMatchIsAnySubcommand locks the blacklist set-semantics: a
// chained command matches when ANY sub-command matches any blacklist pattern.
func TestCommandBlacklistMatchIsAnySubcommand(t *testing.T) {
	rules := []permissionCommandRule{cmdRule("rm *"), cmdRule("curl *")}

	if commandBlacklistMatch("ls -la", rules) != nil {
		t.Fatal("ls should not match blacklist")
	}
	if m := commandBlacklistMatch("rm -rf x", rules); m == nil || m.Pattern != "rm *" {
		t.Fatalf("rm should match blacklist rule 'rm *', got %#v", m)
	}
	// any sub-command matching triggers a match.
	if m := commandBlacklistMatch("ls && curl http://evil", rules); m == nil || m.Pattern != "curl *" {
		t.Fatalf("chained curl should match blacklist 'curl *', got %#v", m)
	}
	if commandBlacklistMatch("echo hi", nil) != nil {
		t.Fatal("empty rules must not match")
	}
}

// TestCommandWhitelistDecisionPerPermissionMode locks the per-mode action for a
// command that is already whitelist-covered: bypassPermissions/acceptEdits ->
// allow; dontAsk -> deny; readOnly -> allow for read, deny for write; default ->
// ask (whitelisting reduces but does not eliminate the default approval gate).
func TestCommandWhitelistDecisionPerPermissionMode(t *testing.T) {
	mk := func(mode string) Narrator {
		m := mode
		return Narrator{PermissionMode: &m}
	}
	cases := []struct {
		mode    string
		command string
		action  string
	}{
		{"bypassPermissions", "rm -rf x", "allow"},
		{"acceptEdits", "rm -rf x", "allow"},
		{"dontAsk", "ls", "deny"},
		{"readOnly", "ls -la", "allow"},
		{"readOnly", "rm -rf x", "deny"}, // write op denied in readOnly
		{"default", "ls", "ask"},
	}
	for _, tc := range cases {
		got := commandWhitelistDecision(tc.command, mk(tc.mode))
		if got.Action != tc.action {
			t.Fatalf("mode=%s cmd=%q action=%q want %q (reason=%q)", tc.mode, tc.command, got.Action, tc.action, got.Reason)
		}
	}
}
