package agentbasic

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// TestOpenAIChatCompletionsDeepSeekReasoningMatchesTS pins the DeepSeek
// chat-completions reasoning shape to the TS openai-provider: DeepSeek sends a
// `thinking` config plus a DeepSeek-tiered reasoning_effort. `none` ->
// thinking:{type:"disabled"} with no reasoning_effort; low/medium/high ->
// thinking:{type:"enabled"} + reasoning_effort:"high"; xhigh -> reasoning_effort:"max".
func TestOpenAIChatCompletionsDeepSeekReasoningMatchesTS(t *testing.T) {
	run := func(t *testing.T, effort string) map[string]any {
		var requestBody map[string]any
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&requestBody)
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: [DONE]\n\n")
		}))
		defer server.Close()
		adapter := &OpenAIChatCompletionsAdapter{Provider: Provider{Prefix: "openai", Kind: "openai", APIKey: "k", BaseURL: server.URL, Model: "deepseek-chat", DefaultReasoningEffort: effort}}
		stream, err := adapter.Chat(context.Background(), ChatParams{History: &BuiltHistory{History: []any{Message{Role: "user", Content: "hi"}}}})
		if err != nil {
			t.Fatalf("Chat error: %v", err)
		}
		for event := range stream {
			if event.Err != nil {
				t.Fatalf("stream error: %v", event.Err)
			}
		}
		return requestBody
	}

	t.Run("xhigh maps to max with thinking enabled", func(t *testing.T) {
		body := run(t, "xhigh")
		thinking, _ := body["thinking"].(map[string]any)
		if thinking == nil || thinking["type"] != "enabled" {
			t.Fatalf("expected thinking enabled, got %#v", body["thinking"])
		}
		if body["reasoning_effort"] != "max" {
			t.Fatalf("xhigh should map to reasoning_effort max, got %#v", body["reasoning_effort"])
		}
	})

	t.Run("medium maps to high", func(t *testing.T) {
		body := run(t, "medium")
		if body["reasoning_effort"] != "high" {
			t.Fatalf("medium should map to reasoning_effort high, got %#v", body["reasoning_effort"])
		}
	})

	t.Run("none disables thinking with no effort", func(t *testing.T) {
		body := run(t, "none")
		thinking, _ := body["thinking"].(map[string]any)
		if thinking == nil || thinking["type"] != "disabled" {
			t.Fatalf("none should disable thinking, got %#v", body["thinking"])
		}
		if _, has := body["reasoning_effort"]; has {
			t.Fatalf("none must not send reasoning_effort, got %#v", body["reasoning_effort"])
		}
	})
}

// TestClineChatCompletionsOmitsReasoningLikeTS pins the Cline provider's
// chat-completions body to the TS cline-provider.ts: it sends
// model/messages/stream/stream_options/tools only and NEVER a reasoning_effort or
// thinking field, even for a gpt-5-named (Cline-proxied) model that would
// otherwise match the generic reasoning gate.
func TestClineChatCompletionsOmitsReasoningLikeTS(t *testing.T) {
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&requestBody)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()
	adapter := &OpenAIChatCompletionsAdapter{Provider: Provider{Prefix: "cline", Kind: "cline", APIKey: "k", BaseURL: server.URL, Model: "openai/gpt-5", DefaultReasoningEffort: "high"}}
	stream, err := adapter.Chat(context.Background(), ChatParams{History: &BuiltHistory{History: []any{Message{Role: "user", Content: "hi"}}}})
	if err != nil {
		t.Fatalf("Chat error: %v", err)
	}
	for event := range stream {
		if event.Err != nil {
			t.Fatalf("stream error: %v", event.Err)
		}
	}
	if _, has := requestBody["reasoning_effort"]; has {
		t.Fatalf("Cline must not send reasoning_effort, got %#v", requestBody["reasoning_effort"])
	}
	if _, has := requestBody["thinking"]; has {
		t.Fatalf("Cline must not send thinking, got %#v", requestBody["thinking"])
	}
}

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
