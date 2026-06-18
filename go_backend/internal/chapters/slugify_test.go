package chapters

import (
	"strings"
	"testing"
)

// TestSlugifyMatchesTypeScriptSemantics pins slugify to the TypeScript
// server/lib/slug.ts behavior: NFKD normalize + strip diacritics, keep Unicode
// letters/digits (CJK preserved), collapse other runs to "-", trim dashes,
// lowercase, cap at 30 runes, and fall back to "chapter" for empty results.
func TestSlugifyMatchesTypeScriptSemantics(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"ascii words", "Hello World", "hello-world"},
		{"surrounding whitespace", "  Trim Me  ", "trim-me"},
		{"special runs collapse", "a---b!!!c", "a-b-c"},
		{"leading and trailing symbols", "***edge***", "edge"},
		{"empty", "", "chapter"},
		{"only symbols", "!@#$%", "chapter"},
		{"cjk preserved with ascii", "重构 API", "重构-api"},
		{"pure cjk preserved", "测试", "测试"},
		{"diacritics stripped", "Café Münu", "cafe-munu"},
		{"digits kept", "Release v2 Build 3", "release-v2-build-3"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := slugify(tc.in); got != tc.want {
				t.Fatalf("slugify(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestSlugifyCapsAtUTF16Units verifies the 30-UTF-16-code-unit cap matching the
// TS slugify `.slice(0, 30)` (server/lib/slug.ts): the cap counts UTF-16 units
// (astral letters = 2 units each) and is applied with NO post-slice dash trim, so
// a cut that lands on a dash keeps the trailing dash exactly like TS.
func TestSlugifyCapsAtUTF16Units(t *testing.T) {
	// Long ASCII: 30 UTF-16 units. The TS slice does NOT post-trim, and for this
	// input char 30 is a digit (verified against bun), so no trailing dash.
	long := "abcdefghij-klmnopqrst-uvwxyz0123-456789"
	if got := slugify(long); got != "abcdefghij-klmnopqrst-uvwxyz01" {
		t.Fatalf("slugify(long ascii) = %q, want abcdefghij-klmnopqrst-uvwxyz01", got)
	}

	// Trailing-dash case: the 30th UTF-16 unit lands on a dash, which TS keeps
	// (verified against bun: "abcdefghijklmnopqrstuvwxyz123-").
	if got := slugify("abcdefghijklmnopqrstuvwxyz123-xyz"); got != "abcdefghijklmnopqrstuvwxyz123-" {
		t.Fatalf("slugify(trailing dash) = %q, want trailing dash preserved like TS", got)
	}

	// Long BMP CJK: each rune is 1 UTF-16 unit, so 30 units = 30 runes.
	cjk := "一二三四五六七八九十一二三四五六七八九十一二三四五六七八九十一二三四五"
	gotCJK := slugify(cjk)
	if r := []rune(gotCJK); len(r) != 30 {
		t.Fatalf("slugify long BMP cjk = %d runes, want 30 (%q)", len(r), gotCJK)
	}

	// Astral CJK (U+20000, CJK Ext-B): each is 2 UTF-16 units, so the 30-unit cap
	// keeps 15 runes, matching TS slice(0,30). A rune-count cap would wrongly keep 30.
	astral := strings.Repeat("\U00020000", 40)
	gotAstral := slugify(astral)
	if r := []rune(gotAstral); len(r) != 15 {
		t.Fatalf("slugify astral CJK = %d runes, want 15 (UTF-16 cap), got %q", len(r), gotAstral)
	}
}
