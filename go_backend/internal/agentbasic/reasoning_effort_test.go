package agentbasic

import "testing"

// TestNormalizeReasoningEffortMatchesTS pins normalizeReasoningEffort to the TS
// ReasoningEffort enum (server/lib/agent/types.ts):
// "none" | "low" | "medium" | "high" | "xhigh" | "max". Any other value
// (including the legacy "minimal", capitalized variants, or garbage) normalizes
// to the empty string, which the providers treat as "no explicit effort".
// "max" is only honored by Kiro and DeepSeek; other providers clamp it down.
func TestNormalizeReasoningEffortMatchesTS(t *testing.T) {
	valid := []string{"none", "low", "medium", "high", "xhigh", "max"}
	for _, v := range valid {
		if got := normalizeReasoningEffort(v); got != v {
			t.Fatalf("normalizeReasoningEffort(%q) = %q, want %q", v, got, v)
		}
		// Surrounding whitespace is trimmed but the value still recognized.
		if got := normalizeReasoningEffort("  " + v + "  "); got != v {
			t.Fatalf("normalizeReasoningEffort(padded %q) = %q, want %q", v, got, v)
		}
	}
	for _, v := range []string{"", "minimal", "High", "NONE", "verylow", "1", "true"} {
		if got := normalizeReasoningEffort(v); got != "" {
			t.Fatalf("normalizeReasoningEffort(%q) = %q, want empty", v, got)
		}
	}
}
