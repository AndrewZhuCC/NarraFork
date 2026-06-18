package agentbasic

import "testing"

// TestUsesStatefulModelMatchesTS pins usesStatefulModel to the TS
// usesStatefulModel (server/lib/settings/provider.ts): codex Kind and the
// responses/codex API modes are stateful (server-side conversation state), so
// transient retries must be disabled for them.
func TestUsesStatefulModelMatchesTS(t *testing.T) {
	stateful := []ProviderResolution{
		{Config: Provider{Kind: "codex"}},
		{Config: Provider{Kind: "Codex"}},
		{Config: Provider{APIMode: "codex"}},
		{Config: Provider{APIMode: "responses"}},
		{Config: Provider{APIMode: "Responses"}},
	}
	for _, r := range stateful {
		if !usesStatefulModel(r) {
			t.Fatalf("expected stateful for %#v", r.Config)
		}
	}
	stateless := []ProviderResolution{
		{Config: Provider{Kind: "openai", APIMode: "completions"}},
		{Config: Provider{Kind: "anthropic"}},
		{Config: Provider{Kind: "nkp", APIMode: "chat"}},
		{Config: Provider{}},
	}
	for _, r := range stateless {
		if usesStatefulModel(r) {
			t.Fatalf("expected stateless for %#v", r.Config)
		}
	}
}

// TestEffectiveMaxChatRetriesMatchesTS pins the getMaxChatRetries() behavior:
// stateful models get 0 retries; otherwise the configured maxChatRetries is used
// (with -1 = infinite, mirroring agent.maxTransientRetries).
func TestEffectiveMaxChatRetriesMatchesTS(t *testing.T) {
	stateless := ProviderResolution{Config: Provider{Kind: "openai", APIMode: "completions"}}
	stateful := ProviderResolution{Config: Provider{APIMode: "responses"}}

	cases := []struct {
		configured int
		resolution ProviderResolution
		want       int
	}{
		{10, stateless, 10},
		{0, stateless, 0},
		{-1, stateless, -1}, // infinite sentinel preserved
		{10, stateful, 0},   // stateful forces 0
		{-1, stateful, 0},   // stateful overrides infinite
		{100, stateful, 0},
	}
	for _, tc := range cases {
		opts := generateOptions{maxChatRetries: tc.configured}
		if got := effectiveMaxChatRetries(opts, tc.resolution); got != tc.want {
			t.Fatalf("effectiveMaxChatRetries(%d, stateful=%v) = %d, want %d", tc.configured, usesStatefulModel(tc.resolution), got, tc.want)
		}
	}
}

// TestNormalizeGenerateOptionsPreservesInfiniteRetrySentinel verifies the -1
// infinite-retry sentinel survives normalization (previously clamped to 0),
// while values below -1 clamp up to -1.
func TestNormalizeGenerateOptionsPreservesInfiniteRetrySentinel(t *testing.T) {
	if opts := normalizeGenerateOptions(WithMaxChatRetries(-1)); opts.maxChatRetries != -1 {
		t.Fatalf("maxChatRetries(-1) normalized to %d, want -1 (infinite sentinel)", opts.maxChatRetries)
	}
	if opts := normalizeGenerateOptions(WithMaxChatRetries(-99)); opts.maxChatRetries != -1 {
		t.Fatalf("maxChatRetries(-99) normalized to %d, want -1", opts.maxChatRetries)
	}
	if opts := normalizeGenerateOptions(WithMaxChatRetries(10)); opts.maxChatRetries != 10 {
		t.Fatalf("maxChatRetries(10) normalized to %d, want 10", opts.maxChatRetries)
	}
}
