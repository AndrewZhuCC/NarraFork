package agentbasic

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"runtime"
	"strings"

	"narrafork-go/internal/buildinfo"

	"github.com/gorilla/websocket"
)

const (
	// codexResponsesWSBeta is the OpenAI-Beta header value for the codex Responses
	// WebSocket protocol (mirrors TS RESPONSES_WS_BETA_HEADER).
	codexResponsesWSBeta = "responses_websockets=2026-02-06"
	// codexSessionIDHeader / codexClientRequestIDHeader associate the WS handshake
	// with the session (mirrors TS SESSION_ID_HEADER / CLIENT_REQUEST_ID_HEADER).
	codexSessionIDHeader       = "session_id"
	codexClientRequestIDHeader = "x-client-request-id"
	// codexDefaultInstructions is the fallback instructions string sent on a codex
	// Responses request when the conversation carries no developer/system message
	// (the codex gateway requires non-empty instructions even on pure tool-result
	// turns). Mirrors the TS openai-provider CODEX_DEFAULT_INSTRUCTIONS constant
	// verbatim so the gateway sees the same default identity/behavior prompt on
	// both backends; Go previously sent a different "You are an AI coding agent
	// working inside NarraFork." string.
	codexDefaultInstructions = "You are NarraFork Narrator. Follow the user's request and answer concisely."
)

// isOfficialChatGPTDomain reports whether baseURL is an official ChatGPT/OpenAI
// host, mirroring the TS isOfficialChatGPTDomain. Used to gate the
// ChatGPT-Account-Id header and choose the Origin value.
func isOfficialChatGPTDomain(baseURL string) bool {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	host := parsed.Hostname()
	return host == "chatgpt.com" || strings.HasSuffix(host, ".chatgpt.com") || strings.HasSuffix(host, ".openai.com")
}

// codexHandshakeUserAgent builds the User-Agent for the codex WS handshake,
// mirroring the TS getUserAgent format `narrafork/<version> (<osType> <osVer>;
// <arch>) <terminalInfo>`. Go cannot replicate TS's environment-derived
// terminalInfo / Linux /etc/os-release version, so it uses the Go runtime
// version as the terminal-info segment; the value is ASCII-sanitized
// (mirrors TS sanitizeUserAgent). Tracked as userAgentExactTSMatch:false.
func codexHandshakeUserAgent() string {
	osType := runtime.GOOS
	switch osType {
	case "darwin":
		osType = "macOS"
	case "linux":
		osType = "Linux"
	case "windows":
		osType = "Windows"
	}
	ua := fmt.Sprintf("narrafork/%s (%s; %s) Go/%s", buildinfo.VersionValue(), osType, runtime.GOARCH, runtime.Version())
	return sanitizeUserAgentASCII(ua)
}

// sanitizeUserAgentASCII replaces non-ASCII-printable bytes with '_' (mirrors TS
// sanitizeUserAgent), since HTTP header values must be ASCII printable.
func sanitizeUserAgentASCII(value string) string {
	var b strings.Builder
	b.Grow(len(value))
	for _, r := range value {
		if r >= 0x20 && r <= 0x7E {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

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
	headers.Set("OpenAI-Beta", codexResponsesWSBeta)
	headers.Set("User-Agent", codexHandshakeUserAgent())
	official := isOfficialChatGPTDomain(provider.BaseURL)
	if official {
		headers.Set("Origin", "https://chatgpt.com")
	} else {
		headers.Set("Origin", provider.BaseURL)
	}
	// session_id / x-client-request-id associate the handshake with the session,
	// mirroring TS (sessionKey = stickySessionKey ?? conversationId). The WS layer
	// only has provider.PromptCacheKey, which upstream (Chat codex branch) sets to
	// the conversationId/sessionKey, so it is the faithful session identifier here.
	if sessionKey := provider.PromptCacheKey; sessionKey != "" {
		headers.Set(codexSessionIDHeader, sessionKey)
		headers.Set(codexClientRequestIDHeader, sessionKey)
	}
	// ChatGPT-Account-Id is only sent to the official ChatGPT domain (mirrors TS),
	// so it is not leaked to non-official proxy endpoints.
	if provider.AccountID != "" && official {
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
		"instructions":        firstNonEmpty(instructions, codexDefaultInstructions),
	}
	if provider.PreviousResponseID != "" {
		body["previous_response_id"] = provider.PreviousResponseID
	}
	// Codex fast mode: narrator fastMode maps to service_tier:"priority", mirroring
	// the TS codex Responses request (same as the SSE codex path).
	if provider.ServiceTier != "" {
		body["service_tier"] = provider.ServiceTier
	}
	if effort := normalizeCodexReasoningEffort(provider.Model, reasoningEffort); effort != "" {
		// Mirror the TS openai-provider codex reasoning config: official Codex
		// requests send `reasoning:{effort, summary:"auto"}` AND request encrypted
		// reasoning replay via `include:["reasoning.encrypted_content"]`. Go already
		// REPLAYS encrypted_content on input but previously never asked the API to
		// return it, breaking cross-turn reasoning continuity with previous_response_id.
		// normalizeCodexReasoningEffort clamps the effort to the model's supported
		// levels (per-model CODEX_MODEL_REASONING_LEVELS) and passes "none" through.
		body["reasoning"] = map[string]any{"effort": effort, "summary": "auto"}
		body["include"] = []string{"reasoning.encrypted_content"}
	}
	if err := conn.WriteJSON(body); err != nil {
		return err
	}
	toolsByItem := map[string]*openAIStreamingTool{}
	reasoningByIndex := map[int]*responsesReasoningAccum{}
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
		if err := handleResponsesJSONEvent(ctx, out, toolsByItem, reasoningByIndex, data); err != nil {
			return err
		}
	}
}

func handleResponsesJSONEvent(ctx context.Context, out chan<- StreamEvent, toolsByItem map[string]*openAIStreamingTool, reasoningByIndex map[int]*responsesReasoningAccum, data []byte) error {
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
	case "response.reasoning_summary_text.delta", "response.reasoning_text.delta",
		"response.reasoning_summary_text.done", "response.reasoning_text.done":
		handleResponsesReasoningEvent(ctx, out, reasoningByIndex, parsed)
	case "response.output_item.added":
		if handleResponsesReasoningEvent(ctx, out, reasoningByIndex, parsed) {
			break
		}
		if parsed.Item != nil {
			handleResponsesOutputItemAdded(ctx, out, toolsByItem, parsed.Item, parsed.OutputIndex)
		}
	case "response.output_item.done":
		if handleResponsesReasoningEvent(ctx, out, reasoningByIndex, parsed) {
			break
		}
		if parsed.Item != nil {
			handleResponsesOutputItemDone(ctx, out, parsed.Item, parsed.OutputIndex)
		}
	case "response.web_search_call.in_progress":
		emitResponsesWebSearchStatus(ctx, out, parsed.ItemID, "in_progress", parsed.OutputIndex)
	case "response.web_search_call.searching":
		emitResponsesWebSearchStatus(ctx, out, parsed.ItemID, "searching", parsed.OutputIndex)
	case "response.web_search_call.completed":
		emitResponsesWebSearchStatus(ctx, out, parsed.ItemID, "completed", parsed.OutputIndex)
	case "response.image_generation_call.generating":
		emitResponsesImageGenerationStatus(ctx, out, parsed.ItemID, "generating", parsed.OutputIndex)
	case "response.image_generation_call.partial_image":
		emitResponsesImageGenerationPartial(ctx, out, parsed.ItemID, parsed.OutputIndex, parsed.PartialImageIndex, parsed.PartialImageB64)
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
