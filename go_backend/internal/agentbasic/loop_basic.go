package agentbasic

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
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
	// progressWG tracks tool-progress ticker goroutines (which emit to `out`
	// asynchronously). The loop goroutine waits for them before closing `out` so a
	// ticker can never send on a closed channel.
	progressWG := &sync.WaitGroup{}
	ctx = contextWithProgressWaitGroup(ctx, progressWG)
	go func() {
		defer close(out)
		runAgentLoopSync(ctx, out, settings, resolution, history, cwd, options)
		progressWG.Wait()
	}()
	return out
}

type progressWaitGroupKey struct{}

func contextWithProgressWaitGroup(ctx context.Context, wg *sync.WaitGroup) context.Context {
	return context.WithValue(ctx, progressWaitGroupKey{}, wg)
}

func progressWaitGroupFromContext(ctx context.Context) *sync.WaitGroup {
	wg, _ := ctx.Value(progressWaitGroupKey{}).(*sync.WaitGroup)
	return wg
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
	// Resolve the model's effective context window so usage snapshots carry a
	// ContextWindow and the context-usage percentage can be computed for providers
	// that do not supply it directly (Anthropic/OpenAI). Kiro supplies the
	// percentage itself, so this is a no-op there.
	options.contextWindow = resolveModelContextWindow(settings, resolution.Model, resolution.Provider)
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
		toolResults, ok := executeTurnTools(ctx, out, adapter, cwd, toolExecutor, state, options, turn, options.maxTurns)
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
		// Re-resolve the context window for the new model so context-usage
		// percentage stays accurate after a runtime model switch.
		options.contextWindow = resolveModelContextWindow(settings, resolution.Model, resolution.Provider)
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
	assistantText         string
	textOutputIndex       *int
	toolUses              []ToolCall
	seenToolUses          map[string]bool
	emittedToolCalls      map[string]bool
	reasoning             map[string]*reasoningAccum
	redacted              []*RedactedThinking
	credentialID          string
	earlyExec             map[string]<-chan toolExecOutcome
	completedOutcomes     map[string]toolExecOutcome
	yieldedToolResults    map[string]bool
	eagerBlocked          bool
	completedToolCount    int
	silentToolCallCount   int
	outputTruncated       bool
	sawMeaningfulResponse bool
}

func newTurnState(initialCompletedToolCount, silentToolCallCount int) *turnState {
	return &turnState{seenToolUses: map[string]bool{}, emittedToolCalls: map[string]bool{}, reasoning: map[string]*reasoningAccum{}, earlyExec: map[string]<-chan toolExecOutcome{}, completedOutcomes: map[string]toolExecOutcome{}, yieldedToolResults: map[string]bool{}, completedToolCount: initialCompletedToolCount, silentToolCallCount: silentToolCallCount}
}

// hasStartedEarlyToolExecution reports whether any eager (early) tool execution
// has begun OR already produced a yielded result this attempt. It mirrors the TS
// hasStartedEarlyToolExecution (loop.ts:1933 = earlyExecMap.size > 0 ||
// yieldedToolResults.size > 0). The yieldedToolResults check is essential: once an
// early tool result is drained it is removed from earlyExec but recorded in
// yieldedToolResults, so a retry after that point would re-run the tool and
// duplicate its side effects — which this guard prevents.
func hasStartedEarlyToolExecution(state *turnState) bool {
	return state != nil && (len(state.earlyExec) > 0 || len(state.yieldedToolResults) > 0)
}

// paymentResumeAction decides the payment_required resumeAction the frontend uses
// after the user recharges: "continue" (resume — partial work already happened)
// vs "retry" (start over). It mirrors TS loop.ts:3046-3049:
// hasStartedEarlyToolExecution() || initialToolResults.length > 0 ? "continue" :
// "retry". built.TrailingToolResults is the Go equivalent of TS initialToolResults
// (a turn that resumes from prior tool results).
func paymentResumeAction(state *turnState, built *BuiltHistory) string {
	if hasStartedEarlyToolExecution(state) || (built != nil && len(built.TrailingToolResults) > 0) {
		return "continue"
	}
	return "retry"
}

