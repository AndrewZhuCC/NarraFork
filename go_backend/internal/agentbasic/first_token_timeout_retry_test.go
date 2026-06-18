package agentbasic

import (
	"context"
	"testing"
	"time"
)

// hangOnceAdapter hangs (waits for the turn context to cancel) on the first
// hangCalls chat attempts, then returns a normal text stream. Used to verify the
// first-token-timeout retry recovers when a later attempt succeeds.
type hangOnceAdapter struct {
	hangCalls int
	chatCalls int
	text      string
}

func (a *hangOnceAdapter) ProviderName() string                { return "fake" }
func (a *hangOnceAdapter) FormatTools(string) []map[string]any { return nil }
func (a *hangOnceAdapter) BuildHistory(messages []Message, _ string, _ string) (BuiltHistory, error) {
	history := make([]any, 0, len(messages))
	for _, message := range messages {
		history = append(history, message)
	}
	return BuiltHistory{History: history}, nil
}
func (a *hangOnceAdapter) InjectSystemPrompt(*BuiltHistory, string, string, string) {}
func (a *hangOnceAdapter) Chat(ctx context.Context, _ ChatParams) (<-chan StreamEvent, error) {
	call := a.chatCalls
	a.chatCalls++
	ch := make(chan StreamEvent)
	go func() {
		defer close(ch)
		if call < a.hangCalls {
			<-ctx.Done() // hang until first-token timeout cancels the turn
			return
		}
		text := a.text
		if text == "" {
			text = "recovered"
		}
		select {
		case <-ctx.Done():
		case ch <- StreamEvent{Parsed: ParsedStreamEvent{Text: text}}:
		}
	}()
	return ch, nil
}
func (a *hangOnceAdapter) FormatToolResult(toolUseID string, output string, isError bool) any {
	return map[string]any{"type": "tool_result", "tool_use_id": toolUseID, "content": output, "is_error": isError}
}
func (a *hangOnceAdapter) PushUserTurn(*BuiltHistory, string, []any)                {}
func (a *hangOnceAdapter) PushAssistantTurn(*BuiltHistory, string, []ToolCall, any) {}

// TestFirstTokenTimeoutRetriesThenSucceeds verifies a first-token timeout on the
// first attempt is retried and the loop recovers when the next attempt produces
// a response, mirroring the TS retry-then-succeed behavior.
func TestFirstTokenTimeoutRetriesThenSucceeds(t *testing.T) {
	adapter := &hangOnceAdapter{hangCalls: 1, text: "recovered"}
	stream := runAgentLoop(
		context.Background(),
		ProviderResolution{Provider: "fake", Model: "model", Adapter: adapter},
		[]Message{{Role: "user", Content: "hi"}},
		t.TempDir(),
		normalizeGenerateOptions(WithMaxTurns(1), WithFirstTokenTimeout(20*time.Millisecond), WithMaxChatRetries(3), WithRetryBaseDelay(0)),
	)
	events := collectEvents(stream)
	if eventTypeCount(events, "retrying") != 1 {
		t.Fatalf("expected one retrying event before recovery, got %v", eventTypes(events))
	}
	if eventTypeCount(events, "retryable_error") != 0 || eventTypeCount(events, "first_token_timeout") != 0 {
		t.Fatalf("recovered run must not emit retryable_error/first_token_timeout, got %v", eventTypes(events))
	}
	if events[len(events)-1].Type != "done" {
		t.Fatalf("expected run to complete with done after retry recovery, got %v", eventTypes(events))
	}
	if adapter.chatCalls != 2 {
		t.Fatalf("expected 2 chat attempts (timeout + recovery), got %d", adapter.chatCalls)
	}
}
