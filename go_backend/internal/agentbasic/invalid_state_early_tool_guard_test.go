package agentbasic

import (
	"context"
	"sync"
	"testing"
)

// TestHasStartedEarlyToolExecution pins the guard contract: it is true when any
// early tool is in-flight OR any early tool result was already yielded, matching
// the TS hasStartedEarlyToolExecution (earlyExecMap.size>0 || yieldedToolResults.size>0).
func TestHasStartedEarlyToolExecution(t *testing.T) {
	if hasStartedEarlyToolExecution(nil) {
		t.Fatal("nil state must report no early tool execution")
	}
	empty := newTurnState(0, 0)
	if hasStartedEarlyToolExecution(empty) {
		t.Fatal("fresh state must report no early tool execution")
	}
	withPending := newTurnState(0, 0)
	ch := make(chan toolExecOutcome, 1)
	withPending.earlyExec["t1"] = ch
	if !hasStartedEarlyToolExecution(withPending) {
		t.Fatal("state with a pending early tool must report started")
	}
	// The key regression: a yielded result with earlyExec already cleared must
	// still report started so the retry layer does not re-run the tool.
	withYielded := newTurnState(0, 0)
	withYielded.yieldedToolResults["t1"] = true
	if !hasStartedEarlyToolExecution(withYielded) {
		t.Fatal("state with a yielded early tool result must report started even after earlyExec is cleared")
	}
}

// earlyToolThenInvalidStateAdapter emits an eager Read tool_use, waits until the
// tool executor has run, then (after the tool result has been drained/yielded)
// emits a retryable invalid_state. This deterministically reproduces the state
// where earlyExec is cleared but yieldedToolResults is populated.
type earlyToolThenInvalidStateAdapter struct {
	chatCalls int
	toolDone  chan struct{}
}

func (a *earlyToolThenInvalidStateAdapter) ProviderName() string                { return "fake" }
func (a *earlyToolThenInvalidStateAdapter) FormatTools(string) []map[string]any { return nil }
func (a *earlyToolThenInvalidStateAdapter) BuildHistory(messages []Message, _ string, _ string) (BuiltHistory, error) {
	history := make([]any, 0, len(messages))
	for _, message := range messages {
		history = append(history, message)
	}
	return BuiltHistory{History: history}, nil
}
func (a *earlyToolThenInvalidStateAdapter) InjectSystemPrompt(*BuiltHistory, string, string, string) {
}
func (a *earlyToolThenInvalidStateAdapter) Chat(ctx context.Context, _ ChatParams) (<-chan StreamEvent, error) {
	a.chatCalls++
	ch := make(chan StreamEvent)
	go func() {
		defer close(ch)
		// 1) Emit the eager Read tool_use (starts background execution).
		select {
		case <-ctx.Done():
			return
		case ch <- StreamEvent{Parsed: ParsedStreamEvent{ToolUses: []ToolCall{newFunctionToolCall("read-1", "Read", `{"file_path":"a.txt"}`)}}}:
		}
		// 2) Wait until the tool executor has actually run.
		select {
		case <-ctx.Done():
			return
		case <-a.toolDone:
		}
		// 3) Emit a stream_text so the loop drains+yields the (now complete) tool
		//    result, then emit the retryable invalid_state.
		select {
		case <-ctx.Done():
			return
		case ch <- StreamEvent{Parsed: ParsedStreamEvent{Text: "partial"}}:
		}
		select {
		case <-ctx.Done():
			return
		case ch <- StreamEvent{Parsed: ParsedStreamEvent{InvalidState: &InvalidState{Reason: "service_unavailable", Message: "upstream briefly unavailable"}}}:
		}
	}()
	return ch, nil
}
func (a *earlyToolThenInvalidStateAdapter) FormatToolResult(toolUseID string, output string, isError bool) any {
	return map[string]any{"type": "tool_result", "tool_use_id": toolUseID, "content": output, "is_error": isError}
}
func (a *earlyToolThenInvalidStateAdapter) PushUserTurn(*BuiltHistory, string, []any) {}
func (a *earlyToolThenInvalidStateAdapter) PushAssistantTurn(*BuiltHistory, string, []ToolCall, any) {
}

// TestRetryableInvalidStateAfterYieldedToolResultDoesNotRetry verifies that when
// an early (eager) tool already ran and its result was yielded, a subsequent
// retryable invalid_state does NOT retry the whole chat (which would re-run the
// tool and duplicate side effects); it terminates with invalid_state instead.
func TestRetryableInvalidStateAfterYieldedToolResultDoesNotRetry(t *testing.T) {
	adapter := &earlyToolThenInvalidStateAdapter{toolDone: make(chan struct{})}
	var toolCalls int
	var once sync.Once
	stream := runAgentLoop(
		context.Background(),
		ProviderResolution{Provider: "fake", Model: "fake-model", Adapter: adapter},
		[]Message{{Role: "user", Content: "read"}},
		t.TempDir(),
		normalizeGenerateOptions(
			WithRetryBaseDelay(0),
			WithMaxChatRetries(5),
			WithToolExecutor(func(ctx context.Context, cwd string, call ToolCall) (ToolRun, error) {
				toolCalls++
				once.Do(func() { close(adapter.toolDone) })
				return ToolRun{ID: call.ID, Name: call.Function.Name, Output: "file contents", Status: "completed"}, nil
			}),
		),
	)
	events := collectEvents(stream)
	if eventTypeCount(events, "retrying") != 0 {
		t.Fatalf("must not retry after an early tool result was yielded, got %v", eventTypes(events))
	}
	if eventTypeCount(events, "invalid_state") != 1 {
		t.Fatalf("expected a terminal invalid_state, got %v", eventTypes(events))
	}
	if toolCalls != 1 {
		t.Fatalf("tool must run exactly once (no duplicated side effects), ran %d times", toolCalls)
	}
	if adapter.chatCalls != 1 {
		t.Fatalf("must not re-issue chat after yielded tool result, got %d chat calls", adapter.chatCalls)
	}
}
