package agentbasic

import "testing"

func TestAnthropicHistoryPreservesStoredContentBlocks(t *testing.T) {
	call := newFunctionToolCall("tool_1", "Read", `{"file_path":"note.txt"}`)
	history, err := (&AnthropicMessagesAdapter{}).BuildHistory([]Message{
		{
			Role:    "assistant",
			Content: "fallback should not replace blocks",
			ContentBlocks: []map[string]any{
				{"type": "reasoning", "text": "think", "providerMetadata": map[string]any{"anthropic": map[string]any{"signature": "sig_1"}}},
				{"type": "redacted_thinking", "data": "redacted_1"},
				{"type": "text", "text": "I will read"},
				{"type": "tool_use", "id": "tool_1", "name": "Read", "input": map[string]any{"file_path": "note.txt"}},
			},
			ToolCalls: []ToolCall{call},
		},
		{Role: "tool", ToolCallID: "tool_1", Content: "file contents"},
	}, "", "")
	if err != nil {
		t.Fatalf("BuildHistory returned error: %v", err)
	}
	if len(history.History) != 2 {
		t.Fatalf("expected assistant and tool-result user messages, got %#v", history.History)
	}
	assistant, _ := history.History[0].(map[string]any)
	parts, _ := assistant["content"].([]any)
	if len(parts) != 4 {
		t.Fatalf("expected four preserved assistant parts, got %#v", parts)
	}
	if part, _ := parts[0].(map[string]any); part["type"] != "thinking" || part["thinking"] != "think" || part["signature"] != "sig_1" {
		t.Fatalf("reasoning block was not replayed as anthropic thinking: %#v", parts[0])
	}
	if part, _ := parts[1].(map[string]any); part["type"] != "redacted_thinking" || part["data"] != "redacted_1" {
		t.Fatalf("redacted thinking block mismatch: %#v", parts[1])
	}
	if part, _ := parts[3].(map[string]any); part["type"] != "tool_use" || part["id"] != "tool_1" || part["name"] != "Read" {
		t.Fatalf("tool_use block mismatch: %#v", parts[3])
	}
	toolResultMsg, _ := history.History[1].(map[string]any)
	toolParts, _ := toolResultMsg["content"].([]any)
	if part, _ := toolParts[0].(map[string]any); part["type"] != "tool_result" || part["tool_use_id"] != "tool_1" || part["content"] != "file contents" {
		t.Fatalf("tool_result replay mismatch: %#v", toolParts)
	}
}

func TestOpenAIResponsesHistoryPreservesToolsAndDegradesUnsupportedReasoning(t *testing.T) {
	input := openAIResponsesInput([]Message{
		{
			Role: "assistant",
			ContentBlocks: []map[string]any{
				{"type": "reasoning", "text": "native thought", "providerMetadata": map[string]any{"openai": map[string]any{"itemId": "rs_1", "reasoningEncryptedContent": "enc_1"}}},
				{"type": "text", "text": "I will read"},
				{"type": "redacted_thinking", "data": "anthropic-only"},
				{"type": "tool_use", "id": "call_1", "name": "Read", "input": map[string]any{"file_path": "note.txt"}},
			},
		},
		{Role: "tool", ToolCallID: "call_1", Content: "tool output"},
		{
			Role:          "assistant",
			ContentBlocks: []map[string]any{{"type": "reasoning", "text": "no encrypted state"}},
		},
	})
	if len(input) != 5 {
		t.Fatalf("expected reasoning/message/function/output/degraded-message items, got %#v", input)
	}
	if item, _ := input[0].(map[string]any); item["type"] != "reasoning" || item["id"] != "rs_1" || item["encrypted_content"] != "enc_1" {
		t.Fatalf("native reasoning item mismatch: %#v", input[0])
	}
	if item, _ := input[2].(map[string]any); item["type"] != "function_call" || item["call_id"] != "call_1" || item["name"] != "Read" {
		t.Fatalf("function call mismatch: %#v", input[2])
	}
	if item, _ := input[3].(map[string]any); item["type"] != "function_call_output" || item["call_id"] != "call_1" || item["output"] != "tool output" {
		t.Fatalf("function output mismatch: %#v", input[3])
	}
	if item, _ := input[4].(map[string]any); item["role"] != "assistant" || item["content"] != "no encrypted state" {
		t.Fatalf("unsupported reasoning should degrade to assistant text, got %#v", input[4])
	}
}
