package agentbasic

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestAnthropicHistoryPreservesUserImageBlocks(t *testing.T) {
	history, err := (&AnthropicMessagesAdapter{}).BuildHistory([]Message{{
		Role: "user",
		ContentBlocks: []map[string]any{
			{"type": "image", "mediaType": "image/png", "base64": "iVBORw0KGgo="},
			{"type": "text", "text": "describe it"},
		},
	}}, "", "")
	if err != nil {
		t.Fatalf("BuildHistory returned error: %v", err)
	}
	message, _ := history.History[0].(map[string]any)
	parts, _ := message["content"].([]any)
	if len(parts) != 2 {
		t.Fatalf("expected image and text parts, got %#v", parts)
	}
	image, _ := parts[0].(map[string]any)
	source, _ := image["source"].(map[string]any)
	if image["type"] != "image" || source["media_type"] != "image/png" || source["data"] != "iVBORw0KGgo=" {
		t.Fatalf("image block was not replayed for Anthropic: %#v", parts[0])
	}
}

func TestOpenAIHistoryPreservesUserImageBlocks(t *testing.T) {
	message := Message{
		Role: "user",
		ContentBlocks: []map[string]any{
			{"type": "image", "mediaType": "image/png", "base64": "iVBORw0KGgo="},
			{"type": "text", "text": "describe it"},
		},
	}
	chat := openAIChatCompletionMessages([]Message{message})
	chatParts, _ := chat[0]["content"].([]map[string]any)
	if len(chatParts) != 2 || chatParts[0]["type"] != "image_url" {
		t.Fatalf("chat image parts not preserved: %#v", chat[0]["content"])
	}
	imageURL, _ := chatParts[0]["image_url"].(map[string]any)
	if imageURL["url"] != "data:image/png;base64,iVBORw0KGgo=" {
		t.Fatalf("unexpected chat image URL: %#v", imageURL)
	}

	responses := openAIResponsesInput([]Message{message})
	responsesMessage, _ := responses[0].(map[string]any)
	responsesParts, _ := responsesMessage["content"].([]map[string]any)
	if len(responsesParts) != 2 || responsesParts[0]["type"] != "input_image" || responsesParts[0]["image_url"] != "data:image/png;base64,iVBORw0KGgo=" {
		t.Fatalf("responses image parts not preserved: %#v", responsesMessage["content"])
	}
}

func TestProviderHistoryReplaysNativeWebSearchBlocks(t *testing.T) {
	message := Message{
		Role: "assistant",
		ContentBlocks: []map[string]any{
			{"type": "text", "text": "I searched."},
			{"type": "web_search", "query": "narrafork go backend", "queries": []any{"narrafork", "go backend"}},
		},
	}

	anthropicHistory, err := (&AnthropicMessagesAdapter{}).BuildHistory([]Message{message}, "", "")
	if err != nil {
		t.Fatalf("Anthropic BuildHistory returned error: %v", err)
	}
	if len(anthropicHistory.History) != 2 {
		t.Fatalf("expected injected web search context and assistant message, got %#v", anthropicHistory.History)
	}
	anthropicSearch, _ := anthropicHistory.History[0].(map[string]any)
	if anthropicSearch["role"] != "user" || anthropicSearch["content"] != "[Web search: narrafork go backend]" {
		t.Fatalf("Anthropic web search context mismatch: %#v", anthropicSearch)
	}

	chat := openAIChatCompletionMessages([]Message{message})
	if len(chat) != 2 || chat[0]["role"] != "user" || chat[0]["content"] != "[Web search: narrafork go backend]" {
		t.Fatalf("OpenAI chat web search context mismatch: %#v", chat)
	}

	responses := openAIResponsesInput([]Message{message})
	if len(responses) != 2 {
		t.Fatalf("expected assistant text and native web_search_call, got %#v", responses)
	}
	webSearch, _ := responses[1].(map[string]any)
	action, _ := webSearch["action"].(map[string]any)
	if webSearch["type"] != "web_search_call" || webSearch["status"] != "completed" || action["query"] != "narrafork go backend" {
		t.Fatalf("OpenAI responses web_search_call mismatch: %#v", webSearch)
	}
	if _, hasID := webSearch["id"]; hasID {
		t.Fatalf("OpenAI responses web_search_call must not replay rejected id field: %#v", webSearch)
	}
}

func TestOpenAIResponsesHistoryReplaysImageGenerationBlocks(t *testing.T) {
	savedPath := filepath.Join(t.TempDir(), "generated.png")
	if err := os.WriteFile(savedPath, []byte("png-bytes"), 0o600); err != nil {
		t.Fatalf("write generated image fixture: %v", err)
	}
	input := openAIResponsesInput([]Message{{
		Role: "assistant",
		ContentBlocks: []map[string]any{
			{"type": "image_generation", "id": "ig_saved", "revisedPrompt": "a blue square", "savedPath": savedPath},
			{"type": "image_generation", "id": "ig_inline", "revisedPrompt": "inline", "result": "aW5saW5l"},
		},
	}})
	if len(input) != 3 {
		t.Fatalf("expected saved-path instruction and two native image_generation_call items, got %#v", input)
	}
	instruction, _ := input[0].(map[string]any)
	if instruction["role"] != "developer" || !strings.Contains(stringValue(instruction["content"]), savedPath) {
		t.Fatalf("saved-path developer instruction mismatch: %#v", instruction)
	}
	saved, _ := input[1].(map[string]any)
	if saved["type"] != "image_generation_call" || saved["id"] != "ig_saved" || saved["result"] != "cG5nLWJ5dGVz" || saved["revised_prompt"] != "a blue square" {
		t.Fatalf("saved image_generation_call mismatch: %#v", saved)
	}
	inline, _ := input[2].(map[string]any)
	if inline["type"] != "image_generation_call" || inline["id"] != "ig_inline" || inline["result"] != "aW5saW5l" || inline["revised_prompt"] != "inline" {
		t.Fatalf("inline image_generation_call mismatch: %#v", inline)
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
