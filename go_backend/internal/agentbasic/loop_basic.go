package agentbasic

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"narrafork-go/internal/config"
	"narrafork-go/internal/id"
)

type reasoningAccum struct {
	Text             string
	ProviderMetadata map[string]any
	OutputIndex      *int
}

type toolChunkAccum struct {
	ID                    string
	Name                  string
	Input                 string
	OutputIndex           *int
	StartedAt             int64
	ExtractedFilePath     string
	ExtractedFields       map[string]string
	StreamingMetadata     map[string]any
	ActiveStreamingField  string
	StreamingFieldYielded int
}

type toolExecOutcome struct {
	Run           ToolRun
	IsError       bool
	DurationMs    int64
	SideCars      []AgentSideCar
	SideCarsReady bool
}

func RunAgentLoop(ctx context.Context, settings *config.Store, model string, history []Message, cwd string, opts ...GenerateOption) (<-chan AgentEvent, error) {
	resolution, err := ResolveProviderAndModel(settings, model)
	if err != nil {
		return nil, err
	}
	if len(history) == 0 {
		return nil, errors.New("no messages to send")
	}
	options := normalizeGenerateOptions(opts...)
	if rules := customRetryRulesFromSettings(settings); len(rules) > 0 {
		options.customRetryRules = append(options.customRetryRules, rules...)
		options.customRetryRules = normalizeCustomRetryRules(options.customRetryRules)
	}
	return runAgentLoopWithSettings(ctx, settings, resolution, history, cwd, options), nil
}

func runAgentLoop(ctx context.Context, resolution ProviderResolution, history []Message, cwd string, options generateOptions) <-chan AgentEvent {
	return runAgentLoopWithSettings(ctx, nil, resolution, history, cwd, options)
}

func runAgentLoopWithSettings(ctx context.Context, settings *config.Store, resolution ProviderResolution, history []Message, cwd string, options generateOptions) <-chan AgentEvent {
	out := make(chan AgentEvent, 32)
	go func() {
		defer close(out)
		runAgentLoopSync(ctx, out, settings, resolution, history, cwd, options)
	}()
	return out
}

func runAgentLoopSync(ctx context.Context, out chan<- AgentEvent, settings *config.Store, resolution ProviderResolution, history []Message, cwd string, options generateOptions) {
	adapter := resolution.Adapter
	if adapter == nil {
		emitAgentEvent(ctx, out, AgentEvent{Type: "error", Message: "provider adapter is not configured"})
		return
	}
	built, err := adapter.BuildHistory(history, resolution.Model, "")
	if err != nil {
		emitAgentEvent(ctx, out, AgentEvent{Type: "error", Message: err.Error()})
		return
	}
	adapter.InjectSystemPrompt(&built, options.systemPrompt, resolution.Model, "")
	inventoryOptions := ToolInventoryOptions{Scope: options.toolScope, EnabledTools: options.enabledTools, DisabledTools: options.disabledTools, ExternalTools: options.externalTools, MCPExternalToolInjection: len(options.externalTools) > 0}
	if options.emitToolInventory {
		emitAgentEvent(ctx, out, AgentEvent{Type: "tool_inventory", Metadata: map[string]any{"inventory": ToolInventoryMetadataForOptions(inventoryOptions)}})
	}
	toolExecutor := options.toolExecutor
	if toolExecutor == nil {
		toolExecutor = defaultToolExecutor
	}
	toolExecutor = guardToolExecutor(toolExecutor, inventoryOptions)
	for turn := 0; turn < options.maxTurns; turn++ {
		if options.beforeTurnHook != nil {
			if err := options.beforeTurnHook(turn); err != nil {
				emitAgentEvent(ctx, out, AgentEvent{Type: "error", Message: err.Error()})
				return
			}
		}
		if turn > 0 {
			if !applyRuntimeSettingsOverride(ctx, out, settings, turn, &resolution, &adapter, &options) {
				return
			}
			if !applyBeforeTurnReplacement(ctx, out, turn, adapter, resolution, &built, &options) {
				return
			}
		}
		state, ok := runAgentTurnWithRetry(ctx, out, adapter, resolution, &built, cwd, options, toolExecutor)
		if !ok {
			return
		}
		if !drainSettledToolResults(ctx, out, state, options) {
			emitAgentEvent(context.Background(), out, AgentEvent{Type: "error", Message: "Aborted"})
			return
		}
		flushTurnBlocks(ctx, out, state)
		emitAgentEvent(ctx, out, AgentEvent{Type: "assistant_message", Text: state.assistantText, ToolUses: append([]ToolCall(nil), state.toolUses...), CredentialID: state.credentialID})
		if len(state.toolUses) == 0 {
			emitAgentEvent(ctx, out, AgentEvent{Type: "done"})
			return
		}
		adapter.PushAssistantTurn(&built, state.assistantText, state.toolUses, nil)
		toolResults, ok := executeTurnTools(ctx, out, adapter, cwd, toolExecutor, state, options)
		if !ok {
			return
		}
		options.sideCarInitialCompletedToolCount = state.completedToolCount
		options.silentToolCallCount = state.silentToolCallCount
		afterToolsSideCars := collectAfterToolsSideCars(ctx, options)
		if len(afterToolsSideCars) > 0 {
			emitAgentEvent(ctx, out, AgentEvent{Type: "sidecars", SideCars: afterToolsSideCars})
		}
		adapter.PushUserTurn(&built, appendAgentSideCarsForProvider("", afterToolsSideCars), toolResults)
		emitAgentEvent(ctx, out, AgentEvent{Type: "turn_complete", TurnIndex: turn})
		if options.shouldStopHook != nil && options.shouldStopHook() {
			return
		}
	}
	emitAgentEvent(ctx, out, AgentEvent{Type: "error", Message: "tool loop reached maximum rounds without final answer"})
}

