package narrators

import (
	"context"
	"testing"

	"narrafork-go/internal/agentbasic"
)

// TestContextUsageBroadcastIncludesThresholds verifies context_usage frames carry
// pruneStart/compactStart, mirroring the TS dualBroadcast context_usage payload
// (narrator-event-handler.ts:1485-1487). Defaults are 95/99.
func TestContextUsageBroadcastIncludesThresholds(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	hub, events := captureNarratorBroadcasts()
	service := &Service{DB: sqlDB, Hub: hub}

	service.broadcastAgentEvent(narratorID, agentbasic.AgentEvent{
		Type:       "context_usage",
		Percentage: 42,
		Usage:      &agentbasic.UsageSnapshot{PromptTokens: 1000, ContextWindow: 128000},
	})

	frame := findBroadcast(events(), narratorID, "context_usage")
	if frame == nil {
		t.Fatalf("missing context_usage broadcast: %#v", events())
	}
	if intNumber(frame["pruneStart"]) != 95 {
		t.Fatalf("pruneStart = %v, want 95", frame["pruneStart"])
	}
	if intNumber(frame["compactStart"]) != 99 {
		t.Fatalf("compactStart = %v, want 99", frame["compactStart"])
	}
}

// TestSubagentLiveStreamMirrorsToParent verifies that a subagent's live streaming
// frame is mirrored to its parent narrator with the subagent linking fields
// (subagentNarratorId + parentToolUseId), mirroring the TS dualBroadcast contract
// (narrator-event-handler.ts:276-304), while the subagent's own frame stays
// scoped to its own id.
func TestSubagentLiveStreamMirrorsToParent(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	parentID := insertNarratorHandlerTestRow(t, sqlDB)
	hub, events := captureNarratorBroadcasts()
	service := &Service{DB: sqlDB, Hub: hub}
	ctx := context.Background()

	parent, err := service.Get(ctx, parentID)
	if err != nil {
		t.Fatalf("load parent: %v", err)
	}
	child, err := service.createSubagentNarrator(ctx, parent, "general", "mirror test", "openai:test", "", "", "fg", false)
	if err != nil {
		t.Fatalf("create subagent: %v", err)
	}
	if _, err := service.addMessageWithBlocksWithCommandAndParentToolUse(ctx, child.ID, "user", "go", []map[string]any{{"type": "text", "text": "go"}}, nil, "", "toolu_mirror"); err != nil {
		t.Fatalf("seed child parent tool use: %v", err)
	}

	state := &agentRunState{narratorID: child.ID, narrator: child}
	if err := service.handleAgentEvent(ctx, state, agentbasic.AgentEvent{Type: "stream_text", Text: "hello"}); err != nil {
		t.Fatalf("handle subagent stream: %v", err)
	}

	// Child's own frame: scoped to child, no subagent linking.
	childFrame := requireJ08Broadcast(t, events(), child.ID, func(payload map[string]any) bool {
		return payload["type"] == "stream_text" && payload["text"] == "hello"
	})
	if childFrame["subagentNarratorId"] != nil {
		t.Fatalf("child self frame must not carry subagentNarratorId, got %#v", childFrame)
	}

	// Parent mirror: carries subagent linking fields.
	parentFrame := requireJ08Broadcast(t, events(), parentID, func(payload map[string]any) bool {
		return payload["type"] == "stream_text" && payload["subagentNarratorId"] == child.ID
	})
	if parentFrame["narratorId"] != parentID {
		t.Fatalf("parent mirror narratorId = %v, want %s", parentFrame["narratorId"], parentID)
	}
	if parentFrame["parentToolUseId"] != "toolu_mirror" {
		t.Fatalf("parent mirror parentToolUseId = %v, want toolu_mirror", parentFrame["parentToolUseId"])
	}
	if parentFrame["text"] != "hello" {
		t.Fatalf("parent mirror text = %v, want hello", parentFrame["text"])
	}
}

// TestSubagentTerminalFrameNotMirroredToParent verifies that non-live frames (e.g.
// a raw done/status passthrough) are NOT mirrored to the parent via the live
// path, since the parent receives terminal/lifecycle updates through dedicated
// broadcasts.
func TestSubagentTerminalFrameNotMirroredToParent(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	parentID := insertNarratorHandlerTestRow(t, sqlDB)
	hub, events := captureNarratorBroadcasts()
	service := &Service{DB: sqlDB, Hub: hub}
	ctx := context.Background()

	parent, err := service.Get(ctx, parentID)
	if err != nil {
		t.Fatalf("load parent: %v", err)
	}
	child, err := service.createSubagentNarrator(ctx, parent, "general", "no mirror", "openai:test", "", "", "fg", false)
	if err != nil {
		t.Fatalf("create subagent: %v", err)
	}
	if _, err := service.addMessageWithBlocksWithCommandAndParentToolUse(ctx, child.ID, "user", "go", []map[string]any{{"type": "text", "text": "go"}}, nil, "", "toolu_nomirror"); err != nil {
		t.Fatalf("seed child parent tool use: %v", err)
	}

	state := &agentRunState{narratorID: child.ID, narrator: child}
	// queue_status is not in the live-mirror allowlist.
	service.broadcastAgentEventForRun(ctx, state, child, agentbasic.AgentEvent{Type: "queue_status", Message: "waiting"})

	for _, ev := range events() {
		if ev.NarratorID == parentID && ev.Payload["type"] == "queue_status" {
			t.Fatalf("queue_status should not be mirrored to parent, got %#v", ev.Payload)
		}
	}
}
