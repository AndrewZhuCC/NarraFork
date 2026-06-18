package agentbasic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

type AnthropicMessagesAdapter struct {
	Provider Provider
	Client   *http.Client
}

func (a *AnthropicMessagesAdapter) ProviderName() string {
	if a.Provider.Prefix != "" {
		return a.Provider.Prefix
	}
	return "anthropic"
}

func (a *AnthropicMessagesAdapter) FormatTools(toolScope string) []map[string]any {
	return anthropicToolDefinitions(toolScope)
}

func (a *AnthropicMessagesAdapter) BuildHistory(messages []Message, _ string, _ string) (BuiltHistory, error) {
	history := make([]any, 0, len(messages))
	for _, message := range messages {
		role := message.Role
		if role != "assistant" {
			role = "user"
		}
		if message.Role == "tool" {
			if strings.TrimSpace(message.ToolCallID) == "" {
				continue
			}
			history = append(history, map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": message.ToolCallID, "content": message.Content}}})
			continue
		}
		if len(message.ContentBlocks) > 0 || len(message.ToolCalls) > 0 {
			if message.Role == "assistant" {
				if searchContext := webSearchContextFromBlocks(message.ContentBlocks); searchContext != "" {
					history = append(history, map[string]any{"role": "user", "content": searchContext})
				}
			}
			parts := anthropicContentFromBlocks(message.ContentBlocks, message.Content, message.ToolCalls)
			if len(parts) == 0 {
				continue
			}
			history = append(history, map[string]any{"role": role, "content": parts})
			continue
		}
		if strings.TrimSpace(message.Content) == "" {
			continue
		}
		history = append(history, map[string]any{"role": role, "content": message.Content})
	}
	return BuiltHistory{History: history}, nil
}

func (a *AnthropicMessagesAdapter) InjectSystemPrompt(_ *BuiltHistory, _ string, _ string, _ string) {
}

func (a *AnthropicMessagesAdapter) Chat(ctx context.Context, params ChatParams) (<-chan StreamEvent, error) {
	ch := make(chan StreamEvent, 16)
	client := a.Client
	if client == nil {
		client = providerHTTPClient(a.Provider)
	}
	messages := anthropicHistoryMaps(params.History)
	go func() {
		defer close(ch)
		reasoningEffort := firstNonEmpty(params.ReasoningEffort, a.Provider.DefaultReasoningEffort)
		if err := streamAnthropicMessages(ctx, client, a.Provider, params.SystemPrompt, messages, params.ToolScope, reasoningEffort, ch); err != nil {
			ch <- StreamEvent{Err: err}
		}
	}()
	return ch, nil
}

func (a *AnthropicMessagesAdapter) FormatToolResult(toolUseID string, output string, isError bool) any {
	result := map[string]any{"type": "tool_result", "tool_use_id": toolUseID, "content": output}
	if isError {
		result["is_error"] = true
	}
	return result
}

func (a *AnthropicMessagesAdapter) PushUserTurn(history *BuiltHistory, content string, toolResults []any) {
	if history == nil {
		return
	}
	if len(toolResults) > 0 {
		blocks := append([]any(nil), toolResults...)
		if strings.TrimSpace(content) != "" {
			blocks = append(blocks, map[string]any{"type": "text", "text": content})
		}
		history.History = append(history.History, map[string]any{"role": "user", "content": blocks})
		return
	}
	if strings.TrimSpace(content) != "" {
		history.History = append(history.History, map[string]any{"role": "user", "content": content})
	}
}

func (a *AnthropicMessagesAdapter) PushAssistantTurn(history *BuiltHistory, text string, toolUses []ToolCall, rawContent any) {
	if history == nil {
		return
	}
	if rawContent != nil {
		history.History = append(history.History, map[string]any{"role": "assistant", "content": rawContent})
		return
	}
	content := []any{}
	if strings.TrimSpace(text) != "" {
		content = append(content, map[string]any{"type": "text", "text": text})
	}
	for _, toolUse := range toolUses {
		content = append(content, map[string]any{
			"type":  "tool_use",
			"id":    toolUse.ID,
			"name":  toolUse.Function.Name,
			"input": anthropicToolInput(toolUse),
		})
	}
	if len(content) > 0 {
		history.History = append(history.History, map[string]any{"role": "assistant", "content": content})
	}
}

func anthropicHistoryMaps(history *BuiltHistory) []map[string]any {
	if history == nil {
		return nil
	}
	messages := make([]map[string]any, 0, len(history.History))
	for _, item := range history.History {
		if message, ok := item.(map[string]any); ok {
			messages = append(messages, message)
		}
	}
	return messages
}

func cleanAnthropicHistoryForProvider(messages []map[string]any, provider Provider) []map[string]any {
	if provider.OfficialAPI {
		return messages
	}
	cleaned := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
		copyMessage := map[string]any{}
		for key, value := range message {
			copyMessage[key] = value
		}
		if content, ok := message["content"].([]any); ok {
			blocks := []any{}
			for _, raw := range content {
				block, ok := raw.(map[string]any)
				if !ok {
					blocks = append(blocks, raw)
					continue
				}
				switch stringValue(block["type"]) {
				case "thinking", "redacted_thinking", "signature", "thinking_signature":
					continue
				default:
					blocks = append(blocks, block)
				}
			}
			if len(blocks) == 0 {
				continue
			}
			copyMessage["content"] = blocks
		}
		cleaned = append(cleaned, copyMessage)
	}
	return cleaned
}

