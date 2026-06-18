package narrators

import (
	"context"
	"testing"
	"time"
)

// insertTestBufferedMessage queues a buffered message row for narratorID.
func insertTestBufferedMessage(t *testing.T, service *Service, narratorID, bufID, text string, seq int) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := service.DB.Exec(`INSERT INTO narrator_buffered_messages (id, narrator_id, text, priority, seq, buffered_at) VALUES (?, ?, ?, 0, ?, ?)`, bufID, narratorID, text, seq, now); err != nil {
		t.Fatalf("insert buffered message: %v", err)
	}
}

func TestFinishAgentRunErrorBroadcastsBufferPreservedWhenQueueNonEmpty(t *testing.T) {
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", "")
	hub, drain := captureNarratorBroadcasts()
	service.Hub = hub
	narrator, err := service.Get(context.Background(), narratorID)
	if err != nil {
		t.Fatalf("load narrator: %v", err)
	}
	insertTestBufferedMessage(t, service, narratorID, "buf-1", "first queued", 0)
	insertTestBufferedMessage(t, service, narratorID, "buf-2", "second queued", 1)

	state := &agentRunState{narratorID: narratorID, narrator: narrator}
	if err := service.finishAgentRunError(context.Background(), state, "provider exploded"); err != nil {
		t.Fatalf("finishAgentRunError: %v", err)
	}

	var preserved *capturedBroadcast
	sawError := false
	events := drain()
	for i := range events {
		switch events[i].Payload["type"] {
		case "buffer_preserved":
			preserved = &events[i]
		case "narrator_error":
			sawError = true
		}
	}
	if !sawError {
		t.Fatal("expected narrator_error broadcast on error finalize")
	}
	if preserved == nil {
		t.Fatal("expected buffer_preserved broadcast when queue is non-empty")
	}
	if preserved.NarratorID != narratorID {
		t.Fatalf("buffer_preserved target = %q, want %q", preserved.NarratorID, narratorID)
	}
	msgs, ok := preserved.Payload["messages"].([]any)
	if !ok {
		t.Fatalf("buffer_preserved.messages type = %T, want []any", preserved.Payload["messages"])
	}
	if len(msgs) != 2 {
		t.Fatalf("buffer_preserved.messages len = %d, want 2", len(msgs))
	}
	first, ok := msgs[0].(map[string]any)
	if !ok {
		t.Fatalf("first message type = %T, want map[string]any", msgs[0])
	}
	if first["id"] != "buf-1" || first["text"] != "first queued" {
		t.Fatalf("first preserved message = %#v, want buf-1/first queued", first)
	}
}

func TestFinishAgentRunErrorNoBufferPreservedWhenQueueEmpty(t *testing.T) {
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", "")
	hub, drain := captureNarratorBroadcasts()
	service.Hub = hub
	narrator, err := service.Get(context.Background(), narratorID)
	if err != nil {
		t.Fatalf("load narrator: %v", err)
	}

	state := &agentRunState{narratorID: narratorID, narrator: narrator}
	if err := service.finishAgentRunError(context.Background(), state, "provider exploded"); err != nil {
		t.Fatalf("finishAgentRunError: %v", err)
	}

	for _, ev := range drain() {
		if ev.Payload["type"] == "buffer_preserved" {
			t.Fatal("buffer_preserved must not be broadcast when no queued messages remain")
		}
	}
}
