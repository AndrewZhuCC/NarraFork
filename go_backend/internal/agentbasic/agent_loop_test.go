package agentbasic

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type scriptedAdapter struct {
	turns           [][]StreamEvent
	chatErrors      []error
	chatCalls       int
	chatParams      []ChatParams
	buildInputs     [][]Message
	buildModels     []string
	injectPrompts   []string
	injectModels    []string
	pushedUsers     int
	pushedAssist    int
	lastToolResult  []any
	lastUserContent string
}

func (a *scriptedAdapter) ProviderName() string                { return "fake" }
func (a *scriptedAdapter) FormatTools(string) []map[string]any { return nil }
func (a *scriptedAdapter) BuildHistory(messages []Message, model string, _ string) (BuiltHistory, error) {
	a.buildInputs = append(a.buildInputs, append([]Message(nil), messages...))
	a.buildModels = append(a.buildModels, model)
	history := make([]any, 0, len(messages))
	for _, message := range messages {
		history = append(history, message)
	}
	return BuiltHistory{History: history}, nil
}
func (a *scriptedAdapter) InjectSystemPrompt(_ *BuiltHistory, systemPrompt string, model string, _ string) {
	a.injectPrompts = append(a.injectPrompts, systemPrompt)
	a.injectModels = append(a.injectModels, model)
}
func (a *scriptedAdapter) Chat(ctx context.Context, params ChatParams) (<-chan StreamEvent, error) {
	call := a.chatCalls
	a.chatCalls++
	a.chatParams = append(a.chatParams, cloneChatParams(params))
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

func cloneChatParams(params ChatParams) ChatParams {
	cloned := params
	if params.History != nil {
		history := *params.History
		history.History = append([]any(nil), params.History.History...)
		history.TrailingToolResults = append([]any(nil), params.History.TrailingToolResults...)
		cloned.History = &history
	}
	cloned.Tools = append([]map[string]any(nil), params.Tools...)
	cloned.ToolResults = append([]any(nil), params.ToolResults...)
	return cloned
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

type waitAfterToolAdapter struct {
	done           <-chan struct{}
	lastToolResult []any
}

func (a *waitAfterToolAdapter) ProviderName() string                { return "fake" }
func (a *waitAfterToolAdapter) FormatTools(string) []map[string]any { return nil }
func (a *waitAfterToolAdapter) BuildHistory(messages []Message, _ string, _ string) (BuiltHistory, error) {
	history := make([]any, 0, len(messages))
	for _, message := range messages {
		history = append(history, message)
	}
	return BuiltHistory{History: history}, nil
}
func (a *waitAfterToolAdapter) InjectSystemPrompt(*BuiltHistory, string, string, string) {}
func (a *waitAfterToolAdapter) Chat(ctx context.Context, params ChatParams) (<-chan StreamEvent, error) {
	ch := make(chan StreamEvent)
	go func() {
		defer close(ch)
		select {
		case <-ctx.Done():
			return
		case ch <- StreamEvent{Parsed: ParsedStreamEvent{ToolUseChunk: &ToolUseChunk{ToolUseID: "read-early", Name: "Read", Input: `{"file_path":"a.txt"}`, Stop: true}}}:
		}
		select {
		case <-ctx.Done():
			return
		case <-a.done:
		}
		select {
		case <-ctx.Done():
		case ch <- StreamEvent{Parsed: ParsedStreamEvent{Text: "still streaming"}}:
		}
	}()
	return ch, nil
}
func (a *waitAfterToolAdapter) FormatToolResult(toolUseID string, output string, isError bool) any {
	return map[string]any{"type": "tool_result", "tool_use_id": toolUseID, "content": output, "is_error": isError}
}
func (a *waitAfterToolAdapter) PushUserTurn(history *BuiltHistory, content string, toolResults []any) {
	a.lastToolResult = append([]any(nil), toolResults...)
	history.History = append(history.History, map[string]any{"role": "user", "content": toolResults, "text": content})
}
func (a *waitAfterToolAdapter) PushAssistantTurn(history *BuiltHistory, text string, toolUses []ToolCall, rawContent any) {
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
	done := make(chan struct{})
	a.done = done
	ch := make(chan StreamEvent)
	go func() {
		<-ctx.Done()
		close(ch)
		close(done)
	}()
	return ch, nil
}
func (a *hangingAdapter) FormatToolResult(toolUseID string, output string, isError bool) any {
	return map[string]any{"type": "tool_result", "tool_use_id": toolUseID, "content": output, "is_error": isError}
}
func (a *hangingAdapter) PushUserTurn(history *BuiltHistory, content string, toolResults []any) {}
func (a *hangingAdapter) PushAssistantTurn(history *BuiltHistory, text string, toolUses []ToolCall, rawContent any) {
}

func TestFirstTokenTimeoutRetriesAndExhaustsToRetryableError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	adapter := &hangingAdapter{}
	// Mirror TS loop.ts: a first-token timeout is retried up to maxFirstTokenRetries
	// (here 2) with `retrying` events, then surfaces a retryable_error (NOT a
	// terminal first_token_timeout). 3 total chat attempts = 1 initial + 2 retries.
	stream := runAgentLoop(ctx, ProviderResolution{Provider: "fake", Model: "model", Adapter: adapter}, []Message{{Role: "user", Content: "hi"}}, t.TempDir(), normalizeGenerateOptions(WithMaxTurns(1), WithFirstTokenTimeout(5*time.Millisecond), WithMaxChatRetries(2), WithRetryBaseDelay(0)))
	events := collectEvents(stream)
	if eventTypeCount(events, "retrying") != 2 {
		t.Fatalf("expected 2 retrying events for first-token timeout, got %v", eventTypes(events))
	}
	if eventTypeCount(events, "retryable_error") != 1 {
		t.Fatalf("expected retryable_error after exhausting first-token retries, got %v", eventTypes(events))
	}
	if eventTypeCount(events, "first_token_timeout") != 0 {
		t.Fatalf("first-token timeout should no longer emit a terminal first_token_timeout event, got %v", eventTypes(events))
	}
	if adapter.chatCalls != 3 {
		t.Fatalf("expected 3 chat attempts (1 + 2 retries), got %d", adapter.chatCalls)
	}
}

func TestFirstTokenTimeoutZeroRetriesEmitsRetryableErrorImmediately(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	adapter := &hangingAdapter{}
	stream := runAgentLoop(ctx, ProviderResolution{Provider: "fake", Model: "model", Adapter: adapter}, []Message{{Role: "user", Content: "hi"}}, t.TempDir(), normalizeGenerateOptions(WithMaxTurns(1), WithFirstTokenTimeout(5*time.Millisecond), WithMaxChatRetries(0), WithRetryBaseDelay(0)))
	events := collectEvents(stream)
	if eventTypeCount(events, "retrying") != 0 {
		t.Fatalf("zero retries should not emit retrying, got %v", eventTypes(events))
	}
	if eventTypeCount(events, "retryable_error") != 1 || adapter.chatCalls != 1 {
		t.Fatalf("expected immediate retryable_error with one chat attempt, events=%v chatCalls=%d", eventTypes(events), adapter.chatCalls)
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

func TestBeforeTurnHookRunsForEachProviderTurn(t *testing.T) {
	call := newFunctionToolCall("hook-tool", "Read", `{"file_path":"a.txt"}`)
	adapter := &scriptedAdapter{turns: [][]StreamEvent{
		{{Parsed: ParsedStreamEvent{ToolUses: []ToolCall{call}}}},
		{{Parsed: ParsedStreamEvent{Text: "final"}}},
	}}
	turns := []int{}
	stream := runAgentLoop(
		context.Background(),
		ProviderResolution{Provider: "fake", Model: "fake-model", Adapter: adapter},
		[]Message{{Role: "user", Content: "use tool"}},
		t.TempDir(),
		normalizeGenerateOptions(
			WithRetryBaseDelay(0),
			WithBeforeTurnHook(func(turnIndex int) error {
				turns = append(turns, turnIndex)
				return nil
			}),
			WithToolExecutor(func(ctx context.Context, cwd string, call ToolCall) (ToolRun, error) {
				return ToolRun{Output: "tool output"}, nil
			}),
		),
	)
	_ = collectEvents(stream)
	if !reflect.DeepEqual(turns, []int{0, 1}) {
		t.Fatalf("before-turn hook indexes = %#v, want %#v", turns, []int{0, 1})
	}
	if adapter.chatCalls != 2 {
		t.Fatalf("expected two provider requests, got %d", adapter.chatCalls)
	}
}

func TestBeforeTurnHookErrorEmitsErrorWithoutProviderRequest(t *testing.T) {
	adapter := &scriptedAdapter{turns: [][]StreamEvent{{{Parsed: ParsedStreamEvent{Text: "unused"}}}}}
	stream := runAgentLoop(
		context.Background(),
		ProviderResolution{Provider: "fake", Model: "fake-model", Adapter: adapter},
		[]Message{{Role: "user", Content: "hi"}},
		t.TempDir(),
		normalizeGenerateOptions(
			WithRetryBaseDelay(0),
			WithBeforeTurnHook(func(turnIndex int) error {
				return errors.New("before-turn failed")
			}),
		),
	)
	events := collectEvents(stream)
	if adapter.chatCalls != 0 {
		t.Fatalf("provider should not be called after hook error, got %d calls", adapter.chatCalls)
	}
	if len(events) != 1 || events[0].Type != "error" || events[0].Message != "before-turn failed" {
		t.Fatalf("expected only hook error event, got %#v", events)
	}
}

func TestBeforeTurnReplacementHookRebuildsHistoryAndSystemPrompt(t *testing.T) {
	call := newFunctionToolCall("replace-tool", "Read", `{"file_path":"a.txt"}`)
	adapter := &scriptedAdapter{turns: [][]StreamEvent{
		{{Parsed: ParsedStreamEvent{ToolUses: []ToolCall{call}}}},
		{{Parsed: ParsedStreamEvent{Text: "final"}}},
	}}
	replacementPrompt := "replacement system"
	stream := runAgentLoop(
		context.Background(),
		ProviderResolution{Provider: "fake", Model: "fake-model", Adapter: adapter},
		[]Message{{Role: "user", Content: "original"}},
		t.TempDir(),
		normalizeGenerateOptions(
			WithRetryBaseDelay(0),
			WithBeforeTurnReplacementHook(func(turnIndex int) (*BeforeTurnReplacement, error) {
				if turnIndex != 1 {
					return nil, nil
				}
				return &BeforeTurnReplacement{
					Messages:     []Message{{Role: "user", Content: "replacement"}},
					SystemPrompt: &replacementPrompt,
				}, nil
			}),
			WithToolExecutor(func(ctx context.Context, cwd string, call ToolCall) (ToolRun, error) {
				return ToolRun{Output: "tool output"}, nil
			}),
		),
	)
	events := collectEvents(stream)
	if events[len(events)-1].Type != "done" || adapter.chatCalls != 2 {
		t.Fatalf("expected two turns and done, calls=%d events=%#v", adapter.chatCalls, eventTypes(events))
	}
	if len(adapter.buildInputs) < 2 || len(adapter.buildInputs[1]) != 1 || adapter.buildInputs[1][0].Content != "replacement" {
		t.Fatalf("replacement hook did not rebuild from replacement messages: %#v", adapter.buildInputs)
	}
	if len(adapter.injectPrompts) < 2 || adapter.injectPrompts[1] != replacementPrompt {
		t.Fatalf("replacement system prompt was not injected: %#v", adapter.injectPrompts)
	}
	secondHistory := adapter.chatParams[1].History
	if secondHistory == nil || len(secondHistory.History) != 1 {
		t.Fatalf("second request should use rebuilt history, got %#v", secondHistory)
	}
	message, ok := secondHistory.History[0].(Message)
	if !ok || message.Content != "replacement" || adapter.chatParams[1].SystemPrompt != replacementPrompt {
		t.Fatalf("second request did not use replacement history/system prompt: params=%#v", adapter.chatParams[1])
	}
}

func TestBeforeTurnReplacementHookErrorEmitsError(t *testing.T) {
	call := newFunctionToolCall("replace-error-tool", "Read", `{"file_path":"a.txt"}`)
	adapter := &scriptedAdapter{turns: [][]StreamEvent{
		{{Parsed: ParsedStreamEvent{ToolUses: []ToolCall{call}}}},
		{{Parsed: ParsedStreamEvent{Text: "unused"}}},
	}}
	stream := runAgentLoop(
		context.Background(),
		ProviderResolution{Provider: "fake", Model: "fake-model", Adapter: adapter},
		[]Message{{Role: "user", Content: "use tool"}},
		t.TempDir(),
		normalizeGenerateOptions(
			WithRetryBaseDelay(0),
			WithBeforeTurnReplacementHook(func(turnIndex int) (*BeforeTurnReplacement, error) {
				if turnIndex == 1 {
					return nil, errors.New("replacement failed")
				}
				return nil, nil
			}),
			WithToolExecutor(func(ctx context.Context, cwd string, call ToolCall) (ToolRun, error) {
				return ToolRun{Output: "tool output"}, nil
			}),
		),
	)
	events := collectEvents(stream)
	if adapter.chatCalls != 1 {
		t.Fatalf("provider should not be called after replacement error, got %d calls", adapter.chatCalls)
	}
	if events[len(events)-1].Type != "error" || events[len(events)-1].Message != "replacement failed" {
		t.Fatalf("expected replacement error event, got %#v", events)
	}
}

func TestRuntimeSettingsOverrideSwitchesModelAndReasoningBeforeNextTurn(t *testing.T) {
	requestBodies := []map[string]any{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		requestBodies = append(requestBodies, body)
		w.Header().Set("Content-Type", "application/json")
		if len(requestBodies) == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": "runtime-tool", "type": "function", "function": map[string]any{"name": "Read", "arguments": `{"file_path":"a.txt"}`}}}}}},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "final"}}},
		})
	}))
	defer server.Close()
	store := testConfigStore(t, map[string]any{
		"openaiProviders": []any{map[string]any{
			"prefix":       "openai",
			"apiKey":       "test-key",
			"baseUrl":      server.URL,
			"defaultModel": "gpt-5-mini",
		}},
	})
	reasoning := "xhigh"
	stream, err := RunAgentLoop(
		context.Background(),
		store,
		"openai:gpt-5-mini",
		[]Message{{Role: "user", Content: "use tool"}},
		t.TempDir(),
		WithMaxTurns(2),
		WithRetryBaseDelay(0),
		WithRuntimeSettingsOverrideHook(func(turnIndex int) (*RuntimeSettingsOverride, error) {
			if turnIndex != 1 {
				return nil, nil
			}
			return &RuntimeSettingsOverride{Model: "openai:gpt-5-test-2", ReasoningEffort: &reasoning}, nil
		}),
		WithToolExecutor(func(ctx context.Context, cwd string, call ToolCall) (ToolRun, error) {
			return ToolRun{Output: "tool output"}, nil
		}),
	)
	if err != nil {
		t.Fatalf("RunAgentLoop returned error: %v", err)
	}
	events := collectEvents(stream)
	if len(requestBodies) != 2 {
		t.Fatalf("expected two provider requests, got %d events=%#v", len(requestBodies), eventTypes(events))
	}
	if requestBodies[0]["model"] != "gpt-5-mini" || requestBodies[1]["model"] != "gpt-5-test-2" {
		t.Fatalf("unexpected request models: %#v", requestBodies)
	}
	if requestBodies[1]["reasoning_effort"] != "high" {
		t.Fatalf("expected second request reasoning effort to be high, got %#v", requestBodies[1]["reasoning_effort"])
	}
	var switched *AgentEvent
	for i := range events {
		if events[i].Type == "model_switched" {
			switched = &events[i]
			break
		}
	}
	if switched == nil || switched.ModelSwitched == nil || switched.ModelSwitched.Model != "gpt-5-test-2" || switched.ModelSwitched.Provider != "openai" || switched.ModelSwitched.ReasoningEffort != "xhigh" || switched.ModelSwitched.Cause != "turn" {
		t.Fatalf("expected model_switched event for runtime override, got %#v", events)
	}
	if events[len(events)-1].Type != "done" {
		t.Fatalf("expected done, got %#v", eventTypes(events))
	}
}

