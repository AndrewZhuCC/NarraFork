package narrators

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func TestMessagesCursorPaginatesTopLevelRefsAndAttachesChildren(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	service := &Service{DB: sqlDB}
	ctx := context.Background()

	for i := 1; i <= 6; i++ {
		if _, err := service.addMessage(ctx, narratorID, "user", "top "+string(rune('0'+i)), nil); err != nil {
			t.Fatalf("add top message %d: %v", i, err)
		}
	}

	first, err := service.MessagesCursor(ctx, narratorID, 2, "", "older")
	if err != nil {
		t.Fatalf("latest page: %v", err)
	}
	if len(first.Messages) != 2 || first.Messages[0].Seq != 5 || first.Messages[1].Seq != 6 {
		t.Fatalf("expected latest seqs [5 6], got %#v", messageSeqs(first.Messages))
	}
	if !first.HasMore || first.NextCursor == nil || *first.NextCursor != "5" {
		t.Fatalf("expected older cursor at seq 5, got %#v", first)
	}

	older, err := service.MessagesCursor(ctx, narratorID, 2, *first.NextCursor, "older")
	if err != nil {
		t.Fatalf("older page: %v", err)
	}
	if len(older.Messages) != 2 || older.Messages[0].Seq != 3 || older.Messages[1].Seq != 4 {
		t.Fatalf("expected older seqs [3 4], got %#v", messageSeqs(older.Messages))
	}

	newer, err := service.MessagesCursor(ctx, narratorID, 2, "4", "newer")
	if err != nil {
		t.Fatalf("newer page: %v", err)
	}
	if len(newer.Messages) != 2 || newer.Messages[0].Seq != 5 || newer.Messages[1].Seq != 6 {
		t.Fatalf("expected newer seqs [5 6], got %#v", messageSeqs(newer.Messages))
	}
}

// TestMessagesCursorLimitZeroReturnsEmpty locks the TS /:id/messages limit
// semantics Math.min(rawLimit, 200) with NO lower clamp: limit=0 fetches LIMIT 1
// and slice(0,0) -> empty pageRows, and TS then early-returns hasMore=false /
// nextCursor=null for an empty page. Go previously forced limit<=0 up to the 50
// fallback and returned rows.
func TestMessagesCursorLimitZeroReturnsEmpty(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	service := &Service{DB: sqlDB}
	ctx := context.Background()
	for i := 1; i <= 3; i++ {
		if _, err := service.addMessage(ctx, narratorID, "user", "m"+string(rune('0'+i)), nil); err != nil {
			t.Fatalf("add message %d: %v", i, err)
		}
	}
	page, err := service.MessagesCursor(ctx, narratorID, 0, "", "older")
	if err != nil {
		t.Fatalf("limit=0 cursor page: %v", err)
	}
	if len(page.Messages) != 0 {
		t.Fatalf("limit=0 should return empty messages (TS slice(0,0)), got %d", len(page.Messages))
	}
	// TS early-returns hasMore:false / nextCursor:null when pageRows is empty.
	if page.HasMore || page.NextCursor != nil {
		t.Fatalf("limit=0 empty page should report HasMore=false / NextCursor=nil (TS empty-page early return), got %#v", page)
	}
}

