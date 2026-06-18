package agentbasic

import "testing"

// TestMaxTurnsClampMatchesTSSettingsRange verifies the agent loop honors the
// full TS agent.maxTurns settings range. The TS settings validator and
// defaults.ts allow 1-1000 with default 200, and the TS loop applies NO extra
// cap (server/lib/agent/loop.ts uses settings.agent.maxTurns directly). Go's
// normalizeGenerateOptions must therefore accept up to 1000 rather than silently
// truncating 201-1000 to the old 200 ceiling.
func TestMaxTurnsClampMatchesTSSettingsRange(t *testing.T) {
	cases := []struct {
		in   int
		want int
	}{
		{1, 1},
		{200, 200},
		// Previously truncated to 200; must now pass through (matches TS range).
		{201, 201},
		{500, 500},
		{1000, 1000},
		// Above the validated settings ceiling clamps to 1000.
		{1001, 1000},
		{99999, 1000},
		// Non-positive falls back to the bare-agent default.
		{0, defaultMaxTurns},
		{-5, defaultMaxTurns},
	}
	for _, tc := range cases {
		opts := normalizeGenerateOptions(WithMaxTurns(tc.in))
		if opts.maxTurns != tc.want {
			t.Fatalf("normalizeGenerateOptions(WithMaxTurns(%d)).maxTurns = %d, want %d", tc.in, opts.maxTurns, tc.want)
		}
	}
	// The ceiling must match the TS settings max (1000), not the old 200.
	if maxAllowedTurns != 1000 {
		t.Fatalf("maxAllowedTurns = %d, want 1000 (TS settings ceiling)", maxAllowedTurns)
	}
}