func anthropicToolInput(toolUse ToolCall) any {
	var input any
	if err := json.Unmarshal([]byte(toolUse.Function.Arguments), &input); err != nil || input == nil {
		return map[string]any{}
	}
	return input
}

type anthropicResponse struct {
	Content    []map[string]any `json:"content"`
	StopReason string           `json:"stop_reason"`
	Usage      *anthropicUsage  `json:"usage"`
	Error      *struct {
		Message string `json:"message"`
	} `json:"error"`
}

type anthropicErrorPayload struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type anthropicUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheCreation            struct {
		Ephemeral5mInputTokens int `json:"ephemeral_5m_input_tokens"`
		Ephemeral1hInputTokens int `json:"ephemeral_1h_input_tokens"`
	} `json:"cache_creation"`
}

type anthropicStreamEvent struct {
	Type    string `json:"type"`
	Index   int    `json:"index"`
	Message struct {
		ID    string          `json:"id"`
		Usage *anthropicUsage `json:"usage"`
	} `json:"message"`
	ContentBlock map[string]any `json:"content_block"`
	Delta        struct {
		Type        string          `json:"type"`
		Text        string          `json:"text"`
		PartialJSON string          `json:"partial_json"`
		Thinking    string          `json:"thinking"`
		Signature   string          `json:"signature"`
		StopReason  string          `json:"stop_reason"`
		Usage       *anthropicUsage `json:"usage"`
	} `json:"delta"`
	Usage *anthropicUsage        `json:"usage"`
	Error *anthropicErrorPayload `json:"error"`
}

type anthropicStreamingBlock struct {
	ID        string
	Name      string
	Arguments string
	Index     int
	Stopped   bool
}

