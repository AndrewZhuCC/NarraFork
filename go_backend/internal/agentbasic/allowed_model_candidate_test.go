package agentbasic

import "testing"

// TestResolveAllowedModelCandidateMatchesTS pins ResolveAllowedModelCandidate to
// the TS resolveAllowedModelCandidate (server/lib/settings/provider.ts): it resolves
// a candidate model against an allowed pool WITHOUT triggering aggregation routing,
// expanding __default__ and __agg__ references for matching. An empty pool imposes
// no restriction; a non-matching candidate yields "".
func TestResolveAllowedModelCandidateMatchesTS(t *testing.T) {
	raw := map[string]any{
		"agent": map[string]any{
			"defaultModel": "openai:gpt-default",
			"modelAggregations": []any{
				map[string]any{
					"id":     "team",
					"models": []any{"openai:gpt-a", "anthropic:claude-b"},
				},
			},
		},
	}

	// Empty pool -> no restriction, normalized candidate returned as-is.
	if got := ResolveAllowedModelCandidate(raw, "openai:gpt-x", nil); got != "openai:gpt-x" {
		t.Fatalf("empty pool should pass through, got %q", got)
	}
	// "default" normalizes to the follow-default sentinel.
	if got := ResolveAllowedModelCandidate(raw, "default", nil); got != "__default__" {
		t.Fatalf("'default' should normalize to sentinel, got %q", got)
	}

	// Exact concrete match.
	if got := ResolveAllowedModelCandidate(raw, "openai:gpt-a", []string{"openai:gpt-a"}); got != "openai:gpt-a" {
		t.Fatalf("exact match should return candidate, got %q", got)
	}
	// Concrete candidate not in pool -> "".
	if got := ResolveAllowedModelCandidate(raw, "openai:gpt-z", []string{"openai:gpt-a"}); got != "" {
		t.Fatalf("non-matching candidate should return empty, got %q", got)
	}

	// Candidate is an aggregation; pool lists a concrete member -> resolves to that
	// member without routing (the agg expands to its members for matching).
	if got := ResolveAllowedModelCandidate(raw, "__agg__:team", []string{"anthropic:claude-b"}); got != "anthropic:claude-b" {
		t.Fatalf("aggregation candidate should resolve to allowed member, got %q", got)
	}
	// Pool is the aggregation; concrete candidate that is a member -> allowed (the
	// allowed agg expands to include the concrete member, which equals raw candidate).
	if got := ResolveAllowedModelCandidate(raw, "openai:gpt-a", []string{"__agg__:team"}); got != "openai:gpt-a" {
		t.Fatalf("concrete member should match aggregation pool, got %q", got)
	}
	// __default__ candidate expands to agent.defaultModel; pool lists that model.
	if got := ResolveAllowedModelCandidate(raw, "__default__", []string{"openai:gpt-default"}); got != "openai:gpt-default" {
		t.Fatalf("__default__ should expand to defaultModel for matching, got %q", got)
	}
	// Aggregation candidate whose members are all outside the pool -> "".
	if got := ResolveAllowedModelCandidate(raw, "__agg__:team", []string{"openai:gpt-other"}); got != "" {
		t.Fatalf("aggregation with no allowed members should return empty, got %q", got)
	}
	// Empty/whitespace candidate -> "".
	if got := ResolveAllowedModelCandidate(raw, "   ", []string{"openai:gpt-a"}); got != "" {
		t.Fatalf("blank candidate should return empty, got %q", got)
	}
}
