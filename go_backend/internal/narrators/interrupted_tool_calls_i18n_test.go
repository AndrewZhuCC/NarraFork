package narrators

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestInterruptedByUserToolOutputBilingual pins the verbatim model-facing tool
// result text to the TS tool.interruptedByUser i18n message (i18n.ts:26-29).
func TestInterruptedByUserToolOutputBilingual(t *testing.T) {
	if got := interruptedByUserToolOutput("en"); got != "The user interrupted this tool call before it could execute." {
		t.Fatalf("en interruptedByUser = %q", got)
	}
	if got := interruptedByUserToolOutput("zh-CN"); got != "用户在此工具调用执行前中断了会话。" {
		t.Fatalf("zh-CN interruptedByUser = %q", got)
	}
	// Unknown locale falls back to English.
	if got := interruptedByUserToolOutput("fr"); got != "The user interrupted this tool call before it could execute." {
		t.Fatalf("unknown locale should fall back to en, got %q", got)
	}
}

// TestMarkInterruptedToolCallsConvertsInFlightCalls verifies that on user
// interrupt, every in-flight (initializing/pending/running) tool call is set to
// status=fail with the internal errorMessage ("Narrator interrupted by user")
// and a model-facing output_json carrying the locale-resolved interruptedByUser
// text. Already-finished calls and other narrators' calls must be untouched.
// Mirrors TS markInterruptedToolCallsForMessage + cleanupOrphanedToolCalls.
func TestMarkInterruptedToolCallsConvertsInFlightCalls(t *testing.T) {
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
	narrator, err := service.Create(context.Background(), CreateInput{Model: "test-model", PermissionMode: "default"})
	if err != nil {
		t.Fatalf("create narrator: %v", err)
	}
	// Seed an owning top-level user message so toolLocale resolves to zh-CN.
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := sqlDB.Exec(`INSERT INTO narrator_messages (id, narrator_id, role, parent_tool_use_id, created_by, created_at) VALUES ('owner-msg', ?, 'user', NULL, 'user-zh', ?)`, narrator.ID, now); err != nil {
		t.Fatalf("insert owner message: %v", err)
	}

	// Three in-flight statuses (must be converted) + one already-success call
	// (must be left untouched).
	for _, tc := range []struct{ id, status string }{
		{"init-tc", "initializing"},
		{"pending-tc", "pending"},
		{"run-tc", "running"},
	} {
		if _, err := sqlDB.Exec(`INSERT INTO narrator_tool_calls (id, narrator_id, tool_use_id, tool_name, input_json, status, created_at) VALUES (?, ?, ?, 'Bash', '{}', ?, ?)`,
			tc.id, narrator.ID, tc.id, tc.status, now); err != nil {
			t.Fatalf("insert %s tool call: %v", tc.status, err)
		}
	}
	if _, err := sqlDB.Exec(`INSERT INTO narrator_tool_calls (id, narrator_id, tool_use_id, tool_name, input_json, output_json, status, completed_at, created_at) VALUES ('done-tc', ?, 'done-tc', 'Read', '{}', '{"output":"already done"}', 'success', ?, ?)`,
		narrator.ID, now, now); err != nil {
		t.Fatalf("insert finished tool call: %v", err)
	}
	// Another narrator's in-flight call must NOT be touched.
	if _, err := sqlDB.Exec(`INSERT INTO narrator_tool_calls (id, narrator_id, tool_use_id, tool_name, input_json, status, created_at) VALUES ('other-tc', 'other-narrator', 'other-tc', 'Bash', '{}', 'running', ?)`, now); err != nil {
		t.Fatalf("insert other narrator tool call: %v", err)
	}

	if err := service.markInterruptedToolCalls(context.Background(), narrator.ID); err != nil {
		t.Fatalf("markInterruptedToolCalls: %v", err)
	}

	for _, id := range []string{"init-tc", "pending-tc", "run-tc"} {
		var status, errorMessage, outputJSON, completedAt string
		if err := sqlDB.QueryRow(`SELECT status, COALESCE(error_message, ''), COALESCE(output_json, ''), COALESCE(completed_at, '') FROM narrator_tool_calls WHERE id = ?`, id).Scan(&status, &errorMessage, &outputJSON, &completedAt); err != nil {
			t.Fatalf("load %s: %v", id, err)
		}
		if status != "fail" {
			t.Fatalf("%s: expected status fail, got %s", id, status)
		}
		if errorMessage != "Narrator interrupted by user" {
			t.Fatalf("%s: expected internal errorMessage %q, got %q", id, "Narrator interrupted by user", errorMessage)
		}
		if completedAt == "" {
			t.Fatalf("%s: expected completed_at to be set", id)
		}
		var parsed map[string]any
		if err := json.Unmarshal([]byte(outputJSON), &parsed); err != nil {
			t.Fatalf("%s: output_json not JSON: %v (%q)", id, err, outputJSON)
		}
		output, _ := parsed["output"].(string)
		// zh-CN owner → localized model-facing output.
		if output != "用户在此工具调用执行前中断了会话。" {
			t.Fatalf("%s: expected zh-CN interruptedByUser tool output, got %q", id, output)
		}
		// Model-facing output must NOT leak the internal English error string.
		if strings.Contains(output, "Narrator interrupted by user") {
			t.Fatalf("%s: model-facing output must differ from internal errorMessage, got %q", id, output)
		}
	}

	// Already-finished call is untouched.
	var doneStatus, doneOutput string
	if err := sqlDB.QueryRow(`SELECT status, COALESCE(output_json, '') FROM narrator_tool_calls WHERE id = 'done-tc'`).Scan(&doneStatus, &doneOutput); err != nil {
		t.Fatalf("load done-tc: %v", err)
	}
	if doneStatus != "success" || !strings.Contains(doneOutput, "already done") {
		t.Fatalf("finished call should be untouched, got status=%s output=%q", doneStatus, doneOutput)
	}

	// Other narrator's call is untouched.
	var otherStatus string
	if err := sqlDB.QueryRow(`SELECT status FROM narrator_tool_calls WHERE id = 'other-tc'`).Scan(&otherStatus); err != nil {
		t.Fatalf("load other-tc: %v", err)
	}
	if otherStatus != "running" {
		t.Fatalf("other narrator's call should be untouched, got status=%s", otherStatus)
	}
}

