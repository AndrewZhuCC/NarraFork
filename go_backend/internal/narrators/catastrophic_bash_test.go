package narrators

import (
	"context"
	"encoding/json"
	"testing"
)

// TestCatastrophicBashCommandDenyBlocksDestructiveSystemCommands locks the
// hard-deny gate for catastrophic shell commands (parity-aligned with the
// TypeScript bash-analyze CATASTROPHIC_PATHS / fork-bomb detection): fork bombs,
// `rm -rf` on system-critical paths, `dd` writing to block devices/critical
// targets, and recursive chmod/chown on critical paths must be denied. Local
// relative-path destructive commands must NOT be hard-denied here (they are
// handled by the softer danger-reflection confirmation gate, not this deny).
func TestCatastrophicBashCommandDenyBlocksDestructiveSystemCommands(t *testing.T) {
	denied := []string{
		":(){ :|:& };:",               // classic fork bomb
		"rm -rf /",                    // wipe root
		"rm -rf /etc",                 // wipe /etc
		"rm -rf /*",                   // wipe root glob
		"rm -rf ~",                    // wipe home
		"rm -rf $HOME",                // wipe home via env
		"rm -fr /usr",                 // flag order variant
		"rm --recursive --force /var", // long flags
		"dd if=/dev/zero of=/dev/sda", // overwrite block device
		"chmod -R 777 /",              // recursive perms on root
		"chown -R root /etc",          // recursive owner on /etc
		"mkfs /dev/sda1",              // filesystem format
		"mkfs.ext4 /dev/sdb1",         // filesystem format (typed)
		"shutdown -h now",             // system power control
		"reboot",                      // system power control
		"halt",                        // system power control
		"poweroff",                    // system power control
		"echo x > /dev/sda",           // redirect to block device (spaced)
		"echo x >/dev/nvme0n1",        // redirect to block device (attached)
		"rm /",                        // non-recursive wipe root
		"rm /*",                       // non-recursive wipe root glob
		"true && mkfs.xfs /dev/sdc",   // catastrophic in a later sub-command
	}
	for _, cmd := range denied {
		if reason := catastrophicBashCommandDeny(cmd); reason == "" {
			t.Fatalf("expected catastrophic deny for %q, got allow", cmd)
		}
	}

	allowed := []string{
		"rm -rf build",            // local relative dir -> not catastrophic
		"rm -rf ./node_modules",   // local relative dir
		"rm file.txt",             // non-recursive
		"rm -rf /dev/null",        // /dev/null is not a block device target here... actually rm on /dev/null
		"dd if=a of=out.img",      // local output, not a device/critical
		"chmod 644 file.txt",      // non-recursive
		"chmod -R 755 ./src",      // recursive but local path
		"ls -la /etc",             // read-only
		"echo hello",              // harmless
		"echo x > out.txt",        // redirect to local file
		"echo x > /dev/null",      // /dev/null is not a block device
		"rm /tmp/foo",             // non-recursive rm of a sub-path (not / or /*)
		"mkfsuffix --help",        // not the mkfs command (no dot/exact)
		"git shutdown-helper run", // "shutdown" only as a subcommand arg, not command name
	}
	for _, cmd := range allowed {
		if reason := catastrophicBashCommandDeny(cmd); reason != "" {
			t.Fatalf("did not expect catastrophic deny for %q, got: %s", cmd, reason)
		}
	}
}

// TestCatastrophicShellTargetClassification locks the critical-target predicate:
// system paths, root glob, home shortcuts, and Windows drive roots are
// catastrophic; ordinary relative/local paths are not.
func TestCatastrophicShellTargetClassification(t *testing.T) {
	yes := []string{"/", "/etc", "/usr", "/var", "/*", "~", "$HOME", "${HOME}", "c:", "c:/", "C:/Windows", "d:/program files"}
	no := []string{"build", "./src", "../sibling", "node_modules", "out.txt", "a/b/c"}
	for _, tgt := range yes {
		if !isCatastrophicShellTarget(tgt) {
			t.Fatalf("isCatastrophicShellTarget(%q)=false want true", tgt)
		}
	}
	for _, tgt := range no {
		if isCatastrophicShellTarget(tgt) {
			t.Fatalf("isCatastrophicShellTarget(%q)=true want false", tgt)
		}
	}
}

// TestResolveToolPermissionCatastrophicIsFatal pins the W785 fatal-deny contract:
// a catastrophic bash command must resolve to a deny that is ALSO marked Fatal so
// the agent loop terminates the whole narrator for safety (mirrors TS
// isCatastrophic -> "fatal" decision). Regular denies (command blacklist,
// readOnly, etc.) must remain non-fatal so the loop can continue. This guards
// against regressing the catastrophic path back to an ordinary per-call deny.
func TestResolveToolPermissionCatastrophicIsFatal(t *testing.T) {
	resolveCmd := func(t *testing.T, mode, command string) toolPermissionDecision {
		t.Helper()
		service := newPermissionTestService(t)
		cwd := t.TempDir()
		narrator := permissionTestNarrator(cwd, mode)
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

	// Catastrophic command: denied AND fatal. Use bypassPermissions to prove the
	// catastrophic hard-deny fires before any permission-mode auto-allow.
	if got := resolveCmd(t, "bypassPermissions", "rm -rf /"); got.Action != "deny" || !got.Fatal {
		t.Fatalf("catastrophic `rm -rf /` = action=%s fatal=%v, want deny+fatal", got.Action, got.Fatal)
	}
	if got := resolveCmd(t, "default", "mkfs.ext4 /dev/sdb1"); got.Action != "deny" || !got.Fatal {
		t.Fatalf("catastrophic `mkfs.ext4` = action=%s fatal=%v, want deny+fatal", got.Action, got.Fatal)
	}

	// Control: a command-blacklist deny is a regular deny — NOT fatal — so the
	// loop keeps going after the blocked call.
	blacklisted := func(t *testing.T, command string) toolPermissionDecision {
		t.Helper()
		service := newPermissionTestService(t)
		cwd := t.TempDir()
		narrator := permissionTestNarrator(cwd, "default")
		if _, err := service.DB.Exec(`INSERT INTO narrator_blacklist_cmds (id, narrator_id, pattern, deny_prompt, enabled, created_at) VALUES ('bl', ?, 'curl', '', 1, 'now')`, narrator.ID); err != nil {
			t.Fatalf("insert blacklist cmd: %v", err)
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
	if got := blacklisted(t, "curl http://example.com"); got.Action != "deny" || got.Fatal {
		t.Fatalf("blacklisted `curl` = action=%s fatal=%v, want deny+non-fatal", got.Action, got.Fatal)
	}
}
