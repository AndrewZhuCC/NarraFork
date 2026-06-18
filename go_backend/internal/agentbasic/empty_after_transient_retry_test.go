package agentbasic

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestEmptyResponseAfterTransientErrorUsesChatRetryAndOriginalMessage verifies
// that an empty response following a transient chat error is treated as a
// continuation of that transient failure: it retries via the chat counter and
// surfaces the ORIGINAL error message (not the "empty response" text), mirroring
// TS loop.ts lastRetryErrorMessage handling.
func TestEmptyResponseAfterTransientErrorUsesChatRetryAndOriginalMessage(t *testing.T) {
	adapter := &scriptedAdapter{
		chatErrors: []error{errors.New("provider returned status 503: service unavailable"), nil, nil},
		turns: [][]StreamEvent{
			nil, // call0 errored
			{},  // call1 empty stream
			{{Parsed: ParsedStreamEvent{Text: "recovered"}}}, // call2 recovers
		},
	}
	stream := runAgentLoop(context.Background(), ProviderResolution{Provider: "fake", Model: "fake-model", Adapter: adapter}, []Message{{Role: "user", Content: "hi"}}, t.TempDir(), normalizeGenerateOptions(WithRetryBaseDelay(0), WithMaxChatRetries(5)))
	events := collectEvents(stream)
	retryings := []AgentEvent{}
	for _, ev := range events {
		if ev.Type == "retrying" {
			retryings = append(retryings, ev)
		}
	}
	if len(retryings) != 2 {
		t.Fatalf("expected 2 retrying events (transient + empty-continuation), got %v", eventTypes(events))
	}
	// Both retrying events must carry the ORIGINAL transient error, not the
	// empty-response message.
	for i, ev := range retryings {
		if !strings.Contains(ev.Message, "503") {
			t.Fatalf("retrying #%d should carry the original transient error, got %q", i, ev.Message)
		}
		if strings.Contains(ev.Message, "empty response") || strings.Contains(ev.Message, "Provider returned an empty response") {
			t.Fatalf("retrying #%d must not use the empty-response message, got %q", i, ev.Message)
		}
	}
	if events[len(events)-1].Type != "done" {
		t.Fatalf("expected run to recover and complete with done, got %v", eventTypes(events))
	}
	if adapter.chatCalls != 3 {
		t.Fatalf("expected 3 chat attempts, got %d", adapter.chatCalls)
	}
}

// TestEmptyResponseAfterTransientErrorExhaustsToRetryableError verifies that when
// the chat retry budget is exhausted while empty responses continue after a
// transient error, the loop surfaces a retryable_error carrying the original
// message (NOT an empty_response invalid_state).
func TestEmptyResponseAfterTransientErrorExhaustsToRetryableError(t *testing.T) {
	adapter := &scriptedAdapter{
		chatErrors: []error{errors.New("provider returned status 503: service unavailable"), nil, nil, nil},
		turns: [][]StreamEvent{
			nil, {}, {}, {}, // transient error then all empty
		},
	}
	stream := runAgentLoop(context.Background(), ProviderResolution{Provider: "fake", Model: "fake-model", Adapter: adapter}, []Message{{Role: "user", Content: "hi"}}, t.TempDir(), normalizeGenerateOptions(WithRetryBaseDelay(0), WithMaxChatRetries(1)))
	events := collectEvents(stream)
	last := events[len(events)-1]
	if last.Type != "retryable_error" {
		t.Fatalf("expected terminal retryable_error, got %v", eventTypes(events))
	}
	if !strings.Contains(last.Message, "503") {
		t.Fatalf("retryable_error should carry the original transient error, got %q", last.Message)
	}
	for _, ev := range events {
		if ev.Type == "invalid_state" && ev.Reason == "empty_response" {
			t.Fatalf("empty-after-transient must not become an empty_response invalid_state, got %v", eventTypes(events))
		}
	}
}

// TestGenuineEmptyResponseStillUsesEmptyResponsePath verifies a genuine empty
// response (no prior transient error) still uses the empty-response counter and
// the empty_response invalid_state with the TS empty-response message.
func TestGenuineEmptyResponseStillUsesEmptyResponsePath(t *testing.T) {
	adapter := &scriptedAdapter{turns: [][]StreamEvent{{}, {}}}
	stream := runAgentLoop(context.Background(), ProviderResolution{Provider: "fake", Model: "fake-model", Adapter: adapter}, []Message{{Role: "user", Content: "empty"}}, t.TempDir(), normalizeGenerateOptions(WithRetryBaseDelay(0), WithEmptyResponseRetries(1)))
	events := collectEvents(stream)
	last := events[len(events)-1]
	if last.Type != "invalid_state" || last.Reason != "empty_response" {
		t.Fatalf("genuine empty response should end in empty_response invalid_state, got %v", eventTypes(events))
	}
	if !strings.Contains(last.Message, "Provider returned an empty response") {
		t.Fatalf("expected TS empty-response message, got %q", last.Message)
	}
}

// TestMeaningfulContentClearsLastRetryError verifies that a transient error
// followed by an attempt that produces real content clears the retained error
// (no empty-continuation behavior leaks into the successful run).
func TestMeaningfulContentClearsLastRetryError(t *testing.T) {
	adapter := &scriptedAdapter{
		chatErrors: []error{errors.New("provider returned status 503"), nil},
		turns: [][]StreamEvent{
			nil,
			{{Parsed: ParsedStreamEvent{Text: "answer"}}},
		},
	}
	stream := runAgentLoop(context.Background(), ProviderResolution{Provider: "fake", Model: "fake-model", Adapter: adapter}, []Message{{Role: "user", Content: "hi"}}, t.TempDir(), normalizeGenerateOptions(WithRetryBaseDelay(0), WithMaxChatRetries(3)))
	events := collectEvents(stream)
	if eventTypeCount(events, "retrying") != 1 {
		t.Fatalf("expected exactly one (transient) retrying, got %v", eventTypes(events))
	}
	if events[len(events)-1].Type != "done" {
		t.Fatalf("expected done after recovery, got %v", eventTypes(events))
	}
}
