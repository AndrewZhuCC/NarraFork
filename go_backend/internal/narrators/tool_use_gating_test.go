package narrators

import (
	"testing"

	"narrafork-go/internal/agentbasic"
)

// TestToolUseGatingDropsOrphanContentBlocks verifies that an assistant message
// whose contentJson contains a tool_use block WITHOUT a matching terminal
// tool-call record does not leak that orphan tool_use into the model-facing
// ContentBlocks, while a tool_use with a completed record is preserved and
// text/thinking blocks keep their original order. Mirrors TS buildHistory
// completedToolUseIds gating (anthropic-provider.ts:1909-1927).
func TestToolUseGatingDropsOrphanContentBlocks(t *testing.T) {
	blocks := []map[string]any{
		{"type": "thinking", "text": "thinking first"},
		{"type": "text", "text": "let me try two tools"},
		{"type": "tool_use", "id": "completed-1", "name": "Read", "input": map[string]any{"file_path": "a.txt"}},
		{"type": "tool_use", "id": "orphan-1", "name": "Write", "input": map[string]any{"file_path": "b.txt"}},
	}
	msg := Message{
		Role:        "assistant",
		ContentJSON: blocks,
		ToolCalls: []ToolCallRecord{
			{ToolUseID: "completed-1", ToolName: "Read", Status: "success", OutputJSON: map[string]any{"output": "ok"}},
			// orphan-1 has no terminal record (e.g. broken/interrupted call).
		},
	}

	messages := agentMessagesFromStoredMessage(msg)
	if len(messages) == 0 || messages[0].Role != "assistant" {
		t.Fatalf("expected leading assistant message, got %#v", messages)
	}
	gated := messages[0].ContentBlocks

	// Order preserved for non-tool blocks; orphan tool_use dropped; completed kept.
	wantTypes := []string{"thinking", "text", "tool_use"}
	if len(gated) != len(wantTypes) {
		t.Fatalf("expected %d gated blocks, got %d: %#v", len(wantTypes), len(gated), gated)
	}
	for i, want := range wantTypes {
		if got, _ := gated[i]["type"].(string); got != want {
			t.Fatalf("gated block %d type = %q, want %q (%#v)", i, got, want, gated)
		}
	}
	if id, _ := gated[2]["id"].(string); id != "completed-1" {
		t.Fatalf("expected the completed tool_use to remain, got id=%q", id)
	}
	for _, block := range gated {
		if id, _ := block["id"].(string); id == "orphan-1" {
			t.Fatalf("orphan tool_use must be filtered from ContentBlocks: %#v", gated)
		}
	}
}

// TestToolUseGatingKeepsBlocksWhenAllMatched verifies the gate is a no-op when
// every tool_use has a terminal tool-call record (no blocks dropped).
func TestToolUseGatingKeepsBlocksWhenAllMatched(t *testing.T) {
	blocks := []map[string]any{
		{"type": "text", "text": "running"},
		{"type": "tool_use", "id": "t1", "name": "Read", "input": map[string]any{"file_path": "a.txt"}},
	}
	msg := Message{
		Role:        "assistant",
		ContentJSON: blocks,
		ToolCalls: []ToolCallRecord{
			{ToolUseID: "t1", ToolName: "Read", Status: "fail", OutputJSON: map[string]any{"output": "err"}},
		},
	}
	messages := agentMessagesFromStoredMessage(msg)
	var gated []map[string]any
	for _, m := range messages {
		if m.Role == "assistant" {
			gated = m.ContentBlocks
			break
		}
	}
	if len(gated) != 2 {
		t.Fatalf("expected both blocks preserved when matched, got %#v", gated)
	}
	// The matched tool_use must still produce a tool-call for the model.
	var assistant agentbasic.Message
	for _, m := range messages {
		if m.Role == "assistant" {
			assistant = m
			break
		}
	}
	if len(assistant.ToolCalls) != 1 || assistant.ToolCalls[0].ID != "t1" {
		t.Fatalf("expected matched tool call retained, got %#v", assistant.ToolCalls)
	}
}
