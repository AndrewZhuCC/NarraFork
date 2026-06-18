package narrators

import "testing"

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
	}
	for _, cmd := range denied {
		if reason := catastrophicBashCommandDeny(cmd); reason == "" {
			t.Fatalf("expected catastrophic deny for %q, got allow", cmd)
		}
	}

	allowed := []string{
		"rm -rf build",          // local relative dir -> not catastrophic
		"rm -rf ./node_modules", // local relative dir
		"rm file.txt",           // non-recursive
		"rm -rf /dev/null",      // /dev/null is not a block device target here... actually rm on /dev/null
		"dd if=a of=out.img",    // local output, not a device/critical
		"chmod 644 file.txt",    // non-recursive
		"chmod -R 755 ./src",    // recursive but local path
		"ls -la /etc",           // read-only
		"echo hello",            // harmless
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
