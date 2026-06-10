package agentbasic

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/gorilla/websocket"
)

func streamCodexCandidate(ctx context.Context, client *http.Client, provider Provider, messages []Message, reasoningEffort string, toolScope string, out chan<- StreamEvent) error {
	if provider.UseWebSocket == nil || *provider.UseWebSocket {
		if err := streamCodexResponsesWebSocket(ctx, provider, messages, reasoningEffort, toolScope, out); err == nil {
			return nil
		} else if isCodexFailoverError(err) {
			return err
		}
	}
	return streamOpenAIResponses(ctx, client, provider, messages, reasoningEffort, toolScope, out)
}

func streamCodexResponsesWebSocket(ctx context.Context, provider Provider, messages []Message, reasoningEffort string, toolScope string, out chan<- StreamEvent) error {
	endpoint, err := codexWebSocketURL(provider.BaseURL)
	if err != nil {
		return err
	}
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+provider.APIKey)
	headers.Set("originator", "narrafork")
	headers.Set("OpenAI-Beta", "responses_websockets=2026-02-06")
	if provider.AccountID != "" {
		headers.Set("ChatGPT-Account-Id", provider.AccountID)
	}
	dialer := websocket.Dialer{Proxy: providerProxyFunc(provider)}
	conn, res, err := dialer.DialContext(ctx, endpoint, headers)
	if err != nil {
		if res != nil && (res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden || res.StatusCode == http.StatusTooManyRequests || res.StatusCode >= 500) {
			return &providerHTTPError{Provider: "codex websocket", StatusCode: res.StatusCode, Body: readProviderErrorBody(res)}
		}
		return err
	}
	defer conn.Close()
	input, instructions := openAIResponsesInputAndInstructions(messages)
	body := map[string]any{
		"type":  "response.create",
		"model": provider.Model,
		"input": input,
		"tools": appendCodexNativeTools(
			openAIResponsesTools(toolScope),
			provider.Model,
			optionalBoolDefault(provider.UseWebSearch, true),
			optionalBoolDefault(provider.UseImageGeneration, true),
		),
		"stream":              true,
		"store":               false,
		"parallel_tool_calls": true,
		"prompt_cache_key":    codexPromptCacheKey(provider),
		"instructions":        firstNonEmpty(instructions, "You are an AI coding agent working inside NarraFork."),
	}
	if provider.PreviousResponseID != "" {
		body["previous_response_id"] = provider.PreviousResponseID
	}
	if effort := chatReasoningEffort(provider.Model, reasoningEffort); effort != "" {
		body["reasoning"] = map[string]any{"effort": effort}
	}
	if err := conn.WriteJSON(body); err != nil {
		return err
	}
	toolsByItem := map[string]*openAIStreamingTool{}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		_, data, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) || websocket.IsUnexpectedCloseError(err) == false {
				return nil
			}
			return err
		}
		if len(data) == 0 || strings.TrimSpace(string(data)) == "[DONE]" {
			continue
		}
		if err := handleResponsesJSONEvent(ctx, out, toolsByItem, data); err != nil {
			return err
		}
	}
}

func handleResponsesJSONEvent(ctx context.Context, out chan<- StreamEvent, toolsByItem map[string]*openAIStreamingTool, data []byte) error {
	var parsed responsesStreamEvent
	if err := json.Unmarshal(data, &parsed); err != nil {
		return fmt.Errorf("decode responses event: %w", err)
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
	case "response.output_item.done":
		if parsed.Item != nil {
			handleResponsesOutputItemDone(ctx, out, parsed.Item, parsed.OutputIndex)
		}
	case "response.web_search_call.in_progress":
		emitResponsesWebSearchStatus(ctx, out, parsed.ItemID, "in_progress", parsed.OutputIndex)
	case "response.web_search_call.searching":
		emitResponsesWebSearchStatus(ctx, out, parsed.ItemID, "searching", parsed.OutputIndex)
	case "response.web_search_call.completed":
		emitResponsesWebSearchStatus(ctx, out, parsed.ItemID, "completed", parsed.OutputIndex)
	case "response.image_generation_call.generating", "response.image_generation_call.partial_image":
		emitResponsesImageGenerationStatus(ctx, out, parsed.ItemID, "generating", parsed.OutputIndex)
	case "response.image_generation_call.completed":
		emitResponsesImageGenerationStatus(ctx, out, parsed.ItemID, "completed", parsed.OutputIndex)
	case "response.function_call_arguments.delta":
		tool := responsesToolForEvent(toolsByItem, parsed.ItemID, parsed.OutputIndex)
		tool.Arguments += parsed.Delta
		emitStreamEvent(ctx, out, StreamEvent{Parsed: ParsedStreamEvent{ToolUseChunk: &ToolUseChunk{ToolUseID: tool.ID, Name: tool.Name, Input: parsed.Delta, OutputIndex: parsed.OutputIndex, InputCharsTotal: len(tool.Arguments)}}})
		if !tool.Stopped && isParsableJSON(tool.Arguments) {
			emitStreamEvent(ctx, out, StreamEvent{Parsed: ParsedStreamEvent{ToolUseChunk: &ToolUseChunk{ToolUseID: tool.ID, Name: tool.Name, Stop: true, OutputIndex: parsed.OutputIndex, InputCharsTotal: len(tool.Arguments)}}})
			tool.Stopped = true
		}
	case "response.function_call_arguments.done":
		tool := responsesToolForEvent(toolsByItem, parsed.ItemID, parsed.OutputIndex)
		if !tool.Stopped {
			emitStreamEvent(ctx, out, StreamEvent{Parsed: ParsedStreamEvent{ToolUseChunk: &ToolUseChunk{ToolUseID: tool.ID, Name: tool.Name, Stop: true, OutputIndex: parsed.OutputIndex, InputCharsTotal: len(tool.Arguments)}}})
			tool.Stopped = true
		}
	case "response.completed":
		emitResponsesCompletedOutput(ctx, out, parsed)
		event := ParsedStreamEvent{ResponsesAPI: true, ResponseID: parsed.Response.ID}
		if parsed.Response.Usage != nil {
			event.Usage = openAIUsageSnapshot(parsed.Response.Usage)
		}
		emitStreamEvent(ctx, out, StreamEvent{Parsed: event})
	}
	return nil
}

func codexWebSocketURL(baseURL string) (string, error) {
	baseURL = strings.TrimRight(baseURL, "/")
	parsed, err := url.Parse(baseURL + "/responses")
	if err != nil {
		return "", err
	}
	switch parsed.Scheme {
	case "http":
		parsed.Scheme = "ws"
	case "https":
		parsed.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("unsupported codex websocket base URL scheme %q", parsed.Scheme)
	}
	return parsed.String(), nil
}
