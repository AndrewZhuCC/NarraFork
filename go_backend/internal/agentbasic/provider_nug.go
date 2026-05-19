package agentbasic

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type NugProviderAdapter struct {
	Provider Provider
}

func (a *NugProviderAdapter) delegate() ProviderAdapter {
	mode := strings.ToLower(strings.TrimSpace(a.Provider.APIMode))
	provider := a.Provider
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
		return &KiroProviderAdapter{Provider: provider}
	}
}

func (a *NugProviderAdapter) ProviderName() string { return a.Provider.Prefix }
func (a *NugProviderAdapter) FormatTools(toolScope string) []map[string]any {
	return a.delegate().FormatTools(toolScope)
}
func (a *NugProviderAdapter) BuildHistory(messages []Message, model string, narratorID string) (BuiltHistory, error) {
	return a.delegate().BuildHistory(messages, model, narratorID)
}
func (a *NugProviderAdapter) InjectSystemPrompt(history *BuiltHistory, systemPrompt string, model string, locale string) {
	a.delegate().InjectSystemPrompt(history, systemPrompt, model, locale)
}
func (a *NugProviderAdapter) Chat(ctx context.Context, params ChatParams) (<-chan StreamEvent, error) {
	inner, err := a.delegate().Chat(ctx, params)
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
	return a.delegate().FormatToolResult(toolUseID, output, isError)
}
func (a *NugProviderAdapter) PushUserTurn(history *BuiltHistory, content string, toolResults []any) {
	a.delegate().PushUserTurn(history, content, toolResults)
}
func (a *NugProviderAdapter) PushAssistantTurn(history *BuiltHistory, text string, toolUses []ToolCall, rawContent any) {
	a.delegate().PushAssistantTurn(history, text, toolUses, rawContent)
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
		detailedBalance := stringValue(payload["detailedQuotaBalance"])
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
	baseURL = strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(baseURL, "/v1") {
		return baseURL
	}
	return baseURL + "/v1"
}
