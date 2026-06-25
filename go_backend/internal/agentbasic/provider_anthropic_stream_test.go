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
	adapter := &AnthropicMessagesAdapter{Provider: Provider{Prefix: "anthropic", Kind: "anthropic", APIKey: "test-key", BaseURL: server.URL, Model: "claude-opus-4-6", DefaultReasoningEffort: "medium"}}
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
	// Proxy mode (OfficialAPI=false): the system block is a plain text block with
	// NO cache_control, mirroring TS which only applies cache_control on the
	// official API. The cache_control-present (official) case is covered by
	// TestAnthropicSystemBlocksGatedOnOfficial and the TS-parity fixture tests.
	systemBlocks, _ := requestBody["system"].([]any)
	if len(systemBlocks) == 0 {
		t.Fatalf("expected system block in request: %#v", requestBody["system"])
	}
	firstBlock, _ := systemBlocks[0].(map[string]any)
	if firstBlock["cache_control"] != nil {
		t.Fatalf("proxy-mode system block must not carry cache_control: %#v", firstBlock)
	}
	if usage == nil || usage.CompletionTokens != 5 || usage.CacheCreationInputTokens != 3 || usage.CacheCreation5mTokens != 1 || usage.CacheCreation1hTokens != 2 {
		t.Fatalf("unexpected usage: %#v", usage)
	}
	// The prompt footprint from message_start (input_tokens / cache_read) must
	// survive the message_delta (which omits them), matching the TS usage
	// accumulator. Previously Go emitted the raw per-event snapshot, so the final
	// (message_delta) usage reported InputTokens=0 / CachedInputTokens=0.
	// PromptTokens is the full prompt footprint (input + cache_read +
	// cache_creation = 10 + 2 + 3 = 15), mirroring TS anthropicPromptFootprintTokens.
	if usage.InputTokens != 10 || usage.PromptTokens != 15 || usage.CachedInputTokens != 2 {
		t.Fatalf("message_start prompt footprint must persist through message_delta, got InputTokens=%d PromptTokens=%d CachedInputTokens=%d", usage.InputTokens, usage.PromptTokens, usage.CachedInputTokens)
	}
	// max_tokens on the main streaming chat must be 32000 (TS getTokenLimits
	// default), not the legacy hardcoded 4096 that truncated long responses.
	if mt, _ := requestBody["max_tokens"].(float64); mt != 32000 {
		t.Fatalf("expected max_tokens 32000 on streaming chat, got %#v", requestBody["max_tokens"])
	}
}

// finalAnthropicUsageForSSE runs an Anthropic streaming Chat against a server that
// returns the given SSE body and returns the last usage snapshot observed.
func finalAnthropicUsageForSSE(t *testing.T, sseBody string) *UsageSnapshot {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sseBody)
	}))
	defer server.Close()
	adapter := &AnthropicMessagesAdapter{Provider: Provider{Prefix: "anthropic", Kind: "anthropic", APIKey: "test-key", BaseURL: server.URL, Model: "claude-opus-4-6"}}
	stream, err := adapter.Chat(context.Background(), ChatParams{History: &BuiltHistory{History: []any{map[string]any{"role": "user", "content": "hi"}}}})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	var usage *UsageSnapshot
	for event := range stream {
		if event.Err != nil {
			t.Fatalf("stream error: %v", event.Err)
		}
		if event.Parsed.Usage != nil {
			usage = event.Parsed.Usage
		}
	}
	if usage == nil {
		t.Fatalf("expected a usage snapshot")
	}
	return usage
}

// TestAnthropicUsageDerivesCacheCreationFrom5mAnd1h verifies that when the
// top-level cache_creation_input_tokens is absent but the 5m/1h breakdown is
// present, the cache-creation total is derived from 5m+1h (mirrors TS).
func TestAnthropicUsageDerivesCacheCreationFrom5mAnd1h(t *testing.T) {
	usage := finalAnthropicUsageForSSE(t, `event: message_start
data: {"type":"message_start","message":{"id":"m","usage":{"input_tokens":20}}}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":7,"cache_creation":{"ephemeral_5m_input_tokens":4,"ephemeral_1h_input_tokens":6}}}

event: message_stop
data: {"type":"message_stop"}

`)
	if usage.CacheCreation5mTokens != 4 || usage.CacheCreation1hTokens != 6 {
		t.Fatalf("expected 5m=4 1h=6, got %#v", usage)
	}
	if usage.CacheCreationInputTokens != 10 {
		t.Fatalf("cacheCreationInputTokens should be derived as 5m+1h=10, got %d", usage.CacheCreationInputTokens)
	}
	if usage.InputTokens != 20 || usage.CompletionTokens != 7 {
		t.Fatalf("unexpected input/output tokens: %#v", usage)
	}
}

// TestAnthropicUsageMessageDeltaZeroInputDoesNotEraseStart verifies that an
// explicit input_tokens:0 in message_delta does not erase the message_start value.
func TestAnthropicUsageMessageDeltaZeroInputDoesNotEraseStart(t *testing.T) {
	usage := finalAnthropicUsageForSSE(t, `event: message_start
data: {"type":"message_start","message":{"id":"m","usage":{"input_tokens":100,"cache_read_input_tokens":40}}}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":0,"cache_read_input_tokens":0,"output_tokens":12}}

event: message_stop
data: {"type":"message_stop"}

`)
	if usage.InputTokens != 100 || usage.CachedInputTokens != 40 {
		t.Fatalf("message_delta zeroes must not erase message_start footprint, got InputTokens=%d CachedInputTokens=%d", usage.InputTokens, usage.CachedInputTokens)
	}
	if usage.CompletionTokens != 12 {
		t.Fatalf("expected output_tokens=12, got %d", usage.CompletionTokens)
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
