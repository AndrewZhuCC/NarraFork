package narrators

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"narrafork-go/internal/agentbasic"
)

func TestContextOverflowInvalidStateCompactsAndSchedulesRetry(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	hub, events := captureNarratorBroadcasts()
	service := &Service{DB: sqlDB, Hub: hub}
	ctx := context.Background()
	for i, content := range []string{"one", "two", "three", "four", "five", "six", "seven"} {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		if _, err := service.addMessage(ctx, narratorID, role, content, nil); err != nil {
			t.Fatalf("add message %d: %v", i, err)
		}
	}
	narrator, err := service.Get(ctx, narratorID)
	if err != nil {
		t.Fatalf("load narrator: %v", err)
	}
	retried := false
	service.contextOverflowRetry = func(_ context.Context, id string) {
		retried = id == narratorID
	}
	state := &agentRunState{narratorID: narratorID, narrator: narrator}
	event := agentbasic.AgentEvent{Type: "invalid_state", Reason: "model_context_window_exceeded", Message: "The model has reached its context window limit."}
	if err := service.handleAgentEvent(ctx, state, event); err != nil {
		t.Fatalf("handle overflow event: %v", err)
	}
	if !state.completed || state.errored {
		t.Fatalf("state completed=%v errored=%v", state.completed, state.errored)
	}
	if !retried {
		t.Fatalf("expected overflow retry to be scheduled")
	}
	if !hasBroadcast(events(), narratorID, "invalid_state") || !hasBroadcast(events(), narratorID, "context_length_exceeded") || !hasBroadcast(events(), narratorID, "compacting") || !hasBroadcast(events(), narratorID, "message") || !hasBroadcast(events(), narratorID, "compact_done") {
		t.Fatalf("expected stable overflow broadcasts, got %#v", events())
	}
	statusBroadcast := findBroadcast(events(), narratorID, "status")
	if statusBroadcast == nil || statusBroadcast["status"] != "working" || statusBroadcast["isBackground"] != false {
		t.Fatalf("expected working status broadcast after overflow compact, got %#v all=%#v", statusBroadcast, events())
	}
	if hasBroadcast(events(), narratorID, "narrator_error") {
		t.Fatalf("overflow retry path should not broadcast narrator_error, got %#v", events())
	}
	var status, substatus string
	var errorMessage sql.NullString
	var contextSummary sql.NullString
	if err := sqlDB.QueryRow(`SELECT status, substatus, error_message, context_summary FROM narrators WHERE id = ?`, narratorID).Scan(&status, &substatus, &errorMessage, &contextSummary); err != nil {
		t.Fatalf("load narrator status: %v", err)
	}
	if status != "working" || substatus != `["calling_model"]` || errorMessage.Valid {
		t.Fatalf("unexpected narrator state status=%s substatus=%s error=%#v", status, substatus, errorMessage)
	}
	if !contextSummary.Valid || !strings.Contains(contextSummary.String, "Context overflow fallback summary") || !strings.Contains(contextSummary.String, "one") {
		t.Fatalf("unexpected context summary: %#v", contextSummary)
	}
	var compactCount int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM narrator_message_refs WHERE narrator_id = ? AND is_compact = 1`, narratorID).Scan(&compactCount); err != nil {
		t.Fatalf("count compact refs: %v", err)
	}
	if compactCount != 1 {
		t.Fatalf("compact refs = %d, want 1", compactCount)
	}
	// The overflow compact content_text mirrors the TS finalizeCompactingMessage
	// format: "[Compact] " + summary.slice(0,200) + "..." (not "[Compact]\n" + full).
	var overflowContentText string
	if err := sqlDB.QueryRow(`SELECT m.content_text FROM narrator_messages m JOIN narrator_message_refs r ON r.message_id = m.id WHERE r.narrator_id = ? AND r.is_compact = 1`, narratorID).Scan(&overflowContentText); err != nil {
		t.Fatalf("load overflow compact content_text: %v", err)
	}
	if !strings.HasPrefix(overflowContentText, "[Compact] ") || !strings.HasSuffix(overflowContentText, "...") {
		t.Fatalf("overflow compact content_text should be \"[Compact] <summary>...\", got %q", overflowContentText)
	}
	if strings.HasPrefix(overflowContentText, "[Compact]\n") {
		t.Fatalf("overflow compact content_text should not use the old \"[Compact]\\n\" format, got %q", overflowContentText)
	}
	history, err := service.agentHistory(ctx, narratorID)
	if err != nil {
		t.Fatalf("agent history: %v", err)
	}
	if len(history) < 2 || !strings.Contains(history[0].Content, "Prior compacted conversation summary") {
		t.Fatalf("expected compact summary at start of rebuilt history, got %#v", history)
	}
	joined := ""
	for _, item := range history {
		joined += item.Content + "\n"
	}
	if !strings.Contains(joined, "four") || !strings.Contains(joined, "seven") {
		t.Fatalf("rebuilt history should keep recent messages after compact, got %#v", history)
	}
}

func TestInvalidStateNonOverflowFinishesErrorWithoutContextCompact(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	hub, events := captureNarratorBroadcasts()
	service := &Service{DB: sqlDB, Hub: hub}
	ctx := context.Background()
	narrator, err := service.Get(ctx, narratorID)
	if err != nil {
		t.Fatalf("load narrator: %v", err)
	}
	state := &agentRunState{narratorID: narratorID, narrator: narrator}
	event := agentbasic.AgentEvent{Type: "invalid_state", Code: "max_tokens", Reason: "output limit", Message: "The model stopped because max_tokens was reached."}
	if err := service.handleAgentEvent(ctx, state, event); err != nil {
		t.Fatalf("handle non-overflow invalid_state: %v", err)
	}
	if !state.completed || !state.errored || state.errorCode != "max_tokens" {
		t.Fatalf("state should finish via error path, completed=%v errored=%v code=%q", state.completed, state.errored, state.errorCode)
	}
	var status, substatus string
	var errorMessage sql.NullString
	var contextSummary sql.NullString
	if err := sqlDB.QueryRow(`SELECT status, substatus, error_message, context_summary FROM narrators WHERE id = ?`, narratorID).Scan(&status, &substatus, &errorMessage, &contextSummary); err != nil {
		t.Fatalf("load narrator status: %v", err)
	}
	if status != "idle" || substatus != `["error"]` || !errorMessage.Valid || !strings.Contains(errorMessage.String, "max_tokens") || contextSummary.Valid {
		t.Fatalf("unexpected non-overflow invalid_state persistence status=%s substatus=%s error=%#v summary=%#v", status, substatus, errorMessage, contextSummary)
	}
	var compactCount int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM narrator_message_refs WHERE narrator_id = ? AND is_compact = 1`, narratorID).Scan(&compactCount); err != nil {
		t.Fatalf("count compact refs: %v", err)
	}
	if compactCount != 0 {
		t.Fatalf("non-overflow invalid_state should not compact, compact refs = %d", compactCount)
	}
	if !hasBroadcast(events(), narratorID, "invalid_state") || !hasBroadcast(events(), narratorID, "status") || !hasBroadcast(events(), narratorID, "narrator_error") {
		t.Fatalf("expected invalid_state error-path broadcasts, got %#v", events())
	}
	if hasBroadcast(events(), narratorID, "context_length_exceeded") || hasBroadcast(events(), narratorID, "compacting") || hasBroadcast(events(), narratorID, "compact_done") {
		t.Fatalf("non-overflow invalid_state should not use overflow broadcasts, got %#v", events())
	}
	statusBroadcast := findBroadcast(events(), narratorID, "status")
	if statusBroadcast["status"] != "idle" {
		t.Fatalf("expected idle status broadcast, got %#v", statusBroadcast)
	}
	errorBroadcast := findBroadcast(events(), narratorID, "narrator_error")
	if errorBroadcast["errorCode"] != "max_tokens" || !strings.Contains(string(errorBroadcast["error"].(string)), "max_tokens") {
		t.Fatalf("expected narrator_error with invalid_state code/message, got %#v", errorBroadcast)
	}
}

