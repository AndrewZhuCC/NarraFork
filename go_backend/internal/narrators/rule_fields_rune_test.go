package narrators

import (
	"strings"
	"testing"
)

// TestValidateNarratorRuleFieldsUsesJSStringLength pins narrator dir/command rule
// length validation to JavaScript String.prototype.length (UTF-16 code units),
// matching the TS narrator rule Zod bounds (server/lib/validators/common.ts +
// narrators.ts): path .min(1).max(4096), pattern .min(1).max(200), denyPrompt
// .max(2000). Two divergences are covered: byte-vs-character (CJK is multibyte
// but 1 UTF-16 unit) and rune-vs-UTF-16 (an emoji is 1 rune but 2 UTF-16 units,
// so 101 emoji = 202 units must be rejected for pattern even though a rune-count
// check would see only 101).
func TestValidateNarratorRuleFieldsUsesJSStringLength(t *testing.T) {
	// path: 4096 CJK chars = 12288 bytes = 4096 UTF-16 units; must pass.
	if err := validateNarratorRuleFields("path", "access_level", strings.Repeat("路", 4096), map[string]any{}); err != nil {
		t.Fatalf("4096 CJK-char path should pass: %v", err)
	}
	if err := validateNarratorRuleFields("path", "access_level", strings.Repeat("路", 4097), map[string]any{}); err == nil {
		t.Fatal("4097 CJK-char path should be rejected")
	}

	// pattern: 200 CJK chars must pass.
	if err := validateNarratorRuleFields("pattern", "deny_level", strings.Repeat("配", 200), map[string]any{}); err != nil {
		t.Fatalf("200 CJK-char pattern should pass: %v", err)
	}
	if err := validateNarratorRuleFields("pattern", "deny_level", strings.Repeat("配", 201), map[string]any{}); err == nil {
		t.Fatal("201 CJK-char pattern should be rejected")
	}
	// pattern astral: 100 emoji = 200 units pass; 101 emoji = 202 units reject.
	if err := validateNarratorRuleFields("pattern", "deny_level", strings.Repeat("😀", 100), map[string]any{}); err != nil {
		t.Fatalf("100-emoji pattern (200 UTF-16 units) should pass: %v", err)
	}
	if err := validateNarratorRuleFields("pattern", "deny_level", strings.Repeat("😀", 101), map[string]any{}); err == nil {
		t.Fatal("101-emoji pattern (202 UTF-16 units) should be rejected")
	}

	// denyPrompt (secondary): 2000 CJK chars must pass, 2001 rejected.
	okBody := map[string]any{"denyPrompt": strings.Repeat("拒", 2000)}
	if err := validateNarratorRuleFields("pattern", "deny_prompt", "p", okBody); err != nil {
		t.Fatalf("2000 CJK-char denyPrompt should pass: %v", err)
	}
	badBody := map[string]any{"denyPrompt": strings.Repeat("拒", 2001)}
	if err := validateNarratorRuleFields("pattern", "deny_prompt", "p", badBody); err == nil {
		t.Fatal("2001 CJK-char denyPrompt should be rejected")
	}
	// denyPrompt astral: 1001 emoji = 2002 units reject.
	emojiBody := map[string]any{"denyPrompt": strings.Repeat("😀", 1001)}
	if err := validateNarratorRuleFields("pattern", "deny_prompt", "p", emojiBody); err == nil {
		t.Fatal("1001-emoji denyPrompt (2002 UTF-16 units) should be rejected")
	}
}

// TestNormalizeGoalObjectiveUsesJSStringLength pins the goal objective length
// validation to UTF-16 code units, matching the TS goal tool schema
// (server/lib/agent/tools/goal.ts: objective z.string().min(1).max(4000)). An
// emoji is 1 rune but 2 UTF-16 units, so 2001 emoji = 4002 units must be
// rejected even though a rune-count check would see only 2001.
func TestNormalizeGoalObjectiveUsesJSStringLength(t *testing.T) {
	// 4000 CJK chars = 4000 UTF-16 units; must pass.
	if _, err := normalizeGoalObjective(strings.Repeat("标", 4000)); err != nil {
		t.Fatalf("4000 CJK-char objective should pass max(4000): %v", err)
	}
	// 4001 CJK chars rejected.
	if _, err := normalizeGoalObjective(strings.Repeat("标", 4001)); err == nil {
		t.Fatal("4001 CJK-char objective should be rejected")
	}
	// 2000 emoji = 4000 UTF-16 units pass (boundary).
	if _, err := normalizeGoalObjective(strings.Repeat("😀", 2000)); err != nil {
		t.Fatalf("2000-emoji objective (4000 UTF-16 units) should pass max(4000): %v", err)
	}
	// 2001 emoji = 4002 UTF-16 units rejected. A rune-count check would see 2001.
	if _, err := normalizeGoalObjective(strings.Repeat("😀", 2001)); err == nil {
		t.Fatal("2001-emoji objective (4002 UTF-16 units) should be rejected by max(4000)")
	}
}
