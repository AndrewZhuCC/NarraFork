package agentbasic

import (
	"reflect"
	"testing"
)

// TestAnthropicThinkingConfigMatchesTS pins anthropicThinkingConfig to the TS
// buildThinkingConfig (server/lib/agent/anthropic-provider.ts, matching Claude
// Code v2.1.71). Expected outputs were captured directly from the TS logic:
//   - non-thinking models (claude-3-5, gpt-5) → nil (no thinking field)
//   - reasoningEffort "none" on a thinking-capable model → {type:"disabled"}
//   - DeepSeek → {type:"enabled", budget_tokens:10000}
//   - all other supported models (opus-4/sonnet-4/haiku-4/3.7) → {type:"adaptive"}
//
// This replaces the previous Go behavior, which sent the deprecated
// {type:"enabled", budget_tokens:1024/4096/8192} for every model and returned nil
// (not "disabled") for effort "none".
func TestAnthropicThinkingConfigMatchesTS(t *testing.T) {
	cases := []struct {
		model  string
		effort string
		want   map[string]any
	}{
		{"claude-opus-4-6", "high", map[string]any{"type": "adaptive"}},
		{"claude-opus-4-6", "none", map[string]any{"type": "disabled"}},
		{"claude-opus-4-6", "", map[string]any{"type": "adaptive"}},
		{"claude-sonnet-4-5", "medium", map[string]any{"type": "adaptive"}},
		{"claude-3-7-sonnet", "low", map[string]any{"type": "adaptive"}},
		{"claude-haiku-4-5", "xhigh", map[string]any{"type": "adaptive"}},
		{"claude-haiku-4-5", "none", map[string]any{"type": "disabled"}},
		{"deepseek-chat", "high", map[string]any{"type": "enabled", "budget_tokens": 10000}},
		{"deepseek-chat", "none", map[string]any{"type": "disabled"}},
		// Non-thinking models → no thinking field.
		{"claude-3-5-sonnet", "high", nil},
		{"gpt-5", "high", nil},
		{"", "high", nil},
	}
	for _, tc := range cases {
		got := anthropicThinkingConfig(tc.model, tc.effort)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("anthropicThinkingConfig(%q, %q) = %#v, want %#v", tc.model, tc.effort, got, tc.want)
		}
	}
}

// TestAnthropicSupportsThinkingMatchesTS pins anthropicSupportsThinking to the TS
// supportsThinking model classification.
func TestAnthropicSupportsThinkingMatchesTS(t *testing.T) {
	supported := []string{
		"deepseek-chat", "claude-3-7-sonnet", "claude-3.7-sonnet",
		"claude-opus-4-6", "claude-sonnet-4-5", "claude-haiku-4-5", "claude-opus-4-1",
	}
	for _, m := range supported {
		if !anthropicSupportsThinking(m) {
			t.Errorf("anthropicSupportsThinking(%q) = false, want true", m)
		}
	}
	unsupported := []string{"claude-3-5-sonnet", "gpt-5", "claude-3-haiku", "gemini-2.5-pro", ""}
	for _, m := range unsupported {
		if anthropicSupportsThinking(m) {
			t.Errorf("anthropicSupportsThinking(%q) = true, want false", m)
		}
	}
}
