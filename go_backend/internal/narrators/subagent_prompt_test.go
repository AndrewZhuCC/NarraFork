package narrators

import (
	"strings"
	"testing"
)

// TestBuiltinSubagentPromptCoversTSBuiltinTypes verifies the four TS
// BuiltinSubagentType prompts (explore/plan/general/search) resolve to non-empty
// bilingual text via builtinSubagentPrompt, while review and custom types do not
// (review is intentionally NOT a TS BuiltinSubagentType — getSubagentPrompt
// returns null in TS, and Go keeps its own one-liner for review).
func TestBuiltinSubagentPromptCoversTSBuiltinTypes(t *testing.T) {
	for _, typ := range []string{"explore", "plan", "general", "search"} {
		for _, locale := range []string{"en", "zh-CN"} {
			prompt, ok := builtinSubagentPrompt(typ, locale)
			if !ok {
				t.Fatalf("builtinSubagentPrompt(%q,%q) should be a recognized builtin", typ, locale)
			}
			if strings.TrimSpace(prompt) == "" {
				t.Fatalf("builtinSubagentPrompt(%q,%q) returned empty prompt", typ, locale)
			}
		}
	}
	for _, typ := range []string{"review", "myagent", "", "custom-thing"} {
		if _, ok := builtinSubagentPrompt(typ, "en"); ok {
			t.Fatalf("builtinSubagentPrompt(%q) must NOT be a TS builtin (review/custom keep Go fallback)", typ)
		}
	}
}

// TestSubagentSystemPromptUsesTSAlignedText verifies subagentSystemPrompt now
// emits the TS-aligned detailed prompts for builtin types (fixing the bug where
// builtin general/search fell into the "custom subagent type" branch), keeps the
// Go one-liner for review/custom, and no longer inlines a redundant
// "Working directory:" line (cwd is appended at runtime by agentSystemPrompt).
func TestSubagentSystemPromptUsesTSAlignedText(t *testing.T) {
	cases := []struct {
		typ, locale, mustContain string
	}{
		{"explore", "en", "exploration specialist"},
		{"plan", "en", "software architect"},
		{"general", "en", "delegated task"},
		{"search", "en", "web search specialist"},
		{"search", "zh-CN", "网络搜索专家"},
		{"explore", "zh-CN", "代码库探索专家"},
		{"review", "en", "review mode"},
		{"myagent", "en", "custom subagent type"},
	}
	for _, tc := range cases {
		got := subagentSystemPrompt(tc.typ, tc.locale, "/some/cwd")
		if !strings.Contains(got, tc.mustContain) {
			t.Fatalf("subagentSystemPrompt(%q,%q) should contain %q, got: %q", tc.typ, tc.locale, tc.mustContain, got)
		}
		if strings.Contains(got, "Working directory:") {
			t.Fatalf("subagentSystemPrompt(%q,%q) must NOT inline 'Working directory:' (cwd handled at runtime), got: %q", tc.typ, tc.locale, got)
		}
	}
}

// TestExplorePlanPromptAdaptedToFinalReplyNotConclusionFile pins the W802
// adaptation (raised by the plan reflection gate): Go has NO explore/plan
// conclusion-file redirect mechanism (explore subagents run readOnly so Write/Edit
// are denied; conclusions return via the final assistant reply). The TS prompt's
// "Write findings/plan to the conclusion file (auto-redirected)" sections must be
// adapted to "return as final reply", not ported verbatim referencing a mechanism
// Go does not implement — while preserving the distillation discipline.
func TestExplorePlanPromptAdaptedToFinalReplyNotConclusionFile(t *testing.T) {
	for _, typ := range []string{"explore", "plan"} {
		for _, locale := range []string{"en", "zh-CN"} {
			prompt, _ := builtinSubagentPrompt(typ, locale)
			lower := strings.ToLower(prompt)
			if strings.Contains(lower, "conclusion file") || strings.Contains(lower, "automatically redirected") || strings.Contains(lower, "结论文件") {
				t.Fatalf("%s(%s) prompt must not reference the Go-nonexistent conclusion-file redirect, got: %q", typ, locale, prompt)
			}
		}
	}
	// explore must still preserve the distillation discipline from TS.
	exploreEN, _ := builtinSubagentPrompt("explore", "en")
	if !strings.Contains(exploreEN, "NEVER return full file contents") {
		t.Fatalf("explore prompt should preserve the distillation discipline, got: %q", exploreEN)
	}
}
