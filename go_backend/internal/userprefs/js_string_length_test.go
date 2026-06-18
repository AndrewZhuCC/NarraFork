package userprefs

import (
	"strings"
	"testing"
)

// TestUserPrefsStringLengthUsesJSStringLength pins the user-preferences string
// length validators to JavaScript String.prototype.length (UTF-16 code units),
// matching the TS Zod .min()/.max() checks. utf8.RuneCountInString would be too
// permissive for astral characters (emoji = 2 UTF-16 units in JS but 1 rune in
// Go), so input TS rejects at a .max() boundary must also be rejected here.
func TestUserPrefsStringLengthUsesJSStringLength(t *testing.T) {
	// jsStringLength itself: BMP char = 1 unit, astral char = 2 units.
	if got := jsStringLength(strings.Repeat("书", 50)); got != 50 {
		t.Fatalf("50 CJK chars should be 50 UTF-16 units, got %d", got)
	}
	if got := jsStringLength(strings.Repeat("😀", 50)); got != 100 {
		t.Fatalf("50 emoji should be 100 UTF-16 units, got %d", got)
	}

	// requiredStringField max=50: 50 CJK chars pass, 26 emoji (52 units) rejected.
	if _, err := requiredStringField(map[string]any{"k": strings.Repeat("书", 50)}, "k", "p", 1, 50); err != nil {
		t.Fatalf("50 CJK chars should pass max(50): %v", err)
	}
	if _, err := requiredStringField(map[string]any{"k": strings.Repeat("😀", 26)}, "k", "p", 1, 50); err == nil {
		t.Fatal("26 emoji (52 UTF-16 units) should be rejected by max(50)")
	}
	// Exactly at the UTF-16 boundary: 25 emoji = 50 units passes.
	if _, err := requiredStringField(map[string]any{"k": strings.Repeat("😀", 25)}, "k", "p", 1, 50); err != nil {
		t.Fatalf("25 emoji (50 UTF-16 units) should pass max(50): %v", err)
	}

	// optionalStringField max=100: 51 emoji (102 units) rejected, absent key OK.
	if err := optionalStringField(map[string]any{"k": strings.Repeat("😀", 51)}, "k", "p", 0, 100); err == nil {
		t.Fatal("51 emoji (102 UTF-16 units) should be rejected by max(100)")
	}
	if err := optionalStringField(map[string]any{}, "k", "p", 0, 100); err != nil {
		t.Fatalf("absent optional field should pass: %v", err)
	}
}

// TestValidateRemoveRecentTabIDUsesJSStringLength pins the removeRecentTab id
// max-50 bound to UTF-16 code units (TS removeRecentTabSchema
// id: z.string().min(1).max(50)). A 50-character CJK id (150 UTF-8 bytes) must
// pass; Go previously used byte length and wrongly rejected it.
func TestValidateRemoveRecentTabIDUsesJSStringLength(t *testing.T) {
	// 50 CJK chars = 50 UTF-16 units (150 bytes): must PASS (byte check would reject).
	if err := validateRemoveRecentTab("chapter", strings.Repeat("书", 50)); err != nil {
		t.Fatalf("50-char CJK id should pass max(50) under UTF-16 semantics: %v", err)
	}
	// 51 CJK chars = 51 UTF-16 units: must be rejected.
	if err := validateRemoveRecentTab("chapter", strings.Repeat("书", 51)); err == nil {
		t.Fatal("51-char CJK id should be rejected by max(50)")
	}
	// Empty id rejected; "session" type is accepted (remove allows it).
	if err := validateRemoveRecentTab("session", ""); err == nil {
		t.Fatal("empty id should be rejected")
	}
	if err := validateRemoveRecentTab("session", "s1"); err != nil {
		t.Fatalf("session type should be accepted by removeRecentTab: %v", err)
	}
}