func applyRuntimeSettingsOverride(ctx context.Context, out chan<- AgentEvent, settings *config.Store, turn int, resolution *ProviderResolution, adapter *ProviderAdapter, options *generateOptions) bool {
	if options.runtimeSettingsOverrideHook == nil {
		return true
	}
	override, err := options.runtimeSettingsOverrideHook(turn)
	if err != nil {
		emitAgentEvent(ctx, out, AgentEvent{Type: "error", Message: err.Error()})
		return false
	}
	if override == nil {
		return true
	}
	modelChanged := false
	reasoningChanged := false
	if requestedModel := strings.TrimSpace(override.Model); requestedModel != "" {
		if prefix, _ := splitModel(requestedModel); prefix == "" && resolution.Provider != "" && !strings.HasPrefix(requestedModel, "__") {
			requestedModel = resolution.Provider + ":" + requestedModel
		}
		nextResolution, err := ResolveProviderAndModel(settings, requestedModel)
		if err != nil {
			emitAgentEvent(ctx, out, AgentEvent{Type: "error", Message: err.Error()})
			return false
		}
		if nextResolution.Adapter == nil {
			emitAgentEvent(ctx, out, AgentEvent{Type: "error", Message: "provider adapter is not configured"})
			return false
		}
		if nextResolution.Model != resolution.Model || nextResolution.Provider != resolution.Provider {
			modelChanged = true
		}
		*resolution = nextResolution
		*adapter = nextResolution.Adapter
	}
	if override.ReasoningEffort != nil {
		nextReasoning := normalizeReasoningEffort(*override.ReasoningEffort)
		if nextReasoning != options.reasoningEffort {
			reasoningChanged = true
			options.reasoningEffort = nextReasoning
		}
	}
	if modelChanged || reasoningChanged {
		switched := &ModelSwitchedEvent{Model: resolution.Model, Provider: resolution.Provider, ReasoningEffort: options.reasoningEffort, Cause: "turn"}
		emitAgentEvent(ctx, out, AgentEvent{Type: "model_switched", Provider: switched.Provider, Model: switched.Model, Message: switched.Cause, Metadata: map[string]any{"reasoningEffort": switched.ReasoningEffort}, ModelSwitched: switched})
	}
	return true
}

func applyBeforeTurnReplacement(ctx context.Context, out chan<- AgentEvent, turn int, adapter ProviderAdapter, resolution ProviderResolution, built *BuiltHistory, options *generateOptions) bool {
	if options.beforeTurnReplacementHook == nil {
		return true
	}
	replacement, err := options.beforeTurnReplacementHook(turn)
	if err != nil {
		emitAgentEvent(ctx, out, AgentEvent{Type: "error", Message: err.Error()})
		return false
	}
	if replacement == nil {
		return true
	}
	rebuilt, err := adapter.BuildHistory(replacement.Messages, resolution.Model, "")
	if err != nil {
		emitAgentEvent(ctx, out, AgentEvent{Type: "error", Message: err.Error()})
		return false
	}
	if replacement.SystemPrompt != nil {
		options.systemPrompt = strings.TrimSpace(*replacement.SystemPrompt)
	}
	adapter.InjectSystemPrompt(&rebuilt, options.systemPrompt, resolution.Model, "")
	*built = rebuilt
	return true
}

type turnState struct {
	assistantText       string
	textOutputIndex     *int
	toolUses            []ToolCall
	seenToolUses        map[string]bool
	emittedToolCalls    map[string]bool
	reasoning           map[string]*reasoningAccum
	redacted            []*RedactedThinking
	credentialID        string
	earlyExec           map[string]<-chan toolExecOutcome
	completedOutcomes   map[string]toolExecOutcome
	yieldedToolResults  map[string]bool
	eagerBlocked        bool
	completedToolCount  int
	silentToolCallCount int
}

func newTurnState(initialCompletedToolCount, silentToolCallCount int) *turnState {
	return &turnState{seenToolUses: map[string]bool{}, emittedToolCalls: map[string]bool{}, reasoning: map[string]*reasoningAccum{}, earlyExec: map[string]<-chan toolExecOutcome{}, completedOutcomes: map[string]toolExecOutcome{}, yieldedToolResults: map[string]bool{}, completedToolCount: initialCompletedToolCount, silentToolCallCount: silentToolCallCount}
}

func runAgentTurnWithRetry(ctx context.Context, out chan<- AgentEvent, adapter ProviderAdapter, resolution ProviderResolution, built *BuiltHistory, cwd string, options generateOptions, toolExecutor ToolExecutor) (*turnState, bool) {
	emptyRetries := 0
	chatRetries := 0
	for {
		state, err := runAgentTurnAttempt(ctx, out, adapter, resolution, built, cwd, options, toolExecutor)
		if err == nil {
			if state.assistantText == "" && len(state.toolUses) == 0 && len(state.reasoning) == 0 {
				if emptyRetries < options.emptyResponseRetries {
					emptyRetries++
					message := resolution.Provider + ": provider returned empty response"
					if !emitRetrying(ctx, out, message, emptyRetries, options.emptyResponseRetries, retryDelay(options.retryBaseDelay, emptyRetries)) {
						return nil, false
					}
					if !sleepWithContext(ctx, retryDelay(options.retryBaseDelay, emptyRetries)) {
						emitAgentEvent(context.Background(), out, AgentEvent{Type: "error", Message: "Aborted"})
						return nil, false
					}
					continue
				}
				emitAgentEvent(ctx, out, AgentEvent{Type: "invalid_state", Reason: "empty_response", Message: resolution.Provider + ": provider returned empty response"})
				return nil, false
			}
			return state, true
		}
		if errors.Is(err, errTerminalEvent) {
			return nil, false
		}
		if isCodexRebuildHistoryRetryError(err) {
			if state != nil {
				flushTurnBlocks(ctx, out, state)
			}
			emitAgentEvent(ctx, out, AgentEvent{Type: "retryable_error", Code: CodexRebuildHistoryRetryCode, Message: err.Error(), Metadata: codexRebuildHistoryRetryMetadata(err)})
			return nil, false
		}
		var retryInvalidStateErr *providerInvalidStateError
		if errors.As(err, &retryInvalidStateErr) && state != nil && len(state.earlyExec) > 0 {
			flushTurnBlocks(ctx, out, state)
			emitAgentEvent(ctx, out, AgentEvent{Type: "invalid_state", Reason: retryInvalidStateErr.Reason, Message: retryInvalidStateErr.Message})
			return nil, false
		}
		if !isTransientAgentError(err, options.customRetryRules) {
			if state != nil {
				flushTurnBlocks(ctx, out, state)
			}
			var invalidStateErr *providerInvalidStateError
			if errors.As(err, &invalidStateErr) {
				emitAgentEvent(ctx, out, AgentEvent{Type: "invalid_state", Reason: invalidStateErr.Reason, Message: invalidStateErr.Message})
				return nil, false
			}
			emitAgentEvent(ctx, out, AgentEvent{Type: "error", Message: err.Error()})
			return nil, false
		}
		if chatRetries >= options.maxChatRetries {
			if state != nil {
				flushTurnBlocks(ctx, out, state)
			}
			emitAgentEvent(ctx, out, AgentEvent{Type: "retryable_error", Message: err.Error()})
			return nil, false
		}
		chatRetries++
		delay := retryDelay(options.retryBaseDelay, chatRetries)
		if !emitRetrying(ctx, out, err.Error(), chatRetries, options.maxChatRetries, delay) {
			return nil, false
		}
		if !sleepWithContext(ctx, delay) {
			emitAgentEvent(context.Background(), out, AgentEvent{Type: "error", Message: "Aborted"})
			return nil, false
		}
	}
}

