package httpx

import "testing"

// TestCoerceURLZodMatchesZodV4URL pins CoerceURLZod to Zod v4 z.string().url(),
// which validates via `new URL(value.trim())` and stores the trimmed value. The
// expected results below were confirmed against the JS URL constructor:
//   - surrounding whitespace is trimmed and the trimmed value is returned;
//   - special schemes (http/https/ws/wss/ftp) require a non-empty host;
//   - opaque/non-special schemes (mailto/tel/data/javascript) are valid without a host;
//   - bare scheme with no host ("http://") and host-less relative inputs are rejected.
func TestCoerceURLZodMatchesZodV4URL(t *testing.T) {
	cases := []struct {
		in      string
		wantVal string
		wantOK  bool
	}{
		{"http://example.com", "http://example.com", true},
		{"https://x.io/path", "https://x.io/path", true},
		{"ftp://h/x", "ftp://h/x", true},
		{"ws://h", "ws://h", true},
		{"https://例え.テスト", "https://例え.テスト", true},
		{"http://h:99/p?q#f", "http://h:99/p?q#f", true},
		// Trimming: the trimmed value is returned (TS stores trimmed + length-checks it).
		{"  http://x.com  ", "http://x.com", true},
		{"\thttps://y.com\n", "https://y.com", true},
		// Opaque/non-special schemes are valid without a host.
		{"mailto:a@b.com", "mailto:a@b.com", true},
		{"tel:123", "tel:123", true},
		{"javascript:alert(1)", "javascript:alert(1)", true},
		// Rejected: bare special scheme with no host, host-less, empty, spaces in authority.
		{"http://", "", false},
		{"https://", "", false},
		{"example.com", "", false},
		{"//example.com", "", false},
		{"relative/path", "", false},
		{"", "", false},
		{"   ", "", false},
		{"http://a b.com", "", false},
	}
	for _, tc := range cases {
		got, ok := CoerceURLZod(tc.in)
		if ok != tc.wantOK {
			t.Errorf("CoerceURLZod(%q) ok = %v, want %v", tc.in, ok, tc.wantOK)
			continue
		}
		if ok && got != tc.wantVal {
			t.Errorf("CoerceURLZod(%q) = %q, want %q", tc.in, got, tc.wantVal)
		}
	}
}
