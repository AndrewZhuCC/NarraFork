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

type OpenAIChatCompletionsAdapter struct {
	Provider Provider
	Client   *http.Client
}

func (a *OpenAIChatCompletionsAdapter) ProviderName() string {
	if a.Provider.Prefix != "" {
		return a.Provider.Prefix
	}
	return "openai"
}

func (a *OpenAIChatCompletionsAdapter) FormatTools(toolScope string) []map[string]any {
	return toolDefinitions(toolScope)
}

func (a *OpenAIChatCompletionsAdapter) BuildHistory(messages []Message, _ string, _ string) (BuiltHistory, error) {
	history := make([]any, 0, len(messages))
	for _, message := range messages {
		history = append(history, message)
	}
	return BuiltHistory{History: history}, nil
}

func (a *OpenAIChatCompletionsAdapter) InjectSystemPrompt(history *BuiltHistory, systemPrompt string, _ string, _ string) {
	if history == nil || systemPrompt == "" {
		return
	}
	messages := make([]any, 0, len(history.History)+1)
	messages = append(messages, Message{Role: "system", Content: systemPrompt})
	messages = append(messages, history.History...)
	history.History = messages
}

func (a *OpenAIChatCompletionsAdapter) Chat(ctx context.Context, params ChatParams) (<-chan StreamEvent, error) {
	ch := make(chan StreamEvent, 16)
	client := a.Client
	if client == nil {
		client = defaultProviderHTTPClient()
	}
	messages := openAIHistoryMessages(params.History)
	go func() {
		defer close(ch)
		apiMode := strings.ToLower(strings.TrimSpace(a.Provider.APIMode))
		var err error
		if strings.TrimSpace(a.Provider.Kind) == "codex" {
			sessionKey := firstNonEmpty(params.StickySessionKey, params.ConversationID)
			if len(a.Provider.CredentialPool) > 0 {
				err = streamCodexWithFailover(ctx, client, a.Provider, messages, params.ReasoningEffort, params.ToolScope, sessionKey, ch)
			} else {
				err = streamCodexCandidate(ctx, client, a.Provider, messages, params.ReasoningEffort, params.ToolScope, ch)
				if err == nil && a.Provider.CredentialID != "" {
					markCodexCredentialRuntimeSuccess(a.Provider.CredentialID)
					bindCodexSession(sessionKey, a.Provider, a.Provider.CredentialID)
				}
			}
		} else {
			switch apiMode {
			case "responses", "codex":
				err = streamOpenAIResponses(ctx, client, a.Provider, messages, params.ReasoningEffort, params.ToolScope, ch)
			default:
				err = streamChatCompletions(ctx, client, a.Provider, messages, params.ReasoningEffort, params.ToolScope, ch)
			}
		}
		if err != nil && strings.TrimSpace(a.Provider.Kind) == "cline" && isProviderUnauthorized(err) {
			if refreshed, ok := refreshClineCredentialFromDisk(); ok {
				retryProvider := a.Provider
				retryProvider.APIKey = stringValue(refreshed["accessToken"])
				err = streamChatCompletions(ctx, client, retryProvider, messages, params.ReasoningEffort, params.ToolScope, ch)
			}
		}
		if err != nil {
			ch <- StreamEvent{Err: err}
		}
	}()
	return ch, nil
}

func (a *OpenAIChatCompletionsAdapter) FormatToolResult(toolUseID string, output string, _ bool) any {
	return Message{Role: "tool", ToolCallID: toolUseID, Content: output}
}

func (a *OpenAIChatCompletionsAdapter) PushUserTurn(history *BuiltHistory, _ string, toolResults []any) {
	if history == nil {
		return
	}
	for _, result := range toolResults {
		history.History = append(history.History, result)
	}
}

func (a *OpenAIChatCompletionsAdapter) PushAssistantTurn(history *BuiltHistory, text string, toolUses []ToolCall, _ any) {
	if history == nil {
		return
	}
	history.History = append(history.History, Message{Role: "assistant", Content: text, ToolCalls: toolUses})
}

func openAIHistoryMessages(history *BuiltHistory) []Message {
	if history == nil {
		return nil
	}
	messages := make([]Message, 0, len(history.History))
	for _, item := range history.History {
		switch value := item.(type) {
		case Message:
			messages = append(messages, value)
		case map[string]any:
			messages = append(messages, messageFromMap(value))
		}
	}
	return messages
}