func runAgentTurnAttempt(ctx context.Context, out chan<- AgentEvent, adapter ProviderAdapter, resolution ProviderResolution, built *BuiltHistory, cwd string, options generateOptions, toolExecutor ToolExecutor) (*turnState, error) {
	effectiveToolScope := encodedToolScope(options.toolScope, options.enabledTools, options.disabledTools)
	turnCtx, cancelTurn := context.WithCancel(ctx)
	defer cancelTurn()
	inventoryOptions := ToolInventoryOptions{Scope: options.toolScope, EnabledTools: options.enabledTools, DisabledTools: options.disabledTools, ExternalTools: options.externalTools, MCPExternalToolInjection: len(options.externalTools) > 0}
	requestID := id.New()
	requestStartedAt := time.Now()
	requestCredentialID := strings.TrimSpace(resolution.Config.CredentialID)
	emitAgentEvent(ctx, out, AgentEvent{Type: "api_request_start", RequestID: requestID, Provider: resolution.Provider, Model: resolution.Model, CredentialID: requestCredentialID})
	var lastUsage *UsageSnapshot
	requestContextPercent := 0.0
	requestMeterUsage := 0.0
	requestMeterUnit := ""
	apiRequestFinished := false
	finishAPIRequest := func(message string) {
		if apiRequestFinished {
			return
		}
		apiRequestFinished = true
		metadata := map[string]any{}
		if requestMeterUsage > 0 {
			metadata["meterUsage"] = requestMeterUsage
			if strings.TrimSpace(requestMeterUnit) != "" {
				metadata["meterUnit"] = requestMeterUnit
			}
		}
		if len(metadata) == 0 {
			metadata = nil
		}
		emitAgentEvent(ctx, out, AgentEvent{Type: "api_request_end", RequestID: requestID, Provider: resolution.Provider, Model: resolution.Model, CredentialID: requestCredentialID, Usage: lastUsage, Message: strings.TrimSpace(message), DurationMs: time.Since(requestStartedAt).Milliseconds(), Percentage: requestContextPercent, Metadata: metadata})
	}
	stream, err := adapter.Chat(turnCtx, ChatParams{
		Model:            resolution.Model,
		Cwd:              cwd,
		History:          built,
		Tools:            formatToolsForAdapter(adapter, inventoryOptions),
		ReasoningEffort:  options.reasoningEffort,
		SystemPrompt:     options.systemPrompt,
		ToolScope:        effectiveToolScope,
		StickySessionKey: options.stickySessionKey,
	})
	if err != nil {
		finishAPIRequest(err.Error())
		return nil, err
	}
	defer finishAPIRequest("")
	state := newTurnState(options.sideCarInitialCompletedToolCount, options.silentToolCallCount)
	chunks := map[string]*toolChunkAccum{}
	var firstTokenTimer *time.Timer
	var firstTokenC <-chan time.Time
	if options.firstTokenTimeout > 0 {
		firstTokenTimer = time.NewTimer(options.firstTokenTimeout)
		firstTokenC = firstTokenTimer.C
		defer firstTokenTimer.Stop()
	}
	for {
		select {
		case <-firstTokenC:
			message := fmt.Sprintf("Provider did not produce a response within %dms", options.firstTokenTimeout.Milliseconds())
			finishAPIRequest(message)
			emitAgentEvent(ctx, out, AgentEvent{
				Type:       "first_token_timeout",
				Message:    message,
				Provider:   resolution.Provider,
				Model:      resolution.Model,
				DurationMs: options.firstTokenTimeout.Milliseconds(),
				Metadata:   map[string]any{"timeoutMs": options.firstTokenTimeout.Milliseconds()},
			})
			return state, errTerminalEvent
		case event, ok := <-stream:
			if !ok {
				return state, nil
			}
			if event.Err != nil {
				finishAPIRequest(event.Err.Error())
				return state, event.Err
			}
			parsed := event.Parsed
			if parsed.Usage != nil {
				lastUsage = parsed.Usage
			}
			if firstTokenC != nil && parsedHasMeaningfulResponse(parsed) {
				if firstTokenTimer != nil {
					firstTokenTimer.Stop()
				}
				firstTokenC = nil
			}
			if parsed.SilentDisconnect {
				message := "Provider stream ended without a final response"
				finishAPIRequest(message)
				emitAgentEvent(ctx, out, AgentEvent{Type: "silent_disconnect", Provider: resolution.Provider, Model: resolution.Model, Message: message})
				return state, errTerminalEvent
			}
			if parsed.CredentialID != "" {
				requestCredentialID = strings.TrimSpace(parsed.CredentialID)
				state.credentialID = requestCredentialID
			}
			if parsed.TextOutputIndex != nil {
				state.textOutputIndex = parsed.TextOutputIndex
			}
			if parsed.Text != "" {
				state.assistantText += parsed.Text
				if strings.TrimSpace(parsed.Text) != "" {
					state.silentToolCallCount = 0
				}
				emitAgentEvent(ctx, out, AgentEvent{Type: "stream_text", Text: parsed.Text, OutputIndex: parsed.TextOutputIndex})
			}
			if parsed.Reasoning != "" || parsed.ReasoningMetadata != nil {
				key := reasoningKey(parsed)
				entry := state.reasoning[key]
				if entry == nil {
					entry = &reasoningAccum{}
					state.reasoning[key] = entry
				}
				entry.Text += parsed.Reasoning
				entry.ProviderMetadata = parsed.ReasoningMetadata
				entry.OutputIndex = parsed.ReasoningOutputIndex
				emitAgentEvent(ctx, out, AgentEvent{Type: "stream_reasoning", Text: parsed.Reasoning, OutputIndex: parsed.ReasoningOutputIndex, Metadata: parsed.ReasoningMetadata})
			}
			if parsed.RedactedThinking != nil {
				state.redacted = append(state.redacted, parsed.RedactedThinking)
			}
			if parsed.ContextUsagePercentage != nil {
				requestContextPercent = *parsed.ContextUsagePercentage
				emitAgentEvent(ctx, out, AgentEvent{Type: "context_usage", Percentage: *parsed.ContextUsagePercentage, Usage: parsed.Usage})
			} else if parsed.Usage != nil {
				if pct := usageContextPercent(parsed.Usage); pct > 0 {
					requestContextPercent = pct
				}
				emitAgentEvent(ctx, out, AgentEvent{Type: "context_usage", Usage: parsed.Usage})
			}
			if parsed.Metering != nil {
				requestMeterUsage = parsed.Metering.Usage
				requestMeterUnit = parsed.Metering.Unit
			}
			emitProviderLifecycleEvents(ctx, out, parsed, resolution.Provider, resolution.Model, requestCredentialID)
			if parsed.InvalidState != nil {
				finishAPIRequest(firstNonEmpty(parsed.InvalidState.Message, parsed.InvalidState.Reason))
				return state, &providerInvalidStateError{
					Reason:  parsed.InvalidState.Reason,
					Message: parsed.InvalidState.Message,
				}
			}
			if parsed.ToolUseChunk != nil {
				handleToolUseChunk(ctx, out, state, chunks, parsed.ToolUseChunk, cwd, toolExecutor)
			}
			for _, call := range parsed.ToolUses {
				addToolUse(ctx, out, state, call, cwd, toolExecutor)
			}
			if !drainSettledToolResults(ctx, out, state, options) {
				emitAgentEvent(context.Background(), out, AgentEvent{Type: "error", Message: "Aborted"})
				return state, errTerminalEvent
			}
		}
	}
}

