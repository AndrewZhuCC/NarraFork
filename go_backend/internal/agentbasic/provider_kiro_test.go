package agentbasic

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestResolveKiroProviderUsesConfiguredRuntimeAdapter(t *testing.T) {
	store := testConfigStore(t, map[string]any{
		"kiroProviders": []any{map[string]any{
			"apiKey":       "kiro-token",
			"baseUrl":      "https://kiro.example.invalid",
			"defaultModel": "claude-sonnet-4.5",
		}},
	})
	resolution, err := ResolveProviderAndModel(store, "kiro:claude-sonnet-4.5")
	if err != nil {
		t.Fatalf("ResolveProviderAndModel returned error: %v", err)
	}
	if resolution.Config.Kind != "kiro" || resolution.Config.APIKey != "kiro-token" {
		t.Fatalf("unexpected kiro resolution: %#v", resolution.Config)
	}
	if _, ok := resolution.Adapter.(*KiroProviderAdapter); !ok {
		t.Fatalf("expected KiroProviderAdapter, got %T", resolution.Adapter)
	}
}

func TestDefaultKiroBridgeScriptPathResolvesFromPackageWorkingDirectory(t *testing.T) {
	path := defaultKiroBridgeScriptPath()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("default Kiro bridge script path should exist from package cwd: %s err=%v", path, err)
	}
}

func TestKiroProviderUsesLocalPluginBridgeWhenConfigured(t *testing.T) {
	bridge := filepath.Join(t.TempDir(), "kiro-bridge")
	script := "#!/bin/sh\ncat >/dev/null\nprintf '%s\n' '{\"eventType\":\"assistantResponseEvent\",\"data\":{\"content\":\"plugin ok\",\"messageId\":\"msg_plugin\"}}'\n"
	if err := os.WriteFile(bridge, []byte(script), 0o700); err != nil {
		t.Fatalf("write bridge: %v", err)
	}
	t.Setenv("NARRAFORK_KIRO_BRIDGE_CMD", bridge)
	store := testConfigStore(t, map[string]any{
		"kiro": map[string]any{"credentialsPath": "/tmp/kiro-creds.json", "configPath": "/tmp/kiro-config.json", "defaultModel": "claude-sonnet-4.5"},
	})
	resolution, err := ResolveProviderAndModel(store, "kiro:claude-sonnet-4.5")
	if err != nil {
		t.Fatalf("ResolveProviderAndModel returned error: %v", err)
	}
	if resolution.Config.BaseURL != "kiro-plugin" {
		t.Fatalf("expected kiro plugin bridge provider, got %#v", resolution.Config)
	}
	stream, err := resolution.Adapter.Chat(context.Background(), ChatParams{History: &BuiltHistory{TrailingUserText: "hi"}})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	text := ""
	for event := range stream {
		if event.Err != nil {
			t.Fatalf("bridge stream error: %v", event.Err)
		}
		text += event.Parsed.Text
	}
	if text != "plugin ok" {
		t.Fatalf("unexpected plugin bridge text: %q", text)
	}
}

func TestKiroBridgeScriptSmokeWithRealPlugin(t *testing.T) {
	if os.Getenv("NARRAFORK_RUN_KIRO_PLUGIN_SMOKE") != "1" {
		t.Skip("set NARRAFORK_RUN_KIRO_PLUGIN_SMOKE=1 with real Kiro credentials to run plugin smoke test")
	}
	credentialsPath := os.Getenv("NARRAFORK_KIRO_CREDENTIALS_PATH")
	configPath := os.Getenv("NARRAFORK_KIRO_CONFIG_PATH")
	if credentialsPath == "" || configPath == "" {
		t.Skip("NARRAFORK_KIRO_CREDENTIALS_PATH and NARRAFORK_KIRO_CONFIG_PATH are required")
	}
	t.Setenv("NARRAFORK_KIRO_BRIDGE_DIAGNOSTICS", "1")
	adapter := &KiroProviderAdapter{Provider: Provider{Prefix: "kiro", Kind: "kiro", BaseURL: "kiro-plugin", Model: firstNonEmpty(os.Getenv("NARRAFORK_KIRO_DEFAULT_MODEL"), "claude-sonnet-4.5"), KiroCredentialsPath: credentialsPath, KiroConfigPath: configPath}}
	stream, err := adapter.Chat(context.Background(), ChatParams{History: &BuiltHistory{TrailingUserText: "Say OK only."}, Cwd: t.TempDir()})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	text := ""
	for event := range stream {
		if event.Err != nil {
			t.Fatalf("plugin bridge stream error: %v", event.Err)
		}
		text += event.Parsed.Text
	}
	if strings.TrimSpace(text) == "" {
		t.Fatalf("expected non-empty response from real Kiro plugin")
	}
}

