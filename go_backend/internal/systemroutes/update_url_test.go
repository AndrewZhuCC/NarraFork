package systemroutes

import "testing"

// TestTrustedUpdateAbsoluteURL locks the update download-URL resolution, which is
// a security hardening OVER the TS update-service: TS blindly concatenates
// `${serverUrl}${step.url}` assuming a relative path, whereas Go resolves
// relative paths against the server base AND rejects cross-origin absolute URLs
// so a malicious update server cannot redirect a download to another host.
func TestTrustedUpdateAbsoluteURL(t *testing.T) {
	const server = "https://updates.example.com"

	cases := []struct {
		name  string
		value string
		want  string
	}{
		// Absolute server path -> joined to the base origin (matches TS for the
		// common leading-slash case).
		{"abs-path", "/patches/p.zst", "https://updates.example.com/patches/p.zst"},
		// Relative path without leading slash -> Go normalizes to a rooted path
		// (TS would produce a broken concatenation here).
		{"rel-path", "patches/p.zst", "https://updates.example.com/patches/p.zst"},
		// Same-origin absolute URL -> accepted unchanged.
		{"same-origin-abs", "https://updates.example.com/d/p.zst", "https://updates.example.com/d/p.zst"},
		// Cross-origin absolute URL -> REJECTED (empty) to block redirection.
		{"cross-origin-abs", "https://evil.example/p.zst", ""},
		// Non-http(s) scheme -> rejected.
		{"file-scheme", "file:///etc/passwd", ""},
		// Empty -> empty.
		{"empty", "", ""},
		// Same host, different scheme (http vs https) -> rejected (scheme must match).
		{"scheme-mismatch", "http://updates.example.com/p.zst", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := trustedUpdateAbsoluteURL(server, tc.value); got != tc.want {
				t.Fatalf("trustedUpdateAbsoluteURL(%q) = %q, want %q", tc.value, got, tc.want)
			}
		})
	}
}

// TestTrustedUpdateArtifactURL locks the artifact download URL construction
// (path-escaped product/version/filename under /api/v2/products/.../download/).
func TestTrustedUpdateArtifactURL(t *testing.T) {
	source := trustedUpdateSource{ServerURL: "https://updates.example.com", Product: "narrafork-go", Channel: "stable", Platform: "linux-x64"}
	got := trustedUpdateArtifactURL(source, "1.3.0", "narrafork-1.3.0")
	want := "https://updates.example.com/api/v2/products/narrafork-go/releases/1.3.0/download/narrafork-1.3.0"
	if got != want {
		t.Fatalf("trustedUpdateArtifactURL = %q, want %q", got, want)
	}
	// Path-escaping of unusual characters in the version/filename segments.
	got = trustedUpdateArtifactURL(source, "1.3.0 beta", "a b.bin")
	want = "https://updates.example.com/api/v2/products/narrafork-go/releases/1.3.0%20beta/download/a%20b.bin"
	if got != want {
		t.Fatalf("trustedUpdateArtifactURL escaped = %q, want %q", got, want)
	}
}
