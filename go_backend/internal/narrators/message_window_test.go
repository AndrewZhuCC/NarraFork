package narrators

import "testing"

// TestNormalizeWindowSizeMatchesTS pins normalizeWindowSize to the TS
// parseWindowSize clamp Math.min(Math.max(value, 0), 100)
// (server/routes/narrators.ts). The NaN->fallback step is handled by queryInt at
// the handler, so this is purely the [0,100] clamp: negatives clamp to 0 (NOT to
// the 5/20 default) and values over 100 clamp to 100.
func TestNormalizeWindowSizeMatchesTS(t *testing.T) {
	cases := map[int]int{
		0:    0,
		5:    5,
		20:   20,
		100:  100,
		101:  100,
		9999: 100,
		-1:   0,
		-50:  0,
	}
	for in, want := range cases {
		if got := normalizeWindowSize(in); got != want {
			t.Fatalf("normalizeWindowSize(%d) = %d, want %d", in, got, want)
		}
	}
}

// TestNormalizeMessageLimitMatchesTS pins normalizeMessageLimit to the TS cursor
// limit handling Math.min(rawLimit, 200) with a 50 fallback for non-positive
// (NaN/<=0) input (server/routes/narrators.ts uses 50 default, 200 cap).
func TestNormalizeMessageLimitMatchesTS(t *testing.T) {
	cases := []struct {
		in   int
		want int
	}{
		{0, 50},    // non-positive -> fallback
		{-5, 50},   // negative -> fallback
		{1, 1},     // smallest valid
		{50, 50},   // default value
		{200, 200}, // at cap
		{201, 200}, // over cap
		{99999, 200},
	}
	for _, tc := range cases {
		if got := normalizeMessageLimit(tc.in, 50, 200); got != tc.want {
			t.Fatalf("normalizeMessageLimit(%d,50,200) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

// TestQueryIntMatchesNumberParseInt pins the pagination param parser to the TS
// Number.parseInt(value, 10) semantics used across server/routes/narrators.ts
// (limit/before/after). A leading integer prefix is taken and trailing
// non-digits are ignored ("50x" -> 50, "12.9" -> 12, "0x10" -> 0), while a value
// with no leading digits falls back. strconv.Atoi would instead reject
// "50x"/"12.9" wholesale and wrongly use the fallback.
func TestQueryIntMatchesNumberParseInt(t *testing.T) {
	const fallback = 20
	cases := []struct {
		in   string
		want int
	}{
		{"42", 42},
		{"50x", 50},
		{"12.9", 12},
		{"0x10", 0}, // base 10: stops at 'x'
		{"-5", -5},
		{"+7", 7},
		{"  9  ", 9},
		{"007", 7},
		{"", fallback},
		{"abc", fallback},
		{"x9", fallback},
		{"   ", fallback},
		{"-", fallback},
	}
	for _, tc := range cases {
		if got := queryInt(tc.in, fallback); got != tc.want {
			t.Fatalf("queryInt(%q, %d) = %d, want %d", tc.in, fallback, got, tc.want)
		}
	}
}

// TestParseIntJSReportsNaNForBlockIndexValidation pins the parseIntJS (int, ok)
// contract used by the blockIndex routes (deleteMessageBlock, rollback-preview):
// the TS guard is Number.isNaN(parseInt(v,10)) || v < 0. The ok flag must be
// false only when there are no leading digits (NaN), and a leading-digit prefix
// like "3abc" must parse to 3 with ok=true (strconv.Atoi would report an error
// and wrongly reject it).
func TestParseIntJSReportsNaNForBlockIndexValidation(t *testing.T) {
	cases := []struct {
		in     string
		want   int
		wantOK bool
	}{
		{"0", 0, true},
		{"3", 3, true},
		{"3abc", 3, true},
		{"12.9", 12, true},
		{"-1", -1, true}, // parses; the handler's <0 check rejects it, not parseIntJS
		{"", 0, false},
		{"abc", 0, false},
		{"x", 0, false},
		{"   ", 0, false},
	}
	for _, tc := range cases {
		got, ok := parseIntJS(tc.in)
		if ok != tc.wantOK || (ok && got != tc.want) {
			t.Fatalf("parseIntJS(%q) = (%d, %v), want (%d, %v)", tc.in, got, ok, tc.want, tc.wantOK)
		}
	}
}
