package agentbasic

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

type scriptedAdapter struct {
	turns           [][]StreamEvent
	chatErrors      []error
	chatCalls       int
	pushedUsers     int
	pushedAssist    int
	lastToolResult  []any
	lastUserContent string
}

func (a *scriptedAdapter) ProviderName() string                { return "fake" }
func (a *scriptedAdapter) FormatTools(string) []map[string]any { return nil }
func (a *scriptedAdapter) BuildHistory(messages []Message, _ string, _ string) (BuiltHistory, error) {
	history := make([]any, 0, len(messages))
	for _, message := range messages {
		history = append(history, message)
	}
	return BuiltHistory{History: history}, nil
}
func (a *scriptedAdapter) InjectSystemPrompt(*BuiltHistory, string, string, string) {}
func (a *scriptedAdapter) Chat(ctx context.Context, params ChatParams) (<-chan StreamEvent, error) {
	call := a.chatCalls
	a.chatCalls++
	if call < len(a.chatErrors) && a.chatErrors[call] != nil {
		return nil, a.chatErrors[call]
	}
	ch := make(chan StreamEvent, 8)
	go func() {
		defer close(ch)
		if call >= len(a.turns) {
			return
		}
		for _, event := range a.turns[call] {
			select {
			case <-ctx.Done():
				return
			case ch <- event:
			}
		}
	}()
	return ch, nil
}
func (a *scriptedAdapter) FormatToolResult(toolUseID string, output string, isError bool) any {
	return map[string]any{"type": "tool_result", "tool_use_id": toolUseID, "content": output, "is_error": isError}
}
func (a *scriptedAdapter) PushUserTurn(history *BuiltHistory, content string, toolResults []any) {
	a.pushedUsers++
	a.lastUserContent = content
	a.lastToolResult = append([]any(nil), toolResults...)
	history.History = append(history.History, map[string]any{"role": "user", "content": toolResults, "text": content})
}
func (a *scriptedAdapter) PushAssistantTurn(history *BuiltHistory, text string, toolUses []ToolCall, rawContent any) {
	a.pushedAssist++
	history.History = append(history.History, map[string]any{"role": "assistant", "content": text, "toolUses": toolUses})
}

func collectEvents(stream <-chan AgentEvent) []AgentEvent {
	events := []AgentEvent{}
	for event := range stream {
		events = append(events, event)
	}
	return events
}

func eventTypes(events []AgentEvent) []string {
	types := make([]string, 0, len(events))
	for _, event := range events {
		types = append(types, event.Type)
	}
	return types
}

func eventTypeCount(events []AgentEvent, eventType string) int {
	count := 0
	for _, event := range events {
		if event.Type == eventType {
			count++
		}
	}
	return count
}

type hangingAdapter struct {
	chatCalls int
	done      chan struct{}
}

func (a *hangingAdapter) ProviderName() string                { return "fake" }
func (a *hangingAdapter) FormatTools(string) []map[string]any { return nil }
func (a *hangingAdapter) BuildHistory(messages []Message, _ string, _ string) (BuiltHistory, error) {
	history := make([]any, 0, len(messages))
	for _, message := range messages {
		history = append(history, message)
	}
	return BuiltHistory{History: history}, nil
}
func (a *hangingAdapter) InjectSystemPrompt(*BuiltHistory, string, string, string) {}
func (a *hangingAdapter) Chat(ctx context.Context, params ChatParams) (<-chan StreamEvent, error) {
	a.chatCalls++
	a.done = make(chan struct{})
	ch := make(chan StreamEvent)
	go func() {
		<-ctx.Done()
		close(ch)
		close(a.done)
	}()
	return ch, nil
}
func (a *hangingAdapter) FormatToolResult(toolUseID string, output string, isError bool) any {
	return map[string]any{"type": "tool_result", "tool_use_id": toolUseID, "content": output, "is_error": isError}
}
func (a *hangingAdapter) PushUserTurn(history *BuiltHistory, content string, toolResults []any) {}
func (a *hangingAdapter) PushAssistantTurn(history *BuiltHistory, text string, toolUses []ToolCall, rawContent any) {
}