var errTerminalEvent = errors.New("terminal agent event emitted")

func parsedHasMeaningfulResponse(parsed ParsedStreamEvent) bool {
	return parsed.Text != "" || parsed.Reasoning != "" || len(parsed.ToolUses) > 0 || parsed.ToolUseChunk != nil || parsed.WebSearch != nil || parsed.ImageGeneration != nil || parsed.InvalidState != nil || parsed.OutputTruncated != "" || parsed.APIRequest != nil || parsed.Metering != nil || parsed.QuotaBalance != nil || parsed.NkpQuotaBalance != nil || parsed.QueueStatus != nil || parsed.NkpQueue != nil
}

func usageContextPercent(usage *UsageSnapshot) float64 {
	if usage == nil || usage.ContextWindow <= 0 {
		return 0
	}
	inputTokens := usage.PromptTokens
	if inputTokens == 0 {
		inputTokens = usage.InputTokens
	}
	if inputTokens <= 0 {
		return 0
	}
	pct := float64(inputTokens) / float64(usage.ContextWindow) * 100
	if pct > 100 {
		return 100
	}
	return pct
}

func handleToolUseChunk(ctx context.Context, out chan<- AgentEvent, state *turnState, chunks map[string]*toolChunkAccum, chunk *ToolUseChunk, cwd string, toolExecutor ToolExecutor) {
	idValue := strings.TrimSpace(chunk.ToolUseID)
	if idValue == "" {
		idValue = id.Must(12)
	}
	acc := chunks[idValue]
	if acc == nil {
		acc = &toolChunkAccum{ID: idValue, StartedAt: time.Now().UnixMilli()}
		chunks[idValue] = acc
	}
	if strings.TrimSpace(chunk.Name) != "" {
		acc.Name = strings.TrimSpace(chunk.Name)
	}
	if chunk.OutputIndex != nil {
		acc.OutputIndex = chunk.OutputIndex
	}
	acc.Input += chunk.Input
	enriched := enrichStreamingToolChunk(acc, chunk, cwd)
	emitAgentEvent(ctx, out, AgentEvent{Type: "tool_use_chunk", ToolUseID: idValue, ToolName: acc.Name, OutputIndex: acc.OutputIndex, Streaming: enriched, StreamStartedAt: acc.StartedAt})
	if chunk.Stop {
		name := acc.Name
		if name == "" {
			name = chunk.Name
		}
		call := newFunctionToolCall(idValue, name, acc.Input)
		call.StreamStartedAt = acc.StartedAt
		call.OutputIndex = acc.OutputIndex
		addToolUse(ctx, out, state, call, cwd, toolExecutor)
		delete(chunks, idValue)
	}
}

func addToolUse(ctx context.Context, out chan<- AgentEvent, state *turnState, call ToolCall, cwd string, toolExecutor ToolExecutor) {
	if strings.TrimSpace(call.ID) == "" {
		call.ID = id.Must(12)
	}
	if call.Type == "" {
		call.Type = "function"
	}
	if state.seenToolUses[call.ID] {
		return
	}
	state.seenToolUses[call.ID] = true
	state.toolUses = append(state.toolUses, call)
	emitAgentEvent(ctx, out, AgentEvent{Type: "block_complete", Block: toolUseBlock(call)})
	emitToolCallIfMissing(ctx, out, call, state.emittedToolCalls)
	if isStrictSerialTool(call) {
		state.eagerBlocked = true
		return
	}
	if !state.eagerBlocked && shouldEagerExecuteTool(call) {
		state.earlyExec[call.ID] = startToolExecution(ctx, out, cwd, toolExecutor, call)
	}
}

func emitToolCallIfMissing(ctx context.Context, out chan<- AgentEvent, call ToolCall, emitted map[string]bool) {
	if emitted[call.ID] {
		return
	}
	emitted[call.ID] = true
	emitAgentEvent(ctx, out, AgentEvent{Type: "tool_call", ToolUseID: call.ID, ToolName: call.Function.Name, Input: toolCallInput(call), StreamStartedAt: call.StreamStartedAt})
}

func executeTurnTools(ctx context.Context, out chan<- AgentEvent, adapter ProviderAdapter, cwd string, toolExecutor ToolExecutor, state *turnState, options generateOptions) ([]any, bool) {
	toolResults := []any{}
	for _, group := range toolExecutionGroups(state.toolUses) {
		outcomes := make([]toolExecOutcome, len(group))
		if len(group) == 1 {
			call := group[0]
			outcome, ok := awaitOrRunTool(ctx, out, cwd, toolExecutor, state, call)
			if !ok {
				emitAgentEvent(context.Background(), out, AgentEvent{Type: "error", Message: "Aborted"})
				return nil, false
			}
			outcomes[0] = outcome
		} else {
			resultCh := make(chan struct {
				Index   int
				Outcome toolExecOutcome
			}, len(group))
			for idx, call := range group {
				if completed, ok := state.completedOutcomes[call.ID]; ok {
					outcomes[idx] = completed
					continue
				}
				if early, ok := state.earlyExec[call.ID]; ok {
					go func(index int, early <-chan toolExecOutcome) {
						result := <-early
						resultCh <- struct {
							Index   int
							Outcome toolExecOutcome
						}{Index: index, Outcome: result}
					}(idx, early)
					continue
				}
				go func(index int, toolCall ToolCall) {
					resultCh <- struct {
						Index   int
						Outcome toolExecOutcome
					}{Index: index, Outcome: executeToolCall(ctx, out, cwd, toolExecutor, toolCall)}
				}(idx, call)
			}
			for remaining := countMissingOutcomes(outcomes); remaining > 0; remaining-- {
				select {
				case <-ctx.Done():
					emitAgentEvent(context.Background(), out, AgentEvent{Type: "error", Message: "Aborted"})
					return nil, false
				case result := <-resultCh:
					outcomes[result.Index] = result.Outcome
					state.completedOutcomes[group[result.Index].ID] = result.Outcome
				}
			}
		}
		for idx, call := range group {
			emitToolCallIfMissing(ctx, out, call, state.emittedToolCalls)
			outcome := ensureOutcomeSideCars(ctx, options, state, call, outcomes[idx])
			run := outcome.Run
			outputForModel := appendAgentSideCarsForProvider(run.Output, outcome.SideCars)
			if !state.yieldedToolResults[call.ID] {
				emitToolResultEvent(ctx, out, state, call, outcome)
			}
			toolResults = append(toolResults, adapter.FormatToolResult(call.ID, outputForModel, outcome.IsError))
		}
	}
	return toolResults, true
}

