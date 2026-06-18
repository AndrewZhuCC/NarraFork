package systemroutes

import (
	"strings"
	"testing"
)

// TestSanitizeNugUsageExtraValueUTF16 pins sanitizeNugUsageExtraValue to the TS
// safeUsageExtraValue (nug.ts:383): a string longer than 300 UTF-16 code units is
// truncated to `text.slice(0, 300)+"…"` (UTF-16). Go previously used byte length +
// a byte slice, which for a non-ASCII value diverged on the >300 gate and could cut
// mid-rune (invalid UTF-8).
func TestSanitizeNugUsageExtraValueUTF16(t *testing.T) {
	// 300 CJK (900 bytes, 300 UTF-16 units): not > 300, unchanged.
	exact := strings.Repeat("章", 300)
	got, ok := sanitizeNugUsageExtraValue(exact)
	if !ok || got != exact {
		t.Fatalf("300-unit CJK should be unchanged, got %q ok=%v", got, ok)
	}

	// 400 CJK (>300 units): truncated to 300 units + "…", valid UTF-8.
	over := strings.Repeat("章", 400)
	gotOver, ok := sanitizeNugUsageExtraValue(over)
	if !ok {
		t.Fatalf("400-unit CJK should sanitize")
	}
	wantOver := strings.Repeat("章", 300) + "…"
	if gotOver != wantOver {
		t.Fatalf("400-unit CJK truncation mismatch:\n got %q\nwant %q", gotOver, wantOver)
	}
	if s, _ := gotOver.(string); strings.ContainsRune(s, 0xFFFD) {
		t.Fatalf("truncated CJK value has invalid UTF-8 (U+FFFD): %q", s)
	}
}

// TestSafeNugUsageExtraKeyUTF16 pins the usage-extra key truncation to the TS
// `label.slice(0, USAGE_EXTRA_KEY_MAX_CHARS)` (80, UTF-16; nug.ts:399). Go
// previously used a byte slice on the label.
func TestSafeNugUsageExtraKeyUTF16(t *testing.T) {
	// An 81-CJK key (243 bytes, 81 UTF-16 units) -> truncated to 80 UTF-16 units.
	longKey := strings.Repeat("键", 81)
	out := safeNugUsageExtra(map[string]any{longKey: "value"})
	if len(out) != 1 {
		t.Fatalf("expected 1 extra entry, got %#v", out)
	}
	for k := range out {
		runes := len([]rune(k))
		if runes != 80 {
			t.Fatalf("usage extra key kept %d runes, want 80 (key=%q)", runes, k)
		}
		if strings.ContainsRune(k, 0xFFFD) {
			t.Fatalf("usage extra key has invalid UTF-8 (U+FFFD): %q", k)
		}
	}
}
