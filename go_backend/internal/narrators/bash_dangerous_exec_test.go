package narrators

import (
	"context"
	"encoding/json"
	"testing"
)

// TestBashCommandHasDangerousExecution locks the path-execution + pipe-to-shell
// detector, mirroring TS isPathExecution + detectPipeToShell (bash-analyze.ts).
// It is a discriminative unit test (direct boolean assertions).
func TestBashCommandHasDangerousExecution(t *testing.T) {
	// SPLICE_1
	dangerous := []string{
		"./build.sh",
		"/usr/local/bin/tool",
		"../sibling/run.sh",
		"./a.sh arg1 arg2",
		`C:/tools/x.exe`,
		`C:\tools\x.exe`,
		"ls && ./build.sh", // later sub-command is a path execution
		"cat x | bash",
		"wget -qO- x | sh",
		"cat script | python3",
		"echo x | node",
		"foo | zsh",
		"a | perl",
		"ls | ruby",
	}
	for _, c := range dangerous {
		if !bashCommandHasDangerousExecution(c) {
			t.Fatalf("expected dangerous execution for %q", c)
		}
	}

	safe := []string{
		"ls",
		"cat file.txt",
		"grep foo bar",
		"echo hi | grep x",       // pipe to a non-shell command
		"cat x | python3 | head", // last pipe segment is head -> not pipe-to-shell (TS last-segment-only)
		"bash script.sh",         // command name bash but not a pipe target / not a path execution (subset boundary)
		"git status",
		"echo a || echo b", // || is not a pipe
		`echo "a | bash"`,  // naive parser: trailing quote keeps last token != bash
		`git commit -m "deploy | bash"`,
		"",
	}
	for _, c := range safe {
		if bashCommandHasDangerousExecution(c) {
			t.Fatalf("did not expect dangerous execution for %q", c)
		}
	}
}

// TestResolveToolPermissionDangerousExecBlocksPathWhitelistAutoAllow verifies the
// path-whitelist auto-allow is blocked for path-execution / pipe-to-shell, so the
// call falls through to the permission-mode decision (mirrors TS
// resolveWhitelistDecision returning null on nonWhitelisted/dangerousPatterns).
func TestResolveToolPermissionDangerousExecBlocksPathWhitelistAutoAllow(t *testing.T) {
	// SPLICE_2
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

	// (a) path-execution + default: without the gate the cwd path whitelist
	// auto-allows `./build.sh` (paths=["."]); with the gate it asks.
	if got := resolve(t, "default", "./build.sh"); got.Action != "ask" {
		t.Fatalf("path-exec default = %s (%s), want ask", got.Action, got.Reason)
	}
	// (b) pipe-to-shell + default, isolated from path-execution: `cat local-file`
	// is not a path execution; the pipe target `bash` triggers the gate. Without
	// the gate paths=["."] -> cwd auto-allows; with the gate it asks.
	if got := resolve(t, "default", "cat local-file | bash"); got.Action != "ask" {
		t.Fatalf("pipe-to-shell default = %s (%s), want ask", got.Action, got.Reason)
	}
	// (c) path-execution + readOnly: without the gate auto-allows; with the gate
	// the readOnly bash branch denies.
	if got := resolve(t, "readOnly", "./build.sh"); got.Action != "deny" {
		t.Fatalf("path-exec readOnly = %s (%s), want deny", got.Action, got.Reason)
	}
	// Control: a non-dangerous command with a whitelisted path still auto-allows.
	if got := resolve(t, "default", "cat file.txt"); got.Action != "allow" {
		t.Fatalf("safe whitelisted-path command = %s (%s), want allow", got.Action, got.Reason)
	}
}