func TestFirstTokenTimeoutEmitsTerminalEventWithoutRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	adapter := &hangingAdapter{}
	stream := runAgentLoop(ctx, ProviderResolution{Provider: "fake", Model: "model", Adapter: adapter}, []Message{{Role: "user", Content: "hi"}}, t.TempDir(), normalizeGenerateOptions(WithMaxTurns(1), WithFirstTokenTimeout(5*time.Millisecond), WithMaxChatRetries(3)))
	events := collectEvents(stream)
	if eventTypeCount(events, "first_token_timeout") != 1 {
		t.Fatalf("expected first_token_timeout event, got %v", eventTypes(events))
	}
	if eventTypeCount(events, "retrying") != 0 || adapter.chatCalls != 1 {
		t.Fatalf("first-token timeout should not enter transient retry loop, events=%v chatCalls=%d", eventTypes(events), adapter.chatCalls)
	}
	select {
	case <-adapter.done:
	case <-time.After(time.Second):
		t.Fatalf("first-token timeout should cancel the provider stream context")
	}
}

func TestSilentDisconnectEmitsTerminalEvent(t *testing.T) {
	adapter := &scriptedAdapter{turns: [][]StreamEvent{{{Parsed: ParsedStreamEvent{SilentDisconnect: true}}}}}
	stream := runAgentLoop(context.Background(), ProviderResolution{Provider: "fake", Model: "model", Adapter: adapter}, []Message{{Role: "user", Content: "hi"}}, t.TempDir(), normalizeGenerateOptions(WithMaxTurns(1), WithFirstTokenTimeout(time.Second)))
	events := collectEvents(stream)
	if eventTypeCount(events, "silent_disconnect") != 1 {
		t.Fatalf("expected silent_disconnect event, got %v", eventTypes(events))
	}
	if eventTypeCount(events, "error") != 0 || eventTypeCount(events, "retrying") != 0 {
		t.Fatalf("silent disconnect should be terminal without error/retry events: %v", eventTypes(events))
	}
}

func TestRunAgentLoopStreamsTextAndDone(t *testing.T) {
	adapter := &scriptedAdapter{turns: [][]StreamEvent{{{Parsed: ParsedStreamEvent{Text: "hello"}}}}}
	stream := runAgentLoop(context.Background(), ProviderResolution{Provider: "fake", Model: "fake-model", Adapter: adapter}, []Message{{Role: "user", Content: "hi"}}, t.TempDir(), normalizeGenerateOptions(WithRetryBaseDelay(0)))
	events := collectEvents(stream)
	want := []string{"api_request_start", "stream_text", "api_request_end", "block_complete", "assistant_message", "done"}
	if got := eventTypes(events); !reflect.DeepEqual(got, want) {
		t.Fatalf("event types = %#v, want %#v", got, want)
	}
	if events[3].Block == nil || events[3].Block.Type != "text" || events[3].Block.Text != "hello" {
		t.Fatalf("unexpected text block: %#v", events[3].Block)
	}
}

func TestRunAgentLoopExecutesToolAndEmitsTurnComplete(t *testing.T) {
	call := newFunctionToolCall("tool-1", "Bash", `{"command":"ignored"}`)
	adapter := &scriptedAdapter{turns: [][]StreamEvent{
		{{Parsed: ParsedStreamEvent{ToolUses: []ToolCall{call}}}},
		{{Parsed: ParsedStreamEvent{Text: "final"}}},
	}}
	stream := runAgentLoop(
		context.Background(),
		ProviderResolution{Provider: "fake", Model: "fake-model", Adapter: adapter},
		[]Message{{Role: "user", Content: "use tool"}},
		t.TempDir(),
		normalizeGenerateOptions(
			WithRetryBaseDelay(0),
			WithToolExecutor(func(ctx context.Context, cwd string, call ToolCall) (ToolRun, error) {
				return ToolRun{Output: "tool output"}, nil
			}),
		),
	)
	events := collectEvents(stream)
	if eventTypeCount(events, "tool_call") != 1 || eventTypeCount(events, "tool_result") != 1 || eventTypeCount(events, "turn_complete") != 1 {
		t.Fatalf("expected tool_call/tool_result/turn_complete events, got %#v", eventTypes(events))
	}
	if adapter.pushedAssist != 1 || adapter.pushedUsers != 1 || len(adapter.lastToolResult) != 1 {
		t.Fatalf("expected adapter history push, assist=%d users=%d results=%#v", adapter.pushedAssist, adapter.pushedUsers, adapter.lastToolResult)
	}
	if events[len(events)-1].Type != "done" {
		t.Fatalf("expected final done event, got %#v", events[len(events)-1])
	}
}

