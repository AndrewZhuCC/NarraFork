package agentbasic

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestOpenAIChatCompletionsStreamsTextAndUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if !strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
			t.Fatalf("expected SSE accept header, got %q", r.Header.Get("Accept"))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"content":"hel"}}]}

`)
		fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"content":"lo"}}]}

`)
		fmt.Fprint(w, `data: {"choices":[],"usage":{"prompt_tokens":3,"completion_tokens":2,"completion_tokens_details":{"reasoning_tokens":1},"prompt_tokens_details":{"cached_tokens":1}}}

`)
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()
	adapter := &OpenAIChatCompletionsAdapter{Provider: Provider{Prefix: "openai", Kind: "openai", APIKey: "test-key", BaseURL: server.URL, Model: "gpt-test"}}
	stream, err := adapter.Chat(context.Background(), ChatParams{History: &BuiltHistory{History: []any{Message{Role: "user", Content: "hi"}}}})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	texts := []string{}
	var usage *UsageSnapshot
	for event := range stream {
		if event.Err != nil {
			t.Fatalf("stream error: %v", event.Err)
		}
		if event.Parsed.Text != "" {
			texts = append(texts, event.Parsed.Text)
		}
		if event.Parsed.Usage != nil {
			usage = event.Parsed.Usage
		}
	}
	if !reflect.DeepEqual(texts, []string{"hel", "lo"}) {
		t.Fatalf("texts = %#v", texts)
	}
	if usage == nil || usage.PromptTokens != 3 || usage.CompletionTokens != 2 || usage.ReasoningTokens != 1 || usage.CachedInputTokens != 1 {
		t.Fatalf("unexpected usage: %#v", usage)
	}
}

func TestOpenAIChatCompletionsEmitsSilentDisconnectWithoutDone(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"content":"partial"}}]}

`)
	}))
	defer server.Close()
	adapter := &OpenAIChatCompletionsAdapter{Provider: Provider{Prefix: "openai", Kind: "openai", APIKey: "test-key", BaseURL: server.URL, Model: "gpt-test"}}
	stream, err := adapter.Chat(context.Background(), ChatParams{History: &BuiltHistory{History: []any{Message{Role: "user", Content: "hi"}}}})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	seenPartial := false
	seenSilent := false
	for event := range stream {
		if event.Err != nil {
			t.Fatalf("stream error: %v", event.Err)
		}
		if event.Parsed.Text == "partial" {
			seenPartial = true
		}
		if event.Parsed.SilentDisconnect {
			seenSilent = true
		}
	}
	if !seenPartial || !seenSilent {
		t.Fatalf("expected partial text and silent disconnect, partial=%v silent=%v", seenPartial, seenSilent)
	}
}

func TestRunAgentLoopConsumesOpenAIStreamingToolChunks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if !strings.Contains(r.Header.Get("Authorization"), "test-key") {
			t.Fatalf("missing auth header")
		}
		fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_read","type":"function","function":{"name":"Read"}}]}}]}

`)
		fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"file"}}]}}]}

`)
		fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"_path\":\"note.txt\"}"}}]}}]}

`)
		fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}

`)
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()
	adapter := &OpenAIChatCompletionsAdapter{Provider: Provider{Prefix: "openai", Kind: "openai", APIKey: "test-key", BaseURL: server.URL, Model: "gpt-test"}}
	stream := runAgentLoop(
		context.Background(),
		ProviderResolution{Provider: "openai", Model: "gpt-test", Adapter: adapter},
		[]Message{{Role: "user", Content: "read"}},
		t.TempDir(),
		normalizeGenerateOptions(
			WithMaxTurns(1),
			WithRetryBaseDelay(0),
			WithToolExecutor(func(ctx context.Context, cwd string, call ToolCall) (ToolRun, error) {
				if call.ID != "call_read" || call.Function.Name != "Read" || call.Function.Arguments != `{"file_path":"note.txt"}` {
					t.Fatalf("unexpected tool call: %#v", call)
				}
				return ToolRun{Output: "read output"}, nil
			}),
		),
	)
	events := collectEvents(stream)
	if eventTypeCount(events, "tool_use_chunk") != 4 || eventTypeCount(events, "tool_call") != 1 || eventTypeCount(events, "tool_result") != 1 {
		t.Fatalf("unexpected events: %#v", eventTypes(events))
	}
}
