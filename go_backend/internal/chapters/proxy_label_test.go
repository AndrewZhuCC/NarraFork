package chapters

import "testing"

// TestGenerateProxyLabelMatchesTS pins generateProxyLabel to the TypeScript
// generateProxyLabel (server/services/container-proxy.ts):
// `${chapterShortId}-${serviceName.toLowerCase().replace(/[^a-z0-9-]/g, "-")}-${port}`.
// Each non-[a-z0-9-] char becomes a single dash; existing dashes are kept and
// consecutive dashes are NOT collapsed; there is no whitespace trim, no
// leading/trailing dash trim, and no "service" fallback for empty input.
func TestGenerateProxyLabelMatchesTS(t *testing.T) {
	cases := []struct {
		short   string
		service string
		port    int
		want    string
	}{
		{"ab12cd", "web", 3000, "ab12cd-web-3000"},
		{"ab12cd", "Web", 3000, "ab12cd-web-3000"},
		// Existing dashes kept, consecutive dashes preserved (NOT collapsed).
		{"ab12cd", "my--service", 8080, "ab12cd-my--service-8080"},
		// Dots/underscores/special chars each become a single dash.
		{"ab12cd", "api.v1_test", 80, "ab12cd-api-v1-test-80"},
		// Leading/trailing non-alnum -> dashes kept (no trim).
		{"ab12cd", "  web  ", 5000, "ab12cd---web---5000"},
		// All-invalid -> all dashes (no "service" fallback): 1 sep + 3 + 1 sep.
		{"ab12cd", "@@@", 9000, "ab12cd-----9000"},
		// Empty service -> empty middle segment.
		{"ab12cd", "", 1234, "ab12cd--1234"},
	}
	for _, tc := range cases {
		if got := generateProxyLabel(tc.short, tc.service, tc.port); got != tc.want {
			t.Fatalf("generateProxyLabel(%q,%q,%d) = %q, want %q", tc.short, tc.service, tc.port, got, tc.want)
		}
	}
}

// TestBuildProxyURLMatchesTS pins buildProxyURL to the TypeScript buildProxyUrl:
// ONLY port 80 omits the port suffix; every other port (including 0) appends it.
func TestBuildProxyURLMatchesTS(t *testing.T) {
	cases := []struct {
		label  string
		domain string
		port   int
		want   string
	}{
		{"ab-web-80", "example.test", 80, "http://ab-web-80.example.test"},
		{"ab-web-3000", "example.test", 3000, "http://ab-web-3000.example.test:3000"},
		// Port 0 must be appended (TS only special-cases 80).
		{"ab-web-0", "example.test", 0, "http://ab-web-0.example.test:0"},
		{"ab-web-443", "dev.example.test", 443, "http://ab-web-443.dev.example.test:443"},
	}
	for _, tc := range cases {
		if got := buildProxyURL(tc.label, tc.domain, tc.port); got != tc.want {
			t.Fatalf("buildProxyURL(%q,%q,%d) = %q, want %q", tc.label, tc.domain, tc.port, got, tc.want)
		}
	}
}
