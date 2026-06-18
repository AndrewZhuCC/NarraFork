package agentbasic

import "testing"

// TestSplitModel covers the model-string splitter used throughout provider
// resolution: a "prefix:bare" model splits into its parts, an empty/whitespace
// prefix defaults to "openai", a bare model (no colon) defaults to the openai
// prefix, and an empty model returns the openai default with an empty bare model.
func TestSplitModel(t *testing.T) {
	cases := []struct {
		in         string
		wantPrefix string
		wantBare   string
	}{
		{"openai:gpt-5", "openai", "gpt-5"},
		{"anthropic:claude-sonnet-4", "anthropic", "claude-sonnet-4"},
		{"gpt-4o", "openai", "gpt-4o"},
		{"", "openai", ""},
		{"  ", "openai", ""},
		{":no-prefix-colon", "openai", ":no-prefix-colon"}, // leading colon (idx 0) is not a split point
		{"  spaced:  model  ", "spaced", "model"},
	}
	for _, tc := range cases {
		prefix, bare := splitModel(tc.in)
		if prefix != tc.wantPrefix || bare != tc.wantBare {
			t.Fatalf("splitModel(%q) = (%q, %q), want (%q, %q)", tc.in, prefix, bare, tc.wantPrefix, tc.wantBare)
		}
	}
}

// TestIsGoRuntimeUnsupportedProvider pins which provider prefixes the standalone
// Go agent runtime cannot run directly (kiro, codex — they require the fallback
// chain), versus the natively-supported ones (openai, anthropic, custom).
func TestIsGoRuntimeUnsupportedProvider(t *testing.T) {
	for _, prefix := range []string{"kiro", "codex", " kiro ", " codex "} {
		if !isGoRuntimeUnsupportedProvider(prefix) {
			t.Fatalf("expected %q to be unsupported by the Go runtime", prefix)
		}
	}
	for _, prefix := range []string{"openai", "anthropic", "nug", "nkp", "cline", "", "custom"} {
		if isGoRuntimeUnsupportedProvider(prefix) {
			t.Fatalf("expected %q to be supported by the Go runtime", prefix)
		}
	}
}
