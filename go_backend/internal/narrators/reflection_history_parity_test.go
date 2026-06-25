package narrators

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// TestAgentHistoryDoesNotDoubleInjectCompactSummary verifies the parity fix for
// the compact-summary double-injection gap: the summary must reach the model
// ONLY through the system prompt's "## Conversation Context" section, never as a
// "Prior compacted conversation summary" history message. Mirrors TS
// getMessagesSinceLastCompact (narrator-messages.ts:787) which returns only the
// messages strictly after the compact marker.
func TestAgentHistoryDoesNotDoubleInjectCompactSummary(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sqlDB.Close()
	if err := createNarratorTestSchema(sqlDB); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	service := &Service{DB: sqlDB}
	narratorID := "nar_no_double_compact"
	if _, err := sqlDB.Exec(`INSERT INTO narrators (id, type, inherit_mode, model, status, context_summary, created_at, updated_at) VALUES (?, 'primary', 'full', 'openai:gpt-test', 'idle', ?, ?, ?)`, narratorID, "Earlier summary", nowForHistoryTest(0), nowForHistoryTest(0)); err != nil {
		t.Fatalf("insert narrator: %v", err)
	}
	insertHistoryMessage(t, sqlDB, narratorID, 0, "compact", "system", []map[string]any{{"type": "compact", "status": "compacted", "summary": "Earlier summary"}}, "[Compact] Earlier summary", true)
	insertHistoryMessage(t, sqlDB, narratorID, 1, "assistant1", "assistant", []map[string]any{{"type": "text", "text": "After compact"}}, "After compact", false)

	history, err := service.agentHistory(context.Background(), narratorID)
	if err != nil {
		t.Fatalf("agentHistory returned error: %v", err)
	}
	for _, msg := range history {
		if strings.Contains(msg.Content, "Prior compacted conversation summary") {
			t.Fatalf("compact summary must NOT be injected into history; got %#v", msg)
		}
	}
	if len(history) != 1 || history[0].Role != "assistant" || history[0].Content != "After compact" {
		t.Fatalf("history should contain only post-compact messages, got %#v", history)
	}
}

// TestModelVisibleSystemInsertionsUseSysRole verifies that goal_continuation and
// review_feedback insertions are stored with role "sys" so they render to the
// model through agent_history_content.go case "sys". A role of "system" would be
// dropped by the default branch and never reach the model. Mirrors TS
// persistSystemMessage (role="sys") used by narrator-session.ts (goal
// continuation) and review-event-handler.ts (review feedback).
func TestModelVisibleSystemInsertionsUseSysRole(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sqlDB.Close()
	if err := createNarratorTestSchema(sqlDB); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	service := &Service{DB: sqlDB}
	narratorID := "nar_sys_visibility"
	if _, err := sqlDB.Exec(`INSERT INTO narrators (id, type, inherit_mode, model, status, created_at, updated_at) VALUES (?, 'primary', 'full', 'openai:gpt-test', 'idle', ?, ?)`, narratorID, nowForHistoryTest(0), nowForHistoryTest(0)); err != nil {
		t.Fatalf("insert narrator: %v", err)
	}
	// goal_continuation, stored as role "sys" by maybeStartGoalContinuation.
	insertHistoryMessage(t, sqlDB, narratorID, 1, "goalcont", "sys", []map[string]any{{"type": "goal_continuation", "goalId": "g1", "objective": "finish it"}}, "Continue working toward the active NarraFork goal.", false)
	// review_feedback, stored as role "sys" by injectReviewFeedback.
	insertHistoryMessage(t, sqlDB, narratorID, 2, "reviewfb", "sys", []map[string]any{{"type": "review_feedback", "verdict": "approve", "findings": []any{}}}, "## Code Review: Approved", false)

	history, err := service.agentHistory(context.Background(), narratorID)
	if err != nil {
		t.Fatalf("agentHistory returned error: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("expected both sys insertions to be model-visible, got %#v", history)
	}
	for i, want := range []string{"Continue working toward the active NarraFork goal.", "## Code Review: Approved"} {
		if history[i].Role != "system" {
			t.Fatalf("sys-role message %d should render as a model system message, got role %q", i, history[i].Role)
		}
		if !strings.Contains(history[i].Content, want) {
			t.Fatalf("sys-role message %d content mismatch: got %q want substring %q", i, history[i].Content, want)
		}
	}
}

// TestRoleSystemInsertionsAreNotModelVisible guards the negative case: a message
// persisted with role "system" (the legacy/internal marker role, e.g. a compact
// marker body) must NOT surface as a model message via agent_history_content's
// default branch. Only "sys" is model-visible. This protects the boundary the
// parity fix relies on.
func TestRoleSystemInsertionsAreNotModelVisible(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sqlDB.Close()
	if err := createNarratorTestSchema(sqlDB); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	service := &Service{DB: sqlDB}
	narratorID := "nar_system_hidden"
	if _, err := sqlDB.Exec(`INSERT INTO narrators (id, type, inherit_mode, model, status, created_at, updated_at) VALUES (?, 'primary', 'full', 'openai:gpt-test', 'idle', ?, ?)`, narratorID, nowForHistoryTest(0), nowForHistoryTest(0)); err != nil {
		t.Fatalf("insert narrator: %v", err)
	}
	// A plain role="system" message (no goal_continuation block) is internal-only.
	insertHistoryMessage(t, sqlDB, narratorID, 1, "sysmark", "system", []map[string]any{{"type": "text", "text": "internal marker"}}, "internal marker", false)
	insertHistoryMessage(t, sqlDB, narratorID, 2, "assistant1", "assistant", []map[string]any{{"type": "text", "text": "visible"}}, "visible", false)

	history, err := service.agentHistory(context.Background(), narratorID)
	if err != nil {
		t.Fatalf("agentHistory returned error: %v", err)
	}
	if len(history) != 1 || history[0].Content != "visible" {
		t.Fatalf("role=system message must be dropped from model history, got %#v", history)
	}
}

// TestConversationContextPrecedesWorkingDirectory verifies the system-prompt
// section ordering parity fix: the compact summary ("## Conversation Context")
// must be injected BEFORE "## Current Working Directory", mirroring TS
// buildEffectiveSystemPrompt step 1 (narrator-prompt.ts:64-70).
func TestConversationContextPrecedesWorkingDirectory(t *testing.T) {
	summary := "Earlier compacted summary text"
	cwd := t.TempDir()
	narrator := Narrator{
		ID:             "nar_ctx_order",
		Cwd:            &cwd,
		ContextSummary: &summary,
	}
	service := &Service{}
	prompt := service.agentSystemPrompt(context.Background(), narrator)
	ctxIdx := strings.Index(prompt, "## Conversation Context")
	cwdIdx := strings.Index(prompt, "## Current Working Directory")
	if ctxIdx < 0 {
		t.Fatalf("system prompt missing Conversation Context section:\n%s", prompt)
	}
	if cwdIdx < 0 {
		t.Fatalf("system prompt missing Current Working Directory section:\n%s", prompt)
	}
	if ctxIdx > cwdIdx {
		t.Fatalf("Conversation Context (%d) must precede Current Working Directory (%d)", ctxIdx, cwdIdx)
	}
	if !strings.Contains(prompt, summary) {
		t.Fatalf("system prompt should contain the compact summary, got:\n%s", prompt)
	}
}