func runAgentTurnWithRetry(ctx context.Context, out chan<- AgentEvent, adapter ProviderAdapter, resolution ProviderResolution, built *BuiltHistory, cwd string, options generateOptions, toolExecutor ToolExecutor) (*turnState, bool) {
	emptyRetries := 0
	chatRetries := 0
	firstTokenRetries := 0
	// lastRetryErrorMessage persists a transient chat error across retries so that
	// if a subsequent attempt returns an EMPTY response (no error, no content), it
	// is treated as a continuation of the transient failure — retried via the chat
	// counter and surfaced with the original error message instead of the
	// misleading "empty response" text. Cleared when an attempt produces meaningful
	// content. Mirrors TS loop.ts lastRetryErrorMessage.
	lastRetryErrorMessage := ""
	for {
		state, err := runAgentTurnAttempt(ctx, out, adapter, resolution, built, cwd, options, toolExecutor)
		if err == nil {
			if !state.sawMeaningfulResponse && state.assistantText == "" && len(state.toolUses) == 0 {
				// Empty response after a prior transient error: continue the chat
				// retry loop with the original error message (TS loop.ts:3217-3272).
				if lastRetryErrorMessage != "" {
					maxRetries := effectiveMaxChatRetries(options, resolution)
					if maxRetries == -1 || chatRetries < maxRetries {
						chatRetries++
						delay := retryDelay(options.retryBaseDelay, chatRetries, options.retryBackoffCeil)
						if !emitRetrying(ctx, out, lastRetryErrorMessage, chatRetries, maxRetries, delay) {
							return nil, false
						}
						if !sleepWithContext(ctx, delay) {
							emitAgentEvent(context.Background(), out, AgentEvent{Type: "error", Message: "Aborted"})
							return nil, false
						}
						continue
					}
					emitAgentEvent(ctx, out, AgentEvent{Type: "retryable_error", Message: lastRetryErrorMessage})
					return nil, false
				}
				if emptyRetries < options.emptyResponseRetries {
					emptyRetries++
					message := resolution.Provider + ": " + emptyResponseMessage
					if !emitRetrying(ctx, out, message, emptyRetries, options.emptyResponseRetries, retryDelay(options.retryBaseDelay, emptyRetries, options.retryBackoffCeil)) {
						return nil, false
					}
					if !sleepWithContext(ctx, retryDelay(options.retryBaseDelay, emptyRetries, options.retryBackoffCeil)) {
						emitAgentEvent(context.Background(), out, AgentEvent{Type: "error", Message: "Aborted"})
						return nil, false
					}
					continue
				}
				emitAgentEvent(ctx, out, AgentEvent{Type: "invalid_state", Reason: "empty_response", Message: resolution.Provider + ": " + emptyResponseMessage})
				return nil, false
			}
			// Meaningful content: clear any retained transient error (TS clears
			// lastRetryErrorMessage when a meaningful stream event arrives).
			lastRetryErrorMessage = ""
			return state, true
		}
		if errors.Is(err, errTerminalEvent) {
			return nil, false
		}
		// First-token timeout retries up to maxFirstTokenRetries (= the configured
		// maxChatRetries / maxTransientRetries), UN-GATED by stateful providers
		// because no token was produced yet so no server-side state was meaningfully
		// consumed. Mirrors TS loop.ts:2962-3008.
		var firstTokenErr *firstTokenTimeoutError
		if errors.As(err, &firstTokenErr) {
			if state != nil {
				flushTurnBlocks(ctx, out, state)
			}
			maxFirstTokenRetries := options.maxChatRetries
			if maxFirstTokenRetries == -1 || firstTokenRetries < maxFirstTokenRetries {
				firstTokenRetries++
				delay := retryDelay(options.retryBaseDelay, firstTokenRetries, options.retryBackoffCeil)
				if !emitRetrying(ctx, out, firstTokenErr.Message, firstTokenRetries, maxFirstTokenRetries, delay) {
					return nil, false
				}
				if !sleepWithContext(ctx, delay) {
					emitAgentEvent(context.Background(), out, AgentEvent{Type: "error", Message: "Aborted"})
					return nil, false
				}
				continue
			}
			emitAgentEvent(ctx, out, AgentEvent{Type: "retryable_error", Message: firstTokenErr.Message})
			return nil, false
		}
		// Mimo ellipsis-only reasoning retries via the chat-retry counter, gated by
		// stateful providers like normal chat retries (mirrors TS which uses
		// chatRetryCount + getMaxChatRetries() for the mimo retry, loop.ts:3170-3189).
		var mimoErr *mimoEllipsisRetryError
		if errors.As(err, &mimoErr) {
			if state != nil {
				flushTurnBlocks(ctx, out, state)
			}
			maxRetries := effectiveMaxChatRetries(options, resolution)
			if maxRetries == -1 || chatRetries < maxRetries {
				chatRetries++
				delay := retryDelay(options.retryBaseDelay, chatRetries, options.retryBackoffCeil)
				if !emitRetrying(ctx, out, mimoErr.Error(), chatRetries, maxRetries, delay) {
					return nil, false
				}
				if !sleepWithContext(ctx, delay) {
					emitAgentEvent(context.Background(), out, AgentEvent{Type: "error", Message: "Aborted"})
					return nil, false
				}
				continue
			}
			emitAgentEvent(ctx, out, AgentEvent{Type: "retryable_error", Message: mimoErr.Error()})
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
		if errors.As(err, &retryInvalidStateErr) && hasStartedEarlyToolExecution(state) {
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
			// Mirror TS loop.ts: for NUG providers, surface a structured
			// payment_required event (instead of a generic error) so the
			// frontend can show the recharge dialog.
			if _, isNug := adapter.(*NugProviderAdapter); isNug {
				if info := getPaymentRequiredErrorInfo(err); info != nil {
					emitAgentEvent(ctx, out, AgentEvent{
						Type:           "payment_required",
						Message:        info.Message,
						ProviderID:     resolution.Config.ID,
						ProviderPrefix: firstNonEmpty(resolution.Config.Prefix, resolution.Provider),
						Balance:        info.Balance,
						Required:       info.Required,
						ResumeAction:   paymentResumeAction(state, built),
					})
					return nil, false
				}
			}
			emitAgentEvent(ctx, out, AgentEvent{Type: "error", Message: err.Error()})
			return nil, false
		}
		maxRetries := effectiveMaxChatRetries(options, resolution)
		if maxRetries != -1 && chatRetries >= maxRetries {
			if state != nil {
				flushTurnBlocks(ctx, out, state)
			}
			emitAgentEvent(ctx, out, AgentEvent{Type: "retryable_error", Message: err.Error()})
			return nil, false
		}
		chatRetries++
		lastRetryErrorMessage = err.Error()
		delay := retryDelay(options.retryBaseDelay, chatRetries, options.retryBackoffCeil)
		if !emitRetrying(ctx, out, err.Error(), chatRetries, maxRetries, delay) {
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
			// Mirror the TS first-token timeout message
			// (loop.ts:2100): "<provider>: First token timeout after <N>s". The
			// timeout is returned as a retryable firstTokenTimeoutError instead of a
			// terminal event so runAgentTurnWithRetry can retry it (un-gated by
			// stateful providers, since no token was produced yet).
			seconds := (options.firstTokenTimeout.Milliseconds() + 500) / 1000
			message := fmt.Sprintf("%s: First token timeout after %ds", resolution.Provider, seconds)
			finishAPIRequest(message)
			return state, &firstTokenTimeoutError{Message: message}
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
				// Inject the resolved context window so downstream context-usage
				// percentage is accurate for providers that report token counts but
				// not the window (Anthropic/OpenAI). Do not override a provider-set
				// window.
				if parsed.Usage.ContextWindow <= 0 && options.contextWindow > 0 {
					parsed.Usage.ContextWindow = options.contextWindow
				}
				lastUsage = parsed.Usage
			}
			if isMeaningfulStreamEvent(parsed) {
				state.sawMeaningfulResponse = true
				if firstTokenC != nil {
					if firstTokenTimer != nil {
						firstTokenTimer.Stop()
					}
					firstTokenC = nil
				}
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
				// Mimo ellipsis-only reasoning: some mimo models emit "..." as the
				// entire reasoning content (a degenerate response). When the reasoning
				// block finalizes (reasoningMetadata = signature delta) with text that
				// trims to exactly "...", discard the block and retry, mirroring TS
				// loop.ts:2649-2666.
				if parsed.ReasoningMetadata != nil && strings.Contains(strings.ToLower(resolution.Model), "mimo") && strings.TrimSpace(entry.Text) == "..." {
					delete(state.reasoning, key)
					finishAPIRequest("mimo ellipsis reasoning")
					return state, &mimoEllipsisRetryError{}
				}
			}
			if parsed.RedactedThinking != nil {
				state.redacted = append(state.redacted, parsed.RedactedThinking)
			}
			if parsed.ContextUsagePercentage != nil {
				requestContextPercent = *parsed.ContextUsagePercentage
				emitAgentEvent(ctx, out, AgentEvent{Type: "context_usage", Percentage: *parsed.ContextUsagePercentage, Usage: parsed.Usage})
			} else if parsed.Usage != nil {
				pct := usageContextPercent(parsed.Usage)
				if pct > 0 {
					requestContextPercent = pct
				}
				emitAgentEvent(ctx, out, AgentEvent{Type: "context_usage", Percentage: pct, Usage: parsed.Usage})
			}
			if parsed.Metering != nil {
				requestMeterUsage = parsed.Metering.Usage
				requestMeterUnit = parsed.Metering.Unit
			}
			emitProviderLifecycleEvents(ctx, out, parsed, resolution.Provider, resolution.Model, requestCredentialID)
			if parsed.InvalidState != nil {
				// Completion-token limit (max_tokens / max_output_tokens / length,
				// all normalized to "max_tokens" by providers) is not a failure: the
				// model simply hit its output cap. Mirror TS loop.ts isCompletionLimitReason
				// handling by emitting output_truncated and continuing to drain the
				// stream so the partial assistant content is preserved and the turn
				// ends gracefully instead of as a provider error.
				if isCompletionLimitReason(parsed.InvalidState.Reason) {
					state.outputTruncated = true
					emitAgentEvent(ctx, out, AgentEvent{Type: "output_truncated", Message: firstNonEmpty(parsed.InvalidState.Message, parsed.InvalidState.Reason)})
					continue
				}
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

// firstTokenTimeoutError signals that the provider produced no meaningful
// response before the first-token timeout fired. It is returned (instead of a
// terminal event) so the retry layer can retry the request, mirroring the TS
// agent loop which retries first-token timeouts up to maxFirstTokenRetries before
// surfacing a retryable_error.
type firstTokenTimeoutError struct {
	Message string
}

func (e *firstTokenTimeoutError) Error() string { return e.Message }

// mimoEllipsisRetryError signals that a mimo model returned "..." as its entire
// reasoning content (a degenerate response). The loop discards the reasoning
// block and retries via the chat-retry counter, mirroring the TS mimo ellipsis
// retry (loop.ts:2649-2666, 3170-3189).
type mimoEllipsisRetryError struct{}

func (e *mimoEllipsisRetryError) Error() string {
	return "Mimo model returned ellipsis-only reasoning, retrying"
}

func parsedHasMeaningfulResponse(parsed ParsedStreamEvent) bool {
	return parsed.Text != "" || parsed.Reasoning != "" || len(parsed.ToolUses) > 0 || parsed.ToolUseChunk != nil || parsed.WebSearch != nil || parsed.ImageGeneration != nil || parsed.InvalidState != nil || parsed.OutputTruncated != "" || parsed.APIRequest != nil || parsed.Metering != nil || parsed.QuotaBalance != nil || parsed.NkpQuotaBalance != nil || parsed.QueueStatus != nil || parsed.NkpQueue != nil
}

// emptyResponseMessage mirrors the TS EMPTY_RESPONSE_MESSAGE (loop.ts:297). It is
// surfaced (prefixed with the provider name) in both the retrying event and the
// final empty_response invalid_state, replacing Go's previous terse text.
const emptyResponseMessage = "Provider returned an empty response. This often indicates an API configuration error (base URL, model, or credentials)."

// isMeaningfulStreamEvent mirrors the TS isMeaningfulStreamEvent (loop.ts:353):
// a stream event counts as a meaningful response when it carries actual model
// content — text, tool uses (committed or streaming chunk), reasoning, a web
// search, an image generation, or a provider queue snapshot. It intentionally
// EXCLUDES invalid_state / api_request / metering / quota / output_truncated /
// silent_disconnect, which are control/metadata signals rather than content.
// This drives the empty-response detection and the first-token-timer clear so a
// response that only produced (for example) a web search or image is not
// mis-classified as empty, matching the TS loop.
func isMeaningfulStreamEvent(parsed ParsedStreamEvent) bool {
	return parsed.Text != "" ||
		len(parsed.ToolUses) > 0 ||
		parsed.ToolUseChunk != nil ||
		parsed.Reasoning != "" ||
		parsed.WebSearch != nil ||
		parsed.ImageGeneration != nil ||
		parsed.NkpQueue != nil ||
		parsed.QueueStatus != nil
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

func executeTurnTools(ctx context.Context, out chan<- AgentEvent, adapter ProviderAdapter, cwd string, toolExecutor ToolExecutor, state *turnState, options generateOptions, turn, maxTurns int) ([]any, bool) {
	toolResults := []any{}
	// Append a wrap-up nudge to the LAST tool result of this turn once >=80% of
	// maxTurns is used, mirroring the TS agent loop (loop.ts:3498-3506). processed
	// tracks the overall tool index across groups so isLast is true only for the
	// final tool of the turn.
	shouldNudge := maxTurns > 0 && turn >= maxTurns*4/5
	processed := 0
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
			if shouldNudge && processed == len(state.toolUses)-1 {
				outputForModel += turnNudgeReminder(turn+1, maxTurns)
			}
			processed++
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

// turnNudgeReminder mirrors the TS tool.turnNudge i18n message (English): it is
// appended to the last tool result of a turn once >=80% of maxTurns is used so
// the model wraps up before hitting the hard max-rounds error. English-only to
// match Go's other agent reminders (locale-aware tool messages are a separate
// gap).
func turnNudgeReminder(turnIndex, maxTurns int) string {
	return fmt.Sprintf("\n\n[SYSTEM: You have used %d of %d turns. Please wrap up your work soon — summarize remaining steps if you cannot finish in time.]", turnIndex, maxTurns)
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
	// Drain only the in-order settled PREFIX of tool results. When we reach a tool
	// that has not yet started (no early channel) or has not finished yet, we stop
	// rather than skipping ahead to a later tool that happens to be ready. This
	// guarantees tool_result emission AND completedToolCount assignment (performed
	// by ensureOutcomeSideCars -> collectToolResultSideCars) always proceed in
	// call order, matching the TS loop where the final in-order `await earlyPromise`
	// pass counts tools deterministically. Skipping ahead here would let a later
	// tool (e.g. tool-4) be counted before an earlier-but-slower one (tool-3),
	// attaching cadence-based sidecars (todo reminders) to the wrong tool result.
	for _, call := range state.toolUses {
		if state.yieldedToolResults[call.ID] {
			continue
		}
		outcome, ok := state.completedOutcomes[call.ID]
		if !ok {
			early := state.earlyExec[call.ID]
			if early == nil {
				// Tool not eager-started yet; stop to preserve call order.
				break
			}
			ready := false
			select {
			case <-ctx.Done():
				return false
			case outcome = <-early:
				state.completedOutcomes[call.ID] = outcome
				delete(state.earlyExec, call.ID)
				ready = true
			default:
			}
			if !ready {
				// Earlier tool not finished yet; stop to preserve call order.
				break
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
		progressWG := progressWaitGroupFromContext(ctx)
		if progressWG != nil {
			progressWG.Add(1)
		}
		go func() {
			if progressWG != nil {
				defer progressWG.Done()
			}
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
					// Send the progress event but abandon it the moment the tool
					// finishes (done) or the context cancels, so the ticker never
					// blocks on a full `out`. Combined with the loop goroutine waiting
					// on progressWG before close(out), this prevents any
					// send-on-closed-channel panic / data race.
					select {
					case <-done:
						return
					case <-ctx.Done():
						return
					case out <- AgentEvent{Type: "tool_progress", ToolUseID: call.ID, Elapsed: elapsed}:
					}
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
		imageMeta := map[string]any{"revisedPrompt": ig.RevisedPrompt, "result": ig.Result}
		if ig.PartialImageIndex != nil {
			imageMeta["partialImageIndex"] = *ig.PartialImageIndex
		}
		if ig.PartialImageB64 != "" {
			imageMeta["partialImageB64"] = ig.PartialImageB64
		}
		emitAgentEvent(ctx, out, AgentEvent{Type: "image_generation", ToolUseID: ig.ID, Message: ig.Status, OutputIndex: ig.OutputIndex, Metadata: imageMeta})
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

func retryDelay(base time.Duration, attempt int, ceil time.Duration) time.Duration {
	if base <= 0 || attempt <= 0 {
		return 0
	}
	if attempt > 10 {
		attempt = 10
	}
	delay := base * time.Duration(1<<(attempt-1))
	// Mirror the TS Math.min(base * 2^(attempt-1), backoffCeil) ceiling so a high
	// retry count cannot back off far longer than the configured cap.
	if ceil > 0 && delay > ceil {
		delay = ceil
	}
	return delay
}

// usesStatefulModel mirrors the TS usesStatefulModel (server/lib/settings/provider.ts):
// codex and the OpenAI responses/codex API modes maintain server-side
// conversation state, so transient retries (which would replay an already-
// committed stateful turn) must be disabled for them.
func usesStatefulModel(resolution ProviderResolution) bool {
	if strings.EqualFold(strings.TrimSpace(resolution.Config.Kind), "codex") {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(resolution.Config.APIMode)) {
	case "codex", "responses":
		return true
	}
	return false
}

// effectiveMaxChatRetries mirrors the TS getMaxChatRetries(): stateful models get
// 0 retries; otherwise the configured maxChatRetries is used (where -1 means
// infinite, matching agent.maxTransientRetries = -1).
func effectiveMaxChatRetries(options generateOptions, resolution ProviderResolution) int {
	if usesStatefulModel(resolution) {
		return 0
	}
	return options.maxChatRetries
}

// retryableErrorPatterns mirrors TS RETRYABLE_PATTERNS (error-handling.ts),
// lower-cased message substrings that indicate a transient, retryable failure.
var retryableErrorPatterns = []string{
	"model_temporarily_unavailable",
	"overload",
	"overloaded",
	"too many requests",
	"rate limit",
	"throttl",
	"service unavailable",
	"temporarily unavailable",
	"capacity",
	"try again",
	"socket connection was closed unexpectedly",
	"connection was closed unexpectedly",
	"socket hang up",
	"connection reset",
	"econnreset",
	"etimedout",
	"eai_again",
	"fetch failed",
	"failed to fetch",
	"network error",
	"stream_read_error",
	"stream read error",
	"unable to connect",
	"the operation timed out",
	"server_error",
	"internal_server_error",
	"the server had an error",
	"internal server error",
}

// retryable429Patterns mirrors TS RETRYABLE_429_PATTERNS: a 429 only retries
// when accompanied by one of these load/rate-limit keywords, so billing/quota
// 429s are not retried in a loop.
var retryable429Patterns = []string{
	"retry",
	"retry-after",
	"overload",
	"overloaded",
	"capacity",
	"capacty",
	"too many requests",
	"rate limit",
	"throttl",
	"try again",
}

// retryableErrorCodes mirrors TS RETRYABLE_ERROR_CODES (lower-cased here because
// Go flattens errors into a single lower-cased match string). These are matched
// as whole-word-ish substrings of the flattened error text.
var retryableErrorCodes = []string{
	"econnreset",
	"epipe",
	"etimedout",
	"eai_again",
	"enetdown",
	"enetunreach",
	"econnrefused",
	"und_err_socket",
	"und_err_connect_timeout",
	"und_err_headers_timeout",
	"stream_read_error",
	"connectionrefused",
	"connectionreset",
	"connectionaborted",
}

// retryableStatusCodes mirrors TS RETRYABLE_STATUS_CODES.
var retryableStatusCodes = []int{500, 502, 503, 529}

// retryableInvalidStateReasons mirrors TS RETRYABLE_INVALID_STATE_REASONS
// (error-handling.ts). Providers may surface a transient failure as an
// invalid_state whose reason is one of these underscore-cased codes (e.g.
// Anthropic passes through err.Type, Kiro passes through data["reason"]). TS
// retries these via a dedicated reason-set branch in isRetryableInvalidStateReason
// that is independent of the broad message-pattern list. Go previously only
// matched the flattened error text against retryableErrorPatterns (space-cased),
// so reasons like service_unavailable/temporarily_unavailable (underscore) and
// internal_error/stream_closed_before_response_completed (absent) were NOT
// retried even though TS retries them.
var retryableInvalidStateReasons = map[string]bool{
	"server_error":                            true,
	"internal_error":                          true,
	"internal_server_error":                   true,
	"service_unavailable":                     true,
	"temporarily_unavailable":                 true,
	"stream_closed_before_response_completed": true,
	"stream_read_error":                       true,
}

// invalidStateReason returns the lower-cased, trimmed reason of a
// providerInvalidStateError wrapped in err, or "" when err is not an
// invalid-state error.
func invalidStateReason(err error) string {
	var invalidErr *providerInvalidStateError
	if errors.As(err, &invalidErr) {
		return strings.ToLower(strings.TrimSpace(invalidErr.Reason))
	}
	return ""
}

func containsAnyMarker(message string, markers []string) bool {
	for _, marker := range markers {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

// isTransientAgentError decides whether a provider chat error should be retried.
// It mirrors the TS isRetryableError decision order (server/lib/agent/error-handling.ts):
// hard quota/plan failures never retry; recognized network/error codes, retryable
// HTTP status codes (500/502/503/529) and 429s WITH a load/rate-limit keyword
// retry; a bare 429 falls through to user custom retry rules instead of
// unconditionally retrying; finally broad transient message patterns and custom
// rules are consulted.
func isTransientAgentError(err error, customRules []customRetryRule) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	message := retryRuleMatchText(err)
	// Hard quota / plan / billing failures must never retry.
	if matchesNonRetryableAgentPattern(message) {
		return false
	}
	// Invalid-state errors with a recognized transient reason retry via a dedicated
	// reason-set branch (mirrors TS isRetryableInvalidStateReason's
	// RETRYABLE_INVALID_STATE_REASONS check), independent of the message patterns.
	// This runs after the non-retryable short-circuit so a quota/billing message
	// still wins, matching TS order.
	if reason := invalidStateReason(err); reason != "" && retryableInvalidStateReasons[reason] {
		return true
	}
	statusCodes := retryRuleStatusCodes(err)

	// Recognized transient network / server error codes.
	if containsAnyMarker(message, retryableErrorCodes) {
		return true
	}

	// Retryable HTTP status codes (500/502/503/529).
	for _, code := range retryableStatusCodes {
		if statusCodes[code] {
			return true
		}
	}

	// 429 only retries when accompanied by a load/rate-limit keyword; a bare 429
	// (e.g. a billing/quota 429) falls through to user custom retry rules.
	if statusCodes[429] {
		if containsAnyMarker(message, retryable429Patterns) {
			return true
		}
		return matchesCustomRetryRules(err, customRules)
	}

	// Broad transient message patterns.
	if containsAnyMarker(message, retryableErrorPatterns) {
		return true
	}

	// User-defined custom retry rules.
	return matchesCustomRetryRules(err, customRules)
}

// isCompletionLimitReason reports whether an invalid-state reason indicates the
// model hit its output/completion token cap (not a context-window overflow and
// not a hard error). Mirrors TS isCompletionLimitReason in error-handling.ts.
// Providers normalize max_output_tokens/length to "max_tokens" upstream, but we
// accept all three forms for robustness.
func isCompletionLimitReason(reason string) bool {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "max_tokens", "max_output_tokens", "length":
		return true
	default:
		return false
	}
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
