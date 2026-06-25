package agentbasic

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestAnthropicSystemBlocksGatedOnOfficial verifies cache_control on the system
// block is gated on the official API: official → array block with cache_control;
// proxy → plain text block with no cache_control (mirrors TS isOfficial gating).
func TestAnthropicSystemBlocksGatedOnOfficial(t *testing.T) {
	official := anthropicSystemBlocks("sys", true)
	blocks, ok := official.([]map[string]any)
	if !ok || len(blocks) != 1 || blocks[0]["cache_control"] == nil {
		t.Fatalf("official system block must carry cache_control, got %#v", official)
	}
	proxy := anthropicSystemBlocks("sys", false)
	pblocks, ok := proxy.([]map[string]any)
	if !ok || len(pblocks) != 1 {
		t.Fatalf("proxy system should be a single text block, got %#v", proxy)
	}
	if pblocks[0]["cache_control"] != nil {
		t.Fatalf("proxy system block must NOT carry cache_control, got %#v", pblocks[0])
	}
	if pblocks[0]["text"] != "sys" || pblocks[0]["type"] != "text" {
		t.Fatalf("proxy system block shape mismatch: %#v", pblocks[0])
	}
	// Empty system prompt returns the raw string unchanged.
	if got := anthropicSystemBlocks("", true); got != "" {
		t.Fatalf("empty system prompt should return empty string, got %#v", got)
	}
}

// TestApplyAnthropicCacheBreakpointsPlacesToolAndMessageBreakpoints verifies the
// last tool definition and the last non-thinking content block of the history
// prefix (skipping the current user turn) get cache_control, mirroring TS
// applyCacheBreakpoints.
func TestApplyAnthropicCacheBreakpointsPlacesToolAndMessageBreakpoints(t *testing.T) {
	messages := []map[string]any{
		// History prefix message (assistant) with a thinking block last → the
		// breakpoint must land on the earlier text block, skipping thinking.
		{"role": "assistant", "content": []any{
			map[string]any{"type": "text", "text": "prior answer"},
			map[string]any{"type": "thinking", "thinking": "secret"},
		}},
		// Current user turn (last message) — must NOT get a breakpoint.
		{"role": "user", "content": []any{
			map[string]any{"type": "text", "text": "current question"},
		}},
	}
	tools := []map[string]any{
		{"name": "Read"},
		{"name": "Bash"},
	}
	applyAnthropicCacheBreakpoints(messages, tools)

	// Last tool has cache_control; earlier tool does not.
	if tools[1]["cache_control"] == nil {
		t.Fatalf("last tool must carry cache_control, got %#v", tools[1])
	}
	if tools[0]["cache_control"] != nil {
		t.Fatalf("non-last tool must not carry cache_control, got %#v", tools[0])
	}
	// History-prefix message: breakpoint on the text block (index 0), NOT the
	// trailing thinking block (index 1).
	prefixBlocks := messages[0]["content"].([]any)
	textBlock := prefixBlocks[0].(map[string]any)
	thinkingBlock := prefixBlocks[1].(map[string]any)
	if textBlock["cache_control"] == nil {
		t.Fatalf("history-prefix text block must carry cache_control, got %#v", textBlock)
	}
	if thinkingBlock["cache_control"] != nil {
		t.Fatalf("thinking block must be skipped for cache_control, got %#v", thinkingBlock)
	}
	// Current user turn must not be marked.
	currentBlocks := messages[1]["content"].([]any)
	if currentBlocks[0].(map[string]any)["cache_control"] != nil {
		t.Fatalf("current user turn must not carry cache_control, got %#v", currentBlocks[0])
	}
}

// TestApplyAnthropicCacheBreakpointsStripsExisting verifies pre-existing
// cache_control on message blocks is stripped before re-placing per the rules.
func TestApplyAnthropicCacheBreakpointsStripsExisting(t *testing.T) {
	messages := []map[string]any{
		{"role": "user", "content": []any{
			map[string]any{"type": "text", "text": "old", "cache_control": map[string]any{"type": "ephemeral"}},
		}},
		{"role": "assistant", "content": []any{
			map[string]any{"type": "text", "text": "a", "cache_control": map[string]any{"type": "ephemeral"}},
		}},
		{"role": "user", "content": []any{
			map[string]any{"type": "text", "text": "current"},
		}},
	}
	applyAnthropicCacheBreakpoints(messages, nil)
	// messages[0] is part of the prefix but not the chosen breakpoint (only one is
	// placed, on messages[1]); its stale cache_control must have been stripped.
	if messages[0]["content"].([]any)[0].(map[string]any)["cache_control"] != nil {
		t.Fatalf("stale cache_control on messages[0] must be stripped, got %#v", messages[0])
	}
	// messages[1] is the chosen prefix breakpoint (last before current turn).
	if messages[1]["content"].([]any)[0].(map[string]any)["cache_control"] == nil {
		t.Fatalf("messages[1] should be the placed breakpoint, got %#v", messages[1])
	}
}

// TestStreamAnthropicCacheControlGatedByOfficial drives streamAnthropicMessages
// against a scripted server and asserts proxy mode sends no cache_control
// anywhere (system/messages/tools) while official mode does.
func TestStreamAnthropicCacheControlGatedByOfficial(t *testing.T) {
	for _, tc := range []struct {
		name     string
		official bool
	}{
		{"proxy", false},
		{"official", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewDecoder(r.Body).Decode(&body)
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":1}}}\n\n")
				fmt.Fprint(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
			}))
			defer server.Close()
			adapter := &AnthropicMessagesAdapter{Provider: Provider{Prefix: "anthropic", Kind: "anthropic", APIKey: "k", BaseURL: server.URL, Model: "claude-test", OfficialAPI: tc.official}}
			stream, err := adapter.Chat(context.Background(), ChatParams{SystemPrompt: "sys", History: &BuiltHistory{History: []any{
				map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "prior"}}},
				map[string]any{"role": "user", "content": "current"},
			}}, ToolScope: "normal"})
			if err != nil {
				t.Fatalf("Chat error: %v", err)
			}
			for range stream {
			}

			systemHasCacheControl := false
			if blocks, ok := body["system"].([]any); ok && len(blocks) > 0 {
				if b, ok := blocks[0].(map[string]any); ok && b["cache_control"] != nil {
					systemHasCacheControl = true
				}
			}
			toolsHaveCacheControl := false
			if tools, ok := body["tools"].([]any); ok {
				for _, raw := range tools {
					if tm, ok := raw.(map[string]any); ok && tm["cache_control"] != nil {
						toolsHaveCacheControl = true
					}
				}
			}
			if tc.official {
				if !systemHasCacheControl {
					t.Fatalf("official mode must set system cache_control: %#v", body["system"])
				}
				if !toolsHaveCacheControl {
					t.Fatalf("official mode must set a tool cache_control breakpoint: %#v", body["tools"])
				}
			} else {
				if systemHasCacheControl {
					t.Fatalf("proxy mode must not set system cache_control: %#v", body["system"])
				}
				if toolsHaveCacheControl {
					t.Fatalf("proxy mode must not set tool cache_control: %#v", body["tools"])
				}
			}
		})
	}
}
