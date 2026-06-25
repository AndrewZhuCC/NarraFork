package narrators

import (
	"context"
	"encoding/json"
	"testing"
)

// TestFindConditionalDanger locks the find recursive detector, mirroring TS
// CONDITIONAL_COMMANDS.find (bash-analyze.ts:738). Discriminative unit test.
func TestFindConditionalDanger(t *testing.T) {
	danger := []string{
		"find . -delete",
		"find . -name x -exec rm {} ;",          // rm in ALWAYS_ASK
		"find . -exec git push --force ;",       // git CONDITIONAL recursion danger
		"find . -exec sudo make ;",              // sudo in ALWAYS_ASK
		"find . -exec unknowncmd {} ;",          // unknown command
		"find . -exec npx create-react-app x ;", // npx recursion danger
		"find . -exec",                          // empty command after flag
	}
	for _, c := range danger {
		if !findConditionalDanger(splitFirst(c), 0) {
			t.Fatalf("expected find danger for %q", c)
		}
	}

	safe := []string{
		"find . -type f",                // no -delete/-exec
		"find . -name x -exec cat {} ;", // cat in SAFE
		"find . -exec biome check ;",    // biome in SAFE
		"find . -exec grep foo {} ;",    // grep in SAFE
		"find . -exec git status ;",     // git CONDITIONAL recursion safe -> SAFE
	}
	for _, c := range safe {
		if findConditionalDanger(splitFirst(c), 0) {
			t.Fatalf("did not expect find danger for %q", c)
		}
	}
}

// TestXargsConditionalDanger locks the xargs recursive detector, mirroring TS
// CONDITIONAL_COMMANDS.xargs (bash-analyze.ts:788). Discriminative unit test.
func TestXargsConditionalDanger(t *testing.T) {
	danger := []string{
		"xargs rm",
		"xargs git push --force",
		"xargs -0 -n1 rm", // skip xargs flags then rm
		"xargs sudo make",
		"xargs unknowncmd",
		"xargs npx create-react-app x",
	}
	for _, c := range danger {
		if !xargsConditionalDanger(splitFirst(c), 0) {
			t.Fatalf("expected xargs danger for %q", c)
		}
	}

	safe := []string{
		"xargs", // empty -> default echo
		"xargs cat",
		"xargs ls",
		"xargs -0 cat",     // skip flag then cat
		"xargs git status", // git CONDITIONAL recursion safe
		"xargs biome check",
	}
	for _, c := range safe {
		if xargsConditionalDanger(splitFirst(c), 0) {
			t.Fatalf("did not expect xargs danger for %q", c)
		}
	}
}

// TestBashCommandHasConditionalDangerFindXargsTopLevel verifies find/xargs feed
// the top-level conditional-danger gate, including later sub-commands and
// pipe-split xargs.
func TestBashCommandHasConditionalDangerFindXargsTopLevel(t *testing.T) {
	danger := []string{
		"find . -delete",
		"ls && find . -delete", // later sub-command
		"echo x | xargs rm",    // pipe-split: `xargs rm` is its own sub-command
		"xargs git push --force",
	}
	for _, c := range danger {
		if !bashCommandHasConditionalDanger(c) {
			t.Fatalf("expected top-level conditional danger for %q", c)
		}
	}
	safe := []string{
		"find . -type f",
		"echo x | xargs cat",
		"ls -la",
	}
	for _, c := range safe {
		if bashCommandHasConditionalDanger(c) {
			t.Fatalf("did not expect top-level conditional danger for %q", c)
		}
	}
}

// TestResolveToolPermissionFindXargsDangerBlocksPathWhitelistAutoAllow verifies
// the path-whitelist auto-allow is blocked for dangerous find/xargs while safe
// forms still auto-allow.
func TestResolveToolPermissionFindXargsDangerBlocksPathWhitelistAutoAllow(t *testing.T) {
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

	// (a) dangerous find + default: without the gate the cwd path whitelist
	// auto-allows; with the gate it asks.
	if got := resolve(t, "default", "find . -name x -exec rm {} ;"); got.Action != "ask" {
		t.Fatalf("find -exec rm default = %s (%s), want ask", got.Action, got.Reason)
	}
	// (b) dangerous xargs + readOnly -> deny.
	if got := resolve(t, "readOnly", "xargs git push --force"); got.Action != "deny" {
		t.Fatalf("xargs danger readOnly = %s (%s), want deny", got.Action, got.Reason)
	}
	// Controls: safe find/xargs still auto-allow.
	if got := resolve(t, "default", "find . -type f"); got.Action != "allow" {
		t.Fatalf("safe find = %s (%s), want allow", got.Action, got.Reason)
	}
	if got := resolve(t, "default", "xargs cat"); got.Action != "allow" {
		t.Fatalf("safe xargs = %s (%s), want allow", got.Action, got.Reason)
	}
}

// splitFirst returns the tokens of the first sub-command of command.
func splitFirst(command string) []string {
	cmds := splitShellCommands(command)
	if len(cmds) == 0 {
		return nil
	}
	return cmds[0]
}
