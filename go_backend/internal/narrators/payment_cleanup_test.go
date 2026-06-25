package narrators

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"narrafork-go/internal/agentbasic"
)

// TestPaymentRequiredCleansUpOrphanedToolCalls verifies that finalizing a run on
// payment_required fails any in-flight tool calls (mirrors the TS
// finalizeOrCleanupPartialMessage path on the payment_required branch,
// narrator-session.ts:1827-1843) using error wording rather than the
// user-interrupt text.
func TestPaymentRequiredCleansUpOrphanedToolCalls(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	hub, _ := captureNarratorBroadcasts()
	service := &Service{DB: sqlDB, Hub: hub}
	ctx := context.Background()

	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := sqlDB.Exec(`INSERT INTO narrator_tool_calls (id, narrator_id, tool_use_id, tool_name, input_json, status, created_at) VALUES ('pay-tc', ?, 'pay-tc', 'Bash', '{}', 'running', ?)`, narratorID, now); err != nil {
		t.Fatalf("insert running tool call: %v", err)
	}

	state := &agentRunState{narratorID: narratorID, narrator: mustGetNarrator(t, service, narratorID)}
	event := agentbasic.AgentEvent{Type: "payment_required", Message: "Insufficient balance", ProviderPrefix: "nug", ResumeAction: "retry"}
	if err := service.finishAgentRunPaymentRequired(ctx, state, event); err != nil {
		t.Fatalf("finishAgentRunPaymentRequired: %v", err)
	}

	var status, errorMessage, outputJSON string
	if err := sqlDB.QueryRow(`SELECT status, COALESCE(error_message, ''), COALESCE(output_json, '') FROM narrator_tool_calls WHERE id = 'pay-tc'`).Scan(&status, &errorMessage, &outputJSON); err != nil {
		t.Fatalf("load pay-tc: %v", err)
	}
	if status != "fail" {
		t.Fatalf("expected orphaned tool call to be failed on payment_required, got %s", status)
	}
	// Internal error message reflects payment, NOT a user interrupt.
	if strings.Contains(errorMessage, "interrupted by user") {
		t.Fatalf("payment orphan must not be labeled as user interrupt, got %q", errorMessage)
	}
	if errorMessage == "" {
		t.Fatalf("expected an internal error message on payment orphan")
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(outputJSON), &parsed); err != nil {
		t.Fatalf("output_json not JSON: %v (%q)", err, outputJSON)
	}
	output, _ := parsed["output"].(string)
	if strings.Contains(output, "user interrupted") || strings.Contains(output, "用户在此工具调用") {
		t.Fatalf("model-facing output must not be the user-interrupt text, got %q", output)
	}

	// Narrator still finalizes with the payment_required substatus + JSON error blob.
	var nStatus, nSubstatus string
	var nError sql.NullString
	if err := sqlDB.QueryRow(`SELECT status, substatus, error_message FROM narrators WHERE id = ?`, narratorID).Scan(&nStatus, &nSubstatus, &nError); err != nil {
		t.Fatalf("load narrator: %v", err)
	}
	if nStatus != "idle" || nSubstatus != `["payment_required"]` {
		t.Fatalf("payment finalize status=%s substatus=%s want idle/[payment_required]", nStatus, nSubstatus)
	}
	if !nError.Valid || !strings.Contains(nError.String, "payment_required") {
		t.Fatalf("expected payment_required JSON blob in error_message, got %#v", nError)
	}
}

