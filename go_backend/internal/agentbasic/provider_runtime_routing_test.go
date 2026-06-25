package agentbasic

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"narrafork-go/internal/config"
)

func TestNkpSmokeWithLocalSettings(t *testing.T) {
	if os.Getenv("NARRAFORK_RUN_NKP_SMOKE") != "1" {
		t.Skip("set NARRAFORK_RUN_NKP_SMOKE=1 to run real NKP/Kiro smoke with local settings")
	}
	store, err := config.Load()
	if err != nil {
		t.Fatalf("load local settings: %v", err)
	}
	candidates, err := localNkpSmokeModelRefs(
		context.Background(),
		store.Raw(),
		strings.TrimSpace(os.Getenv("NARRAFORK_NKP_SMOKE_MODEL")),
		strings.TrimSpace(os.Getenv("NARRAFORK_NKP_SMOKE_PREFIX")),
	)
	if err != nil {
		t.Fatalf("resolve local NKP smoke models: %v", err)
	}
	if len(candidates) == 0 {
		t.Fatalf("no enabled local NKP provider with a defaultModel, enabledModels entry, or discoverable /v1/kiro/models entry was found")
	}
	failures := []string{}
	for _, modelRef := range candidates {
		if summary, ok := runLocalNkpSmokeCandidate(t, store, modelRef); ok {
			t.Logf("NKP/Kiro smoke succeeded with %s: %s", modelRef, summary)
			return
		} else {
			failures = append(failures, modelRef+": "+summary)
		}
	}
	t.Fatalf("all NKP/Kiro smoke candidates failed: %v", failures)
}

func runLocalNkpSmokeCandidate(t *testing.T, store *config.Store, modelRef string) (string, bool) {
	t.Helper()
	resolution, err := ResolveProviderAndModel(store, modelRef)
	if err != nil {
		return "resolve provider: " + err.Error(), false
	}
	if resolution.Config.Kind != "nkp" || resolution.Config.APIKey == "" || resolution.Config.BaseURL == "" {
		return fmt.Sprintf("provider not configured: kind=%s hasKey=%v hasBaseURL=%v", resolution.Config.Kind, resolution.Config.APIKey != "", resolution.Config.BaseURL != ""), false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	adapter, ok := resolution.Adapter.(*KiroProviderAdapter)
	if ok {
		adapter.Client = &http.Client{Timeout: 35 * time.Second}
	}
	stream, err := resolution.Adapter.Chat(ctx, ChatParams{
		History: &BuiltHistory{TrailingUserText: "Say OK only."},
		Cwd:     t.TempDir(),
	})
	if err != nil {
		return "chat start: " + err.Error(), false
	}
	text := ""
	messageID := ""
	conversationID := ""
	eventCount := 0
	summaries := []string{}
	for event := range stream {
		if event.Err != nil {
			return "stream error: " + event.Err.Error(), false
		}
		eventCount++
		summaries = append(summaries, parsedSmokeSummary(event.Parsed))
		text += event.Parsed.Text
		if event.Parsed.MessageID != "" {
			messageID = event.Parsed.MessageID
		}
		if event.Parsed.ConversationID != "" {
			conversationID = event.Parsed.ConversationID
		}
	}
	if strings.TrimSpace(text) == "" && messageID == "" && conversationID == "" {
		return fmt.Sprintf("no text or identifiers; events=%d summaries=%v", eventCount, summaries), false
	}
	return fmt.Sprintf("events=%d summaries=%v", eventCount, summaries), true
}

func parsedSmokeSummary(parsed ParsedStreamEvent) string {
	parts := []string{}
	if parsed.Text != "" {
		parts = append(parts, "text")
	}
	if parsed.MessageID != "" {
		parts = append(parts, "message_id")
	}
	if parsed.ConversationID != "" {
		parts = append(parts, "conversation_id")
	}
	if parsed.Reasoning != "" {
		parts = append(parts, "reasoning")
	}
	if parsed.ToolUseChunk != nil {
		parts = append(parts, "tool_chunk")
	}
	if parsed.Usage != nil {
		parts = append(parts, "usage")
	}
	if parsed.ContextUsagePercentage != nil {
		parts = append(parts, "context_usage")
	}
	if parsed.NkpQueue != nil {
		parts = append(parts, "nkp_queue")
	}
	if parsed.NkpQuotaBalance != nil {
		parts = append(parts, "nkp_quota")
	}
	if parsed.QueueStatus != nil {
		parts = append(parts, "queue")
	}
	if parsed.Metering != nil {
		parts = append(parts, "metering")
	}
	if parsed.InvalidState != nil {
		message := strings.TrimSpace(parsed.InvalidState.Message)
		if len(message) > 160 {
			message = message[:160]
		}
		parts = append(parts, "invalid:"+parsed.InvalidState.Reason+":"+message)
	}
	if len(parts) == 0 {
		return "empty"
	}
	return strings.Join(parts, "+")
}

func localNkpSmokeModelRefs(ctx context.Context, raw map[string]any, envModel string, envPrefix string) ([]string, error) {
	if envModel != "" {
		if strings.Contains(envModel, ":") {
			return []string{envModel}, nil
		}
		prefix := firstNonEmpty(envPrefix, "nkp")
		return []string{prefixedModelCandidate(prefix, envModel)}, nil
	}
	var lastErr error
	refs := []string{}
	seen := map[string]bool{}
	for _, item := range readArray(raw["nkpProviders"]) {
		provider, ok := item.(map[string]any)
		if !ok || boolValue(provider["disabled"]) {
			continue
		}
		prefix := stringValue(provider["prefix"])
		if prefix == "" {
			prefix = "nkp"
		}
		if envPrefix != "" && prefix != envPrefix {
			continue
		}
		models := []string{}
		if model := stringValue(provider["defaultModel"]); model != "" {
			models = append(models, model)
		}
		for _, modelValue := range readArray(provider["enabledModels"]) {
			if model := stringValue(modelValue); model != "" {
				models = append(models, model)
			}
		}
		if len(models) == 0 {
			models = append(models, cachedNkpSmokeModels(provider)...)
		}
		if len(models) == 0 {
			discovered, err := fetchNkpSmokeModels(ctx, provider)
			if err != nil {
				lastErr = err
				continue
			}
			models = append(models, discovered...)
		}
		if model := firstNkpSmokeModel(models); model != "" {
			ref := prefixedModelCandidate(prefix, model)
			if ref != "" && !seen[ref] {
				seen[ref] = true
				refs = append(refs, ref)
			}
		}
	}
	return refs, lastErr
}

func firstNkpSmokeModel(models []string) string {
	for _, model := range models {
		if strings.TrimSpace(model) != "" && strings.TrimSpace(model) != "auto" {
			return model
		}
	}
	return ""
}

func cachedNkpSmokeModels(provider map[string]any) []string {
	providerID := stringValue(provider["id"])
	if providerID == "" {
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(home, ".narrafork", "nkp-models-providers.json"))
	if err != nil || len(data) == 0 {
		return nil
	}
	var payload map[string][]map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil
	}
	models := []string{}
	seen := map[string]bool{}
	for _, model := range payload[providerID] {
		id := firstNonEmpty(stringValue(model["model_id"]), stringValue(model["modelId"]), stringValue(model["id"]), stringValue(model["model"]))
		if id != "" && !seen[id] {
			seen[id] = true
			models = append(models, id)
		}
	}
	return models
}

