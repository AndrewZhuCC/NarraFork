package agentbasic

import (
	"math"
	"unicode/utf16"
)

// estimateTokens estimates the token count of a string using a character-based
// heuristic, mirroring server/lib/agent/estimate-tokens.ts:
//   - CJK / wide characters: ~0.6 tokens per character
//   - other (ASCII / Latin) characters: ~0.3 tokens per character
//
// The estimate is intentionally conservative (slightly over-estimating) and the
// result is rounded up. It is used to estimate Kiro output tokens, which the
// upstream never reports.
//
// Iteration is over UTF-16 code units (not Go runes) to match the TS source,
// which loops `for (i=0; i<text.length; i++)` over text.charCodeAt(i). An astral
// character (emoji / CJK Ext-B / etc.) is two UTF-16 code units, so it
// contributes 2*0.3 = 0.6 like TS, instead of the 0.3 a single Go rune would
// add. None of the wide ranges below cover the surrogate range (0xD800-0xDFFF),
// so astral surrogate halves each fall into the 0.3 bucket exactly as in TS.
func estimateTokens(text string) int {
	if text == "" {
		return 0
	}
	tokens := 0.0
	for _, unit := range utf16.Encode([]rune(text)) {
		if isWideCodeUnit(unit) {
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

// isWideCodeUnit reports whether a UTF-16 code unit falls in a CJK / fullwidth /
// kana range, matching the character buckets and code-unit comparison used by
// estimate-tokens.ts (which tests charCodeAt(i) against these ranges).
func isWideCodeUnit(u uint16) bool {
	switch {
	case u >= 0x2e80 && u <= 0x9fff: // CJK radicals, ideographs, extension A
		return true
	case u >= 0xac00 && u <= 0xd7af: // Hangul syllables
		return true
	case u >= 0xf900 && u <= 0xfaff: // CJK compatibility ideographs
		return true
	case u >= 0xff00 && u <= 0xffef: // Fullwidth forms
		return true
	case u >= 0x3000 && u <= 0x303f: // CJK symbols and punctuation
		return true
	case u >= 0x3040 && u <= 0x30ff: // Hiragana + Katakana
		return true
	default:
		return false
	}
}
