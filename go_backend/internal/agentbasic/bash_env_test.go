package agentbasic

import (
	"context"
	"strings"
	"testing"
)

// TestBuildMinimalBashEnv verifies the minimal fresh-shell environment keeps the
// essential variables (HOME/PATH) and excludes arbitrary parent-process variables,
// mirroring TS buildMinimalEnv (server/lib/agent/shell.ts).
func TestBuildMinimalBashEnv(t *testing.T) {
	t.Setenv("NARRAFORK_FRESH_PROBE", "leaked-value")
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("HOME", "/home/probe")

	env := BuildMinimalBashEnv()
	joined := strings.Join(env, "\n")

	if !strings.Contains(joined, "HOME=/home/probe") {
		t.Fatalf("minimal env must keep HOME, got: %v", env)
	}
	if !strings.Contains(joined, "PATH=/usr/bin:/bin") {
		t.Fatalf("minimal env must keep PATH, got: %v", env)
	}
	for _, e := range env {
		if strings.HasPrefix(e, "NARRAFORK_FRESH_PROBE=") {
			t.Fatalf("minimal env must NOT carry arbitrary parent vars, got: %v", env)
		}
	}
}

// TestBashToolFreshEnvUsesMinimalEnv verifies that with freshShellEnv enabled the
// Bash tool runs with the minimal environment, so an arbitrary variable set in the
// server process does NOT leak into the command; with freshShellEnv disabled the
// variable is inherited. This proves both env isolation and that the setting is
// actually consumed (mirrors TS bash.ts freshShellEnv branch).
func TestBashToolFreshEnvUsesMinimalEnv(t *testing.T) {
	t.Setenv("NARRAFORK_FRESH_PROBE", "leaked-value")
	cwd := t.TempDir()
	cmd := `{"command":"printf '%s' \"$NARRAFORK_FRESH_PROBE\""}`

	freshCtx := ContextWithToolRuntimeSettings(context.Background(), ToolRuntimeSettings{FreshShellEnv: true})
	freshOut := ExecuteTool(freshCtx, cwd, "Bash", cmd)
	if strings.Contains(freshOut, "leaked-value") {
		t.Fatalf("fresh shell env must not inherit arbitrary parent vars, got: %q", freshOut)
	}

	// Control: default (no fresh env) inherits the server environment.
	defaultOut := ExecuteTool(context.Background(), cwd, "Bash", cmd)
	if !strings.Contains(defaultOut, "leaked-value") {
		t.Fatalf("default Bash should inherit parent env var, got: %q", defaultOut)
	}
}

// TestBashToolDefaultInheritsEnv verifies the default (freshShellEnv=false / no
// runtime settings) path inherits the server environment.
func TestBashToolDefaultInheritsEnv(t *testing.T) {
	t.Setenv("NARRAFORK_INHERIT_PROBE", "inherited")
	cwd := t.TempDir()
	cmd := `{"command":"printf '%s' \"$NARRAFORK_INHERIT_PROBE\""}`

	// No ToolRuntimeSettings in context at all.
	out := ExecuteTool(context.Background(), cwd, "Bash", cmd)
	if !strings.Contains(out, "inherited") {
		t.Fatalf("default Bash (no runtime settings) should inherit parent env, got: %q", out)
	}

	// Explicit FreshShellEnv:false behaves the same.
	falseCtx := ContextWithToolRuntimeSettings(context.Background(), ToolRuntimeSettings{FreshShellEnv: false})
	out2 := ExecuteTool(falseCtx, cwd, "Bash", cmd)
	if !strings.Contains(out2, "inherited") {
		t.Fatalf("FreshShellEnv:false should inherit parent env, got: %q", out2)
	}
}