func fetchNkpSmokeModels(ctx context.Context, provider map[string]any) ([]string, error) {
	baseURL := strings.TrimRight(stringValue(provider["baseUrl"]), "/")
	apiKey := stringValue(provider["apiKey"])
	if baseURL == "" || apiKey == "" {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/v1/kiro/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	res, err := defaultProviderHTTPClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("NKP /v1/kiro/models returned status %d", res.StatusCode)
	}
	var payload map[string]any
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		return nil, err
	}
	models := []string{}
	seen := map[string]bool{}
	for _, item := range readArray(payload["data"]) {
		model, ok := item.(map[string]any)
		if !ok {
			continue
		}
		id := firstNonEmpty(stringValue(model["model_id"]), stringValue(model["modelId"]), stringValue(model["id"]), stringValue(model["model"]))
		if id != "" && !seen[id] {
			seen[id] = true
			models = append(models, id)
		}
	}
	return models, nil
}

func TestCachedNkpSmokeModelsUsesProviderScopedCache(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cacheDir := filepath.Join(home, ".narrafork")
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		t.Fatalf("mkdir cache dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, "nkp-models-providers.json"), []byte(`{"provider-a":[{"model_id":"claude-sonnet-4.5"},{"id":"deepseek-3.2"}]}`), 0o600); err != nil {
		t.Fatalf("write cache: %v", err)
	}
	models := cachedNkpSmokeModels(map[string]any{"id": "provider-a"})
	if len(models) != 2 || models[0] != "claude-sonnet-4.5" || models[1] != "deepseek-3.2" {
		t.Fatalf("unexpected cached models: %v", models)
	}
}

func TestLocalNkpSmokeModelRefsHonorsExplicitPrefix(t *testing.T) {
	refs, err := localNkpSmokeModelRefs(context.Background(), map[string]any{}, "nkp2:claude-sonnet-4.5", "")
	if err != nil {
		t.Fatalf("localNkpSmokeModelRefs returned error: %v", err)
	}
	if len(refs) != 1 || refs[0] != "nkp2:claude-sonnet-4.5" {
		t.Fatalf("expected explicit provider-prefixed model to pass through, got %v", refs)
	}
	refs, err = localNkpSmokeModelRefs(context.Background(), map[string]any{}, "claude-sonnet-4.5", "nkp2")
	if err != nil {
		t.Fatalf("localNkpSmokeModelRefs returned error: %v", err)
	}
	if len(refs) != 1 || refs[0] != "nkp2:claude-sonnet-4.5" {
		t.Fatalf("expected env prefix to be applied, got %v", refs)
	}
}

func TestFirstNkpSmokeModelSkipsAuto(t *testing.T) {
	if got := firstNkpSmokeModel([]string{"auto", "claude-haiku-4.5"}); got != "claude-haiku-4.5" {
		t.Fatalf("expected first concrete model, got %q", got)
	}
}

func TestNkpProviderUsesKiroProtocolAndRequestToken(t *testing.T) {
	seenToken := ""
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/kiro/chat" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		seenToken = r.Header.Get("X-NKP-Request-Token")
		_ = json.NewDecoder(r.Body).Decode(&requestBody)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `event: assistantResponseEvent
data: {"content":"nkp ok"}

`)
	}))
	defer server.Close()
	adapter := &KiroProviderAdapter{Provider: Provider{Prefix: "nkp", Kind: "nkp", APIKey: "nkp-key", BaseURL: server.URL, Model: "claude-sonnet-4.5"}}
	stream, err := adapter.Chat(context.Background(), ChatParams{History: &BuiltHistory{TrailingUserText: "hi"}})
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
	if text != "nkp ok" {
		t.Fatalf("unexpected text %q", text)
	}
	decoded, err := base64.StdEncoding.DecodeString(seenToken)
	if err != nil || !strings.Contains(string(decoded), ":") {
		t.Fatalf("invalid NKP request token %q decoded=%q err=%v", seenToken, string(decoded), err)
	}
	state, _ := requestBody["conversationState"].(map[string]any)
	workspace, _ := state["workspaceState"].(map[string]any)
	if workspace["sessionEpoch"] == nil || strings.TrimSpace(stringValue(workspace["traceId"])) == "" {
		t.Fatalf("expected NKP workspace telemetry, got %#v", workspace)
	}
}

func TestProviderCredentialsFromDiskRejectOversizedFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dataDir := filepath.Join(home, ".narrafork")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatalf("mkdir data dir: %v", err)
	}
	oversized := strings.Repeat("x", maxProviderJSONFileBytes+1)
	if err := os.WriteFile(filepath.Join(dataDir, "cline-credentials.json"), []byte(`{"accessToken":"`+oversized+`"}`), 0o600); err != nil {
		t.Fatalf("write cline creds: %v", err)
	}
	if credential, ok := firstClineCredentialFromDisk(); ok || credential != nil {
		t.Fatalf("oversized cline credentials should be ignored, got %#v", credential)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "codex-credentials.json"), []byte(`[{"accessToken":"`+oversized+`"}]`), 0o600); err != nil {
		t.Fatalf("write codex creds: %v", err)
	}
	if entries, ok := codexCredentialEntriesFromDisk(); ok || entries != nil {
		t.Fatalf("oversized codex credentials should be ignored, got %#v", entries)
	}
}