func collectAfterToolsSideCars(ctx context.Context, options generateOptions) []AgentSideCar {
	if options.sideCarCollector == nil {
		return nil
	}
	collected := options.sideCarCollector(ctx, SideCarRequest{Phase: "after_tools"})
	sideCars := []AgentSideCar{}
	for _, sideCar := range collected {
		if sideCar.Target == "user_message" {
			sideCars = append(sideCars, sideCar)
		}
	}
	return normalizeAgentSideCars(sideCars, "", false)
}

func collectToolResultSideCars(ctx context.Context, options generateOptions, state *turnState, call ToolCall, outcome toolExecOutcome) []AgentSideCar {
	sideCars := append([]AgentSideCar(nil), outcome.SideCars...)
	if !outcome.IsError && options.silentToolCallThreshold >= 0 {
		state.silentToolCallCount++
		if state.silentToolCallCount >= options.silentToolCallThreshold {
			sideCars = append(sideCars, AgentSideCar{Target: "tool_result", Source: "silent_progress", Content: silentProgressReminder(state.silentToolCallCount), ToolUseID: call.ID, OrderIndex: 10})
			state.silentToolCallCount = 0
		}
	}
	if options.sideCarCollector == nil || call.Function.Name == "TaskCreate" {
		return normalizeAgentSideCars(sideCars, call.ID, true)
	}
	state.completedToolCount++
	if options.onSideCarCompletedToolCount != nil {
		options.onSideCarCompletedToolCount(state.completedToolCount)
	}
	collected := options.sideCarCollector(ctx, SideCarRequest{Phase: "tool_result", ToolName: call.Function.Name, ToolUseID: call.ID, ToolInput: toolCallInput(call), CompletedToolCount: state.completedToolCount})
	for _, sideCar := range collected {
		if sideCar.Target == "tool_result" {
			sideCars = append(sideCars, sideCar)
		}
	}
	return normalizeAgentSideCars(sideCars, call.ID, true)
}

func silentProgressReminder(count int) string {
	return fmt.Sprintf("<progress_update_request>\nYou have completed %d tool call(s) since your last visible text reply. Before calling any more tools, briefly tell the user in one sentence what you are working on right now, then continue.\n</progress_update_request>", count)
}

func normalizeAgentSideCars(sideCars []AgentSideCar, fallbackToolUseID string, forceToolUseID bool) []AgentSideCar {
	items := []AgentSideCar{}
	for idx, sideCar := range sideCars {
		if strings.TrimSpace(sideCar.Target) == "" || strings.TrimSpace(sideCar.Content) == "" {
			continue
		}
		sideCar.Target = strings.TrimSpace(sideCar.Target)
		sideCar.Source = strings.TrimSpace(sideCar.Source)
		if sideCar.Source == "" {
			sideCar.Source = "sidecar"
		}
		if forceToolUseID && strings.TrimSpace(fallbackToolUseID) != "" {
			sideCar.ToolUseID = strings.TrimSpace(fallbackToolUseID)
		} else {
			sideCar.ToolUseID = strings.TrimSpace(sideCar.ToolUseID)
		}
		if sideCar.OrderIndex == 0 {
			sideCar.OrderIndex = idx
		}
		items = append(items, sideCar)
	}
	return items
}

func appendAgentSideCarsForProvider(baseText string, sideCars []AgentSideCar) string {
	if len(sideCars) == 0 {
		return baseText
	}
	parts := []string{}
	for _, sideCar := range sideCars {
		if strings.TrimSpace(sideCar.Content) == "" {
			continue
		}
		source := strings.TrimSpace(sideCar.Source)
		if source == "" {
			source = "sidecar"
		}
		parts = append(parts, fmt.Sprintf("<side_car source=\"%s\">\n%s\n</side_car>", escapeSideCarAttribute(source), sideCar.Content))
	}
	if len(parts) == 0 {
		return baseText
	}
	sideCarText := strings.Join(parts, "\n\n")
	if strings.TrimSpace(baseText) == "" {
		return sideCarText
	}
	return baseText + "\n\n" + sideCarText
}

func escapeSideCarAttribute(value string) string {
	replacer := strings.NewReplacer("&", "&amp;", "\"", "&quot;", "<", "&lt;")
	return replacer.Replace(value)
}

func guardToolExecutor(executor ToolExecutor, opts ToolInventoryOptions) ToolExecutor {
	allowed := map[string]bool{}
	for _, spec := range resolveToolSpecs(opts) {
		allowed[spec.Name] = true
	}
	return func(ctx context.Context, cwd string, call ToolCall) (ToolRun, error) {
		name := strings.TrimSpace(call.Function.Name)
		if !allowed[name] {
			return ToolRun{ID: call.ID, Name: name, Arguments: call.Function.Arguments, Output: "tool is disabled or unavailable in the current Go tool inventory: " + name, Status: "denied"}, nil
		}
		return executor(ctx, cwd, call)
	}
}

func countMissingOutcomes(outcomes []toolExecOutcome) int {
	count := 0
	for _, outcome := range outcomes {
		if strings.TrimSpace(outcome.Run.ID) == "" {
			count++
		}
	}
	return count
}

func drainSettledToolResults(ctx context.Context, out chan<- AgentEvent, state *turnState, options generateOptions) bool {
	if state == nil || len(state.earlyExec) == 0 {
		return true
	}
	for _, call := range state.toolUses {
		if state.yieldedToolResults[call.ID] {
			continue
		}
		outcome, ok := state.completedOutcomes[call.ID]
		if !ok {
			early := state.earlyExec[call.ID]
			if early == nil {
				continue
			}
			select {
			case <-ctx.Done():
				return false
			case outcome = <-early:
				state.completedOutcomes[call.ID] = outcome
				delete(state.earlyExec, call.ID)
			default:
				continue
			}
		}
		outcome = ensureOutcomeSideCars(ctx, options, state, call, outcome)
		if !emitToolResultEvent(ctx, out, state, call, outcome) {
			return false
		}
	}
	return true
}