func TestKiroProviderStreamsTextToolUsageAndRequestShape(t *testing.T) {
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/kiro/chat" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer kiro-token" {
			t.Fatalf("missing authorization header: %q", r.Header.Get("Authorization"))
		}
		_ = json.NewDecoder(r.Body).Decode(&requestBody)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `event: assistantResponseEvent
data: {"content":"hello","messageId":"msg_1","conversationId":"conv_1","usage":{"inputTokens":4,"outputTokens":2}}

`)
		fmt.Fprint(w, `event: reasoningContentEvent
data: {"content":"think"}

`)
		fmt.Fprint(w, `event: toolUseEvent
data: {"toolUseId":"tool_1","name":"Read","input":"{\"file_path\":\"note.txt\"}"}

`)
		fmt.Fprint(w, `event: toolUseEvent
data: {"toolUseId":"tool_1","name":"Read","stop":true}

`)
		fmt.Fprint(w, `event: contextUsageEvent
data: {"contextUsagePercentage":12.5}

`)
	}))
	defer server.Close()
	adapter := &KiroProviderAdapter{Provider: Provider{Prefix: "kiro", Kind: "kiro", APIKey: "kiro-token", BaseURL: server.URL, Model: "claude-sonnet-4.5"}}
	stream, err := adapter.Chat(context.Background(), ChatParams{History: &BuiltHistory{TrailingUserText: "hi"}, Cwd: "/tmp/work", Tools: adapter.FormatTools("normal")})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	seenText := false
	seenReasoning := false
	seenStop := false
	seenContext := false
	var usage *UsageSnapshot
	for event := range stream {
		if event.Err != nil {
			t.Fatalf("stream error: %v", event.Err)
		}
		if event.Parsed.Text == "hello" && event.Parsed.MessageID == "msg_1" && event.Parsed.ConversationID == "conv_1" {
			seenText = true
		}
		if event.Parsed.Reasoning == "think" {
			seenReasoning = true
		}
		if event.Parsed.ToolUseChunk != nil && event.Parsed.ToolUseChunk.ToolUseID == "tool_1" && event.Parsed.ToolUseChunk.Stop {
			seenStop = true
		}
		if event.Parsed.ContextUsagePercentage != nil && *event.Parsed.ContextUsagePercentage == 12.5 {
			seenContext = true
		}
		if event.Parsed.Usage != nil {
			usage = event.Parsed.Usage
		}
	}
	if !seenText || !seenReasoning || !seenStop || !seenContext {
		t.Fatalf("missing kiro events text=%v reasoning=%v stop=%v context=%v", seenText, seenReasoning, seenStop, seenContext)
	}
	if usage == nil || usage.InputTokens != 4 || usage.CompletionTokens != 2 {
		t.Fatalf("unexpected usage: %#v", usage)
	}
	state, _ := requestBody["conversationState"].(map[string]any)
	current, _ := state["currentMessage"].(map[string]any)
	user, _ := current["userInputMessage"].(map[string]any)
	if user["content"] != "hi" || user["modelId"] != "claude-sonnet-4.5" {
		t.Fatalf("unexpected kiro current message: %#v", user)
	}
	ctx, _ := user["userInputMessageContext"].(map[string]any)
	if _, ok := ctx["tools"].([]any); !ok {
		t.Fatalf("expected kiro tools in request context: %#v", ctx)
	}
}

func TestKiroRequestUsesNonEmptyConversationIDAndEmptyToolArray(t *testing.T) {
	request := buildKiroChatRequest("claude-sonnet-4.5", ChatParams{History: &BuiltHistory{TrailingUserText: "hi"}})
	state, _ := request["conversationState"].(map[string]any)
	conversationID := strings.TrimSpace(stringValue(state["conversationId"]))
	if conversationID == "" || strings.TrimSpace(stringValue(state["agentContinuationId"])) == "" {
		t.Fatalf("expected generated conversation identifiers, got %#v", state)
	}
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(conversationID) {
		t.Fatalf("expected generated Kiro conversation id to be UUID v4, got %q", conversationID)
	}
	current, _ := state["currentMessage"].(map[string]any)
	user, _ := current["userInputMessage"].(map[string]any)
	ctx, _ := user["userInputMessageContext"].(map[string]any)
	tools, ok := ctx["tools"].([]map[string]any)
	if !ok || len(tools) != 0 {
		t.Fatalf("expected empty tools array instead of null, got %#v", ctx["tools"])
	}
}

