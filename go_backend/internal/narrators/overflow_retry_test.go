package narrators

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"narrafork-go/internal/agentbasic"
)

// TestContextOverflowRetryCapStopsAfterMaxAttempts verifies the TS
// MAX_CONTEXT_OVERFLOW_RETRIES cap (narrator-recovery.ts:26,131-135): the first
// maxContextOverflowRetries (2) overflow events compact + schedule a retry, but
// the next one (attempt 3) gives up and finalizes the run as an error
// (errorCode=context_too_long_recovery_exhausted) instead of compacting/retrying
// forever.
func TestContextOverflowRetryCapStopsAfterMaxAttempts(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	resetContextOverflowRetries(narratorID)
	defer resetContextOverflowRetries(narratorID)
	hub, events := captureNarratorBroadcasts()
	service := &Service{DB: sqlDB, Hub: hub}
	ctx := context.Background()

	seedOverflowMessages := func() {
		for i, content := range []string{"one", "two", "three", "four", "five", "six", "seven"} {
			role := "user"
			if i%2 == 1 {
				role = "assistant"
			}
			if _, err := service.addMessage(ctx, narratorID, role, content, nil); err != nil {
				t.Fatalf("add message %d: %v", i, err)
			}
		}
	}

	retryCount := 0
	service.contextOverflowRetry = func(_ context.Context, id string) {
		if id == narratorID {
			retryCount++
		}
	}

	event := agentbasic.AgentEvent{Type: "invalid_state", Reason: "model_context_window_exceeded", Message: "context window limit"}

	// Attempts 1 and 2: compact + schedule retry.
	for attempt := 1; attempt <= maxContextOverflowRetries; attempt++ {
		seedOverflowMessages()
		state := &agentRunState{narratorID: narratorID, narrator: mustGetNarrator(t, service, narratorID)}
		if err := service.finishAgentRunContextOverflow(ctx, state, event); err != nil {
			t.Fatalf("overflow attempt %d: %v", attempt, err)
		}
		if state.errored {
			t.Fatalf("attempt %d should not error (within cap), got errored=%v", attempt, state.errored)
		}
		// Reset the working status back so the narrator can overflow again.
		if _, err := sqlDB.Exec(`UPDATE narrators SET status = 'working', substatus = '["calling_model"]' WHERE id = ?`, narratorID); err != nil {
			t.Fatalf("reset status: %v", err)
		}
	}
	if retryCount != maxContextOverflowRetries {
		t.Fatalf("expected %d scheduled retries within cap, got %d", maxContextOverflowRetries, retryCount)
	}

	// Attempt 3: exceeds the cap → finalize as error, no further retry.
	seedOverflowMessages()
	state := &agentRunState{narratorID: narratorID, narrator: mustGetNarrator(t, service, narratorID)}
	if err := service.finishAgentRunContextOverflow(ctx, state, event); err != nil {
		t.Fatalf("overflow over-cap attempt: %v", err)
	}
	if !state.errored {
		t.Fatalf("over-cap attempt should finalize errored, got errored=%v", state.errored)
	}
	if state.errorCode != "context_too_long_recovery_exhausted" {
		t.Fatalf("expected errorCode context_too_long_recovery_exhausted, got %q", state.errorCode)
	}
	if retryCount != maxContextOverflowRetries {
		t.Fatalf("over-cap attempt must not schedule another retry, retryCount=%d", retryCount)
	}

	var status, substatus string
	var errorMessage sql.NullString
	if err := sqlDB.QueryRow(`SELECT status, substatus, error_message FROM narrators WHERE id = ?`, narratorID).Scan(&status, &substatus, &errorMessage); err != nil {
		t.Fatalf("load narrator status: %v", err)
	}
	if status != "idle" || substatus != `["error"]` {
		t.Fatalf("over-cap finalize status=%s substatus=%s want idle/[error]", status, substatus)
	}
	if !errorMessage.Valid || !strings.Contains(errorMessage.String, "still too long") {
		t.Fatalf("over-cap error_message = %#v, want recovery-exhausted message", errorMessage)
	}
	if !hasBroadcast(events(), narratorID, "narrator_error") {
		t.Fatalf("over-cap finalize should broadcast narrator_error, got %#v", events())
	}
}

// TestContextOverflowRetriesResetOnSuccess verifies a clean completion resets the
// per-narrator overflow counter so a later independent overflow starts from zero
// again (mirrors the TS loop resetting contextOverflowRetries on success).
func TestContextOverflowRetriesResetOnSuccess(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	resetContextOverflowRetries(narratorID)
	defer resetContextOverflowRetries(narratorID)
	service := &Service{DB: sqlDB}
	ctx := context.Background()

	if got := incrementContextOverflowRetries(narratorID); got != 1 {
		t.Fatalf("first increment = %d, want 1", got)
	}
	if got := incrementContextOverflowRetries(narratorID); got != 2 {
		t.Fatalf("second increment = %d, want 2", got)
	}

	state := &agentRunState{narratorID: narratorID, narrator: mustGetNarrator(t, service, narratorID)}
	if err := service.finishAgentRunSuccess(ctx, state); err != nil {
		t.Fatalf("finishAgentRunSuccess: %v", err)
	}

	if got := incrementContextOverflowRetries(narratorID); got != 1 {
		t.Fatalf("after success reset the counter should restart at 1, got %d", got)
	}
}

func mustGetNarrator(t *testing.T, service *Service, narratorID string) Narrator {
	t.Helper()
	narrator, err := service.Get(context.Background(), narratorID)
	if err != nil {
		t.Fatalf("load narrator: %v", err)
	}
	return narrator
}
