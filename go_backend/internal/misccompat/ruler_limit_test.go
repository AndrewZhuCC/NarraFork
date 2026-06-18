package misccompat

import (
	"net/url"
	"testing"
)

// TestNormalizeRulerLimitIsGoOnlyBoundedGuard documents and locks normalizeRulerLimit
// as a GO-ONLY commit-page bound on GET /:id/ruler. The TS ruler route
// (server/routes/ruler.ts) applies NO upper bound to the limit query param
// (limit = limitParam ? parseInt(limitParam) : 200) and feeds it straight to
// git log -n. Go caps it at 200 to avoid an unbounded git log read in the request
// path (backend performance rule). This is functionally equivalent for the real
// frontend, which requests limit<=200 and pages via cursor (useRuler.ts limit:200).
func TestNormalizeRulerLimitIsGoOnlyBoundedGuard(t *testing.T) {
	cases := []struct {
		in   int
		want int
	}{
		{0, 200},      // non-positive -> default
		{-5, 200},     // negative -> default
		{1, 1},        // small valid value preserved
		{50, 50},      // mid valid value preserved
		{200, 200},    // exactly the cap preserved
		{201, 200},    // over the cap -> clamped to 200
		{999999, 200}, // far over the cap -> clamped to 200 (the TS-unbounded case)
	}
	for _, tc := range cases {
		if got := normalizeRulerLimit(tc.in); got != tc.want {
			t.Fatalf("normalizeRulerLimit(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

// TestIntMapOrMatchesNumberParseInt pins the ruler limit/skip query parser to the
// TS Number.parseInt(param, 10) semantics: a leading-digit prefix like "5x"
// parses to 5 and "12.9" to 12; a value with no leading digits (or absent/
// whitespace) falls back. strconv.Atoi would reject "5x"/"12.9" wholesale and
// wrongly fall back, diverging from TS for e.g. ?skip=5x.
func TestIntMapOrMatchesNumberParseInt(t *testing.T) {
	const fallback = 0
	cases := []struct {
		raw  string // the "skip" query value; "" means the key is absent
		want int
	}{
		{"5", 5},
		{"5x", 5},
		{"12.9", 12},
		{"0x10", 0}, // base 10 stops at 'x'
		{"-3", -3},
		{"  9  ", 9},
		{"", fallback},    // absent key
		{"abc", fallback}, // no leading digits
		{"   ", fallback}, // whitespace only -> trims to empty -> fallback
	}
	for _, tc := range cases {
		values := url.Values{}
		if tc.raw != "" {
			values.Set("skip", tc.raw)
		}
		if got := intMapOr(values, "skip", fallback); got != tc.want {
			t.Fatalf("intMapOr(skip=%q) = %d, want %d", tc.raw, got, tc.want)
		}
	}
	// A nil url.Values must also fall back.
	if got := intMapOr(nil, "skip", fallback); got != fallback {
		t.Fatalf("intMapOr(nil) = %d, want %d", got, fallback)
	}
}