func TestRunAgentLoopCollectsToolResultSideCarsForModelAndEvents(t *testing.T) {
	calls := []ToolCall{
		newFunctionToolCall("tool-1", "Read", `{"file_path":"a"}`),
		newFunctionToolCall("tool-2", "Read", `{"file_path":"b"}`),
		newFunctionToolCall("tool-3", "Read", `{"file_path":"c"}`),
		newFunctionToolCall("tool-4", "Read", `{"file_path":"d"}`),
	}
	adapter := &scriptedAdapter{turns: [][]StreamEvent{
		{{Parsed: ParsedStreamEvent{ToolUses: calls}}},
		{{Parsed: ParsedStreamEvent{Text: "final"}}},
	}}
	seenCount := 0
	stream := runAgentLoop(
		context.Background(),
		ProviderResolution{Provider: "fake", Model: "fake-model", Adapter: adapter},
		[]Message{{Role: "user", Content: "use tools"}},
		t.TempDir(),
		normalizeGenerateOptions(
			WithRetryBaseDelay(0),
			WithToolExecutor(func(ctx context.Context, cwd string, call ToolCall) (ToolRun, error) {
				return ToolRun{ID: call.ID, Name: call.Function.Name, Output: "output " + call.ID, Status: "completed"}, nil
			}),
			WithSideCarCollector(0, func(ctx context.Context, request SideCarRequest) []AgentSideCar {
				if request.Phase != "tool_result" {
					return nil
				}
				seenCount = request.CompletedToolCount
				if request.CompletedToolCount != 4 {
					return nil
				}
				return []AgentSideCar{{Target: "tool_result", Source: "todo_reminder", Content: "todo detail", ToolUseID: request.ToolUseID}}
			}, nil),
		),
	)
	events := collectEvents(stream)
	if seenCount != 4 {
		t.Fatalf("expected sidecar collector at 4 completed tools, got %d", seenCount)
	}
	var sideCarEvent *AgentEvent
	for i := range events {
		if events[i].Type == "tool_result" && events[i].ToolUseID == "tool-4" {
			sideCarEvent = &events[i]
			break
		}
	}
	if sideCarEvent == nil || len(sideCarEvent.SideCars) != 1 || sideCarEvent.SideCars[0].Source != "todo_reminder" {
		t.Fatalf("expected tool_result sidecar event for fourth tool, events=%#v", events)
	}
	if len(adapter.lastToolResult) != 4 {
		t.Fatalf("expected four pushed tool results, got %#v", adapter.lastToolResult)
	}
	fourth, _ := adapter.lastToolResult[3].(map[string]any)
	content, _ := fourth["content"].(string)
	if !strings.Contains(content, "output tool-4\n\n<side_car") || !strings.Contains(content, `source="todo_reminder"`) || !strings.Contains(content, "todo detail") {
		t.Fatalf("expected fourth model tool result to include sidecar text, got %#v", fourth)
	}
}

