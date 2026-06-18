package httpx

import (
	"strings"
	"testing"
)

// TestJSStringLength pins JSStringLength to JavaScript String.prototype.length
// (UTF-16 code units), which is what the TS backend's Zod .min()/.max() string
// checks count. ASCII and BMP characters are 1 unit each; astral characters
// (e.g. emoji) are 2 units each. utf8.RuneCountInString would report 1 per
// astral char and thus over-accept input that the TS backend rejects.
func TestJSStringLength(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"empty", "", 0},
		{"ascii", "hello", 5},
		{"cjk", strings.Repeat("章", 200), 200},
		{"emoji", "😀", 2},
		{"emoji-repeat", strings.Repeat("😀", 101), 202},
		{"mixed", "a章😀", 1 + 1 + 2},
	}
	for _, tc := range cases {
		if got := JSStringLength(tc.in); got != tc.want {
			t.Fatalf("%s: JSStringLength = %d, want %d", tc.name, got, tc.want)
		}
	}
}

// TestJSStringSlicePrefix pins JSStringSlicePrefix to JavaScript
// value.slice(0, end) over UTF-16 code units: ASCII/CJK chars are 1 unit and an
// emoji is 2 units, so slicing must cut at the same boundary as TS and never
// split a multi-byte rune into invalid UTF-8.
func TestJSStringSlicePrefix(t *testing.T) {
	if got := JSStringSlicePrefix("hello", 3); got != "hel" {
		t.Fatalf("ascii slice = %q, want hel", got)
	}
	// end >= length returns the whole string.
	if got := JSStringSlicePrefix("hi", 5); got != "hi" {
		t.Fatalf("over-length slice = %q, want hi", got)
	}
	if got := JSStringSlicePrefix("anything", 0); got != "" {
		t.Fatalf("zero slice should be empty, got %q", got)
	}
	// 200 CJK chars sliced to 160 keeps exactly 160 chars (480 bytes), valid UTF-8.
	cjk := strings.Repeat("章", 200)
	got := JSStringSlicePrefix(cjk, 160)
	if JSStringLength(got) != 160 || strings.Count(got, "章") != 160 {
		t.Fatalf("CJK slice to 160 kept %d chars (len units %d)", strings.Count(got, "章"), JSStringLength(got))
	}
	// Slicing an emoji at an even UTF-16 boundary keeps whole emoji intact.
	// "😀😀" is 4 UTF-16 units; slicing to 2 keeps exactly the first emoji.
	if got := JSStringSlicePrefix("😀😀", 2); got != "😀" {
		t.Fatalf("emoji slice to 2 units = %q, want one emoji", got)
	}
}

// TestJSStringSliceFromEnd pins JSStringSliceFromEnd to JavaScript
// String.prototype.slice(-n) semantics (last n UTF-16 code units), used by the
// settings maskApiKey parity (last 4 chars of a secret).
func TestJSStringSliceFromEnd(t *testing.T) {
	if got := JSStringSliceFromEnd("abcdef", 4); got != "cdef" {
		t.Fatalf("ascii slice(-4) = %q, want cdef", got)
	}
	// n >= length returns the whole string.
	if got := JSStringSliceFromEnd("ab", 4); got != "ab" {
		t.Fatalf("over-length slice(-4) = %q, want ab", got)
	}
	if got := JSStringSliceFromEnd("anything", 0); got != "" {
		t.Fatalf("zero slice should be empty, got %q", got)
	}
	// Last 4 of an all-CJK string keeps exactly 4 CJK chars (valid UTF-8), not a
	// byte slice that would cut mid-rune.
	cjk := strings.Repeat("章", 10)
	got := JSStringSliceFromEnd(cjk, 4)
	if JSStringLength(got) != 4 || strings.Count(got, "章") != 4 {
		t.Fatalf("CJK slice(-4) kept %d chars (len units %d), want 4", strings.Count(got, "章"), JSStringLength(got))
	}
	// "😀😀" is 4 UTF-16 units; slice(-2) keeps exactly the last emoji.
	if got := JSStringSliceFromEnd("😀😀", 2); got != "😀" {
		t.Fatalf("emoji slice(-2) = %q, want one emoji", got)
	}
}