func TestClineProviderRefreshesAndRetriesOnUnauthorized(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dataDir := filepath.Join(home, ".narrafork")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatalf("mkdir data dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "cline-credentials.json"), []byte(`{"accessToken":"old-token","refreshToken":"refresh-token","expiresAt":4102444800}`), 0o600); err != nil {
		t.Fatalf("write cline creds: %v", err)
	}
	refreshServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"success":true,"data":{"accessToken":"new-token","refreshToken":"refresh-token-2","expiresAt":"2099-01-01T00:00:00Z"}}`)
	}))
	defer refreshServer.Close()
	t.Setenv("CLINE_API_BASE_URL", refreshServer.URL)
	requests := 0
	chatServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests == 1 {
			if r.Header.Get("Authorization") != "Bearer workos:old-token" {
				t.Fatalf("expected old cline token, got %s", r.Header.Get("Authorization"))
			}
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Header.Get("Authorization") != "Bearer workos:new-token" {
			t.Fatalf("expected refreshed cline token, got %s", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"content":"cline retry ok"}}]}

`)
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer chatServer.Close()
	adapter := &OpenAIChatCompletionsAdapter{Provider: Provider{Prefix: "cline", Kind: "cline", APIKey: "old-token", BaseURL: chatServer.URL, Model: "anthropic/claude-sonnet-4"}}
	stream, err := adapter.Chat(context.Background(), ChatParams{History: &BuiltHistory{History: []any{Message{Role: "user", Content: "hi"}}}})
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
	if text != "cline retry ok" || requests != 2 {
		t.Fatalf("unexpected cline retry result text=%q requests=%d", text, requests)
	}
}

func TestClineProviderAddsWorkOSTokenAndOpenRouterHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer workos:cline-token" {
			t.Fatalf("unexpected cline auth header: %s", got)
		}
		// Cline VS Code extension header set (TS buildOpenRouterHeaders +
		// buildClineHeaders): exact values, not narrafork-branded.
		if r.Header.Get("HTTP-Referer") != "https://cline.bot" || r.Header.Get("X-Title") != "Cline" {
			t.Fatalf("cline HTTP-Referer/X-Title must be cline-branded, got referer=%q title=%q", r.Header.Get("HTTP-Referer"), r.Header.Get("X-Title"))
		}
		if r.Header.Get("User-Agent") != "Cline/3.74.0" || r.Header.Get("X-PLATFORM") != "node" || r.Header.Get("X-CLIENT-TYPE") != "extension" || r.Header.Get("X-CLIENT-VERSION") != "3.74.0" || r.Header.Get("X-CORE-VERSION") != "3.74.0" {
			t.Fatalf("missing/incorrect cline client headers: ua=%q platform=%q clientType=%q clientVer=%q coreVer=%q", r.Header.Get("User-Agent"), r.Header.Get("X-PLATFORM"), r.Header.Get("X-CLIENT-TYPE"), r.Header.Get("X-CLIENT-VERSION"), r.Header.Get("X-CORE-VERSION"))
		}
		if r.Header.Get("X-PLATFORM-VERSION") == "" {
			t.Fatalf("expected non-empty X-PLATFORM-VERSION")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"content":"cline ok"}}]}

`)
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()
	adapter := &OpenAIChatCompletionsAdapter{Provider: Provider{Prefix: "cline", Kind: "cline", APIKey: "cline-token", BaseURL: server.URL, Model: "anthropic/claude-sonnet-4"}}
	stream, err := adapter.Chat(context.Background(), ChatParams{History: &BuiltHistory{History: []any{Message{Role: "user", Content: "hi"}}}})
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
	if text != "cline ok" {
		t.Fatalf("unexpected cline text %q", text)
	}
}

func TestCodexProviderUsesWebSocketResponsesWhenAvailable(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("unexpected codex ws path: %s", r.URL.Path)
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("upgrade websocket: %v", err)
		}
		defer conn.Close()
		var req map[string]any
		if err := conn.ReadJSON(&req); err != nil {
			t.Fatalf("read response.create: %v", err)
		}
		if req["type"] != "response.create" || req["model"] != "gpt-5.3-codex" {
			t.Fatalf("unexpected ws request: %#v", req)
		}
		_ = conn.WriteJSON(map[string]any{"type": "response.output_text.delta", "delta": "codex ws ok"})
		_ = conn.WriteJSON(map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp_ws"}})
		_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "done"))
	}))
	defer server.Close()
	adapter := &OpenAIChatCompletionsAdapter{Provider: Provider{Prefix: "codex", Kind: "codex", APIKey: "codex-token", BaseURL: server.URL, Model: "gpt-5.3-codex", APIMode: "codex"}}
	stream, err := adapter.Chat(context.Background(), ChatParams{History: &BuiltHistory{History: []any{Message{Role: "user", Content: "hi"}}}})
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
	if text != "codex ws ok" {
		t.Fatalf("unexpected codex ws text %q", text)
	}
}

func TestCodexProviderFallsBackToSSEWhenWebSocketUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("unexpected codex path: %s", r.URL.Path)
		}
		if strings.Contains(strings.ToLower(r.Header.Get("Upgrade")), "websocket") {
			http.Error(w, "websocket unavailable", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"type":"response.output_text.delta","delta":"codex sse fallback"}

`)
		fmt.Fprint(w, `data: {"type":"response.completed","response":{"id":"resp_sse"}}

`)
	}))
	defer server.Close()
	adapter := &OpenAIChatCompletionsAdapter{Provider: Provider{Prefix: "codex", Kind: "codex", APIKey: "codex-token", BaseURL: server.URL, Model: "gpt-5.3-codex", APIMode: "codex"}}
	stream, err := adapter.Chat(context.Background(), ChatParams{History: &BuiltHistory{History: []any{Message{Role: "user", Content: "hi"}}}})
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
	if text != "codex sse fallback" {
		t.Fatalf("unexpected codex fallback text %q", text)
	}
}

func TestCodexProviderSkipsWebSocketWhenDisabled(t *testing.T) {
	postRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("unexpected codex path: %s", r.URL.Path)
		}
		if strings.Contains(strings.ToLower(r.Header.Get("Upgrade")), "websocket") {
			t.Fatalf("websocket should not be attempted when disabled")
		}
		if r.Method == http.MethodPost {
			postRequests++
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"type":"response.output_text.delta","delta":"codex http only"}

`)
		fmt.Fprint(w, `data: {"type":"response.completed","response":{"id":"resp_http_only"}}