func TestRunAgentLoopInjectsSilentProgressSideCar(t *testing.T) {
	calls := []ToolCall{
		newFunctionToolCall("silent-1", "Read", `{"file_path":"a"}`),
		newFunctionToolCall("silent-2", "Read", `{"file_path":"b"}`),
	}
	adapter := &scriptedAdapter{turns: [][]StreamEvent{
		{{Parsed: ParsedStreamEvent{ToolUses: calls}}},
		{{Parsed: ParsedStreamEvent{Text: "visible"}}},
	}}
	stream := runAgentLoop(
		context.Background(),
		ProviderResolution{Provider: "fake", Model: "fake-model", Adapter: adapter},
		[]Message{{Role: "user", Content: "use tools"}},
		t.TempDir(),
		normalizeGenerateOptions(
			WithRetryBaseDelay(0),
			WithSilentToolCallThreshold(2),
			WithToolExecutor(func(ctx context.Context, cwd string, call ToolCall) (ToolRun, error) {
				return ToolRun{ID: call.ID, Name: call.Function.Name, Output: "output " + call.ID, Status: "completed"}, nil
			}),
		),
	)
	events := collectEvents(stream)
	var sideCarEvent *AgentEvent
	for i := range events {
		if events[i].Type == "tool_result" && events[i].ToolUseID == "silent-2" {
			sideCarEvent = &events[i]
			break
		}
	}
	if sideCarEvent == nil || len(sideCarEvent.SideCars) != 1 || sideCarEvent.SideCars[0].Source != "silent_progress" {
		t.Fatalf("expected silent_progress sidecar on second tool result, events=%#v", events)
	}
	second, _ := adapter.lastToolResult[1].(map[string]any)
	content, _ := second["content"].(string)
	if !strings.Contains(content, `<side_car source="silent_progress">`) || !strings.Contains(content, "You have completed 2 tool call") {
		t.Fatalf("expected model tool result to include silent progress sidecar, got %#v", second)
	}
}

func TestRunAgentLoopCollectsAfterToolsUserSideCars(t *testing.T) {
	call := newFunctionToolCall("tool-after", "Read", `{"file_path":"a"}`)
	adapter := &scriptedAdapter{turns: [][]StreamEvent{
		{{Parsed: ParsedStreamEvent{ToolUses: []ToolCall{call}}}},
		{{Parsed: ParsedStreamEvent{Text: "final"}}},
	}}
	stream := runAgentLoop(
		context.Background(),
		ProviderResolution{Provider: "fake", Model: "fake-model", Adapter: adapter},
		[]Message{{Role: "user", Content: "use tool"}},
		t.TempDir(),
		normalizeGenerateOptions(
			WithRetryBaseDelay(0),
			WithToolExecutor(func(ctx context.Context, cwd string, call ToolCall) (ToolRun, error) {
				return ToolRun{ID: call.ID, Name: call.Function.Name, Output: "ok", Status: "completed"}, nil
			}),
			WithSideCarCollector(0, func(ctx context.Context, request SideCarRequest) []AgentSideCar {
				if request.Phase != "after_tools" {
					return nil
				}
				return []AgentSideCar{{Target: "user_message", Source: "team_message", Content: "team ping"}}
			}, nil),
		),
	)
	events := collectEvents(stream)
	if eventTypeCount(events, "sidecars") != 1 {
		t.Fatalf("expected sidecars event, got %v", eventTypes(events))
	}
	if !strings.Contains(adapter.lastUserContent, `<side_car source="team_message">`) || !strings.Contains(adapter.lastUserContent, "team ping") {
		t.Fatalf("expected after-tools sidecar text in next user turn, got %q", adapter.lastUserContent)
	}
}

func TestRunAgentLoopAccumulatesToolUseChunks(t *testing.T) {
	adapter := &scriptedAdapter{turns: [][]StreamEvent{
		{
			{Parsed: ParsedStreamEvent{ToolUseChunk: &ToolUseChunk{ToolUseID: "chunk-1", Name: "Bash", Input: `{"command":`}}},
			{Parsed: ParsedStreamEvent{ToolUseChunk: &ToolUseChunk{ToolUseID: "chunk-1", Input: `"ignored"}`, Stop: true}}},
		},
		{{Parsed: ParsedStreamEvent{Text: "done"}}},
	}}
	stream := runAgentLoop(
		context.Background(),
		ProviderResolution{Provider: "fake", Model: "fake-model", Adapter: adapter},
		[]Message{{Role: "user", Content: "chunk tool"}},
		t.TempDir(),
		normalizeGenerateOptions(
			WithRetryBaseDelay(0),
			WithToolExecutor(func(ctx context.Context, cwd string, call ToolCall) (ToolRun, error) {
				if call.Function.Arguments != `{"command":"ignored"}` {
					t.Fatalf("unexpected accumulated args: %s", call.Function.Arguments)
				}
				return ToolRun{Output: "ok"}, nil
			}),
		),
	)
	events := collectEvents(stream)
	if eventTypeCount(events, "tool_use_chunk") != 2 || eventTypeCount(events, "tool_call") != 1 {
		t.Fatalf("expected chunk and tool call events, got %#v", eventTypes(events))
	}
}