func TestShouldStopHookStopsAfterToolsWithoutDoneOrError(t *testing.T) {
	call := newFunctionToolCall("stop-tool", "Read", `{"file_path":"a.txt"}`)
	adapter := &scriptedAdapter{turns: [][]StreamEvent{
		{{Parsed: ParsedStreamEvent{ToolUses: []ToolCall{call}}}},
		{{Parsed: ParsedStreamEvent{Text: "should not request"}}},
	}}
	shouldStopCalls := 0
	stream := runAgentLoop(
		context.Background(),
		ProviderResolution{Provider: "fake", Model: "fake-model", Adapter: adapter},
		[]Message{{Role: "user", Content: "use tool"}},
		t.TempDir(),
		normalizeGenerateOptions(
			WithRetryBaseDelay(0),
			WithShouldStop(func() bool {
				shouldStopCalls++
				return true
			}),
			WithToolExecutor(func(ctx context.Context, cwd string, call ToolCall) (ToolRun, error) {
				return ToolRun{Output: "tool output"}, nil
			}),
		),
	)
	events := collectEvents(stream)
	if adapter.chatCalls != 1 {
		t.Fatalf("expected one provider request before stop, got %d", adapter.chatCalls)
	}
	if shouldStopCalls != 1 {
		t.Fatalf("expected one should-stop hook call, got %d", shouldStopCalls)
	}
	if eventTypeCount(events, "tool_result") != 1 || eventTypeCount(events, "turn_complete") != 1 {
		t.Fatalf("expected tool_result and turn_complete before stop, got %#v", eventTypes(events))
	}
	if eventTypeCount(events, "done") != 0 || eventTypeCount(events, "error") != 0 {
		t.Fatalf("should-stop should not emit done/error, got %#v", eventTypes(events))
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
	seenCount := int64(0)
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
				// The collector runs from parallel tool-execution goroutines, so use an
				// atomic store to avoid a benign test-level data race on seenCount.
				atomic.StoreInt64(&seenCount, int64(request.CompletedToolCount))
				if request.CompletedToolCount != 4 {
					return nil
				}
				return []AgentSideCar{{Target: "tool_result", Source: "todo_reminder", Content: "todo detail", ToolUseID: request.ToolUseID}}
			}, nil),
		),
	)
	events := collectEvents(stream)
	if atomic.LoadInt64(&seenCount) != 4 {
		t.Fatalf("expected sidecar collector at 4 completed tools, got %d", atomic.LoadInt64(&seenCount))
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

// TestDrainSettledToolResultsStopsAtUnsettledPrefix pins the drain-prefix
// contract: drainSettledToolResults emits tool_result events (and assigns
// completedToolCount via ensureOutcomeSideCars) strictly in call order. When an
// earlier tool has not settled yet, draining must STOP rather than skip ahead to
// a later tool that happens to be ready. Skipping ahead would let a later tool be
// counted first and mis-attribute cadence-based sidecars (e.g. todo reminders) to
// the wrong tool result. This guards the regression exposed by the progress
// ticker timing change.
func TestDrainSettledToolResultsStopsAtUnsettledPrefix(t *testing.T) {
	calls := []ToolCall{
		newFunctionToolCall("tool-1", "Read", `{"file_path":"a"}`),
		newFunctionToolCall("tool-2", "Read", `{"file_path":"b"}`),
		newFunctionToolCall("tool-3", "Read", `{"file_path":"c"}`),
		newFunctionToolCall("tool-4", "Read", `{"file_path":"d"}`),
	}
	settled := func(id string) <-chan toolExecOutcome {
		ch := make(chan toolExecOutcome, 1)
		ch <- toolExecOutcome{Run: ToolRun{ID: id, Name: "Read", Output: "output " + id, Status: "completed"}}
		return ch
	}
	pending := func() <-chan toolExecOutcome { return make(chan toolExecOutcome) }

	state := newTurnState(0, 0)
	state.toolUses = calls
	// tool-1, tool-2 settled; tool-3 still pending; tool-4 already settled.
	state.earlyExec = map[string]<-chan toolExecOutcome{
		"tool-1": settled("tool-1"),
		"tool-2": settled("tool-2"),
		"tool-3": pending(),
		"tool-4": settled("tool-4"),
	}

	out := make(chan AgentEvent, 16)
	completedCounts := []int{}
	options := normalizeGenerateOptions(
		WithSideCarCollector(0, func(ctx context.Context, request SideCarRequest) []AgentSideCar {
			completedCounts = append(completedCounts, request.CompletedToolCount)
			return nil
		}, nil),
	)
	if !drainSettledToolResults(context.Background(), out, state, options) {
		t.Fatalf("drain returned false unexpectedly")
	}
	close(out)
	emitted := []string{}
	for ev := range out {
		if ev.Type == "tool_result" {
			emitted = append(emitted, ev.ToolUseID)
		}
	}
	// Only the in-order settled prefix (tool-1, tool-2) should be drained; tool-4
	// must NOT jump ahead of the still-pending tool-3.
	if len(emitted) != 2 || emitted[0] != "tool-1" || emitted[1] != "tool-2" {
		t.Fatalf("expected drain prefix [tool-1 tool-2], got %v", emitted)
	}
	if state.completedToolCount != 2 {
		t.Fatalf("expected completedToolCount=2 after prefix drain, got %d", state.completedToolCount)
	}
	if len(completedCounts) != 2 || completedCounts[0] != 1 || completedCounts[1] != 2 {
		t.Fatalf("expected sidecar counts [1 2] in call order, got %v", completedCounts)
	}
	if state.yieldedToolResults["tool-4"] {
		t.Fatalf("tool-4 must not be yielded ahead of pending tool-3")
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
		{Parsed: ParsedStreamEvent{CredentialID: "cred-live"}},
		{Parsed: ParsedStreamEvent{ContextUsagePercentage: &percentage, Usage: &UsageSnapshot{PromptTokens: 10, ContextWindow: 100}}},
		{Parsed: ParsedStreamEvent{Metering: &MeteringSnapshot{Unit: "request", UnitPlural: "requests", Usage: 3}}},
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
	for _, want := range []string{"context_usage", "metering", "web_search", "image_generation", "output_truncated", "model_switched", "api_request_start", "api_request_end"} {
		if eventTypeCount(events, want) == 0 {
			t.Fatalf("expected %s event, got %#v", want, eventTypes(events))
		}
	}
	var requestEnd *AgentEvent
	for i := range events {
		if events[i].Type == "api_request_end" && events[i].RequestID != "req-1" {
			requestEnd = &events[i]
		}
	}
	if requestEnd == nil {
		t.Fatalf("expected loop-owned api_request_end, got %#v", events)
	}
	if requestEnd.CredentialID != "cred-live" || requestEnd.Percentage != percentage || requestEnd.Usage == nil || requestEnd.Usage.PromptTokens != 10 {
		t.Fatalf("api_request_end should carry credential/context/usage telemetry, got %#v", requestEnd)
	}
	if requestEnd.Metadata["meterUsage"] != float64(3) || requestEnd.Metadata["meterUnit"] != "request" {
		t.Fatalf("api_request_end should carry metering telemetry, got %#v", requestEnd.Metadata)
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

func TestRunAgentLoopRetriesCustomKeywordErrors(t *testing.T) {
	adapter := &scriptedAdapter{
		chatErrors: []error{errors.New("provider returned status 418: upstream says shard warming")},
		turns:      [][]StreamEvent{nil, {{Parsed: ParsedStreamEvent{Text: "recovered"}}}},
	}
	stream := runAgentLoop(
		context.Background(),
		ProviderResolution{Provider: "fake", Model: "fake-model", Adapter: adapter},
		[]Message{{Role: "user", Content: "retry"}},
		t.TempDir(),
		normalizeGenerateOptions(
			WithRetryBaseDelay(0),
			WithMaxChatRetries(2),
			withCustomRetryRules([]customRetryRule{{Keyword: "shard warming"}}),
		),
	)
	events := collectEvents(stream)
	if eventTypeCount(events, "retrying") != 1 || events[len(events)-1].Type != "done" {
		t.Fatalf("expected custom keyword retry then done, got %#v", eventTypes(events))
	}
	if adapter.chatCalls != 2 {
		t.Fatalf("expected two chat calls, got %d", adapter.chatCalls)
	}
}

func TestRunAgentLoopRetriesCustomKeywordInvalidState(t *testing.T) {
	adapter := &scriptedAdapter{turns: [][]StreamEvent{
		{{Parsed: ParsedStreamEvent{InvalidState: &InvalidState{Reason: "upstream_busy", Message: "vendor queue draining"}}}},
		{{Parsed: ParsedStreamEvent{Text: "recovered"}}},
	}}
	stream := runAgentLoop(
		context.Background(),
		ProviderResolution{Provider: "fake", Model: "fake-model", Adapter: adapter},
		[]Message{{Role: "user", Content: "retry"}},
		t.TempDir(),
		normalizeGenerateOptions(
			WithRetryBaseDelay(0),
			WithMaxChatRetries(2),
			withCustomRetryRules([]customRetryRule{{Keyword: "queue draining"}}),
		),
	)
	events := collectEvents(stream)
	if eventTypeCount(events, "retrying") != 1 || events[len(events)-1].Type != "done" {
		t.Fatalf("expected custom invalid_state retry then done, got %#v", eventTypes(events))
	}
	if adapter.chatCalls != 2 {
		t.Fatalf("expected two chat calls, got %d", adapter.chatCalls)
	}
}

func TestRunAgentLoopDoesNotRetryDisabledCustomKeywordRule(t *testing.T) {
	adapter := &scriptedAdapter{
		chatErrors: []error{errors.New("provider returned status 418: shard warming")},
	}
	stream := runAgentLoop(
		context.Background(),
		ProviderResolution{Provider: "fake", Model: "fake-model", Adapter: adapter},
		[]Message{{Role: "user", Content: "retry"}},
		t.TempDir(),
		normalizeGenerateOptions(
			WithRetryBaseDelay(0),
			WithMaxChatRetries(2),
			withCustomRetryRules([]customRetryRule{{Keyword: "shard warming", Disabled: true}}),
		),
	)
	events := collectEvents(stream)
	if eventTypeCount(events, "retrying") != 0 || events[len(events)-1].Type != "error" {
		t.Fatalf("expected disabled custom rule to leave error terminal, got %#v", eventTypes(events))
	}
}

func TestCustomRetryRulesFromSettingsParsesKeywordOnlyRule(t *testing.T) {
	store := testConfigStore(t, map[string]any{
		"agent": map[string]any{
			"customRetryRules": []any{
				map[string]any{"id": "r1", "keyword": "Vendor Busy"},
				map[string]any{"id": "r2", "keyword": "Disabled", "enabled": false},
			},
		},
	})
	rules := customRetryRulesFromSettings(store)
	if len(rules) != 1 || rules[0].Keyword != "vendor busy" || rules[0].Disabled {
		t.Fatalf("unexpected parsed custom retry rules: %#v", rules)
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
	// Mirror the TS EMPTY_RESPONSE_MESSAGE surfaced in both the retrying and the
	// final invalid_state events.
	for _, ev := range events {
		if ev.Type == "retrying" || ev.Type == "invalid_state" {
			if !strings.Contains(ev.Message, "Provider returned an empty response") || !strings.Contains(ev.Message, "API configuration error") {
				t.Fatalf("expected TS empty-response message on %s, got %q", ev.Type, ev.Message)
			}
		}
	}
}

func TestRunAgentLoopWebSearchOnlyResponseNotTreatedAsEmpty(t *testing.T) {
	adapter := &scriptedAdapter{turns: [][]StreamEvent{
		{{Parsed: ParsedStreamEvent{WebSearch: &WebSearchEvent{Status: "completed", Query: "golang"}}}},
		{{Parsed: ParsedStreamEvent{Text: "done"}}},
	}}
	stream := runAgentLoop(context.Background(), ProviderResolution{Provider: "fake", Model: "fake-model", Adapter: adapter}, []Message{{Role: "user", Content: "search"}}, t.TempDir(), normalizeGenerateOptions(WithRetryBaseDelay(0), WithEmptyResponseRetries(2)))
	events := collectEvents(stream)
	// A web-search-only turn carries meaningful content, so it must not be treated
	// as an empty response (no retrying / empty_response invalid_state).
	if eventTypeCount(events, "retrying") != 0 {
		t.Fatalf("web-search-only response must not trigger empty-response retry, got %v", eventTypes(events))
	}
	for _, ev := range events {
		if ev.Type == "invalid_state" && ev.Reason == "empty_response" {
			t.Fatalf("web-search-only response must not be classified as empty_response, got %v", eventTypes(events))
		}
	}
	if events[len(events)-1].Type != "done" {
		t.Fatalf("expected run to complete with done, got %v", eventTypes(events))
	}
}

func TestRunAgentLoopImageGenerationOnlyResponseNotTreatedAsEmpty(t *testing.T) {
	adapter := &scriptedAdapter{turns: [][]StreamEvent{
		{{Parsed: ParsedStreamEvent{ImageGeneration: &ImageGenerationEvent{Status: "completed"}}}},
		{{Parsed: ParsedStreamEvent{Text: "done"}}},
	}}
	stream := runAgentLoop(context.Background(), ProviderResolution{Provider: "fake", Model: "fake-model", Adapter: adapter}, []Message{{Role: "user", Content: "image"}}, t.TempDir(), normalizeGenerateOptions(WithRetryBaseDelay(0), WithEmptyResponseRetries(2)))
	events := collectEvents(stream)
	if eventTypeCount(events, "retrying") != 0 {
		t.Fatalf("image-generation-only response must not trigger empty-response retry, got %v", eventTypes(events))
	}
	for _, ev := range events {
		if ev.Type == "invalid_state" && ev.Reason == "empty_response" {
			t.Fatalf("image-generation-only response must not be classified as empty_response, got %v", eventTypes(events))
		}
	}
	if events[len(events)-1].Type != "done" {
		t.Fatalf("expected run to complete with done, got %v", eventTypes(events))
	}
}

func TestRunAgentLoopStreamingToolChunksExtractFields(t *testing.T) {
	adapter := &scriptedAdapter{turns: [][]StreamEvent{
		{
			{Parsed: ParsedStreamEvent{ToolUseChunk: &ToolUseChunk{ToolUseID: "write-1", Name: "Write", Input: `{"file_path":"notes.txt","content":"hel`}}},
			{Parsed: ParsedStreamEvent{ToolUseChunk: &ToolUseChunk{ToolUseID: "write-1", Input: `lo"}`, Stop: true}}},
		},
		{{Parsed: ParsedStreamEvent{Text: "done"}}},
	}}
	stream := runAgentLoop(
		context.Background(),
		ProviderResolution{Provider: "fake", Model: "fake-model", Adapter: adapter},
		[]Message{{Role: "user", Content: "write"}},
		t.TempDir(),
		normalizeGenerateOptions(
			WithRetryBaseDelay(0),
			WithToolExecutor(func(ctx context.Context, cwd string, call ToolCall) (ToolRun, error) {
				return ToolRun{ID: call.ID, Name: call.Function.Name, Output: "ok", Status: "completed"}, nil
			}),
		),
	)
	events := collectEvents(stream)
	var sawPath, sawField bool
	for _, event := range events {
		if event.Type != "tool_use_chunk" || event.Streaming == nil {
			continue
		}
		if event.Streaming.ExtractedFilePath == "notes.txt" && event.Streaming.ExtractedFields["file_path"] == "notes.txt" {
			sawPath = true
		}
		if event.Streaming.StreamingFieldName == "content" && strings.Contains(event.Streaming.StreamingFieldDelta, "hel") {
			sawField = true
		}
	}
	if !sawPath || !sawField {
		t.Fatalf("expected extracted path and streaming content field, events=%#v", events)
	}
}

func TestRunAgentLoopDrainsEarlyToolResultBeforeAssistantMessage(t *testing.T) {
	executed := make(chan struct{})
	adapter := &waitAfterToolAdapter{done: executed}
	stream := runAgentLoop(
		context.Background(),
		ProviderResolution{Provider: "fake", Model: "fake-model", Adapter: adapter},
		[]Message{{Role: "user", Content: "read"}},
		t.TempDir(),
		normalizeGenerateOptions(
			WithMaxTurns(1),
			WithRetryBaseDelay(0),
			WithToolExecutor(func(ctx context.Context, cwd string, call ToolCall) (ToolRun, error) {
				close(executed)
				return ToolRun{ID: call.ID, Name: call.Function.Name, Output: "early output", Status: "completed"}, nil
			}),
		),
	)

	events := collectEvents(stream)
	resultIndex, assistantIndex := -1, -1
	for i, event := range events {
		if event.Type == "tool_result" && event.ToolUseID == "read-early" && resultIndex == -1 {
			resultIndex = i
		}
		if event.Type == "assistant_message" && assistantIndex == -1 {
			assistantIndex = i
		}
	}
	if resultIndex == -1 || assistantIndex == -1 || resultIndex > assistantIndex {
		t.Fatalf("expected early tool_result before assistant_message, got %v", eventTypes(events))
	}
	if eventTypeCount(events, "tool_result") != 1 {
		t.Fatalf("expected exactly one tool_result, got %v", eventTypes(events))
	}
}

func TestRunAgentLoopEmitsToolRuntimeProgressOutputAndLongRunning(t *testing.T) {
	oldProgress := toolProgressInterval
	toolProgressInterval = time.Millisecond
	defer func() { toolProgressInterval = oldProgress }()
	call := newFunctionToolCall("runtime-1", "Read", `{"file_path":"a.txt"}`)
	adapter := &scriptedAdapter{turns: [][]StreamEvent{
		{{Parsed: ParsedStreamEvent{ToolUses: []ToolCall{call}}}},
		{{Parsed: ParsedStreamEvent{Text: "done"}}},
	}}
	stream := runAgentLoop(
		context.Background(),
		ProviderResolution{Provider: "fake", Model: "fake-model", Adapter: adapter},
		[]Message{{Role: "user", Content: "runtime"}},
		t.TempDir(),
		normalizeGenerateOptions(
			WithRetryBaseDelay(0),
			WithToolExecutor(func(ctx context.Context, cwd string, call ToolCall) (ToolRun, error) {
				EmitToolRuntimeOutput(ctx, "partial output")
				EmitToolRuntimeLongRunning(ctx, 61*time.Second)
				time.Sleep(30 * time.Millisecond)
				return ToolRun{ID: call.ID, Name: call.Function.Name, Output: "final output", Status: "completed"}, nil
			}),
		),
	)
	events := collectEvents(stream)
	if eventTypeCount(events, "tool_output") == 0 || eventTypeCount(events, "tool_long_running") == 0 || eventTypeCount(events, "tool_progress") == 0 {
		t.Fatalf("expected runtime output/long-running/progress events, got %v", eventTypes(events))
	}
}

// TestExecuteToolCallTickerDoesNotEmitAfterReturn guards the fix for the
// send-on-closed-channel race: with a very small progress interval and a tool
// that produces many ticks, the progress ticker goroutine must fully exit before
// executeToolCall returns, so the agent loop can close `out` without a panic or
// race. Run under `go test -race` to catch regressions.
func TestExecuteToolCallTickerDoesNotEmitAfterReturn(t *testing.T) {
	oldProgress := toolProgressInterval
	toolProgressInterval = time.Millisecond
	defer func() { toolProgressInterval = oldProgress }()
	// Two sequential tool turns so the loop spawns and tears down tickers twice.
	callA := newFunctionToolCall("ticker-A", "Read", `{"file_path":"a.txt"}`)
	callB := newFunctionToolCall("ticker-B", "Read", `{"file_path":"b.txt"}`)
	adapter := &scriptedAdapter{turns: [][]StreamEvent{
		{{Parsed: ParsedStreamEvent{ToolUses: []ToolCall{callA}}}},
		{{Parsed: ParsedStreamEvent{ToolUses: []ToolCall{callB}}}},
		{{Parsed: ParsedStreamEvent{Text: "done"}}},
	}}
	stream := runAgentLoop(
		context.Background(),
		ProviderResolution{Provider: "fake", Model: "fake-model", Adapter: adapter},
		[]Message{{Role: "user", Content: "go"}},
		t.TempDir(),
		normalizeGenerateOptions(
			WithRetryBaseDelay(0),
			WithToolExecutor(func(ctx context.Context, cwd string, call ToolCall) (ToolRun, error) {
				// Sleep long enough to generate many progress ticks per tool.
				time.Sleep(20 * time.Millisecond)
				return ToolRun{ID: call.ID, Name: call.Function.Name, Output: "ok", Status: "completed"}, nil
			}),
		),
	)
	// Drain slowly to apply backpressure on the buffered out channel, maximizing
	// the chance a ticker send is in-flight when the tool finishes.
	for range stream {
		time.Sleep(time.Millisecond)
	}
	// Reaching here without a panic/race (under -race) confirms tickers never emit
	// after the loop closes the channel.
}
