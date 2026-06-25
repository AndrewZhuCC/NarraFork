package agentbasic

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type NugProviderAdapter struct {
	Provider Provider
}

func (a *NugProviderAdapter) delegate(extraHeaders map[string]string) ProviderAdapter {
	mode := strings.ToLower(strings.TrimSpace(a.Provider.APIMode))
	provider := a.Provider
	provider.ExtraHeaders = extraHeaders
	switch mode {
	case "anthropic":
		provider.Kind = "anthropic"
		provider.BaseURL = nugV1BaseURL(provider.BaseURL)
		return &AnthropicMessagesAdapter{Provider: provider}
	case "openai", "completions":
		provider.Kind = "openai"
		provider.APIMode = "completions"
		provider.BaseURL = nugV1BaseURL(provider.BaseURL)
		return &OpenAIChatCompletionsAdapter{Provider: provider}
	case "codex":
		provider.Kind = "codex"
		provider.APIMode = "codex"
		provider.BaseURL = nugV1BaseURL(provider.BaseURL)
		return &OpenAIChatCompletionsAdapter{Provider: provider}
	case "responses":
		provider.Kind = "openai"
		provider.APIMode = "responses"
		provider.BaseURL = nugV1BaseURL(provider.BaseURL)
		return &OpenAIChatCompletionsAdapter{Provider: provider}
	default:
		provider.Kind = "kiro"
		// NUG kiro channel: the kiro request uses the bare model as its internal
		// modelId, while the HTTP body is wrapped with a top-level "model" field
		// set to the channel-prefixed routedModel. Mirrors TS nug-provider.ts
		// chatKiro (buildKiroRequest model: meta.bareModel; body model: meta.routedModel).
		routedModel := provider.Model
		provider.Model = stripNugChannelPrefix(routedModel, provider.APIMode)
		provider.NugKiroRoutedModel = routedModel
		return &KiroProviderAdapter{Provider: provider}
	}
}

// stripNugChannelPrefix removes a leading "<channel>:" from a NUG model value,
// yielding the bare upstream model. e.g. ("kiro:claude-opus-4.8", "kiro") ->
// "claude-opus-4.8". Returns the input unchanged when it carries no such prefix
// (already a bare model) or when the channel is empty.
func stripNugChannelPrefix(model, channel string) string {
	model = strings.TrimSpace(model)
	channel = strings.TrimSpace(channel)
	if channel == "" {
		return model
	}
	prefix := channel + ":"
	if strings.HasPrefix(model, prefix) {
		return strings.TrimSpace(model[len(prefix):])
	}
	return model
}

// nugChannelIsKiro reports whether the NUG channel routes through the kiro
// native protocol (the delegate() default branch), mirroring TS where the full
// chatHeaders (X-Conversation-ID + X-Kiro-Effort) only apply to the kiro path.
func nugChannelIsKiro(apiMode string) bool {
	switch strings.ToLower(strings.TrimSpace(apiMode)) {
	case "anthropic", "openai", "completions", "codex", "responses":
		return false
	default:
		return true
	}
}

// nugChatExtraHeaders builds the NUG-specific request headers. X-NUG-Model-Hash
// is sent on every channel (mirrors TS createDelegate extraHeaders); the kiro
// native channel additionally sends X-Conversation-ID (mirrors TS chatHeaders,
// which is only used on the kiro path). X-Kiro-Effort is already emitted by the
// kiro request path itself, so it is not added here.
func nugChatExtraHeaders(provider Provider, params ChatParams) map[string]string {
	headers := map[string]string{"X-NUG-Model-Hash": nugCachedModelHash(provider.ID)}
	if nugChannelIsKiro(provider.APIMode) {
		if conversationID := strings.TrimSpace(params.ConversationID); conversationID != "" {
			headers["X-Conversation-ID"] = conversationID
		}
	}
	return headers
}

