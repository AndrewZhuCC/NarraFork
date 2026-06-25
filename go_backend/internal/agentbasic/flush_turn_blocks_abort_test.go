package agentbasic

import (
	"context"
	"testing"
)

// TestFlushTurnBlocksDropsNothingOnAbort verifies that when the run context is
// cancelled (user interrupt), flushTurnBlocks still delivers the accumulated
// text/reasoning blocks as block_complete events instead of dropping them.
// This guards the abort-path fix: a plain emitAgentEvent would hit its
// <-ctx.Done() branch and silently discard the blocks, losing any content the
// model had already streamed before the interrupt.
func TestFlushTurnBlocksDropsNothingOnAbort(t *testing.T) {
	idx := 0
	state := newTurnState(0, 0)
	state.reasoning["__default"] = &reasoningAccum{Text: "thinking before interrupt"}
	state.assistantText = "completed text before interrupt"
	state.textOutputIndex = &idx

	out := make(chan AgentEvent, 8)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // simulate a user interrupt: run context already cancelled

	flushTurnBlocks(ctx, out, state)
	close(out)

	var texts, reasonings int
	for event := range out {
		if event.Type != "block_complete" || event.Block == nil {
			t.Fatalf("unexpected event on abort flush: %#v", event)
		}
		switch event.Block.Type {
		case "text":
			texts++
			if event.Block.Text != "completed text before interrupt" {
				t.Fatalf("text block content drifted: %q", event.Block.Text)
			}
		case "reasoning":
			reasonings++
			if event.Block.Text != "thinking before interrupt" {
				t.Fatalf("reasoning block content drifted: %q", event.Block.Text)
			}
		default:
			t.Fatalf("unexpected block type on abort flush: %q", event.Block.Type)
		}
	}

	if texts != 1 {
		t.Fatalf("expected exactly 1 text block_complete on abort, got %d", texts)
	}
	if reasonings != 1 {
		t.Fatalf("expected exactly 1 reasoning block_complete on abort, got %d", reasonings)
	}
}

// TestFlushTurnBlocksNormalContextStillEmits ensures the non-aborted path keeps
// working: with a live context the blocks are emitted via the normal
// ctx-honoring emitter.
func TestFlushTurnBlocksNormalContextStillEmits(t *testing.T) {
	state := newTurnState(0, 0)
	state.assistantText = "normal text"

	out := make(chan AgentEvent, 4)
	flushTurnBlocks(context.Background(), out, state)
	close(out)

	var texts int
	for event := range out {
		if event.Type == "block_complete" && event.Block != nil && event.Block.Type == "text" {
			texts++
		}
	}
	if texts != 1 {
		t.Fatalf("expected 1 text block_complete on normal flush, got %d", texts)
	}
}