func streamAnthropicMessages(ctx context.Context, client *http.Client, provider Provider, systemPrompt string, messages []map[string]any, toolScope string, reasoningEffort string, out chan<- StreamEvent) error {
	messages = cleanAnthropicHistoryForProvider(messages, provider)
	body := map[string]any{
		"model":      provider.Model,
		"max_tokens": anthropicMaxTokens(provider.Model),
		"system":     anthropicSystemBlocks(systemPrompt),
		"messages":   messages,
		"tools":      anthropicToolDefinitions(toolScope),
		"stream":     true,
	}
	thinking := anthropicThinkingConfig(provider.Model, reasoningEffort)
	thinkingEnabled := thinking != nil && thinking["type"] != "disabled"
	if thinking != nil {
		body["thinking"] = thinking
	}
	payload, _ := json.Marshal(body)
	endpoint := strings.TrimRight(provider.BaseURL, "/") + "/messages"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	setAnthropicHeaders(req, provider)
	// The interleaved-thinking beta header must only be sent when thinking is
	// actually enabled (adaptive/enabled), not when it is explicitly disabled.
	if thinkingEnabled {
		req.Header.Set("anthropic-beta", "interleaved-thinking-2025-05-14")
	}
	req.Header.Set("Accept", "text/event-stream")
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		bodyText := readProviderErrorBody(res)
		if bodyText != "" {
			return fmt.Errorf("anthropic provider returned status %d: %s", res.StatusCode, bodyText)
		}
		return fmt.Errorf("anthropic provider returned status %d", res.StatusCode)
	}
	if !isSSEContentType(res.Header) {
		decoded, err := decodeAnthropicNonStream(res)
		if err != nil {
			return err
		}
		if decoded.Usage != nil {
			emitStreamEvent(ctx, out, StreamEvent{Parsed: ParsedStreamEvent{Usage: anthropicUsageSnapshot(decoded.Usage)}})
		}
		textParts := []string{}
		toolCalls := []ToolCall{}
		for _, block := range decoded.Content {
			switch stringValue(block["type"]) {
			case "text":
				if text := stringValue(block["text"]); strings.TrimSpace(text) != "" {
					textParts = append(textParts, text)
				}
			case "tool_use":
				call := anthropicToolCall(block)
				if strings.TrimSpace(call.ID) != "" && strings.TrimSpace(call.Function.Name) != "" {
					toolCalls = append(toolCalls, call)
				}
			}
		}
		emitStreamEvent(ctx, out, StreamEvent{Parsed: ParsedStreamEvent{Text: strings.Join(textParts, "\n"), ToolUses: toolCalls, StopReason: decoded.StopReason}})
		return nil
	}
	blocks := map[int]*anthropicStreamingBlock{}
	messageID := ""
	completed := false
	// Accumulate usage across message_start/message_delta. Anthropic sends the
	// prompt footprint (input_tokens / cache_read / cache_creation) on
	// message_start and output_tokens on message_delta, where message_delta may
	// omit or zero the input/cache fields. Mirror the TS AnthropicUsageAccum: only
	// overwrite input/cache fields when the incoming value is > 0 so a later
	// message_delta does not erase the real values from message_start.
	usageAccum := &anthropicUsageAccum{}
	err = readSSE(ctx, res.Body, 0, func(event sseEvent) error {
		data := strings.TrimSpace(event.Data)
		if data == "" {
			return nil
		}
		if data == "[DONE]" {
			completed = true
			return nil
		}
		var parsed anthropicStreamEvent
		if err := json.Unmarshal([]byte(data), &parsed); err != nil {
			return fmt.Errorf("decode anthropic stream event: %w", err)
		}
		if invalid := anthropicInvalidStateFromError(parsed.Error); invalid != nil {
			emitStreamEvent(ctx, out, StreamEvent{Parsed: ParsedStreamEvent{MessageID: messageID, InvalidState: invalid}})
			return nil
		}
		switch parsed.Type {
		case "message_start":
			messageID = parsed.Message.ID
			if parsed.Message.Usage != nil {
				usageAccum.merge(parsed.Message.Usage)
				emitStreamEvent(ctx, out, StreamEvent{Parsed: ParsedStreamEvent{MessageID: messageID, Usage: usageAccum.snapshot()}})
			}
		case "content_block_start":
			handleAnthropicBlockStart(blocks, parsed.Index, parsed.ContentBlock)
			if stringValue(parsed.ContentBlock["type"]) == "redacted_thinking" {
				outputIndex := parsed.Index
				emitStreamEvent(ctx, out, StreamEvent{Parsed: ParsedStreamEvent{MessageID: messageID, RedactedThinking: &RedactedThinking{Data: firstNonEmpty(stringValue(parsed.ContentBlock["data"]), stringValue(parsed.ContentBlock["text"])), OutputIndex: &outputIndex}}})
			}
		case "content_block_delta":
			handleAnthropicBlockDelta(ctx, out, blocks, parsed.Index, parsed.Delta, messageID)
		case "content_block_stop":
			if block := blocks[parsed.Index]; block != nil && block.Name != "" && !block.Stopped {
				emitStreamEvent(ctx, out, StreamEvent{Parsed: ParsedStreamEvent{MessageID: messageID, ToolUseChunk: &ToolUseChunk{ToolUseID: block.ID, Name: block.Name, Stop: true, OutputIndex: &block.Index, InputCharsTotal: len(block.Arguments)}}})
				block.Stopped = true
			}
		case "message_delta":
			streamEvent := ParsedStreamEvent{MessageID: messageID, StopReason: parsed.Delta.StopReason}
			if parsed.Usage != nil {
				usageAccum.merge(parsed.Usage)
				streamEvent.Usage = usageAccum.snapshot()
			} else if parsed.Delta.Usage != nil {
				usageAccum.merge(parsed.Delta.Usage)
				streamEvent.Usage = usageAccum.snapshot()
			}
			if invalid := anthropicInvalidStateFromStopReason(parsed.Delta.StopReason); invalid != nil {
				streamEvent.InvalidState = invalid
			}
			emitStreamEvent(ctx, out, StreamEvent{Parsed: streamEvent})
		case "message_stop":
			completed = true
			emitStreamEvent(ctx, out, StreamEvent{Parsed: ParsedStreamEvent{MessageID: messageID}})
		}
		return nil
	})
	if err != nil {
		return err
	}
	if !completed {
		emitStreamEvent(ctx, out, StreamEvent{Parsed: ParsedStreamEvent{MessageID: messageID, SilentDisconnect: true}})
	}
	return nil
}

