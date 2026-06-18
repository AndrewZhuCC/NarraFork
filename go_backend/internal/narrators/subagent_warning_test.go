package narrators

import (
	"context"
	"testing"
	"time"

	"narrafork-go/internal/agentbasic"
	"narrafork-go/internal/id"
)

// insertSubagentNarratorForTest inserts a subagent narrator row whose parent is
// parentID and returns the new subagent narrator id.
func insertSubagentNarratorForTest(t *testing.T, service *Service, parentID, model, cwd string) string {
	t.Helper()
	subagentID := id.New()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := service.DB.Exec(`INSERT INTO narrators (
		id, type, subagent_type, title, inherit_mode, parent_narrator_id, model, permission_mode,
		status, substatus, plan_mode, cwd, variant, traits, is_background, is_ask_in_passing,
		message_version, message_count, total_cost_usd, fast_mode, relaxed_plan,
		plan_reflection_auto_approve_override, danger_reflection_override, created_at, updated_at
	) VALUES (?, 'subagent', 'explore', 'Sub', 'fresh', ?, ?, 'default', 'in_progress', '[]', 0, ?, 'subagent', '[]', 0, 0, 0, 0, 0, 0, 0, 'inherit', 'inherit', ?, ?)`,
		subagentID, parentID, model, cwd, now, now)
	if err != nil {
		t.Fatalf("insert subagent narrator: %v", err)
	}
	return subagentID
}

// TestRetryingEventForwardsSubagentWarningToParent verifies that a retryable
// warning (`retrying` agent event) emitted by a subagent is forwarded to the
// parent narrator as a `subagent_warning` frame with the retry metadata, matching
// the TS narrator-recovery dual broadcast.
func TestRetryingEventForwardsSubagentWarningToParent(t *testing.T) {
	service, parentID, cwd := newTestNarratorService(t, "openai:gpt-test", "http://127.0.0.1")
	hub, events := captureNarratorBroadcasts()
	service.Hub = hub

	subagentID := insertSubagentNarratorForTest(t, service, parentID, "openai:gpt-test", cwd)
	narrator, err := service.Get(context.Background(), subagentID)
	if err != nil {
		t.Fatalf("load subagent narrator: %v", err)
	}
	state := &agentRunState{narratorID: subagentID, narrator: narrator}

	event := agentbasic.AgentEvent{Type: "retrying", Message: "provider returned status 503", Attempt: 2, MaxRetries: 5, DelayMs: 1500}
	if err := service.handleAgentEvent(context.Background(), state, event); err != nil {
		t.Fatalf("handle retrying event: %v", err)
	}

	frame := findBroadcast(events(), parentID, "subagent_warning")
	if frame == nil {
		t.Fatalf("expected subagent_warning broadcast to parent %s, got %#v", parentID, events())
	}
	if frame["subagentNarratorId"] != subagentID {
		t.Fatalf("expected subagentNarratorId=%s, got %#v", subagentID, frame["subagentNarratorId"])
	}
	if frame["narratorId"] != parentID {
		t.Fatalf("expected narratorId=%s (parent), got %#v", parentID, frame["narratorId"])
	}
	if frame["message"] != "provider returned status 503" {
		t.Fatalf("unexpected message: %#v", frame["message"])
	}
	// JSON round-trip turns numbers into float64.
	if got, ok := frame["retryCount"].(float64); !ok || got != 2 {
		t.Fatalf("expected retryCount=2, got %#v", frame["retryCount"])
	}
	if got, ok := frame["maxRetries"].(float64); !ok || got != 5 {
		t.Fatalf("expected maxRetries=5, got %#v", frame["maxRetries"])
	}
	if got, ok := frame["delayMs"].(float64); !ok || got != 1500 {
		t.Fatalf("expected delayMs=1500, got %#v", frame["delayMs"])
	}
}

// TestRetryingEventNoSubagentWarningForPrimary verifies a primary narrator's
// retryable warning does NOT produce a subagent_warning frame (no parent to
// notify).
func TestRetryingEventNoSubagentWarningForPrimary(t *testing.T) {
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", "http://127.0.0.1")
	hub, events := captureNarratorBroadcasts()
	service.Hub = hub

	narrator, err := service.Get(context.Background(), narratorID)
	if err != nil {
		t.Fatalf("load narrator: %v", err)
	}
	state := &agentRunState{narratorID: narratorID, narrator: narrator}

	event := agentbasic.AgentEvent{Type: "retrying", Message: "provider returned status 503", Attempt: 1, MaxRetries: 3, DelayMs: 1000}
	if err := service.handleAgentEvent(context.Background(), state, event); err != nil {
		t.Fatalf("handle retrying event: %v", err)
	}

	for _, ev := range events() {
		if ev.Payload["type"] == "subagent_warning" {
			t.Fatalf("primary narrator must not emit subagent_warning, got %#v", ev.Payload)
		}
	}
}
