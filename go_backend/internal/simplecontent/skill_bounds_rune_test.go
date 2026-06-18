package simplecontent

import (
	"strings"
	"testing"
)

// TestValidateProjectSkillBoundsUsesJSStringLength pins project-skill length
// validation to JavaScript String.prototype.length (UTF-16 code units), matching
// the TS skill Zod schema (server/lib/validators/skills.ts: name .max(100),
// description .max(2000), content .max(200000)). Two divergences are covered:
// byte-vs-character (100 CJK = 300 bytes but 100 UTF-16 units, must pass) and
// rune-vs-UTF-16 (an emoji is 1 rune but 2 UTF-16 units, so 51 emoji = 102 units
// must be rejected for name even though a rune-count check would see only 51).
func TestValidateProjectSkillBoundsUsesJSStringLength(t *testing.T) {
	// name: 100 CJK chars = 300 UTF-8 bytes = 100 UTF-16 units; must pass.
	if msg := validateProjectSkillBounds(strings.Repeat("技", 100), "", ""); msg != "" {
		t.Fatalf("100 CJK-char name should pass: %q", msg)
	}
	if msg := validateProjectSkillBounds(strings.Repeat("技", 101), "", ""); msg == "" {
		t.Fatal("101 CJK-char name should be rejected")
	}

	// name astral: 50 emoji = 100 UTF-16 units pass; 51 emoji = 102 units reject.
	if msg := validateProjectSkillBounds(strings.Repeat("😀", 50), "", ""); msg != "" {
		t.Fatalf("50-emoji name (100 UTF-16 units) should pass: %q", msg)
	}
	if msg := validateProjectSkillBounds(strings.Repeat("😀", 51), "", ""); msg == "" {
		t.Fatal("51-emoji name (102 UTF-16 units) should be rejected")
	}

	// description: 2000 CJK chars must pass; 1001 emoji = 2002 units reject.
	if msg := validateProjectSkillBounds("ok", strings.Repeat("述", 2000), ""); msg != "" {
		t.Fatalf("2000 CJK-char description should pass: %q", msg)
	}
	if msg := validateProjectSkillBounds("ok", strings.Repeat("述", 2001), ""); msg == "" {
		t.Fatal("2001 CJK-char description should be rejected")
	}
	if msg := validateProjectSkillBounds("ok", strings.Repeat("😀", 1001), ""); msg == "" {
		t.Fatal("1001-emoji description (2002 UTF-16 units) should be rejected")
	}

	// content: 200000 CJK chars must pass; 100001 emoji = 200002 units reject.
	if msg := validateProjectSkillBounds("ok", "", strings.Repeat("文", 200000)); msg != "" {
		t.Fatalf("200000 CJK-char content should pass: %q", msg)
	}
	if msg := validateProjectSkillBounds("ok", "", strings.Repeat("文", 200001)); msg == "" {
		t.Fatal("200001 CJK-char content should be rejected")
	}
	if msg := validateProjectSkillBounds("ok", "", strings.Repeat("😀", 100001)); msg == "" {
		t.Fatal("100001-emoji content (200002 UTF-16 units) should be rejected")
	}
}
