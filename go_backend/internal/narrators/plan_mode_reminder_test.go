package narrators

import (
	"context"
	"strings"
	"testing"

	"narrafork-go/internal/config"
)

// TestPlanModeSystemReminderTextMatchesTS exercises the helper directly across
// locale x allowInline, mirroring TS getPlanModeSystemReminder (prompt-i18n.ts).
func TestPlanModeSystemReminderTextMatchesTS(t *testing.T) {
	enInline := planModeSystemReminder(".narrafork/plan-abc.md", "en", true)
	for _, want := range []string{
		"<system-reminder>",
		"# Plan Mode",
		"CRITICAL: Plan mode is ACTIVE",
		".narrafork/plan-abc.md",
		"## Plan Submission — Two Modes",
		"### Mode A: Inline (for short/medium plans)",
		"### Mode B: File-based",
		"</system-reminder>",
	} {
		if !strings.Contains(enInline, want) {
			t.Fatalf("en+inline reminder missing %q:\n%s", want, enInline)
		}
	}

	enFile := planModeSystemReminder(".narrafork/plan-abc.md", "en", false)
	if !strings.Contains(enFile, "## Plan Submission — File-based only") {
		t.Fatalf("en+file-only reminder missing file-based-only section:\n%s", enFile)
	}
	if strings.Contains(enFile, "Mode A: Inline") {
		t.Fatalf("en+file-only reminder must not advertise inline Mode A:\n%s", enFile)
	}

	zhInline := planModeSystemReminder(".narrafork/plan-xyz.md", "zh-CN", true)
	for _, want := range []string{
		"<system-reminder>",
		"# 计划模式",
		"计划模式已激活",
		".narrafork/plan-xyz.md",
		"## 计划提交 — 两种模式",
		"### 模式 A：内联",
		"### 模式 B：文件模式",
	} {
		if !strings.Contains(zhInline, want) {
			t.Fatalf("zh+inline reminder missing %q:\n%s", want, zhInline)
		}
	}
}

// TestAgentSystemPromptPlanModeUsesSystemReminderBlock verifies the end-to-end
// system prompt for a zh owner in plan mode appends the verbatim TS
// <system-reminder> block (not the old single-line "## Plan Mode" section) and
// drops the Go-only plan-compact fallback sentence.
func TestAgentSystemPromptPlanModeUsesSystemReminderBlock(t *testing.T) {
	db := openReplyLanguageTestDB(t)
	svc := &Service{DB: db}
	insertReplyLangPref(t, db, "user-zh", "zh-CN", 1)
	insertReplyLangUserMessage(t, db, "nar-zh", "user-zh")
	cwd := t.TempDir()
	planID := "abc"
	prompt := svc.agentSystemPrompt(context.Background(), Narrator{
		ID:           "nar-zh",
		Cwd:          &cwd,
		SystemPrompt: ptr("base"),
		PlanMode:     true,
		PlanFileID:   &planID,
	})
	for _, want := range []string{
		"<system-reminder>",
		"# 计划模式",
		planFileRelativePath(planID),
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("plan-mode prompt missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "currently in NarraFork plan mode") {
		t.Fatalf("plan-mode prompt still contains the old single-line text:\n%s", prompt)
	}
	if strings.Contains(prompt, "If TS-style plan compact") {
		t.Fatalf("plan-mode prompt must drop the Go-only compact-fallback sentence:\n%s", prompt)
	}
}

// TestAgentSystemPromptPlanModeFileOnlyWhenInlineDisabled verifies allowInline=false
// (agent.planModeAllowInlinePlan=false) produces the file-based-only submission.
func TestAgentSystemPromptPlanModeFileOnlyWhenInlineDisabled(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store, err := config.Load()
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	if _, err := store.Patch(map[string]any{
		"agent": map[string]any{"planModeAllowInlinePlan": false},
	}); err != nil {
		t.Fatalf("patch settings: %v", err)
	}

	db := openReplyLanguageTestDB(t)
	svc := &Service{DB: db, Settings: store}
	insertReplyLangPref(t, db, "user-en", "en", 1)
	insertReplyLangUserMessage(t, db, "nar-en", "user-en")
	cwd := t.TempDir()
	planID := "def"
	prompt := svc.agentSystemPrompt(context.Background(), Narrator{
		ID:           "nar-en",
		Cwd:          &cwd,
		SystemPrompt: ptr("base"),
		PlanMode:     true,
		PlanFileID:   &planID,
	})
	if !strings.Contains(prompt, "## Plan Submission — File-based only") {
		t.Fatalf("inline-disabled plan-mode prompt missing file-based-only section:\n%s", prompt)
	}
	if strings.Contains(prompt, "Mode A: Inline") {
		t.Fatalf("inline-disabled plan-mode prompt must not advertise inline Mode A:\n%s", prompt)
	}
}
