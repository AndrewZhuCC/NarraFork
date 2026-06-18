package narrators

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// seqOf returns the message_refs.seq for a given message id in a narrator.
func seqOf(t *testing.T, service *Service, narratorID, messageID string) int {
	t.Helper()
	var seq int
	if err := service.DB.QueryRow(`SELECT seq FROM narrator_message_refs WHERE narrator_id = ? AND message_id = ?`, narratorID, messageID).Scan(&seq); err != nil {
		t.Fatalf("seqOf(%s): %v", messageID, err)
	}
	return seq
}

// TestRecallReadConversationAroundWindowSymmetric verifies the message_id path
// uses a closed SYMMETRIC window matching TS handleReadConversation:
// half = floor(cap/2), seq in [ref.seq - half, ref.seq + (cap - half)] with NO
// LIMIT, so messages both BEFORE and AFTER the anchor are returned (the prior
// Go impl used `seq >= start ORDER BY seq ASC LIMIT cap`, which omitted later
// messages once the anchor sat deep in a long conversation).
func TestRecallReadConversationAroundWindowSymmetric(t *testing.T) {
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", "http://127.0.0.1")
	ctx := context.Background()
	ids := make([]string, 0, 40)
	for i := 0; i < 40; i++ {
		m, err := service.addMessage(ctx, narratorID, "assistant", fmt.Sprintf("msg-%02d", i), nil)
		if err != nil {
			t.Fatalf("add message %d: %v", i, err)
		}
		ids = append(ids, m.ID)
	}
	anchor := ids[20]
	anchorSeq := seqOf(t, service, narratorID, anchor)

	out, status, err := service.executeRecallTool(ctx, narratorID, map[string]any{
		"action":     "read_conversation",
		"message_id": anchor,
		"limit":      10,
	})
	if err != nil || status != "" {
		t.Fatalf("read_conversation err=%v status=%q", err, status)
	}
	// cap=10 -> half=5 -> window [anchorSeq-5, anchorSeq+5] = 11 messages.
	if !strings.Contains(out, "Showing 11 message(s):") {
		t.Fatalf("expected 11 messages in symmetric window, got: %q", firstLine(out))
	}
	// Must include messages AFTER the anchor (the old impl would too, but the
	// upper bound must extend to anchorSeq+5). Verify both edges present.
	for _, want := range []int{anchorSeq - 5, anchorSeq, anchorSeq + 5} {
		if !strings.Contains(out, fmt.Sprintf("[seq=%d]", want)) {
			t.Fatalf("expected seq=%d in window, out=%q", want, out)
		}
	}
	// Must NOT include just outside the window.
	for _, notWant := range []int{anchorSeq - 6, anchorSeq + 6} {
		if strings.Contains(out, fmt.Sprintf("[seq=%d]", notWant)) {
			t.Fatalf("seq=%d should be outside window, out=%q", notWant, out)
		}
	}
}

// TestRecallReadConversationPreviewUTF16 verifies message text preview uses a
// plain UTF-16 slice(0,500) (no ellipsis), matching TS, so CJK content is cut at
// the same code-unit boundary rather than at Go byte offsets.
func TestRecallReadConversationPreviewUTF16(t *testing.T) {
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", "http://127.0.0.1")
	ctx := context.Background()
	// 600 CJK runes (each 1 UTF-16 unit, 3 bytes). Preview must keep 500 runes.
	big := strings.Repeat("好", 600)
	m, err := service.addMessage(ctx, narratorID, "assistant", big, nil)
	if err != nil {
		t.Fatalf("add message: %v", err)
	}
	out, status, err := service.executeRecallTool(ctx, narratorID, map[string]any{
		"action":     "read_conversation",
		"message_id": m.ID,
		"limit":      1,
	})
	if err != nil || status != "" {
		t.Fatalf("read_conversation err=%v status=%q", err, status)
	}
	want := strings.Repeat("好", 500)
	if !strings.Contains(out, want) {
		t.Fatalf("preview did not keep 500 CJK runes")
	}
	if strings.Contains(out, strings.Repeat("好", 501)) {
		t.Fatalf("preview kept more than 500 runes")
	}
	// No ellipsis appended by the preview path.
	if strings.Contains(out, "好…") || strings.Contains(out, "好...") {
		t.Fatalf("preview should not append ellipsis")
	}
}
