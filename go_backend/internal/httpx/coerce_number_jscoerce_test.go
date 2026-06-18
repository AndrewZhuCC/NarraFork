package httpx

import (
	"math"
	"testing"
)

// TestCoerceNumberJSMatchesNumber pins CoerceNumberJS to JavaScript Number(value),
// the coercion Zod's z.coerce.number() applies. Unlike ParseIntJS (parseInt
// prefix semantics) and strconv.Atoi (whole-string integer only), Number()
// requires the whole trimmed string to be numeric but accepts decimals,
// scientific notation, explicit hex/octal/binary literals, surrounding
// whitespace, and the Infinity tokens; an empty/whitespace string is 0.
func TestCoerceNumberJSMatchesNumber(t *testing.T) {
	cases := []struct {
		in     string
		want   float64
		wantOK bool
	}{
		{"50.0", 50, true},
		{"1e2", 100, true},
		{"0x10", 16, true},
		{"0o17", 15, true},
		{"0b101", 5, true},
		{" 10 ", 10, true},
		{"", 0, true},
		{"  ", 0, true},
		{"5.5", 5.5, true},
		{"+5", 5, true},
		{"-3", -3, true},
		{".5", 0.5, true},
		{"5.", 5, true},
		{"42", 42, true},
		// NaN cases (Number() returns NaN -> ok=false).
		{"50x", 0, false},
		{"0x10p0", 0, false},
		{"abc", 0, false},
		{"1_000", 0, false},
		{"inf", 0, false},
		{"nan", 0, false},
		{"0x", 0, false},
	}
	for _, tc := range cases {
		got, ok := CoerceNumberJS(tc.in)
		if ok != tc.wantOK {
			t.Errorf("CoerceNumberJS(%q) ok = %v, want %v", tc.in, ok, tc.wantOK)
			continue
		}
		if ok && got != tc.want {
			t.Errorf("CoerceNumberJS(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}

	// Infinity tokens coerce to +/-Inf (caller's .int() rejects them).
	for _, in := range []string{"Infinity", "+Infinity"} {
		got, ok := CoerceNumberJS(in)
		if !ok || !math.IsInf(got, 1) {
			t.Errorf("CoerceNumberJS(%q) = %v ok=%v, want +Inf", in, got, ok)
		}
	}
	if got, ok := CoerceNumberJS("-Infinity"); !ok || !math.IsInf(got, -1) {
		t.Errorf("CoerceNumberJS(-Infinity) = %v ok=%v, want -Inf", got, ok)
	}
}

// TestCoerceIntJSMatchesCoerceNumberInt pins CoerceIntJS to
// z.coerce.number().int(): a value coerces like Number() and must be a finite
// integer. Fractional values, Infinity, and NaN are rejected, while integral
// floats ("50.0"), scientific ("1e2"), and hex ("0x10") succeed.
func TestCoerceIntJSMatchesCoerceNumberInt(t *testing.T) {
	cases := []struct {
		in     string
		want   int
		wantOK bool
	}{
		{"50", 50, true},
		{"50.0", 50, true},
		{"1e2", 100, true},
		{"0x10", 16, true},
		{" 10 ", 10, true},
		{"", 0, true},
		{"-3", -3, true},
		// Rejected: fractional, NaN, infinite.
		{"5.5", 0, false},
		{"50x", 0, false},
		{"Infinity", 0, false},
		{"-Infinity", 0, false},
		{"abc", 0, false},
	}
	for _, tc := range cases {
		got, ok := CoerceIntJS(tc.in)
		if ok != tc.wantOK {
			t.Errorf("CoerceIntJS(%q) ok = %v, want %v", tc.in, ok, tc.wantOK)
			continue
		}
		if ok && got != tc.want {
			t.Errorf("CoerceIntJS(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