`)
	}))
	defer server.Close()
	useWebSocket := false
	adapter := &OpenAIChatCompletionsAdapter{Provider: Provider{Prefix: "codex", Kind: "codex", APIKey: "codex-token", BaseURL: server.URL, Model: "gpt-5.3-codex", APIMode: "codex", UseWebSocket: &useWebSocket}}
	stream, err := adapter.Chat(context.Background(), ChatParams{History: &BuiltHistory{History: []any{Message{Role: "user", Content: "hi"}}}})
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
	if text != "codex http only" || postRequests != 1 {
		t.Fatalf("unexpected codex http-only result text=%q postRequests=%d", text, postRequests)
	}
}

func TestCodexProviderFailsOverAcrossCredentialPool(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dataDir := filepath.Join(home, ".narrafork")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatalf("mkdir data dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "codex-credentials.json"), []byte(`[
		{"id":"bad","accessToken":"bad-token","accountId":"bad-acct","priority":0},
		{"id":"good","accessToken":"good-token","accountId":"good-acct","priority":1}
	]`), 0o600); err != nil {
		t.Fatalf("write codex creds: %v", err)
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests == 1 {
			if r.Header.Get("Authorization") != "Bearer bad-token" {
				t.Fatalf("expected first bad token, got %s", r.Header.Get("Authorization"))
			}
			http.Error(w, "quota exhausted", http.StatusTooManyRequests)
			return
		}
		// ChatGPT-Account-Id is gated to official ChatGPT domains (TS parity), so
		// it is NOT sent to this non-official test server even though the
		// credential has an accountId.
		if r.Header.Get("Authorization") != "Bearer good-token" || r.Header.Get("ChatGPT-Account-Id") != "" {
			t.Fatalf("expected failover credential without account header on non-official domain, auth=%s account=%s", r.Header.Get("Authorization"), r.Header.Get("ChatGPT-Account-Id"))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"type":"response.output_text.delta","delta":"failover ok"}

`)
		fmt.Fprint(w, `data: {"type":"response.completed","response":{"id":"resp_1"}}

`)
	}))
	defer server.Close()
	store, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if _, err := store.Patch(map[string]any{"codex": map[string]any{"baseUrl": server.URL}}); err != nil {
		t.Fatalf("patch config: %v", err)
	}
	resolution, err := ResolveProviderAndModel(store, "codex:gpt-5.3-codex")
	if err != nil {
		t.Fatalf("resolve codex pool: %v", err)
	}
	adapter := &OpenAIChatCompletionsAdapter{Provider: resolution.Config}
	stream, err := adapter.Chat(context.Background(), ChatParams{History: &BuiltHistory{History: []any{Message{Role: "user", Content: "hi"}}}})
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
	if text != "failover ok" || requests != 3 {
		t.Fatalf("unexpected failover result text=%q requests=%d", text, requests)
	}
	data, err := os.ReadFile(filepath.Join(dataDir, "codex-credentials.json"))
	if err != nil {
		t.Fatalf("read codex creds: %v", err)
	}
	var entries []map[string]any
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Fatalf("decode codex creds: %v", err)
	}
	if entries[0]["disabledReason"] != "quota_exhausted" || entries[0]["lastRuntimeError"] == nil {
		t.Fatalf("expected quota failure written back to first credential: %#v", entries[0])
	}
}

func TestCodexProviderUsesStickySessionAfterFailover(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dataDir := filepath.Join(home, ".narrafork")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatalf("mkdir data dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "codex-credentials.json"), []byte(`[
		{"id":"bad","accessToken":"bad-token","accountId":"bad-acct","priority":0},
		{"id":"good","accessToken":"good-token","accountId":"good-acct","priority":1}
	]`), 0o600); err != nil {
		t.Fatalf("write codex creds: %v", err)
	}
	badRequests := 0
	goodRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("Authorization") {
		case "Bearer bad-token":
			badRequests++
			http.Error(w, `{"error":{"code":"usage_limit_reached","message":"usage limit reached","resets_at":2000000000}}`, http.StatusTooManyRequests)
			return
		case "Bearer good-token":
			goodRequests++
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, `data: {"type":"response.output_text.delta","delta":"ok"}

`)
			fmt.Fprint(w, `data: {"type":"response.completed","response":{"id":"resp_1"}}

`)
			return
		default:
			t.Fatalf("unexpected auth header: %s", r.Header.Get("Authorization"))
		}
	}))
	defer server.Close()
	store, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if _, err := store.Patch(map[string]any{"codex": map[string]any{"baseUrl": server.URL, "loadBalancingMode": "tier-balanced"}}); err != nil {
		t.Fatalf("patch config: %v", err)
	}
	resolution, err := ResolveProviderAndModel(store, "codex:gpt-5.3-codex")
	if err != nil {
		t.Fatalf("resolve codex pool: %v", err)
	}
	adapter := &OpenAIChatCompletionsAdapter{Provider: resolution.Config}
	for i := 0; i < 2; i++ {
		stream, err := adapter.Chat(context.Background(), ChatParams{StickySessionKey: "nar-sticky", History: &BuiltHistory{History: []any{Message{Role: "user", Content: "hi"}}}})
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
		if text != "ok" {
			t.Fatalf("unexpected text on call %d: %q", i+1, text)
		}
	}
	if badRequests != 1 || goodRequests < 2 || CodexStickySessionCount() < 1 {
		t.Fatalf("sticky session did not prefer the successful credential: bad=%d good=%d sticky=%d", badRequests, goodRequests, CodexStickySessionCount())
	}
}

func TestCodexProviderMapsUsageLimitFailureToRebuildRetryAfterYield(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dataDir := filepath.Join(home, ".narrafork")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatalf("mkdir data dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "codex-credentials.json"), []byte(`[
		{"id":"bad","accessToken":"bad-token","accountId":"bad-acct","priority":0},
		{"id":"good","accessToken":"good-token","accountId":"good-acct","priority":1}
	]`), 0o600); err != nil {
		t.Fatalf("write codex creds: %v", err)
	}
	goodRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer good-token" {
			goodRequests++
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"type":"response.output_text.delta","delta":"partial"}

`)
		fmt.Fprint(w, `data: {"type":"response.failed","response":{"error":{"code":"usage_limit_reached","message":"usage limit reached","resets_at":2000000000}}}

`)
	}))
	defer server.Close()
	store, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if _, err := store.Patch(map[string]any{"codex": map[string]any{"baseUrl": server.URL, "loadBalancingMode": "tier-balanced"}}); err != nil {
		t.Fatalf("patch config: %v", err)
	}
	resolution, err := ResolveProviderAndModel(store, "codex:gpt-5.3-codex")
	if err != nil {
		t.Fatalf("resolve codex pool: %v", err)
	}
	adapter := &OpenAIChatCompletionsAdapter{Provider: resolution.Config}
	stream, err := adapter.Chat(context.Background(), ChatParams{StickySessionKey: "nar-rebuild", History: &BuiltHistory{History: []any{Message{Role: "user", Content: "hi"}}}})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	text := ""
	var streamErr error
	for event := range stream {
		if event.Err != nil {
			streamErr = event.Err
			continue
		}
		text += event.Parsed.Text
	}
	if text != "partial" || !isCodexRebuildHistoryRetryError(streamErr) || goodRequests != 0 {
		t.Fatalf("expected partial output and rebuild retry without in-stream failover, text=%q err=%v goodRequests=%d", text, streamErr, goodRequests)
	}
	data, err := os.ReadFile(filepath.Join(dataDir, "codex-credentials.json"))
	if err != nil {
		t.Fatalf("read codex creds: %v", err)
	}
	var entries []map[string]any
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Fatalf("decode codex creds: %v", err)
	}
	if entries[0]["disabled"] != true || entries[0]["disabledReason"] != "quota_exhausted" || entries[0]["quotaResetsAt"] == nil {
		t.Fatalf("expected quota failure metadata on first credential: %#v", entries[0])
	}
}

