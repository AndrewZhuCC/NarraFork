package narrators

import (
	"path/filepath"
	"testing"
)

// TestBlacklistedPathReasonLevelSemantics locks the path-blacklist deny logic
// (parity with the TypeScript findBlacklistedPath): a path inside a blacklist
// rule is denied when the rule is denyAll (any operation) or when the operation
// is a write (non-denyAll rules block writes only, allowing reads). Paths
// outside every rule are never denied. Rules are pre-filtered by `enabled`
// upstream, so this function sees only active rules.
func TestBlacklistedPathReasonLevelSemantics(t *testing.T) {
	cwd := filepath.FromSlash("/work/repo")
	rules := []permissionPathRule{
		{Path: filepath.Join(cwd, "secrets"), Level: "denyAll"},
		{Path: filepath.Join(cwd, "readonly"), Level: "denyWrite"},
	}

	cases := []struct {
		name      string
		path      string
		operation string
		denied    bool
	}{
		// denyAll blocks both read and write.
		{"denyAll read", "secrets/key.pem", "read", true},
		{"denyAll write", "secrets/key.pem", "write", true},
		{"denyAll nested", "secrets/sub/dir/x", "read", true},
		// non-denyAll blocks writes only, allows reads.
		{"denyWrite read allowed", "readonly/data.txt", "read", false},
		{"denyWrite write blocked", "readonly/data.txt", "write", true},
		// path outside all rules -> never denied.
		{"outside read", "src/main.go", "read", false},
		{"outside write", "src/main.go", "write", false},
		// the rule dir itself.
		{"rule dir denyAll", "secrets", "read", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reason := blacklistedPathReason(cwd, []string{tc.path}, rules, tc.operation)
			if (reason != "") != tc.denied {
				t.Fatalf("blacklistedPathReason(%q, op=%s) reason=%q deniedWant=%v", tc.path, tc.operation, reason, tc.denied)
			}
		})
	}

	// Empty rules / empty paths -> never denied.
	if blacklistedPathReason(cwd, []string{"secrets/x"}, nil, "write") != "" {
		t.Fatal("empty rules must not deny")
	}
	if blacklistedPathReason(cwd, nil, rules, "write") != "" {
		t.Fatal("empty paths must not deny")
	}
}
