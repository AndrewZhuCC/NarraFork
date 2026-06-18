package httpx

import "testing"

// TestParseIntJSMatchesNumberParseInt pins ParseIntJS to JavaScript
// Number.parseInt(value, 10): a leading integer prefix is taken and trailing
// non-digits are ignored ("50x" -> 50, "12.9" -> 12, "0x10" -> 0), an optional
// +/- sign and leading whitespace are honored, and a value with no leading
// digits reports NaN via ok=false. strconv.Atoi would instead reject the
// prefix cases wholesale.
func TestParseIntJSMatchesNumberParseInt(t *testing.T) {
	cases := []struct {
		in     string
		want   int
		wantOK bool
	}{
		{"42", 42, true},
		{"50x", 50, true},
		{"120x", 120, true},
		{"12.9", 12, true},
		{"0x10", 0, true}, // base 10 stops at 'x'
		{"-5", -5, true},
		{"+7", 7, true},
		{"  9  ", 9, true},
		{"007", 7, true},
		{"", 0, false},
		{"abc", 0, false},
		{"x9", 0, false},
		{"   ", 0, false},
		{"-", 0, false},
		{"+", 0, false},
	}
	for _, tc := range cases {
		got, ok := ParseIntJS(tc.in)
		if ok != tc.wantOK || (ok && got != tc.want) {
			t.Fatalf("ParseIntJS(%q) = (%d, %v), want (%d, %v)", tc.in, got, ok, tc.want, tc.wantOK)
		}
	}
}

// TestParseIntJSOrFallsBackOnNaN pins the fallback-returning form to the TS
// `Number.isNaN(parseInt(v)) ? fallback : parseInt(v)` idiom.
func TestParseIntJSOrFallsBackOnNaN(t *testing.T) {
	const fallback = 50
	cases := map[string]int{
		"100":  100,
		"100x": 100,
		"0":    0,
		"-3":   -3,
		"":     fallback,
		"abc":  fallback,
		"   ":  fallback,
	}
	for in, want := range cases {
		if got := ParseIntJSOr(in, fallback); got != want {
			t.Fatalf("ParseIntJSOr(%q, %d) = %d, want %d", in, fallback, got, want)
		}
	}
}
