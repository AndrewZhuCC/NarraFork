package narrators

import (
	"testing"
)

// TestNormalizeSubagentTypeRecognizesSearch verifies "search" is treated as a
// canonical builtin subagent type (mirrors TS, where search is one of explore/
// plan/search/general), not run through the custom-type cleanup path.
func TestNormalizeSubagentTypeRecognizesSearch(t *testing.T) {
	cases := map[string]string{
		"search":    "search",
		"Search":    "search",
		"  search ": "search",
	}
	for in, want := range cases {
		if got := normalizeSubagentType(in); got != want {
			t.Fatalf("normalizeSubagentType(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestSearchSubagentCustomToolAccessIsBuiltin verifies search is short-circuited
// as a builtin type (no SUBAGENT.md lookup), matching the TS builtin set.
func TestSearchSubagentCustomToolAccessIsBuiltin(t *testing.T) {
	if access := customSubagentToolAccessForType("search"); access.Found {
		t.Fatalf("search must be treated as a builtin subagent type (Found=false), got %+v", access)
	}
}

// TestSearchSubagentModelPolicyUsesSearchPool verifies a search subagent resolves
// its model pool from settings.agent.subagentAllowedModels.search rather than
// collapsing onto the general pool (mirrors TS poolKey resolution).
func TestSearchSubagentModelPolicyUsesSearchPool(t *testing.T) {
	svc := newSubagentPolicyTestService(t, map[string]any{
		"agent": map[string]any{"subagentAllowedModels": map[string]any{
			"search":  []any{"openai:search-model"},
			"general": []any{"openai:general-model"},
		}},
	})
	src, key, models, _ := svc.subagentModelPolicy(nil, "search")
	if src != "settings" || key != "search" || len(models) != 1 || models[0] != "openai:search-model" {
		t.Fatalf("search policy should use the search pool, got src=%q key=%q models=%v", src, key, models)
	}

	// Custom restriction trait with a direct "search" pool also wins for search.
	traits := []string{restrictionTrait(t, map[string]any{"search": []any{map[string]any{"model": "anthropic:claude-search"}}})}
	src2, key2, models2, _ := svc.subagentModelPolicy(traits, "search")
	if src2 != "custom" || key2 != "search" || len(models2) != 1 || models2[0] != "anthropic:claude-search" {
		t.Fatalf("custom search pool should win, got src=%q key=%q models=%v", src2, key2, models2)
	}
}

// TestSearchSubagentModelDefaultPref verifies search reads its per-type model
// preference from settings.agent.subagentModels.search (mirrors TS subagentPref).
func TestSearchSubagentModelDefaultPref(t *testing.T) {
	svc := newSubagentPolicyTestService(t, map[string]any{
		"agent": map[string]any{"subagentModels": map[string]any{"search": "openai:gpt-search"}},
	})
	if got := svc.subagentModelDefaultPref("search"); got != "openai:gpt-search" {
		t.Fatalf("search pref, got %q", got)
	}
}
