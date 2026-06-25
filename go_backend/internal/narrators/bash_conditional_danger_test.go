package narrators

import (
	"context"
	"encoding/json"
	"testing"
)

// TestBashCommandHasConditionalDanger locks the CONDITIONAL_COMMANDS dangerous-
// argument detectors, mirroring TS CONDITIONAL_COMMANDS (bash-analyze.ts:558) for
// git/npm/bun/yarn/pnpm/sed/tar/tee/awk/gawk. Discriminative unit test.
func TestBashCommandHasConditionalDanger(t *testing.T) {
	danger := []string{
		// git
		"git push --force", "git push -f", "git push origin --delete x",
		"git reset --hard", "git clean -fd", "git checkout -- file",
		"git rebase main", "git merge x", "git filter-branch --all",
		"git reflog expire", "git gc --prune=now", "git branch -D x",
		"git submodule deinit x", "git worktree remove x",
		// npm
		"npm run build", "npm ci", "npm install", "npm test", "npm exec foo",
		"npm publish", "npm cache clean --force", "npm frobnicate",
		// bun
		"bun run x", `bun -e "x"`, "bun install", "bun build", "bun frob",
		// yarn
		"yarn add x", "yarn run b", "yarn install", "yarn frob",
		// pnpm
		"pnpm install", "pnpm run b", "pnpm add x", "pnpm frob",
		// sed
		"sed -i s/a/b/ f", "sed -ibak s/a/b/ f",
		// tar
		"tar -xf a.tar", "tar xzf a.tgz", "tar --extract -f a",
		// tee
		"tee out.txt", "tee -a log",
		// awk/gawk
		`awk 'BEGIN{system("id")}'`, "gawk '{x | getline}'",
		// later sub-command
		"ls && git push --force",
	}
	for _, c := range danger {
		if !bashCommandHasConditionalDanger(c) {
			t.Fatalf("expected conditional danger for %q", c)
		}
	}

	safe := []string{
		// git read-only / safe
		"git status", "git log", "git add .", "git commit -m x", "git push",
		"git checkout main", "git branch -a",
		// package managers read-only / bare
		"npm ls", "npm list", "npm view x", "npm outdated", "npm", "npm audit",
		"bun", "bun --version", "bun pm ls",
		"yarn list", "yarn", "yarn why x",
		"pnpm list", "pnpm", "pnpm why x",
		// sed/tar non-dangerous
		"sed s/a/b/ f", "sed -n 1p f", "tar -czf a.tgz dir", "tar tf a",
		// awk non-dangerous
		"awk '{print $1}' f",
		// per-statement guard: system( appears only in a non-awk statement
		`echo x; echo "system("`,
		// always-safe / non-conditional
		"ls", "cat f", "cp a b", "curl x", "mkdir d", "diff a b", "unzip a", "",
	}
	for _, c := range safe {
		if bashCommandHasConditionalDanger(c) {
			t.Fatalf("did not expect conditional danger for %q", c)
		}
	}
}

// TestResolveToolPermissionConditionalDangerBlocksPathWhitelistAutoAllow verifies
// the path-whitelist auto-allow is blocked for dangerous CONDITIONAL commands,
// falling through to the permission-mode decision (mirrors TS
// resolveWhitelistDecision returning null on dangerousPatterns).
func TestResolveToolPermissionConditionalDangerBlocksPathWhitelistAutoAllow(t *testing.T) {
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

	// (a) conditional danger + default: without the gate the cwd path whitelist
	// auto-allows `npm run build` (paths=["."]); with the gate it asks.
	if got := resolve(t, "default", "npm run build"); got.Action != "ask" {
		t.Fatalf("conditional danger default = %s (%s), want ask", got.Action, got.Reason)
	}
	// (b) conditional danger + readOnly: without the gate auto-allows; with the
	// gate the readOnly bash branch denies.
	if got := resolve(t, "readOnly", "git push --force"); got.Action != "deny" {
		t.Fatalf("conditional danger readOnly = %s (%s), want deny", got.Action, got.Reason)
	}
	// (c) conditional danger + default, stable paths=["."]: `tar -xf a.tar`.
	if got := resolve(t, "default", "tar -xf a.tar"); got.Action != "ask" {
		t.Fatalf("tar extract default = %s (%s), want ask", got.Action, got.Reason)
	}
	// Control: a read-only conditional subcommand still auto-allows.
	if got := resolve(t, "default", "npm ls"); got.Action != "allow" {
		t.Fatalf("npm ls (read-only) = %s (%s), want allow", got.Action, got.Reason)
	}
}
