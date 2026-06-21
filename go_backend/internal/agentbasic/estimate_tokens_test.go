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
