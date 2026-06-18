package narrators

import (
	"testing"

	"narrafork-go/internal/config"
)

// TestNormalizeOverrideHelpers covers the override normalizers: unknown/empty
// values fall back to "inherit"; only the documented enum values pass through.
func TestNormalizeOverrideHelpers(t *testing.T) {
	for _, in := range []string{"on", "off"} {
		if got := normalizeBooleanOverride(in); got != in {
			t.Fatalf("normalizeBooleanOverride(%q) = %q", in, got)
		}
	}
	for _, in := range []string{"inherit", "", "yes", "true", "weird"} {
		if got := normalizeBooleanOverride(in); got != "inherit" {
			t.Fatalf("normalizeBooleanOverride(%q) should be inherit, got %q", in, got)
		}
	}

	for _, in := range []string{"on", "off", "light", "standard", "strict"} {
		if got := normalizeDangerReflectionOverride(in); got != in {
			t.Fatalf("normalizeDangerReflectionOverride(%q) = %q", in, got)
		}
	}
	for _, in := range []string{"inherit", "", "loose", "STRICT"} {
		if got := normalizeDangerReflectionOverride(in); got != "inherit" {
			t.Fatalf("normalizeDangerReflectionOverride(%q) should be inherit, got %q", in, got)
		}
	}
}

// TestNormalizeDangerReflectionLevel covers the level normalizer including the
// legacy-enabled fallback: a recognized level passes through; an unrecognized
// value falls back to "standard" when legacyEnabled and "off" otherwise.
func TestNormalizeDangerReflectionLevel(t *testing.T) {
	if got := normalizeDangerReflectionLevel("strict", true); got != dangerReflectionStrict {
		t.Fatalf("recognized level should pass through, got %q", got)
	}
	if got := normalizeDangerReflectionLevel("LIGHT", true); got != dangerReflectionLight {
		t.Fatalf("level should be case-insensitive, got %q", got)
	}
	if got := normalizeDangerReflectionLevel("", true); got != dangerReflectionStandard {
		t.Fatalf("legacy-enabled fallback should be standard, got %q", got)
	}
	if got := normalizeDangerReflectionLevel("bogus", false); got != dangerReflectionOff {
		t.Fatalf("legacy-disabled fallback should be off, got %q", got)
	}
}

// TestEffectiveDangerReflectionLevelPrecedence pins the override precedence,
// especially the subtle "on" branch: override "on" maps to the global level
// unless the global is "off", in which case it becomes "standard"; explicit level
// overrides win outright; "inherit"/unknown defers to the global level.
func TestEffectiveDangerReflectionLevelPrecedence(t *testing.T) {
	withGlobalLevel := func(t *testing.T, level string) *config.Store {
		t.Helper()
		t.Setenv("HOME", t.TempDir())
		settings, err := config.Load()
		if err != nil {
			t.Fatalf("load settings: %v", err)
		}
		if _, err := settings.Patch(map[string]any{
			"agent": map[string]any{"dangerReflectionLevel": level},
		}); err != nil {
			t.Fatalf("patch settings: %v", err)
		}
		return settings
	}

	cases := []struct {
		name        string
		globalLevel string
		override    string
		want        dangerReflectionLevel
	}{
		{"inherit defers to global", "light", "inherit", dangerReflectionLight},
		{"unknown override defers to global", "strict", "bogus", dangerReflectionStrict},
		{"on with non-off global keeps global", "strict", "on", dangerReflectionStrict},
		{"on with off global becomes standard", "off", "on", dangerReflectionStandard},
		{"explicit off wins", "strict", "off", dangerReflectionOff},
		{"explicit light wins", "strict", "light", dangerReflectionLight},
		{"explicit strict wins over light global", "light", "strict", dangerReflectionStrict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			settings := withGlobalLevel(t, tc.globalLevel)
			narrator := Narrator{DangerReflectionOverride: tc.override}
			if got := effectiveDangerReflectionLevel(settings, narrator); got != tc.want {
				t.Fatalf("effectiveDangerReflectionLevel(global=%q, override=%q) = %q, want %q",
					tc.globalLevel, tc.override, got, tc.want)
			}
		})
	}
}