func TestCodexProviderUsesResponsesEndpointAndHeaders(t *testing.T) {
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("unexpected codex path: %s", r.URL.Path)
		}
		// ChatGPT-Account-Id is gated to official ChatGPT domains (TS parity), so
		// it is NOT sent to this non-official test server.
		if r.Header.Get("Authorization") != "Bearer codex-token" || r.Header.Get("originator") != "narrafork" || r.Header.Get("ChatGPT-Account-Id") != "" {
			t.Fatalf("unexpected codex headers authorization=%q originator=%q account=%q", r.Header.Get("Authorization"), r.Header.Get("originator"), r.Header.Get("ChatGPT-Account-Id"))
		}
		_ = json.NewDecoder(r.Body).Decode(&requestBody)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"type":"response.output_text.delta","delta":"codex ok"}

`)
		fmt.Fprint(w, `data: {"type":"response.completed","response":{"id":"resp_1"}}

`)
	}))
	defer server.Close()
	adapter := &OpenAIChatCompletionsAdapter{Provider: Provider{Prefix: "codex", Kind: "codex", APIKey: "codex-token", BaseURL: server.URL, Model: "gpt-5.3-codex", APIMode: "codex", AccountID: "acct_1", DefaultReasoningEffort: "medium"}}
	stream, err := adapter.Chat(context.Background(), ChatParams{ConversationID: "conv_codex_headers", History: &BuiltHistory{History: []any{Message{Role: "user", Content: "hi"}}}})
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
	if text != "codex ok" {
		t.Fatalf("unexpected codex text %q", text)
	}
	if requestBody["store"] != false || requestBody["parallel_tool_calls"] != true || requestBody["prompt_cache_key"] != "conv_codex_headers" || requestBody["instructions"] == nil {
		t.Fatalf("unexpected codex responses body: %#v", requestBody)
	}
	reasoning, _ := requestBody["reasoning"].(map[string]any)
	if reasoning["effort"] != "medium" {
		t.Fatalf("expected default reasoning effort to be applied, body=%#v", requestBody)
	}
	tools, _ := requestBody["tools"].([]any)
	seenWeb := false
	seenImage := false
	for _, raw := range tools {
		tool, _ := raw.(map[string]any)
		if tool["type"] == "web_search" {
			seenWeb = true
		}
		if tool["type"] == "image_generation" {
			seenImage = true
		}
	}
	if !seenWeb || !seenImage {
		t.Fatalf("expected codex native tools in body: %#v", tools)
	}
}

func TestCodexResponsesUsesConfiguredHTTPProxy(t *testing.T) {
	proxyHit := false
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyHit = true
		if r.URL.Scheme != "http" || r.URL.Host != "codex-upstream.example" || r.URL.Path != "/v1/responses" {
			t.Fatalf("unexpected proxied codex request URL: scheme=%q host=%q path=%q requestURI=%q", r.URL.Scheme, r.URL.Host, r.URL.Path, r.RequestURI)
		}
		if r.Header.Get("Authorization") != "Bearer codex-token" {
			t.Fatalf("unexpected proxied auth header: %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"type":"response.output_text.delta","delta":"proxied codex"}

`)
		fmt.Fprint(w, `data: {"type":"response.completed","response":{"id":"resp_proxy"}}

`)
	}))
	defer proxy.Close()
	useWebSocket := false
	adapter := &OpenAIChatCompletionsAdapter{Provider: Provider{Prefix: "codex", Kind: "codex", APIKey: "codex-token", BaseURL: "http://codex-upstream.example/v1", Model: "gpt-5.3-codex", APIMode: "codex", UseWebSocket: &useWebSocket, ProxyURL: proxy.URL}}
	stream, err := adapter.Chat(context.Background(), ChatParams{ConversationID: "conv_proxy", History: &BuiltHistory{History: []any{Message{Role: "user", Content: "hi"}}}})
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
	if !proxyHit || text != "proxied codex" {
		t.Fatalf("expected codex request through proxy, proxyHit=%v text=%q", proxyHit, text)
	}
}

func TestCodexWebSocketUsesConfiguredHTTPProxy(t *testing.T) {
	proxyHit := make(chan string, 1)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyHit <- r.URL.String()
		http.Error(w, "proxy observed websocket", http.StatusBadGateway)
	}))
	defer proxy.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := streamCodexResponsesWebSocket(ctx, Provider{Prefix: "codex", Kind: "codex", APIKey: "codex-token", BaseURL: "http://codex-ws.example/backend-api/codex", Model: "gpt-5.3-codex", APIMode: "codex", ProxyURL: proxy.URL}, nil, "", "", make(chan StreamEvent, 1))
	if err == nil {
		t.Fatalf("expected websocket proxy test to fail after proxy response")
	}
	select {
	case target := <-proxyHit:
		if !strings.Contains(target, "codex-ws.example") {
			t.Fatalf("unexpected proxied websocket target %q", target)
		}
	case <-ctx.Done():
		t.Fatalf("codex websocket did not reach configured proxy: %v", err)
	}
}

func TestResolverFetchesNugRemoteModelMetadataAndCaches(t *testing.T) {
	modelRequests := 0
	healthRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer nug-key" {
			t.Fatalf("missing NUG auth header: %s", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/models":
			modelRequests++
			fmt.Fprint(w, `{"data":[{"id":"remote-logical","channelType":"openai","routedModel":"gpt-routed"}]}`)
		case "/v1/channels/health":
			healthRequests++
			fmt.Fprint(w, `{"data":[{"channel":"openai","status":"healthy"}]}`)
		default:
			t.Fatalf("unexpected NUG path: %s", r.URL.Path)
		}
	}))
	defer server.Close()
	store := testConfigStore(t, map[string]any{
		"nugProviders": []any{map[string]any{
			"apiKey":       "nug-key",
			"baseUrl":      server.URL,
			"defaultModel": "remote-logical",
		}},
	})
	for i := 0; i < 2; i++ {
		resolution, err := ResolveProviderAndModel(store, "nug:remote-logical")
		if err != nil {
			t.Fatalf("resolve remote nug meta: %v", err)
		}
		if resolution.Config.APIMode != "openai" || resolution.Config.Model != "gpt-routed" {
			t.Fatalf("unexpected remote nug resolution: %#v", resolution.Config)
		}
	}
	if modelRequests != 1 || healthRequests != 1 {
		t.Fatalf("expected remote NUG model/health cache to fetch once, got models=%d health=%d", modelRequests, healthRequests)
	}
}