// TestFinishAgentRunCancelledMarksInterruptedToolCalls is the end-to-end check:
// running runBasicAgent and cancelling it (user interrupt) must, via
// finishAgentRunCancelled, finalize the narrator's in-flight tool calls with the
// owner-locale interruptedByUser output (English owner here).
func TestFinishAgentRunCancelledMarksInterruptedToolCalls(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sqlDB.Close()
	if err := createNarratorTestSchema(sqlDB); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	service := &Service{DB: sqlDB}
	narrator, err := service.Create(context.Background(), CreateInput{Model: "test-model", PermissionMode: "default"})
	if err != nil {
		t.Fatalf("create narrator: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	// English owner (created_by user with no zh preference → en default).
	if _, err := sqlDB.Exec(`INSERT INTO narrator_messages (id, narrator_id, role, parent_tool_use_id, created_by, created_at) VALUES ('owner-msg', ?, 'user', NULL, 'user-en', ?)`, narrator.ID, now); err != nil {
		t.Fatalf("insert owner message: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO narrator_tool_calls (id, narrator_id, tool_use_id, tool_name, input_json, status, created_at) VALUES ('run-tc', ?, 'run-tc', 'Bash', '{}', 'running', ?)`, narrator.ID, now); err != nil {
		t.Fatalf("insert running tool call: %v", err)
	}

	state := &agentRunState{narratorID: narrator.ID}
	if err := service.finishAgentRunCancelled(context.Background(), state); err != nil {
		t.Fatalf("finishAgentRunCancelled: %v", err)
	}

	var status, errorMessage, outputJSON string
	if err := sqlDB.QueryRow(`SELECT status, COALESCE(error_message, ''), COALESCE(output_json, '') FROM narrator_tool_calls WHERE id = 'run-tc'`).Scan(&status, &errorMessage, &outputJSON); err != nil {
		t.Fatalf("load run-tc: %v", err)
	}
	if status != "fail" {
		t.Fatalf("expected status fail after cancel, got %s", status)
	}
	if errorMessage != "Narrator interrupted by user" {
		t.Fatalf("expected internal errorMessage, got %q", errorMessage)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(outputJSON), &parsed); err != nil {
		t.Fatalf("output_json not JSON: %v (%q)", err, outputJSON)
	}
	output, _ := parsed["output"].(string)
	if output != "The user interrupted this tool call before it could execute." {
		t.Fatalf("expected en interruptedByUser tool output, got %q", output)
	}
}