func TestRunAgentLoopEmitsReasoningBlocks(t *testing.T) {
	adapter := &scriptedAdapter{turns: [][]StreamEvent{{
		{Parsed: ParsedStreamEvent{Reasoning: "think"}},
		{Parsed: ParsedStreamEvent{Text: "answer"}},
	}}}
	stream := runAgentLoop(context.Background(), ProviderResolution{Provider: "fake", Model: "fake-model", Adapter: adapter}, []Message{{Role: "user", Content: "why"}}, t.TempDir(), normalizeGenerateOptions(WithRetryBaseDelay(0)))
	events := collectEvents(stream)
	if eventTypeCount(events, "stream_reasoning") != 1 {
		t.Fatalf("expected stream_reasoning event, got %#v", eventTypes(events))
	}
	seenReasoningBlock := false
	for _, event := range events {
		if event.Type == "block_complete" && event.Block != nil && event.Block.Type == "reasoning" && event.Block.Text == "think" {
			seenReasoningBlock = true
		}
	}
	if !seenReasoningBlock {
		t.Fatalf("expected reasoning block, got %#v", events)
	}
}

func TestRunAgentLoopFlushesPartialTextBeforeError(t *testing.T) {
	adapter := &scriptedAdapter{turns: [][]StreamEvent{{
		{Parsed: ParsedStreamEvent{Text: "partial"}},
		{Err: errors.New("non transient failure")},
	}}}
	stream := runAgentLoop(context.Background(), ProviderResolution{Provider: "fake", Model: "fake-model", Adapter: adapter}, []Message{{Role: "user", Content: "fail"}}, t.TempDir(), normalizeGenerateOptions(WithRetryBaseDelay(0)))
	events := collectEvents(stream)
	if events[len(events)-1].Type != "error" {
		t.Fatalf("expected final error, got %#v", eventTypes(events))
	}
	seenTextBlock := false
	for _, event := range events {
		if event.Type == "block_complete" && event.Block != nil && event.Block.Type == "text" && event.Block.Text == "partial" {
			seenTextBlock = true
		}
	}
	if !seenTextBlock {
		t.Fatalf("expected partial text block before error, got %#v", events)
	}
}

func TestRunAgentLoopMapsProviderLifecycleEvents(t *testing.T) {
	percentage := 42.0
	adapter := &scriptedAdapter{turns: [][]StreamEvent{{
		{Parsed: ParsedStreamEvent{ContextUsagePercentage: &percentage, Usage: &UsageSnapshot{PromptTokens: 10}}},
		{Parsed: ParsedStreamEvent{WebSearch: &WebSearchEvent{ID: "ws-1", Status: "completed", Query: "query", Final: true}}},
		{Parsed: ParsedStreamEvent{ImageGeneration: &ImageGenerationEvent{ID: "img-1", Status: "completed", RevisedPrompt: "prompt", Result: "base64", Final: true}}},
		{Parsed: ParsedStreamEvent{OutputTruncated: "truncated"}},
		{Parsed: ParsedStreamEvent{ModelSwitched: &ModelSwitchedEvent{Provider: "fake2", Model: "fake-model-2", Cause: "retry"}}},
		{Parsed: ParsedStreamEvent{APIRequest: &APIRequestEvent{Phase: "start", RequestID: "req-1", Provider: "fake", Model: "fake-model"}}},
		{Parsed: ParsedStreamEvent{APIRequest: &APIRequestEvent{Phase: "end", RequestID: "req-1", Provider: "fake", Model: "fake-model", Usage: &UsageSnapshot{CompletionTokens: 1}}}},
		{Parsed: ParsedStreamEvent{Text: "done"}},
	}}}
	stream := runAgentLoop(context.Background(), ProviderResolution{Provider: "fake", Model: "fake-model", Adapter: adapter}, []Message{{Role: "user", Content: "lifecycle"}}, t.TempDir(), normalizeGenerateOptions(WithRetryBaseDelay(0)))
	events := collectEvents(stream)
	for _, want := range []string{"context_usage", "web_search", "image_generation", "output_truncated", "model_switched", "api_request_start", "api_request_end"} {
		if eventTypeCount(events, want) == 0 {
			t.Fatalf("expected %s event, got %#v", want, eventTypes(events))
		}
	}
}