func TestResolverUsesNugChannelHealthForDynamicRouting(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/models":
			fmt.Fprint(w, `{"data":[{"id":"multi-channel","channelType":"openai","routedModel":"gpt-down"},{"id":"multi-channel","channelType":"anthropic","routedModel":"claude-up"}]}`)
		case "/v1/channels/health":
			fmt.Fprint(w, `{"data":[{"channel":"openai","status":"unhealthy"},{"channel":"anthropic","status":"healthy"}]}`)
		default:
			t.Fatalf("unexpected NUG path: %s", r.URL.Path)
		}
	}))
	defer server.Close()
	store := testConfigStore(t, map[string]any{
		"nugProviders": []any{map[string]any{"apiKey": "nug-key", "baseUrl": server.URL, "defaultModel": "multi-channel"}},
	})
	resolution, err := ResolveProviderAndModel(store, "nug:multi-channel")
	if err != nil {
		t.Fatalf("resolve NUG health route: %v", err)
	}
	if resolution.Config.APIMode != "anthropic" || resolution.Config.Model != "claude-up" {
		t.Fatalf("expected health-aware route to anthropic, got %#v", resolution.Config)
	}
}

func TestResolverUsesNugModelMetadataForChannelRouting(t *testing.T) {
	store := testConfigStore(t, map[string]any{
		"nugProviders": []any{map[string]any{
			"apiKey":  "nug-key",
			"baseUrl": "https://nug.example",
			"models":  []any{map[string]any{"id": "logical-model", "channelType": "anthropic", "routedModel": "claude-routed"}},
		}},
	})
	resolution, err := ResolveProviderAndModel(store, "nug:logical-model")
	if err != nil {
		t.Fatalf("resolve nug model meta: %v", err)
	}
	if resolution.Config.APIMode != "anthropic" || resolution.Config.Model != "claude-routed" {
		t.Fatalf("unexpected nug meta resolution: %#v", resolution.Config)
	}
}

func TestNugProviderDelegatesByChannelMode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/usage/summary":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"data":{"remaining":"99","usage":3,"unit":"credit","unitPlural":"credits"}}`)
		case "/v1/chat/completions":
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"content":"nug openai ok"}}]}

