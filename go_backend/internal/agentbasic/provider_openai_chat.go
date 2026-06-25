package agentbasic

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type OpenAIChatCompletionsAdapter struct {
	Provider Provider
	Client   *http.Client
}

// openAIIdentityPrompts / codexIdentityPrompts / clineIdentityPrompts mirror the
// TS OPENAI_IDENTITY / CODEX_IDENTITY (openai-provider.ts:258-325) and
// CLINE_IDENTITY (cline-provider.ts:26-29) verbatim. They are prepended to the
// system prompt by InjectSystemPrompt so OpenAI/Codex/Cline models receive the
// explicit identity + tool-use instruction TS sends.
var openAIIdentityPrompts = map[string]string{
	"en":    `You are an AI coding assistant with access to tools for reading, writing, and editing files, running shell commands, searching codebases, and more. You MUST use your tools to accomplish tasks — do not just describe what you would do. When the user asks you to do something, take action by calling the appropriate tools. For example, use Read to examine files, Write/Edit to modify them, Bash to run commands, Glob/Grep to search, etc.`,
	"zh-CN": `你是一个 AI 编程助手，拥有读取、写入和编辑文件、运行 shell 命令、搜索代码库等工具。你必须使用工具来完成任务——不要只是描述你会做什么。当用户要求你做某事时，请通过调用相应的工具来采取行动。例如，使用 Read 查看文件，Write/Edit 修改文件，Bash 运行命令，Glob/Grep 搜索等。`,
}

// clineIdentityPrompts is identical to openAIIdentityPrompts in TS, kept separate
// to mirror the TS source structure (cline-provider has its own CLINE_IDENTITY).
var clineIdentityPrompts = openAIIdentityPrompts