func anthropicSystemBlocks(systemPrompt string) any {
	if strings.TrimSpace(systemPrompt) == "" {
		return systemPrompt
	}
	return []map[string]any{{"type": "text", "text": systemPrompt, "cache_control": map[string]any{"type": "ephemeral"}}}
}

func decodeAnthropicNonStream(res *http.Response) (anthropicResponse, error) {
	var decoded anthropicResponse
	if err := json.NewDecoder(res.Body).Decode(&decoded); err != nil {
		return decoded, err
	}
	if decoded.Error != nil && decoded.Error.Message != "" {
		return decoded, errors.New(decoded.Error.Message)
	}
	return decoded, nil
}

func handleAnthropicBlockStart(blocks map[int]*anthropicStreamingBlock, index int, block map[string]any) {
	if stringValue(block["type"]) != "tool_use" {
		return
	}
	blocks[index] = &anthropicStreamingBlock{ID: stringValue(block["id"]), Name: stringValue(block["name"]), Index: index}
}

func handleAnthropicBlockDelta(ctx context.Context, out chan<- StreamEvent, blocks map[int]*anthropicStreamingBlock, index int, delta struct {
	Type        string          `json:"type"`
	Text        string          `json:"text"`
	PartialJSON string          `json:"partial_json"`
	Thinking    string          `json:"thinking"`
	Signature   string          `json:"signature"`
	StopReason  string          `json:"stop_reason"`
	Usage       *anthropicUsage `json:"usage"`
}, messageID string) {
	outputIndex := index
	switch delta.Type {
	case "text_delta":
		emitStreamEvent(ctx, out, StreamEvent{Parsed: ParsedStreamEvent{MessageID: messageID, Text: delta.Text, TextOutputIndex: &outputIndex}})
	case "thinking_delta":
		emitStreamEvent(ctx, out, StreamEvent{Parsed: ParsedStreamEvent{MessageID: messageID, Reasoning: delta.Thinking, ReasoningOutputIndex: &outputIndex}})
	case "signature_delta":
		emitStreamEvent(ctx, out, StreamEvent{Parsed: ParsedStreamEvent{MessageID: messageID, ReasoningMetadata: map[string]any{"signature": delta.Signature}, ReasoningOutputIndex: &outputIndex}})
	case "input_json_delta":
		block := blocks[index]
		if block == nil {
			block = &anthropicStreamingBlock{ID: fmt.Sprintf("tool_%d", index), Index: index}
			blocks[index] = block
		}
		block.Arguments += delta.PartialJSON
		emitStreamEvent(ctx, out, StreamEvent{Parsed: ParsedStreamEvent{MessageID: messageID, ToolUseChunk: &ToolUseChunk{ToolUseID: block.ID, Name: block.Name, Input: delta.PartialJSON, OutputIndex: &block.Index, InputCharsTotal: len(block.Arguments)}}})
		if !block.Stopped && isParsableJSON(block.Arguments) {
			emitStreamEvent(ctx, out, StreamEvent{Parsed: ParsedStreamEvent{MessageID: messageID, ToolUseChunk: &ToolUseChunk{ToolUseID: block.ID, Name: block.Name, Stop: true, OutputIndex: &block.Index, InputCharsTotal: len(block.Arguments)}}})
			block.Stopped = true
		}
	}
}