func (a *NugProviderAdapter) ProviderName() string { return a.Provider.Prefix }
func (a *NugProviderAdapter) FormatTools(toolScope string) []map[string]any {
	return a.delegate(nil).FormatTools(toolScope)
}
func (a *NugProviderAdapter) BuildHistory(messages []Message, model string, narratorID string) (BuiltHistory, error) {
	return a.delegate(nil).BuildHistory(messages, a.delegateModel(model), narratorID)
}
func (a *NugProviderAdapter) InjectSystemPrompt(history *BuiltHistory, systemPrompt string, model string, locale string) {
	a.delegate(nil).InjectSystemPrompt(history, systemPrompt, a.delegateModel(model), locale)
}

// delegateModel converts the loop-provided (channel-prefixed) model into the
// value the delegate expects. For the kiro channel the delegate uses the bare
// model as its internal modelId (mirrors TS buildHistory/injectSystemPrompt
// using meta.bareModel); other channels keep the model unchanged.
func (a *NugProviderAdapter) delegateModel(model string) string {
	if nugChannelIsKiro(a.Provider.APIMode) {
		return stripNugChannelPrefix(model, a.Provider.APIMode)
	}
	return model
}
func (a *NugProviderAdapter) Chat(ctx context.Context, params ChatParams) (<-chan StreamEvent, error) {
	inner, err := a.delegate(nugChatExtraHeaders(a.Provider, params)).Chat(ctx, params)
	if err != nil {
		return nil, err
	}
	out := make(chan StreamEvent, 16)
	go func() {
		defer close(out)
		emitNugUsageSummary(ctx, out, a.Provider)
		for event := range inner {
			emitStreamEvent(ctx, out, event)
		}
	}()
	return out, nil
}
func (a *NugProviderAdapter) FormatToolResult(toolUseID string, output string, isError bool) any {
	return a.delegate(nil).FormatToolResult(toolUseID, output, isError)
}
func (a *NugProviderAdapter) PushUserTurn(history *BuiltHistory, content string, toolResults []any) {
	a.delegate(nil).PushUserTurn(history, content, toolResults)
}
func (a *NugProviderAdapter) PushAssistantTurn(history *BuiltHistory, text string, toolUses []ToolCall, rawContent any) {
	a.delegate(nil).PushAssistantTurn(history, text, toolUses, rawContent)
}

func emitNugUsageSummary(ctx context.Context, out chan<- StreamEvent, provider Provider) {
	if strings.TrimSpace(provider.BaseURL) == "" || strings.TrimSpace(provider.APIKey) == "" {
		return
	}
	client := &http.Client{Timeout: 2 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, nugV1BaseURL(provider.BaseURL)+"/usage/summary", nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+provider.APIKey)
	res, err := client.Do(req)
	if err != nil {
		return
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return
	}
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1024*1024))
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return
	}
	payload := decoded
	if nested, _ := decoded["data"].(map[string]any); nested != nil {
		payload = nested
	}
	if balance := firstNonEmpty(stringValue(payload["quotaBalance"]), stringValue(payload["remaining"]), stringValue(payload["creditsRemaining"])); balance != "" {
		detailedBalance := firstNonEmpty(stringValue(payload["detailedQuotaBalance"]), formatExtraDetails(payload["extra"]))
		emitStreamEvent(ctx, out, StreamEvent{Parsed: ParsedStreamEvent{QuotaBalance: &balance, DetailedQuotaBalance: &detailedBalance}})
	}
	if usage, ok := numericAsFloat(payload["usage"]); ok {
		emitStreamEvent(ctx, out, StreamEvent{Parsed: ParsedStreamEvent{Metering: &MeteringSnapshot{Unit: firstNonEmpty(stringValue(payload["unit"]), "credit"), UnitPlural: firstNonEmpty(stringValue(payload["unitPlural"]), "credits"), Usage: usage}}})
	}
	if used, ok := numericAsFloat(payload["used"]); ok {
		emitStreamEvent(ctx, out, StreamEvent{Parsed: ParsedStreamEvent{Metering: &MeteringSnapshot{Unit: firstNonEmpty(stringValue(payload["unit"]), "credit"), UnitPlural: firstNonEmpty(stringValue(payload["unitPlural"]), "credits"), Usage: used}}})
	}
}