`)
			fmt.Fprint(w, "data: [DONE]\n\n")
		default:
			t.Fatalf("unexpected NUG delegated path: %s", r.URL.Path)
		}
	}))
	defer server.Close()
	adapter := &NugProviderAdapter{Provider: Provider{Prefix: "nug", Kind: "nug", APIKey: "nug-key", BaseURL: server.URL, Model: "gpt-test", APIMode: "openai"}}
	stream, err := adapter.Chat(context.Background(), ChatParams{History: &BuiltHistory{History: []any{Message{Role: "user", Content: "hi"}}}})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	text := ""
	seenQuota := false
	seenMetering := false
	for event := range stream {
		if event.Err != nil {
			t.Fatalf("stream error: %v", event.Err)
		}
		text += event.Parsed.Text
		if event.Parsed.QuotaBalance != nil && *event.Parsed.QuotaBalance == "99" {
			seenQuota = true
		}
		if event.Parsed.Metering != nil && event.Parsed.Metering.Usage == 3 {
			seenMetering = true
		}
	}
	if !seenQuota || !seenMetering {
		t.Fatalf("expected NUG usage/quota events quota=%v metering=%v", seenQuota, seenMetering)
	}
	if text != "nug openai ok" {
		t.Fatalf("unexpected nug text %q", text)
	}
}

func TestResolverTreatsLegacyOpenAIProviderCodexModeAsCodex(t *testing.T) {
	store := testConfigStore(t, map[string]any{
		"openaiProviders": []any{
			map[string]any{"prefix": "legacycx", "apiMode": "codex", "apiKey": "codex-key", "baseUrl": "https://codex.example", "defaultModel": "gpt-5.3-codex", "codexAccountId": "acct_legacy", "codexWebSocket": true},
			map[string]any{"prefix": "legacycxdefault", "apiMode": "codex", "apiKey": "codex-key", "baseUrl": "https://codex.example", "defaultModel": "gpt-5.3-codex", "codexAccountId": "acct_default"},
		},
	})
	resolution, err := ResolveProviderAndModel(store, "legacycx:gpt-5.3-codex")
	if err != nil {
		t.Fatalf("resolve legacy openai codex provider: %v", err)
	}
	if resolution.Config.Kind != "codex" || resolution.Config.APIMode != "codex" || resolution.Config.AccountID != "acct_legacy" || resolution.Config.UseWebSocket == nil || !*resolution.Config.UseWebSocket {
		t.Fatalf("unexpected legacy openai codex provider: %#v", resolution.Config)
	}
	defaultResolution, err := ResolveProviderAndModel(store, "legacycxdefault:gpt-5.3-codex")
	if err != nil {
		t.Fatalf("resolve default legacy openai codex provider: %v", err)
	}
	if defaultResolution.Config.UseWebSocket == nil || *defaultResolution.Config.UseWebSocket {
		t.Fatalf("expected legacy OpenAI codex provider to default WebSocket off, got %#v", defaultResolution.Config)
	}
}

func TestResolverAppliesDefaultReasoningEffortFallbacks(t *testing.T) {
	store := testConfigStore(t, map[string]any{
		"agent": map[string]any{"defaultReasoningEffort": "low"},
		"codex": map[string]any{"defaultReasoningEffort": "medium"},
		"openaiProviders": []any{
			map[string]any{"prefix": "open", "apiKey": "open-key", "baseUrl": "https://open.example", "defaultModel": "gpt-5-test", "apiMode": "responses"},
			map[string]any{"prefix": "cx", "apiKey": "codex-key", "baseUrl": "https://codex.example", "defaultModel": "gpt-5.3-codex", "apiMode": "codex", "codexWebSocket": false},
		},
		"anthropicProviders": []any{
			map[string]any{"prefix": "anth", "apiKey": "anth-key", "baseUrl": "https://anth.example/v1", "defaultModel": "claude-test", "defaultReasoningEffort": "high"},
			map[string]any{"prefix": "anthfallback", "apiKey": "anth-key-2", "baseUrl": "https://anth2.example/v1", "defaultModel": "claude-test"},
		},
	})
	cases := []struct {
		model string
		want  string
	}{
		{"open:gpt-5-test", "low"},
		{"cx:gpt-5.3-codex", "medium"},
		{"anth:claude-test", "high"},
		{"anthfallback:claude-test", "low"},
	}
	for _, tc := range cases {
		resolution, err := ResolveProviderAndModel(store, tc.model)
		if err != nil {
			t.Fatalf("resolve %s: %v", tc.model, err)
		}
		if resolution.Config.DefaultReasoningEffort != tc.want {
			t.Fatalf("expected %s default reasoning %q, got %#v", tc.model, tc.want, resolution.Config)
		}
	}
}

func TestResolverSupportsCustomAnthropicAndCodexProtocols(t *testing.T) {
	store := testConfigStore(t, map[string]any{
		"customApiProviders": []any{
			map[string]any{"prefix": "anth", "protocol": "anthropic-official", "apiKey": "anth-key", "baseUrl": "https://anth.example/v1", "defaultModel": "claude-test", "proxy": "http://anth-proxy.example:8080", "tlsRejectUnauthorized": false},
			map[string]any{"prefix": "cx", "protocol": "codex-native", "apiKey": "codex-key", "baseUrl": "https://codex.example", "defaultModel": "gpt-5.3-codex", "codexAccountId": "acct_2", "codexWebSocket": false},
			map[string]any{"prefix": "cxdefault", "protocol": "codex-native", "apiKey": "codex-key-default", "baseUrl": "https://codex-default.example", "defaultModel": "gpt-5.3-codex", "codexAccountId": "acct_default"},
			map[string]any{"prefix": "cxws", "protocol": "codex-native", "apiKey": "codex-key-ws", "baseUrl": "https://codex-ws.example", "defaultModel": "gpt-5.3-codex", "codexAccountId": "acct_ws", "codexWebSocket": true},
		},
	})
	anth, err := ResolveProviderAndModel(store, "anth:claude-test")
	if err != nil {
		t.Fatalf("resolve custom anthropic: %v", err)
	}
	if anth.Config.Kind != "anthropic" || !anth.Config.OfficialAPI || anth.Config.ProxyURL != "http://anth-proxy.example:8080" || anth.Config.TLSRejectUnauthorized == nil || *anth.Config.TLSRejectUnauthorized {
		t.Fatalf("unexpected anthropic custom provider: %#v", anth.Config)
	}
	codex, err := ResolveProviderAndModel(store, "cx:gpt-5.3-codex")
	if err != nil {
		t.Fatalf("resolve custom codex: %v", err)
	}
	if codex.Config.Kind != "codex" || codex.Config.APIMode != "codex" || codex.Config.AccountID != "acct_2" || codex.Config.UseWebSocket == nil || *codex.Config.UseWebSocket {
		t.Fatalf("unexpected codex custom provider: %#v", codex.Config)
	}
	codexDefault, err := ResolveProviderAndModel(store, "cxdefault:gpt-5.3-codex")
	if err != nil {
		t.Fatalf("resolve custom codex default websocket: %v", err)
	}
	if codexDefault.Config.UseWebSocket == nil || *codexDefault.Config.UseWebSocket {
		t.Fatalf("expected custom codex provider to default WebSocket off, got %#v", codexDefault.Config)
	}
	codexWS, err := ResolveProviderAndModel(store, "cxws:gpt-5.3-codex")
	if err != nil {
		t.Fatalf("resolve custom codex websocket: %v", err)
	}
	if codexWS.Config.Kind != "codex" || codexWS.Config.APIMode != "codex" || codexWS.Config.AccountID != "acct_ws" || codexWS.Config.UseWebSocket == nil || !*codexWS.Config.UseWebSocket {
		t.Fatalf("unexpected codex websocket custom provider: %#v", codexWS.Config)
	}
}

func TestResolverOrdersCodexCredentialsByTierBalancedMode(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dataDir := filepath.Join(home, ".narrafork")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatalf("mkdir data dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "codex-credentials.json"), []byte(`[
		{"id":"plus-busy","accessToken":"plus-token","accountId":"plus","usage":{"plan_type":"plus","usedPercent":90},"priority":0},
		{"id":"pro-free","accessToken":"pro-token","accountId":"pro","usage":{"plan_type":"pro","usedPercent":10},"priority":1},
		{"id":"pro-busy","accessToken":"pro-busy-token","accountId":"pro-busy","usage":{"plan_type":"pro","usedPercent":80},"priority":2}
	]`), 0o600); err != nil {
		t.Fatalf("write codex creds: %v", err)
	}
	store, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if _, err := store.Patch(map[string]any{"codex": map[string]any{"baseUrl": "https://codex.example", "proxy": "http://codex-proxy.example:8080", "loadBalancingMode": "tier-balanced", "tierOrder": []any{"pro", "plus"}}}); err != nil {
		t.Fatalf("patch config: %v", err)
	}
	resolution, err := ResolveProviderAndModel(store, "codex:gpt-5.3-codex")
	if err != nil {
		t.Fatalf("resolve tier-balanced codex: %v", err)
	}
	if resolution.Config.CredentialID != "pro-free" || resolution.Config.APIKey != "pro-token" || resolution.Config.ProxyURL != "http://codex-proxy.example:8080" {
		t.Fatalf("expected pro-free credential first with proxy, got %#v", resolution.Config)
	}
	if len(resolution.Config.CredentialPool) != 3 || resolution.Config.CredentialPool[0].CredentialID != "pro-free" || resolution.Config.CredentialPool[1].CredentialID != "pro-busy" || resolution.Config.CredentialPool[2].CredentialID != "plus-busy" || resolution.Config.CredentialPool[0].ProxyURL != "http://codex-proxy.example:8080" {
		t.Fatalf("unexpected tier-balanced pool order/proxy: %#v", resolution.Config.CredentialPool)
	}
}

func TestResolverRefreshesExpiredCodexAndClineCredentials(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dataDir := filepath.Join(home, ".narrafork")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatalf("mkdir data dir: %v", err)
	}
	clineRefresh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/auth/refresh" {
			t.Fatalf("unexpected cline refresh path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"success":true,"data":{"accessToken":"cline-refreshed","refreshToken":"cline-refresh-2","expiresAt":"2099-01-01T00:00:00Z"}}`)
	}))
	defer clineRefresh.Close()
	codexRefresh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/token" {
			t.Fatalf("unexpected codex refresh path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token":"codex-refreshed","refresh_token":"codex-refresh-2","expires_in":3600}`)
	}))
	defer codexRefresh.Close()
	t.Setenv("CLINE_API_BASE_URL", clineRefresh.URL)
	t.Setenv("CODEX_ISSUER_URL", codexRefresh.URL)
	if err := os.WriteFile(filepath.Join(dataDir, "cline-credentials.json"), []byte(`{"accessToken":"cline-old","refreshToken":"cline-refresh","expiresAt":1}`), 0o600); err != nil {
		t.Fatalf("write cline creds: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "codex-credentials.json"), []byte(`[{"id":"codex-a","accessToken":"codex-old","refreshToken":"codex-refresh","accountId":"acct_refresh","expiresAt":1,"priority":0}]`), 0o600); err != nil {
		t.Fatalf("write codex creds: %v", err)
	}
	store, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if _, err := store.Patch(map[string]any{
		"codex":          map[string]any{"baseUrl": "https://codex.example"},
		"clineProviders": []any{map[string]any{"baseUrl": "https://cline.example", "defaultModel": "anthropic/claude"}},
	}); err != nil {
		t.Fatalf("patch config: %v", err)
	}
	codex, err := ResolveProviderAndModel(store, "codex:gpt-5.3-codex")
	if err != nil {
		t.Fatalf("resolve refreshed codex: %v", err)
	}
	if codex.Config.APIKey != "codex-refreshed" {
		t.Fatalf("expected refreshed codex token, got %#v", codex.Config)
	}
	cline, err := ResolveProviderAndModel(store, "cline:anthropic/claude")
	if err != nil {
		t.Fatalf("resolve refreshed cline: %v", err)
	}
	if cline.Config.APIKey != "cline-refreshed" {
		t.Fatalf("expected refreshed cline token, got %#v", cline.Config)
	}
}

func TestResolverSkipsExpiredCodexAccessTokenOnlyCredentialWithoutRefresh(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dataDir := filepath.Join(home, ".narrafork")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatalf("mkdir data dir: %v", err)
	}
	refreshRequests := 0
	codexRefresh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		refreshRequests++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer codexRefresh.Close()
	t.Setenv("CODEX_ISSUER_URL", codexRefresh.URL)
	future := time.Now().Add(time.Hour).UnixMilli()
	if err := os.WriteFile(filepath.Join(dataDir, "codex-credentials.json"), []byte(fmt.Sprintf(`[
		{"id":"expired-at-only","accessToken":"expired-token","accountId":"expired","expiresAt":1,"priority":0},
		{"id":"fresh-at-only","accessToken":"fresh-token","accountId":"fresh","expiresAt":%d,"priority":1}
	]`, future)), 0o600); err != nil {
		t.Fatalf("write codex creds: %v", err)
	}
	store, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if _, err := store.Patch(map[string]any{"codex": map[string]any{"baseUrl": "https://codex.example"}}); err != nil {
		t.Fatalf("patch config: %v", err)
	}
	codex, err := ResolveProviderAndModel(store, "codex:gpt-5.3-codex")
	if err != nil {
		t.Fatalf("resolve codex: %v", err)
	}
	if codex.Config.CredentialID != "fresh-at-only" || codex.Config.APIKey != "fresh-token" {
		t.Fatalf("expected fresh access-token-only credential, got %#v", codex.Config)
	}
	if refreshRequests != 0 {
		t.Fatalf("access-token-only credentials must not refresh, got %d refresh requests", refreshRequests)
	}
	if len(codex.Config.CredentialPool) != 1 || codex.Config.CredentialPool[0].CredentialID != "fresh-at-only" {
		t.Fatalf("expected expired access-token-only credential excluded from pool, got %#v", codex.Config.CredentialPool)
	}
}

func TestResolverLoadsCodexAndClineCredentialsFromDisk(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dataDir := filepath.Join(home, ".narrafork")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatalf("mkdir data dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "codex-credentials.json"), []byte(`[{"id":"codex-a","accessToken":"codex-disk-token","accountId":"acct_disk","priority":0}]`), 0o600); err != nil {
		t.Fatalf("write codex creds: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "cline-credentials.json"), []byte(`{"accessToken":"cline-disk-token"}`), 0o600); err != nil {
		t.Fatalf("write cline creds: %v", err)
	}
	store, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if _, err := store.Patch(map[string]any{
		"codex":          map[string]any{"baseUrl": "https://codex.example"},
		"clineProviders": []any{map[string]any{"baseUrl": "https://cline.example", "defaultModel": "anthropic/claude"}},
	}); err != nil {
		t.Fatalf("patch config: %v", err)
	}
	codex, err := ResolveProviderAndModel(store, "codex:gpt-5.3-codex")
	if err != nil {
		t.Fatalf("resolve disk codex: %v", err)
	}
	if codex.Config.APIKey != "codex-disk-token" || codex.Config.AccountID != "acct_disk" {
		t.Fatalf("unexpected disk codex provider: %#v", codex.Config)
	}
	cline, err := ResolveProviderAndModel(store, "cline:anthropic/claude")
	if err != nil {
		t.Fatalf("resolve disk cline: %v", err)
	}
	if cline.Config.APIKey != "cline-disk-token" {
		t.Fatalf("unexpected disk cline provider: %#v", cline.Config)
	}
}

func TestResolverReturnsDedicatedProviderKinds(t *testing.T) {
	store := testConfigStore(t, map[string]any{
		"nkpProviders":   []any{map[string]any{"apiKey": "nkp-key", "baseUrl": "https://nkp.example", "defaultModel": "claude"}},
		"clineProviders": []any{map[string]any{"accessToken": "cline-token", "baseUrl": "https://cline.example", "defaultModel": "anthropic/claude"}},
		"nugProviders":   []any{map[string]any{"apiKey": "nug-key", "baseUrl": "https://nug.example", "defaultModel": "gpt", "channelType": "openai"}},
		"codexProviders": []any{map[string]any{"accessToken": "codex-token", "baseUrl": "https://codex.example", "defaultModel": "gpt-5.3-codex", "accountId": "acct"}},
	})
	cases := map[string]string{
		"nkp:claude":          "nkp",
		"cline:anthropic/foo": "cline",
		"nug:gpt":             "nug",
		"codex:gpt-5.3-codex": "codex",
	}
	for model, wantKind := range cases {
		resolution, err := ResolveProviderAndModel(store, model)
		if err != nil {
			t.Fatalf("ResolveProviderAndModel(%s) error: %v", model, err)
		}
		if resolution.Config.Kind != wantKind {
			t.Fatalf("ResolveProviderAndModel(%s) kind=%s want %s", model, resolution.Config.Kind, wantKind)
		}
	}
}