func ensureOutcomeSideCars(ctx context.Context, options generateOptions, state *turnState, call ToolCall, outcome toolExecOutcome) toolExecOutcome {
	if outcome.SideCarsReady {
		state.completedOutcomes[call.ID] = outcome
		return outcome
	}
	outcome.SideCars = collectToolResultSideCars(ctx, options, state, call, outcome)
	outcome.SideCarsReady = true
	state.completedOutcomes[call.ID] = outcome
	return outcome
}

func emitToolResultEvent(ctx context.Context, out chan<- AgentEvent, state *turnState, call ToolCall, outcome toolExecOutcome) bool {
	if state.yieldedToolResults[call.ID] {
		return true
	}
	state.yieldedToolResults[call.ID] = true
	run := outcome.Run
	return emitAgentEvent(ctx, out, AgentEvent{Type: "tool_result", ToolUseID: run.ID, ToolName: run.Name, Output: run.Output, IsError: outcome.IsError, DurationMs: outcome.DurationMs, ToolRun: &run, SideCars: outcome.SideCars})
}

func awaitOrRunTool(ctx context.Context, out chan<- AgentEvent, cwd string, toolExecutor ToolExecutor, state *turnState, call ToolCall) (toolExecOutcome, bool) {
	if completed, ok := state.completedOutcomes[call.ID]; ok {
		return completed, true
	}
	if early, ok := state.earlyExec[call.ID]; ok {
		select {
		case <-ctx.Done():
			return toolExecOutcome{}, false
		case outcome := <-early:
			state.completedOutcomes[call.ID] = outcome
			delete(state.earlyExec, call.ID)
			return outcome, true
		}
	}
	outcome := executeToolCall(ctx, out, cwd, toolExecutor, call)
	state.completedOutcomes[call.ID] = outcome
	return outcome, true
}

func startToolExecution(ctx context.Context, out chan<- AgentEvent, cwd string, toolExecutor ToolExecutor, call ToolCall) <-chan toolExecOutcome {
	ch := make(chan toolExecOutcome, 1)
	go func() {
		ch <- executeToolCall(ctx, out, cwd, toolExecutor, call)
		close(ch)
	}()
	return ch
}

var toolProgressInterval = 5 * time.Second

func executeToolCall(ctx context.Context, out chan<- AgentEvent, cwd string, toolExecutor ToolExecutor, call ToolCall) toolExecOutcome {
	started := time.Now()
	execCtx := ContextWithToolRuntimeEvents(ctx, NewToolRuntimeEventSink(call.ID, func(event AgentEvent) {
		emitAgentEvent(ctx, out, event)
	}))
	done := make(chan struct{})
	if toolProgressInterval > 0 {
		go func() {
			ticker := time.NewTicker(toolProgressInterval)
			defer ticker.Stop()
			for {
				select {
				case <-done:
					return
				case <-ctx.Done():
					return
				case tick := <-ticker.C:
					elapsed := int64(tick.Sub(started).Seconds())
					if elapsed <= 0 {
						elapsed = 1
					}
					emitAgentEvent(ctx, out, AgentEvent{Type: "tool_progress", ToolUseID: call.ID, Elapsed: elapsed})
				}
			}
		}()
	}
	run, err := toolExecutor(execCtx, cwd, call)
	close(done)
	FlushToolRuntimeEvents(execCtx)
	if err != nil {
		run = ToolRun{ID: call.ID, Name: call.Function.Name, Arguments: call.Function.Arguments, Output: "Tool error: " + err.Error(), Status: "error", Error: err.Error()}
	}
	run = normalizeToolRun(run, call)
	if run.Status != "completed" && strings.TrimSpace(run.Error) == "" {
		run.Error = run.Output
	}
	isError := run.Status != "" && run.Status != "completed"
	if strings.TrimSpace(run.Error) != "" {
		isError = true
	}
	return toolExecOutcome{Run: run, IsError: isError, DurationMs: time.Since(started).Milliseconds()}
}

func toolExecutionGroups(toolUses []ToolCall) [][]ToolCall {
	groups := [][]ToolCall{}
	for _, call := range toolUses {
		isParallel := isParallelTool(call) && !isStrictSerialTool(call)
		if len(groups) > 0 {
			last := groups[len(groups)-1]
			if isParallel && len(last) > 0 && isParallelTool(last[0]) && !isStrictSerialTool(last[0]) {
				groups[len(groups)-1] = append(last, call)
				continue
			}
		}
		groups = append(groups, []ToolCall{call})
	}
	return groups
}

func isParallelTool(call ToolCall) bool {
	switch call.Function.Name {
	case "Agent", "Read", "Glob", "Grep", "WebSearch", "WebFetch", "Await", "Send", "Bash":
		return true
	default:
		return false
	}
}

func shouldEagerExecuteTool(call ToolCall) bool {
	if strings.HasPrefix(call.Function.Name, "mcp__") {
		return false
	}
	switch call.Function.Name {
	case "Bash", "Shell", "Execute", "Agent", "Write", "Edit", "Browser", "Terminal", "ShareFile", "NarraForkAdmin", "ForkNarrator", "UpdateGoal":
		return false
	default:
		return true
	}
}

func isStrictSerialTool(call ToolCall) bool {
	input := toolCallInput(call)
	if call.Function.Name == "Bash" && boolValue(input["strict_serial"]) {
		return true
	}
	switch call.Function.Name {
	case "StartPipeline", "EndPipeline", "ExitPlanMode":
		return true
	default:
		return false
	}
}

func flushTurnBlocks(ctx context.Context, out chan<- AgentEvent, state *turnState) {
	for _, entry := range state.reasoning {
		if entry.Text == "" && entry.ProviderMetadata == nil {
			continue
		}
		emitAgentEvent(ctx, out, AgentEvent{Type: "block_complete", Block: &ContentBlock{Type: "reasoning", Text: entry.Text, OutputIndex: entry.OutputIndex, ProviderMetadata: entry.ProviderMetadata}})
	}
	for _, block := range state.redacted {
		emitAgentEvent(ctx, out, AgentEvent{Type: "block_complete", Block: &ContentBlock{Type: "redacted_thinking", Data: block.Data, OutputIndex: block.OutputIndex}})
	}
	if state.assistantText != "" {
		emitAgentEvent(ctx, out, AgentEvent{Type: "block_complete", Block: &ContentBlock{Type: "text", Text: state.assistantText, OutputIndex: state.textOutputIndex}})
	}
}

func toolUseBlock(call ToolCall) *ContentBlock {
	return &ContentBlock{Type: "tool_use", ToolUseID: call.ID, ToolName: call.Function.Name, Input: toolCallInput(call), OutputIndex: call.OutputIndex, StreamStartedAt: call.StreamStartedAt}
}

func reasoningKey(parsed ParsedStreamEvent) string {
	if parsed.ReasoningOutputIndex != nil {
		return fmt.Sprintf("%d", *parsed.ReasoningOutputIndex)
	}
	return "__default"
}

