package narrators

import (
	"testing"

	"narrafork-go/internal/config"
)

func newSubagentPolicyTestService(t *testing.T, patch map[string]any) *Service {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	store, err := config.Load()
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	if patch != nil {
		if _, err := store.Patch(patch); err != nil {
			t.Fatalf("patch settings: %v", err)
		}
	}
	return &Service{Settings: store}
}

func restrictionTrait(t *testing.T, pools map[string]any) string {
	t.Helper()
	return encodeTrait(subagentModelRestrictionTraitPrefix, map[string]any{"version": 1, "pools": pools})
}

// TestSubagentModelPolicyMatchesTS pins subagentModelPolicy to the TS
// resolveEffectiveSubagentModelPolicy (server/lib/narrator-custom-traits.ts):
// custom trait pool > global settings pool > none, with explore/plan/general pool
// keys and the non-builtin->general fallback.
func TestSubagentModelPolicyMatchesTS(t *testing.T) {
	// No trait, empty settings pool -> none.
	svc := newSubagentPolicyTestService(t, nil)
	if src, key, models, empty := svc.subagentModelPolicy(nil, "explore"); src != "none" || key != "explore" || len(models) != 0 || empty {
		t.Fatalf("default -> none, got src=%q key=%q models=%v empty=%v", src, key, models, empty)
	}

	// Settings pool configured -> source settings.
	svc2 := newSubagentPolicyTestService(t, map[string]any{
		"agent": map[string]any{"subagentAllowedModels": map[string]any{"explore": []any{"openai:gpt-a", "openai:gpt-b"}}},
	})
	if src, key, models, _ := svc2.subagentModelPolicy(nil, "explore"); src != "settings" || key != "explore" || len(models) != 2 || models[0] != "openai:gpt-a" {
		t.Fatalf("settings pool, got src=%q key=%q models=%v", src, key, models)
	}

	// Custom trait direct key wins over settings.
	traits := []string{restrictionTrait(t, map[string]any{"explore": []any{map[string]any{"model": "anthropic:claude-x"}}})}
	if src, key, models, empty := svc2.subagentModelPolicy(traits, "explore"); src != "custom" || key != "explore" || len(models) != 1 || models[0] != "anthropic:claude-x" || empty {
		t.Fatalf("custom direct key, got src=%q key=%q models=%v empty=%v", src, key, models, empty)
	}

	// Non-builtin subagent type falls back to the custom "general" pool.
	traitsGeneral := []string{restrictionTrait(t, map[string]any{"general": []any{"openai:gpt-g"}})}
	if src, key, models, _ := svc.subagentModelPolicy(traitsGeneral, "my-custom-type"); src != "custom" || key != "general" || len(models) != 1 || models[0] != "openai:gpt-g" {
		t.Fatalf("non-builtin -> general fallback, got src=%q key=%q models=%v", src, key, models)
	}

	// Explicitly empty custom pool -> isExplicitEmpty true.
	traitsEmpty := []string{restrictionTrait(t, map[string]any{"plan": []any{}})}
	if src, _, models, empty := svc.subagentModelPolicy(traitsEmpty, "plan"); src != "custom" || len(models) != 0 || !empty {
		t.Fatalf("empty custom pool -> isExplicitEmpty, got src=%q models=%v empty=%v", src, models, empty)
	}
}

// TestResolveSubagentModelFromPolicyMatchesTS pins resolveSubagentModelFromPolicy to
// the TS helper: none passes explicit/first-candidate through; a restriction filters
// through the allowed pool; an unsatisfiable restriction yields (",false") so the
// caller rejects the spawn.
func TestResolveSubagentModelFromPolicyMatchesTS(t *testing.T) {
	svc := newSubagentPolicyTestService(t, nil)

	// source none: explicit wins.
	if m, ok := svc.resolveSubagentModelFromPolicy("none", nil, false, "openai:explicit", []string{"openai:cand"}); !ok || m != "openai:explicit" {
		t.Fatalf("none+explicit, got %q ok=%v", m, ok)
	}
	// source none: no explicit -> first non-empty candidate.
	if m, ok := svc.resolveSubagentModelFromPolicy("none", nil, false, "", []string{"", "openai:cand"}); !ok || m != "openai:cand" {
		t.Fatalf("none+candidate, got %q ok=%v", m, ok)
	}

	pool := []string{"openai:gpt-a", "openai:gpt-b"}
	// settings: explicit in pool -> allowed.
	if m, ok := svc.resolveSubagentModelFromPolicy("settings", pool, false, "openai:gpt-b", nil); !ok || m != "openai:gpt-b" {
		t.Fatalf("settings+explicit in pool, got %q ok=%v", m, ok)
	}
	// settings: explicit outside pool -> rejected.
	if m, ok := svc.resolveSubagentModelFromPolicy("settings", pool, false, "openai:gpt-z", nil); ok || m != "" {
		t.Fatalf("settings+explicit out of pool should reject, got %q ok=%v", m, ok)
	}
	// settings: no explicit, first allowed candidate wins.
	if m, ok := svc.resolveSubagentModelFromPolicy("settings", pool, false, "", []string{"openai:gpt-z", "openai:gpt-a"}); !ok || m != "openai:gpt-a" {
		t.Fatalf("settings+candidate match, got %q ok=%v", m, ok)
	}
	// settings: no candidate matches -> fall back to pool[0].
	if m, ok := svc.resolveSubagentModelFromPolicy("settings", pool, false, "", []string{"openai:gpt-z"}); !ok || m != "openai:gpt-a" {
		t.Fatalf("settings+no match -> pool[0], got %q ok=%v", m, ok)
	}
	// isExplicitEmpty -> rejected.
	if m, ok := svc.resolveSubagentModelFromPolicy("custom", nil, true, "openai:gpt-a", nil); ok || m != "" {
		t.Fatalf("isExplicitEmpty should reject, got %q ok=%v", m, ok)
	}
}

// TestSubagentModelDefaultPrefMatchesTS pins subagentModelDefaultPref: explore/plan
// read settings.agent.subagentModels[type]; general/review/builtin have no per-type
// preference (custom subagent defaultModel is sourced from SUBAGENT.md elsewhere).
func TestSubagentModelDefaultPrefMatchesTS(t *testing.T) {
	svc := newSubagentPolicyTestService(t, map[string]any{
		"agent": map[string]any{"subagentModels": map[string]any{"explore": "openai:gpt-explore", "plan": "anthropic:claude-plan"}},
	})
	if got := svc.subagentModelDefaultPref("explore"); got != "openai:gpt-explore" {
		t.Fatalf("explore pref, got %q", got)
	}
	if got := svc.subagentModelDefaultPref("plan"); got != "anthropic:claude-plan" {
		t.Fatalf("plan pref, got %q", got)
	}
	if got := svc.subagentModelDefaultPref("general"); got != "" {
		t.Fatalf("general should have no per-type pref, got %q", got)
	}
	if got := svc.subagentModelDefaultPref("review"); got != "" {
		t.Fatalf("review should have no per-type pref, got %q", got)
	}
}
