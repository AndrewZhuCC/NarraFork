package agentbasic

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// collectAnthropicRelayStream drives streamAnthropicMessages through an httptest
// SSE server returning the given event lines and returns the parsed reasoning and
// visible-text strings seen on the stream.
func collectAnthropicRelayStream(t *testing.T, sse string) (reasoning []string, text []string) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sse)
	}))
	t.Cleanup(server.Close)
	adapter := &AnthropicMessagesAdapter{Provider: Provider{Prefix: "anthropic", Kind: "anthropic", APIKey: "test-key", BaseURL: server.URL, Model: "claude-opus-4-6", DefaultReasoningEffort: "medium"}}
	stream, err := adapter.Chat(context.Background(), ChatParams{History: &BuiltHistory{History: []any{map[string]any{"role": "user", "content": "hi"}}}, SystemPrompt: "system prompt"})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	for event := range stream {
		if event.Err != nil {
			t.Fatalf("stream error: %v", event.Err)
		}
		if event.Parsed.Reasoning != "" {
			reasoning = append(reasoning, event.Parsed.Reasoning)
		}
		if event.Parsed.Text != "" {
			text = append(text, event.Parsed.Text)
		}
	}
	return reasoning, text
}

func containsString(items []string, want string) bool {
	for _, s := range items {
		if s == want {
			return true
		}
	}
	return false
}

// TestAnthropicThinkingBlockRelayViaTextDelta pins the relay parity: an
// Anthropic-compatible relay (e.g. DeepSeek over the Anthropic protocol) opens a
// thinking block carrying initial text on content_block_start, then streams more
// thinking via text_delta. Go must route the initial text AND the subsequent
// text_delta to the reasoning channel, not visible assistant text, while a normal
// text block's text_delta still becomes visible text (mirrors TS
// anthropic-provider.ts:1644-1688).
func TestAnthropicThinkingBlockRelayViaTextDelta(t *testing.T) {
	sse := `event: message_start
data: {"type":"message_start","message":{"id":"msg_relay","usage":{"input_tokens":5}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"init"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"more thinking"}}

event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"visible answer"}}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"}}

event: message_stop
data: {"type":"message_stop"}

`
	reasoning, text := collectAnthropicRelayStream(t, sse)
	if !containsString(reasoning, "init") {
		t.Fatalf("expected initial thinking 'init' in reasoning, got %#v", reasoning)
	}
	if !containsString(reasoning, "more thinking") {
		t.Fatalf("expected relayed text_delta 'more thinking' routed to reasoning, got %#v", reasoning)
	}
	if !containsString(text, "visible answer") {
		t.Fatalf("expected normal text block 'visible answer' as visible text, got %#v", text)
	}
	if containsString(text, "more thinking") {
		t.Fatalf("relayed thinking must NOT appear as visible text, got %#v", text)
	}
}

// TestAnthropicThinkingBlockRelayViaReasoningContent verifies a relay that streams
// thinking via a reasoning_content delta (with no explicit delta.type) on a
// registered thinking block is routed to the reasoning channel — exercising the
// thinking pre-check that fires even when delta.type is empty.
func TestAnthropicThinkingBlockRelayViaReasoningContent(t *testing.T) {
	sse := `event: message_start
data: {"type":"message_start","message":{"id":"msg_rc","usage":{"input_tokens":5}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"reasoning_content":"rc thinking"}}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"}}

event: message_stop
data: {"type":"message_stop"}

`
	reasoning, text := collectAnthropicRelayStream(t, sse)
	if !containsString(reasoning, "rc thinking") {
		t.Fatalf("expected reasoning_content delta routed to reasoning, got %#v", reasoning)
	}
	if containsString(text, "rc thinking") {
		t.Fatalf("reasoning_content must NOT appear as visible text, got %#v", text)
	}
}

// TestAnthropicOfficialThinkingDeltaStillReasoning is the zero-regression control:
// the official Anthropic thinking_delta path still routes to reasoning.
func TestAnthropicOfficialThinkingDeltaStillReasoning(t *testing.T) {
	sse := `event: message_start
data: {"type":"message_start","message":{"id":"msg_off","usage":{"input_tokens":5}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"official"}}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"}}

event: message_stop
data: {"type":"message_stop"}

`
	reasoning, text := collectAnthropicRelayStream(t, sse)
	if !containsString(reasoning, "official") {
		t.Fatalf("official thinking_delta must still route to reasoning, got %#v", reasoning)
	}
	if containsString(text, "official") {
		t.Fatalf("official thinking_delta must not be visible text, got %#v", text)
	}
}
