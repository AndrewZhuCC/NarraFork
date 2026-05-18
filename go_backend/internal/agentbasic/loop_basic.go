package agentbasic

import (
	"context"
	"errors"
	"fmt"
	"net/http"
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
	ID          string
	Name        string
	Input       string
	OutputIndex *int
}

type toolExecOutcome struct {
	Run        ToolRun
	IsError    bool
	DurationMs int64
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
	return runAgentLoop(ctx, resolution, history, cwd, options), nil
}

func runAgentLoop(ctx context.Context, resolution ProviderResolution, history []Message, cwd string, options generateOptions) <-chan AgentEvent {
	out := make(chan AgentEvent, 32)
	go func() {
		defer close(out)
		runAgentLoopSync(ctx, out, resolution, history, cwd, options)
	}()
	return out
}

func runAgentLoopSync(ctx context.Context, out chan<- AgentEvent, resolution ProviderResolution, history []Message, cwd string, options generateOptions) {
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
	inventoryOptions := ToolInventoryOptions{Scope: options.toolScope, EnabledTools: options.enabledTools, DisabledTools: options.disabledTools}
	if options.emitToolInventory {
		emitAgentEvent(ctx, out, AgentEvent{Type: "tool_inventory", Metadata: map[string]any{"inventory": ToolInventoryMetadataForOptions(inventoryOptions)}})
	}
	toolExecutor := options.toolExecutor
	if toolExecutor == nil {
		toolExecutor = defaultToolExecutor
	}
	toolExecutor = guardToolExecutor(toolExecutor, inventoryOptions)
	for turn := 0; turn < options.maxTurns; turn++ {
		state, ok := runAgentTurnWithRetry(ctx, out, adapter, resolution, &built, cwd, options, toolExecutor)
		if !ok {
			return
		}
		flushTurnBlocks(ctx, out, state)
		emitAgentEvent(ctx, out, AgentEvent{Type: "assistant_message", Text: state.assistantText, ToolUses: append([]ToolCall(nil), state.toolUses...), CredentialID: state.credentialID})
		if len(state.toolUses) == 0 {
			emitAgentEvent(ctx, out, AgentEvent{Type: "done"})
			return
		}
		adapter.PushAssistantTurn(&built, state.assistantText, state.toolUses, nil)
		toolResults, ok := executeTurnTools(ctx, out, adapter, cwd, toolExecutor, state)
		if !ok {
			return
		}
		adapter.PushUserTurn(&built, "", toolResults)
		emitAgentEvent(ctx, out, AgentEvent{Type: "turn_complete", TurnIndex: turn})
	}
	emitAgentEvent(ctx, out, AgentEvent{Type: "error", Message: "tool loop reached maximum rounds without final answer"})
}

type turnState struct {
	assistantText    string
	textOutputIndex  *int
	toolUses         []ToolCall
	seenToolUses     map[string]bool
	emittedToolCalls map[string]bool
	reasoning        map[string]*reasoningAccum
	redacted         []*RedactedThinking
	credentialID     string
	earlyExec        map[string]<-chan toolExecOutcome
	eagerBlocked     bool
}

