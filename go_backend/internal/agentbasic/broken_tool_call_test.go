package agentbasic

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestBrokenToolCallDetection covers the isBrokenToolCall classifier that mirrors
// the TS tool-executor broken detection (tool-executor.ts:212-249): malformed /
// truncated JSON is broken, and a file-writing tool with empty input (complete
// truncation) is broken. Well-formed input and empty input for non-file tools is
// not broken.
func TestBrokenToolCallDetection(t *testing.T) {
	cases := []struct {
		name string
		tool string
		args string
		want bool
	}{
		{"truncated json edit", "Edit", `{"file_path":"a.txt","new_string":"abc`, true},
		{"truncated json write", "Write", `{"file_path":"a.txt","content":"`, true},
		{"valid edit", "Edit", `{"file_path":"a.txt","old_string":"x","new_string":"y"}`, false},
		{"valid read", "Read", `{"file_path":"a.txt"}`, false},
		{"empty write is complete truncation", "Write", `{}`, true},
		{"empty edit is complete truncation", "Edit", ``, true},
		{"empty read is fine", "Read", `{}`, false},
		{"json array is malformed object", "Read", `[1,2,3]`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isBrokenToolCall(tc.tool, tc.args); got != tc.want {
				t.Fatalf("isBrokenToolCall(%q, %q) = %v, want %v", tc.tool, tc.args, got, tc.want)
			}
		})
	}
}

// TestBrokenToolCallSanitizeInput pins sanitizeBrokenInput parity with TS
// (tool-executor.ts:450-495): _raw-only input extracts file_path and stubs
// content fields; normal input keeps structural fields and replaces large
// content fields with the placeholder.
func TestBrokenToolCallSanitizeInput(t *testing.T) {
	placeholder := brokenToolInputPlaceholder("en")

	raw := sanitizeBrokenInput("Write", map[string]any{"_raw": `{"file_path":"big.txt","content":"aaaa`}, "en")
	if raw["file_path"] != "big.txt" {
		t.Fatalf("expected file_path extracted from _raw, got %#v", raw)
	}
	if raw["content"] != placeholder {
		t.Fatalf("expected content placeholder, got %#v", raw)
	}

	rawEdit := sanitizeBrokenInput("Edit", map[string]any{"_raw": `{"file_path":"e.txt"`}, "en")
	if rawEdit["old_string"] != placeholder || rawEdit["new_string"] != placeholder {
		t.Fatalf("expected edit placeholders, got %#v", rawEdit)
	}

	normal := sanitizeBrokenInput("Write", map[string]any{"file_path": "n.txt", "content": "huge"}, "en")
	if normal["file_path"] != "n.txt" || normal["content"] != placeholder {
		t.Fatalf("expected structural file_path kept + content replaced, got %#v", normal)
	}
}

// TestBrokenToolCallMessagesLocalized verifies the broken-tool i18n helpers carry
// both locales and that the reminder names the broken tool(s).
func TestBrokenToolCallMessagesLocalized(t *testing.T) {
	if !strings.Contains(brokenToolCallResultMessage("en"), "truncated by token limit") {
		t.Fatalf("en broken result message missing expected text: %q", brokenToolCallResultMessage("en"))
	}
	if !strings.Contains(brokenToolCallResultMessage("zh-CN"), "token 限制截断") {
		t.Fatalf("zh broken result message missing expected text: %q", brokenToolCallResultMessage("zh-CN"))
	}
	enReminder := brokenToolCallReminder("Write, Edit", "en")
	if !strings.Contains(enReminder, "Write, Edit") || !strings.Contains(enReminder, "SKELETON-FIRST") {
		t.Fatalf("en reminder missing tool names or skeleton guidance: %q", enReminder)
	}
	zhReminder := brokenToolCallReminder("Write", "zh-CN")
	if !strings.Contains(zhReminder, "Write") || !strings.Contains(zhReminder, "骨架优先") {
		t.Fatalf("zh reminder missing tool names or skeleton guidance: %q", zhReminder)
	}
}

// brokenRecordingAdapter records the tool uses pushed into each assistant turn so
// the broken-tool stripping can be asserted. It scripts a fixed sequence of
// provider turns like scriptedAdapter but is defined locally so the broken-tool
// test can inspect pushed assistant tool uses without touching shared helpers.
type brokenRecordingAdapter struct {
	turns             [][]StreamEvent
	chatCalls         int
	pushedAssistTools [][]ToolCall
	lastUserContent   string
}