func TestRunAgentLoopStartsEagerParallelToolsBeforeAwaitingResults(t *testing.T) {
	callA := newFunctionToolCall("read-1", "Read", `{"file_path":"a.txt"}`)
	callB := newFunctionToolCall("read-2", "Read", `{"file_path":"b.txt"}`)
	adapter := &scriptedAdapter{turns: [][]StreamEvent{
		{{Parsed: ParsedStreamEvent{ToolUses: []ToolCall{callA, callB}}}},
		{{Parsed: ParsedStreamEvent{Text: "done"}}},
	}}
	started := make(chan string, 2)
	release := make(chan struct{})
	stream := runAgentLoop(
		context.Background(),
		ProviderResolution{Provider: "fake", Model: "fake-model", Adapter: adapter},
		[]Message{{Role: "user", Content: "parallel"}},
		t.TempDir(),
		normalizeGenerateOptions(
			WithRetryBaseDelay(0),
			WithToolExecutor(func(ctx context.Context, cwd string, call ToolCall) (ToolRun, error) {
				started <- call.ID
				<-release
				return ToolRun{Output: call.ID + " output"}, nil
			}),
		),
	)
	first := <-started
	second := ""
	select {
	case second = <-started:
	case <-time.After(500 * time.Millisecond):
		close(release)
		t.Fatalf("expected second eager parallel tool to start before first completed; first=%s", first)
	}
	close(release)
	events := collectEvents(stream)
	if first == second || eventTypeCount(events, "tool_result") != 2 || events[len(events)-1].Type != "done" {
		t.Fatalf("unexpected parallel execution result first=%s second=%s events=%#v", first, second, eventTypes(events))
	}
}

func TestRunAgentLoopRetriesTransientErrors(t *testing.T) {
	adapter := &scriptedAdapter{
		chatErrors: []error{errors.New("provider returned status 503")},
		turns:      [][]StreamEvent{nil, {{Parsed: ParsedStreamEvent{Text: "recovered"}}}},
	}
	stream := runAgentLoop(context.Background(), ProviderResolution{Provider: "fake", Model: "fake-model", Adapter: adapter}, []Message{{Role: "user", Content: "retry"}}, t.TempDir(), normalizeGenerateOptions(WithRetryBaseDelay(0), WithMaxChatRetries(2)))
	events := collectEvents(stream)
	if eventTypeCount(events, "retrying") != 1 || events[len(events)-1].Type != "done" {
		t.Fatalf("expected retrying then done, got %#v", eventTypes(events))
	}
	if adapter.chatCalls != 2 {
		t.Fatalf("expected two chat calls, got %d", adapter.chatCalls)
	}
}

func TestRunAgentLoopEmitsRetryableErrorAfterTransientRetries(t *testing.T) {
	adapter := &scriptedAdapter{
		chatErrors: []error{errors.New("provider returned status 503"), errors.New("provider returned status 503")},
	}
	stream := runAgentLoop(context.Background(), ProviderResolution{Provider: "fake", Model: "fake-model", Adapter: adapter}, []Message{{Role: "user", Content: "retry"}}, t.TempDir(), normalizeGenerateOptions(WithRetryBaseDelay(0), WithMaxChatRetries(1)))
	events := collectEvents(stream)
	if eventTypeCount(events, "retrying") != 1 || events[len(events)-1].Type != "retryable_error" {
		t.Fatalf("expected retrying then retryable_error, got %#v", eventTypes(events))
	}
}

func TestRunAgentLoopEmptyResponseRetriesThenInvalidState(t *testing.T) {
	adapter := &scriptedAdapter{turns: [][]StreamEvent{{}, {}}}
	stream := runAgentLoop(context.Background(), ProviderResolution{Provider: "fake", Model: "fake-model", Adapter: adapter}, []Message{{Role: "user", Content: "empty"}}, t.TempDir(), normalizeGenerateOptions(WithRetryBaseDelay(0), WithEmptyResponseRetries(1)))
	events := collectEvents(stream)
	if eventTypeCount(events, "retrying") != 1 || events[len(events)-1].Type != "invalid_state" {
		t.Fatalf("expected retrying then invalid_state, got %#v", eventTypes(events))
	}
}
