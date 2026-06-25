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
		Type             string          `json:"type"`
		Text             string          `json:"text"`
		PartialJSON      string          `json:"partial_json"`
		Thinking         string          `json:"thinking"`
		ReasoningContent string          `json:"reasoning_content"`
		Signature        string          `json:"signature"`
		StopReason       string          `json:"stop_reason"`
		Usage            *anthropicUsage `json:"usage"`
	} `json:"delta"`
	Usage *anthropicUsage        `json:"usage"`
	Error *anthropicErrorPayload `json:"error"`
}

type anthropicStreamingBlock struct {
	ID         string
	Name       string
	Arguments  string
	Index      int
	Stopped    bool
	IsThinking bool
}

func streamAnthropicMessages(ctx context.Context, client *http.Client, provider Provider, systemPrompt string, messages []map[string]any, toolScope string, reasoningEffort string, out chan<- StreamEvent) error {
	messages = cleanAnthropicHistoryForProvider(messages, provider)
	tools := anthropicToolDefinitions(toolScope)
	// Apply prompt-cache breakpoints on tools + message-history prefix only for the
	// official API (proxies do not support cache_control), mirroring TS isOfficial
	// gating. The system block carries cache_control via anthropicSystemBlocks.
	if provider.OfficialAPI {
		applyAnthropicCacheBreakpoints(messages, tools)
	}
	body := map[string]any{
		"model":      provider.Model,
		"max_tokens": anthropicMaxTokens(provider.Model),
		"system":     anthropicSystemBlocks(systemPrompt, provider.OfficialAPI),
		"messages":   messages,
		"tools":      tools,
		"stream":     true,
	}
	thinking := anthropicThinkingConfig(provider.Model, reasoningEffort)
	thinkingEnabled := thinking != nil && thinking["type"] != "disabled"
	if thinking != nil {
		body["thinking"] = thinking
	}
	// Temperature: only set when thinking is disabled (Anthropic rejects temperature
	// alongside thinking). Mirrors TS anthropic-provider.ts:886-889 (temperature=1).
	if !thinkingEnabled {
		body["temperature"] = 1
	}
	// output_config.effort: effort-capable models (Opus 4.6 / Sonnet 4.6) and
	// DeepSeek carry their thinking-intensity via output_config.effort when thinking
	// is enabled (mirrors TS anthropic-provider.ts:894-906). The two branches are
	// mutually exclusive (DeepSeek is not an adaptive-thinking model).
	if thinkingEnabled {
		if anthropicSupportsEffort(provider.Model) {
			effort := mapAnthropicEffortParam(reasoningEffort)
			if effort == "" {
				effort = "medium"
			}
			body["output_config"] = map[string]any{"effort": effort}
		} else if anthropicIsDeepSeekModel(provider.Model) {
			if effort := mapDeepSeekEffort(reasoningEffort); effort != "" {
				body["output_config"] = map[string]any{"effort": effort}
			}
		}
	}
	payload, _ := json.Marshal(body)
	endpoint := strings.TrimRight(provider.BaseURL, "/") + "/messages"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	setAnthropicHeaders(req, provider)
	// The interleaved-thinking beta header must only be sent when thinking is
	// actually enabled (adaptive/enabled), not when it is explicitly disabled. For
	// effort-capable models, also declare the adaptive-thinking/effort beta flags so
	// output_config.effort is honored (mirrors TS, which sends ANTHROPIC_EFFORT_BETA_FLAGS
	// for supportsEffort models). The full Claude Code official beta flag set
	// (claude-code-*/context-1m/context-management) remains a tracked official-spoof
	// boundary (see W771 anthropicOfficialThreeBlockSystemSpoof) and is NOT sent here.
	if thinkingEnabled {
		betaFlags := "interleaved-thinking-2025-05-14"
		if anthropicSupportsEffort(provider.Model) {
			betaFlags += ",adaptive-thinking-2026-01-28,effort-2025-11-24"
		}
		req.Header.Set("anthropic-beta", betaFlags)
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
			initialThinking := handleAnthropicBlockStart(blocks, parsed.Index, parsed.ContentBlock)
			if initialThinking != "" {
				outputIndex := parsed.Index
				emitStreamEvent(ctx, out, StreamEvent{Parsed: ParsedStreamEvent{MessageID: messageID, Reasoning: initialThinking, ReasoningMetadata: map[string]any{"blockIndex": parsed.Index}, ReasoningOutputIndex: &outputIndex}})
			}
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

// anthropicSystemBlocks builds the top-level `system` field. Mirrors the TS
// anthropic-provider system construction: prompt caching (cache_control) is
// gated on the official API. For the official API the system block carries
// cache_control:{type:ephemeral}; for proxy/non-official providers the system is
// a plain text block with NO cache_control (proxies do not support it), matching
// TS which only applies cache_control when isOfficial.
func anthropicSystemBlocks(systemPrompt string, officialAPI bool) any {
	if strings.TrimSpace(systemPrompt) == "" {
		return systemPrompt
	}
	block := map[string]any{"type": "text", "text": systemPrompt}
	if officialAPI {
		block["cache_control"] = map[string]any{"type": "ephemeral"}
	}
	return []map[string]any{block}
}

// applyAnthropicCacheBreakpoints places cache_control breakpoints on the last
// tool definition and the last non-thinking content block of the message-history
// prefix (skipping the current user turn), mirroring the TS applyCacheBreakpoints
// (anthropic-provider.ts). It first strips any existing cache_control from
// message content blocks. Only called for the official API (proxies do not
// support cache_control). At most one message breakpoint is placed (the system
// already uses cache_control, keeping within Anthropic's 4-block limit).
func applyAnthropicCacheBreakpoints(messages []map[string]any, tools []map[string]any) {
	// Strip any existing cache_control from all message content blocks.
	for _, msg := range messages {
		content, ok := msg["content"].([]any)
		if !ok {
			continue
		}
		for _, raw := range content {
			if block, ok := raw.(map[string]any); ok {
				delete(block, "cache_control")
			}
		}
	}

	// Breakpoint 1: last tool definition.
	if len(tools) > 0 {
		tools[len(tools)-1]["cache_control"] = map[string]any{"type": "ephemeral"}
	}

	// Breakpoint 2: last non-thinking content block in the message-history prefix
	// (skip the current user turn at messages[len-1]). Place at most one.
	placed := 0
	for i := len(messages) - 2; i >= 0 && placed < 1; i-- {
		content := messages[i]["content"]
		if blocks, ok := content.([]any); ok && len(blocks) > 0 {
			targetIdx := -1
			for j := len(blocks) - 1; j >= 0; j-- {
				block, ok := blocks[j].(map[string]any)
				if !ok {
					continue
				}
				blockType, _ := block["type"].(string)
				if blockType != "thinking" && blockType != "redacted_thinking" {
					targetIdx = j
					break
				}
			}
			if targetIdx >= 0 {
				if block, ok := blocks[targetIdx].(map[string]any); ok {
					block["cache_control"] = map[string]any{"type": "ephemeral"}
					placed++
				}
			}
		} else if text, ok := content.(string); ok {
			messages[i]["content"] = []any{map[string]any{"type": "text", "text": text, "cache_control": map[string]any{"type": "ephemeral"}}}
			placed++
		}
	}
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

// handleAnthropicBlockStart registers a streaming content block. tool_use blocks
// are tracked for input-json accumulation. thinking blocks are tracked so that
// subsequent text_delta/reasoning_content deltas on the same index are routed to
// the reasoning channel (some Anthropic-compatible relays, e.g. DeepSeek over the
// Anthropic protocol, stream thinking via a thinking block + text_delta rather
// than thinking_delta). Returns the initial thinking text to emit (non-empty only
// for a thinking block that carries it on the start event), mirroring TS
// anthropic-provider.ts content_block_start handling.
func handleAnthropicBlockStart(blocks map[int]*anthropicStreamingBlock, index int, block map[string]any) string {
	switch stringValue(block["type"]) {
	case "tool_use":
		blocks[index] = &anthropicStreamingBlock{ID: stringValue(block["id"]), Name: stringValue(block["name"]), Index: index}
		return ""
	case "thinking":
		blocks[index] = &anthropicStreamingBlock{Index: index, IsThinking: true}
		return firstNonEmpty(stringValue(block["thinking"]), stringValue(block["reasoning_content"]), stringValue(block["text"]))
	default:
		return ""
	}
}

func handleAnthropicBlockDelta(ctx context.Context, out chan<- StreamEvent, blocks map[int]*anthropicStreamingBlock, index int, delta struct {
	Type             string          `json:"type"`
	Text             string          `json:"text"`
	PartialJSON      string          `json:"partial_json"`
	Thinking         string          `json:"thinking"`
	ReasoningContent string          `json:"reasoning_content"`
	Signature        string          `json:"signature"`
	StopReason       string          `json:"stop_reason"`
	Usage            *anthropicUsage `json:"usage"`
}, messageID string) {
	outputIndex := index
	// Thinking pre-check (mirrors TS isThinkingBlockDelta + thinking??reasoning_content??text):
	// official streams use thinking_delta; some Anthropic-compatible relays stream a
	// thinking block via text_delta or reasoning_content. When the delta is a
	// thinking_delta OR the current block was opened as a thinking block, route the
	// text (thinking/reasoning_content/text precedence) to the reasoning channel
	// instead of visible assistant text. Empty thinking deltas are dropped (noise).
	isThinking := delta.Type == "thinking_delta" || (blocks[index] != nil && blocks[index].IsThinking)
	thinkingText := firstNonEmpty(delta.Thinking, delta.ReasoningContent, delta.Text)
	if isThinking && thinkingText != "" {
		emitStreamEvent(ctx, out, StreamEvent{Parsed: ParsedStreamEvent{MessageID: messageID, Reasoning: thinkingText, ReasoningMetadata: map[string]any{"blockIndex": index}, ReasoningOutputIndex: &outputIndex}})
		return
	}
	switch delta.Type {
	case "text_delta":
		emitStreamEvent(ctx, out, StreamEvent{Parsed: ParsedStreamEvent{MessageID: messageID, Text: delta.Text, TextOutputIndex: &outputIndex}})
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
	if effort == "xhigh" || effort == "max" {
		return "max"
	}
	return "high"
}

// anthropicSupportsEffort mirrors the TS supportsAdaptiveThinking/supportsEffort
// (anthropic-provider.ts): only the adaptive-thinking models (Opus 4.6 / Sonnet
// 4.6) accept the output_config.effort parameter and the effort beta flags.
func anthropicSupportsEffort(model string) bool {
	lower := strings.ToLower(model)
	return strings.Contains(lower, "opus-4-6") ||
		strings.Contains(lower, "opus-4.6") ||
		strings.Contains(lower, "sonnet-4-6") ||
		strings.Contains(lower, "sonnet-4.6")
}

// mapAnthropicEffortParam mirrors the TS mapEffortParam (anthropic-provider.ts):
// none/empty -> "", low -> low, medium -> medium, high -> high, xhigh -> high
// (Anthropic has no xhigh tier), max -> max.
func mapAnthropicEffortParam(reasoningEffort string) string {
	switch normalizeReasoningEffort(reasoningEffort) {
	case "", "none":
		return ""
	case "low":
		return "low"
	case "medium":
		return "medium"
	case "high", "xhigh":
		return "high"
	case "max":
		return "max"
	default:
		return ""
	}
}

// anthropicThinkingConfig mirrors the TS buildThinkingConfig (anthropic-provider.ts,
// matching Claude Code v2.1.71):
//   - models that don't support thinking → nil (no thinking field)
//   - reasoningEffort == "none" → {type:"disabled"} (thinking explicitly off)
//   - DeepSeek → {type:"enabled", budget_tokens:10000} (DeepSeek ignores the value
//     but the schema requires it; effort is controlled via output_config.effort,
//     set in streamAnthropicMessages)
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
// the main chat path.
//
// Architectural divergence (W800, tracked via capability
// narrator.agentRuntime.anthropicGenerateReusesAgentLoop): TS has a dedicated
// lightweight non-streaming generate path (generateWithMeta -> /messages,
// max_tokens:4096, no tools) for title/summary helpers. Go has NO such path:
// title/summary generation reuses the full agent loop (agentbasic.Generate ->
// RunAgentLoop -> streamAnthropicMessages), so every Anthropic generate also
// sends max_tokens 32000 with readonly tools. The previous comment here claimed a
// non-streaming "doAnthropicRequest" helper kept 4096 "matching the TS generate
// helper"; that helper was dead code (never called) and has been removed, so this
// 32000 value is the only Anthropic request-body max_tokens Go ever sends.
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

func setAnthropicHeaders(req *http.Request, provider Provider) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	if provider.OfficialAPI {
		req.Header.Set("Authorization", "Bearer "+provider.APIKey)
	} else {
		req.Header.Set("x-api-key", provider.APIKey)
	}
	applyProviderExtraHeaders(req, provider)
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
