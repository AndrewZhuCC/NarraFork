package agentbasic

import "testing"

func TestEstimateTokensHeuristic(t *testing.T) {
	if got := estimateTokens(""); got != 0 {
		t.Fatalf("empty = %d, want 0", got)
	}
	// 10 ASCII chars * 0.3 = 3.0 -> ceil 3
	if got := estimateTokens("helloworld"); got != 3 {
		t.Fatalf("ascii = %d, want 3", got)
	}
	// 5 CJK chars * 0.6 = 3.0 -> ceil 3
	if got := estimateTokens("你好世界吗"); got != 3 {
		t.Fatalf("cjk = %d, want 3", got)
	}
	// Astral characters (emoji) are 2 UTF-16 code units each, matching TS
	// text.length/charCodeAt iteration. 3 emoji = 6 code units * 0.3 = 1.8
	// -> ceil 2. (A Go rune-based loop would count 3 * 0.3 = 0.9 -> ceil 1,
	// understating the estimate; this pins the UTF-16 parity.)
	if got := estimateTokens("😀😀😀"); got != 2 {
		t.Fatalf("emoji x3 = %d, want 2 (6 UTF-16 units * 0.3 -> ceil 1.8)", got)
	}
	// 10 emoji = 20 code units * 0.3 = 6.0 -> ceil 6 (vs rune-based 10*0.3=3).
	if got := estimateTokens("😀😀😀😀😀😀😀😀😀😀"); got != 6 {
		t.Fatalf("emoji x10 = %d, want 6", got)
	}
	// CJK Extension B (U+20000) is astral: 2 code units, neither a wide range
	// (surrogates are not wide), so 2 * 0.3 = 0.6 -> ceil 1, matching TS.
	if got := estimateTokens("\U00020000"); got != 1 {
		t.Fatalf("cjk-ext-b = %d, want 1", got)
	}
}

func TestEstimateTokensFromContextPercent(t *testing.T) {
	cases := []struct {
		percent float64
		window  int
		want    int
	}{
		{0, 200000, 0},
		{-1, 200000, 0},
		{50, 200000, 100000},
		{100, 200000, 200000},
		{150, 200000, 200000}, // clamped
		{25, 0, 0},            // no window
		{12.5, 200000, 25000},
	}
	for _, c := range cases {
		if got := estimateTokensFromContextPercent(c.percent, c.window); got != c.want {
			t.Fatalf("estimateTokensFromContextPercent(%v, %d) = %d, want %d", c.percent, c.window, got, c.want)
		}
	}
}