func messageFromMap(value map[string]any) Message {
	message := Message{Role: stringValue(value["role"]), Content: stringValue(value["content"]), ToolCallID: stringValue(value["tool_call_id"])}
	if calls, ok := value["tool_calls"].([]ToolCall); ok {
		message.ToolCalls = calls
	}
	return message
}

type chatResponse struct {
	Choices []struct {
		Message      Message `json:"message"`
		FinishReason string  `json:"finish_reason"`
	} `json:"choices"`
	Usage *openAIUsage     `json:"usage"`
	Error *apiErrorPayload `json:"error"`
}

type apiErrorPayload struct {
	Message  string `json:"message"`
	Type     string `json:"type"`
	Code     any    `json:"code"`
	ResetsAt any    `json:"resets_at,omitempty"`
	ResetAt  any    `json:"reset_at,omitempty"`
}

type openAIUsage struct {
	PromptTokens        int `json:"prompt_tokens"`
	CompletionTokens    int `json:"completion_tokens"`
	InputTokens         int `json:"input_tokens"`
	OutputTokens        int `json:"output_tokens"`
	TotalTokens         int `json:"total_tokens"`
	PromptTokensDetails struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	InputTokensDetails struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"input_tokens_details"`
	CompletionTokensDetails struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
	OutputTokensDetails struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"output_tokens_details"`
}

