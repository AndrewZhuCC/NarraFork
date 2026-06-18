package git

import "testing"

// TestIsCloneAuthError covers the clone-failure classifier that decides whether a
// failed clone should surface as an auth error (which drives the credential
// prompt) versus a generic clone failure. Matching is case-insensitive across the
// known git auth-failure phrases.
func TestIsCloneAuthError(t *testing.T) {
	authMessages := []string{
		"fatal: Authentication failed for 'https://example/repo.git'",
		"could not read Username for 'https://github.com'",
		"fatal: could not read Password",
		"terminal prompts disabled",
		"remote: Invalid credentials",
		"remote: HTTP Basic: Logon failed",
		"git@host: Permission denied (publickey).",
		"AUTHENTICATION FAILED", // case-insensitive
	}
	for _, msg := range authMessages {
		if !isCloneAuthError(msg) {
			t.Fatalf("expected %q to be classified as an auth error", msg)
		}
	}

	nonAuthMessages := []string{
		"fatal: repository 'https://example/repo.git' not found",
		"fatal: unable to access: Could not resolve host",
		"error: RPC failed; curl 56",
		"",
	}
	for _, msg := range nonAuthMessages {
		if isCloneAuthError(msg) {
			t.Fatalf("expected %q NOT to be classified as an auth error", msg)
		}
	}
}

// TestCloneCredentialsFromURL covers extracting embedded user:password from a
// clone URL: present credentials are parsed, URLs without userinfo return nil,
// and a username-only URL still yields credentials.
func TestCloneCredentialsFromURL(t *testing.T) {
	if got := cloneCredentialsFromURL("https://user:pass@github.com/o/r.git"); got == nil ||
		got.Username != "user" || got.Password != "pass" {
		t.Fatalf("expected user:pass extraction, got %#v", got)
	}
	if got := cloneCredentialsFromURL("https://token@github.com/o/r.git"); got == nil ||
		got.Username != "token" || got.Password != "" {
		t.Fatalf("expected username-only extraction, got %#v", got)
	}
	if got := cloneCredentialsFromURL("https://github.com/o/r.git"); got != nil {
		t.Fatalf("URL without userinfo should yield nil, got %#v", got)
	}
	if got := cloneCredentialsFromURL("not a url at all"); got != nil {
		t.Fatalf("unparsable URL should yield nil, got %#v", got)
	}
}
