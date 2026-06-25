package narrators

import (
	"context"
	"encoding/json"
	"testing"
)

// TestBashCommandHasAlwaysAskCommand locks the ALWAYS_ASK command-name detector,
// mirroring TS ALWAYS_ASK_COMMANDS (bash-analyze.ts:163). Discriminative unit
// test (direct boolean assertions).
func TestBashCommandHasAlwaysAskCommand(t *testing.T) {
	alwaysAsk := []string{
		"sudo rm x",
		"kill 123",
		"python3 script.py",
		"node app.js",
		"make build",
		"ssh host",
		"scp a b",
		"rsync a b",
		"podman run x",
		"kubectl get pods",
		"systemctl restart x",
		"mount /dev/x /mnt",
		"dd if=a of=b",
		`eval "x"`,
		"exec foo",
		"env FOO=1 cmd",
		"source script.sh",
		". ./env.sh",
		"crontab -e",
		"perl s.pl",
		"go build ./...",
		"pip install x",
		"ls && sudo make", // later sub-command is always-ask
	}
	for _, c := range alwaysAsk {
		if !bashCommandHasAlwaysAskCommand(c) {
			t.Fatalf("expected always-ask for %q", c)
		}
	}

	notAlwaysAsk := []string{
		"ls",
		"cat file.txt",
		"grep foo bar",
		"git status",
		"echo hi",
		"touch note.txt", // CONDITIONAL/SAFE, not ALWAYS_ASK
		"npm ls",         // CONDITIONAL
		"find . -name x", // CONDITIONAL
		"",
	}
	for _, c := range notAlwaysAsk {
		if bashCommandHasAlwaysAskCommand(c) {
			t.Fatalf("did not expect always-ask for %q", c)
		}
	}
}

// TestResolveToolPermissionAlwaysAskBlocksPathWhitelistAutoAllow verifies the
// path-whitelist auto-allow is blocked for ALWAYS_ASK commands, falling through
// to the permission-mode decision (mirrors TS resolveWhitelistDecision returning
// null on nonWhitelisted).
func TestResolveToolPermissionAlwaysAskBlocksPathWhitelistAutoAllow(t *testing.T) {
	resolve := func(t *testing.T, mode, command string) toolPermissionDecision {
		t.Helper()
		service := newPermissionTestService(t)
		cwd := t.TempDir()
		narrator := permissionTestNarrator(cwd, mode)
		if _, err := service.DB.Exec(`INSERT INTO narrator_whitelist_dirs (id, narrator_id, path, access_level, enabled, created_at) VALUES ('wl', ?, ?, 'readWrite', 1, 'now')`, narrator.ID, cwd); err != nil {
			t.Fatalf("insert whitelist dir: %v", err)
		}
		cmdJSON, err := json.Marshal(map[string]string{"command": command})
		if err != nil {
			t.Fatalf("marshal command: %v", err)
		}
		got, err := service.resolveToolPermission(context.Background(), narrator, cwd, permissionToolCall("Bash", string(cmdJSON)))
		if err != nil {
			t.Fatalf("resolve %q: %v", command, err)
		}
		return got
	}

	// (a) always-ask + default: without the gate the cwd path whitelist
	// auto-allows `make build` (paths=["."]); with the gate it asks.
	if got := resolve(t, "default", "make build"); got.Action != "ask" {
		t.Fatalf("always-ask default = %s (%s), want ask", got.Action, got.Reason)
	}
	// (b) always-ask + readOnly: without the gate auto-allows; with the gate the
	// readOnly bash branch denies (kill is not safe-for-readonly).
	if got := resolve(t, "readOnly", "kill 123"); got.Action != "deny" {
		t.Fatalf("always-ask readOnly = %s (%s), want deny", got.Action, got.Reason)
	}
	// Control: a non-always-ask command with a whitelisted path still auto-allows.
	if got := resolve(t, "default", "cat file.txt"); got.Action != "allow" {
		t.Fatalf("safe whitelisted-path command = %s (%s), want allow", got.Action, got.Reason)
	}
}
