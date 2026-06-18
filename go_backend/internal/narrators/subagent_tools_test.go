package narrators

import (
	"sort"
	"testing"
)

// TestSubagentAllowedToolSetsMatchTS pins the complete allowed builtin-tool sets
// for explore/plan/general subagents to the TypeScript EXPLORE_PLAN_TOOLS /
// GENERAL_TOOLS (server/services/subagent-tools.ts). This guards against a tool
// being silently dropped from or added to a subagent's capability set - a
// security-relevant boundary. The TS shell tool is SHELL_TOOL_NAME which is
// "Bash" on the bash deployment target, matching Go's hardcoded "Bash".
func TestSubagentAllowedToolSetsMatchTS(t *testing.T) {
	// TS EXPLORE_PLAN_TOOLS (with SHELL_TOOL_NAME == "Bash" on target).
	explorePlanTS := []string{
		"Read", "Glob", "Grep", "WebSearch", "WebFetch", "Bash",
		"Write", "Edit", "TaskCreate", "TeamStatus", "Await", "Send",
	}
	// TS GENERAL_TOOLS = EXPLORE_PLAN_TOOLS + Skill.
	generalTS := append(append([]string{}, explorePlanTS...), "Skill")

	check := func(subagentType string, wantList []string) {
		allowed := allowedToolsForSubagentType(subagentType, customSubagentToolAccess{})
		got := []string{}
		for tool := range allowed {
			got = append(got, tool)
		}
		sort.Strings(got)
		want := append([]string{}, wantList...)
		sort.Strings(want)
		if len(got) != len(want) {
			t.Fatalf("%s allowed set size = %d %v, want %d %v", subagentType, len(got), got, len(want), want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%s allowed set = %v, want %v", subagentType, got, want)
			}
		}
		// AskUserQuestion and Agent must never be allowed (DISALLOWED + no nesting).
		if allowed["AskUserQuestion"] || allowed["Agent"] {
			t.Fatalf("%s must never allow AskUserQuestion/Agent: %v", subagentType, got)
		}
	}

	check("explore", explorePlanTS)
	check("plan", explorePlanTS)
	check("review", explorePlanTS)
	check("general", generalTS)
	// Unknown builtin type falls back to the read-only explore/plan set.
	check("", explorePlanTS)
}
