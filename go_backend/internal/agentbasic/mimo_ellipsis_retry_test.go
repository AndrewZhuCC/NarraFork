package agentbasic

import (
	"context"
	"strings"
	"testing"
)

// mimoReasoningTurn builds a turn that streams "..." as reasoning text followed
// by a reasoning-metadata (signature) event finalizing the block — the shape a
// mimo model produces for a degenerate ellipsis-only reasoning response.
func mimoEllipsisTurn() []StreamEvent {
	idx := 0
	return []StreamEvent{
		{Parsed: ParsedStreamEvent{Reasoning: "...", ReasoningOutputIndex: &idx}},
		{Parsed: ParsedStreamEvent{ReasoningMetadata: map[string]any{"signature": "sig"}, ReasoningOutputIndex: &idx}},
	}
}

// TestMimoEllipsisReasoningRetriesThenSucceeds verifies that a mimo model
// returning "..." as its entire reasoning is discarded and retried, recovering
// when the next attempt produces real content (mirrors TS loop.ts mimo retry).
func TestMimoEllipsisReasoningRetriesThenSucceeds(t *testing.T) {
	adapter := &scriptedAdapter{turns: [][]StreamEvent{
		mimoEllipsisTurn(),
		{{Parsed: ParsedStreamEvent{Text: "recovered"}}},
	}}
	stream := runAgentLoop(context.Background(), ProviderResolution{Provider: "fake", Model: "mimo-v2", Adapter: adapter}, []Message{{Role: "user", Content: "hi"}}, t.TempDir(), normalizeGenerateOptions(WithRetryBaseDelay(0), WithMaxChatRetries(3)))
	events := collectEvents(stream)
	if eventTypeCount(events, "retrying") != 1 {
		t.Fatalf("expected one retrying event for mimo ellipsis, got %v", eventTypes(events))
	}
	var sawMimoMessage bool
	for _, ev := range events {
		if ev.Type == "retrying" && strings.Contains(ev.Message, "ellipsis-only reasoning") {
			sawMimoMessage = true
		}
	}
	if !sawMimoMessage {
		t.Fatalf("expected mimo ellipsis retry message, got %v", events)
	}
	if events[len(events)-1].Type != "done" {
		t.Fatalf("expected run to complete with done after retry, got %v", eventTypes(events))
	}
	if adapter.chatCalls != 2 {
		t.Fatalf("expected 2 chat attempts, got %d", adapter.chatCalls)
	}
}

// TestMimoEllipsisReasoningNonMimoModelNotRetried verifies the ellipsis retry is
// mimo-only: a non-mimo model returning "..." reasoning is processed normally.
func TestMimoEllipsisReasoningNonMimoModelNotRetried(t *testing.T) {
	adapter := &scriptedAdapter{turns: [][]StreamEvent{
		append(mimoEllipsisTurn(), StreamEvent{Parsed: ParsedStreamEvent{Text: "answer"}}),
	}}
	stream := runAgentLoop(context.Background(), ProviderResolution{Provider: "fake", Model: "gpt-4o", Adapter: adapter}, []Message{{Role: "user", Content: "hi"}}, t.TempDir(), normalizeGenerateOptions(WithRetryBaseDelay(0), WithMaxChatRetries(3)))
	events := collectEvents(stream)
	if eventTypeCount(events, "retrying") != 0 {
		t.Fatalf("non-mimo model must not trigger ellipsis retry, got %v", eventTypes(events))
	}
	if adapter.chatCalls != 1 {
		t.Fatalf("expected single chat attempt for non-mimo model, got %d", adapter.chatCalls)
	}
}

// TestMimoEllipsisReasoningNonEllipsisNotRetried verifies a mimo model with real
// (non-ellipsis) reasoning is not retried.
func TestMimoEllipsisReasoningNonEllipsisNotRetried(t *testing.T) {
	idx := 0
	adapter := &scriptedAdapter{turns: [][]StreamEvent{
		{
			{Parsed: ParsedStreamEvent{Reasoning: "real thinking here", ReasoningOutputIndex: &idx}},
			{Parsed: ParsedStreamEvent{ReasoningMetadata: map[string]any{"signature": "sig"}, ReasoningOutputIndex: &idx}},
			{Parsed: ParsedStreamEvent{Text: "answer"}},
		},
	}}
	stream := runAgentLoop(context.Background(), ProviderResolution{Provider: "fake", Model: "mimo-v2", Adapter: adapter}, []Message{{Role: "user", Content: "hi"}}, t.TempDir(), normalizeGenerateOptions(WithRetryBaseDelay(0), WithMaxChatRetries(3)))
	events := collectEvents(stream)
	if eventTypeCount(events, "retrying") != 0 {
		t.Fatalf("mimo model with real reasoning must not retry, got %v", eventTypes(events))
	}
	if adapter.chatCalls != 1 {
		t.Fatalf("expected single chat attempt, got %d", adapter.chatCalls)
	}
}
