package narrators

import (
	"context"
	"testing"

	"narrafork-go/internal/agentbasic"
)

// TestStatefulRetryableErrorFinalizesWithoutSessionRerun is a behavior-lock guard
// for the documented replacement-boundary divergence
// (narrator.agentFinalization.statefulOuterRetryWithRebuiltHistory=false): TS
// performs a session-level outer retry that rebuilds history from the DB for
// stateful providers (codex/responses), with codex quota-failover bypassRetryLimit
// making it unbounded. Go's loop emits the retryable_error with the
// codex_quota_failover_rebuild code (preserved through WS / narrator_error), but
// the session does NOT re-run — it finalizes as a narrator_error. This test pins
// that current behavior so any future change (e.g. actually implementing the
// outer retry) must update the capability contract.
func TestStatefulRetryableErrorFinalizesWithoutSessionRerun(t *testing.T) {
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", "http://127.0.0.1")
	hub, events := captureNarratorBroadcasts()
	service.Hub = hub
	ctx := context.Background()
	narrator, err := service.Get(ctx, narratorID)
	if err != nil {
		t.Fatalf("get narrator: %v", err)
	}

	assistantBefore := assistantMessageCount(t, service, narratorID)

	state := &agentRunState{narratorID: narratorID, narrator: narrator}
	event := agentbasic.AgentEvent{
		Type:     "retryable_error",
		Code:     agentbasic.CodexRebuildHistoryRetryCode,
		Message:  "codex quota failover requires history rebuild",
		Provider: "codex",
		Model:    "gpt-5-codex",
		Metadata: map[string]any{"previousCredentialId": "cred-old"},
	}
	if err := service.handleAgentEvent(ctx, state, event); err != nil {
		t.Fatalf("handle stateful retryable error: %v", err)
	}

	// Go finalizes as narrator_error (no session-level rebuilt-history rerun).
	finalError := findBroadcast(events(), narratorID, "narrator_error")
	if finalError == nil || finalError["errorCode"] != agentbasic.CodexRebuildHistoryRetryCode {
		t.Fatalf("expected narrator_error preserving the codex rebuild code, got %#v", finalError)
	}
	// No retrying event (no outer retry) and no re-run side effects.
	if hasBroadcast(events(), narratorID, "retrying") {
		t.Fatalf("session must not emit a retrying/outer-retry for the stateful rebuild error, got %#v", events())
	}
	var status, substatus string
	if err := service.DB.QueryRow(`SELECT status, substatus FROM narrators WHERE id = ?`, narratorID).Scan(&status, &substatus); err != nil {
		t.Fatalf("query status: %v", err)
	}
	if status != "idle" || substatus != `["error"]` {
		t.Fatalf("stateful retryable error should finalize idle/error (no rerun), got status=%s substatus=%s", status, substatus)
	}
	// No new assistant message was produced by a (non-existent) rerun.
	if after := assistantMessageCount(t, service, narratorID); after != assistantBefore {
		t.Fatalf("session must not produce new assistant content via an outer retry: before=%d after=%d", assistantBefore, after)
	}
}

func assistantMessageCount(t *testing.T, service *Service, narratorID string) int {
	t.Helper()
	var count int
	if err := service.DB.QueryRow(`SELECT COUNT(*) FROM narrator_messages WHERE narrator_id = ? AND role = 'assistant'`, narratorID).Scan(&count); err != nil {
		t.Fatalf("count assistant messages: %v", err)
	}
	return count
}
