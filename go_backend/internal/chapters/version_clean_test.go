package chapters

import "testing"

// TestCleanVersionStringStripsProviderNoise verifies the container-setup version
// fields are cleaned of combined stdout+stderr noise (provider banner, ANSI,
// blank lines), matching the TypeScript stdout-only trimmed version output. The
// `podman compose version` real output prepends a ">>>> Executing external
// compose provider" banner on stderr that runLimited merges in.
func TestCleanVersionStringStripsProviderNoise(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"clean podman", "podman version 5.4.2", "podman version 5.4.2"},
		{"trailing newline", "podman version 5.4.2\n", "podman version 5.4.2"},
		{
			"compose provider banner",
			">>>> Executing external compose provider \"/home/u/.local/bin/podman-compose\". <<<<\n\npodman version 5.4.2\npodman-compose version 1.5.0",
			"podman-compose version 1.5.0",
		},
		{
			"ansi banner",
			"\x1b[4m>>>> Executing external compose provider <<<<\x1b[0m\n\npodman version 5.4.2",
			"podman version 5.4.2",
		},
		{"no version keyword falls back to last line", "weird-tool-output", "weird-tool-output"},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cleanVersionString(tc.in); got != tc.want {
				t.Fatalf("cleanVersionString(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestCleanComposeVersionStringExtractsNumber mirrors the TS regex extraction:
// "podman-compose version X.Y.Z" yields the bare number, otherwise falls back to
// the cleaned last line.
func TestCleanComposeVersionStringExtractsNumber(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			"podman compose provider banner then versions",
			">>>> Executing external compose provider <<<<\n\npodman version 5.4.2\npodman-compose version 1.5.0",
			"1.5.0",
		},
		{"direct podman-compose --version", "podman version 5.4.2\npodman-compose version 1.5.0", "1.5.0"},
		{"ansi noise around", "\x1b[4mbanner\x1b[0m\npodman-compose version 1.0.3\n", "1.0.3"},
		{"no podman-compose line falls back", "docker compose version v2.20.0", "docker compose version v2.20.0"},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cleanComposeVersionString(tc.in); got != tc.want {
				t.Fatalf("cleanComposeVersionString(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestIsVersionNumber(t *testing.T) {
	cases := map[string]bool{
		"1.5.0":   true,
		"5":       true,
		"1.2.3.4": true,
		"v2.0":    false, // leading v is not digits/dots
		"1.x.0":   false,
		"":        false,
		"...":     false,
	}
	for in, want := range cases {
		if got := isVersionNumber(in); got != want {
			t.Fatalf("isVersionNumber(%q) = %v, want %v", in, got, want)
		}
	}
}
