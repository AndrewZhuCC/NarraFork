package narrators

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"
)

// TestExitPlanContinuePromptMatchesTS exercises the helper directly across
// locales, mirroring the three TS plan-approval continuation i18n keys
// (i18n.ts:69-80): tool.exitPlanModeApproved (non-compact, no diff),
// tool.exitPlanModeApprovedWithDiff (non-compact + diff), and
// tool.planCompactContinue (compactAfter). These are the user-message text
// injected to drive plan execution after ExitPlanMode is approved
// (narrator-session.ts:2099-2113).
func TestExitPlanContinuePromptMatchesTS(t *testing.T) {
	// Non-compact, no diff -> exitPlanModeApproved.
	enApproved := exitPlanContinuePrompt(false, "", "en")
	if enApproved != "The user approved your plan. You may now begin execution." {
		t.Fatalf("en approved prompt mismatch: %q", enApproved)
	}
	zhApproved := exitPlanContinuePrompt(false, "", "zh-CN")
	if zhApproved != "用户批准了你的计划，可以开始执行。" {
		t.Fatalf("zh approved prompt mismatch: %q", zhApproved)
	}

	// Non-compact + diff -> exitPlanModeApprovedWithDiff, with the diff embedded
	// between the verbatim TS pre/post segments.
	enDiff := exitPlanContinuePrompt(false, "- old\n+ new", "en")
	if enDiff != "The user edited your plan before approving it. The following changes were made:\n\n- old\n+ new\n\nPlease follow the edited plan." {
		t.Fatalf("en diff prompt mismatch: %q", enDiff)
	}
	zhDiff := exitPlanContinuePrompt(false, "- old\n+ new", "zh-CN")
	if zhDiff != "用户在批准前编辑了你的计划。以下是修改内容：\n\n- old\n+ new\n\n请按照编辑后的计划执行。" {
		t.Fatalf("zh diff prompt mismatch: %q", zhDiff)
	}

	// compactAfter -> planCompactContinue (diff is intentionally ignored).
	enCompact := exitPlanContinuePrompt(true, "- ignored", "en")
	if enCompact != "The user approved your plan and the context has been reset. Your plan is now in the system prompt under Conversation Context. Please begin executing the plan." {
		t.Fatalf("en compact prompt mismatch: %q", enCompact)
	}
	if strings.Contains(enCompact, "ignored") {
		t.Fatalf("compact prompt must not embed the diff body: %q", enCompact)
	}
	zhCompact := exitPlanContinuePrompt(true, "- ignored", "zh-CN")
	if zhCompact != "用户批准了你的计划，上下文已重置。你的计划现在位于系统提示的 Conversation Context 部分。请开始执行计划。" {
		t.Fatalf("zh compact prompt mismatch: %q", zhCompact)
	}

	// Unknown locale falls back to English (mirrors getToolMessage ?? en).
	if got := exitPlanContinuePrompt(false, "", "fr"); got != enApproved {
		t.Fatalf("unknown locale should fall back to en, got %q", got)
	}
}

// TestCompleteExitPlanModeToolUsesOwnerLocaleContinuePrompt verifies the
// continue prompt persisted by completeExitPlanModeTool uses the narrator owner's
// locale (resolved via toolLocale from the earliest top-level user message's
// created_by), not a hardcoded English string.
func TestCompleteExitPlanModeToolUsesOwnerLocaleContinuePrompt(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sqlDB.Close()
	if err := createNarratorTestSchema(sqlDB); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	if _, err := sqlDB.Exec(`CREATE TABLE user_preferences (user_id TEXT PRIMARY KEY, language TEXT)`); err != nil {
		t.Fatalf("create user_preferences: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO user_preferences (user_id, language) VALUES ('user-zh', 'zh-CN')`); err != nil {
		t.Fatalf("insert pref: %v", err)
	}
	service := &Service{DB: sqlDB}
	start := true
	narrator, err := service.Create(context.Background(), CreateInput{Model: "test-model", PermissionMode: "default", StartInPlanMode: &start})
	if err != nil {
		t.Fatalf("create narrator: %v", err)
	}
	// Seed an owning top-level user message so toolLocale resolves to zh-CN.
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := sqlDB.Exec(`INSERT INTO narrator_messages (id, narrator_id, role, parent_tool_use_id, created_by, created_at) VALUES ('owner-msg', ?, 'user', NULL, 'user-zh', ?)`, narrator.ID, now); err != nil {
		t.Fatalf("insert owner message: %v", err)
	}

	plan := "Implement the approved plan by changing the service, updating tests, and validating focused packages."
	payload, err := service.completeExitPlanModeTool(context.Background(), narrator.ID, map[string]any{"plan": plan})
	if err != nil {
		t.Fatalf("complete ExitPlanMode: %v", err)
	}
	prompt := stringFromAny(payload["continuePrompt"])
	if prompt != "用户批准了你的计划，可以开始执行。" {
		t.Fatalf("expected zh owner-locale continue prompt, got %q", prompt)
	}
	if strings.Contains(prompt, "approved your plan") {
		t.Fatalf("continue prompt should not be English for a zh-CN owner: %q", prompt)
	}
}
