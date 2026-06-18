package narrators

import "testing"

// TestNormalizePermissionModeStrictValidationBoundary locks the strict
// validation gate used by the user-facing permission-mode endpoints
// (updatePermissionMode and the generic column update): the 5 canonical modes
// and the recognized legacy/synonym aliases are accepted (ok=true) and mapped to
// canonical values, while genuinely-unknown strings are rejected (ok=false) so
// the API returns 400 rather than persisting an arbitrary mode.
//
// Note: the danger synonyms (danger-full-access/unrestricted/bypass/trust-all ->
// bypassPermissions) are a documented Go-only leniency (phase0 matrix
// testOnlyDriftExamples / "centralize Go enum constants" remainingGap); TS's
// legacyPermissionModeSchema only recognizes allowByDefault/denyByDefault/plan.
// This test pins the CURRENT Go behavior so any future centralization toward the
// TS enum set is a deliberate, visible change rather than a silent drift.
func TestNormalizePermissionModeStrictValidationBoundary(t *testing.T) {
	accepted := map[string]string{
		"default":           "default",
		"acceptEdits":       "acceptEdits",
		"bypassPermissions": "bypassPermissions",
		"readOnly":          "readOnly",
		"dontAsk":           "dontAsk",
		// TS-recognized legacy aliases.
		"allowByDefault": "acceptEdits",
		"denyByDefault":  "dontAsk",
		"plan":           "default",
		// Go-only documented synonyms (drift, intentionally accepted).
		"danger-full-access": "bypassPermissions",
		"unrestricted":       "bypassPermissions",
		"bypass":             "bypassPermissions",
		"trust-all":          "bypassPermissions",
		// Surrounding whitespace is trimmed.
		"  readOnly  ": "readOnly",
	}
	for in, want := range accepted {
		got, ok := normalizePermissionModeStrict(in)
		if !ok || got != want {
			t.Fatalf("normalizePermissionModeStrict(%q)=(%q,%v) want (%q,true)", in, got, ok, want)
		}
	}

	rejected := []string{"", "   ", "garbage", "READONLY", "Default", "allow", "deny", "full"}
	for _, in := range rejected {
		if got, ok := normalizePermissionModeStrict(in); ok {
			t.Fatalf("normalizePermissionModeStrict(%q) accepted as %q, want rejected", in, got)
		}
	}
}