func anthropicUsageSnapshot(usage *anthropicUsage) *UsageSnapshot {
	if usage == nil {
		return nil
	}
	cacheCreation := usage.CacheCreationInputTokens
	if cacheCreation == 0 && (usage.CacheCreation.Ephemeral5mInputTokens != 0 || usage.CacheCreation.Ephemeral1hInputTokens != 0) {
		cacheCreation = usage.CacheCreation.Ephemeral5mInputTokens + usage.CacheCreation.Ephemeral1hInputTokens
	}
	// promptTokens is the full prompt footprint (input + cache_read +
	// cache_creation), mirroring TS promptTokensFromAnthropicPayload. This
	// drives context-usage percent; cost/DB input_tokens use InputTokens.
	return &UsageSnapshot{InputTokens: usage.InputTokens, PromptTokens: usage.InputTokens + usage.CacheReadInputTokens + cacheCreation, CompletionTokens: usage.OutputTokens, CachedInputTokens: usage.CacheReadInputTokens, CacheCreationInputTokens: usage.CacheCreationInputTokens, CacheCreation5mTokens: usage.CacheCreation.Ephemeral5mInputTokens, CacheCreation1hTokens: usage.CacheCreation.Ephemeral1hInputTokens}
}

// anthropicUsageAccum accumulates usage across the Anthropic SSE message_start
// and message_delta events. It mirrors the TS updateAnthropicUsageAccum
// (anthropic-provider.ts:459-487): input/cache fields are only overwritten when
// the incoming value is > 0 (so a later message_delta's zero/omitted input/cache
// does not erase the real prompt footprint from message_start), output_tokens is
// taken as the latest cumulative value, and cacheCreationInputTokens is derived
// from the 5m+1h breakdown when the top-level field is absent.
type anthropicUsageAccum struct {
	inputTokens              int
	outputTokens             int
	cachedInputTokens        int
	cacheCreationInputTokens int
	cacheCreation5mTokens    int
	cacheCreation1hTokens    int
}