func numericAsFloat(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case int:
		return float64(typed), true
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	case string:
		var parsed float64
		if _, err := fmt.Sscanf(typed, "%f", &parsed); err == nil {
			return parsed, true
		}
	}
	return 0, false
}

func nugV1BaseURL(baseURL string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	baseURL = strings.TrimSuffix(strings.TrimSuffix(baseURL, "/api/v1"), "/v1")
	baseURL = strings.TrimSuffix(baseURL, "/api")
	return baseURL + "/v1"
}

// nugUnknownModelHash mirrors the TS NUG_UNKNOWN_MODEL_HASH fallback.
const nugUnknownModelHash = "none"

// nugModelHashCache holds the parsed provider->hash map from
// nug-models-providers.json, validated by the file's (mtime, size). The TS side
// keeps this map in process memory and only reloads it when the catalog is
// rewritten; mirror that intent so the per-request hot path (nugChatExtraHeaders
// on every NUG Chat) does a cheap os.Stat instead of a full ReadFile +
// json.Unmarshal. A change in mtime or size invalidates the cache.
var nugModelHashCache struct {
	mu      sync.Mutex
	path    string
	modTime time.Time
	size    int64
	loaded  bool
	hashes  map[string]string
}

// nugModelHashesFromFile returns the provider->hash map, re-reading and parsing
// the catalog file only when its (mtime, size) changed since the last load. A
// missing/corrupt file yields an empty map (cached as such until it changes).
func nugModelHashesFromFile(path string) map[string]string {
	nugModelHashCache.mu.Lock()
	defer nugModelHashCache.mu.Unlock()

	info, statErr := os.Stat(path)
	if statErr == nil && nugModelHashCache.loaded && nugModelHashCache.path == path &&
		nugModelHashCache.modTime.Equal(info.ModTime()) && nugModelHashCache.size == info.Size() {
		return nugModelHashCache.hashes
	}

	hashes := map[string]string{}
	data, readErr := os.ReadFile(path)
	if readErr == nil {
		var decoded struct {
			Providers map[string]struct {
				ModelHash string `json:"modelHash"`
				Hash      string `json:"hash"`
			} `json:"providers"`
		}
		if json.Unmarshal(data, &decoded) == nil {
			for id, entry := range decoded.Providers {
				if hash := strings.TrimSpace(firstNonEmpty(entry.ModelHash, entry.Hash)); hash != "" {
					hashes[id] = hash
				}
			}
		}
	}

	nugModelHashCache.path = path
	nugModelHashCache.hashes = hashes
	nugModelHashCache.loaded = true
	if statErr == nil {
		nugModelHashCache.modTime = info.ModTime()
		nugModelHashCache.size = info.Size()
	} else {
		// File missing: reset the validators so it reloads once the file appears.
		nugModelHashCache.modTime = time.Time{}
		nugModelHashCache.size = 0
	}
	return hashes
}

// nugCachedModelHash reads the cached NUG model-catalog hash for a provider id
// from ~/.narrafork/nug-models-providers.json (same file the systemroutes model
// cache writes), mirroring TS getNugCachedModelHash(config.id) ?? "none". Returns
// "none" when the id is empty, the file is missing, or no hash is cached. The
// parsed map is cached in process memory and only reloaded when the file's
// (mtime, size) changes, so the per-request path avoids a synchronous read+parse.
func nugCachedModelHash(providerID string) string {
	if strings.TrimSpace(providerID) == "" {
		return nugUnknownModelHash
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nugUnknownModelHash
	}
	path := filepath.Join(home, ".narrafork", "nug-models-providers.json")
	if hash, ok := nugModelHashesFromFile(path)[providerID]; ok && hash != "" {
		return hash
	}
	return nugUnknownModelHash
}
