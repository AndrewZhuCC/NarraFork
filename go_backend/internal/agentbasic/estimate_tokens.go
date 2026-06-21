package agentbasic

import "math"

// estimateTokens estimates the token count of a string using a character-based
// heuristic, mirroring server/lib/agent/estimate-tokens.ts:
//   - CJK / wide characters: ~0.6 tokens per character
//   - other (ASCII / Latin) characters: ~0.3 tokens per character
//
// The estimate is intentionally conservative (slightly over-estimating) and the
// result is rounded up. It is used to estimate Kiro output tokens, which the
// upstream never reports.
func estimateTokens(text string) int {
	if text == "" {
		return 0
	}
	tokens := 0.0
	for _, r := range text {
		if isWideRune(r) {
			tokens += 0.6
		} else {
			tokens += 0.3
		}
	}
	return int(math.Ceil(tokens))
}

// estimateTokensFromContextPercent converts a context-window occupancy
// percentage (0..100) into an estimated input/prompt token count using the
// model's context window. The percentage is clamped to [0, 100]; a non-positive
// window yields 0.
func estimateTokensFromContextPercent(percent float64, contextWindow int) int {
	if contextWindow <= 0 || percent <= 0 {
		return 0
	}
	if percent > 100 {
		percent = 100
	}
	return int(math.Round(percent / 100 * float64(contextWindow)))
}

// isWideRune reports whether r falls in a CJK / fullwidth / kana range, matching
// the character buckets used by estimate-tokens.ts.
func isWideRune(r rune) bool {
	switch {
	case r >= 0x2e80 && r <= 0x9fff: // CJK radicals, ideographs, extension A
		return true
	case r >= 0xac00 && r <= 0xd7af: // Hangul syllables
		return true
	case r >= 0xf900 && r <= 0xfaff: // CJK compatibility ideographs
		return true
	case r >= 0xff00 && r <= 0xffef: // Fullwidth forms
		return true
	case r >= 0x3000 && r <= 0x303f: // CJK symbols and punctuation
		return true
	case r >= 0x3040 && r <= 0x30ff: // Hiragana + Katakana
		return true
	default:
		return false
	}
}
