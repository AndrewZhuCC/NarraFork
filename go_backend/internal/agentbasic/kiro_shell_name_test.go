package agentbasic

import (
	"testing"
)

// TestKiroShellNameMirrorsShellEnvBasename pins the W801 Kiro/NKP/NUG-kiro request
// shellState.shellName to the TS buildKiroRequest semantics
// (kiro-protocol.ts:152): process.env.SHELL?.split("/").pop() ?? "bash". Go
// previously hardcoded "bash". The normal-path basename extraction must match TS
// exactly; the only documented divergence is the defensive "bash" fallback for an
// empty basename (TS would emit "" there — a likely TS bug Go avoids).
func TestKiroShellNameMirrorsShellEnvBasename(t *testing.T) {
	cases := []struct {
		name  string
		shell string
		want  string
	}{
		{"absolute zsh path", "/bin/zsh", "zsh"},
		{"usr local fish", "/usr/local/bin/fish", "fish"},
		{"bash path", "/bin/bash", "bash"},
		{"bare name no slash", "zsh", "zsh"},
		{"unset falls back to bash", "", "bash"},
		{"trailing slash empty basename falls back to bash", "/bin/", "bash"},
		{"root slash only falls back to bash", "/", "bash"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SHELL", tc.shell)
			if got := kiroShellName(); got != tc.want {
				t.Fatalf("kiroShellName() with SHELL=%q = %q, want %q", tc.shell, got, tc.want)
			}
		})
	}
}

// TestKiroRequestShellStateReflectsShellEnv verifies the buildKiroChatRequest
// conversationState.shellState.shellName is populated from $SHELL (via
// kiroShellName) instead of the previous hardcoded "bash", covering the
// Kiro/NKP/NUG-kiro shared request builder.
func TestKiroRequestShellStateReflectsShellEnv(t *testing.T) {
	t.Setenv("SHELL", "/usr/bin/zsh")
	request := buildKiroChatRequest("claude-sonnet-4.5", ChatParams{History: &BuiltHistory{TrailingUserText: "hi"}})
	state, _ := request["conversationState"].(map[string]any)
	shellState, _ := state["shellState"].(map[string]any)
	if got := stringValue(shellState["shellName"]); got != "zsh" {
		t.Fatalf("shellState.shellName = %q, want zsh (from $SHELL=/usr/bin/zsh)", got)
	}

	t.Setenv("SHELL", "")
	request = buildKiroChatRequest("claude-sonnet-4.5", ChatParams{History: &BuiltHistory{TrailingUserText: "hi"}})
	state, _ = request["conversationState"].(map[string]any)
	shellState, _ = state["shellState"].(map[string]any)
	if got := stringValue(shellState["shellName"]); got != "bash" {
		t.Fatalf("shellState.shellName with empty $SHELL = %q, want bash fallback", got)
	}
}
