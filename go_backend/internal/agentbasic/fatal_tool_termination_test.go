package agentbasic

import (
	"context"
	"strings"
	"testing"
)

// TestRunAgentLoopFatalToolResultTerminatesLoop pins the fatal-terminate-loop
// contract (mirrors TS loop.ts:3721-3730). When a tool result carries Fatal=true
// (set by narrators for catastrophic command denies), the agent loop must:
//  1. still emit the tool_result event for that tool (UI visibility), then
//  2. emit a terminal `error` event carrying the FATAL message, and
//  3. stop the loop entirely — no further provider turn is requested and no
//     "max rounds" / "done" terminal is produced.
//
// This guards the W785 parity with TS, where catastrophic commands are denied as
// fatal and terminate the whole narrator for safety rather than letting the model
// continue issuing tool calls.
func TestRunAgentLoopFatalToolResultTerminatesLoop(t *testing.T) {
	fatalCall := newFunctionToolCall("fatal-1", "Bash", `{"command":"rm -rf /"}`)
	// turn 1 would continue the conversation if the loop did NOT terminate; it
	// must never be requested once the fatal result is processed.
	adapter := &scriptedAdapter{turns: [][]StreamEvent{
		{{Parsed: ParsedStreamEvent{ToolUses: []ToolCall{fatalCall}}}},
		{{Parsed: ParsedStreamEvent{Text: "should never be requested"}}},
	}}
	const fatalMsg = "FATAL: rm recursive on critical path /. Narrator terminated for safety."
	stream := runAgentLoop(
		context.Background(),
		ProviderResolution{Provider: "fake", Model: "fake-model", Adapter: adapter},
		[]Message{{Role: "user", Content: "destroy"}},
		t.TempDir(),
		normalizeGenerateOptions(
			WithRetryBaseDelay(0),
			WithMaxTurns(5),
			WithToolExecutor(func(_ context.Context, _ string, call ToolCall) (ToolRun, error) {
				return ToolRun{ID: call.ID, Name: call.Function.Name, Output: fatalMsg, Status: "denied", Fatal: true}, nil
			}),
		),
	)
	events := collectEvents(stream)

	// (1) The fatal tool's result must still be emitted for the UI.
	if eventTypeCount(events, "tool_result") != 1 {
		t.Fatalf("expected the fatal tool_result to still be emitted, got %#v", eventTypes(events))
	}
	// (2) A terminal error event must carry the FATAL message.
	var errEvent *AgentEvent
	for i := range events {
		if events[i].Type == "error" {
			errEvent = &events[i]
			break
		}
	}
	if errEvent == nil {
		t.Fatalf("expected a terminal error event for the fatal result, got %#v", eventTypes(events))
	}
	if !strings.Contains(errEvent.Message, "FATAL") || !strings.Contains(errEvent.Message, "Narrator terminated for safety.") {
		t.Fatalf("error event must surface the FATAL message, got %q", errEvent.Message)
	}
	if errEvent != &events[len(events)-1] {
		t.Fatalf("the fatal error event must be terminal (last), got events %#v", eventTypes(events))
	}
	// (3) The loop must terminate: only the first provider turn is requested, no
	// "max rounds" error wording, and no successful done terminal.
	if adapter.chatCalls != 1 {
		t.Fatalf("fatal result must terminate before the next provider turn, chatCalls=%d", adapter.chatCalls)
	}
	if strings.Contains(errEvent.Message, "maximum rounds") {
		t.Fatalf("fatal termination must not fall through to the max-rounds error, got %q", errEvent.Message)
	}
	if eventTypeCount(events, "done") != 0 {
		t.Fatalf("fatal termination must not emit a done terminal, got %#v", eventTypes(events))
	}
	// History must NOT have the tool results pushed back (loop returns before the
	// caller's PushUserTurn), matching TS fatal-return-before-pushAssistantTurn.
	if adapter.pushedUsers != 0 {
		t.Fatalf("fatal termination must not push tool results into history, pushedUsers=%d", adapter.pushedUsers)
	}
}

// TestRunAgentLoopNonFatalDenyContinuesLoop is the negative control for the
// fatal-terminate contract: a regular (non-fatal) deny — e.g. a
// protected/blacklist command — yields a tool_result with Status "denied" but
// Fatal=false, and the loop MUST continue so the model can try a different
// approach. Only catastrophic (fatal) denies stop the whole narrator.
func TestRunAgentLoopNonFatalDenyContinuesLoop(t *testing.T) {
	deniedCall := newFunctionToolCall("denied-1", "Bash", `{"command":"git push --force"}`)
	adapter := &scriptedAdapter{turns: [][]StreamEvent{
		{{Parsed: ParsedStreamEvent{ToolUses: []ToolCall{deniedCall}}}},
		{{Parsed: ParsedStreamEvent{Text: "continued after non-fatal deny"}}},
	}}
	stream := runAgentLoop(
		context.Background(),
		ProviderResolution{Provider: "fake", Model: "fake-model", Adapter: adapter},
		[]Message{{Role: "user", Content: "push"}},
		t.TempDir(),
		normalizeGenerateOptions(
			WithRetryBaseDelay(0),
			WithMaxTurns(5),
			WithToolExecutor(func(_ context.Context, _ string, call ToolCall) (ToolRun, error) {
				// Non-fatal deny: blocked, but the loop should keep going.
				return ToolRun{ID: call.ID, Name: call.Function.Name, Output: "Permission denied.", Status: "denied"}, nil
			}),
		),
	)
	events := collectEvents(stream)
	if eventTypeCount(events, "tool_result") != 1 {
		t.Fatalf("expected the denied tool_result, got %#v", eventTypes(events))
	}
	if adapter.chatCalls != 2 {
		t.Fatalf("non-fatal deny must let the loop continue to the next turn, chatCalls=%d", adapter.chatCalls)
	}
	if events[len(events)-1].Type != "done" {
		t.Fatalf("non-fatal deny run should complete with done, got %#v", eventTypes(events))
	}
	for _, ev := range events {
		if ev.Type == "error" {
			t.Fatalf("non-fatal deny must not emit an error event, got %#v", eventTypes(events))
		}
	}
}