func (a *brokenRecordingAdapter) ProviderName() string                { return "fake" }
func (a *brokenRecordingAdapter) FormatTools(string) []map[string]any { return nil }
func (a *brokenRecordingAdapter) BuildHistory(messages []Message, _ string, _ string) (BuiltHistory, error) {
	history := make([]any, 0, len(messages))
	for _, message := range messages {
		history = append(history, message)
	}
	return BuiltHistory{History: history}, nil
}
func (a *brokenRecordingAdapter) InjectSystemPrompt(*BuiltHistory, string, string, string) {}
func (a *brokenRecordingAdapter) Chat(ctx context.Context, _ ChatParams) (<-chan StreamEvent, error) {
	call := a.chatCalls
	a.chatCalls++
	ch := make(chan StreamEvent, 8)
	go func() {
		defer close(ch)
		if call >= len(a.turns) {
			return
		}
		for _, event := range a.turns[call] {
			select {
			case <-ctx.Done():
				return
			case ch <- event:
			}
		}
	}()
	return ch, nil
}
func (a *brokenRecordingAdapter) FormatToolResult(toolUseID string, output string, isError bool) any {
	return map[string]any{"type": "tool_result", "tool_use_id": toolUseID, "content": output, "is_error": isError}
}
func (a *brokenRecordingAdapter) PushUserTurn(history *BuiltHistory, content string, toolResults []any) {
	a.lastUserContent = content
	history.History = append(history.History, map[string]any{"role": "user", "content": toolResults, "text": content})
}
func (a *brokenRecordingAdapter) PushAssistantTurn(history *BuiltHistory, text string, toolUses []ToolCall, _ any) {
	a.pushedAssistTools = append(a.pushedAssistTools, append([]ToolCall(nil), toolUses...))
	history.History = append(history.History, map[string]any{"role": "assistant", "content": text, "toolUses": toolUses})
}

// TestBrokenToolCallNotExecutedAndReminderInjected is the end-to-end loop test:
// the tool executor flags a truncated/malformed tool call as broken (ToolRun.Broken,
// mirroring TS executeTool tool-executor.ts:213-249 where the executor returns
// `broken: true`). The loop must then exclude it from the model-facing tool
// results, strip the broken tool_use from the assistant turn pushed to the model,
// and inject the brokenToolCallReminder into the next user turn. Mirrors TS
// loop.ts:1895 (brokenToolUseIds from settled results) + loop.ts:3853-3885.
func TestBrokenToolCallNotExecutedAndReminderInjected(t *testing.T) {
	broken := ToolCall{ID: "broken-1", Type: "function"}
	broken.Function.Name = "Write"
	broken.Function.Arguments = `{"file_path":"big.txt","content":"aaaa` // truncated, no closing brace

	adapter := &brokenRecordingAdapter{turns: [][]StreamEvent{
		{{Parsed: ParsedStreamEvent{ToolUses: []ToolCall{broken}}}},
		{{Parsed: ParsedStreamEvent{Text: "recovered"}}},
	}}

	// The executor detects the truncated input (its arguments do not parse as a JSON
	// object → surfaced as a broken result) and returns ToolRun.Broken, exactly as
	// the narrator executor's guardedMalformedToolInputRun does in production.
	var executedArgs string
	stream := runAgentLoop(
		context.Background(),
		ProviderResolution{Provider: "fake", Model: "fake-model", Adapter: adapter},
		[]Message{{Role: "user", Content: "write a big file"}},
		t.TempDir(),
		normalizeGenerateOptions(
			WithRetryBaseDelay(0),
			WithMaxTurns(5),
			WithToolExecutor(func(_ context.Context, _ string, call ToolCall) (ToolRun, error) {
				executedArgs = call.Function.Arguments
				if !json.Valid([]byte(call.Function.Arguments)) {
					return ToolRun{ID: call.ID, Name: call.Function.Name, Status: "error", Output: brokenToolCallResultMessage("en"), Error: "broken", Broken: true}, nil
				}
				return ToolRun{Output: "ran"}, nil
			}),
		),
	)
	events := collectEvents(stream)

	// The executor was invoked (broken detection happens at execution time), but it
	// must have rejected the broken input rather than performing a real write.
	if executedArgs != broken.Function.Arguments {
		t.Fatalf("executor should have been called with the broken args, got %q", executedArgs)
	}

	// A broken tool_result event must be emitted carrying the broken result text.
	var brokenResult *AgentEvent
	for i := range events {
		if events[i].Type == "tool_result" && events[i].ToolUseID == "broken-1" {
			brokenResult = &events[i]
			break
		}
	}
	if brokenResult == nil {
		t.Fatalf("expected a broken tool_result event, got %#v", eventTypes(events))
	}
	if !strings.Contains(brokenResult.Output, "truncated by token limit") {
		t.Fatalf("broken tool_result must carry the broken result message, got %q", brokenResult.Output)
	}

	// The assistant turn pushed to the model must NOT include the broken tool_use.
	for _, pushed := range adapter.pushedAssistTools {
		for _, tu := range pushed {
			if tu.ID == "broken-1" {
				t.Fatalf("broken tool_use must be stripped from the assistant turn, got %#v", pushed)
			}
		}
	}

	// The next user turn must carry the brokenToolCallReminder naming the tool.
	if !strings.Contains(adapter.lastUserContent, "Write") || !strings.Contains(adapter.lastUserContent, "SKELETON-FIRST") {
		t.Fatalf("next user turn must carry broken reminder, got %q", adapter.lastUserContent)
	}

	// The loop must still finish (the second turn produced text).
	if eventTypeCount(events, "done") != 1 {
		t.Fatalf("loop must recover and finish after a broken tool, got %#v", eventTypes(events))
	}
}
