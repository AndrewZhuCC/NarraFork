package agentbasic

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAnthropicCompatibleProviderCleansThinkingBlocksFromHistory(t *testing.T) {
	messages := []map[string]any{{
		"role": "assistant",
		"content": []any{
			map[string]any{"type": "thinking", "thinking": "private"},
			map[string]any{"type": "redacted_thinking", "data": "secret"},
			map[string]any{"type": "text", "text": "visible"},
		},
	}}
	cleaned := cleanAnthropicHistoryForProvider(messages, Provider{Kind: "anthropic", OfficialAPI: false})
	if len(cleaned) != 1 {
		t.Fatalf("expected one cleaned message, got %#v", cleaned)
	}
	content, _ := cleaned[0]["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("expected only visible text block after cleaning, got %#v", content)
	}
	block, _ := content[0].(map[string]any)
	if block["type"] != "text" || block["text"] != "visible" {
		t.Fatalf("unexpected cleaned block: %#v", block)
	}
	preserved := cleanAnthropicHistoryForProvider(messages, Provider{Kind: "anthropic", OfficialAPI: true})
	if len(preserved) != 1 {
		t.Fatalf("expected official API history to be preserved")
	}
	preservedContent, _ := preserved[0]["content"].([]any)
	if len(preservedContent) != 3 {
		t.Fatalf("expected official API thinking blocks to be preserved, got %#v", preservedContent)
	}
}

func TestAnthropicEmitsSilentDisconnectWithoutMessageStop(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial"}}

`)
	}))
	defer server.Close()
	adapter := &AnthropicMessagesAdapter{Provider: Provider{Prefix: "anthropic", Kind: "anthropic", APIKey: "test-key", BaseURL: server.URL, Model: "claude-test"}}
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

func TestAnthropicMessagesStreamsTextThinkingToolAndUsage(t *testing.T) {
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/messages" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("x-api-key") != "test-key" {
			t.Fatalf("missing x-api-key header")
		}
		if r.Header.Get("anthropic-beta") == "" {
			t.Fatalf("expected anthropic beta header when thinking is enabled")
		}
		_ = json.NewDecoder(r.Body).Decode(&requestBody)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `event: message_start
data: {"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":10,"cache_read_input_tokens":2}}}

`)
		fmt.Fprint(w, `event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}

`)
		fmt.Fprint(w, `event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"think"}}

`)
		fmt.Fprint(w, `event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig_1"}}

`)
		fmt.Fprint(w, `event: content_block_start
data: {"type":"content_block_start","index":3,"content_block":{"type":"redacted_thinking","data":"redacted_blob"}}

`)
		fmt.Fprint(w, `event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}

`)
		fmt.Fprint(w, `event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"hello"}}

`)
		fmt.Fprint(w, `event: content_block_start
data: {"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"tool_1","name":"Read","input":{}}}

`)
		fmt.Fprint(w, `event: content_block_delta
data: {"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"file_path\":\"note.txt\"}"}}

`)
		fmt.Fprint(w, `event: content_block_stop
data: {"type":"content_block_stop","index":2}

`)
		fmt.Fprint(w, `event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":5,"cache_creation_input_tokens":3,"cache_creation":{"ephemeral_5m_input_tokens":1,"ephemeral_1h_input_tokens":2}}}

`)
		fmt.Fprint(w, `event: message_stop
data: {"type":"message_stop"}

