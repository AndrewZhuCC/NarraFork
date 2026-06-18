package narrators

import (
	"path/filepath"
	"testing"
)

// TestResolveAgentToolPermissionMatrix locks the Agent subagent permission
// decision, parity-aligned with the TypeScript narrator-permission Agent branch:
//   - readOnly mode: only explore/plan subagents are allowed; a workdir outside
//     cwd requires approval (ask); otherwise allow.
//   - other modes with a workdir different from cwd: bypassPermissions->allow,
//     dontAsk->deny, default->ask.
//   - same-dir (or no workdir): allow.
func TestResolveAgentToolPermissionMatrix(t *testing.T) {
	cwd := filepath.FromSlash("/work/repo")
	outside := filepath.FromSlash("/other/dir")
	narr := func(mode string) Narrator { m := mode; return Narrator{PermissionMode: &m} }

	cases := []struct {
		name   string
		mode   string
		args   map[string]any
		action string
	}{
		// readOnly: non-explore/plan denied.
		{"readOnly general denied", "readOnly", map[string]any{"subagent_type": "general"}, "deny"},
		{"readOnly explore allow", "readOnly", map[string]any{"subagent_type": "explore"}, "allow"},
		{"readOnly plan allow", "readOnly", map[string]any{"subagent_type": "plan"}, "allow"},
		{"readOnly explore outside cwd asks", "readOnly", map[string]any{"subagent_type": "explore", "workdir": outside}, "ask"},
		// default mode: different dir -> ask; same dir -> allow.
		{"default different dir asks", "default", map[string]any{"subagent_type": "general", "workdir": outside}, "ask"},
		{"default no workdir allow", "default", map[string]any{"subagent_type": "general"}, "allow"},
		{"default same dir allow", "default", map[string]any{"subagent_type": "general", "workdir": cwd}, "allow"},
		// bypass / dontAsk for a different dir.
		{"bypass different dir allow", "bypassPermissions", map[string]any{"subagent_type": "general", "workdir": outside}, "allow"},
		{"dontAsk different dir deny", "dontAsk", map[string]any{"subagent_type": "general", "workdir": outside}, "deny"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveAgentToolPermission(cwd, narr(tc.mode), tc.args, nil)
			if got.Action != tc.action {
				t.Fatalf("mode=%s args=%v action=%q want %q (reason=%q)", tc.mode, tc.args, got.Action, tc.action, got.Reason)
			}
		})
	}
}

// TestBlacklistedAgentWorkdirReason locks the extra blacklist layer on Agent
// workdir: a workdir inside a denyAll rule is blocked for any subagent; a
// non-denyAll rule blocks write-capable (non explore/plan) subagents but allows
// read-only explore/plan subagents.
func TestBlacklistedAgentWorkdirReason(t *testing.T) {
	cwd := filepath.FromSlash("/work/repo")
	rules := []permissionPathRule{
		{Path: filepath.FromSlash("/secret"), Level: "denyAll"},
		{Path: filepath.FromSlash("/ro"), Level: "denyWrite"},
	}
	denyAllWd := filepath.FromSlash("/secret/sub")
	roWd := filepath.FromSlash("/ro/sub")
	cleanWd := filepath.FromSlash("/work/repo/wt")

	if blacklistedAgentWorkdirReason(cwd, denyAllWd, "explore", rules) == "" {
		t.Fatal("denyAll workdir must block even explore subagent")
	}
	if blacklistedAgentWorkdirReason(cwd, denyAllWd, "general", rules) == "" {
		t.Fatal("denyAll workdir must block general subagent")
	}
	if blacklistedAgentWorkdirReason(cwd, roWd, "general", rules) == "" {
		t.Fatal("denyWrite workdir must block write-capable general subagent")
	}
	if blacklistedAgentWorkdirReason(cwd, roWd, "explore", rules) != "" {
		t.Fatal("denyWrite workdir must allow read-only explore subagent")
	}
	if blacklistedAgentWorkdirReason(cwd, cleanWd, "general", rules) != "" {
		t.Fatal("workdir outside all rules must not be blocked")
	}
	if blacklistedAgentWorkdirReason(cwd, denyAllWd, "general", nil) != "" {
		t.Fatal("empty rules must not block")
	}
}
