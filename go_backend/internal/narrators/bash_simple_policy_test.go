package narrators

import "testing"

// TestBashCommandAllowedBySimplePolicy locks the danger-reflection bash allowlist
// to the TS analyzeShellCommand `nonWhitelisted === []` determination
// (bash-analyze.ts): ordinary read-only command chains (ls/find/sort/tree/diff/…)
// must NOT be flagged as "outside the safety allowlist", while always-ask,
// conditional-danger, and unknown commands still must be. This guards the
// reported regression where `ls && find . | head` falsely triggered danger
// reflection because the old hand-rolled allowlist omitted `find`.
func TestBashCommandAllowedBySimplePolicy(t *testing.T) {
	allowed := []string{
		`ls -la && echo "---" && find . -maxdepth 2 -type d -name "*go*" 2>/dev/null | head -50`,
		`grep -rn foo . | sort | uniq`,
		`cat x.txt | head -20`,
		`tree -L 2`,
		`diff a.txt b.txt`,
		`seq 1 10`,
		`find . -name "*.go" | wc -l`,
	}
	for _, cmd := range allowed {
		if !bashCommandAllowedBySimplePolicy(cmd) {
			t.Errorf("expected read-only command to be allowed (no danger reflection): %q", cmd)
		}
	}

	flagged := []string{
		`npm install left-pad`,    // conditional-danger (write/install subcommand)
		`sed -i s/a/b/ file.txt`,  // conditional-danger (-i in-place edit)
		`rm -rf build`,            // always-ask (deletion)
		`sudo apt-get update`,     // always-ask (privilege escalation)
		`some-unknown-binary --x`, // unknown command
		`FOO=bar ls`,              // env-var assignment prefix fails closed
	}
	for _, cmd := range flagged {
		if bashCommandAllowedBySimplePolicy(cmd) {
			t.Errorf("expected non-allowlisted command to be flagged: %q", cmd)
		}
	}
}