func (a *anthropicUsageAccum) merge(usage *anthropicUsage) {
	if usage == nil {
		return
	}
	if usage.InputTokens > 0 {
		a.inputTokens = usage.InputTokens
	}
	if usage.CacheReadInputTokens > 0 {
		a.cachedInputTokens = usage.CacheReadInputTokens
	}
	if usage.CacheCreationInputTokens > 0 {
		a.cacheCreationInputTokens = usage.CacheCreationInputTokens
	}
	if usage.OutputTokens != 0 {
		a.outputTokens = usage.OutputTokens
	}
	if usage.CacheCreation.Ephemeral5mInputTokens != 0 {
		a.cacheCreation5mTokens = usage.CacheCreation.Ephemeral5mInputTokens
	}
	if usage.CacheCreation.Ephemeral1hInputTokens != 0 {
		a.cacheCreation1hTokens = usage.CacheCreation.Ephemeral1hInputTokens
	}
	// Derive the top-level cache-creation total from the 5m+1h breakdown when the
	// provider did not send cache_creation_input_tokens directly (mirrors TS
	// anthropic-provider.ts:480-486).
	if usage.CacheCreationInputTokens == 0 && (usage.CacheCreation.Ephemeral5mInputTokens != 0 || usage.CacheCreation.Ephemeral1hInputTokens != 0) {
		a.cacheCreationInputTokens = a.cacheCreation5mTokens + a.cacheCreation1hTokens
	}
}

func (a *anthropicUsageAccum) snapshot() *UsageSnapshot {
	return &UsageSnapshot{
		InputTokens: a.inputTokens,
		// Full prompt footprint (input + cache_read + cache_creation), mirroring
		// TS anthropicPromptFootprintTokens. Drives context-usage percent; the DB
		// input_tokens column and cost use InputTokens, so this is display-only.
		PromptTokens:             a.inputTokens + a.cachedInputTokens + a.cacheCreationInputTokens,
		CompletionTokens:         a.outputTokens,
		CachedInputTokens:        a.cachedInputTokens,
		CacheCreationInputTokens: a.cacheCreationInputTokens,
		CacheCreation5mTokens:    a.cacheCreation5mTokens,
		CacheCreation1hTokens:    a.cacheCreation1hTokens,
	}
}

func anthropicInvalidStateFromError(err *anthropicErrorPayload) *InvalidState {
	if err == nil {
		return nil
	}
	return &InvalidState{Reason: firstNonEmpty(err.Type, "api_error"), Message: firstNonEmpty(err.Message, "Anthropic API error")}
}

func anthropicInvalidStateFromStopReason(stopReason string) *InvalidState {
	switch stopReason {
	case "max_tokens":
		return &InvalidState{Reason: "max_tokens", Message: "Response truncated: model reached maximum token limit."}
	case "model_context_window_exceeded":
		return &InvalidState{Reason: "model_context_window_exceeded", Message: "The model has reached its context window limit."}
	case "refusal":
		return &InvalidState{Reason: "refusal", Message: "Claude is unable to respond to this request, which appears to violate the Usage Policy."}
	default:
		return nil
	}
}

// anthropicSupportsThinking mirrors the TS supportsThinking (anthropic-provider.ts):
// extended thinking is supported by DeepSeek, Claude 3.7 Sonnet, and the Claude 4+
// families (opus-4/sonnet-4/haiku-4).
func anthropicSupportsThinking(model string) bool {
	lower := strings.ToLower(model)
	if strings.Contains(lower, "deepseek") {
		return true
	}
	if strings.Contains(lower, "3-7") || strings.Contains(lower, "3.7") {
		return true
	}
	if strings.Contains(lower, "opus-4") || strings.Contains(lower, "sonnet-4") || strings.Contains(lower, "haiku-4") {
		return true
	}
	return false
}

// anthropicIsDeepSeekModel mirrors the TS isDeepSeekModel (types.ts).
func anthropicIsDeepSeekModel(model string) bool {
	return isDeepSeekModelName(model)
}

// isDeepSeekModelName mirrors the TS isDeepSeekModel (types.ts): a model id whose
// (case-insensitive) name contains "deepseek".
func isDeepSeekModelName(model string) bool {
	return strings.Contains(strings.ToLower(model), "deepseek")
}

