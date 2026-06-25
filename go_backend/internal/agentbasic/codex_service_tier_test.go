package agentbasic

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// runServiceTierBody drives the OpenAI/codex adapter against a scripted server
// and returns the captured request body, used to assert service_tier presence.
func runServiceTierBody(t *testing.T, provider Provider, serviceTier string) map[string]any {
	t.Helper()
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&requestBody)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"type":"response.completed","response":{"id":"resp_1"}}

`)
	}))
	defer server.Close()
	provider.BaseURL = server.URL
	adapter := &OpenAIChatCompletionsAdapter{Provider: provider}
	stream, err := adapter.Chat(context.Background(), ChatParams{
		History:     &BuiltHistory{History: []any{Message{Role: "user", Content: "hi"}}},
		ServiceTier: serviceTier,
	})
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

// TestCodexResponsesBodyIncludesServiceTierWhenSet verifies a Kind=="codex"
// provider emits service_tier in the request body when fastMode set it (mirrors
// TS codex priority processing).
func TestCodexResponsesBodyIncludesServiceTierWhenSet(t *testing.T) {
	body := runServiceTierBody(t, Provider{Prefix: "codex", Kind: "codex", APIKey: "k", Model: "gpt-5-codex", APIMode: "codex"}, "priority")
	if body["service_tier"] != "priority" {
		t.Fatalf("codex (Kind) body should include service_tier=priority, got %#v", body["service_tier"])
	}
}

// TestCodexApiModeBodyIncludesServiceTier verifies a provider with APIMode=="codex"
// but Kind!="codex" (routed through the else→responses/codex branch) ALSO emits
// service_tier — the second codex-capable path that must thread ServiceTier.
func TestCodexApiModeBodyIncludesServiceTier(t *testing.T) {
	body := runServiceTierBody(t, Provider{Prefix: "openai", Kind: "openai", APIKey: "k", Model: "gpt-5-codex", APIMode: "codex"}, "priority")
	if body["service_tier"] != "priority" {
		t.Fatalf("apiMode=codex body should include service_tier=priority, got %#v", body["service_tier"])
	}
}

// TestCodexResponsesBodyOmitsServiceTierWhenEmpty verifies no service_tier key is
// sent when fastMode is off (empty serviceTier).
func TestCodexResponsesBodyOmitsServiceTierWhenEmpty(t *testing.T) {
	body := runServiceTierBody(t, Provider{Prefix: "codex", Kind: "codex", APIKey: "k", Model: "gpt-5-codex", APIMode: "codex"}, "")
	if _, ok := body["service_tier"]; ok {
		t.Fatalf("body must omit service_tier when empty, got %#v", body["service_tier"])
	}
}

// TestNonCodexResponsesBodyOmitsServiceTier verifies a non-codex responses
// provider does NOT emit service_tier even when ServiceTier is set (body-level
// gating to codex only, matching TS which writes service_tier only in codex bodies).
func TestNonCodexResponsesBodyOmitsServiceTier(t *testing.T) {
	body := runServiceTierBody(t, Provider{Prefix: "openai", Kind: "openai", APIKey: "k", Model: "gpt-4o", APIMode: "responses"}, "priority")
	if _, ok := body["service_tier"]; ok {
		t.Fatalf("non-codex responses body must not emit service_tier, got %#v", body["service_tier"])
	}
}
