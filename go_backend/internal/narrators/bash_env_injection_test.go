package narrators

import (
	"context"
	"path/filepath"
	"testing"
)

// TestBashCommandHasEnvInjection locks the command-prefix dangerous
// environment-variable detection, mirroring TS detectEnvInjection
// (bash-analyze.ts:1147 + DANGEROUS_ENV_VARS): only a command-prefix assignment
// of a dangerous variable counts; bare assignments, `export`-style assignments,
// non-dangerous variables, and dangerous strings passed as arguments do not.
func TestBashCommandHasEnvInjection(t *testing.T) {
	injected := []string{
		"LD_PRELOAD=/tmp/x.so ls",
		"PYTHONPATH=/x python3 script.py",
		"NODE_OPTIONS=--inspect node app.js",
		"A=1 LD_LIBRARY_PATH=/x cmd",                  // dangerous var not in first position
		"ls && DYLD_INSERT_LIBRARIES=/x otool -L bin", // injection in a later sub-command
		"BASH_ENV=/x sh",
		"ENV=/x sh -c true",
		"PROMPT_COMMAND=evil bash",
		"DYLD_LIBRARY_PATH=/x cmd",
		"NODE_PATH=/x node app.js",
		"PERL5LIB=/x perl s.pl",
		"RUBYLIB=/x ruby s.rb",
		"CLASSPATH=/x java Main",
	}
	for _, c := range injected {
		if !bashCommandHasEnvInjection(c) {
			t.Fatalf("expected env injection for %q", c)
		}
	}

	clean := []string{
		"ls",
		"FOO=bar ls",           // non-dangerous variable prefix
		"LD_PRELOAD=/tmp/x.so", // bare assignment, no following command
		"export LD_PRELOAD=/x", // export command, assignment is an argument
		"echo LD_PRELOAD=x",    // dangerous string as an echo argument
		`git commit -m "LD_PRELOAD=x"`,
		"A=1 B=2 ls", // only non-dangerous prefixes
		"",
	}
	for _, c := range clean {
		if bashCommandHasEnvInjection(c) {
			t.Fatalf("did not expect env injection for %q", c)
		}
	}
}

// TestEnvAssignmentName verifies the POSIX-name assignment recognizer used by
// the env-injection detector: NAME=value with a valid name returns the name;
// invalid names, missing '=', or empty names are rejected.
func TestEnvAssignmentName(t *testing.T) {
	cases := []struct {
		token    string
		wantName string
		wantOK   bool
	}{
		{"LD_PRELOAD=/x", "LD_PRELOAD", true},
		{"A=1", "A", true},
		{"_X=1", "_X", true},
		{"FOO9=bar", "FOO9", true},
		{"=value", "", false},    // empty name
		{"9FOO=1", "", false},    // name cannot start with a digit
		{"ls", "", false},        // no '='
		{"a-b=1", "", false},     // invalid char in name
		{"path/to=1", "", false}, // invalid char in name
	}
	for _, tc := range cases {
		name, ok := envAssignmentName(tc.token)
		if ok != tc.wantOK || name != tc.wantName {
			t.Fatalf("envAssignmentName(%q)=(%q,%v) want (%q,%v)", tc.token, name, ok, tc.wantName, tc.wantOK)
		}
	}
}

// TestResolveToolPermissionEnvInjectionBlocksPathWhitelistAutoAllow is the
// integration guard: with a path whitelist covering a cwd subdirectory, a clean
// command reading a whitelisted file auto-allows, but the same command prefixed
// by a dangerous env var must NOT auto-allow — it falls through to the
// permission-mode decision (default -> ask), mirroring TS hasEnvInjection.
func TestResolveToolPermissionEnvInjectionBlocksPathWhitelistAutoAllow(t *testing.T) {
	service := newPermissionTestService(t)
	cwd := t.TempDir()
	narrator := permissionTestNarrator(cwd, "default")
	allowedDir := filepath.Join(cwd, "sub")
	if _, err := service.DB.Exec(`INSERT INTO narrator_whitelist_dirs (id, narrator_id, path, access_level, enabled, created_at) VALUES ('wl', ?, ?, 'readWrite', 1, 'now')`, narrator.ID, allowedDir); err != nil {
		t.Fatalf("insert whitelist dir: %v", err)
	}

	// Clean read of a whitelisted path auto-allows.
	clean, err := service.resolveToolPermission(context.Background(), narrator, cwd, permissionToolCall("Bash", `{"command":"cat sub/file.txt"}`))
	if err != nil {
		t.Fatalf("resolve clean command: %v", err)
	}
	if clean.Action != "allow" {
		t.Fatalf("clean whitelisted-path command = %s (%s), want allow", clean.Action, clean.Reason)
	}

	// The same command prefixed by a dangerous env var must not auto-allow.
	injected, err := service.resolveToolPermission(context.Background(), narrator, cwd, permissionToolCall("Bash", `{"command":"LD_PRELOAD=/tmp/evil.so cat sub/file.txt"}`))
	if err != nil {
		t.Fatalf("resolve env-injected command: %v", err)
	}
	if injected.Action != "ask" {
		t.Fatalf("env-injected whitelisted-path command = %s (%s), want ask", injected.Action, injected.Reason)
	}
}

// TestResolveToolPermissionEnvInjectionBlocksCommandWhitelistAutoAllow verifies
// the same guard on the command-whitelist auto-allow path. With a broad `*`
// command whitelist in acceptEdits mode, a clean command is auto-allowed by the
// whitelist decision, but the env-prefixed variant must skip the whitelist
// auto-allow and fall through to the acceptEdits mode branch, which asks for a
// command that is not allowed by the simple safe-command policy (mirrors TS,
// where hasEnvInjection blocks the whitelist decision and the acceptEdits
// branch returns ask at narrator-permission.ts:490 before the allow at 496).
func TestResolveToolPermissionEnvInjectionBlocksCommandWhitelistAutoAllow(t *testing.T) {
	service := newPermissionTestService(t)
	cwd := t.TempDir()
	narrator := permissionTestNarrator(cwd, "acceptEdits")
	if _, err := service.DB.Exec(`INSERT INTO narrator_whitelist_cmds (id, narrator_id, pattern, enabled, created_at) VALUES ('cw', ?, '*', 1, 'now')`, narrator.ID); err != nil {
		t.Fatalf("insert command whitelist: %v", err)
	}

	// A `*`-whitelisted command in acceptEdits mode auto-allows via the
	// command-whitelist decision.
	covered, err := service.resolveToolPermission(context.Background(), narrator, cwd, permissionToolCall("Bash", `{"command":"some-tool --flag"}`))
	if err != nil {
		t.Fatalf("resolve whitelisted command: %v", err)
	}
	if covered.Action != "allow" {
		t.Fatalf("whitelisted command (acceptEdits) = %s (%s), want allow", covered.Action, covered.Reason)
	}

	// The env-prefixed variant must NOT be auto-allowed by the whitelist; it
	// falls through to the acceptEdits branch and asks (the command is not in
	// the simple safe-command allowlist).
	injected, err := service.resolveToolPermission(context.Background(), narrator, cwd, permissionToolCall("Bash", `{"command":"LD_PRELOAD=/tmp/evil.so some-tool --flag"}`))
	if err != nil {
		t.Fatalf("resolve env-injected whitelisted command: %v", err)
	}
	if injected.Action != "ask" {
		t.Fatalf("env-injected whitelisted command = %s (%s), want ask", injected.Action, injected.Reason)
	}
}