// mapDeepSeekEffort mirrors the TS mapDeepSeekEffort (types.ts): none/empty -> "",
// xhigh -> "max", everything else -> "high". DeepSeek's Anthropic/OpenAI-compatible
// API exposes only "high" and "max" thinking-intensity tiers.
func mapDeepSeekEffort(reasoningEffort string) string {
	effort := normalizeReasoningEffort(reasoningEffort)
	if effort == "" || effort == "none" {
		return ""
	}
	if effort == "xhigh" {
		return "max"
	}
	return "high"
}

// anthropicThinkingConfig mirrors the TS buildThinkingConfig (anthropic-provider.ts,
// matching Claude Code v2.1.71):
//   - models that don't support thinking → nil (no thinking field)
//   - reasoningEffort == "none" → {type:"disabled"} (thinking explicitly off)
//   - DeepSeek → {type:"enabled", budget_tokens:10000} (DeepSeek ignores the value
//     but the schema requires it; effort is controlled via output_config elsewhere)
//   - all other supported models → {type:"adaptive"}
//
// The previous Go implementation used the deprecated {type:"enabled",
// budget_tokens:N} form (1024/4096/8192 by effort) for EVERY model regardless of
// thinking support, and returned nil for "none" (so thinking-capable models could
// silently default thinking on instead of being explicitly disabled).
// anthropicMaxTokens mirrors the TS getTokenLimits default (anthropic-provider.ts):
// the main streaming chat request sends max_tokens = 32000 for every model (the
// `default` tier; the 64000 `upperLimit` for Claude 4 families is not used in the
// request body). Go previously hardcoded 4096, which truncated long responses on
// the main chat path. The non-streaming generate/title/summary helper
// (doAnthropicRequest) keeps 4096, matching the TS generate helper.
func anthropicMaxTokens(model string) int {
	return 32000
}

func anthropicThinkingConfig(model, reasoningEffort string) map[string]any {
	if strings.TrimSpace(model) == "" {
		return nil
	}
	if !anthropicSupportsThinking(model) {
		return nil
	}
	if normalizeReasoningEffort(reasoningEffort) == "none" {
		return map[string]any{"type": "disabled"}
	}
	if anthropicIsDeepSeekModel(model) {
		return map[string]any{"type": "enabled", "budget_tokens": 10000}
	}
	return map[string]any{"type": "adaptive"}
}

func doAnthropicRequest(ctx context.Context, client *http.Client, provider Provider, systemPrompt string, messages []map[string]any, toolScope string) (anthropicResponse, error) {
	body := map[string]any{
		"model":      provider.Model,
		"max_tokens": 4096,
		"system":     systemPrompt,
		"messages":   messages,
		"tools":      anthropicToolDefinitions(toolScope),
	}
	payload, _ := json.Marshal(body)
	endpoint := strings.TrimRight(provider.BaseURL, "/") + "/messages"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return anthropicResponse{}, err
	}
	setAnthropicHeaders(req, provider)
	res, err := client.Do(req)
	if err != nil {
		return anthropicResponse{}, err
	}
	defer res.Body.Close()
	var decoded anthropicResponse
	if err := json.NewDecoder(res.Body).Decode(&decoded); err != nil {
		return anthropicResponse{}, err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		if decoded.Error != nil && decoded.Error.Message != "" {
			return anthropicResponse{}, errors.New(decoded.Error.Message)
		}
		return anthropicResponse{}, fmt.Errorf("anthropic provider returned status %d", res.StatusCode)
	}
	return decoded, nil
}

func setAnthropicHeaders(req *http.Request, provider Provider) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	if provider.OfficialAPI {
		req.Header.Set("Authorization", "Bearer "+provider.APIKey)
	} else {
		req.Header.Set("x-api-key", provider.APIKey)
	}
}

func anthropicToolCall(block map[string]any) ToolCall {
	var call ToolCall
	call.ID = stringValue(block["id"])
	call.Type = "function"
	call.Function.Name = stringValue(block["name"])
	args, err := json.Marshal(block["input"])
	if err != nil || string(args) == "null" {
		args = []byte("{}")
	}
	call.Function.Arguments = string(args)
	return call
}
