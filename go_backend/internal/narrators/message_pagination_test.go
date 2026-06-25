package narrators

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func TestMessagesAfterCatchUpTracksChildAnchors(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	childNarratorID := insertChildNarratorTestRow(t, sqlDB, "child_nar")
	service := &Service{DB: sqlDB}
	ctx := context.Background()

	if _, err := service.addMessage(ctx, narratorID, "user", "start", nil); err != nil {
		t.Fatalf("add start: %v", err)
	}
	assistant, err := service.addMessageWithBlocks(ctx, narratorID, "assistant", "launch child", []map[string]any{{"type": "tool_use", "id": "tool_1", "name": "Task", "input": map[string]any{"prompt": "go"}}, {"type": "text", "text": "launch child"}}, nil)
	if err != nil {
		t.Fatalf("add assistant: %v", err)
	}
	insertToolCallTestRow(t, sqlDB, narratorID, assistant.ID, "tool_1")
	childOne := insertChildMessageTestRow(t, sqlDB, childNarratorID, "tool_1", 1, "child one")

	first, err := service.MessagesAfter(ctx, narratorID, CatchUpCursor{ParentLastMessageID: &assistant.ID}, 40)
	if err != nil {
		t.Fatalf("first catch-up: %v", err)
	}
	if len(first.TopLevel) != 0 || len(first.OrphanChildren) != 1 || first.OrphanChildren[0].ID != childOne {
		t.Fatalf("expected one orphan child catch-up, got %#v", first)
	}
	if first.Cursor == nil || len(first.Cursor.ChildAnchors) != 1 || first.Cursor.ChildAnchors[0].LastMessageID == nil || *first.Cursor.ChildAnchors[0].LastMessageID != childOne {
		t.Fatalf("expected child cursor at child one, got %#v", first.Cursor)
	}

	childTwo := insertChildMessageTestRow(t, sqlDB, childNarratorID, "tool_1", 2, "child two")
	topAfter, err := service.addMessage(ctx, narratorID, "user", "after child", nil)
	if err != nil {
		t.Fatalf("add top after: %v", err)
	}
	second, err := service.MessagesAfter(ctx, narratorID, *first.Cursor, 40)
	if err != nil {
		t.Fatalf("second catch-up: %v", err)
	}
	if len(second.TopLevel) != 1 || second.TopLevel[0].ID != topAfter.ID {
		t.Fatalf("expected new top-level message, got %#v", second.TopLevel)
	}
	if len(second.OrphanChildren) != 1 || second.OrphanChildren[0].ID != childTwo {
		t.Fatalf("expected second orphan child, got %#v", second.OrphanChildren)
	}
}

func TestMessagesRouteRejectsRemovedCursorEndpoint(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)

	// The legacy GET /:id/messages cursor/around/catch-up endpoint was removed in
	// favor of the chunk virtualization endpoints; catch-up now flows over the
	// WebSocket only. The route must no longer be served.
	w := serveNarratorHandlerRequest(sqlDB, http.MethodGet, "/narrators/"+narratorID+"/messages")
	if w.Code != http.StatusNotFound && w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected legacy GET /messages to be gone (404/405), got %d body=%s", w.Code, w.Body.String())
	}
}

func insertChildNarratorTestRow(t *testing.T, sqlDB *sql.DB, narratorID string) string {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := sqlDB.Exec(`INSERT INTO narrators (
		id, type, title, inherit_mode, model, permission_mode, status, substatus, plan_mode,
		cwd, variant, traits, is_background, is_ask_in_passing, message_version, message_count,
		total_cost_usd, fast_mode, relaxed_plan, plan_reflection_auto_approve_override,
		danger_reflection_override, created_at, updated_at
	) VALUES (?, 'subagent', 'Child narrator', 'fresh', 'openai:test', 'default', 'idle', '[]', 0, '', 'subagent:general', '[]', 0, 0, 0, 0, 0, 0, 0, 'inherit', 'inherit', ?, ?)`, narratorID, now, now)
	if err != nil {
		t.Fatalf("insert child narrator: %v", err)
	}
	return narratorID
}

func insertToolCallTestRow(t *testing.T, sqlDB *sql.DB, narratorID, messageID, toolUseID string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := sqlDB.Exec(`INSERT INTO narrator_tool_calls (id, narrator_id, message_id, tool_use_id, tool_name, input_json, output_json, status, created_at) VALUES (?, ?, ?, ?, 'Task', '{}', '{}', 'success', ?)`, "tc_"+toolUseID, narratorID, messageID, toolUseID, now)
	if err != nil {
		t.Fatalf("insert tool call: %v", err)
	}
}

func insertChildMessageTestRow(t *testing.T, sqlDB *sql.DB, narratorID, parentToolUseID string, seq int, text string) string {
	t.Helper()
	messageID := narratorID + "_msg_" + string(rune('0'+seq))
	now := time.Now().UTC().Add(time.Duration(seq) * time.Millisecond).Format(time.RFC3339Nano)
	contentJSON, _ := json.Marshal([]map[string]any{{"type": "text", "text": text}})
	_, err := sqlDB.Exec(`INSERT INTO narrator_messages (id, narrator_id, parent_tool_use_id, role, content_json, content_text, created_at) VALUES (?, ?, ?, 'assistant', ?, ?, ?)`, messageID, narratorID, parentToolUseID, string(contentJSON), text, now)
	if err != nil {
		t.Fatalf("insert child message: %v", err)
	}
	_, err = sqlDB.Exec(`INSERT INTO narrator_message_refs (id, narrator_id, message_id, seq, is_compact) VALUES (?, ?, ?, ?, 0)`, "ref_"+messageID, narratorID, messageID, seq)
	if err != nil {
		t.Fatalf("insert child ref: %v", err)
	}
	return messageID
}

func messageSeqs(messages []Message) []int {
	seqs := make([]int, 0, len(messages))
	for _, msg := range messages {
		seqs = append(seqs, msg.Seq)
	}
	return seqs
}
