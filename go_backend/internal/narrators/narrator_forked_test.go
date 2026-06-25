package narrators

import (
	"context"
	"testing"
)

// TestForkNarratorBroadcastsNarratorForkedToParent pins the W786 parity gap:
// after a successful fork, Go must broadcast narrator_forked to the PARENT
// narrator (mirroring TS narrator-service.forkNarrator narrator-service.ts:1444),
// carrying narratorId (the new child) and parentNarratorId. The frontend uses
// this to refresh the parent's fork list/graph.
func TestForkNarratorBroadcastsNarratorForkedToParent(t *testing.T) {
	service, parentID, _ := newTestNarratorService(t, "openai:gpt-test", "")
	hub, drain := captureNarratorBroadcasts()
	service.Hub = hub

	child, err := service.ForkNarrator(context.Background(), parentID, "", "Child fork", "full")
	if err != nil {
		t.Fatalf("ForkNarrator: %v", err)
	}

	payload := findBroadcast(drain(), parentID, "narrator_forked")
	if payload == nil {
		t.Fatal("expected narrator_forked broadcast to the parent narrator")
	}
	if payload["parentNarratorId"] != parentID {
		t.Fatalf("narrator_forked.parentNarratorId = %v, want %q", payload["parentNarratorId"], parentID)
	}
	if payload["narratorId"] != child.ID {
		t.Fatalf("narrator_forked.narratorId = %v, want %q (new child)", payload["narratorId"], child.ID)
	}
}

// TestForkFromMessagesDoesNotBroadcastNarratorForked is the negative control:
// TS forkFromMessages returns without broadcasting narrator_forked, so the Go
// ForkFromMessages path must stay silent. Only the central ForkNarrator path
// broadcasts.
func TestForkFromMessagesDoesNotBroadcastNarratorForked(t *testing.T) {
	service, parentID, _ := newTestNarratorService(t, "openai:gpt-test", "")
	hub, drain := captureNarratorBroadcasts()
	service.Hub = hub

	// Resolve the seeded parent message id to fork from.
	var msgID string
	if err := service.DB.QueryRowContext(context.Background(),
		`SELECT m.id FROM narrator_message_refs r JOIN narrator_messages m ON m.id = r.message_id WHERE r.narrator_id = ? ORDER BY r.seq ASC LIMIT 1`,
		parentID).Scan(&msgID); err != nil {
		t.Fatalf("load parent message id: %v", err)
	}

	if _, err := service.ForkFromMessages(context.Background(), parentID, []string{msgID}, "Subset fork"); err != nil {
		t.Fatalf("ForkFromMessages: %v", err)
	}

	if payload := findBroadcast(drain(), parentID, "narrator_forked"); payload != nil {
		t.Fatalf("ForkFromMessages must not broadcast narrator_forked, got %#v", payload)
	}
}
