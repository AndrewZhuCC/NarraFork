package narrators

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestAgentHistoryRebuildsStructuredContentJSON(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sqlDB.Close()
	if err := createNarratorTestSchema(sqlDB); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	service := &Service{DB: sqlDB}
	narratorID := "nar_h03"
	if _, err := sqlDB.Exec(`INSERT INTO narrators (id, type, inherit_mode, model, status, created_at, updated_at) VALUES (?, 'primary', 'full', 'openai:gpt-test', 'idle', ?, ?)`, narratorID, nowForHistoryTest(0), nowForHistoryTest(0)); err != nil {
		t.Fatalf("insert narrator: %v", err)
	}
	insertHistoryMessage(t, sqlDB, narratorID, 0, "compact", "system", []map[string]any{{"type": "compact", "status": "compacted", "summary": "Earlier summary"}}, "[Compact] Earlier summary", true)
	insertHistoryMessage(t, sqlDB, narratorID, 1, "assistant1", "assistant", []map[string]any{
		{"type": "reasoning", "text": "thinking text", "providerMetadata": map[string]any{"openai": map[string]any{"itemId": "rs_1", "reasoningEncryptedContent": "enc_1"}}},
		{"type": "text", "text": "I will inspect"},
		{"type": "tool_use", "id": "call_read", "name": "Read", "input": map[string]any{"file_path": "README.md"}},
	}, "legacy assistant text should not win", false)
	if _, err := sqlDB.Exec(`INSERT INTO narrator_tool_calls (id, narrator_id, message_id, tool_use_id, tool_name, input_json, output_json, status, created_at) VALUES ('tc1', ?, 'assistant1', 'call_read', 'Read', ?, ?, 'success', ?)`, narratorID, `{"file_path":"README.md"}`, `{"output":"read output"}`, nowForHistoryTest(1)); err != nil {
		t.Fatalf("insert tool call: %v", err)
	}
	insertHistoryMessage(t, sqlDB, narratorID, 2, "user1", "user", []map[string]any{{"type": "tool_result", "tool_use_id": "manual_call", "content": "manual output"}, {"type": "text", "text": "Thanks"}}, "stale user text", false)
	insertHistoryMessage(t, sqlDB, narratorID, 3, "assistant2", "assistant", []map[string]any{{"type": "redacted_thinking", "data": "secret"}, {"type": "text", "text": "Done"}}, "legacy done", false)

	history, err := service.agentHistory(context.Background(), narratorID, 20)
	if err != nil {
		t.Fatalf("agentHistory returned error: %v", err)
	}
	if len(history) != 6 {
		t.Fatalf("expected compact summary, assistant, tool result, content_json tool result, user, assistant; got %#v", history)
	}
	if history[0].Role != "user" || history[0].Content != "Prior compacted conversation summary:\nEarlier summary" {
		t.Fatalf("compact summary mismatch: %#v", history[0])
	}
	if history[1].Role != "assistant" || history[1].Content != "thinking text\nI will inspect" || len(history[1].ToolCalls) != 1 {
		t.Fatalf("assistant content/tool calls not rebuilt from content_json: %#v", history[1])
	}
	if history[1].ToolCalls[0].ID != "call_read" || history[1].ToolCalls[0].Function.Name != "Read" || history[1].ToolCalls[0].Function.Arguments != `{"file_path":"README.md"}` {
		t.Fatalf("tool_use mismatch: %#v", history[1].ToolCalls[0])
	}
	if history[2].Role != "tool" || history[2].ToolCallID != "call_read" || history[2].Content != "read output" {
		t.Fatalf("tool_result not rebuilt from narrator_tool_calls: %#v", history[2])
	}
	if history[3].Role != "tool" || history[3].ToolCallID != "manual_call" || history[3].Content != "manual output" {
		t.Fatalf("tool_result not rebuilt from user content_json: %#v", history[3])
	}
	if history[4].Role != "user" || history[4].Content != "Thanks" || len(history[4].ContentBlocks) != 1 {
		t.Fatalf("user text should come from content_json text block without duplicating tool_result: %#v", history[4])
	}
	if history[5].Role != "assistant" || history[5].Content != "Done" || len(history[5].ContentBlocks) != 2 {
		t.Fatalf("assistant redacted/text blocks not preserved: %#v", history[5])
	}
}

func insertHistoryMessage(t *testing.T, sqlDB *sql.DB, narratorID string, seq int, messageID string, role string, blocks []map[string]any, contentText string, compact bool) {
	t.Helper()
	data, err := json.Marshal(blocks)
	if err != nil {
		t.Fatalf("marshal blocks: %v", err)
	}
	createdAt := nowForHistoryTest(seq)
	if _, err := sqlDB.Exec(`INSERT INTO narrator_messages (id, narrator_id, role, content_json, content_text, created_at) VALUES (?, ?, ?, ?, ?, ?)`, messageID, narratorID, role, string(data), contentText, createdAt); err != nil {
		t.Fatalf("insert message %s: %v", messageID, err)
	}
	isCompact := 0
	if compact {
		isCompact = 1
	}
	if _, err := sqlDB.Exec(`INSERT INTO narrator_message_refs (id, narrator_id, message_id, seq, is_compact) VALUES (?, ?, ?, ?, ?)`, "ref_"+messageID, narratorID, messageID, seq, isCompact); err != nil {
		t.Fatalf("insert ref %s: %v", messageID, err)
	}
}

func nowForHistoryTest(offset int) string {
	return time.Date(2026, 5, 15, 12, 0, 0, offset, time.UTC).Format(time.RFC3339Nano)
}
