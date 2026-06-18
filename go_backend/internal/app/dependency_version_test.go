package app

import "testing"

// TestExtractToolVersionMatchesTypeScriptRegex verifies the dependency-status
// version parser extracts the bare dotted-numeric version like the TypeScript
// dependency-service regexes (/git version ([\d.]+)/, /ripgrep ([\d.]+)/),
// rather than returning the full "git version X.Y.Z" string. This is the
// cross-backend display parity fix for GET /api/dependencies.
func TestExtractToolVersionMatchesTypeScriptRegex(t *testing.T) {
	cases := []struct {
		name   string
		out    string
		marker string
		want   string
	}{
		{"git real", "git version 2.47.3", "git version", "2.47.3"},
		{"git trailing newline", "git version 2.43.0\n", "git version", "2.43.0"},
		{"git windows suffix", "git version 2.43.0.windows.1", "git version", "2.43.0"},
		{"rg real", "ripgrep 14.1.1", "ripgrep", "14.1.1"},
		{"rg multiline", "ripgrep 14.1.0\n\nfeatures:+pcre2", "ripgrep", "14.1.0"},
		{"rg paren suffix", "ripgrep 13.0.0 (rev abc)", "ripgrep", "13.0.0"},
		{"marker absent", "some other tool 1.2.3", "git version", ""},
		{"no numeric token", "git version unknown", "git version", ""},
		{"empty", "", "git version", ""},
		{"case insensitive marker", "GIT VERSION 2.40.0", "git version", "2.40.0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractToolVersion(tc.out, tc.marker); got != tc.want {
				t.Fatalf("extractToolVersion(%q, %q) = %q, want %q", tc.out, tc.marker, got, tc.want)
			}
		})
	}
}

func TestLeadingVersionNumber(t *testing.T) {
	cases := map[string]string{
		"2.43.0":           "2.43.0",
		"14.1.1":           "14.1.1",
		"2.43.0.windows.1": "2.43.0", // digit/dot run stops at 'w'
		"13.0.0)":          "13.0.0",
		"5":                "5",
		"v2.0":             "", // leading 'v' is not a digit
		"abc":              "",
		"":                 "",
		".":                "",
		"1.2.3-beta":       "1.2.3",
	}
	for in, want := range cases {
		if got := leadingVersionNumber(in); got != want {
			t.Fatalf("leadingVersionNumber(%q) = %q, want %q", in, got, want)
		}
	}
}