func TestNormalizeKiroChatRequestFiltersStaleToolResults(t *testing.T) {
	request := map[string]any{"conversationState": map[string]any{
		"history": []any{
			map[string]any{"assistantResponseMessage": map[string]any{"content": "", "toolUses": []any{
				map[string]any{"toolUseId": "tool_keep", "name": "Read", "input": map[string]any{}},
				map[string]any{"toolUseId": "tool_drop", "name": "Read", "input": map[string]any{}},
			}}},
			map[string]any{"userInputMessage": map[string]any{"content": "", "modelId": "claude-sonnet-4.5", "userInputMessageContext": map[string]any{"toolResults": []any{
				map[string]any{"toolUseId": "tool_keep", "content": []any{map[string]any{"text": "ok"}}},
				map[string]any{"toolUseId": "stale", "content": []any{map[string]any{"text": "old"}}},
			}}}},
			map[string]any{"assistantResponseMessage": map[string]any{}},
		},
		"currentMessage": map[string]any{"userInputMessage": map[string]any{"content": "next", "modelId": "claude-sonnet-4.5", "userInputMessageContext": map[string]any{"tools": []any{}}}},
	}}
	normalized := normalizeKiroChatRequest(request)
	state, _ := normalized["conversationState"].(map[string]any)
	history := readArray(state["history"])
	if len(history) != 3 {
		t.Fatalf("expected assistant/tool-result pair plus synthetic current separator, got %#v", history)
	}
	assistant, _ := history[0].(map[string]any)
	assistantBody, _ := assistant["assistantResponseMessage"].(map[string]any)
	toolUses := readArray(assistantBody["toolUses"])
	if len(toolUses) != 1 || stringValue(toolUses[0].(map[string]any)["toolUseId"]) != "tool_keep" {
		t.Fatalf("expected only matched tool use to remain, got %#v", toolUses)
	}
	user, _ := history[1].(map[string]any)
	userBody, _ := user["userInputMessage"].(map[string]any)
	ctx, _ := userBody["userInputMessageContext"].(map[string]any)
	results := readArray(ctx["toolResults"])
	if len(results) != 1 || stringValue(results[0].(map[string]any)["toolUseId"]) != "tool_keep" {
		t.Fatalf("expected stale tool result to be removed, got %#v", results)
	}
}

func TestKiroParserMapsQueueQuotaAndMeteringEvents(t *testing.T) {
	metering := parseKiroEvent("meteringEvent", map[string]any{"usage": float64(3), "unit": "credit", "unitPlural": "credits"}, false)
	if metering.Metering == nil || metering.Metering.Usage != 3 {
		t.Fatalf("unexpected metering event: %#v", metering.Metering)
	}
	queue := parseKiroEvent("queueEvent", map[string]any{"position": float64(2), "queueDepth": float64(5), "message": "waiting"}, false)
	if queue.QueueStatus == nil || queue.QueueStatus.Position != 2 || queue.QueueStatus.QueueDepth != 5 {
		t.Fatalf("unexpected queue event: %#v", queue.QueueStatus)
	}
	quota := parseKiroEvent("quotaBalanceEvent", map[string]any{"quotaBalance": "42"}, false)
	if quota.QuotaBalance == nil || *quota.QuotaBalance != "42" {
		t.Fatalf("unexpected quota event: %#v", quota.QuotaBalance)
	}
}

func TestRunAgentLoopConsumesKiroStreamingToolChunks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `event: toolUseEvent
data: {"toolUseId":"tool_read","name":"Read","input":"{\"file_path\":\"note.txt\"}"}

`)
		fmt.Fprint(w, `event: toolUseEvent
data: {"toolUseId":"tool_read","name":"Read","stop":true}

`)
		fmt.Fprint(w, `event: assistantResponseEvent
data: {"stopReason":"end_turn"}

`)
	}))
	defer server.Close()
	adapter := &KiroProviderAdapter{Provider: Provider{Prefix: "kiro", Kind: "kiro", APIKey: "kiro-token", BaseURL: server.URL, Model: "claude-sonnet-4.5"}}
	stream := runAgentLoop(
		context.Background(),
		ProviderResolution{Provider: "kiro", Model: "claude-sonnet-4.5", Adapter: adapter},
		[]Message{{Role: "user", Content: "read"}},
		t.TempDir(),
		normalizeGenerateOptions(
			WithMaxTurns(1),
			WithRetryBaseDelay(0),
			WithToolExecutor(func(ctx context.Context, cwd string, call ToolCall) (ToolRun, error) {
				if call.ID != "tool_read" || call.Function.Name != "Read" || !strings.Contains(call.Function.Arguments, "note.txt") {
					t.Fatalf("unexpected tool call: %#v", call)
				}
				return ToolRun{Output: "kiro output"}, nil
			}),
		),
	)
	events := collectEvents(stream)
	if eventTypeCount(events, "tool_call") != 1 || eventTypeCount(events, "tool_result") != 1 {
		t.Fatalf("unexpected events: %#v", eventTypes(events))
	}
}

