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

func TestAnthropicSupportsEffort(t *testing.T) {
	for _, m := range []string{"claude-opus-4-6", "claude-opus-4.6-20251101", "claude-sonnet-4-6", "anthropic:claude-sonnet-4.6"} {
		if !anthropicSupportsEffort(m) {
			t.Fatalf("expected %q to support effort", m)
		}
	}
	for _, m := range []string{"claude-opus-4", "claude-sonnet-4-5", "claude-3-7-sonnet", "deepseek-chat", "gpt-4o"} {
		if anthropicSupportsEffort(m) {
			t.Fatalf("expected %q to NOT support effort", m)
		}
	}
}

func TestMapAnthropicEffortParam(t *testing.T) {
	cases := map[string]string{
		"":       "",
		"none":   "",
		"low":    "low",
		"medium": "medium",
		"high":   "high",
		"xhigh":  "high",
		"max":    "max",
	}
	for in, want := range cases {
		if got := mapAnthropicEffortParam(in); got != want {
			t.Fatalf("mapAnthropicEffortParam(%q) = %q, want %q", in, got, want)
		}
	}
}

// captureAnthropicBody runs a streaming Anthropic request against a fake server
// and returns the decoded request body plus the anthropic-beta header.
func captureAnthropicBody(t *testing.T, model, reasoningEffort string) (map[string]any, string) {
	t.Helper()
	var body map[string]any
	var betaHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		betaHeader = r.Header.Get("anthropic-beta")
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":1}}}\n\n")
		fmt.Fprint(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer server.Close()
	adapter := &AnthropicMessagesAdapter{Provider: Provider{Prefix: "anthropic", Kind: "anthropic", APIKey: "k", BaseURL: server.URL, Model: model}}
	stream, err := adapter.Chat(context.Background(), ChatParams{
		SystemPrompt:    "sys",
		ReasoningEffort: reasoningEffort,
		History:         &BuiltHistory{History: []any{map[string]any{"role": "user", "content": "hi"}}},
		ToolScope:       "normal",
	})
	if err != nil {
		t.Fatalf("Chat error: %v", err)
	}
	for range stream {
	}
	return body, betaHeader
}

func outputConfigEffort(t *testing.T, body map[string]any) (string, bool) {
	t.Helper()
	oc, ok := body["output_config"].(map[string]any)
	if !ok {
		return "", false
	}
	return fmt.Sprintf("%v", oc["effort"]), true
}

// TestAnthropicBodyOutputConfigForEffortModel: an effort-capable model with
// thinking enabled sends output_config.effort, no temperature (thinking on), and
// the effort beta flags (mirrors TS anthropic-provider.ts).
func TestAnthropicBodyOutputConfigForEffortModel(t *testing.T) {
	body, beta := captureAnthropicBody(t, "claude-opus-4-6", "high")
	effort, has := outputConfigEffort(t, body)
	if !has || effort != "high" {
		t.Fatalf("expected output_config.effort=high, got %#v", body["output_config"])
	}
	if _, ok := body["temperature"]; ok {
		t.Fatalf("thinking-enabled body must not send temperature, got %#v", body["temperature"])
	}
	if !strings.Contains(beta, "effort-2025-11-24") || !strings.Contains(beta, "adaptive-thinking-2026-01-28") {
		t.Fatalf("effort-capable model must declare effort beta flags, got %q", beta)
	}
}

// TestAnthropicBodyOutputConfigDefaultsMedium: effort-capable model with no
// explicit effort but thinking enabled defaults output_config.effort to "medium".
func TestAnthropicBodyOutputConfigDefaultsMedium(t *testing.T) {
	body, _ := captureAnthropicBody(t, "claude-sonnet-4.6", "")
	effort, has := outputConfigEffort(t, body)
	if !has || effort != "medium" {
		t.Fatalf("expected output_config.effort=medium default, got %#v", body["output_config"])
	}
}

// TestAnthropicBodyOutputConfigForDeepSeek: a DeepSeek model with thinking enabled
// carries output_config.effort via mapDeepSeekEffort.
func TestAnthropicBodyOutputConfigForDeepSeek(t *testing.T) {
	body, _ := captureAnthropicBody(t, "deepseek-chat", "high")
	effort, has := outputConfigEffort(t, body)
	if !has || effort != "high" {
		t.Fatalf("expected DeepSeek output_config.effort=high, got %#v", body["output_config"])
	}
	if _, ok := body["temperature"]; ok {
		t.Fatalf("thinking-enabled DeepSeek body must not send temperature, got %#v", body["temperature"])
	}
}

// TestAnthropicBodyTemperatureWhenThinkingDisabled: reasoningEffort=none disables
// thinking, so the body sends temperature=1, no output_config, and no anthropic-beta.
func TestAnthropicBodyTemperatureWhenThinkingDisabled(t *testing.T) {
	body, beta := captureAnthropicBody(t, "claude-opus-4-6", "none")
	temp, ok := body["temperature"]
	if !ok {
		t.Fatalf("thinking-disabled body must send temperature, got %#v", body)
	}
	if fmt.Sprintf("%v", temp) != "1" {
		t.Fatalf("expected temperature=1, got %#v", temp)
	}
	if _, has := body["output_config"]; has {
		t.Fatalf("thinking-disabled body must not send output_config, got %#v", body["output_config"])
	}
	if beta != "" {
		t.Fatalf("thinking-disabled body must not send anthropic-beta, got %q", beta)
	}
}

// TestAnthropicBodyNoOutputConfigForNonEffortModel: a thinking-capable but NOT
// effort-capable model (sonnet-4, non-DeepSeek) gets no output_config and no
// temperature (thinking on), and only the interleaved-thinking beta flag.
func TestAnthropicBodyNoOutputConfigForNonEffortModel(t *testing.T) {
	body, beta := captureAnthropicBody(t, "claude-sonnet-4-5", "high")
	if _, has := body["output_config"]; has {
		t.Fatalf("non-effort model must not send output_config, got %#v", body["output_config"])
	}
	if _, ok := body["temperature"]; ok {
		t.Fatalf("thinking-enabled body must not send temperature, got %#v", body["temperature"])
	}
	if strings.Contains(beta, "effort-2025-11-24") {
		t.Fatalf("non-effort model must not declare effort beta flags, got %q", beta)
	}
	if !strings.Contains(beta, "interleaved-thinking-2025-05-14") {
		t.Fatalf("thinking-enabled model must declare interleaved-thinking, got %q", beta)
	}
}