func emitProviderLifecycleEvents(
	ctx context.Context,
	out chan<- AgentEvent,
	parsed ParsedStreamEvent,
	provider string,
	model string,
	credentialID string,
) {
	if parsed.WebSearch != nil {
		ws := parsed.WebSearch
		if ws.Final {
			emitAgentEvent(ctx, out, AgentEvent{Type: "block_complete", Block: &ContentBlock{Type: "web_search", ID: ws.ID, Status: ws.Status, Query: ws.Query, Queries: ws.Queries, OutputIndex: ws.OutputIndex}})
		}
		emitAgentEvent(ctx, out, AgentEvent{Type: "web_search", ToolUseID: ws.ID, Message: ws.Status, OutputIndex: ws.OutputIndex, Metadata: map[string]any{"query": ws.Query, "queries": ws.Queries, "action": ws.Action}})
	}
	if parsed.ImageGeneration != nil {
		ig := parsed.ImageGeneration
		if ig.Final {
			emitAgentEvent(ctx, out, AgentEvent{Type: "block_complete", Block: &ContentBlock{Type: "image_generation", ID: ig.ID, Status: ig.Status, RevisedPrompt: ig.RevisedPrompt, Result: ig.Result, OutputIndex: ig.OutputIndex}})
		}
		emitAgentEvent(ctx, out, AgentEvent{Type: "image_generation", ToolUseID: ig.ID, Message: ig.Status, OutputIndex: ig.OutputIndex, Metadata: map[string]any{"revisedPrompt": ig.RevisedPrompt, "result": ig.Result}})
	}
	if parsed.OutputTruncated != "" {
		emitAgentEvent(ctx, out, AgentEvent{Type: "output_truncated", Message: parsed.OutputTruncated})
	}
	if parsed.ModelSwitched != nil {
		emitAgentEvent(ctx, out, AgentEvent{Type: "model_switched", Provider: parsed.ModelSwitched.Provider, Model: parsed.ModelSwitched.Model, Message: parsed.ModelSwitched.Cause, Metadata: map[string]any{"reasoningEffort": parsed.ModelSwitched.ReasoningEffort}, ModelSwitched: parsed.ModelSwitched})
	}
	if parsed.NkpQueue != nil {
		emitAgentEvent(ctx, out, AgentEvent{Type: "nkp_queue_status", Message: parsed.NkpQueue.QueueMessage, Metadata: map[string]any{"position": parsed.NkpQueue.Position, "queueDepth": parsed.NkpQueue.QueueDepth}})
	}
	if parsed.NkpQuotaBalance != nil {
		emitAgentEvent(ctx, out, AgentEvent{Type: "nkp_quota_balance", Metadata: map[string]any{"quotaBalance": *parsed.NkpQuotaBalance}})
	}
	if parsed.QueueStatus != nil {
		emitAgentEvent(ctx, out, AgentEvent{Type: "queue_status", Message: parsed.QueueStatus.QueueMessage, Metadata: map[string]any{"position": parsed.QueueStatus.Position, "queueDepth": parsed.QueueStatus.QueueDepth}})
	}
	if parsed.Metering != nil {
		emitAgentEvent(ctx, out, AgentEvent{Type: "metering", CredentialID: credentialID, Metadata: map[string]any{"unit": parsed.Metering.Unit, "unitPlural": parsed.Metering.UnitPlural, "usage": parsed.Metering.Usage}})
	}
	if parsed.QuotaBalance != nil {
		detailedQuotaBalance := ""
		if parsed.DetailedQuotaBalance != nil {
			detailedQuotaBalance = *parsed.DetailedQuotaBalance
		}
		emitAgentEvent(ctx, out, AgentEvent{Type: "quota_balance", Message: *parsed.QuotaBalance, Provider: provider, Model: model, Metadata: map[string]any{"quotaBalance": *parsed.QuotaBalance, "detailedQuotaBalance": detailedQuotaBalance}})
	}
	if parsed.APIRequest != nil {
		req := parsed.APIRequest
		eventType := "api_request_" + req.Phase
		emitAgentEvent(ctx, out, AgentEvent{Type: eventType, RequestID: req.RequestID, Provider: req.Provider, Model: req.Model, CredentialID: req.CredentialID, Usage: req.Usage, Message: req.ErrorMessage, DurationMs: req.DurationMs, Metadata: map[string]any{"ttftMs": req.TTFTMs}})
	}
}

func collectAgentResult(stream <-chan AgentEvent) (Result, error) {
	result := Result{ToolRuns: []ToolRun{}}
	for event := range stream {
		switch event.Type {
		case "assistant_message":
			result.Text = event.Text
		case "tool_result":
			if event.ToolRun != nil {
				result.ToolRuns = append(result.ToolRuns, *event.ToolRun)
			} else {
				result.ToolRuns = append(result.ToolRuns, ToolRun{ID: event.ToolUseID, Name: event.ToolName, Output: event.Output, Status: statusFromError(event.IsError)})
			}
		case "done":
			if strings.TrimSpace(result.Text) == "" {
				return Result{}, errors.New("provider returned empty content")
			}
			return result, nil
		case "error", "retryable_error":
			return Result{}, errors.New(event.Message)
		case "invalid_state":
			return Result{}, errors.New(firstNonEmpty(event.Message, event.Reason))
		}
	}
	if strings.TrimSpace(result.Text) != "" {
		return result, nil
	}
	return Result{}, errors.New("agent loop ended without final answer")
}

func statusFromError(isError bool) string {
	if isError {
		return "error"
	}
	return "completed"
}

func emitRetrying(ctx context.Context, out chan<- AgentEvent, message string, attempt int, maxRetries int, delay time.Duration) bool {
	return emitAgentEvent(ctx, out, AgentEvent{Type: "retrying", Message: message, Attempt: attempt, MaxRetries: maxRetries, DelayMs: delay.Milliseconds()})
}

func emitAgentEvent(ctx context.Context, out chan<- AgentEvent, event AgentEvent) bool {
	select {
	case <-ctx.Done():
		return false
	case out <- event:
		return true
	}
}

