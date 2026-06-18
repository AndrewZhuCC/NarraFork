package misccompat

import "testing"

// TestSanitizeCustomSubagentDirNameMatchesTS pins sanitizeCustomSubagentDirName to
// the TypeScript sanitizeDirName (server/services/custom-subagent-service.ts):
// trim, lowercase, replace any char outside [a-z0-9\u4e00-\u9fff_-] with "-",
// collapse consecutive dashes, and trim leading/trailing dashes. CJK is preserved.
func TestSanitizeCustomSubagentDirNameMatchesTS(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"ascii words", "My Agent", "my-agent"},
		{"trim and lowercase", "  Code Reviewer  ", "code-reviewer"},
		{"special chars to dash", "a!@#b", "a-b"},
		{"collapse consecutive dashes", "x___y", "x___y"}, // underscore is allowed, kept as-is
		{"collapse dash runs", "a - - b", "a-b"},
		{"leading and trailing trimmed", "--edge--", "edge"},
		{"cjk preserved", "评审 Agent", "评审-agent"},
		{"pure cjk preserved", "评审员", "评审员"},
		{"digits and underscore kept", "agent_v2", "agent_v2"},
		{"all invalid yields empty", "!!!", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sanitizeCustomSubagentDirName(tc.in); got != tc.want {
				t.Fatalf("sanitizeCustomSubagentDirName(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