func TestIsContextOverflowAgentEvent(t *testing.T) {
	if !isContextOverflowAgentEvent(agentbasic.AgentEvent{Type: "invalid_state", Reason: "context_length_exceeded"}) {
		t.Fatalf("expected context_length_exceeded to be recognized")
	}
	if !isContextOverflowAgentEvent(agentbasic.AgentEvent{Type: "error", Message: "input is too long for this model context window"}) {
		t.Fatalf("expected context window message to be recognized")
	}
	if isContextOverflowAgentEvent(agentbasic.AgentEvent{Type: "invalid_state", Reason: "max_tokens"}) {
		t.Fatalf("max_tokens is output truncation, not context overflow")
	}

	// Patterns aligned with TS CONTEXT_OVERFLOW_PATTERNS + isContextOverflowReason.
	overflowCases := []struct {
		name  string
		event agentbasic.AgentEvent
	}{
		// Anthropic's real 400 error text surfaced via the loop's "error" event.
		{"anthropic prompt is too long", agentbasic.AgentEvent{Type: "error", Message: "anthropic provider returned status 400: {\"error\":{\"message\":\"prompt is too long: 250000 tokens > 200000 maximum\"}}"}},
		{"exceeds the context window", agentbasic.AgentEvent{Type: "error", Message: "this request exceeds the context window for the model"}},
		{"maximum context length", agentbasic.AgentEvent{Type: "error", Message: "This model's maximum context length is 128000 tokens"}},
		{"reason context_window", agentbasic.AgentEvent{Type: "invalid_state", Reason: "context_window_exceeded"}},
		{"reason input_too_long", agentbasic.AgentEvent{Type: "invalid_state", Reason: "input_too_long"}},
		{"reason prompt_too_long", agentbasic.AgentEvent{Type: "invalid_state", Reason: "prompt_too_long"}},
		{"reason context_overflow", agentbasic.AgentEvent{Type: "invalid_state", Reason: "context_overflow"}},
		{"code in code field", agentbasic.AgentEvent{Type: "retryable_error", Code: "context_length_exceeded"}},
	}
	for _, tc := range overflowCases {
		if !isContextOverflowAgentEvent(tc.event) {
			t.Fatalf("expected %q to be recognized as context overflow", tc.name)
		}
	}

	// Non-overflow events must not be misclassified.
	nonOverflowCases := []struct {
		name  string
		event agentbasic.AgentEvent
	}{
		{"completion length limit", agentbasic.AgentEvent{Type: "invalid_state", Reason: "length"}},
		{"max_output_tokens", agentbasic.AgentEvent{Type: "invalid_state", Reason: "max_output_tokens"}},
		{"generic api error", agentbasic.AgentEvent{Type: "error", Message: "internal server error"}},
		{"wrong event type", agentbasic.AgentEvent{Type: "stream_text", Message: "prompt is too long"}},
	}
	for _, tc := range nonOverflowCases {
		if isContextOverflowAgentEvent(tc.event) {
			t.Fatalf("expected %q to NOT be recognized as context overflow", tc.name)
		}
	}
}
