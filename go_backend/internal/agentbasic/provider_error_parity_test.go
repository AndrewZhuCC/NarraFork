package agentbasic

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAIChatCompletionsParityToolStartAndInvalidStates(t *testing.T) {
	t.Run("streams tool start chunks before argument deltas", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_read","type":"function","function":{"name":"Read"}}]}}]}

`)
			fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"file_path\":"}}]}}]}

`)
			fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"note.txt\"}"}}]}}]}

`)
			fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}

`)
			fmt.Fprint(w, `data: {"choices":[],"usage":{"prompt_tokens":11,"completion_tokens":7,"completion_tokens_details":{"reasoning_tokens":3},"prompt_tokens_details":{"cached_tokens":2}}}

`)
			fmt.Fprint(w, "data: [DONE]\n\n")
		}))
		defer server.Close()

		adapter := &OpenAIChatCompletionsAdapter{Provider: Provider{Prefix: "openai", Kind: "openai", APIKey: "test-key", BaseURL: server.URL, Model: "gpt-test"}}
		stream, err := adapter.Chat(context.Background(), ChatParams{History: &BuiltHistory{History: []any{Message{Role: "user", Content: "read"}}}})
		if err != nil {
			t.Fatalf("Chat returned error: %v", err)
		}
		chunks := []*ToolUseChunk{}
		var usage *UsageSnapshot
		for event := range stream {
			if event.Err != nil {
				t.Fatalf("stream error: %v", event.Err)
			}
			if event.Parsed.ToolUseChunk != nil {
				chunks = append(chunks, event.Parsed.ToolUseChunk)
			}
			if event.Parsed.Usage != nil {
				usage = event.Parsed.Usage
			}
		}
		if len(chunks) != 4 || chunks[0].ToolUseID != "call_read" || chunks[0].Name != "Read" || chunks[0].Input != "" || !chunks[3].Stop {
			t.Fatalf("unexpected tool chunks: %#v", chunks)
		}
		if usage == nil || usage.PromptTokens != 11 || usage.CompletionTokens != 7 || usage.ReasoningTokens != 3 || usage.CachedInputTokens != 2 {
			t.Fatalf("unexpected usage: %#v", usage)
		}
	})

	t.Run("maps stream error chunks to invalid state events", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, `data: {"error":{"message":"rate limited","type":"rate_limit_error","code":"rate_limit"}}

`)
		}))
		defer server.Close()

		adapter := &OpenAIChatCompletionsAdapter{Provider: Provider{Prefix: "openai", Kind: "openai", APIKey: "test-key", BaseURL: server.URL, Model: "gpt-test"}}
		stream, err := adapter.Chat(context.Background(), ChatParams{History: &BuiltHistory{History: []any{Message{Role: "user", Content: "hi"}}}})
		if err != nil {
			t.Fatalf("Chat returned error: %v", err)
		}
		invalid := (*InvalidState)(nil)
		for event := range stream {
			if event.Err != nil {
				t.Fatalf("expected invalidState event instead of stream error: %v", event.Err)
			}
			if event.Parsed.InvalidState != nil {
				invalid = event.Parsed.InvalidState
			}
		}
		if invalid == nil || invalid.Reason != "rate_limit" || invalid.Message != "rate limited" {
			t.Fatalf("unexpected invalid state: %#v", invalid)
		}
	})

	t.Run("maps truncating finish reasons to invalid state events", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"content":"partial"},"finish_reason":"length"}]}

