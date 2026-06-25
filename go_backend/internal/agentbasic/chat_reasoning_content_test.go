package agentbasic

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestChatCompletionsCapturesReasoningContentDelta pins Part A of the
// reasoning_content parity: DeepSeek/QwQ stream extended-thinking text in
// delta.reasoning_content over the chat-completions path. Go must surface it as a
// Reasoning stream event (mirroring TS openai-provider.ts:2301-2306) so it is
// shown and persisted as a reasoning block, instead of being silently discarded.
func TestChatCompletionsCapturesReasoningContentDelta(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"reasoning_content":"thinking hard"}}]}`+"\n\n")
		fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"content":"the answer"}}]}`+"\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()
	adapter := &OpenAIChatCompletionsAdapter{Provider: Provider{Prefix: "openai", Kind: "openai", APIKey: "k", BaseURL: server.URL, Model: "deepseek-chat"}}
	stream, err := adapter.Chat(context.Background(), ChatParams{History: &BuiltHistory{History: []any{Message{Role: "user", Content: "hi"}}}})
	if err != nil {
		t.Fatalf("Chat error: %v", err)
	}
	sawReasoning := false
	sawText := false
	for event := range stream {
		if event.Err != nil {
			t.Fatalf("stream error: %v", event.Err)
		}
		if event.Parsed.Reasoning == "thinking hard" {
			sawReasoning = true
		}
		if event.Parsed.Text == "the answer" {
			sawText = true
		}
	}
	if !sawReasoning {
		t.Fatal("expected a Reasoning stream event from delta.reasoning_content")
	}
	if !sawText {
		t.Fatal("expected the visible text event after reasoning")
	}
}

// TestChatCompletionMessagesReplaysReasoningContent pins Part B: a stored
// assistant message whose ContentBlocks carry a reasoning block is rebuilt with
// reasoning_content set from the reasoning block and content from text blocks only
// (no duplication), mirroring TS buildOAIHistory + reasoning_content replay.
func TestChatCompletionMessagesReplaysReasoningContent(t *testing.T) {
	message := Message{
		Role:    "assistant",
		Content: "prior thought\nvisible", // messageTextForAgent folds reasoning into Content
		ContentBlocks: []map[string]any{
			{"type": "reasoning", "text": "prior thought"},
			{"type": "text", "text": "visible"},
		},
	}
	out := openAIChatCompletionMessages([]Message{message}, "deepseek-chat")
	if len(out) != 1 {
		t.Fatalf("expected 1 wire message, got %d", len(out))
	}
	if out[0]["reasoning_content"] != "prior thought" {
		t.Fatalf("reasoning_content = %#v, want \"prior thought\"", out[0]["reasoning_content"])
	}
	if out[0]["content"] != "visible" {
		t.Fatalf("content must be text-only (no folded reasoning), got %#v", out[0]["content"])
	}
}

// TestChatCompletionMessagesDeepSeekPatchesEmptyReasoningContent pins the DeepSeek
// "all assistant messages need reasoning_content" patch (TS openai-provider.ts:
// 559-560): an assistant message without a reasoning block gets reasoning_content
// = "" for DeepSeek models so the API does not reject the multi-turn request.
func TestChatCompletionMessagesDeepSeekPatchesEmptyReasoningContent(t *testing.T) {
	message := Message{Role: "assistant", Content: "hi"}
	out := openAIChatCompletionMessages([]Message{message}, "deepseek-chat")
	rc, has := out[0]["reasoning_content"]
	if !has || rc != "" {
		t.Fatalf("DeepSeek assistant without reasoning should get reasoning_content=\"\", got has=%v val=%#v", has, rc)
	}
	if out[0]["content"] != "hi" {
		t.Fatalf("content should be preserved, got %#v", out[0]["content"])
	}
}

// TestChatCompletionMessagesNonDeepSeekNoReasoningPatch is the zero-regression
// control: a non-DeepSeek assistant message without a reasoning block must NOT get
// a reasoning_content field, and its content is unchanged.
func TestChatCompletionMessagesNonDeepSeekNoReasoningPatch(t *testing.T) {
	message := Message{Role: "assistant", Content: "hello"}
	out := openAIChatCompletionMessages([]Message{message}, "gpt-4o")
	if _, has := out[0]["reasoning_content"]; has {
		t.Fatalf("non-DeepSeek must not add reasoning_content, got %#v", out[0]["reasoning_content"])
	}
	if out[0]["content"] != "hello" {
		t.Fatalf("content should be preserved, got %#v", out[0]["content"])
	}
}

// TestChatCompletionMessagesReasoningContentJSONShape sanity-checks the serialized
// wire payload includes reasoning_content as a top-level string field.
func TestChatCompletionMessagesReasoningContentJSONShape(t *testing.T) {
	message := Message{
		Role:          "assistant",
		ContentBlocks: []map[string]any{{"type": "reasoning", "text": "r"}, {"type": "text", "text": "v"}},
	}
	out := openAIChatCompletionMessages([]Message{message}, "deepseek-reasoner")
	encoded, err := json.Marshal(out[0])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded["reasoning_content"] != "r" || decoded["content"] != "v" {
		t.Fatalf("unexpected wire shape: %s", string(encoded))
	}
}
