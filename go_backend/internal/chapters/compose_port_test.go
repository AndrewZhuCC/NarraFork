package chapters

import "testing"

// TestParseComposePortMatchesTS pins parseComposePort to the TypeScript compose
// port parsing in parseComposeYaml (server/services/container-service.ts), which
// applies the regex /^(\d+):(\d+)/: only a leading numeric host:container pair is
// accepted. Single ports, IP-prefixed mappings, and non-numeric leading
// segments are skipped (0,0). A "/tcp" suffix or trailing ":extra" segment is
// ignored because the match is a prefix.
func TestParseComposePortMatchesTS(t *testing.T) {
	cases := []struct {
		in        string
		host      int
		container int
	}{
		{"8080:80", 8080, 80},
		{"3000:3000", 3000, 3000},
		// "/tcp" protocol suffix ignored (prefix match).
		{"3000:3000/tcp", 3000, 3000},
		// Trailing extra segment ignored (prefix match keeps host:container).
		{"8080:80:90", 8080, 80},
		// Single port with no colon -> skipped (TS regex needs host:container).
		{"3000", 0, 0},
		// IP-prefixed mapping -> non-numeric leading segment -> skipped.
		{"127.0.0.1:8080:80", 0, 0},
		// Leading whitespace trimmed, then matched.
		{"  8080:80  ", 8080, 80},
		// Non-numeric -> skipped.
		{"web:80", 0, 0},
		{"", 0, 0},
		// Range/published forms that don't start numeric:numeric -> skipped.
		{":80", 0, 0},
	}
	for _, tc := range cases {
		host, container := parseComposePort(tc.in)
		if host != tc.host || container != tc.container {
			t.Fatalf("parseComposePort(%q) = (%d,%d), want (%d,%d)", tc.in, host, container, tc.host, tc.container)
		}
	}
}
