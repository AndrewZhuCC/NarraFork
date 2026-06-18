package agentbasic

import (
	"context"
	"testing"
)

func TestBuiltinModelContextWindowExactAndFuzzy(t *testing.T) {
	cases := []struct {
		model string
		want  int
		ok    bool
	}{
		{"gpt-4o", 128000, true},
		{"openai:gpt-4o", 128000, true},
		{"claude-opus-4-6", 1000000, true},
		{"gpt-5-codex", 256000, true},
		{"gpt-4", 8192, true},
		// Longest-prefix fuzzy matches.
		{"claude-opus-4-6-20251101", 1000000, true}, // matches claude-opus-4-6
		{"gpt-4o-2024-08-06", 128000, true},         // matches gpt-4o
		{"anthropic:claude-3-5-sonnet-20241022", 200000, true},
		// Unknown.
		{"totally-unknown-model", 0, false},
	}
	for _, tc := range cases {
		got, ok := builtinModelContextWindow(tc.model)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Fatalf("builtinModelContextWindow(%q)=(%d,%v), want (%d,%v)", tc.model, got, ok, tc.want, tc.ok)
		}
	}
}

func TestResolveModelContextWindowPrecedence(t *testing.T) {
	// No settings -> builtin table -> default.
	if got := resolveModelContextWindow(nil, "gpt-4o", "openai"); got != 128000 {
		t.Fatalf("builtin path: got %d", got)
	}
	if got := resolveModelContextWindow(nil, "totally-unknown", "openai"); got != defaultModelContextWindow {
		t.Fatalf("default path: got %d, want %d", got, defaultModelContextWindow)
	}

	// User override (agent.modelContextWindows[prefix:bare]) wins over builtin.
	store := testConfigStore(t, map[string]any{
		"agent": map[string]any{
			"modelContextWindows": map[string]any{
				"openai:gpt-4o": float64(50000),
			},
		},
	})
	if got := resolveModelContextWindow(store, "gpt-4o", "openai"); got != 50000 {
		t.Fatalf("user override should win, got %d", got)
	}

	// Provider config defaultContextWindow wins over builtin (no user override).
	store2 := testConfigStore(t, map[string]any{
		"openaiProviders": []any{
			map[string]any{"prefix": "myopenai", "defaultContextWindow": float64(321000)},
		},
	})
	if got := resolveModelContextWindow(store2, "gpt-4o", "myopenai"); got != 321000 {
		t.Fatalf("provider defaultContextWindow should win, got %d", got)
	}
}

func TestUsageContextPercentWithResolvedWindow(t *testing.T) {
	usage := &UsageSnapshot{PromptTokens: 100000, InputTokens: 100000, ContextWindow: 200000}
	if pct := usageContextPercent(usage); pct != 50 {
		t.Fatalf("expected 50%%, got %v", pct)
	}
	// Without a window, percentage is 0 (cannot compute).
	if pct := usageContextPercent(&UsageSnapshot{PromptTokens: 100000}); pct != 0 {
		t.Fatalf("expected 0%% without window, got %v", pct)
	}
}

// TestLoopComputesContextPercentForResolvedWindow verifies the loop injects the
// resolved context window into usage snapshots so a context_usage event carries a
// non-zero percentage for providers that report only token counts (previously
// always 0 because no window was resolved).
func TestLoopComputesContextPercentForResolvedWindow(t *testing.T) {
	// gpt-4o resolves to a 128000 window; 64000 prompt tokens -> 50%.
	adapter := &scriptedAdapter{turns: [][]StreamEvent{
		{
			{Parsed: ParsedStreamEvent{Usage: &UsageSnapshot{PromptTokens: 64000, InputTokens: 64000}}},
			{Parsed: ParsedStreamEvent{Text: "done"}},
		},
	}}
	stream := runAgentLoopWithSettings(
		context.Background(),
		nil,
		ProviderResolution{Provider: "openai", Model: "gpt-4o", Adapter: adapter},
		[]Message{{Role: "user", Content: "hi"}},
		t.TempDir(),
		normalizeGenerateOptions(WithRetryBaseDelay(0)),
	)
	var pct float64
	for event := range stream {
		if event.Type == "context_usage" && event.Percentage > 0 {
			pct = event.Percentage
		}
	}
	if pct < 49 || pct > 51 {
		t.Fatalf("expected ~50%% context usage for gpt-4o (128k window), got %v", pct)
	}
}
