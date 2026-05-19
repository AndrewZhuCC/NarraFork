package narrators

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDisabledToolsFromTraitsParsesCustomTrait(t *testing.T) {
	payload, _ := json.Marshal(map[string]any{"version": 1, "tools": []string{"Read", "Write"}})
	trait := "custom-disabled-tools:" + base64.RawURLEncoding.EncodeToString(payload)
	tools := disabledToolsFromTraits([]string{"unrelated", trait})
	if len(tools) != 2 || tools[0] != "Read" || tools[1] != "Write" {
		t.Fatalf("disabled tools = %#v, want Read/Write", tools)
	}
}

func TestAnyStringSliceAcceptsEnabledToolsStringSlice(t *testing.T) {
	tools := anyStringSlice([]string{"Browser", "", "ShareFile"})
	if len(tools) != 2 || tools[0] != "Browser" || tools[1] != "ShareFile" {
		t.Fatalf("enabled tools = %#v, want Browser/ShareFile", tools)
	}
}

func TestOptionalToolMenuOnlyIncludesGoSupportedTools(t *testing.T) {
	items := optionalToolMenuItems()
	seen := map[string]bool{}
	for _, item := range items {
		seen[stringValue(item["toolName"])] = true
	}
	if !seen["Terminal"] || !seen["Browser"] || !seen["ShareFile"] {
		t.Fatalf("supported optional tools missing from menu: %#v", items)
	}
	for _, name := range []string{"Recall", "ForkNarrator", "NarraForkAdmin"} {
		if seen[name] {
			t.Fatalf("unsupported optional tool %s leaked into /load menu: %#v", name, items)
		}
	}
}

func TestSubagentDisabledToolsKeepPlanWriteEditVisible(t *testing.T) {
	disabled := disabledToolsForSubagentType("plan")
	set := map[string]bool{}
	for _, tool := range disabled {
		set[tool] = true
	}
	if !set["AskUserQuestion"] || !set["Agent"] || !set["Skill"] {
		t.Fatalf("plan subagent disabled tools missing unsafe entries: %#v", disabled)
	}
	if set["Write"] || set["Edit"] {
		t.Fatalf("plan subagent must not disable Write/Edit because plan-mode restricts destination: %#v", disabled)
	}
}

func TestGeneralSubagentAllowsSkillButNotNestedAgent(t *testing.T) {
	disabled := disabledToolsForSubagentType("general")
	set := map[string]bool{}
	for _, tool := range disabled {
		set[tool] = true
	}
	if set["Skill"] {
		t.Fatalf("general subagent should retain Skill: %#v", disabled)
	}
	if !set["Agent"] || !set["AskUserQuestion"] {
		t.Fatalf("general subagent should disable nested/interactive tools: %#v", disabled)
	}
}

func TestCustomSubagentToolAccessCustomToolsFilterStaticTools(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".narrafork", "subagents", "audit")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir custom subagent: %v", err)
	}
	content := `---
name: Audit
toolAccess: custom
customTools:
  - Read
  - AskUserQuestion
  - Agent
  - mcp__filesystem__ask_tool
---
Audit prompt.
`
	if err := os.WriteFile(filepath.Join(dir, "SUBAGENT.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write custom subagent: %v", err)
	}
	access := customSubagentToolAccessForType("audit")
	if !access.Found || access.ToolAccess != "custom" || len(access.CustomTools) != 4 {
		t.Fatalf("unexpected custom access: %#v", access)
	}
	disabled := disabledToolsForSubagentType("audit")
	set := map[string]bool{}
	for _, tool := range disabled {
		set[tool] = true
	}
	if set["Read"] {
		t.Fatalf("custom subagent should allow explicitly listed Read: %#v", disabled)
	}
	if !set["Grep"] || !set["AskUserQuestion"] || !set["Agent"] {
		t.Fatalf("custom subagent should disable unlisted and never-allowed tools: %#v", disabled)
	}
}