`)
	}))
	defer server.Close()
	adapter := &AnthropicMessagesAdapter{Provider: Provider{Prefix: "anthropic", Kind: "anthropic", APIKey: "test-key", BaseURL: server.URL, Model: "claude-test", DefaultReasoningEffort: "medium"}}
	stream, err := adapter.Chat(context.Background(), ChatParams{History: &BuiltHistory{History: []any{map[string]any{"role": "user", "content": "hi"}}}, SystemPrompt: "system prompt"})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	seenText := false
	seenThinking := false
	seenStop := false
	seenSignature := false
	seenRedacted := false
	var usage *UsageSnapshot
	for event := range stream {
		if event.Err != nil {
			t.Fatalf("stream error: %v", event.Err)
		}
		if event.Parsed.Text == "hello" && event.Parsed.MessageID == "msg_1" {
			seenText = true
		}
		if event.Parsed.Reasoning == "think" {
			seenThinking = true
		}
		if event.Parsed.ToolUseChunk != nil && event.Parsed.ToolUseChunk.ToolUseID == "tool_1" && event.Parsed.ToolUseChunk.Stop {
			seenStop = true
		}
		if event.Parsed.ReasoningMetadata != nil && event.Parsed.ReasoningMetadata["signature"] == "sig_1" {
			seenSignature = true
		}
		if event.Parsed.RedactedThinking != nil && event.Parsed.RedactedThinking.Data == "redacted_blob" {
			seenRedacted = true
		}
		if event.Parsed.Usage != nil {
			usage = event.Parsed.Usage
		}
	}
	if !seenText || !seenThinking || !seenStop || !seenSignature || !seenRedacted {
		t.Fatalf("missing expected anthropic events text=%v thinking=%v stop=%v signature=%v redacted=%v", seenText, seenThinking, seenStop, seenSignature, seenRedacted)
	}
	systemBlocks, _ := requestBody["system"].([]any)
	if len(systemBlocks) == 0 {
		t.Fatalf("expected system cache_control block in request: %#v", requestBody["system"])
	}
	firstBlock, _ := systemBlocks[0].(map[string]any)
	if firstBlock["cache_control"] == nil {
		t.Fatalf("expected cache_control on system block: %#v", firstBlock)
	}
	if usage == nil || usage.CompletionTokens != 5 || usage.CacheCreationInputTokens != 3 || usage.CacheCreation5mTokens != 1 || usage.CacheCreation1hTokens != 2 {
		t.Fatalf("unexpected usage: %#v", usage)
	}
}

func TestAnthropicMessagesHonorsTLSRejectUnauthorizedFalse(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/messages" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `event: message_start
data: {"type":"message_start","message":{"id":"msg_tls"}}

`)
		fmt.Fprint(w, `event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

`)
		fmt.Fprint(w, `event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"tls ok"}}

`)
		fmt.Fprint(w, `event: message_stop
data: {"type":"message_stop"}

`)
	}))
	defer server.Close()
	tlsRejectUnauthorized := false
	adapter := &AnthropicMessagesAdapter{Provider: Provider{Prefix: "anthropic", Kind: "anthropic", APIKey: "test-key", BaseURL: server.URL, Model: "claude-test", TLSRejectUnauthorized: &tlsRejectUnauthorized}}
	stream, err := adapter.Chat(context.Background(), ChatParams{History: &BuiltHistory{History: []any{map[string]any{"role": "user", "content": "hi"}}}})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	text := ""
	for event := range stream {
		if event.Err != nil {
			t.Fatalf("stream error: %v", event.Err)
		}
		text += event.Parsed.Text
	}
	if text != "tls ok" {
		t.Fatalf("unexpected text %q", text)
	}
}

func TestRunAgentLoopConsumesAnthropicStreamingToolChunks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"tool_read","name":"Read","input":{}}}

`)
		fmt.Fprint(w, `event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"file_path\":\"note.txt\"}"}}

`)
		fmt.Fprint(w, `event: content_block_stop
data: {"type":"content_block_stop","index":0}

`)
		fmt.Fprint(w, `event: message_stop
data: {"type":"message_stop"}

`)
	}))
	defer server.Close()
	adapter := &AnthropicMessagesAdapter{Provider: Provider{Prefix: "anthropic", Kind: "anthropic", APIKey: "test-key", BaseURL: server.URL, Model: "claude-test"}}
	stream := runAgentLoop(
		context.Background(),
		ProviderResolution{Provider: "anthropic", Model: "claude-test", Adapter: adapter},
		[]Message{{Role: "user", Content: "read"}},
		t.TempDir(),
		normalizeGenerateOptions(
			WithMaxTurns(1),
			WithRetryBaseDelay(0),
			WithToolExecutor(func(ctx context.Context, cwd string, call ToolCall) (ToolRun, error) {
				if call.ID != "tool_read" || call.Function.Name != "Read" || !strings.Contains(call.Function.Arguments, "note.txt") {
					t.Fatalf("unexpected tool call: %#v", call)
				}
				return ToolRun{Output: "anthropic tool output"}, nil
			}),
		),
	)
	events := collectEvents(stream)
	if eventTypeCount(events, "tool_call") != 1 || eventTypeCount(events, "tool_result") != 1 {
		t.Fatalf("unexpected events: %#v", eventTypes(events))
	}
}
