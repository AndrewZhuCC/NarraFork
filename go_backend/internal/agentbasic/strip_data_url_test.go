package agentbasic

import "testing"

// TestStripDataURLPrefix covers the data-URL base64 payload extractor used when
// forwarding image content to the OpenAI-compatible provider: a base64 data URL
// returns just the payload; non-data or non-base64 values pass through unchanged.
func TestStripDataURLPrefix(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"data:image/png;base64,iVBORw0KGgo=", "iVBORw0KGgo="},
		{"data:image/jpeg;base64,  AAAA  ", "AAAA"},                // payload is trimmed
		{"data:text/plain,hello", "data:text/plain,hello"},         // no ;base64, -> unchanged
		{"https://example.com/x.png", "https://example.com/x.png"}, // not a data URL
		{"", ""},
	}
	for _, tc := range cases {
		if got := stripDataURLPrefix(tc.in); got != tc.want {
			t.Fatalf("stripDataURLPrefix(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
