package agentbasic

import (
	"context"
	"testing"
	"time"
)

// TestSilentDisconnectFlushesPartialBlocks locks that a provider silent-disconnect
// mid-turn flushes the already-streamed assistant text and reasoning as
// block_complete events before the terminal silent_disconnect event. The
// errTerminalEvent path in runAgentTurnWithRetry must flush (via the emission
// site) like TS flushPartialContent, otherwise the streamed reply vanishes from
// the persisted assistant message on reload.
func TestSilentDisconnectFlushesPartialBlocks(t *testing.T) {
	adapter := &scriptedAdapter{turns: [][]StreamEvent{{
		{Parsed: ParsedStreamEvent{Reasoning: "thinking about the task"}},
		{Parsed: ParsedStreamEvent{Text: "Here is my plan before the drop"}},
		{Parsed: ParsedStreamEvent{SilentDisconnect: true}},
	}}}
	stream := runAgentLoop(
		context.Background(),
		ProviderResolution{Provider: "fake", Model: "model", Adapter: adapter},
		[]Message{{Role: "user", Content: "hi"}},
		t.TempDir(),
		normalizeGenerateOptions(WithMaxTurns(1), WithFirstTokenTimeout(time.Second), WithRetryBaseDelay(0)),
	)
	events := collectEvents(stream)
	if eventTypeCount(events, "silent_disconnect") != 1 {
		t.Fatalf("expected silent_disconnect event, got %v", eventTypes(events))
	}
	var textBlocks, reasoningBlocks int
	for _, ev := range events {
		if ev.Type == "block_complete" && ev.Block != nil {
			switch ev.Block.Type {
			case "text":
				textBlocks++
				if ev.Block.Text != "Here is my plan before the drop" {
					t.Fatalf("text block drifted: %q", ev.Block.Text)
				}
			case "reasoning":
				reasoningBlocks++
				if ev.Block.Text != "thinking about the task" {
					t.Fatalf("reasoning block drifted: %q", ev.Block.Text)
				}
			}
		}
	}
	if textBlocks != 1 {
		t.Fatalf("expected 1 text block_complete flushed before silent_disconnect, got %d (events=%v)", textBlocks, eventTypes(events))
	}
	if reasoningBlocks != 1 {
		t.Fatalf("expected 1 reasoning block_complete flushed before silent_disconnect, got %d (events=%v)", reasoningBlocks, eventTypes(events))
	}
}