func newTurnState() *turnState {
	return &turnState{seenToolUses: map[string]bool{}, emittedToolCalls: map[string]bool{}, reasoning: map[string]*reasoningAccum{}, earlyExec: map[string]<-chan toolExecOutcome{}}
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
		if !isTransientAgentError(err) {
			if state != nil {
				flushTurnBlocks(ctx, out, state)
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
	stream, err := adapter.Chat(ctx, ChatParams{
		Model:           resolution.Model,
		Cwd:             cwd,
		History:         built,
		Tools:           adapter.FormatTools(effectiveToolScope),
		ReasoningEffort: options.reasoningEffort,
		SystemPrompt:    options.systemPrompt,
		ToolScope:       effectiveToolScope,
	})
	if err != nil {
		return nil, err
	}
	state := newTurnState()
	chunks := map[string]*toolChunkAccum{}
	for event := range stream {
		if event.Err != nil {
			return state, event.Err
		}
		parsed := event.Parsed
		if parsed.CredentialID != "" {
			state.credentialID = parsed.CredentialID
		}
		if parsed.TextOutputIndex != nil {
			state.textOutputIndex = parsed.TextOutputIndex
		}
		if parsed.Text != "" {
			state.assistantText += parsed.Text
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
			emitAgentEvent(ctx, out, AgentEvent{Type: "context_usage", Percentage: *parsed.ContextUsagePercentage, Usage: parsed.Usage})
		} else if parsed.Usage != nil {
			emitAgentEvent(ctx, out, AgentEvent{Type: "context_usage", Usage: parsed.Usage})
		}
		emitProviderLifecycleEvents(ctx, out, parsed)
		if parsed.InvalidState != nil {
			emitAgentEvent(ctx, out, AgentEvent{Type: "invalid_state", Reason: parsed.InvalidState.Reason, Message: parsed.InvalidState.Message})
			return nil, errTerminalEvent
		}
		if parsed.ToolUseChunk != nil {
			handleToolUseChunk(ctx, out, state, chunks, parsed.ToolUseChunk, cwd, toolExecutor)
		}
		for _, call := range parsed.ToolUses {
			addToolUse(ctx, out, state, call, cwd, toolExecutor)
		}
	}
	return state, nil
}

var errTerminalEvent = errors.New("terminal agent event emitted")

func handleToolUseChunk(ctx context.Context, out chan<- AgentEvent, state *turnState, chunks map[string]*toolChunkAccum, chunk *ToolUseChunk, cwd string, toolExecutor ToolExecutor) {
	idValue := strings.TrimSpace(chunk.ToolUseID)
	if idValue == "" {
		idValue = id.Must(12)
	}
	acc := chunks[idValue]
	if acc == nil {
		acc = &toolChunkAccum{ID: idValue}
		chunks[idValue] = acc
	}
	if strings.TrimSpace(chunk.Name) != "" {
		acc.Name = strings.TrimSpace(chunk.Name)
	}
	if chunk.OutputIndex != nil {
		acc.OutputIndex = chunk.OutputIndex
	}
	acc.Input += chunk.Input
	emitAgentEvent(ctx, out, AgentEvent{Type: "tool_use_chunk", ToolUseID: idValue, ToolName: acc.Name, OutputIndex: acc.OutputIndex, Streaming: chunk})
	if chunk.Stop {
		name := acc.Name
		if name == "" {
			name = chunk.Name
		}
		call := newFunctionToolCall(idValue, name, acc.Input)
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
		state.earlyExec[call.ID] = startToolExecution(ctx, cwd, toolExecutor, call)
	}
}

func emitToolCallIfMissing(ctx context.Context, out chan<- AgentEvent, call ToolCall, emitted map[string]bool) {
	if emitted[call.ID] {
		return
	}
	emitted[call.ID] = true
	emitAgentEvent(ctx, out, AgentEvent{Type: "tool_call", ToolUseID: call.ID, ToolName: call.Function.Name, Input: toolCallInput(call)})
}

func executeTurnTools(ctx context.Context, out chan<- AgentEvent, adapter ProviderAdapter, cwd string, toolExecutor ToolExecutor, state *turnState) ([]any, bool) {
	toolResults := []any{}
	for _, group := range toolExecutionGroups(state.toolUses) {
		outcomes := make([]toolExecOutcome, len(group))
		if len(group) == 1 {
			call := group[0]
			outcome, ok := awaitOrRunTool(ctx, cwd, toolExecutor, state, call)
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
				if early, ok := state.earlyExec[call.ID]; ok {
					go func(index int, early <-chan toolExecOutcome) {
						resultCh <- struct {
							Index   int
							Outcome toolExecOutcome
						}{Index: index, Outcome: <-early}
					}(idx, early)
					continue
				}
				go func(index int, toolCall ToolCall) {
					resultCh <- struct {
						Index   int
						Outcome toolExecOutcome
					}{Index: index, Outcome: executeToolCall(ctx, cwd, toolExecutor, toolCall)}
				}(idx, call)
			}
			for range group {
				select {
				case <-ctx.Done():
					emitAgentEvent(context.Background(), out, AgentEvent{Type: "error", Message: "Aborted"})
					return nil, false
				case result := <-resultCh:
					outcomes[result.Index] = result.Outcome
				}
			}
		}
		for idx, call := range group {
			emitToolCallIfMissing(ctx, out, call, state.emittedToolCalls)
			outcome := outcomes[idx]
			run := outcome.Run
			emitAgentEvent(ctx, out, AgentEvent{Type: "tool_result", ToolUseID: run.ID, ToolName: run.Name, Output: run.Output, IsError: outcome.IsError, DurationMs: outcome.DurationMs, ToolRun: &run})
			toolResults = append(toolResults, adapter.FormatToolResult(call.ID, run.Output, outcome.IsError))
		}
	}
	return toolResults, true
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

func awaitOrRunTool(ctx context.Context, cwd string, toolExecutor ToolExecutor, state *turnState, call ToolCall) (toolExecOutcome, bool) {
	if early, ok := state.earlyExec[call.ID]; ok {
		select {
		case <-ctx.Done():
			return toolExecOutcome{}, false
		case outcome := <-early:
			return outcome, true
		}
	}
	return executeToolCall(ctx, cwd, toolExecutor, call), true
}

func startToolExecution(ctx context.Context, cwd string, toolExecutor ToolExecutor, call ToolCall) <-chan toolExecOutcome {
	ch := make(chan toolExecOutcome, 1)
	go func() {
		ch <- executeToolCall(ctx, cwd, toolExecutor, call)
		close(ch)
	}()
	return ch
}

func executeToolCall(ctx context.Context, cwd string, toolExecutor ToolExecutor, call ToolCall) toolExecOutcome {
	started := time.Now()
	run, err := toolExecutor(ctx, cwd, call)
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
	return &ContentBlock{Type: "tool_use", ToolUseID: call.ID, ToolName: call.Function.Name, Input: toolCallInput(call)}
}

func reasoningKey(parsed ParsedStreamEvent) string {
	if parsed.ReasoningOutputIndex != nil {
		return fmt.Sprintf("%d", *parsed.ReasoningOutputIndex)
	}
	return "__default"
}

func emitProviderLifecycleEvents(ctx context.Context, out chan<- AgentEvent, parsed ParsedStreamEvent) {
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
		emitAgentEvent(ctx, out, AgentEvent{Type: "model_switched", Provider: parsed.ModelSwitched.Provider, Model: parsed.ModelSwitched.Model, Message: parsed.ModelSwitched.Cause, Metadata: map[string]any{"reasoningEffort": parsed.ModelSwitched.ReasoningEffort}})
	}
	if parsed.NkpQueue != nil {
		emitAgentEvent(ctx, out, AgentEvent{Type: "nkp_queue", Message: parsed.NkpQueue.QueueMessage, Metadata: map[string]any{"position": parsed.NkpQueue.Position, "queueDepth": parsed.NkpQueue.QueueDepth}})
	}
	if parsed.NkpQuotaBalance != nil {
		emitAgentEvent(ctx, out, AgentEvent{Type: "nkp_quota_balance", Metadata: map[string]any{"quotaBalance": *parsed.NkpQuotaBalance}})
	}
	if parsed.QueueStatus != nil {
		emitAgentEvent(ctx, out, AgentEvent{Type: "queue_status", Message: parsed.QueueStatus.QueueMessage, Metadata: map[string]any{"position": parsed.QueueStatus.Position, "queueDepth": parsed.QueueStatus.QueueDepth}})
	}
	if parsed.Metering != nil {
		emitAgentEvent(ctx, out, AgentEvent{Type: "metering", Metadata: map[string]any{"unit": parsed.Metering.Unit, "unitPlural": parsed.Metering.UnitPlural, "usage": parsed.Metering.Usage}})
	}
	if parsed.QuotaBalance != nil {
		emitAgentEvent(ctx, out, AgentEvent{Type: "quota_balance", Message: *parsed.QuotaBalance})
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

func isTransientAgentError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	message := strings.ToLower(err.Error())
	transientMarkers := []string{"429", "500", "502", "503", "504", "rate limit", "timeout", "temporary", "temporarily", "connection reset", "connection refused", "eof", "server overloaded", "try again"}
	for _, marker := range transientMarkers {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func defaultProviderHTTPClient() *http.Client {
	return &http.Client{Timeout: 180 * time.Second}
}

func errProviderEmptyResponse() error {
	return errors.New("provider returned empty response")
}
