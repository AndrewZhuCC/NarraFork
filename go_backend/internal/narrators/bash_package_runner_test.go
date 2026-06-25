package narrators

import (
	"context"
	"encoding/json"
	"testing"
)

// TestPackageRunnerHasDanger locks the npx/bunx classifyPackageRunner danger
// detector, mirroring TS classifyPackageRunner error!=null (bash-analyze.ts:433).
// Discriminative unit test over splitShellCommands tokens.
func TestPackageRunnerHasDanger(t *testing.T) {
	tok := func(command string) []string {
		var tokens []string
		for _, toks := range splitShellCommands(command) {
			tokens = toks
		}
		return tokens
	}

	danger := []string{
		"npx create-react-app myapp", // package not in allowlist
		"npx -p foo bar",             // dangerous flag
		"npx --package x y",
		"npx --registry http bar pkg",
		"npx some-pkg@latest", // unstable tag
		"npx pkg@next",
		"npx git+https://x",          // remote source (git+)
		"npx file:./x",               // remote source (file:)
		"npx",                        // no explicit command
		"npx biome --config x check", // dynamic code loading flag
		"npx biome --plugin p check", // dynamic code loading flag
		"npx eslint --frobnicate",    // flag not in allowlist
		"npx prettier badsubcmd",     // non-flag arg, prettier is not biome/tsc
		"npx prettier --write .",     // --write not in SAFE_PACKAGE_ARGS; prettier is not biome/tsc
		"npx tsc --frobnicate",       // unknown tsc flag
		"bunx create-x app",          // bunx symmetry
	}
	for _, c := range danger {
		if !packageRunnerHasDanger(tok(c)) {
			t.Fatalf("expected package-runner danger for %q", c)
		}
	}

	safe := []string{
		"npx biome check .",
		"npx biome check --write .", // biome write arg
		"npx biome check --write=false",
		"npx biome check --max-diagnostics=200", // biome safe prefix
		"npx biome somesubcmd",                  // biome accepts non-flag as path target
		"npx prettier --check .",
		"npx eslint .",
		"npx tsc --noEmit",
		"npx tsc --outDir dist", // tsc write arg
		"npx tsc --strict",
		"npx @biomejs/biome check .",       // scoped package
		"npx @biomejs/biome@1.2.3 check .", // scoped package with version
		"bunx biome check .",
	}
	for _, c := range safe {
		if packageRunnerHasDanger(tok(c)) {
			t.Fatalf("did not expect package-runner danger for %q", c)
		}
	}
}

// TestResolveToolPermissionPackageRunnerDangerBlocksPathWhitelistAutoAllow
// verifies the path-whitelist auto-allow is blocked for dangerous npx/bunx
// invocations, while allowlisted safe packages still auto-allow.
func TestResolveToolPermissionPackageRunnerDangerBlocksPathWhitelistAutoAllow(t *testing.T) {
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

	// (a) dangerous package runner + default: without the gate the cwd path
	// whitelist auto-allows; with the gate it asks.
	if got := resolve(t, "default", "npx create-react-app myapp"); got.Action != "ask" {
		t.Fatalf("package-runner danger default = %s (%s), want ask", got.Action, got.Reason)
	}
	// (b) dangerous package runner + readOnly -> deny.
	if got := resolve(t, "readOnly", "npx -p foo bar"); got.Action != "deny" {
		t.Fatalf("package-runner danger readOnly = %s (%s), want deny", got.Action, got.Reason)
	}
	// Control: an allowlisted safe package still auto-allows.
	if got := resolve(t, "default", "npx biome check ."); got.Action != "allow" {
		t.Fatalf("allowlisted safe package = %s (%s), want allow", got.Action, got.Reason)
	}
}
