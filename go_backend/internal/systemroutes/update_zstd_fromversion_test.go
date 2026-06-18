package systemroutes

import "testing"

// TestTrustedUpdateVersionMatches pins the fromVersion equality gate to the TS
// `data.zstdPatch.fromVersion === APP_VERSION` check (server/services/update-service.ts):
// a patch is usable only when its declared base version equals the current build
// version; an empty fromVersion never matches.
func TestTrustedUpdateVersionMatches(t *testing.T) {
	cases := []struct {
		from    string
		current string
		want    bool
	}{
		{"1.2.3", "1.2.3", true},
		{"v1.2.3", "1.2.3", true},  // leading v ignored defensively
		{"1.2.3", "v1.2.3", true},  //
		{" 1.2.3 ", "1.2.3", true}, // trimmed
		{"1.2.2", "1.2.3", false},  // mismatch -> not usable
		{"", "1.2.3", false},       // empty never matches
		{"1.2.3", "", false},       // empty current
		{"1.2.3-beta", "1.2.3", false},
	}
	for _, tc := range cases {
		if got := trustedUpdateVersionMatches(tc.from, tc.current); got != tc.want {
			t.Fatalf("trustedUpdateVersionMatches(%q,%q) = %v, want %v", tc.from, tc.current, got, tc.want)
		}
	}
}

// TestTrustedUpdateReleaseZstdStrategyRequiresMatchingFromVersion verifies that
// trustedUpdateReleaseFromV2 only selects the zstd delta-patch download strategy
// when zstdPatch.fromVersion matches the current build version, mirroring TS.
// A mismatched fromVersion must fall through to a full download (strategy "",
// downloadSize == full file size) rather than applying a delta patch from the
// wrong base version (which would corrupt the binary).
func TestTrustedUpdateReleaseZstdStrategyRequiresMatchingFromVersion(t *testing.T) {
	source := trustedUpdateSource{ServerURL: "https://updates.example", Product: "narrafork-go", Channel: "stable", Platform: "linux-x64"}

	build := func(fromVersion string) map[string]any {
		return map[string]any{
			"updateAvailable": true,
			"version":         "1.3.0",
			"file":            map[string]any{"filename": "narrafork-1.3.0", "size": float64(10_000_000), "sha512": "abc"},
			"zstdPatch": map[string]any{
				"url":         "/patches/p.zst",
				"metaUrl":     "/patches/p.json",
				"fromVersion": fromVersion,
				"patchSize":   float64(500_000),
			},
		}
	}

	// Matching fromVersion -> zstd strategy with the small patch download size.
	matched := trustedUpdateReleaseFromV2(source, "1.2.5", build("1.2.5"))
	if !matched.Available {
		t.Fatalf("expected available release")
	}
	if matched.Strategy != "zstd" {
		t.Fatalf("matching fromVersion strategy = %q, want zstd", matched.Strategy)
	}
	if matched.DownloadSize != 500_000 {
		t.Fatalf("matching fromVersion downloadSize = %d, want 500000 (patch size)", matched.DownloadSize)
	}

	// Mismatched fromVersion -> NOT zstd; full download size.
	mismatched := trustedUpdateReleaseFromV2(source, "1.2.5", build("1.0.0"))
	if !mismatched.Available {
		t.Fatalf("expected available release even with mismatched patch")
	}
	if mismatched.Strategy == "zstd" {
		t.Fatalf("mismatched fromVersion must NOT select zstd strategy")
	}
	if mismatched.DownloadSize != 10_000_000 {
		t.Fatalf("mismatched fromVersion downloadSize = %d, want 10000000 (full file)", mismatched.DownloadSize)
	}
}