func TestMessagesCursorAttachesChildTreeForTopLevelRefs(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	childNarratorID := insertChildNarratorTestRow(t, sqlDB, "tree_child")
	service := &Service{DB: sqlDB}
	ctx := context.Background()

	assistant, err := service.addMessageWithBlocks(ctx, narratorID, "assistant", "launch child", []map[string]any{{"type": "tool_use", "id": "tree_tool", "name": "Task", "input": map[string]any{"prompt": "go"}}}, nil)
	if err != nil {
		t.Fatalf("add assistant: %v", err)
	}
	insertToolCallTestRow(t, sqlDB, narratorID, assistant.ID, "tree_tool")
	childID := insertChildMessageTestRow(t, sqlDB, childNarratorID, "tree_tool", 1, "tree child")

	page, err := service.MessagesCursor(ctx, narratorID, 10, "", "older")
	if err != nil {
		t.Fatalf("message page: %v", err)
	}
	if len(page.Messages) != 1 || len(page.Messages[0].Children) != 1 || page.Messages[0].Children[0].ID != childID {
		t.Fatalf("expected child tree attached to top-level ref, got %#v", page.Messages)
	}
	if len(page.Messages[0].ToolCalls) != 1 || page.Messages[0].ToolCalls[0].ToolUseID != "tree_tool" || page.Messages[0].ToolCalls[0].ToolName != "Task" {
		t.Fatalf("expected tool call attached to top-level assistant message, got %#v", page.Messages[0].ToolCalls)
	}
}

func TestMessagesAroundReturnsBoundedWindow(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	service := &Service{DB: sqlDB}
	ctx := context.Background()

	var anchor Message
	for i := 1; i <= 6; i++ {
		msg, err := service.addMessage(ctx, narratorID, "user", "top "+string(rune('0'+i)), nil)
		if err != nil {
			t.Fatalf("add top message %d: %v", i, err)
		}
		if i == 4 {
			anchor = msg
		}
	}

	page, err := service.MessagesAround(ctx, narratorID, anchor.ID, 1, 1)
	if err != nil {
		t.Fatalf("around page: %v", err)
	}
	if len(page.Messages) != 3 || page.Messages[0].Seq != 3 || page.Messages[1].Seq != 4 || page.Messages[2].Seq != 5 {
		t.Fatalf("expected around seqs [3 4 5], got %#v", messageSeqs(page.Messages))
	}
	if !page.HasMore || !page.HasMoreAfter || page.NextCursor == nil || page.PrevCursor == nil {
		t.Fatalf("expected both window cursors, got %#v", page)
	}
}

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

func TestMessagesRouteSupportsAfterCatchUpResponseWithMetadata(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	childNarratorID := insertChildNarratorTestRow(t, sqlDB, "route_child")
	service := &Service{DB: sqlDB}
	ctx := context.Background()

	assistant, err := service.addMessageWithBlocks(ctx, narratorID, "assistant", "launch child", []map[string]any{{"type": "tool_use", "id": "route_tool", "name": "Task", "input": map[string]any{"prompt": "go"}}}, nil)
	if err != nil {
		t.Fatalf("add assistant: %v", err)
	}
	insertToolCallTestRow(t, sqlDB, narratorID, assistant.ID, "route_tool")
	childID := insertChildMessageTestRow(t, sqlDB, childNarratorID, "route_tool", 1, "route child")
	if _, err := sqlDB.Exec(`UPDATE narrator_messages SET sdk_message_uuid = ? WHERE id = ?`, "sdk-msg-route", childID); err != nil {
		t.Fatalf("set child sdk uuid: %v", err)
	}

	w := serveNarratorHandlerRequest(sqlDB, http.MethodGet, "/narrators/"+narratorID+"/messages?after="+assistant.ID)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode catch-up response: %v body=%s", err, w.Body.String())
	}
	if _, ok := body["messageVersion"]; !ok {
		t.Fatalf("expected messageVersion metadata, got %#v", body)
	}
	orphans, _ := body["orphanChildren"].([]any)
	if len(orphans) != 1 || orphans[0].(map[string]any)["id"] != childID {
		t.Fatalf("expected route orphan child %s, got %#v", childID, body["orphanChildren"])
	}
	child, _ := orphans[0].(map[string]any)
	if child["messageUuid"] != "sdk-msg-route" || child["sdkMessageUuid"] != "sdk-msg-route" {
		t.Fatalf("expected TS-compatible messageUuid alias, got %#v", child)
	}
	if _, ok := body["cursor"].(map[string]any); !ok {
		t.Fatalf("expected cursor metadata, got %#v", body["cursor"])
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
