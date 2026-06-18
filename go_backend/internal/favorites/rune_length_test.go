package favorites

import (
	"strings"
	"testing"
)

// TestFavoriteStringValidationUsesJSStringLength pins favorite path/label length
// validation to JavaScript String.prototype.length (UTF-16 code units), which is
// what the TS Zod schema (server/lib/validators/settings.ts) .min()/.max() checks
// count. Two divergences are covered:
//   - byte-vs-character: BMP/CJK chars are 1 UTF-16 unit but multiple UTF-8 bytes,
//     so len()-based checks would wrongly reject them (the original regression);
//   - rune-vs-UTF-16: astral chars (emoji) are 1 rune but 2 UTF-16 units, so a
//     rune-count check would be too permissive and accept input TS rejects.
func TestFavoriteStringValidationUsesJSStringLength(t *testing.T) {
	// label: 200 CJK chars = 1 UTF-16 unit each = 600 UTF-8 bytes; must pass max(200).
	label200 := strings.Repeat("书", 200)
	if _, err := optionalStringValue(label200, "label", 200); err != nil {
		t.Fatalf("200 CJK-char label should pass (200 UTF-16 units): %v", err)
	}
	// 201 CJK chars must be rejected.
	label201 := strings.Repeat("书", 201)
	if _, err := optionalStringValue(label201, "label", 200); err == nil {
		t.Fatal("201 CJK-char label should be rejected")
	}

	// Astral characters: each emoji is 2 UTF-16 units. 100 emoji = 200 units -> passes.
	emoji100 := strings.Repeat("😀", 100)
	if _, err := optionalStringValue(emoji100, "label", 200); err != nil {
		t.Fatalf("100 emoji (200 UTF-16 units) should pass max(200): %v", err)
	}
	// 101 emoji = 202 UTF-16 units -> must be rejected, matching JS str.length.
	// A rune-count implementation would see only 101 and wrongly accept it.
	emoji101 := strings.Repeat("😀", 101)
	if _, err := optionalStringValue(emoji101, "label", 200); err == nil {
		t.Fatal("101 emoji (202 UTF-16 units) should be rejected by max(200)")
	}

	// path via requiredStringField: 4096 CJK chars must pass max, empty rejected.
	path4096 := strings.Repeat("路", 4096)
	if _, err := requiredStringField(map[string]any{"path": path4096}, "path", 1, 4096); err != nil {
		t.Fatalf("4096 CJK-char path should pass (4096 UTF-16 units): %v", err)
	}
	if _, err := requiredStringField(map[string]any{"path": ""}, "path", 1, 4096); err == nil {
		t.Fatal("empty path should be rejected by min length")
	}
	// 2049 emoji = 4098 UTF-16 units -> exceeds max(4096) even though only 2049 runes.
	pathAstral := strings.Repeat("😀", 2049)
	if _, err := requiredStringField(map[string]any{"path": pathAstral}, "path", 1, 4096); err == nil {
		t.Fatal("2049 emoji (4098 UTF-16 units) path should be rejected by max(4096)")
	}
}
