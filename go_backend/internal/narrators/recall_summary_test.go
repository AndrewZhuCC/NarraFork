package narrators

import "testing"

// TestRecallToolCallSummaryMatchesTS pins recallToolCallSummary to the
// TypeScript getToolCallSummary (server/lib/agent/tools/recall.ts) behavior,
// which is fed into the recall context shown to the model. The key parity
// points are: file/share tools use a "/"-split basename; Bash/Shell truncate at
// 80 with `slice(0,77)+"..."`; Grep/WebSearch at 60 (`slice(0,57)+"..."`);
// WebFetch url at 50 (`slice(0,47)+"..."`) with optional `mode: ` prefix;
// Agent/Send use a plain slice(0,60) with NO ellipsis; extractStr returns the
// first string-typed key WITHOUT trimming; and the fixed-label tools return
// their constant strings.
func TestRecallToolCallSummaryMatchesTS(t *testing.T) {
	long := func(n int) string {
		b := make([]byte, n)
		for i := range b {
			b[i] = 'a'
		}
		return string(b)
	}
	cases := []struct {
		name  string
		tool  string
		input map[string]any
		want  string
	}{
		{"read-basename", "Read", map[string]any{"file_path": "/a/b/c/file.ts"}, "file.ts"},
		{"glob-pattern-fallback", "Glob", map[string]any{"pattern": "src/**/x.go"}, "x.go"},
		{"read-no-path", "Read", map[string]any{}, "Read"},
		{"read-trailing-slash", "Read", map[string]any{"path": "/a/b/"}, "/a/b/"},
		// Bash short: returned unchanged.
		{"bash-short", "Bash", map[string]any{"command": "ls -la"}, "ls -la"},
		// Bash exactly 80: not > 80, unchanged.
		{"bash-exact-80", "Bash", map[string]any{"command": long(80)}, long(80)},
		// Bash 81: slice(0,77)+"..."
		{"bash-81", "Bash", map[string]any{"command": long(81)}, long(77) + "..."},
		{"bash-no-command", "Bash", map[string]any{}, "Bash"},
		{"shell-alias", "Shell", map[string]any{"command": long(100)}, long(77) + "..."},
		// Grep 61 -> slice(0,57)+"..."
		{"grep-61", "Grep", map[string]any{"pattern": long(61)}, long(57) + "..."},
		{"grep-no-pattern", "Grep", map[string]any{}, "Grep"},
		{"websearch-empty", "WebSearch", map[string]any{}, "Web Search"},
		{"websearch-61", "WebSearch", map[string]any{"query": long(61)}, long(57) + "..."},
		// WebFetch url 51 -> slice(0,47)+"..." with mode prefix.
		{"webfetch-mode-url", "WebFetch", map[string]any{"url": long(51), "mode": "smart"}, "smart: " + long(47) + "..."},
		{"webfetch-no-url-mode", "WebFetch", map[string]any{"mode": "dom"}, "dom"},
		{"webfetch-no-url-no-mode", "WebFetch", map[string]any{}, "WebFetch"},
		{"webfetch-url-only-short", "WebFetch", map[string]any{"url": "http://x"}, "http://x"},
		// Agent/Send: plain slice(0,60), NO ellipsis.
		{"agent-65", "Agent", map[string]any{"description": long(65)}, long(60)},
		{"send-message", "Send", map[string]any{"message": "hello"}, "hello"},
		{"agent-empty", "Agent", map[string]any{}, "Agent"},
		{"terminal-action", "Terminal", map[string]any{"action": "create"}, "create"},
		{"terminal-no-action", "Terminal", map[string]any{}, "Terminal"},
		{"sharefile-basename", "ShareFile", map[string]any{"path": "/x/y/z.zip"}, "z.zip"},
		{"sharefile-empty", "ShareFile", map[string]any{}, "Share"},
		{"askuser-header", "AskUserQuestion", map[string]any{"questions": []any{map[string]any{"header": "Pick one"}}}, "Pick one"},
		{"askuser-empty", "AskUserQuestion", map[string]any{}, "Question"},
		{"taskcreate", "TaskCreate", map[string]any{}, "Update todos"},
		{"enterplan", "EnterPlanMode", map[string]any{}, "Enter plan mode"},
		{"exitplan", "ExitPlanMode", map[string]any{}, "Plan ready"},
		{"unknown-tool", "MysteryTool", map[string]any{}, "MysteryTool"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := recallToolCallSummary(tc.tool, tc.input); got != tc.want {
				t.Fatalf("recallToolCallSummary(%q,%v)=%q want %q", tc.tool, tc.input, got, tc.want)
			}
		})
	}
}

// TestRecallToolCallSummaryNonMapInput verifies that, like the TS guard
// `if (!inputJson || typeof inputJson !== "object") return toolName`, a nil or
// non-object input yields the bare tool name.
func TestRecallToolCallSummaryNonMapInput(t *testing.T) {
	for _, in := range []any{nil, "string-input", 42, []any{"a"}} {
		if got := recallToolCallSummary("Bash", in); got != "Bash" {
			t.Fatalf("recallToolCallSummary(Bash,%v)=%q want Bash", in, got)
		}
	}
}

// TestRecallExtractStrNoTrim confirms recallExtractStr mirrors the TS extractStr:
// it returns the first key whose value is a string verbatim (no trimming, no
// emptiness skipping). A whitespace-only string value is returned as-is and a
// non-string value is skipped.
func TestRecallExtractStrNoTrim(t *testing.T) {
	if got := recallExtractStr(map[string]any{"command": "   "}, "command"); got != "   " {
		t.Fatalf("recallExtractStr whitespace = %q, want untrimmed three spaces", got)
	}
	// First key holds a non-string (number) -> skipped; second key wins.
	got := recallExtractStr(map[string]any{"file_path": 12, "path": "p.go"}, "file_path", "path")
	if got != "p.go" {
		t.Fatalf("recallExtractStr skip-non-string = %q, want p.go", got)
	}
	if got := recallExtractStr(map[string]any{}, "a", "b"); got != "" {
		t.Fatalf("recallExtractStr missing = %q, want empty", got)
	}
}

// TestRecallSummaryTruncateUTF16 verifies the truncation uses JavaScript
// String.length / slice semantics (UTF-16 code units), so CJK content truncates
// at the same boundary as the TS implementation. Each CJK rune is one UTF-16
// code unit, so a 90-rune CJK command (>80) keeps 77 runes plus the ellipsis.
func TestRecallSummaryTruncateUTF16(t *testing.T) {
	runes := make([]rune, 90)
	for i := range runes {
		runes[i] = '好'
	}
	cmd := string(runes)
	got := recallToolCallSummary("Bash", map[string]any{"command": cmd})
	want := string(runes[:77]) + "..."
	if got != want {
		t.Fatalf("CJK bash truncate len mismatch: got %d runes, want 77+ellipsis", len([]rune(got)))
	}
}
