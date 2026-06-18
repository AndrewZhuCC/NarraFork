package settings

import (
	"strings"
	"testing"
)

// TestValidateSettingsStringFieldBoundsUsesJSStringLength pins the settings string
// field length validation to JavaScript String.prototype.length (UTF-16 code
// units), matching the TS settings Zod .min()/.max() bounds. An emoji is 1 rune
// but 2 UTF-16 units, so a value of N emoji is 2N UTF-16 units; a rune-count
// implementation would under-count and over-accept versus the JS metric.
func TestValidateSettingsStringFieldBoundsUsesJSStringLength(t *testing.T) {
	const maxLen = 100
	field := func(s string) map[string]any { return map[string]any{"k": s} }

	// 100 CJK chars = 100 UTF-16 units: passes max(100).
	if err := validateSettingsStringFieldBounds(field(strings.Repeat("设", 100)), "k", "p", 1, maxLen); err != nil {
		t.Fatalf("100 CJK chars should pass max(100): %v", err)
	}
	// 101 CJK chars rejected.
	if err := validateSettingsStringFieldBounds(field(strings.Repeat("设", 101)), "k", "p", 1, maxLen); err == nil {
		t.Fatal("101 CJK chars should be rejected")
	}
	// 50 emoji = 100 UTF-16 units: passes (boundary).
	if err := validateSettingsStringFieldBounds(field(strings.Repeat("😀", 50)), "k", "p", 1, maxLen); err != nil {
		t.Fatalf("50 emoji (100 UTF-16 units) should pass max(100): %v", err)
	}
	// 51 emoji = 102 UTF-16 units: rejected. A rune-count check would see 51 and accept.
	if err := validateSettingsStringFieldBounds(field(strings.Repeat("😀", 51)), "k", "p", 1, maxLen); err == nil {
		t.Fatal("51 emoji (102 UTF-16 units) should be rejected by max(100)")
	}

	// min length is also counted in UTF-16 units: 1 emoji = 2 units >= min(1).
	if err := validateSettingsStringFieldBounds(field("😀"), "k", "p", 1, maxLen); err != nil {
		t.Fatalf("single emoji (2 UTF-16 units) should satisfy min(1): %v", err)
	}
	// empty string fails min(1).
	if err := validateSettingsStringFieldBounds(field(""), "k", "p", 1, maxLen); err == nil {
		t.Fatal("empty string should fail min(1)")
	}
}

// TestMaskSecretMatchesTSMaskApiKey pins maskSecret to the TS maskApiKey
// (server/routes/settings.ts): empty -> "", length<=4 -> stars of that length,
// else 8 stars + last 4 chars, measured in UTF-16 code units (not Go bytes). The
// non-ASCII cases guard against byte-length divergence and mid-rune byte slicing
// that would emit invalid UTF-8 in the masked display value.
func TestMaskSecretMatchesTSMaskApiKey(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"ab", "**"},
		{"abcd", "****"},
		{"abcdef", "********cdef"},
		{"sk-1234567890", "********7890"},
		// 3 CJK chars: JSStringLength 3 (<=4) -> 3 stars (TS key.length is 3).
		{"密钥串", "***"},
		// 6 CJK chars: length 6 (>4) -> 8 stars + last 4 CJK chars (valid UTF-8).
		{"超长密钥令牌", "********密钥令牌"},
	}
	for _, tc := range cases {
		if got := maskSecret(tc.in); got != tc.want {
			t.Fatalf("maskSecret(%q) = %q, want %q", tc.in, got, tc.want)
		}
		if !utf8ValidString(maskSecret(tc.in)) {
			t.Fatalf("maskSecret(%q) produced invalid UTF-8: %q", tc.in, maskSecret(tc.in))
		}
	}
}

func utf8ValidString(s string) bool {
	for _, r := range s {
		if r == 0xFFFD {
			// Could be a real replacement char in input, but our test inputs have none,
			// so a U+FFFD here signals a mid-rune slice corruption.
			return false
		}
	}
	return true
}