`)
		}))
		defer server.Close()

		adapter := &OpenAIChatCompletionsAdapter{Provider: Provider{Prefix: "openai", Kind: "openai", APIKey: "test-key", BaseURL: server.URL, Model: "gpt-test"}}
		stream, err := adapter.Chat(context.Background(), ChatParams{History: &BuiltHistory{History: []any{Message{Role: "user", Content: "hi"}}}})
		if err != nil {
			t.Fatalf("Chat returned error: %v", err)
		}
		invalid := (*InvalidState)(nil)
		for event := range stream {
			if event.Err != nil {
				t.Fatalf("stream error: %v", event.Err)
			}
			if event.Parsed.InvalidState != nil {
				invalid = event.Parsed.InvalidState
			}
		}
		if invalid == nil || invalid.Reason != "max_tokens" {
			t.Fatalf("expected max_tokens invalid state, got %#v", invalid)
		}
	})
}

func TestOpenAIResponsesParityResponseIDsToolStartAndErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"type":"response.created","response":{"id":"resp_start"}}

`)
		fmt.Fprint(w, `data: {"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","id":"fc_1","call_id":"call_read","name":"Read"}}

`)
		fmt.Fprint(w, `data: {"type":"response.function_call_arguments.delta","item_id":"fc_1","output_index":1,"delta":"{\"file_path\":\"note.txt\"}"}

`)
		fmt.Fprint(w, `data: {"type":"response.function_call_arguments.done","item_id":"fc_1","output_index":1}

`)
		fmt.Fprint(w, `data: {"type":"response.failed","response":{"id":"resp_failed","error":{"message":"boom","code":"server_error"}}}

`)
	}))
	defer server.Close()

	adapter := &OpenAIChatCompletionsAdapter{Provider: Provider{Prefix: "openai", Kind: "openai", APIKey: "test-key", BaseURL: server.URL, Model: "gpt-test", APIMode: "responses"}}
	stream, err := adapter.Chat(context.Background(), ChatParams{History: &BuiltHistory{History: []any{Message{Role: "user", Content: "hi"}}}})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	seenResponseID := false
	chunks := []*ToolUseChunk{}
	invalid := (*InvalidState)(nil)
	for event := range stream {
		if event.Err != nil {
			t.Fatalf("expected invalidState event instead of stream error: %v", event.Err)
		}
		if event.Parsed.ResponseID == "resp_start" && event.Parsed.ResponsesAPI {
			seenResponseID = true
		}
		if event.Parsed.ToolUseChunk != nil {
			chunks = append(chunks, event.Parsed.ToolUseChunk)
		}
		if event.Parsed.InvalidState != nil {
			invalid = event.Parsed.InvalidState
		}
	}
	if !seenResponseID {
		t.Fatalf("expected response.created response id event")
	}
	if len(chunks) != 3 || chunks[0].ToolUseID != "call_read" || chunks[0].Name != "Read" || !chunks[2].Stop {
		t.Fatalf("unexpected responses tool chunks: %#v", chunks)
	}
	if invalid == nil || invalid.Reason != "server_error" || invalid.Message != "boom" {
		t.Fatalf("unexpected responses invalid state: %#v", invalid)
	}
}

func TestAnthropicMessagesParityInvalidStates(t *testing.T) {
	t.Run("maps truncating stop reasons to invalid state events", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, `event: message_start
data: {"type":"message_start","message":{"id":"msg_1"}}

`)
			fmt.Fprint(w, `event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"max_tokens"}}

`)
		}))
		defer server.Close()

		adapter := &AnthropicMessagesAdapter{Provider: Provider{Prefix: "anthropic", Kind: "anthropic", APIKey: "test-key", BaseURL: server.URL, Model: "claude-test"}}
		stream, err := adapter.Chat(context.Background(), ChatParams{History: &BuiltHistory{History: []any{map[string]any{"role": "user", "content": "hi"}}}})
		if err != nil {
			t.Fatalf("Chat returned error: %v", err)
		}
		invalid := (*InvalidState)(nil)
		for event := range stream {
			if event.Err != nil {
				t.Fatalf("stream error: %v", event.Err)
			}
			if event.Parsed.InvalidState != nil {
				invalid = event.Parsed.InvalidState
			}
		}
		if invalid == nil || invalid.Reason != "max_tokens" {
			t.Fatalf("expected max_tokens invalid state, got %#v", invalid)
		}
	})

	t.Run("maps error events to invalid state events", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, `event: error
data: {"type":"error","error":{"type":"overloaded_error","message":"try again"}}

`)
		}))
		defer server.Close()

		adapter := &AnthropicMessagesAdapter{Provider: Provider{Prefix: "anthropic", Kind: "anthropic", APIKey: "test-key", BaseURL: server.URL, Model: "claude-test"}}
		stream, err := adapter.Chat(context.Background(), ChatParams{History: &BuiltHistory{History: []any{map[string]any{"role": "user", "content": "hi"}}}})
		if err != nil {
			t.Fatalf("Chat returned error: %v", err)
		}
		invalid := (*InvalidState)(nil)
		for event := range stream {
			if event.Err != nil {
				t.Fatalf("expected invalidState event instead of stream error: %v", event.Err)
			}
			if event.Parsed.InvalidState != nil {
				invalid = event.Parsed.InvalidState
			}
		}
		if invalid == nil || invalid.Reason != "overloaded_error" || invalid.Message != "try again" {
			t.Fatalf("unexpected anthropic invalid state: %#v", invalid)
		}
	})
}
