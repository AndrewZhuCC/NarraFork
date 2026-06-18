package agentbasic

import (
	"context"
	"testing"
)

// TestRunAgentLoopCompletionLimitEmitsOutputTruncatedNotError verifies that a
// provider InvalidState with a completion-limit reason (max_tokens) is treated
// as a graceful output truncation: the partial assistant text is preserved, an
// output_truncated event is emitted, and the turn ends with done (not an
// invalid_state/error event). Mirrors TS loop.ts isCompletionLimitReason.
func TestRunAgentLoopCompletionLimitEmitsOutputTruncatedNotError(t *testing.T) {
	adapter := &scriptedAdapter{turns: [][]StreamEvent{{
		{Parsed: ParsedStreamEvent{Text: "partial answer that got cut"}},
		{Parsed: ParsedStreamEvent{InvalidState: &InvalidState{Reason: "max_tokens", Message: "Response truncated: model reached maximum token limit."}}},
	}}}
	stream := runAgentLoop(
		context.Background(),
		ProviderResolution{Provider: "fake", Model: "fake-model", Adapter: adapter},
		[]Message{{Role: "user", Content: "write a long answer"}},
		t.TempDir(),
		normalizeGenerateOptions(WithRetryBaseDelay(0)),
	)
	events := collectEvents(stream)

	if eventTypeCount(events, "output_truncated") != 1 {
		t.Fatalf("expected exactly one output_truncated event, got %#v", eventTypes(events))
	}
	if eventTypeCount(events, "invalid_state") != 0 {
		t.Fatalf("completion limit must not surface as invalid_state, got %#v", eventTypes(events))
	}
	if eventTypeCount(events, "error") != 0 {
		t.Fatalf("completion limit must not surface as error, got %#v", eventTypes(events))
	}
	if events[len(events)-1].Type != "done" {
		t.Fatalf("expected the turn to end with done, got %#v", eventTypes(events))
	}
	// The partial assistant text streamed before the limit must be preserved.
	var assistant *AgentEvent
	for i := range events {
		if events[i].Type == "assistant_message" {
			assistant = &events[i]
		}
	}
	if assistant == nil {
		t.Fatalf("expected an assistant_message event, got %#v", eventTypes(events))
	}
	if assistant.Text != "partial answer that got cut" {
		t.Fatalf("partial assistant text not preserved, got %q", assistant.Text)
	}
	// Only one provider call should occur (no retry on completion limit).
	if adapter.chatCalls != 1 {
		t.Fatalf("expected one chat call, got %d", adapter.chatCalls)
	}
}

// TestRunAgentLoopNonCompletionInvalidStateStillErrors guards that a genuine
// non-retryable InvalidState (e.g. content_filter) still surfaces as an
// invalid_state terminal event rather than being swallowed as output_truncated.
func TestRunAgentLoopNonCompletionInvalidStateStillErrors(t *testing.T) {
	adapter := &scriptedAdapter{turns: [][]StreamEvent{{
		{Parsed: ParsedStreamEvent{InvalidState: &InvalidState{Reason: "content_filter", Message: "blocked by content filter"}}},
	}}}
	stream := runAgentLoop(
		context.Background(),
		ProviderResolution{Provider: "fake", Model: "fake-model", Adapter: adapter},
		[]Message{{Role: "user", Content: "blocked"}},
		t.TempDir(),
		normalizeGenerateOptions(WithRetryBaseDelay(0)),
	)
	events := collectEvents(stream)
	if eventTypeCount(events, "output_truncated") != 0 {
		t.Fatalf("content_filter must not be treated as output_truncated, got %#v", eventTypes(events))
	}
	if events[len(events)-1].Type != "invalid_state" {
		t.Fatalf("expected terminal invalid_state, got %#v", eventTypes(events))
	}
}