type chatStreamChunk struct {
	ID      string `json:"id"`
	Choices []struct {
		Index int `json:"index"`
		Delta struct {
			Role      string `json:"role"`
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *openAIUsage     `json:"usage"`
	Error *apiErrorPayload `json:"error"`
}

type openAIStreamingTool struct {
	ID        string
	Name      string
	Arguments string
	Started   bool
	Stopped   bool
	Index     int
}

func streamChatCompletions(ctx context.Context, client *http.Client, provider Provider, messages []Message, reasoningEffort string, toolScope string, out chan<- StreamEvent) error {
	body := map[string]any{
		"model":       provider.Model,
		"messages":    messages,
		"temperature": 0.2,
		"tools":       toolDefinitions(toolScope),
		"stream":      true,
		"stream_options": map[string]any{
			"include_usage": true,
		},
	}
	if effort := chatReasoningEffort(provider.Model, reasoningEffort); effort != "" {
		body["reasoning_effort"] = effort
	}
	payload, _ := json.Marshal(body)
	endpoint := strings.TrimRight(provider.BaseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	setOpenAICompatibleHeaders(req, provider)
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return &providerHTTPError{Provider: "openai", StatusCode: res.StatusCode, Body: readProviderErrorBody(res)}
	}
	if !isSSEContentType(res.Header) {
		var decoded chatResponse
		if err := json.NewDecoder(res.Body).Decode(&decoded); err != nil {
			return err
		}
		if decoded.Error != nil && decoded.Error.Message != "" {
			return errors.New(decoded.Error.Message)
		}
		if decoded.Usage != nil {
			emitStreamEvent(ctx, out, StreamEvent{Parsed: ParsedStreamEvent{Usage: openAIUsageSnapshot(decoded.Usage)}})
		}
		if len(decoded.Choices) == 0 {
			return errProviderEmptyResponse()
		}
		choice := decoded.Choices[0]
		emitStreamEvent(ctx, out, StreamEvent{Parsed: ParsedStreamEvent{Text: choice.Message.Content, ToolUses: choice.Message.ToolCalls, StopReason: choice.FinishReason}})
		return nil
	}
	acc := map[int]*openAIStreamingTool{}
	completed := false
	err = readSSE(ctx, res.Body, 0, func(event sseEvent) error {
		data := strings.TrimSpace(event.Data)
		if data == "" {
			return nil
		}
		if data == "[DONE]" {
			completed = true
			return nil
		}
		var chunk chatStreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return fmt.Errorf("decode openai stream chunk: %w", err)
		}
		if invalid := openAIInvalidStateFromError(chunk.Error); invalid != nil {
			emitStreamEvent(ctx, out, StreamEvent{Parsed: ParsedStreamEvent{InvalidState: invalid}})
			return nil
		}
		if chunk.Usage != nil {
			emitStreamEvent(ctx, out, StreamEvent{Parsed: ParsedStreamEvent{Usage: openAIUsageSnapshot(chunk.Usage)}})
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.Content != "" {
				idx := choice.Index
				emitStreamEvent(ctx, out, StreamEvent{Parsed: ParsedStreamEvent{Text: choice.Delta.Content, TextOutputIndex: &idx}})
			}
			for _, delta := range choice.Delta.ToolCalls {
				tool := acc[delta.Index]
				if tool == nil {
					tool = &openAIStreamingTool{Index: delta.Index}
					acc[delta.Index] = tool
				}
				if delta.ID != "" {
					tool.ID = delta.ID
				}
				if delta.Function.Name != "" {
					tool.Name = delta.Function.Name
				}
				if delta.ID != "" && delta.Function.Name != "" && !tool.Started {
					emitStreamEvent(ctx, out, StreamEvent{Parsed: ParsedStreamEvent{ToolUseChunk: &ToolUseChunk{ToolUseID: tool.ID, Name: tool.Name, OutputIndex: &tool.Index}}})
					tool.Started = true
				}
				if delta.Function.Arguments != "" {
					tool.Arguments += delta.Function.Arguments
					idValue := tool.ID
					if idValue == "" {
						idValue = fmt.Sprintf("tool_%d", delta.Index)
					}
					inputTotal := len(tool.Arguments)
					emitStreamEvent(ctx, out, StreamEvent{Parsed: ParsedStreamEvent{ToolUseChunk: &ToolUseChunk{ToolUseID: idValue, Name: tool.Name, Input: delta.Function.Arguments, OutputIndex: &tool.Index, InputCharsTotal: inputTotal}}})
				}
			}
			if choice.FinishReason != "" {
				for _, tool := range acc {
					if tool.Stopped {
						continue
					}
					idValue := tool.ID
					if idValue == "" {
						idValue = fmt.Sprintf("tool_%d", tool.Index)
					}
					emitStreamEvent(ctx, out, StreamEvent{Parsed: ParsedStreamEvent{ToolUseChunk: &ToolUseChunk{ToolUseID: idValue, Name: tool.Name, Stop: true, OutputIndex: &tool.Index, InputCharsTotal: len(tool.Arguments)}}})
					tool.Stopped = true
				}
				if choice.FinishReason != "tool_calls" {
					parsed := ParsedStreamEvent{StopReason: choice.FinishReason}
					if invalid := openAIInvalidStateFromFinishReason(choice.FinishReason); invalid != nil {
						parsed.InvalidState = invalid
					}
					emitStreamEvent(ctx, out, StreamEvent{Parsed: parsed})
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if !completed {
		emitStreamEvent(ctx, out, StreamEvent{Parsed: ParsedStreamEvent{SilentDisconnect: true}})
	}
	return nil
}

func emitStreamEvent(ctx context.Context, out chan<- StreamEvent, event StreamEvent) bool {
	select {
	case <-ctx.Done():
		return false
	case out <- event:
		return true
	}
}

func setOpenAICompatibleHeaders(req *http.Request, provider Provider) {
	apiKey := provider.APIKey
	if strings.TrimSpace(provider.Kind) == "cline" && apiKey != "" && !strings.HasPrefix(apiKey, "workos:") {
		apiKey = "workos:" + apiKey
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	if strings.TrimSpace(provider.Kind) == "cline" {
		req.Header.Set("HTTP-Referer", "https://narrafork.local")
		req.Header.Set("X-Title", "NarraFork")
	}
	if strings.TrimSpace(provider.Kind) == "codex" || strings.EqualFold(provider.APIMode, "codex") {
		req.Header.Set("originator", "narrafork")
		if provider.AccountID != "" {
			req.Header.Set("ChatGPT-Account-Id", provider.AccountID)
		}
	}
}

func openAIUsageSnapshot(usage *openAIUsage) *UsageSnapshot {
	if usage == nil {
		return nil
	}
	promptTokens := usage.PromptTokens
	if promptTokens == 0 {
		promptTokens = usage.InputTokens
	}
	completionTokens := usage.CompletionTokens
	if completionTokens == 0 {
		completionTokens = usage.OutputTokens
	}
	reasoningTokens := usage.CompletionTokensDetails.ReasoningTokens
	if reasoningTokens == 0 {
		reasoningTokens = usage.OutputTokensDetails.ReasoningTokens
	}
	cachedTokens := usage.PromptTokensDetails.CachedTokens
	if cachedTokens == 0 {
		cachedTokens = usage.InputTokensDetails.CachedTokens
	}
	return &UsageSnapshot{PromptTokens: promptTokens, InputTokens: promptTokens, CompletionTokens: completionTokens, ReasoningTokens: reasoningTokens, CachedInputTokens: cachedTokens}
}

func openAIInvalidStateFromError(err *apiErrorPayload) *InvalidState {
	if err == nil {
		return nil
	}
	reason := strings.TrimSpace(err.Type)
	if err.Code != nil {
		reason = strings.TrimSpace(fmt.Sprint(err.Code))
	}
	if reason == "" {
		reason = "api_error"
	}
	message := firstNonEmpty(err.Message, "Unknown OpenAI API error")
	return &InvalidState{Reason: reason, Message: message}
}

func openAIInvalidStateFromFinishReason(finishReason string) *InvalidState {
	switch finishReason {
	case "length":
		return &InvalidState{Reason: "max_tokens", Message: "Response truncated: model reached maximum token limit."}
	case "content_filter":
		return &InvalidState{Reason: "content_filter", Message: "Response blocked by content filter."}
	case "model_context_window_exceeded":
		return &InvalidState{Reason: "model_context_window_exceeded", Message: "The model has reached its context window limit."}
	default:
		return nil
	}
}

func openAIInvalidStateFromResponsesEvent(parsed responsesStreamEvent) *InvalidState {
	if invalid := openAIInvalidStateFromError(parsed.Error); invalid != nil {
		return invalid
	}
	switch parsed.Type {
	case "response.failed":
		if invalid := openAIInvalidStateFromError(parsed.Response.Error); invalid != nil {
			return invalid
		}
		return &InvalidState{Reason: "api_error", Message: "Response failed"}
	case "response.incomplete":
		reason := "unknown"
		if parsed.Response.IncompleteDetails != nil && parsed.Response.IncompleteDetails.Reason != "" {
			reason = parsed.Response.IncompleteDetails.Reason
		}
		return &InvalidState{Reason: reason, Message: "Response incomplete: " + reason}
	}
	return nil
}

type responsesStreamEvent struct {
	Type        string `json:"type"`
	Delta       string `json:"delta"`
	ItemID      string `json:"item_id"`
	OutputIndex *int   `json:"output_index"`
	Response    struct {
		ID                string           `json:"id"`
		Usage             *openAIUsage     `json:"usage"`
		Error             *apiErrorPayload `json:"error"`
		IncompleteDetails *struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details"`
		Output []struct {
			Type      string `json:"type"`
			ID        string `json:"id"`
			CallID    string `json:"call_id"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
			Content   []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	} `json:"response"`
	Item  *responsesOutputItem `json:"item"`
	Error *apiErrorPayload     `json:"error"`
}

type responsesOutputItem struct {
	Type          string `json:"type"`
	ID            string `json:"id"`
	CallID        string `json:"call_id"`
	Name          string `json:"name"`
	Arguments     string `json:"arguments"`
	Status        string `json:"status"`
	Query         string `json:"query"`
	RevisedPrompt string `json:"revised_prompt"`
	Result        string `json:"result"`
}

func streamCodexWithFailover(ctx context.Context, client *http.Client, provider Provider, messages []Message, reasoningEffort string, toolScope string, sessionKey string, out chan<- StreamEvent) error {
	var lastErr error
	pool := codexCredentialPoolForSession(provider, sessionKey)
	for _, candidate := range pool {
		if candidate.Model == "" {
			candidate.Model = provider.Model
		}
		if candidate.BaseURL == "" {
			candidate.BaseURL = provider.BaseURL
		}
		candidate.Kind = "codex"
		candidate.APIMode = "codex"
		candidate.LoadBalancingMode = provider.LoadBalancingMode
		yielded, err := streamCodexCandidateObserved(ctx, client, candidate, messages, reasoningEffort, toolScope, out)
		if err == nil {
			if candidate.CredentialID != "" {
				markCodexCredentialRuntimeSuccess(candidate.CredentialID)
				bindCodexSession(sessionKey, provider, candidate.CredentialID)
				emitStreamEvent(ctx, out, StreamEvent{Parsed: ParsedStreamEvent{CredentialID: candidate.CredentialID}})
			}
			return nil
		}
		lastErr = err
		if !isCodexFailoverError(err) {
			return err
		}
		if candidate.CredentialID != "" {
			markCodexCredentialRuntimeFailure(candidate.CredentialID, err)
		}
		if yielded {
			if candidate.CredentialID != "" {
				unbindCodexSession(sessionKey)
			}
			return &codexRebuildHistoryRetryError{PreviousCredentialID: candidate.CredentialID, Operation: "chat", Cause: err}
		}
	}
	if lastErr != nil {
		return lastErr
	}
	return streamOpenAIResponses(ctx, client, provider, messages, reasoningEffort, toolScope, out)
}

func streamCodexCandidateObserved(ctx context.Context, client *http.Client, candidate Provider, messages []Message, reasoningEffort string, toolScope string, out chan<- StreamEvent) (bool, error) {
	local := make(chan StreamEvent, 16)
	done := make(chan struct{})
	yielded := false
	go func() {
		defer close(done)
		for event := range local {
			if event.Err == nil && parsedHasMeaningfulResponse(event.Parsed) {
				yielded = true
			}
			emitStreamEvent(ctx, out, event)
		}
	}()
	err := streamCodexCandidate(ctx, client, candidate, messages, reasoningEffort, toolScope, local)
	close(local)
	<-done
	return yielded, err
}

func isProviderUnauthorized(err error) bool {
	var httpErr *providerHTTPError
	return errors.As(err, &httpErr) && (httpErr.StatusCode == http.StatusUnauthorized || httpErr.StatusCode == http.StatusForbidden)
}

func isCodexFailoverError(err error) bool {
	return classifyCodexRuntimeError(err).Failover
}

func streamOpenAIResponses(ctx context.Context, client *http.Client, provider Provider, messages []Message, reasoningEffort string, toolScope string, out chan<- StreamEvent) error {
	input, instructions := openAIResponsesInputAndInstructions(messages)
	tools := openAIResponsesTools(toolScope)
	if strings.EqualFold(provider.APIMode, "codex") || provider.Kind == "codex" {
		tools = appendCodexNativeTools(tools, provider.Model)
	}
	body := map[string]any{
		"model":  provider.Model,
		"input":  input,
		"tools":  tools,
		"stream": true,
		"store":  false,
	}
	if provider.PreviousResponseID != "" {
		body["previous_response_id"] = provider.PreviousResponseID
	}
	if instructions != "" {
		body["instructions"] = instructions
	} else if strings.EqualFold(provider.APIMode, "codex") || provider.Kind == "codex" {
		body["instructions"] = "You are an AI coding agent working inside NarraFork."
	}
	if strings.EqualFold(provider.APIMode, "codex") || provider.Kind == "codex" {
		body["parallel_tool_calls"] = true
		body["prompt_cache_key"] = firstNonEmpty(provider.CredentialID, provider.AccountID, "narrafork")
	}
	if effort := chatReasoningEffort(provider.Model, reasoningEffort); effort != "" {
		body["reasoning"] = map[string]any{"effort": effort}
	}
	payload, _ := json.Marshal(body)
	endpoint := strings.TrimRight(provider.BaseURL, "/") + "/responses"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	setOpenAICompatibleHeaders(req, provider)
	if strings.EqualFold(provider.APIMode, "codex") {
		req.Header.Set("originator", "narrafork")
		if provider.AccountID != "" {
			req.Header.Set("ChatGPT-Account-Id", provider.AccountID)
		}
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return &providerHTTPError{Provider: "responses", StatusCode: res.StatusCode, Body: readProviderErrorBody(res)}
	}
	if !isSSEContentType(res.Header) {
		var decoded responsesStreamEvent
		if err := json.NewDecoder(res.Body).Decode(&decoded); err != nil {
			return err
		}
		if (strings.EqualFold(provider.APIMode, "codex") || provider.Kind == "codex") && decoded.Type == "response.failed" {
			if err := codexFailoverErrorFromResponsesEvent(decoded); err != nil {
				return err
			}
		}
		if decoded.Response.Usage != nil {
			emitStreamEvent(ctx, out, StreamEvent{Parsed: ParsedStreamEvent{Usage: openAIUsageSnapshot(decoded.Response.Usage), ResponseID: decoded.Response.ID, ResponsesAPI: true}})
		}
		emitResponsesCompletedOutput(ctx, out, decoded)
		return nil
	}
	toolsByItem := map[string]*openAIStreamingTool{}
	completed := false
	err = readSSE(ctx, res.Body, 0, func(event sseEvent) error {
		data := strings.TrimSpace(event.Data)
		if data == "" {
			return nil
		}
		if data == "[DONE]" {
			completed = true
			return nil
		}
		var parsed responsesStreamEvent
		if err := json.Unmarshal([]byte(data), &parsed); err != nil {
			return fmt.Errorf("decode responses stream event: %w", err)
		}
		if (strings.EqualFold(provider.APIMode, "codex") || provider.Kind == "codex") && parsed.Type == "response.failed" {
			if err := codexFailoverErrorFromResponsesEvent(parsed); err != nil {
				return err
			}
		}
		if invalid := openAIInvalidStateFromResponsesEvent(parsed); invalid != nil {
			emitStreamEvent(ctx, out, StreamEvent{Parsed: ParsedStreamEvent{InvalidState: invalid, ResponsesAPI: true, ResponseID: parsed.Response.ID}})
			return nil
		}
		switch parsed.Type {
		case "response.created":
			emitStreamEvent(ctx, out, StreamEvent{Parsed: ParsedStreamEvent{ResponseID: parsed.Response.ID, ResponsesAPI: true}})
		case "response.output_text.delta":
			emitStreamEvent(ctx, out, StreamEvent{Parsed: ParsedStreamEvent{Text: parsed.Delta, TextOutputIndex: parsed.OutputIndex, ResponsesAPI: true}})
		case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
			emitStreamEvent(ctx, out, StreamEvent{Parsed: ParsedStreamEvent{Reasoning: parsed.Delta, ReasoningOutputIndex: parsed.OutputIndex, ResponsesAPI: true}})
		case "response.output_item.added":
			if parsed.Item != nil {
				handleResponsesOutputItemAdded(ctx, out, toolsByItem, parsed.Item, parsed.OutputIndex)
			}
		case "response.function_call_arguments.delta":
			tool := responsesToolForEvent(toolsByItem, parsed.ItemID, parsed.OutputIndex)
			tool.Arguments += parsed.Delta
			emitStreamEvent(ctx, out, StreamEvent{Parsed: ParsedStreamEvent{ToolUseChunk: &ToolUseChunk{ToolUseID: tool.ID, Name: tool.Name, Input: parsed.Delta, OutputIndex: parsed.OutputIndex, InputCharsTotal: len(tool.Arguments)}}})
		case "response.function_call_arguments.done":
			tool := responsesToolForEvent(toolsByItem, parsed.ItemID, parsed.OutputIndex)
			if !tool.Stopped {
				emitStreamEvent(ctx, out, StreamEvent{Parsed: ParsedStreamEvent{ToolUseChunk: &ToolUseChunk{ToolUseID: tool.ID, Name: tool.Name, Stop: true, OutputIndex: parsed.OutputIndex, InputCharsTotal: len(tool.Arguments)}}})
				tool.Stopped = true
			}
		case "response.completed":
			completed = true
			emitResponsesCompletedOutput(ctx, out, parsed)
			event := ParsedStreamEvent{ResponsesAPI: true, ResponseID: parsed.Response.ID}
			if parsed.Response.Usage != nil {
				event.Usage = openAIUsageSnapshot(parsed.Response.Usage)
			}
			emitStreamEvent(ctx, out, StreamEvent{Parsed: event})
		}
		return nil
	})
	if err != nil {
		return err
	}
	if !completed {
		emitStreamEvent(ctx, out, StreamEvent{Parsed: ParsedStreamEvent{SilentDisconnect: true, ResponsesAPI: true}})
	}
	return nil
}

func emitResponsesCompletedOutput(ctx context.Context, out chan<- StreamEvent, decoded responsesStreamEvent) {
	for _, item := range decoded.Response.Output {
		switch item.Type {
		case "message", "output_text":
			for _, part := range item.Content {
				if strings.TrimSpace(part.Text) != "" {
					emitStreamEvent(ctx, out, StreamEvent{Parsed: ParsedStreamEvent{Text: part.Text, ResponsesAPI: true, ResponseID: decoded.Response.ID}})
				}
			}
		case "function_call":
			emitStreamEvent(ctx, out, StreamEvent{Parsed: ParsedStreamEvent{ToolUses: []ToolCall{responsesToolCall(item.CallID, item.Name, item.Arguments)}, ResponsesAPI: true, ResponseID: decoded.Response.ID}})
		}
	}
}

func openAIResponsesInput(messages []Message) []any {
	input, _ := openAIResponsesInputAndInstructions(messages)
	return input
}

func openAIResponsesInputAndInstructions(messages []Message) ([]any, string) {
	input := make([]any, 0, len(messages))
	instructions := []string{}
	for _, message := range messages {
		switch message.Role {
		case "tool":
			input = append(input, map[string]any{"type": "function_call_output", "call_id": message.ToolCallID, "output": message.Content})
		case "assistant":
			if len(message.ContentBlocks) > 0 {
				input = append(input, openAIResponsesAssistantItems(message)...)
				continue
			}
			if strings.TrimSpace(message.Content) != "" {
				input = append(input, map[string]any{"role": message.Role, "content": message.Content})
			}
			for _, call := range message.ToolCalls {
				input = append(input, map[string]any{"type": "function_call", "call_id": call.ID, "name": call.Function.Name, "arguments": firstNonEmpty(call.Function.Arguments, "{}")})
			}
		case "system", "developer":
			if strings.TrimSpace(message.Content) != "" {
				instructions = append(instructions, message.Content)
			}
		case "user":
			if len(message.ContentBlocks) > 0 {
				for _, toolResult := range contentBlockToolResults(message.ContentBlocks) {
					input = append(input, map[string]any{"type": "function_call_output", "call_id": toolResult.ToolCallID, "output": toolResult.Content})
				}
			}
			if strings.TrimSpace(message.Content) != "" {
				input = append(input, map[string]any{"role": message.Role, "content": message.Content})
			}
		}
	}
	return input, strings.Join(instructions, "\n\n")
}

func openAIResponsesAssistantItems(message Message) []any {
	items := []any{}
	textParts := []string{}
	flushText := func() {
		text := strings.TrimSpace(strings.Join(textParts, "\n"))
		textParts = []string{}
		if text != "" {
			items = append(items, map[string]any{"role": "assistant", "content": text})
		}
	}
	seenToolUses := map[string]bool{}
	for _, block := range message.ContentBlocks {
		switch stringValue(block["type"]) {
		case "text":
			if text := stringValue(block["text"]); text != "" {
				textParts = append(textParts, text)
			}
		case "reasoning":
			metadata, _ := block["providerMetadata"].(map[string]any)
			openai, _ := metadata["openai"].(map[string]any)
			if encrypted := stringValue(openai["reasoningEncryptedContent"]); encrypted != "" {
				flushText()
				item := map[string]any{"type": "reasoning", "summary": []map[string]any{}}
				if id := stringValue(openai["itemId"]); id != "" {
					item["id"] = id
				}
				if text := stringValue(block["text"]); text != "" {
					item["summary"] = []map[string]any{{"type": "summary_text", "text": text}}
				}
				item["encrypted_content"] = encrypted
				items = append(items, item)
			} else if text := stringValue(block["text"]); text != "" {
				// Chat Completions and Responses without encrypted provider state cannot
				// replay native reasoning; fold it into visible assistant text.
				textParts = append(textParts, text)
			}
		case "tool_use":
			flushText()
			id := stringValue(block["id"])
			name := stringValue(block["name"])
			if id != "" && name != "" {
				items = append(items, map[string]any{"type": "function_call", "call_id": id, "name": name, "arguments": jsonStringValue(block["input"])})
				seenToolUses[id] = true
			}
		}
	}
	flushText()
	for _, call := range message.ToolCalls {
		if call.ID == "" || seenToolUses[call.ID] {
			continue
		}
		items = append(items, map[string]any{"type": "function_call", "call_id": call.ID, "name": call.Function.Name, "arguments": firstNonEmpty(call.Function.Arguments, "{}")})
	}
	return items
}

func appendCodexNativeTools(tools []map[string]any, model string) []map[string]any {
	if !hasResponseTool(tools, "web_search") {
		tools = append(tools, map[string]any{"type": "web_search"})
	}
	if strings.Contains(strings.ToLower(model), "gpt") && !hasResponseTool(tools, "image_generation") {
		tools = append(tools, map[string]any{"type": "image_generation", "output_format": "png"})
	}
	return tools
}

func hasResponseTool(tools []map[string]any, name string) bool {
	for _, tool := range tools {
		if stringValue(tool["type"]) == name || stringValue(tool["name"]) == name {
			return true
		}
	}
	return false
}

func openAIResponsesTools(toolScope string) []map[string]any {
	chatTools := toolDefinitions(toolScope)
	tools := make([]map[string]any, 0, len(chatTools))
	for _, tool := range chatTools {
		fn, _ := tool["function"].(map[string]any)
		name := stringValue(fn["name"])
		if name == "" {
			continue
		}
		entry := map[string]any{"type": "function", "name": name}
		if description := stringValue(fn["description"]); description != "" {
			entry["description"] = description
		}
		if params, ok := fn["parameters"].(map[string]any); ok {
			entry["parameters"] = params
		}
		tools = append(tools, entry)
	}
	return tools
}

func handleResponsesOutputItemAdded(ctx context.Context, out chan<- StreamEvent, tools map[string]*openAIStreamingTool, item *responsesOutputItem, outputIndex *int) {
	switch item.Type {
	case "function_call":
		idValue := firstNonEmpty(item.CallID, item.ID)
		if idValue == "" && outputIndex != nil {
			idValue = fmt.Sprintf("response_tool_%d", *outputIndex)
		}
		tool := &openAIStreamingTool{ID: idValue, Name: item.Name, Arguments: item.Arguments, Started: true}
		key := firstNonEmpty(item.ID, idValue)
		if key != "" {
			tools[key] = tool
		}
		if outputIndex != nil {
			tools[fmt.Sprintf("idx:%d", *outputIndex)] = tool
		}
		emitStreamEvent(ctx, out, StreamEvent{Parsed: ParsedStreamEvent{ToolUseChunk: &ToolUseChunk{ToolUseID: idValue, Name: item.Name, OutputIndex: outputIndex}, ResponsesAPI: true}})
	case "web_search_call":
		emitStreamEvent(ctx, out, StreamEvent{Parsed: ParsedStreamEvent{WebSearch: &WebSearchEvent{ID: item.ID, Status: item.Status, Query: item.Query, OutputIndex: outputIndex, Final: true}, ResponsesAPI: true}})
	case "image_generation_call":
		emitStreamEvent(ctx, out, StreamEvent{Parsed: ParsedStreamEvent{ImageGeneration: &ImageGenerationEvent{ID: item.ID, Status: item.Status, RevisedPrompt: item.RevisedPrompt, Result: item.Result, OutputIndex: outputIndex, Final: true}, ResponsesAPI: true}})
	}
}

func responsesToolForEvent(tools map[string]*openAIStreamingTool, itemID string, outputIndex *int) *openAIStreamingTool {
	if itemID != "" {
		if tool := tools[itemID]; tool != nil {
			return tool
		}
	}
	if outputIndex != nil {
		if tool := tools[fmt.Sprintf("idx:%d", *outputIndex)]; tool != nil {
			return tool
		}
	}
	idValue := firstNonEmpty(itemID, "response_tool")
	tool := &openAIStreamingTool{ID: idValue}
	if itemID != "" {
		tools[itemID] = tool
	}
	if outputIndex != nil {
		tools[fmt.Sprintf("idx:%d", *outputIndex)] = tool
	}
	return tool
}

func responsesToolCall(idValue, name, arguments string) ToolCall {
	if idValue == "" {
		idValue = "response_tool"
	}
	return newFunctionToolCall(idValue, name, firstNonEmpty(arguments, "{}"))
}

func doChatRequest(ctx context.Context, client *http.Client, provider Provider, messages []Message, reasoningEffort string, toolScope string) (chatResponse, error) {
	body := map[string]any{
		"model":       provider.Model,
		"messages":    messages,
		"temperature": 0.2,
		"tools":       toolDefinitions(toolScope),
	}
	if effort := chatReasoningEffort(provider.Model, reasoningEffort); effort != "" {
		body["reasoning_effort"] = effort
	}
	payload, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, provider.BaseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return chatResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	setOpenAICompatibleHeaders(req, provider)
	res, err := client.Do(req)
	if err != nil {
		return chatResponse{}, err
	}
	defer res.Body.Close()
	var decoded chatResponse
	if err := json.NewDecoder(res.Body).Decode(&decoded); err != nil {
		return chatResponse{}, err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		if decoded.Error != nil && decoded.Error.Message != "" {
			return chatResponse{}, errors.New(decoded.Error.Message)
		}
		return chatResponse{}, fmt.Errorf("provider returned status %d", res.StatusCode)
	}
	return decoded, nil
}

func chatReasoningEffort(model, reasoningEffort string) string {
	reasoningEffort = normalizeReasoningEffort(reasoningEffort)
	if reasoningEffort == "" || reasoningEffort == "none" {
		return ""
	}
	lowerModel := strings.ToLower(model)
	supportsReasoning := strings.Contains(lowerModel, "gpt-5") ||
		strings.Contains(lowerModel, "o1") ||
		strings.Contains(lowerModel, "o3") ||
		strings.Contains(lowerModel, "o4") ||
		strings.Contains(lowerModel, "codex") ||
		strings.Contains(lowerModel, "deepseek")
	if !supportsReasoning {
		return ""
	}
	if reasoningEffort == "xhigh" {
		return "high"
	}
	return reasoningEffort
}