// TestKiroUsageSnapshotPromptFootprint locks the W840 fix: kiroUsageSnapshot
// mirrors the TS parseUsagePayload prompt footprint (kiro-protocol.ts:463-529).
// PromptTokens must be input + cached + cacheCreation (not input alone), the
// cacheCreation buckets must be parsed (and derived from 5m+1h when the top-level
// field is absent), and an explicit promptTokens key must win.
func TestKiroUsageSnapshotPromptFootprint(t *testing.T) {
	t.Run("footprint is input+cached+cacheCreation", func(t *testing.T) {
		snap := kiroUsageSnapshot(map[string]any{"usage": map[string]any{
			"inputTokens":              float64(100),
			"outputTokens":             float64(20),
			"cachedInputTokens":        float64(40),
			"cacheCreationInputTokens": float64(10),
		}})
		if snap == nil {
			t.Fatal("expected non-nil usage snapshot")
		}
		if snap.InputTokens != 100 || snap.CachedInputTokens != 40 || snap.CompletionTokens != 20 {
			t.Fatalf("base fields wrong: %#v", snap)
		}
		if snap.CacheCreationInputTokens != 10 {
			t.Fatalf("CacheCreationInputTokens = %d, want 10", snap.CacheCreationInputTokens)
		}
		if snap.PromptTokens != 150 {
			t.Fatalf("PromptTokens = %d, want 150 (100+40+10)", snap.PromptTokens)
		}
	})

	t.Run("derives cacheCreation from 5m+1h", func(t *testing.T) {
		snap := kiroUsageSnapshot(map[string]any{"usage": map[string]any{
			"inputTokens":           float64(100),
			"cacheCreation5mTokens": float64(7),
			"cacheCreation1hTokens": float64(3),
		}})
		if snap == nil {
			t.Fatal("expected non-nil usage snapshot")
		}
		if snap.CacheCreation5mTokens != 7 || snap.CacheCreation1hTokens != 3 {
			t.Fatalf("ephemeral buckets wrong: %#v", snap)
		}
		if snap.CacheCreationInputTokens != 10 {
			t.Fatalf("derived CacheCreationInputTokens = %d, want 10 (7+3)", snap.CacheCreationInputTokens)
		}
		if snap.PromptTokens != 110 {
			t.Fatalf("PromptTokens = %d, want 110 (100+0+10)", snap.PromptTokens)
		}
	})

	t.Run("explicit promptTokens wins", func(t *testing.T) {
		snap := kiroUsageSnapshot(map[string]any{"usage": map[string]any{
			"promptTokens":      float64(222),
			"inputTokens":       float64(100),
			"cachedInputTokens": float64(40),
		}})
		if snap == nil {
			t.Fatal("expected non-nil usage snapshot")
		}
		if snap.PromptTokens != 222 {
			t.Fatalf("PromptTokens = %d, want 222 (explicit key wins)", snap.PromptTokens)
		}
	})

	t.Run("cacheCreation-only payload is not dropped by hasAny guard", func(t *testing.T) {
		snap := kiroUsageSnapshot(map[string]any{"usage": map[string]any{
			"cacheCreationInputTokens": float64(15),
		}})
		if snap == nil {
			t.Fatal("cacheCreation-only payload should produce a snapshot (hasAny includes cacheCreation)")
		}
		if snap.CacheCreationInputTokens != 15 || snap.PromptTokens != 15 {
			t.Fatalf("cacheCreation-only: %#v (want CacheCreation=15, Prompt=15)", snap)
		}
	})

	t.Run("empty usage returns nil", func(t *testing.T) {
		if snap := kiroUsageSnapshot(map[string]any{"usage": map[string]any{}}); snap != nil {
			t.Fatalf("empty usage should return nil, got %#v", snap)
		}
	})
}
