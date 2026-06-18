package chapters

import "testing"

// TestParseRootlessNetworkBackendToleratesProviderNoise verifies the rootless
// network backend parser isolates the real backend token even when runLimited's
// combined stdout+stderr prepends a rootless/provider warning line. Before this
// hardening, trimming the whole blob would fail to match "pasta" when a warning
// preceded it, wrongly reporting a non-pasta backend and blocking proxy mode.
func TestParseRootlessNetworkBackendToleratesProviderNoise(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"clean", "pasta\n", "pasta"},
		{"clean passt", "passt", "passt"},
		{"quote wrapped", "'pasta'\n", "pasta"},
		{"double quote wrapped", "\"pasta\"", "pasta"},
		{"uppercase", "PASTA\n", "pasta"},
		{"warning prefix line", "WARN[0000] rootless networking warning\npasta\n", "pasta"},
		{"ansi noise", "\x1b[33mWARN\x1b[0m something\npasta", "pasta"},
		{"slirp4netns clean", "slirp4netns\n", "slirp4netns"},
		{"slirp4netns with noise", "Error: some warning\nslirp4netns\n", "slirp4netns"},
		{"unknown single token", "customnetcmd\n", "customnetcmd"},
		{"empty", "", ""},
		{"only whitespace", "   \n\t", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseRootlessNetworkBackend(tc.in); got != tc.want {
				t.Fatalf("parseRootlessNetworkBackend(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestParseRootlessNetworkBackendKeywordWinsOverTrailingToken ensures a known
// backend keyword is preferred over a later non-keyword token, so a trailing
// warning fragment cannot mask a real "pasta"/"passt"/"slirp4netns" value.
func TestParseRootlessNetworkBackendKeywordWinsOverTrailingToken(t *testing.T) {
	// pasta appears before a trailing noise token; keyword must win.
	if got := parseRootlessNetworkBackend("pasta trailingjunk"); got != "pasta" {
		t.Fatalf("got %q, want pasta", got)
	}
	// keyword on a later line still wins over an earlier non-keyword token.
	if got := parseRootlessNetworkBackend("leadingjunk\npasta"); got != "pasta" {
		t.Fatalf("got %q, want pasta", got)
	}
}