// TestErrorAndTimeoutOrphansUseNonInterruptWording verifies the error and timeout
// finalizers fail in-flight tool calls with error/timeout semantics rather than
// mislabeling them as a user interrupt.
func TestErrorAndTimeoutOrphansUseNonInterruptWording(t *testing.T) {
	cases := []struct {
		name   string
		run    func(s *Service, state *agentRunState) error
		reason string
	}{
		{
			name: "error",
			run: func(s *Service, state *agentRunState) error {
				return s.finishAgentRunError(context.Background(), state, "boom")
			},
			reason: "failed",
		},
		{
			name: "timeout",
			run: func(s *Service, state *agentRunState) error {
				return s.finishAgentRunTimeout(context.Background(), state, "slow")
			},
			reason: "timed out",
		},
		{
			name: "silent_disconnect",
			run: func(s *Service, state *agentRunState) error {
				return s.finishAgentRunSilentDisconnect(context.Background(), state)
			},
			reason: "disconnected",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sqlDB := openNarratorHandlerTestDB(t)
			defer sqlDB.Close()
			narratorID := insertNarratorHandlerTestRow(t, sqlDB)
			hub, _ := captureNarratorBroadcasts()
			service := &Service{DB: sqlDB, Hub: hub}
			now := time.Now().UTC().Format(time.RFC3339Nano)
			if _, err := sqlDB.Exec(`INSERT INTO narrator_tool_calls (id, narrator_id, tool_use_id, tool_name, input_json, status, created_at) VALUES ('orphan-tc', ?, 'orphan-tc', 'Bash', '{}', 'running', ?)`, narratorID, now); err != nil {
				t.Fatalf("insert running tool call: %v", err)
			}
			state := &agentRunState{narratorID: narratorID, narrator: mustGetNarrator(t, service, narratorID)}
			if err := tc.run(service, state); err != nil {
				t.Fatalf("finalize %s: %v", tc.name, err)
			}
			var status, errorMessage, outputJSON string
			if err := sqlDB.QueryRow(`SELECT status, COALESCE(error_message, ''), COALESCE(output_json, '') FROM narrator_tool_calls WHERE id = 'orphan-tc'`).Scan(&status, &errorMessage, &outputJSON); err != nil {
				t.Fatalf("load orphan-tc: %v", err)
			}
			if status != "fail" {
				t.Fatalf("%s: expected orphan tool failed, got %s", tc.name, status)
			}
			if strings.Contains(errorMessage, "interrupted by user") {
				t.Fatalf("%s: orphan must not be labeled as user interrupt, got %q", tc.name, errorMessage)
			}
			var parsed map[string]any
			if err := json.Unmarshal([]byte(outputJSON), &parsed); err != nil {
				t.Fatalf("%s: output_json not JSON: %v", tc.name, err)
			}
			output, _ := parsed["output"].(string)
			if output != orphanedToolOutputForError("en") {
				t.Fatalf("%s: model-facing output = %q, want error-semantics text", tc.name, output)
			}
		})
	}
}

// TestMaxTurnsExceededFinalizesError verifies the max_turns_exceeded event sets
// the "Max turns (N) exceeded" message and finalizes via the error path,
// mirroring TS narrator-executor.ts:163-167.
func TestMaxTurnsExceededFinalizesError(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	hub, events := captureNarratorBroadcasts()
	service := &Service{DB: sqlDB, Hub: hub}
	ctx := context.Background()

	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := sqlDB.Exec(`INSERT INTO narrator_tool_calls (id, narrator_id, tool_use_id, tool_name, input_json, status, created_at) VALUES ('mt-tc', ?, 'mt-tc', 'Bash', '{}', 'running', ?)`, narratorID, now); err != nil {
		t.Fatalf("insert running tool call: %v", err)
	}

	state := &agentRunState{narratorID: narratorID, narrator: mustGetNarrator(t, service, narratorID)}
	event := agentbasic.AgentEvent{Type: "max_turns_exceeded", Metadata: map[string]any{"maxTurns": 7}}
	if err := service.handleAgentEvent(ctx, state, event); err != nil {
		t.Fatalf("handle max_turns_exceeded: %v", err)
	}
	if !state.errored {
		t.Fatalf("max_turns_exceeded should finalize errored")
	}
	if state.errorMessage != "Max turns (7) exceeded" {
		t.Fatalf("errorMessage = %q, want %q", state.errorMessage, "Max turns (7) exceeded")
	}
	if state.errorCode != "max_turns_exceeded" {
		t.Fatalf("errorCode = %q, want max_turns_exceeded", state.errorCode)
	}
	// Orphaned tool call must be failed (error-path cleanup).
	var status string
	if err := sqlDB.QueryRow(`SELECT status FROM narrator_tool_calls WHERE id = 'mt-tc'`).Scan(&status); err != nil {
		t.Fatalf("load mt-tc: %v", err)
	}
	if status != "fail" {
		t.Fatalf("expected orphan tool failed after max_turns, got %s", status)
	}
	var nStatus, nSubstatus string
	if err := sqlDB.QueryRow(`SELECT status, substatus FROM narrators WHERE id = ?`, narratorID).Scan(&nStatus, &nSubstatus); err != nil {
		t.Fatalf("load narrator: %v", err)
	}
	if nStatus != "idle" || nSubstatus != `["error"]` {
		t.Fatalf("max_turns finalize status=%s substatus=%s want idle/[error]", nStatus, nSubstatus)
	}
	// Both the raw frame and the error broadcast should fire.
	if !hasBroadcast(events(), narratorID, "max_turns_exceeded") {
		t.Fatalf("expected raw max_turns_exceeded broadcast, got %#v", events())
	}
	if !hasBroadcast(events(), narratorID, "narrator_error") {
		t.Fatalf("expected narrator_error broadcast, got %#v", events())
	}
}
