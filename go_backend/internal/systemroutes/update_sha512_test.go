package systemroutes

import (
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"testing"
)

// TestUpdateSHA512MatchesBase64ParityAndSuperset locks updateSHA512Matches.
//
// The TS update-service verifies with strict exact base64 equality
// (computeFileSha512 -> digest("base64"); actualSha512 !== releaseInfo.sha512),
// so the BASE64 path is the parity-critical case and must match exactly (Go uses
// base64.StdEncoding, equal to Node's digest("base64")). Go additionally accepts
// a hex digest (case-insensitive) and a leading "sha512-" prefix - a benign
// SUPERSET that still requires the correct hash in a recognized encoding; it
// never accepts a wrong or empty hash. This documents/guards both the base64
// parity and the deliberate extra-tolerance.
func TestUpdateSHA512MatchesBase64ParityAndSuperset(t *testing.T) {
	sum := sha512.Sum512([]byte("narrafork update artifact"))
	wantHex := hex.EncodeToString(sum[:])
	wantB64 := base64.StdEncoding.EncodeToString(sum[:])

	// Parity-critical: exact base64 match (the form TS produces/expects).
	if !updateSHA512Matches(wantB64, wantHex, wantB64) {
		t.Fatalf("base64 digest should match (TS parity path)")
	}
	// Superset: hex match, case-insensitive.
	if !updateSHA512Matches(wantHex, wantHex, wantB64) {
		t.Fatalf("hex digest should match (Go superset)")
	}
	if !updateSHA512Matches(toUpperASCII(wantHex), wantHex, wantB64) {
		t.Fatalf("upper-case hex digest should match (case-insensitive)")
	}
	// Superset: "sha512-" prefix stripped before comparison.
	if !updateSHA512Matches("sha512-"+wantB64, wantHex, wantB64) {
		t.Fatalf("sha512--prefixed base64 should match (Go superset)")
	}

	// Rejections: empty and wrong hashes never match.
	if updateSHA512Matches("", wantHex, wantB64) {
		t.Fatalf("empty expected must not match")
	}
	otherSum := sha512.Sum512([]byte("different content"))
	otherB64 := base64.StdEncoding.EncodeToString(otherSum[:])
	if updateSHA512Matches(otherB64, wantHex, wantB64) {
		t.Fatalf("a different (wrong) hash must not match")
	}
	// base64 comparison is case-SENSITIVE (only hex is case-insensitive); a
	// case-mangled base64 must not match.
	if updateSHA512Matches(mangleBase64Case(wantB64), wantHex, wantB64) {
		t.Fatalf("case-mangled base64 must not match (only hex is case-insensitive)")
	}
}

func toUpperASCII(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'a' && b[i] <= 'z' {
			b[i] -= 'a' - 'A'
		}
	}
	return string(b)
}

// mangleBase64Case flips the case of the first ASCII letter found, producing a
// base64 string that differs from the original under case-sensitive comparison.
func mangleBase64Case(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'a' && b[i] <= 'z' {
			b[i] -= 'a' - 'A'
			return string(b)
		}
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
			return string(b)
		}
	}
	return s
}
