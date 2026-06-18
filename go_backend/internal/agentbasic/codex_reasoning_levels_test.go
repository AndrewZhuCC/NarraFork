package agentbasic

import "testing"

// TestNormalizeCodexReasoningEffortMatchesTS pins normalizeCodexReasoningEffort to
// the TS normalizeCodexReasoningEffort (server/lib/agent/openai-provider.ts) with
// the per-model CODEX_MODEL_REASONING_LEVELS table. Expected outputs were captured
// directly from the TS logic:
//   - empty effort → "" (no reasoning)
//   - "none" passes through verbatim (codex sends effort:"none")
//   - a supported level passes through
//   - unsupported level degrades via xhigh->high or low->medium, else clamps to the
//     model's lowest supported level
//   - models absent from the table keep the requested effort unchanged
//
// This replaces the previous blanket xhigh->high clamp in chatReasoningEffort,
// which wrongly downgraded xhigh for models that DO support it (gpt-5.2/5.3/5.4/5.5/
// codex-max/spark) and missed the codex-mini low->medium clamp.
func TestNormalizeCodexReasoningEffortMatchesTS(t *testing.T) {
	cases := []struct {
		model  string
		effort string
		want   string
	}{
		// codex-mini supports only {medium, high}: low degrades to medium, xhigh to high.
		{"gpt-5.1-codex-mini", "low", "medium"},
		{"gpt-5.1-codex-mini", "xhigh", "high"},
		{"gpt-5.1-codex-mini", "medium", "medium"},
		// gpt-5.1-codex supports {low, medium, high}: xhigh degrades to high, low stays.
		{"gpt-5.1-codex", "xhigh", "high"},
		{"gpt-5.1-codex", "low", "low"},
		// Models that support xhigh keep it (previously wrongly downgraded).
		{"gpt-5.2-codex", "xhigh", "xhigh"},
		{"gpt-5.3-codex-spark", "xhigh", "xhigh"},
		{"gpt-5.4-mini", "xhigh", "xhigh"},
		{"gpt-5.5", "xhigh", "xhigh"},
		// Provider-prefixed model id is parsed to the bare model.
		{"openai:gpt-5.1-codex", "xhigh", "high"},
		// "none" passes through; empty → "".
		{"gpt-5.1-codex", "none", "none"},
		{"gpt-5.1-codex", "", ""},
		// Unknown model keeps the requested effort.
		{"unknown-model", "xhigh", "xhigh"},
		{"gpt-5-codex", "high", "high"},
	}
	for _, tc := range cases {
		if got := normalizeCodexReasoningEffort(tc.model, tc.effort); got != tc.want {
			t.Errorf("normalizeCodexReasoningEffort(%q, %q) = %q, want %q", tc.model, tc.effort, got, tc.want)
		}
	}
}
