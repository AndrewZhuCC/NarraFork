package auth

import (
	"strings"
	"testing"
)

// TestIsValidEmailMatchesZodEmailRegex pins isValidEmail to the Zod v4
// z.string().email() "practical email" regex used for gitEmail
// (server/lib/validators/auth.ts; node_modules/zod/v4/core/regexes.js):
//
//	/^(?!\.)(?!.*\.\.)([A-Za-z0-9_'+\-\.]*)[A-Za-z0-9_+-]@([A-Za-z0-9][A-Za-z0-9\-]*\.)+[A-Za-z]{2,}$/
//
// The previous net/mail.ParseAddress implementation was RFC-5322 permissive and
// wrongly accepted forms Zod rejects (notably a dotless domain like "user@localhost"
// or "a@b"). The accept/reject expectations below were captured from the real Zod v4
// safeParse output for each input.
func TestIsValidEmailMatchesZodEmailRegex(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"a@b.com", true},
		{"user@example.com", true},
		{"user.name+tag@example.co.uk", true},
		{"a@b", false},                        // no dotted TLD
		{"user@localhost", false},             // no dotted TLD (net/mail accepted this)
		{`"quoted local"@example.com`, false}, // quoted local part
		{"user@[127.0.0.1]", false},           // IP literal domain
		{"Display Name <a@b.com>", false},     // display-name form
		{"a@-b.com", false},                   // domain label must start with alphanumeric
		{"a..b@example.com", false},           // consecutive dots
		{".a@b.com", false},                   // leading dot
		{"a@b.c", false},                      // 1-char TLD (needs {2,})
		{"", false},                           // empty is not a valid email (callers bypass "" separately)
	}
	for _, tc := range cases {
		if got := isValidEmail(tc.in); got != tc.want {
			t.Fatalf("isValidEmail(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}

	// The key regression: a dotless domain must be rejected (net/mail accepted it).
	if isValidEmail("user@localhost") {
		t.Fatal("dotless-domain email must be rejected to match Zod")
	}
	// A 254-length-boundary-irrelevant well-formed address still passes (length is
	// enforced separately by the caller via JSStringLength <= 254).
	if !isValidEmail("u@" + strings.Repeat("a", 60) + ".com") {
		t.Fatal("well-formed long-domain email should pass the regex")
	}
}
