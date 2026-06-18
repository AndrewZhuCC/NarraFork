package agentbasic

import (
	"testing"

	"narrafork-go/internal/config"
)

// TestResolveEffectiveModelFromRawPinnedAggregationModel locks the pinned-model
// aggregation branch of resolveEffectiveModelFromRaw: a value of the form
// "__agg__:id:provider:model" must resolve directly to the pinned model without
// consulting the aggregation member list, mirroring the TypeScript
// resolveEffectiveModel/parseAggModelValue pinned path in
// server/lib/settings/provider.ts.
func TestResolveEffectiveModelFromRawPinnedAggregationModel(t *testing.T) {
	raw := map[string]any{
		"agent": map[string]any{
			"modelAggregations": []any{
				map[string]any{
					"id":     "primary",
					"models": []any{"openai:gpt-routed"},
				},
			},
		},
	}
	got := resolveEffectiveModelFromRaw(raw, "__agg__:primary:anthropic:claude-pinned")
	if got != "anthropic:claude-pinned" {
		t.Fatalf("pinned aggregation model: expected %q, got %q", "anthropic:claude-pinned", got)
	}
}

// TestResolveEffectiveModelFromRawPinnedSentinelUsesHardFallback locks the
// subtle parity distinction in the pinned-model branch: when the pinned value
// is itself a default sentinel, sanitizeResolvedModelCandidate maps it to the
// hard-coded config.DefaultAgentModel (TS FALLBACK_DEFAULT_MODEL) BEFORE the
// configured-default fallback can fire. So even with a different configured
// agent.defaultModel, a pinned sentinel resolves to the hard fallback, matching
// the TS `sanitizeResolvedModelCandidate(agg.pinnedModel) ?? resolveConfiguredDefaultModel`
// short-circuit in server/lib/settings/provider.ts.
func TestResolveEffectiveModelFromRawPinnedSentinelUsesHardFallback(t *testing.T) {
	raw := map[string]any{
		"agent": map[string]any{
			"defaultModel": "openai:gpt-config-default",
		},
	}
	got := resolveEffectiveModelFromRaw(raw, "__agg__:primary:__default__")
	if got != config.DefaultAgentModel {
		t.Fatalf("pinned sentinel model: expected hard fallback %q (not configured default), got %q", config.DefaultAgentModel, got)
	}
}

// TestResolveEffectiveModelFromRawEmptyModelUsesConfiguredDefault covers the
// empty-input branch that delegates to resolveConfiguredDefaultModelFromRaw,
// returning the configured agent.defaultModel verbatim when it is a concrete
// provider:model string.
func TestResolveEffectiveModelFromRawEmptyModelUsesConfiguredDefault(t *testing.T) {
	raw := map[string]any{
		"agent": map[string]any{
			"defaultModel": "anthropic:claude-config",
		},
	}
	if got := resolveEffectiveModelFromRaw(raw, "   "); got != "anthropic:claude-config" {
		t.Fatalf("blank model: expected configured default %q, got %q", "anthropic:claude-config", got)
	}
}

// TestResolveEffectiveModelFromRawUnknownAggregationFallsBackToConfiguredDefault
// covers the branch where the requested aggregation has no resolvable members
// (here: missing aggregation id) so resolution falls back to the configured
// default model.
func TestResolveEffectiveModelFromRawUnknownAggregationFallsBackToConfiguredDefault(t *testing.T) {
	raw := map[string]any{
		"agent": map[string]any{
			"defaultModel": "openai:gpt-config-default",
		},
	}
	if got := resolveEffectiveModelFromRaw(raw, "__agg__:missing"); got != "openai:gpt-config-default" {
		t.Fatalf("unknown aggregation: expected configured default %q, got %q", "openai:gpt-config-default", got)
	}
}

// TestResolveConfiguredDefaultModelFromRawHardFallbackWhenDefaultIsSentinel
// covers resolveConfiguredDefaultModelFromRaw when agent.defaultModel is itself
// a sentinel: it must resolve to the hard-coded config.DefaultAgentModel rather
// than recursing or emitting the sentinel.
func TestResolveConfiguredDefaultModelFromRawHardFallbackWhenDefaultIsSentinel(t *testing.T) {
	raw := map[string]any{
		"agent": map[string]any{
			"defaultModel": "__default__",
		},
	}
	if got := resolveEffectiveModelFromRaw(raw, ""); got != config.DefaultAgentModel {
		t.Fatalf("sentinel configured default: expected hard fallback %q, got %q", config.DefaultAgentModel, got)
	}
}

// TestResolveConfiguredDefaultModelFromRawPinnedAggregationDefault covers the
// configured-default path where agent.defaultModel is a pinned aggregation
// value: the pinned model is returned directly.
func TestResolveConfiguredDefaultModelFromRawPinnedAggregationDefault(t *testing.T) {
	raw := map[string]any{
		"agent": map[string]any{
			"defaultModel": "__agg__:primary:openai:gpt-pinned-default",
		},
	}
	if got := resolveEffectiveModelFromRaw(raw, ""); got != "openai:gpt-pinned-default" {
		t.Fatalf("pinned configured default: expected %q, got %q", "openai:gpt-pinned-default", got)
	}
}