var codexIdentityPrompts = map[string]string{
	"en": `You are NarraFork Narrator, an AI coding assistant with access to tools for reading, writing, and editing files, running shell commands, searching codebases, and more.

## Tool usage
- Prefer specialized tools over shell for file operations:
  - Use Read to view files, Edit to modify files, and Write only when needed.
  - Use Glob to find files by name and Grep to search file contents.
- Use Bash for terminal operations (git, bun, builds, tests, running scripts).
- Run tool calls in parallel when neither call needs the other's output; otherwise run sequentially.

## Git and workspace hygiene
- You may be in a dirty git worktree.
    * NEVER revert existing changes you did not make unless explicitly requested, since these changes were made by the user.
    * If asked to make a commit or code edits and there are unrelated changes to your work or changes that you didn't make in those files, don't revert those changes.
    * If the changes are in files you've touched recently, you should read carefully and understand how you can work with the changes rather than reverting them.
    * If the changes are in unrelated files, just ignore them and don't revert them.
- Do not amend commits unless explicitly requested.
- **NEVER** use destructive commands like ` + "`git reset --hard`" + ` or ` + "`git checkout --`" + ` unless specifically requested or approved by the user.

## Presenting your work
- Default: be very concise; friendly coding teammate tone.
- Default: do the work without asking questions. Treat short tasks as sufficient direction; infer missing details by reading the codebase and following existing conventions.
- Questions: only ask when you are truly blocked after checking relevant context AND you cannot safely pick a reasonable default.
- If you must ask: do all non-blocked work first, then ask exactly one targeted question, include your recommended default, and state what would change based on the answer.
- Never ask permission questions like "Should I proceed?" or "Do you want me to run tests?"; proceed with the most reasonable option and mention what you did.
- For substantial work, summarize clearly but avoid heavy formatting for simple confirmations.
- Don't dump large files you've written; reference paths only.
- The user does not see command execution outputs. When asked to show the output of a command, relay the important details in your answer or summarize the key lines.`,
	"zh-CN": `你是 NarraFork 叙述者，一个 AI 编程助手，拥有读取、写入和编辑文件、运行 shell 命令、搜索代码库等工具。

## 工具使用
- 文件操作优先使用专用工具而非 shell：
  - 使用 Read 查看文件，Edit 修改文件，仅在必要时使用 Write。
  - 使用 Glob 按名称查找文件，Grep 搜索文件内容。
- 使用 Bash 执行终端操作（git、bun、构建、测试、运行脚本）。
- 当工具调用之间无依赖关系时并行执行；否则顺序执行。

## Git 和工作区卫生
- 你可能处于一个脏的 git worktree 中。
    * 除非明确要求，否则永远不要还原你未做的现有更改，因为这些更改是用户做的。
    * 如果被要求提交或编辑代码，而文件中存在与你的工作无关的更改或你未做的更改，不要还原这些更改。
    * 如果更改在你最近接触过的文件中，你应该仔细阅读并理解如何与这些更改协作，而不是还原它们。
    * 如果更改在无关文件中，直接忽略它们，不要还原。
- 除非明确要求，否则不要修改提交。
- **永远不要**使用破坏性命令如 ` + "`git reset --hard`" + ` 或 ` + "`git checkout --`" + `，除非用户明确要求或批准。

## 呈现你的工作
- 默认：非常简洁；友好的编程队友语气。
- 默认：直接完成工作，不要提问。将简短任务视为充分的指示；通过阅读代码库并遵循现有约定来推断缺失的细节。
- 提问：仅在检查相关上下文后仍然真正受阻且无法安全选择合理默认值时提问。
- 如果必须提问：先完成所有未受阻的工作，然后提出一个精确的问题，包含你推荐的默认值，并说明答案会如何改变结果。
- 永远不要问"我应该继续吗？"或"你想让我运行测试吗？"这样的许可问题；选择最合理的选项并提及你做了什么。
- 对于大量工作，清晰总结，但对于简单确认避免过度格式化。
- 不要转储你写的大文件；仅引用路径。
- 用户看不到命令执行输出。当被要求显示命令输出时，在你的回答中传达重要细节或总结关键行。`,
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

func (a *OpenAIChatCompletionsAdapter) InjectSystemPrompt(history *BuiltHistory, systemPrompt string, _ string, locale string) {
	if history == nil {
		return
	}
	// Prepend the provider identity to the system prompt, mirroring TS
	// OpenAIProvider.injectSystemPrompt (openai-provider.ts:566-580) /
	// ClineProvider.injectSystemPrompt: OpenAI/Codex models need an explicit
	// identity + tool-use instruction. Codex apiMode uses CODEX_IDENTITY, Cline
	// uses CLINE_IDENTITY, everything else uses OPENAI_IDENTITY. Responses-format
	// (codex / responses apiMode) uses a "developer" role for the injected block;
	// chat-completions uses "system". Go previously injected only the raw
	// systemPrompt with no identity, so OpenAI/Codex models lost the tool-use
	// guidance that TS sends.
	identity := openAIIdentityForProvider(a.Provider, locale)
	content := systemPrompt
	if identity != "" {
		if strings.TrimSpace(systemPrompt) != "" {
			content = identity + "\n\n" + systemPrompt
		} else {
			content = identity
		}
	}
	if strings.TrimSpace(content) == "" {
		return
	}
	role := "system"
	if openAIProviderUsesResponsesFormat(a.Provider) {
		role = "developer"
	}
	messages := make([]any, 0, len(history.History)+1)
	messages = append(messages, Message{Role: role, Content: content})
	messages = append(messages, history.History...)
	history.History = messages
}

// openAIProviderUsesResponsesFormat reports whether the provider talks the
// Responses API (codex kind or codex/responses apiMode), which uses a "developer"
// system role, mirroring the TS responsesFormat flag.
func openAIProviderUsesResponsesFormat(provider Provider) bool {
	if strings.TrimSpace(provider.Kind) == "codex" {
		return true
	}
	apiMode := strings.ToLower(strings.TrimSpace(provider.APIMode))
	return apiMode == "codex" || apiMode == "responses"
}

// openAIIdentityForProvider returns the locale-aware identity prompt for the
// provider: CODEX_IDENTITY for codex apiMode/kind, CLINE_IDENTITY for cline,
// OPENAI_IDENTITY otherwise (mirrors TS identity selection).
func openAIIdentityForProvider(provider Provider, locale string) string {
	pick := func(table map[string]string) string {
		if v, ok := table[normalizeIdentityLocale(locale)]; ok {
			return v
		}
		return table["en"]
	}
	if strings.TrimSpace(provider.Kind) == "codex" || strings.EqualFold(provider.APIMode, "codex") {
		return pick(codexIdentityPrompts)
	}
	if strings.TrimSpace(provider.Kind) == "cline" {
		return pick(clineIdentityPrompts)
	}
	return pick(openAIIdentityPrompts)
}

func normalizeIdentityLocale(locale string) string {
	if strings.EqualFold(strings.TrimSpace(locale), "zh-CN") {
		return "zh-CN"
	}
	return "en"
}

func (a *OpenAIChatCompletionsAdapter) Chat(ctx context.Context, params ChatParams) (<-chan StreamEvent, error) {
	ch := make(chan StreamEvent, 16)
	client := a.Client
	if client == nil {
		client = providerHTTPClient(a.Provider)
	}
	messages := openAIHistoryMessages(params.History)
	go func() {
		defer close(ch)
		// Unified local provider copy carrying request-scoped fields (service tier
		// from fastMode). Never mutate the shared a.Provider. All codex-capable
		// paths below use this local copy so ServiceTier reaches both the
		// Kind=="codex" and the apiMode=="codex" (responses) request bodies.
		provider := a.Provider
		provider.ServiceTier = params.ServiceTier
		apiMode := strings.ToLower(strings.TrimSpace(provider.APIMode))
		reasoningEffort := firstNonEmpty(params.ReasoningEffort, provider.DefaultReasoningEffort)
		var err error
		if strings.TrimSpace(provider.Kind) == "codex" {
			sessionKey := firstNonEmpty(params.StickySessionKey, params.ConversationID)
			provider.PromptCacheKey = firstNonEmpty(params.ConversationID, provider.PromptCacheKey)
			if len(provider.CredentialPool) > 0 {
				err = streamCodexWithFailover(ctx, client, provider, messages, reasoningEffort, params.ToolScope, sessionKey, ch)
			} else {
				_, err = streamCodexCandidateObserved(ctx, client, provider, messages, reasoningEffort, params.ToolScope, ch)
				if err == nil && provider.CredentialID != "" {
					markCodexCredentialRuntimeSuccess(provider.CredentialID)
					bindCodexSession(sessionKey, provider, provider.CredentialID)
				}
			}
		} else {
			switch apiMode {
			case "responses", "codex":
				err = streamOpenAIResponses(ctx, client, provider, messages, reasoningEffort, params.ToolScope, ch)
			default:
				err = streamChatCompletions(ctx, client, provider, messages, reasoningEffort, params.ToolScope, ch)
			}
		}
		if err != nil && strings.TrimSpace(provider.Kind) == "cline" && isProviderUnauthorized(err) {
			if refreshed, ok := refreshClineCredentialFromDisk(); ok {
				retryProvider := provider
				retryProvider.APIKey = stringValue(refreshed["accessToken"])
				err = streamChatCompletions(ctx, client, retryProvider, messages, reasoningEffort, params.ToolScope, ch)
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

func (a *OpenAIChatCompletionsAdapter) PushUserTurn(history *BuiltHistory, content string, toolResults []any) {
	if history == nil {
		return
	}
	for _, result := range toolResults {
		history.History = append(history.History, result)
	}
	if strings.TrimSpace(content) != "" {
		history.History = append(history.History, Message{Role: "user", Content: content})
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
			Role    string `json:"role"`
			Content string `json:"content"`
			// ReasoningContent carries extended-thinking text for models that stream
			// it over the chat-completions delta (e.g. DeepSeek, QwQ), mirroring TS
			// openai-provider.ts reading delta.reasoning_content.
			ReasoningContent string `json:"reasoning_content"`
			ToolCalls        []struct {
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
		"model":    provider.Model,
		"messages": openAIChatCompletionMessages(messages, provider.Model),
		// No temperature: TS openai-provider.ts sends model/messages/stream/
		// stream_options/tools only and never sets temperature, letting the provider
		// use its own default. Go previously hardcoded temperature:0.2, which
		// silently forced low-variance generation diverging from TS.
		"tools":  toolDefinitions(toolScope),
		"stream": true,
		"stream_options": map[string]any{
			"include_usage": true,
		},
	}
	// Mirror the TS openai-provider chat-completions DeepSeek handling: DeepSeek
	// carries a `thinking` config plus a DeepSeek-tiered reasoning_effort. `none`
	// sends `thinking:{type:"disabled"}` (and no reasoning_effort); otherwise
	// `thinking:{type:"enabled"}` plus reasoning_effort mapped to DeepSeek's
	// "high"/"max" tiers (mapDeepSeekEffort: low/medium/high -> "high", xhigh -> "max").
	// Non-DeepSeek models keep the generic effort (Go routes gpt-5/o1/o3 through
	// this chat-completions path, so dropping their reasoning_effort would lose
	// reasoning entirely rather than matching TS, which routes them to /responses).
	// The TS Cline provider (cline-provider.ts) is a dedicated adapter whose
	// chat-completions body is model/messages/stream/stream_options/tools ONLY — it
	// never sends reasoning_effort or thinking. Go routes Cline through this shared
	// chat-completions path, so skip all reasoning config for Cline to match TS
	// (otherwise a gpt-5-named Cline-proxied model would wrongly gain reasoning_effort).
	if strings.TrimSpace(provider.Kind) == "cline" {
		// no reasoning config for Cline
	} else if isDeepSeekModelName(provider.Model) {
		if normalizeReasoningEffort(reasoningEffort) == "none" {
			body["thinking"] = map[string]any{"type": "disabled"}
		} else {
			body["thinking"] = map[string]any{"type": "enabled"}
			if effort := mapDeepSeekEffort(reasoningEffort); effort != "" {
				body["reasoning_effort"] = effort
			}
		}
	} else if effort := chatReasoningEffort(provider.Model, reasoningEffort); effort != "" {
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
			if choice.Delta.ReasoningContent != "" {
				idx := choice.Index
				emitStreamEvent(ctx, out, StreamEvent{Parsed: ParsedStreamEvent{Reasoning: choice.Delta.ReasoningContent, ReasoningOutputIndex: &idx}})
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
					if !tool.Stopped && isParsableJSON(tool.Arguments) {
						emitStreamEvent(ctx, out, StreamEvent{Parsed: ParsedStreamEvent{ToolUseChunk: &ToolUseChunk{ToolUseID: idValue, Name: tool.Name, Stop: true, OutputIndex: &tool.Index, InputCharsTotal: inputTotal}}})
						tool.Stopped = true
					}
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

// parseGatewayDataEvent parses a unified-gateway data-embedded event (the
// {type:"queueEvent"|"quotaBalanceEvent"|"modelCatalogEvent"|"imageCacheAckEvent",
// ...} shape the narrafork-unified-gateway injects into OpenAI/Codex SSE and
// WebSocket streams) into a ParsedStreamEvent, mirroring the TS gateway-events
// parseGatewayDataEvent. Returns (event, true) only when the data carries a known
// gateway event type with a usable payload, so callers continue normal parsing
// otherwise. queueEvent/quotaBalanceEvent reuse the same field shapes the Kiro
// path consumes (see provider_kiro.go); modelCatalogEvent/imageCacheAckEvent fill
// the NugModelCatalog/NugImageCacheAck snapshots.
func parseGatewayDataEvent(data map[string]any) (ParsedStreamEvent, bool) {
	switch stringValue(data["type"]) {
	case "queueEvent":
		_, hasPosition := data["position"]
		_, hasMessage := data["queueMessage"]
		if !hasPosition && !hasMessage {
			return ParsedStreamEvent{}, false
		}
		return ParsedStreamEvent{QueueStatus: &QueueSnapshot{
			Position:     intValue(data["position"], 0),
			QueueDepth:   intValue(data["queueDepth"], 0),
			QueueMessage: stringValue(data["queueMessage"]),
		}}, true
	case "quotaBalanceEvent":
		balance := stringValue(data["quotaBalance"])
		detailed := firstNonEmpty(stringValue(data["detailedQuotaBalance"]), formatExtraDetails(data["extra"]))
		// TS parseGatewaySSEEvent always returns the quotaBalance event (balance may
		// be null/empty). Mirror that: always carry a (possibly empty) balance.
		event := ParsedStreamEvent{QuotaBalance: &balance}
		if detailed != "" {
			event.DetailedQuotaBalance = &detailed
		}
		return event, true
	case "modelCatalogEvent":
		models := mapSliceValue(data["models"])
		if models == nil {
			return ParsedStreamEvent{}, false
		}
		return ParsedStreamEvent{NugModelCatalog: &NugModelCatalogSnapshot{
			ModelHash: firstNonEmpty(stringValue(data["modelHash"]), stringValue(data["hash"])),
			Models:    models,
		}}, true
	case "imageCacheAckEvent":
		refs := stringList(data["refs"])
		if data["refs"] == nil {
			return ParsedStreamEvent{}, false
		}
		return ParsedStreamEvent{NugImageCacheAck: &NugImageCacheAckSnapshot{Refs: refs}}, true
	}
	return ParsedStreamEvent{}, false
}

// mapSliceValue coerces a JSON array of objects into []map[string]any, dropping
// non-object entries (mirrors the TS data.models.filter(isRecord)).
func mapSliceValue(value any) []map[string]any {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if record, ok := item.(map[string]any); ok {
			out = append(out, record)
		}
	}
	return out
}

// clineAgentVersion is the Cline extension version reported in client-identifying
// headers, kept in sync with the TS cline-auth CLINE_VERSION.
const clineAgentVersion = "3.74.0"

// clineAgentRequestHeaders mirrors the TS cline-auth buildOpenRouterHeaders +
// buildClineHeaders set used by the Cline provider's chat requests. X-PLATFORM-
// VERSION uses the Go runtime version (TS uses the Bun/Node version); this is a
// platform-identifier adaptation, the other values match TS exactly.
func clineAgentRequestHeaders() map[string]string {
	return map[string]string{
		"User-Agent":         "Cline/" + clineAgentVersion,
		"X-PLATFORM":         "node",
		"X-PLATFORM-VERSION": runtime.Version(),
		"X-CLIENT-TYPE":      "extension",
		"X-CLIENT-VERSION":   clineAgentVersion,
		"X-CORE-VERSION":     clineAgentVersion,
		"HTTP-Referer":       "https://cline.bot",
		"X-Title":            "Cline",
	}
}

func setOpenAICompatibleHeaders(req *http.Request, provider Provider) {
	apiKey := provider.APIKey
	if strings.TrimSpace(provider.Kind) == "cline" && apiKey != "" && !strings.HasPrefix(apiKey, "workos:") {
		apiKey = "workos:" + apiKey
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	if strings.TrimSpace(provider.Kind) == "cline" {
		// Mimic the Cline VS Code extension headers (TS cline-auth
		// buildOpenRouterHeaders + buildClineHeaders), which the Cline/OpenRouter
		// backend validates. Go previously sent narrafork-branded HTTP-Referer/
		// X-Title and omitted the User-Agent / X-PLATFORM* / X-CLIENT* / X-CORE*
		// client-identifying headers entirely.
		for key, value := range clineAgentRequestHeaders() {
			req.Header.Set(key, value)
		}
	}
	if strings.TrimSpace(provider.Kind) == "codex" || strings.EqualFold(provider.APIMode, "codex") {
		req.Header.Set("originator", "narrafork")
		// ChatGPT-Account-Id is only sent to official ChatGPT domains (mirrors TS
		// openai-provider buildHeaders), so it is not leaked to proxy endpoints.
		if provider.AccountID != "" && isOfficialChatGPTDomain(provider.BaseURL) {
			req.Header.Set("ChatGPT-Account-Id", provider.AccountID)
		}
	}
	applyProviderExtraHeaders(req, provider)
}

// applyProviderExtraHeaders applies provider-scoped extra request headers (e.g.
// the NUG provider injects X-NUG-Model-Hash / X-Conversation-ID via this). A nil
// map is a no-op, so providers that do not use it are unaffected.
func applyProviderExtraHeaders(req *http.Request, provider Provider) {
	for key, value := range provider.ExtraHeaders {
		if key == "" {
			continue
		}
		req.Header.Set(key, value)
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
	Text        string `json:"text"`
	ItemID      string `json:"item_id"`
	OutputIndex *int   `json:"output_index"`
	// Present on response.image_generation_call.partial_image events.
	PartialImageIndex *int   `json:"partial_image_index"`
	PartialImageB64   string `json:"partial_image_b64"`
	Response          struct {
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
	// Usage at the top level: some gateways (e.g. bob_cx) attach usage to any
	// event rather than only response.completed/response, mirroring the TS
	// `chunk.response?.usage || chunk.usage` extraction in parseResponsesAPIEvent.
	Usage *openAIUsage `json:"usage"`
}

type responsesOutputItem struct {
	Type             string         `json:"type"`
	ID               string         `json:"id"`
	Role             string         `json:"role"`
	CallID           string         `json:"call_id"`
	Name             string         `json:"name"`
	Arguments        string         `json:"arguments"`
	Status           string         `json:"status"`
	Query            string         `json:"query"`
	Action           map[string]any `json:"action"`
	RevisedPrompt    string         `json:"revised_prompt"`
	Result           string         `json:"result"`
	EncryptedContent string         `json:"encrypted_content"`
}

// responsesReasoningAccum tracks a Responses API reasoning item by output_index,
// mirroring the TS ResponsesReasoningAccum. It captures the item id and
// encrypted_content (for cross-turn encrypted reasoning replay) and how much
// reasoning text was already streamed via deltas, so a trailing .done event only
// emits the full text when no delta preceded it (matching emitOnlyIfAccumulatorEmpty).
type responsesReasoningAccum struct {
	ItemID           string
	EncryptedContent string
	EmittedLen       int
}

// reasoningMetadataFromAccum builds the providerMetadata.openai payload the loop
// persists onto reasoning content blocks and replays as encrypted_content on the
// next turn (openAIResponsesAssistantItems rebuild). Returns nil when there is
// nothing to carry.
func reasoningMetadataFromAccum(acc *responsesReasoningAccum) map[string]any {
	if acc == nil {
		return nil
	}
	openai := map[string]any{}
	if acc.ItemID != "" {
		openai["itemId"] = acc.ItemID
	}
	if acc.EncryptedContent != "" {
		openai["reasoningEncryptedContent"] = acc.EncryptedContent
	}
	if len(openai) == 0 {
		return nil
	}
	return map[string]any{"openai": openai}
}

// resolveResponsesReasoningAccum returns (creating if needed) the reasoning
// accumulator for the event's output_index. Responses reasoning events always
// carry output_index, so a nil index yields no accumulator.
func resolveResponsesReasoningAccum(reasoning map[int]*responsesReasoningAccum, outputIndex *int) *responsesReasoningAccum {
	if outputIndex == nil {
		return nil
	}
	acc := reasoning[*outputIndex]
	if acc == nil {
		acc = &responsesReasoningAccum{}
		reasoning[*outputIndex] = acc
	}
	return acc
}

// handleResponsesReasoningEvent handles the four Responses reasoning event types
// (summary/text delta+done, and output_item.added/done with item.type=reasoning),
// capturing itemId/encrypted_content for cross-turn encrypted reasoning replay
// and emitting reasoning text/metadata events. Returns true when the event was a
// reasoning event (handled), so callers can skip their default handling. Shared
// by the SSE (streamOpenAIResponses) and Codex WebSocket (handleResponsesJSONEvent)
// paths so both capture encrypted reasoning identically. Mirrors the TS
// openai-provider reasoning accumulator (pushReasoningTextEvent + output_item).
func handleResponsesReasoningEvent(ctx context.Context, out chan<- StreamEvent, reasoning map[int]*responsesReasoningAccum, parsed responsesStreamEvent) bool {
	switch parsed.Type {
	case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
		// Streamed reasoning text: append to the accumulator and carry the
		// captured itemId/encrypted_content metadata so the loop persists it
		// for cross-turn encrypted reasoning replay (mirrors TS pushReasoningTextEvent).
		acc := resolveResponsesReasoningAccum(reasoning, parsed.OutputIndex)
		if acc != nil {
			acc.EmittedLen += len(parsed.Delta)
		}
		emitStreamEvent(ctx, out, StreamEvent{Parsed: ParsedStreamEvent{Reasoning: parsed.Delta, ReasoningMetadata: reasoningMetadataFromAccum(acc), ReasoningOutputIndex: parsed.OutputIndex, ResponsesAPI: true}})
		return true
	case "response.reasoning_summary_text.done", "response.reasoning_text.done":
		// Final reasoning text: emit only when no delta preceded it (a provider
		// that streams only the .done full text), mirroring TS emitOnlyIfAccumulatorEmpty.
		acc := resolveResponsesReasoningAccum(reasoning, parsed.OutputIndex)
		if parsed.Text != "" && (acc == nil || acc.EmittedLen == 0) {
			if acc != nil {
				acc.EmittedLen += len(parsed.Text)
			}
			emitStreamEvent(ctx, out, StreamEvent{Parsed: ParsedStreamEvent{Reasoning: parsed.Text, ReasoningMetadata: reasoningMetadataFromAccum(acc), ReasoningOutputIndex: parsed.OutputIndex, ResponsesAPI: true}})
		}
		return true
	case "response.output_item.added":
		if parsed.Item != nil && parsed.Item.Type == "reasoning" {
			if acc := resolveResponsesReasoningAccum(reasoning, parsed.OutputIndex); acc != nil {
				acc.ItemID = parsed.Item.ID
				acc.EncryptedContent = parsed.Item.EncryptedContent
			}
			return true
		}
		return false
	case "response.output_item.done":
		if parsed.Item != nil && parsed.Item.Type == "reasoning" {
			// Update the final encrypted_content and emit a metadata-only event
			// so the loop persists the final value for continuation (mirrors TS).
			if acc := resolveResponsesReasoningAccum(reasoning, parsed.OutputIndex); acc != nil {
				if parsed.Item.ID != "" {
					acc.ItemID = parsed.Item.ID
				}
				if parsed.Item.EncryptedContent != "" {
					acc.EncryptedContent = parsed.Item.EncryptedContent
				}
				if metadata := reasoningMetadataFromAccum(acc); metadata != nil {
					emitStreamEvent(ctx, out, StreamEvent{Parsed: ParsedStreamEvent{ReasoningMetadata: metadata, ReasoningOutputIndex: parsed.OutputIndex, ResponsesAPI: true}})
				}
			}
			return true
		}
		return false
	}
	return false
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
		if candidate.UseWebSocket == nil {
			candidate.UseWebSocket = provider.UseWebSocket
		}
		if candidate.UseWebSearch == nil {
			candidate.UseWebSearch = provider.UseWebSearch
		}
		if candidate.UseImageGeneration == nil {
			candidate.UseImageGeneration = provider.UseImageGeneration
		}
		candidate.PromptCacheKey = firstNonEmpty(provider.PromptCacheKey, candidate.PromptCacheKey)
		yielded, err := streamCodexCandidateObserved(ctx, client, candidate, messages, reasoningEffort, toolScope, out)
		if err == nil {
			if candidate.CredentialID != "" {
				markCodexCredentialRuntimeSuccess(candidate.CredentialID)
				bindCodexSession(sessionKey, provider, candidate.CredentialID)
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
	provider.PromptCacheKey = firstNonEmpty(provider.PromptCacheKey, sessionKey)
	return streamOpenAIResponses(ctx, client, provider, messages, reasoningEffort, toolScope, out)
}

func streamCodexCandidateObserved(ctx context.Context, client *http.Client, candidate Provider, messages []Message, reasoningEffort string, toolScope string, out chan<- StreamEvent) (bool, error) {
	local := make(chan StreamEvent, 16)
	done := make(chan struct{})
	yielded := false
	go func() {
		defer close(done)
		for event := range local {
			if event.Err == nil {
				if candidate.CredentialID != "" && event.Parsed.CredentialID == "" {
					event.Parsed.CredentialID = candidate.CredentialID
				}
				if parsedHasMeaningfulResponse(event.Parsed) {
					yielded = true
				}
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

func codexPromptCacheKey(provider Provider) string {
	return firstNonEmpty(provider.PromptCacheKey, provider.CredentialID, provider.AccountID, "narrafork")
}

func streamOpenAIResponses(ctx context.Context, client *http.Client, provider Provider, messages []Message, reasoningEffort string, toolScope string, out chan<- StreamEvent) error {
	input, instructions := openAIResponsesInputAndInstructions(messages)
	tools := openAIResponsesTools(toolScope)
	if strings.EqualFold(provider.APIMode, "codex") || provider.Kind == "codex" {
		tools = appendCodexNativeTools(
			tools,
			provider.Model,
			codexWebSearchEnabledForScope(provider, toolScope),
			optionalBoolDefault(provider.UseImageGeneration, true),
		)
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
		body["instructions"] = codexDefaultInstructions
	}
	if strings.EqualFold(provider.APIMode, "codex") || provider.Kind == "codex" {
		body["parallel_tool_calls"] = true
		body["prompt_cache_key"] = codexPromptCacheKey(provider)
		// Codex fast mode: narrator fastMode maps to service_tier:"priority"
		// (priority processing), mirroring TS. Only codex bodies emit it.
		if provider.ServiceTier != "" {
			body["service_tier"] = provider.ServiceTier
		}
	}
	if strings.EqualFold(provider.APIMode, "codex") || provider.Kind == "codex" {
		// Codex Responses requests: clamp effort to the model's supported levels
		// (per-model CODEX_MODEL_REASONING_LEVELS), then send
		// reasoning:{effort, summary:"auto"} + include:["reasoning.encrypted_content"]
		// (encrypted reasoning replay), matching the TS openai-provider codex path.
		if effort := normalizeCodexReasoningEffort(provider.Model, reasoningEffort); effort != "" {
			body["reasoning"] = map[string]any{"effort": effort, "summary": "auto"}
			body["include"] = []string{"reasoning.encrypted_content"}
		}
	} else if effort := chatReasoningEffort(provider.Model, reasoningEffort); effort != "" {
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
		// Only sent to official ChatGPT domains (mirrors TS), not proxy endpoints.
		if provider.AccountID != "" && isOfficialChatGPTDomain(provider.BaseURL) {
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
	reasoningByIndex := map[int]*responsesReasoningAccum{}
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
		// Unified-gateway data-embedded events (queue/quota/modelCatalog/imageCacheAck)
		// are injected into the Responses SSE stream and must short-circuit normal
		// parsing (mirrors the TS parseGatewayDataEvent branch in _parseResponsesAPIStream).
		if raw := map[string]any{}; json.Unmarshal([]byte(data), &raw) == nil {
			if gwEvent, ok := parseGatewayDataEvent(raw); ok {
				emitStreamEvent(ctx, out, StreamEvent{Parsed: gwEvent})
				return nil
			}
		}
		// Top-level usage can appear on any event from some gateways (mirrors TS
		// `chunk.response?.usage || chunk.usage`); response.completed handles its own
		// Response.Usage below, so skip it here to avoid double counting.
		if parsed.Usage != nil && parsed.Type != "response.completed" {
			emitStreamEvent(ctx, out, StreamEvent{Parsed: ParsedStreamEvent{Usage: openAIUsageSnapshot(parsed.Usage), ResponsesAPI: true, ResponseID: parsed.Response.ID}})
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
		case "response.reasoning_summary_text.delta", "response.reasoning_text.delta",
			"response.reasoning_summary_text.done", "response.reasoning_text.done":
			handleResponsesReasoningEvent(ctx, out, reasoningByIndex, parsed)
		case "response.output_item.added":
			if handleResponsesReasoningEvent(ctx, out, reasoningByIndex, parsed) {
				break
			}
			if parsed.Item != nil {
				if parsed.Item.Type == "message" && parsed.Item.Role == "assistant" && parsed.Item.ID != "" {
					emitStreamEvent(ctx, out, StreamEvent{Parsed: ParsedStreamEvent{MessageID: parsed.Item.ID, ResponsesAPI: true}})
					break
				}
				handleResponsesOutputItemAdded(ctx, out, toolsByItem, parsed.Item, parsed.OutputIndex)
			}
		case "response.output_item.done":
			if handleResponsesReasoningEvent(ctx, out, reasoningByIndex, parsed) {
				break
			}
			if parsed.Item != nil {
				if parsed.Item.Type == "message" && parsed.Item.Role == "assistant" && parsed.Item.ID != "" {
					emitStreamEvent(ctx, out, StreamEvent{Parsed: ParsedStreamEvent{MessageID: parsed.Item.ID, ResponsesAPI: true}})
					break
				}
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

func openAIChatCompletionMessages(messages []Message, model string) []map[string]any {
	deepSeek := isDeepSeekModelName(model)
	out := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
		if message.Role == "assistant" {
			if searchContext := webSearchContextFromBlocks(message.ContentBlocks); searchContext != "" {
				out = append(out, map[string]any{"role": "user", "content": searchContext})
			}
		}
		item := map[string]any{"role": message.Role}
		if message.ToolCallID != "" {
			item["tool_call_id"] = message.ToolCallID
		}
		if len(message.ToolCalls) > 0 {
			item["tool_calls"] = message.ToolCalls
		}
		if message.Role == "user" && len(message.ContentBlocks) > 0 {
			if parts := openAIUserContentParts(message, "chat"); len(parts) > 0 {
				item["content"] = parts
			} else {
				item["content"] = message.Content
			}
		} else {
			item["content"] = message.Content
		}
		if message.Role == "assistant" {
			// Mirror TS buildOAIHistory + buildHistory post-processing: reasoning is
			// replayed in a separate reasoning_content field (models like DeepSeek/QwQ
			// require it). When the stored ContentBlocks carry reasoning blocks, set
			// reasoning_content from them and rebuild content from text blocks only so
			// reasoning is not duplicated into the visible content (messageTextForAgent
			// otherwise folds reasoning into Content).
			if reasoningText := chatReasoningTextFromBlocks(message.ContentBlocks); reasoningText != "" {
				item["reasoning_content"] = reasoningText
				item["content"] = chatVisibleTextFromBlocks(message.ContentBlocks)
			}
			// DeepSeek thinking mode requires reasoning_content on ALL assistant
			// messages; patch missing ones with an empty string so the API does not
			// reject the request (mirrors TS openai-provider.ts:559-560).
			if deepSeek {
				if _, has := item["reasoning_content"]; !has {
					item["reasoning_content"] = ""
				}
			}
		}
		out = append(out, item)
	}
	return out
}

// chatReasoningTextFromBlocks joins the text of all reasoning content blocks with
// blank lines, mirroring TS reasoningBlocks.map(b=>b.text).join("\n\n").
func chatReasoningTextFromBlocks(blocks []map[string]any) string {
	parts := []string{}
	for _, block := range blocks {
		if stringValue(block["type"]) != "reasoning" {
			continue
		}
		if text := stringValue(block["text"]); strings.TrimSpace(text) != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n\n")
}

// chatVisibleTextFromBlocks joins the text of all text content blocks with
// newlines, mirroring TS buildOAIHistory which sets assistant content from text
// blocks only (reasoning goes to reasoning_content).
func chatVisibleTextFromBlocks(blocks []map[string]any) string {
	parts := []string{}
	for _, block := range blocks {
		if stringValue(block["type"]) != "text" {
			continue
		}
		if text := stringValue(block["text"]); strings.TrimSpace(text) != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
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
				if parts := openAIUserContentParts(message, "responses"); len(parts) > 0 {
					input = append(input, map[string]any{"role": message.Role, "content": parts})
					continue
				}
			}
			if strings.TrimSpace(message.Content) != "" {
				input = append(input, map[string]any{"role": message.Role, "content": message.Content})
			}
		}
	}
	return input, strings.Join(instructions, "\n\n")
}

func openAIUserContentParts(message Message, mode string) []map[string]any {
	parts := []map[string]any{}
	seenText := false
	textType := "text"
	imageType := "image_url"
	if mode == "responses" {
		textType = "input_text"
		imageType = "input_image"
	}
	for _, block := range message.ContentBlocks {
		switch stringValue(block["type"]) {
		case "text":
			if text := stringValue(block["text"]); strings.TrimSpace(text) != "" {
				parts = append(parts, map[string]any{"type": textType, "text": text})
				seenText = true
			}
		case "image":
			if url, ok := imageBlockDataURL(block); ok {
				if mode == "responses" {
					parts = append(parts, map[string]any{"type": imageType, "image_url": url})
				} else {
					parts = append(parts, map[string]any{"type": imageType, "image_url": map[string]any{"url": url}})
				}
			}
		}
	}
	if !seenText && strings.TrimSpace(message.Content) != "" {
		parts = append([]map[string]any{{"type": textType, "text": message.Content}}, parts...)
	}
	return parts
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
		case "web_search":
			flushText()
			if action, ok := webSearchActionFromBlock(block); ok {
				items = append(items, map[string]any{"type": "web_search_call", "status": "completed", "action": action})
			}
		case "image_generation":
			flushText()
			result := openAIImageGenerationReplayResult(block)
			if result == "" {
				continue
			}
			if savedPath := stringValue(block["savedPath"]); savedPath != "" {
				items = append(items, map[string]any{"role": "developer", "content": imageGenerationSavedPathInstruction(savedPath)})
			}
			item := map[string]any{"type": "image_generation_call", "status": "completed", "result": result}
			if id := stringValue(block["id"]); id != "" {
				item["id"] = id
			}
			if revisedPrompt := stringValue(block["revisedPrompt"]); revisedPrompt != "" {
				item["revised_prompt"] = revisedPrompt
			}
			items = append(items, item)
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

const maxImageGenerationReplayBytes = 20 * 1024 * 1024

func openAIImageGenerationReplayResult(block map[string]any) string {
	result := strings.TrimSpace(stringValue(block["result"]))
	if result != "" {
		return stripDataURLPrefix(result)
	}
	savedPath := strings.TrimSpace(stringValue(block["savedPath"]))
	if savedPath == "" {
		return ""
	}
	info, err := os.Stat(savedPath)
	if err != nil || info.IsDir() || info.Size() <= 0 || info.Size() > maxImageGenerationReplayBytes {
		return ""
	}
	data, err := os.ReadFile(savedPath)
	if err != nil || len(data) == 0 || len(data) > maxImageGenerationReplayBytes {
		return ""
	}
	return base64.StdEncoding.EncodeToString(data)
}

func stripDataURLPrefix(value string) string {
	if strings.HasPrefix(value, "data:") {
		if idx := strings.Index(value, ";base64,"); idx >= 0 {
			return strings.TrimSpace(value[idx+len(";base64,"):])
		}
	}
	return value
}

func imageGenerationSavedPathInstruction(savedPath string) string {
	dir := filepath.Dir(savedPath)
	return "Generated images are saved to " + dir + " by default.\n" +
		"The most recent generated image is saved as " + savedPath + ".\n" +
		"If you need to use a generated image at another path, copy it and leave the original in place unless the user explicitly asks you to delete it."
}

// codexImageGenerationPartialImages mirrors the TS
// CODEX_IMAGE_GENERATION_PARTIAL_IMAGES (openai-provider.ts:125): the number of
// streaming partial preview images requested from codex. Go already consumes
// the resulting response.image_generation_call.partial_image events
// (loop_basic.go / provider_codex_ws.go), so the request must ask for them.
const codexImageGenerationPartialImages = 2

// codexModelInputModalities mirrors the TS CODEX_MODEL_INPUT_MODALITIES table
// (openai-provider.ts:128-139): codex models that do NOT list "image" as an
// input modality must not receive the native image_generation tool. Models not
// in the table default to text+image (DEFAULT_CODEX_INPUT_MODALITIES).
var codexModelInputModalities = map[string][]string{
	"gpt-5.5":             {"text", "image"},
	"gpt-5.4":             {"text", "image"},
	"gpt-5.4-mini":        {"text", "image"},
	"gpt-5.3-codex-spark": {"text"},
	"gpt-5.3-codex":       {"text", "image"},
	"gpt-5.2-codex":       {"text", "image"},
	"gpt-5.2":             {"text", "image"},
	"gpt-5.1-codex-max":   {"text", "image"},
	"gpt-5.1-codex":       {"text", "image"},
	"gpt-5.1-codex-mini":  {"text", "image"},
}

// supportsCodexImageGeneration mirrors the TS supportsCodexImageGeneration
// (openai-provider.ts:169-173): look up the bare model's input modalities
// (defaulting to text+image) and report whether "image" is supported.
func supportsCodexImageGeneration(model string) bool {
	modalities, ok := codexModelInputModalities[bareModelID(model)]
	if !ok {
		return true // DEFAULT_CODEX_INPUT_MODALITIES = ["text", "image"]
	}
	for _, modality := range modalities {
		if modality == "image" {
			return true
		}
	}
	return false
}

// applyCodexImageGenerationDefaults mirrors the TS
// applyCodexImageGenerationDefaults (openai-provider.ts:157-167): backfill
// output_format and partial_images on an already-present image_generation tool.
func applyCodexImageGenerationDefaults(tools []map[string]any) {
	for _, tool := range tools {
		if stringValue(tool["type"]) != "image_generation" {
			continue
		}
		if _, ok := tool["output_format"]; !ok {
			tool["output_format"] = "png"
		}
		if _, ok := tool["partial_images"]; !ok {
			tool["partial_images"] = codexImageGenerationPartialImages
		}
	}
}

func appendCodexNativeTools(tools []map[string]any, model string, useWebSearch bool, useImageGeneration bool) []map[string]any {
	if useWebSearch {
		if !hasResponseTool(tools, "web_search") {
			tools = append(tools, map[string]any{"type": "web_search"})
		}
	} else {
		tools = removeResponseTool(tools, "web_search")
	}
	if !useImageGeneration {
		return removeResponseTool(tools, "image_generation")
	}
	// Mirror TS appendCodexNativeTools (openai-provider.ts:189-198): gate the
	// image_generation tool on the model's input modalities (not a coarse
	// Contains("gpt")), and request partial_images so codex streams partial
	// previews. Backfill defaults when the tool is already present.
	if !supportsCodexImageGeneration(model) {
		return tools
	}
	if !hasResponseTool(tools, "image_generation") {
		tools = append(tools, map[string]any{
			"type":           "image_generation",
			"output_format":  "png",
			"partial_images": codexImageGenerationPartialImages,
		})
		return tools
	}
	applyCodexImageGenerationDefaults(tools)
	return tools
}

func optionalBoolDefault(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}

// codexWebSearchEnabledForScope reports whether the codex native web_search tool
// should be attached to the request, mirroring the TS gating
// `this.codexWebSearchEnabled && isNativeSearchChannelFirstEnabled()` plus the
// tool-scope visibility. The TS codexWebSearch config flag maps to
// provider.UseWebSearch (default true). The native-search-channel-first check is a
// tracked boundary: Go does not yet read the unified search-channel ordering from
// settings here, so it approximates that gate with the WebSearch tool's scope
// visibility — reflection scope (which hides WebSearch) must not advertise native
// web_search either, matching the intent that web_search is unavailable when the
// agent's tool inventory excludes it. (nativeSearchChannelFirstGating:false.)
func codexWebSearchEnabledForScope(provider Provider, toolScope string) bool {
	if !optionalBoolDefault(provider.UseWebSearch, true) {
		return false
	}
	for _, spec := range visibleToolSpecs(toolScope) {
		if spec.Name == "WebSearch" {
			return true
		}
	}
	return false
}

func removeResponseTool(tools []map[string]any, name string) []map[string]any {
	out := tools[:0]
	for _, tool := range tools {
		if stringValue(tool["type"]) == name || stringValue(tool["name"]) == name {
			continue
		}
		out = append(out, tool)
	}
	return out
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
		emitStreamEvent(ctx, out, StreamEvent{Parsed: ParsedStreamEvent{ToolUseChunk: &ToolUseChunk{ToolUseID: idValue, Name: item.Name, OutputIndex: outputIndex, InputCharsTotal: len(tool.Arguments)}, ResponsesAPI: true}})
		if tool.Arguments != "" && isParsableJSON(tool.Arguments) {
			emitStreamEvent(ctx, out, StreamEvent{Parsed: ParsedStreamEvent{ToolUseChunk: &ToolUseChunk{ToolUseID: idValue, Name: item.Name, Stop: true, OutputIndex: outputIndex, InputCharsTotal: len(tool.Arguments)}, ResponsesAPI: true}})
			tool.Stopped = true
		}
	case "web_search_call":
		emitResponsesWebSearchStatus(ctx, out, item.ID, "in_progress", outputIndex)
	case "image_generation_call":
		emitResponsesImageGenerationStatus(ctx, out, item.ID, "in_progress", outputIndex)
	}
}

func handleResponsesOutputItemDone(ctx context.Context, out chan<- StreamEvent, item *responsesOutputItem, outputIndex *int) {
	switch item.Type {
	case "web_search_call":
		query, queries := responsesWebSearchQueryAndQueries(item)
		emitStreamEvent(ctx, out, StreamEvent{Parsed: ParsedStreamEvent{WebSearch: &WebSearchEvent{ID: item.ID, Status: "completed", Query: query, Queries: queries, OutputIndex: outputIndex, Final: true, Action: item.Action}, ResponsesAPI: true}})
	case "image_generation_call":
		status := firstNonEmpty(item.Status, "completed")
		emitStreamEvent(ctx, out, StreamEvent{Parsed: ParsedStreamEvent{ImageGeneration: &ImageGenerationEvent{ID: item.ID, Status: status, RevisedPrompt: item.RevisedPrompt, Result: item.Result, OutputIndex: outputIndex, Final: true}, ResponsesAPI: true}})
	}
}

func emitResponsesWebSearchStatus(ctx context.Context, out chan<- StreamEvent, idValue, status string, outputIndex *int) {
	emitStreamEvent(ctx, out, StreamEvent{Parsed: ParsedStreamEvent{WebSearch: &WebSearchEvent{ID: idValue, Status: status, OutputIndex: outputIndex}, ResponsesAPI: true}})
}

func emitResponsesImageGenerationStatus(ctx context.Context, out chan<- StreamEvent, idValue, status string, outputIndex *int) {
	emitStreamEvent(ctx, out, StreamEvent{Parsed: ParsedStreamEvent{ImageGeneration: &ImageGenerationEvent{ID: idValue, Status: status, OutputIndex: outputIndex}, ResponsesAPI: true}})
}

// emitResponsesImageGenerationPartial mirrors the TypeScript
// response.image_generation_call.partial_image handling: it emits a "generating"
// status event that also carries the partial image index/base64 preview payload.
func emitResponsesImageGenerationPartial(ctx context.Context, out chan<- StreamEvent, idValue string, outputIndex, partialIndex *int, partialB64 string) {
	emitStreamEvent(ctx, out, StreamEvent{Parsed: ParsedStreamEvent{ImageGeneration: &ImageGenerationEvent{ID: idValue, Status: "generating", OutputIndex: outputIndex, PartialImageIndex: partialIndex, PartialImageB64: partialB64}, ResponsesAPI: true}})
}

func responsesWebSearchQueryAndQueries(item *responsesOutputItem) (string, []string) {
	if item == nil {
		return "", nil
	}
	query := item.Query
	queries := []string{}
	if item.Action != nil {
		if query == "" {
			query = stringValue(item.Action["query"])
		}
		queries = stringList(item.Action["queries"])
	}
	return query, queries
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
		"model":    provider.Model,
		"messages": messages,
		// No temperature, matching TS openai-provider.ts (see streamChatCompletions).
		"tools": toolDefinitions(toolScope),
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

// codexModelReasoningLevels mirrors the TS CODEX_MODEL_REASONING_LEVELS table
// (server/lib/agent/openai-provider.ts), extracted from the codex model catalog's
// supported_reasoning_levels. "none" is intentionally absent: it is handled by an
// early return in normalizeCodexReasoningEffort before this table is consulted.
var codexModelReasoningLevels = map[string][]string{
	"gpt-5.3-codex-spark": {"low", "medium", "high", "xhigh"},
	"gpt-5.3-codex":       {"low", "medium", "high", "xhigh"},
	"gpt-5.2-codex":       {"low", "medium", "high", "xhigh"},
	"gpt-5.1-codex-max":   {"low", "medium", "high", "xhigh"},
	"gpt-5.1-codex":       {"low", "medium", "high"},
	"gpt-5.1-codex-mini":  {"medium", "high"},
	"gpt-5.2":             {"low", "medium", "high", "xhigh"},
	"gpt-5.5":             {"low", "medium", "high", "xhigh"},
	"gpt-5.4":             {"low", "medium", "high", "xhigh"},
	"gpt-5.4-mini":        {"low", "medium", "high", "xhigh"},
}

// normalizeCodexReasoningEffort mirrors the TS normalizeCodexReasoningEffort:
// it clamps a requested reasoning effort to the per-model supported levels from
// codexModelReasoningLevels. Returns "" for an empty effort (no reasoning field);
// "none" passes through verbatim (codex sends effort:"none"); a supported level
// passes through; an unsupported level degrades via xhigh->high or low->medium,
// otherwise clamps to the model's lowest supported level. Models absent from the
// table keep the requested effort unchanged (matching TS `supported ?? requested`).
func normalizeCodexReasoningEffort(model, reasoningEffort string) string {
	effort := normalizeReasoningEffort(reasoningEffort)
	if effort == "" {
		return ""
	}
	if effort == "none" {
		return "none"
	}
	_, bare := splitModel(model)
	supported, ok := codexModelReasoningLevels[bare]
	contains := func(target string) bool {
		for _, level := range supported {
			if level == target {
				return true
			}
		}
		return false
	}
	// Codex has no "max" tier; degrade it to the model's highest available tier.
	if effort == "max" {
		if !ok {
			return "xhigh"
		}
		if contains("xhigh") {
			return "xhigh"
		}
		if contains("high") {
			return "high"
		}
		if len(supported) > 0 {
			return supported[len(supported)-1]
		}
		return "high"
	}
	if !ok {
		return effort
	}
	for _, level := range supported {
		if level == effort {
			return effort
		}
	}
	if effort == "xhigh" && contains("high") {
		return "high"
	}
	if effort == "low" && contains("medium") {
		return "medium"
	}
	if len(supported) > 0 {
		return supported[0]
	}
	return effort
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
	if reasoningEffort == "xhigh" || reasoningEffort == "max" {
		return "high"
	}
	return reasoningEffort
}
