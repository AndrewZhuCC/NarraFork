package systemroutes

import (
	"strings"
	"testing"
)

// TestNormalizeCodexTierOrderMatchesTS pins normalizeCodexTierOrder to the TS
// normalizeCodexTierOrder (server/lib/codex-manager.ts): it filters out tiers
// outside ALL_CODEX_TIER_ORDER, de-duplicates keeping first occurrence, and
// falls back to DEFAULT_CODEX_TIER_ORDER ["pro","prolite","plus","team","free"]
// when the result is empty. This locks the empty->default branch (reachable via
// the GET /codex config path with no stored tierOrder) and the dedup/filter
// behavior against regressions.
func TestNormalizeCodexTierOrderMatchesTS(t *testing.T) {
	join := func(values []any) string { return strings.Join(anyStrings(values), ",") }

	cases := []struct {
		name string
		in   []any
		want string
	}{
		// Empty input falls back to DEFAULT_CODEX_TIER_ORDER.
		{"empty", []any{}, "pro,prolite,plus,team,free"},
		{"nil", nil, "pro,prolite,plus,team,free"},
		// All-invalid input also falls back to default (result empty after filter).
		{"all invalid", []any{"gold", "silver", 42, true}, "pro,prolite,plus,team,free"},
		// Valid tiers preserved in order; duplicates removed keeping first occurrence.
		{"dedup keeps first", []any{"team", "pro", "team", "pro"}, "team,pro"},
		// Invalid entries dropped, valid ones kept in order.
		{"filter invalid", []any{"team", "bogus", "free", 7}, "team,free"},
		// All six valid tiers preserved.
		{"all valid", []any{"other", "free", "team", "plus", "prolite", "pro"}, "other,free,team,plus,prolite,pro"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := join(normalizeCodexTierOrder(tc.in)); got != tc.want {
				t.Fatalf("normalizeCodexTierOrder(%#v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}

	// codexEffectiveTierOrder appends the remaining ALL_CODEX_TIER_ORDER tiers
	// (pro,prolite,plus,team,free,other) after the normalized prefix, matching TS.
	if got := join(codexEffectiveTierOrder([]any{"team", "pro"})); got != "team,pro,prolite,plus,free,other" {
		t.Fatalf("codexEffectiveTierOrder([team pro]) = %q, want team,pro,prolite,plus,free,other", got)
	}
	// Empty input -> normalized default, then effective appends the rest (only "other").
	if got := join(codexEffectiveTierOrder(nil)); got != "pro,prolite,plus,team,free,other" {
		t.Fatalf("codexEffectiveTierOrder(nil) = %q, want pro,prolite,plus,team,free,other", got)
	}
}