func sleepWithContext(ctx context.Context, delay time.Duration) bool {
	if delay <= 0 {
		select {
		case <-ctx.Done():
			return false
		default:
			return true
		}
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func retryDelay(base time.Duration, attempt int) time.Duration {
	if base <= 0 || attempt <= 0 {
		return 0
	}
	if attempt > 10 {
		attempt = 10
	}
	return base * time.Duration(1<<(attempt-1))
}

func isTransientAgentError(err error, customRules []customRetryRule) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	message := retryRuleMatchText(err)
	if matchesNonRetryableAgentPattern(message) {
		return false
	}
	if matchesCustomRetryRules(err, customRules) {
		return true
	}
	transientMarkers := []string{"429", "500", "502", "503", "504", "rate limit", "timeout", "temporary", "temporarily", "connection reset", "connection refused", "eof", "server overloaded", "try again"}
	for _, marker := range transientMarkers {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func matchesNonRetryableAgentPattern(message string) bool {
	nonRetryableMarkers := []string{
		"usage_limit_reached",
		"usage limit has been reached",
		"insufficient_quota",
		"quota exceeded",
		"exceeded your current quota",
		"check your plan and billing",
		"billing details",
		"payment required",
		"insufficient balance",
		"out of balance",
		"credit balance",
		`"plan_type":"free"`,
	}
	for _, marker := range nonRetryableMarkers {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func matchesCustomRetryRules(err error, rules []customRetryRule) bool {
	rules = normalizeCustomRetryRules(rules)
	if len(rules) == 0 {
		return false
	}
	allText := retryRuleMatchText(err)
	statusCodes := retryRuleStatusCodes(err)
	for _, rule := range rules {
		matched := true
		hasCondition := false
		if rule.Domain != "" {
			hasCondition = true
			if !strings.Contains(allText, rule.Domain) {
				matched = false
			}
		}
		if matched && rule.StatusCode > 0 {
			hasCondition = true
			if !statusCodes[rule.StatusCode] {
				matched = false
			}
		}
		if matched && rule.Keyword != "" {
			hasCondition = true
			if !strings.Contains(allText, rule.Keyword) {
				matched = false
			}
		}
		if hasCondition && matched {
			return true
		}
	}
	return false
}

func retryRuleMatchText(err error) string {
	if err == nil {
		return ""
	}
	parts := []string{err.Error()}
	var httpErr *providerHTTPError
	if errors.As(err, &httpErr) {
		parts = append(parts, httpErr.Provider, httpErr.Body)
	}
	var invalidErr *providerInvalidStateError
	if errors.As(err, &invalidErr) {
		parts = append(parts, invalidErr.Reason, invalidErr.Message)
	}
	return strings.ToLower(strings.Join(parts, " "))
}

func retryRuleStatusCodes(err error) map[int]bool {
	codes := map[int]bool{}
	var httpErr *providerHTTPError
	if errors.As(err, &httpErr) && httpErr.StatusCode >= 100 && httpErr.StatusCode <= 599 {
		codes[httpErr.StatusCode] = true
	}
	for _, code := range parseHTTPStatusCodes(retryRuleMatchText(err)) {
		codes[code] = true
	}
	return codes
}

func parseHTTPStatusCodes(text string) []int {
	codes := []int{}
	for i := 0; i+2 < len(text); i++ {
		if text[i] < '0' || text[i] > '9' || text[i+1] < '0' || text[i+1] > '9' || text[i+2] < '0' || text[i+2] > '9' {
			continue
		}
		beforeIsDigit := i > 0 && text[i-1] >= '0' && text[i-1] <= '9'
		afterIsDigit := i+3 < len(text) && text[i+3] >= '0' && text[i+3] <= '9'
		if beforeIsDigit || afterIsDigit {
			continue
		}
		code := int(text[i]-'0')*100 + int(text[i+1]-'0')*10 + int(text[i+2]-'0')
		if code >= 100 && code <= 599 {
			codes = append(codes, code)
		}
	}
	return codes
}

func customRetryRulesFromSettings(settings *config.Store) []customRetryRule {
	if settings == nil {
		return nil
	}
	raw := settings.Raw()
	agent, _ := raw["agent"].(map[string]any)
	items, _ := agent["customRetryRules"].([]any)
	if len(items) == 0 {
		return nil
	}
	rules := make([]customRetryRule, 0, len(items))
	for _, item := range items {
		ruleMap, ok := item.(map[string]any)
		if !ok {
			continue
		}
		rule := customRetryRule{
			Domain:     stringValue(ruleMap["domain"]),
			StatusCode: intValue(ruleMap["statusCode"], 0),
			Keyword:    stringValue(ruleMap["keyword"]),
		}
		if enabled, ok := ruleMap["enabled"].(bool); ok && !enabled {
			rule.Disabled = true
		}
		rules = append(rules, rule)
	}
	return normalizeCustomRetryRules(rules)
}

func normalizeCustomRetryRules(rules []customRetryRule) []customRetryRule {
	if len(rules) == 0 {
		return nil
	}
	normalized := make([]customRetryRule, 0, len(rules))
	for _, rule := range rules {
		if rule.Disabled {
			continue
		}
		rule.Domain = strings.ToLower(strings.TrimSpace(rule.Domain))
		rule.Keyword = strings.ToLower(strings.TrimSpace(rule.Keyword))
		if rule.StatusCode < 100 || rule.StatusCode > 599 {
			rule.StatusCode = 0
		}
		if rule.Domain == "" && rule.Keyword == "" && rule.StatusCode == 0 {
			continue
		}
		normalized = append(normalized, rule)
	}
	return normalized
}

type providerInvalidStateError struct {
	Reason  string
	Message string
}

func (e *providerInvalidStateError) Error() string {
	if strings.TrimSpace(e.Reason) != "" && strings.TrimSpace(e.Message) != "" {
		return e.Reason + ": " + e.Message
	}
	return firstNonEmpty(e.Message, e.Reason, "provider invalid state")
}

func defaultProviderHTTPClient() *http.Client {
	return &http.Client{Timeout: 180 * time.Second}
}

func providerHTTPClient(provider Provider) *http.Client {
	if strings.TrimSpace(provider.ProxyURL) == "" && !providerDisablesTLSVerification(provider) {
		return defaultProviderHTTPClient()
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = providerProxyFunc(provider)
	if providerDisablesTLSVerification(provider) {
		tlsConfig := &tls.Config{InsecureSkipVerify: true}
		if transport.TLSClientConfig != nil {
			tlsConfig = transport.TLSClientConfig.Clone()
			tlsConfig.InsecureSkipVerify = true
		}
		transport.TLSClientConfig = tlsConfig
	}
	return &http.Client{Timeout: 180 * time.Second, Transport: transport}
}

func providerProxyFunc(provider Provider) func(*http.Request) (*url.URL, error) {
	proxyURL := strings.TrimSpace(provider.ProxyURL)
	if proxyURL == "" {
		return http.ProxyFromEnvironment
	}
	parsed, err := url.Parse(proxyURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return http.ProxyFromEnvironment
	}
	return http.ProxyURL(parsed)
}

func providerDisablesTLSVerification(provider Provider) bool {
	return provider.TLSRejectUnauthorized != nil && !*provider.TLSRejectUnauthorized
}

func errProviderEmptyResponse() error {
	return errors.New("provider returned empty response")
}
