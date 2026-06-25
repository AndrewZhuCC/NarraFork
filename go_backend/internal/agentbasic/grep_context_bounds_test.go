package agentbasic

import "testing"

// TestGrepContextBoundsPrecedence locks the W844 fix: grepContextBounds mirrors
// the TS grep.ts:215-222 context-line precedence — effectiveC = cAlias (-C) ??
// contextLines (context); when EITHER -C or context is provided the tool emits a
// symmetric context and IGNORES -B/-A; only when both are absent do -B/-A apply.
// The -1 sentinel marks "not provided". Go previously inverted this (letting
// -B/-A override -C/context).
func TestGrepContextBoundsPrecedence(t *testing.T) {
	// opt builds a grepOptions with the -1 "not provided" sentinel for every
	// context field, overridden by the provided values.
	opt := func(context, contextAlias, before, after int) grepOptions {
		return grepOptions{Context: context, ContextAlias: contextAlias, Before: before, After: after}
	}
	cases := []struct {
		name                             string
		context, ctxAlias, before, after int
		wantBefore, wantAfter            int
	}{
		// context (or -C) present => symmetric, -A/-B ignored (the inversion bug).
		{"context wins over -A", 5, -1, -1, 2, 5, 5},
		{"context wins over -B", 5, -1, 1, -1, 5, 5},
		{"context wins over -B and -A", 5, -1, 1, 2, 5, 5},
		{"-C wins over -A", -1, 3, -1, 2, 3, 3},
		{"-C wins over context (cAlias ?? context)", 4, 7, -1, -1, 7, 7},
		// Only -A/-B (no -C/context) => asymmetric, applied directly.
		{"only -A", -1, -1, -1, 2, 0, 2},
		{"only -B", -1, -1, 1, -1, 1, 0},
		{"only -B and -A", -1, -1, 1, 2, 1, 2},
		// Only context/-C => symmetric.
		{"only context", 7, -1, -1, -1, 7, 7},
		{"only -C", -1, 3, -1, -1, 3, 3},
		// Nothing provided => zero context.
		{"nothing", -1, -1, -1, -1, 0, 0},
		// 20-line Go resource cap is retained on each side.
		{"context capped at 20", 50, -1, -1, -1, 20, 20},
		{"-A capped at 20", -1, -1, -1, 50, 0, 20},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			before, after := grepContextBounds(opt(c.context, c.ctxAlias, c.before, c.after))
			if before != c.wantBefore || after != c.wantAfter {
				t.Fatalf("grepContextBounds(context=%d,-C=%d,-B=%d,-A=%d) = (%d,%d), want (%d,%d)",
					c.context, c.ctxAlias, c.before, c.after, before, after, c.wantBefore, c.wantAfter)
			}
		})
	}
}
