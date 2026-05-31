package narrators

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestAgentHistoryIncludesStoredSideCarsInModelContext(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sqlDB.Close()
	if err := createNarratorTestSchema(sqlDB); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	if _, err := sqlDB.Exec(`CREATE TABLE narrator_sidecars (id TEXT PRIMARY KEY, narrator_id TEXT, message_id TEXT, tool_use_id TEXT, target TEXT, source TEXT, content TEXT, order_index INTEGER, created_at TEXT)`); err != nil {
		t.Fatalf("create narrator_sidecars: %v", err)
	}
	service := &Service{DB: sqlDB}
	narratorID := "nar_sidecars"
	if _, err := sqlDB.Exec(`INSERT INTO narrators (id, type, inherit_mode, model, status, created_at, updated_at) VALUES (?, 'primary', 'full', 'openai:gpt-test', 'idle', ?, ?)`, narratorID, nowForHistoryTest(0), nowForHistoryTest(0)); err != nil {
		t.Fatalf("insert narrator: %v", err)
	}
	insertHistoryMessage(t, sqlDB, narratorID, 1, "msg-1", "assistant", []map[string]any{{"type": "text", "text": "done"}, {"type": "tool_use", "id": "call_1", "name": "Read", "input": map[string]any{"file_path": "a.txt"}}}, "done", false)
	if _, err := sqlDB.Exec(`INSERT INTO narrator_tool_calls (id, narrator_id, message_id, tool_use_id, tool_name, input_json, output_json, status, created_at) VALUES ('tc-side', ?, 'msg-1', 'call_1', 'Read', '{"file_path":"a.txt"}', '{"output":"file contents"}', 'success', ?)`, narratorID, nowForHistoryTest(2)); err != nil {
		t.Fatalf("insert tool call: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO narrator_sidecars (id, narrator_id, message_id, tool_use_id, target, source, content, order_index, created_at) VALUES ('side-tool', ?, 'msg-1', 'call_1', 'tool_result', 'a"<b&c', 'tool detail', 0, ?), ('side-user', ?, 'msg-1', NULL, 'user_message', 'progress', 'follow-up hint', 1, ?)`, narratorID, nowForHistoryTest(3), narratorID, nowForHistoryTest(4)); err != nil {
		t.Fatalf("insert sidecars: %v", err)
	}

	history, err := service.agentHistory(context.Background(), narratorID, 20)
	if err != nil {
		t.Fatalf("agentHistory returned error: %v", err)
	}
	if len(history) != 3 {
		t.Fatalf("expected assistant, tool result, user sidecar messages, got %#v", history)
	}
	if history[1].Role != "tool" || history[1].ToolCallID != "call_1" || !strings.Contains(history[1].Content, "file contents\n\n<side_car") || !strings.Contains(history[1].Content, `source="a&quot;&lt;b&amp;c"`) || !strings.Contains(history[1].Content, "tool detail") {
		t.Fatalf("tool result should include formatted sidecar text, got %#v", history[1])
	}
	if history[2].Role != "user" || !strings.Contains(history[2].Content, `<side_car source="progress">`) || !strings.Contains(history[2].Content, "follow-up hint") {
		t.Fatalf("assistant user_message sidecar should become model-visible user message, got %#v", history[2])
	}
}

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

func TestAgentHistorySubagentTreatsOwnParentToolUseMessagesAsTopLevel(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sqlDB.Close()
	if err := createNarratorTestSchema(sqlDB); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	service := &Service{DB: sqlDB}
	parentID := "nar_parent_history"
	childID := "nar_child_history"
	if _, err := sqlDB.Exec(`INSERT INTO narrators (id, type, inherit_mode, model, status, created_at, updated_at) VALUES (?, 'primary', 'full', 'openai:gpt-test', 'idle', ?, ?), (?, 'subagent', 'fresh', 'openai:gpt-test', 'idle', ?, ?)`, parentID, nowForHistoryTest(0), nowForHistoryTest(0), childID, nowForHistoryTest(0), nowForHistoryTest(0)); err != nil {
		t.Fatalf("insert narrators: %v", err)
	}
	if _, err := sqlDB.Exec(`UPDATE narrators SET parent_narrator_id = ?, variant = 'subagent:general' WHERE id = ?`, parentID, childID); err != nil {
		t.Fatalf("tag child narrator: %v", err)
	}

	insertHistoryMessage(t, sqlDB, parentID, 1, "parent-visible", "user", []map[string]any{{"type": "text", "text": "parent visible"}}, "parent visible", false)
	insertHistoryMessage(t, sqlDB, parentID, 2, "parent-childish", "assistant", []map[string]any{{"type": "text", "text": "parent hidden child tree"}}, "parent hidden child tree", false)
	if _, err := sqlDB.Exec(`UPDATE narrator_messages SET parent_tool_use_id = 'agent-hidden' WHERE id = 'parent-childish'`); err != nil {
		t.Fatalf("tag parent child-tree message: %v", err)
	}

	insertHistoryMessage(t, sqlDB, childID, 1, "child-user", "user", []map[string]any{{"type": "text", "text": "child prompt"}}, "child prompt", false)
	insertHistoryMessage(t, sqlDB, childID, 2, "child-assistant", "assistant", []map[string]any{{"type": "text", "text": "child reads"}, {"type": "tool_use", "id": "child-read", "name": "Read", "input": map[string]any{"file_path": "child.txt"}}}, "child reads", false)
	if _, err := sqlDB.Exec(`UPDATE narrator_messages SET parent_tool_use_id = 'agent-1' WHERE narrator_id = ?`, childID); err != nil {
		t.Fatalf("tag child messages parent tool use: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO narrator_tool_calls (id, narrator_id, message_id, tool_use_id, tool_name, input_json, output_json, status, created_at) VALUES ('tc-child-read', ?, 'child-assistant', 'child-read', 'Read', ?, ?, 'success', ?)`, childID, `{"file_path":"child.txt"}`, `{"output":"child file"}`, nowForHistoryTest(3)); err != nil {
		t.Fatalf("insert child tool call: %v", err)
	}

	parentHistory, err := service.agentHistory(context.Background(), parentID, 20)
	if err != nil {
		t.Fatalf("parent agentHistory returned error: %v", err)
	}
	if len(parentHistory) != 1 || parentHistory[0].Content != "parent visible" {
		t.Fatalf("parent history should filter parent_tool_use_id child-tree messages, got %#v", parentHistory)
	}

	childHistory, err := service.agentHistory(context.Background(), childID, 20)
	if err != nil {
		t.Fatalf("child agentHistory returned error: %v", err)
	}
	if len(childHistory) != 3 {
		t.Fatalf("subagent history should replay own parent_tool_use_id messages and tool result, got %#v", childHistory)
	}
	if childHistory[0].Role != "user" || childHistory[0].Content != "child prompt" {
		t.Fatalf("subagent user prompt should be top-level for provider replay: %#v", childHistory[0])
	}
	if childHistory[1].Role != "assistant" || len(childHistory[1].ToolCalls) != 1 || childHistory[1].ToolCalls[0].ID != "child-read" {
		t.Fatalf("subagent assistant tool_use should replay despite parent_tool_use_id: %#v", childHistory[1])
	}
	if childHistory[2].Role != "tool" || childHistory[2].ToolCallID != "child-read" || childHistory[2].Content != "child file" {
		t.Fatalf("subagent tool result should replay despite parent_tool_use_id: %#v", childHistory[2])
	}
}

func TestAgentHistoryKeepsTextFileAttachmentHintForProviderReplay(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sqlDB.Close()
	if err := createNarratorTestSchema(sqlDB); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	service := &Service{DB: sqlDB}
	narratorID := "nar_text_file_history"
	if _, err := sqlDB.Exec(`INSERT INTO narrators (id, type, inherit_mode, model, status, created_at, updated_at) VALUES (?, 'primary', 'full', 'openai:gpt-test', 'idle', ?, ?)`, narratorID, nowForHistoryTest(0), nowForHistoryTest(0)); err != nil {
		t.Fatalf("insert narrator: %v", err)
	}
	effective := "Review notes\n\n<attached_files>\nThe user has attached the following files for your reference. Use the Read tool to access their contents when needed.\n- /work/notes.md (notes.md, 12 B)\n</attached_files>"
	insertHistoryMessage(t, sqlDB, narratorID, 1, "msg-text-file", "user", []map[string]any{
		{"type": "text_file", "filename": "notes.md", "size": 12, "filePath": "/work/notes.md"},
		{"type": "text", "text": "Review notes"},
	}, effective, false)

	history, err := service.agentHistory(context.Background(), narratorID, 20)
	if err != nil {
		t.Fatalf("agentHistory returned error: %v", err)
	}
	if len(history) != 1 || !strings.Contains(history[0].Content, "<attached_files>") {
		t.Fatalf("expected provider content to keep attachment hint, got %#v", history)
	}
	if len(history[0].ContentBlocks) != 2 || history[0].ContentBlocks[1]["text"] != effective {
		t.Fatalf("expected text block to carry provider-effective attachment hint, got %#v", history[0].ContentBlocks)
	}
}

func TestAgentHistoryEmbedsUploadedImageBlocksForProviderReplay(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sqlDB.Close()
	if err := createNarratorTestSchema(sqlDB); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	service := &Service{DB: sqlDB}
	narratorID := "nar_image_history"
	if _, err := sqlDB.Exec(`INSERT INTO narrators (id, type, inherit_mode, model, status, created_at, updated_at) VALUES (?, 'primary', 'full', 'openai:gpt-test', 'idle', ?, ?)`, narratorID, nowForHistoryTest(0), nowForHistoryTest(0)); err != nil {
		t.Fatalf("insert narrator: %v", err)
	}
	uploadDir := filepath.Join(home, ".narrafork", "uploads", narratorID)
	if err := os.MkdirAll(uploadDir, 0o755); err != nil {
		t.Fatalf("create upload dir: %v", err)
	}
	pngBytes := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	if err := os.WriteFile(filepath.Join(uploadDir, "img-1.png"), pngBytes, 0o644); err != nil {
		t.Fatalf("write image: %v", err)
	}
	insertHistoryMessage(t, sqlDB, narratorID, 1, "msg-image", "user", []map[string]any{
		{"type": "image", "imageId": "img-1", "filename": "img.png", "mediaType": "image/png"},
		{"type": "text", "text": "describe image"},
	}, "describe image", false)

	history, err := service.agentHistory(context.Background(), narratorID, 20)
	if err != nil {
		t.Fatalf("agentHistory returned error: %v", err)
	}
	if len(history) != 1 || len(history[0].ContentBlocks) != 2 {
		t.Fatalf("unexpected history: %#v", history)
	}
	image := history[0].ContentBlocks[0]
	if image["base64"] != "iVBORw0KGgo=" || image["uploadNarratorId"] != narratorID || image["mediaType"] != "image/png" {
		t.Fatalf("uploaded image block was not embedded for provider replay: %#v", image)
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
