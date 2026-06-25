package agentbasic

import (
	"context"
	"strings"
	"testing"
)

func TestTurnNudgeReminderFormat(t *testing.T) {
	out := turnNudgeReminder(5, 5, "en")
	if !strings.Contains(out, "You have used 5 of 5 turns") {
		t.Fatalf("turnNudgeReminder missing turn counts: %q", out)
	}
	if !strings.Contains(out, "wrap up") {
		t.Fatalf("turnNudgeReminder missing wrap-up guidance: %q", out)
	}
}

// resultContent extracts the model-facing content string from a scriptedAdapter
// formatted tool result (map{"content": ...}).
func resultContent(t *testing.T, result any) string {
	t.Helper()
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("tool result is not a map: %#v", result)
	}
	s, _ := m["content"].(string)
	return s
}

func TestAgentLoopAppendsTurnNudgeNearMaxTurns(t *testing.T) {
	// maxTurns=5 → nudge threshold floor(5*0.8)=4. Drive a tool call every turn so
	// the loop reaches turn index 4 (the last allowed turn) and pushes a nudged
	// tool result before exiting with the max-rounds error.
	turns := make([][]StreamEvent, 5)
	for i := range turns {
		call := newFunctionToolCall("read-"+strings.Repeat("x", i+1), "Read", `{"file_path":"f.txt"}`)
		turns[i] = []StreamEvent{{Parsed: ParsedStreamEvent{ToolUses: []ToolCall{call}}}}
	}
	adapter := &scriptedAdapter{turns: turns}
	stream := runAgentLoop(
		context.Background(),
		ProviderResolution{Provider: "fake", Model: "fake-model", Adapter: adapter},
		[]Message{{Role: "user", Content: "go"}},
		t.TempDir(),
		normalizeGenerateOptions(
			WithRetryBaseDelay(0),
			WithMaxTurns(5),
			WithToolExecutor(func(ctx context.Context, cwd string, call ToolCall) (ToolRun, error) {
				return ToolRun{ID: call.ID, Name: call.Function.Name, Output: "ok", Status: "completed"}, nil
			}),
		),
	)
	collectEvents(stream)
	if len(adapter.lastToolResult) != 1 {
		t.Fatalf("expected one tool result on the last turn, got %#v", adapter.lastToolResult)
	}
	content := resultContent(t, adapter.lastToolResult[0])
	if !strings.Contains(content, "of 5 turns") {
		t.Fatalf("near-max turn tool result should carry the turn nudge, got: %q", content)
	}
}

func TestAgentLoopNoTurnNudgeEarly(t *testing.T) {
	// maxTurns=10 → threshold floor(10*0.8)=8. The model calls a tool on turn 0
	// then returns text on turn 1, so the loop ends at turn 1 with the turn-0 tool
	// result (turn 0 < 8 → no nudge).
	call := newFunctionToolCall("read-1", "Read", `{"file_path":"f.txt"}`)
	adapter := &scriptedAdapter{turns: [][]StreamEvent{
		{{Parsed: ParsedStreamEvent{ToolUses: []ToolCall{call}}}},
		{{Parsed: ParsedStreamEvent{Text: "done"}}},
	}}
	stream := runAgentLoop(
		context.Background(),
		ProviderResolution{Provider: "fake", Model: "fake-model", Adapter: adapter},
		[]Message{{Role: "user", Content: "go"}},
		t.TempDir(),
		normalizeGenerateOptions(
			WithRetryBaseDelay(0),
			WithMaxTurns(10),
			WithToolExecutor(func(ctx context.Context, cwd string, call ToolCall) (ToolRun, error) {
				return ToolRun{ID: call.ID, Name: call.Function.Name, Output: "ok", Status: "completed"}, nil
			}),
		),
	)
	collectEvents(stream)
	if len(adapter.lastToolResult) != 1 {
		t.Fatalf("expected one tool result, got %#v", adapter.lastToolResult)
	}
	if content := resultContent(t, adapter.lastToolResult[0]); strings.Contains(content, "turns. Please wrap up") {
		t.Fatalf("early turn must not carry the turn nudge, got: %q", content)
	}
}

func TestAgentLoopTurnNudgeOnlyLastTool(t *testing.T) {
	// maxTurns=2 → threshold floor(2*0.8)=1. turn 0 advances via a single tool;
	// turn 1 issues two parallel Read tools (same group). Only the LAST tool of the
	// turn should carry the nudge.
	single := newFunctionToolCall("read-single", "Read", `{"file_path":"a.txt"}`)
	callA := newFunctionToolCall("read-A", "Read", `{"file_path":"a.txt"}`)
	callB := newFunctionToolCall("read-B", "Read", `{"file_path":"b.txt"}`)
	adapter := &scriptedAdapter{turns: [][]StreamEvent{
		{{Parsed: ParsedStreamEvent{ToolUses: []ToolCall{single}}}},
		{{Parsed: ParsedStreamEvent{ToolUses: []ToolCall{callA, callB}}}},
	}}
	stream := runAgentLoop(
		context.Background(),
		ProviderResolution{Provider: "fake", Model: "fake-model", Adapter: adapter},
		[]Message{{Role: "user", Content: "go"}},
		t.TempDir(),
		normalizeGenerateOptions(
			WithRetryBaseDelay(0),
			WithMaxTurns(2),
			WithToolExecutor(func(ctx context.Context, cwd string, call ToolCall) (ToolRun, error) {
				return ToolRun{ID: call.ID, Name: call.Function.Name, Output: "out-" + call.ID, Status: "completed"}, nil
			}),
		),
	)
	collectEvents(stream)
	if len(adapter.lastToolResult) != 2 {
		t.Fatalf("expected two tool results on the parallel turn, got %#v", adapter.lastToolResult)
	}
	var aContent, bContent string
	for _, r := range adapter.lastToolResult {
		c := resultContent(t, r)
		if strings.Contains(c, "out-read-A") {
			aContent = c
		}
		if strings.Contains(c, "out-read-B") {
			bContent = c
		}
	}
	if aContent == "" || bContent == "" {
		t.Fatalf("could not locate both parallel tool results: %#v", adapter.lastToolResult)
	}
	if strings.Contains(aContent, "of 2 turns") {
		t.Fatalf("non-last parallel tool must not carry the nudge, got: %q", aContent)
	}
	if !strings.Contains(bContent, "of 2 turns") {
		t.Fatalf("last parallel tool should carry the nudge, got: %q", bContent)
	}
}
