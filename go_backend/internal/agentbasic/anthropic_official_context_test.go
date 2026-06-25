package agentbasic

import "testing"

// TestSupportsOfficialAnthropic1mContext mirrors the TS
// supportsOfficialAnthropic1mContext model matcher.
func TestSupportsOfficialAnthropic1mContext(t *testing.T) {
	cases := []struct {
		model string
		want  bool
	}{
		{"claude-sonnet-4-20250514", true},
		{"claude-sonnet-4-5-20250929", true},
		{"anthropic:claude-sonnet-4-6", true},
		{"claude-opus-4-6", true},
		{"claude-opus-4.6", true},
		{"claude-opus-4-20250514", false},
		{"claude-opus-4-5-20251101", false},
		{"claude-3-5-sonnet-20241022", false},
		{"gpt-4o", false},
	}
	for _, tc := range cases {
		if got := supportsOfficialAnthropic1mContext(tc.model); got != tc.want {
			t.Fatalf("supportsOfficialAnthropic1mContext(%q)=%v want %v", tc.model, got, tc.want)
		}
	}
}

// TestEffectiveModelContextWindowOfficialUplift verifies the Anthropic official
// API uplifts 1M-capable models to 1M (max with resolved window) while leaving
// non-official, non-anthropic, and non-1M-capable models at the resolved window,
// mirroring TS getAnthropicEffectiveContextWindow.
func TestEffectiveModelContextWindowOfficialUplift(t *testing.T) {
	// claude-sonnet-4-20250514 builtin = 200k; official API uplifts to 1M.
	if got := effectiveModelContextWindow(nil, "claude-sonnet-4-20250514", "anthropic", "anthropic", true); got != 1000000 {
		t.Fatalf("official anthropic sonnet-4 must uplift to 1M, got %d", got)
	}
	// Without officialApi the resolved builtin window (200k) stands.
	if got := effectiveModelContextWindow(nil, "claude-sonnet-4-20250514", "anthropic", "anthropic", false); got != 200000 {
		t.Fatalf("non-official sonnet-4 must stay at builtin 200k, got %d", got)
	}
	// opus-4 (not 1M-capable) stays at builtin 200k even on official API.
	if got := effectiveModelContextWindow(nil, "claude-opus-4-20250514", "anthropic", "anthropic", true); got != 200000 {
		t.Fatalf("official opus-4 (non-1M) must stay at builtin 200k, got %d", got)
	}
	// A 1M-capable model whose builtin already reports 1M is unchanged (max keeps 1M).
	if got := effectiveModelContextWindow(nil, "claude-sonnet-4-6", "anthropic", "anthropic", true); got != 1000000 {
		t.Fatalf("official sonnet-4.6 must remain 1M, got %d", got)
	}
	// A non-anthropic provider kind serving a sonnet-4 alias does not get the
	// anthropic-only uplift (e.g. an OpenAI-compatible gateway), staying at builtin.
	if got := effectiveModelContextWindow(nil, "claude-sonnet-4", "someproxy", "openai", true); got != 200000 {
		t.Fatalf("non-anthropic kind must not uplift, got %d", got)
	}
}
