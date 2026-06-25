package agentbasic

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestAnthropicOfficialAppendsNativeWebSearchTool verifies the official Anthropic
// API attaches the server-side web_search_20250305 tool + tool_choice:auto when
// the scope exposes WebSearch, and that proxy (non-official) mode does NOT
// (mirrors TS anthropic-provider.ts isOfficial && shouldUseNativeSearch gating).
func TestAnthropicOfficialAppendsNativeWebSearchTool(t *testing.T) {
	run := func(t *testing.T, official bool, scope string) map[string]any {
		t.Helper()
		var body map[string]any
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&body)
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"type\":\"message_stop\"}\n\n")
		}))
		defer server.Close()
		provider := Provider{Prefix: "anthropic", Kind: "anthropic", APIKey: "k", BaseURL: server.URL, Model: "claude-sonnet-4", OfficialAPI: official}
		out := make(chan StreamEvent, 8)
		err := streamAnthropicMessages(context.Background(), providerHTTPClient(provider), provider, "system", []map[string]any{{"role": "user", "content": "hi"}}, scope, "", out)
		close(out)
		if err != nil {
			t.Fatalf("stream error: %v", err)
		}
		return body
	}

	hasNativeWebSearch := func(body map[string]any) bool {
		tools, _ := body["tools"].([]any)
		for _, raw := range tools {
			tool, _ := raw.(map[string]any)
			if tool["type"] == "web_search_20250305" && tool["name"] == "web_search" {
				return true
			}
		}
		return false
	}

	t.Run("official + normal scope attaches native web_search", func(t *testing.T) {
		body := run(t, true, "normal")
		if !hasNativeWebSearch(body) {
			t.Fatalf("official mode must attach native web_search tool, got %#v", body["tools"])
		}
		if choice, _ := body["tool_choice"].(map[string]any); choice["type"] != "auto" {
			t.Fatalf("official native web_search must set tool_choice auto, got %#v", body["tool_choice"])
		}
	})

	t.Run("proxy mode never attaches native web_search", func(t *testing.T) {
		body := run(t, false, "normal")
		if hasNativeWebSearch(body) {
			t.Fatalf("proxy mode must not attach native web_search, got %#v", body["tools"])
		}
	})

	t.Run("reflection scope (no WebSearch) skips native web_search even when official", func(t *testing.T) {
		body := run(t, true, "reflection")
		if hasNativeWebSearch(body) {
			t.Fatalf("reflection scope must not attach native web_search, got %#v", body["tools"])
		}
	})
}

// TestAnthropicNativeWebSearchStreamLifecycle verifies the streaming
// server_tool_use → searching → web_search_tool_result lifecycle emits
// in_progress/searching/completed WebSearch events with the extracted query, and
// that the server tool is NOT surfaced as a local tool-call stop chunk.
func TestAnthropicNativeWebSearchStreamLifecycle(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\"}}\n\n")
		fmt.Fprint(w, "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"server_tool_use\",\"id\":\"srv_1\",\"name\":\"web_search\"}}\n\n")
		fmt.Fprint(w, "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"query\\\":\\\"golang\\\"}\"}}\n\n")
		fmt.Fprint(w, "data: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
		fmt.Fprint(w, "data: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"web_search_tool_result\",\"tool_use_id\":\"srv_1\"}}\n\n")
		fmt.Fprint(w, "data: {\"type\":\"message_stop\"}\n\n")
	}))
	defer server.Close()
	provider := Provider{Prefix: "anthropic", Kind: "anthropic", APIKey: "k", BaseURL: server.URL, Model: "claude-sonnet-4", OfficialAPI: true}
	out := make(chan StreamEvent, 32)
	go func() {
		_ = streamAnthropicMessages(context.Background(), providerHTTPClient(provider), provider, "system", []map[string]any{{"role": "user", "content": "hi"}}, "normal", "", out)
		close(out)
	}()

	statuses := []string{}
	var query string
	sawServerToolStop := false
	for event := range out {
		if event.Err != nil {
			t.Fatalf("stream error: %v", event.Err)
		}
		if ws := event.Parsed.WebSearch; ws != nil {
			statuses = append(statuses, ws.Status)
			if ws.Query != "" {
				query = ws.Query
			}
		}
		if chunk := event.Parsed.ToolUseChunk; chunk != nil && chunk.ToolUseID == "srv_1" {
			sawServerToolStop = true
		}
	}
	if len(statuses) < 3 || statuses[0] != "in_progress" {
		t.Fatalf("expected in_progress/searching/completed lifecycle, got %#v", statuses)
	}
	if statuses[len(statuses)-1] != "completed" {
		t.Fatalf("expected final completed status, got %#v", statuses)
	}
	if query != "golang" {
		t.Fatalf("expected extracted query 'golang', got %q", query)
	}
	if sawServerToolStop {
		t.Fatalf("server_tool_use must NOT be surfaced as a local tool-call chunk")
	}
}
