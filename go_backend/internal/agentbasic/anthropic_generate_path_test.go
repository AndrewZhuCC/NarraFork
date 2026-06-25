package agentbasic

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestAnthropicGenerateUsesStreamingAgentLoopWith32000 pins the W800 architectural
// divergence: Go has NO dedicated lightweight non-streaming Anthropic generate
// path (the dead doAnthropicRequest helper with max_tokens:4096 was removed).
// Instead, agentbasic.Generate (used by title/summary helpers) reuses the full
// agent loop, so an Anthropic generate request flows through streamAnthropicMessages
// and sends max_tokens:32000 with readonly tools — diverging from TS, whose
// generateWithMeta posts /messages with max_tokens:4096 and no tools.
//
// This test drives the real agentbasic.Generate entrypoint through a config.Store
// with an Anthropic provider pointed at an httptest server, and asserts the
// captured request body uses the streaming-loop shape (32000 + tools), not the
// removed 4096 helper. If a future change reintroduces a 4096 non-streaming
// generate path, this guard fails and forces an explicit contract update.
func TestAnthropicGenerateUsesStreamingAgentLoopWith32000(t *testing.T) {
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&requestBody)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m1\",\"usage\":{\"input_tokens\":5}}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"OK\"}}\n\n")
		fmt.Fprint(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\n")
		fmt.Fprint(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer server.Close()

	store := testConfigStore(t, map[string]any{
		"anthropicProviders": []any{
			map[string]any{
				"prefix":       "anthropic",
				"apiKey":       "test-key",
				"baseUrl":      server.URL,
				"defaultModel": "claude-test",
			},
		},
	})

	result, err := Generate(
		context.Background(),
		store,
		"anthropic:claude-test",
		[]Message{{Role: "user", Content: "Reply with OK only."}},
		t.TempDir(),
		WithMaxTurns(1),
		WithMaxChatRetries(0),
		WithEmptyResponseRetries(0),
		WithRetryBaseDelay(0),
		WithToolScope("readonly"),
	)
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if result.Text != "OK" {
		t.Fatalf("unexpected generate text %q", result.Text)
	}
	if requestBody == nil {
		t.Fatal("server did not capture a request body")
	}
	if mt, _ := requestBody["max_tokens"].(float64); mt != 32000 {
		t.Fatalf("Anthropic generate must use the streaming agent-loop max_tokens 32000 (no 4096 lightweight path), got %#v", requestBody["max_tokens"])
	}
	if _, hasTools := requestBody["tools"]; !hasTools {
		t.Fatalf("Anthropic generate via agent loop should carry tools (readonly scope), got body keys %v", mapKeys(requestBody))
	}
	if _, streaming := requestBody["stream"]; !streaming {
		t.Fatalf("Anthropic generate should use the streaming request body, got body keys %v", mapKeys(requestBody))
	}
}

func mapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
