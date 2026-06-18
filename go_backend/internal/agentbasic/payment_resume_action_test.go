package agentbasic

import "testing"

// TestPaymentResumeAction pins the payment_required resumeAction parity with TS
// loop.ts:3046-3049: "continue" when any early tool has started/yielded OR the
// turn resumed from prior tool results (TrailingToolResults); otherwise "retry".
func TestPaymentResumeAction(t *testing.T) {
	// Fresh turn, no early tools, no initial tool results -> retry.
	if got := paymentResumeAction(newTurnState(0, 0), &BuiltHistory{}); got != "retry" {
		t.Fatalf("fresh turn should resume with retry, got %q", got)
	}
	if got := paymentResumeAction(nil, nil); got != "retry" {
		t.Fatalf("nil state/built should resume with retry, got %q", got)
	}

	// Pending early tool -> continue.
	pending := newTurnState(0, 0)
	pending.earlyExec["t1"] = make(chan toolExecOutcome, 1)
	if got := paymentResumeAction(pending, &BuiltHistory{}); got != "continue" {
		t.Fatalf("pending early tool should resume with continue, got %q", got)
	}

	// Yielded early tool result (earlyExec already cleared) -> continue.
	yielded := newTurnState(0, 0)
	yielded.yieldedToolResults["t1"] = true
	if got := paymentResumeAction(yielded, &BuiltHistory{}); got != "continue" {
		t.Fatalf("yielded early tool result should resume with continue, got %q", got)
	}

	// Turn resumed from prior tool results (no new early tools) -> continue.
	resumed := newTurnState(0, 0)
	built := &BuiltHistory{TrailingToolResults: []any{map[string]any{"type": "tool_result"}}}
	if got := paymentResumeAction(resumed, built); got != "continue" {
		t.Fatalf("turn with initial tool results should resume with continue, got %q", got)
	}
}
