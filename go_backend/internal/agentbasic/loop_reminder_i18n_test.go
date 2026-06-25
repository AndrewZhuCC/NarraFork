package agentbasic

import (
	"context"
	"strings"
	"testing"
)

// TestTurnNudgeReminderLocalized exercises the helper directly across locales,
// mirroring TS tool.turnNudge (i18n.ts:191-195). Note the en/zh separator
// difference ("5 of 5 turns" vs "5/5 轮") is verbatim from TS.
func TestTurnNudgeReminderLocalized(t *testing.T) {
	en := turnNudgeReminder(5, 5, "en")
	if !strings.Contains(en, "You have used 5 of 5 turns") || !strings.Contains(en, "Please wrap up your work soon") {
		t.Fatalf("en turn nudge text mismatch: %q", en)
	}
	zh := turnNudgeReminder(5, 5, "zh-CN")
	if !strings.Contains(zh, "你已使用 5/5 轮") || !strings.Contains(zh, "请尽快收尾") {
		t.Fatalf("zh turn nudge text mismatch: %q", zh)
	}
	if strings.Contains(zh, "You have used") {
		t.Fatalf("zh turn nudge must not contain english text: %q", zh)
	}
	// Unknown locale falls back to en.
	if turnNudgeReminder(1, 2, "fr") != turnNudgeReminder(1, 2, "en") {
		t.Fatalf("unknown locale should fall back to en")
	}
}

// TestSilentProgressReminderLocalized exercises the helper directly across
// locales, mirroring TS tool.silentToolCallProgressReminder (i18n.ts:196-203).
func TestSilentProgressReminderLocalized(t *testing.T) {
	en := silentProgressReminder(3, "en")
	for _, want := range []string{
		"<progress_update_request>",
		"You have completed 3 tool call(s) since your last visible text reply.",
		"</progress_update_request>",
	} {
		if !strings.Contains(en, want) {
			t.Fatalf("en silent progress missing %q: %q", want, en)
		}
	}
	zh := silentProgressReminder(3, "zh-CN")
	if !strings.Contains(zh, "你已经连续 3 次工具调用没有向用户输出可见文本。") {
		t.Fatalf("zh silent progress text mismatch: %q", zh)
	}
	if strings.Contains(zh, "You have completed") {
		t.Fatalf("zh silent progress must not contain english text: %q", zh)
	}
}

// TestAgentLoopTurnNudgeUsesLocale verifies the loop threads WithLocale into the
// turn nudge appended to the last tool result.
func TestAgentLoopTurnNudgeUsesLocale(t *testing.T) {
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
			WithLocale("zh-CN"),
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
	if !strings.Contains(content, "5/5 轮") {
		t.Fatalf("near-max turn tool result should carry the localized turn nudge, got: %q", content)
	}
	if strings.Contains(content, "of 5 turns") {
		t.Fatalf("zh-CN turn nudge must not contain english text, got: %q", content)
	}
}

// TestAgentLoopSilentProgressUsesLocale verifies the loop threads WithLocale into
// the silent-progress sidecar.
func TestAgentLoopSilentProgressUsesLocale(t *testing.T) {
	calls := []ToolCall{
		newFunctionToolCall("silent-1", "Read", `{"file_path":"a"}`),
		newFunctionToolCall("silent-2", "Read", `{"file_path":"b"}`),
	}
	adapter := &scriptedAdapter{turns: [][]StreamEvent{
		{{Parsed: ParsedStreamEvent{ToolUses: calls}}},
		{{Parsed: ParsedStreamEvent{Text: "visible"}}},
	}}
	stream := runAgentLoop(
		context.Background(),
		ProviderResolution{Provider: "fake", Model: "fake-model", Adapter: adapter},
		[]Message{{Role: "user", Content: "use tools"}},
		t.TempDir(),
		normalizeGenerateOptions(
			WithRetryBaseDelay(0),
			WithSilentToolCallThreshold(2),
			WithLocale("zh-CN"),
			WithToolExecutor(func(ctx context.Context, cwd string, call ToolCall) (ToolRun, error) {
				return ToolRun{ID: call.ID, Name: call.Function.Name, Output: "output " + call.ID, Status: "completed"}, nil
			}),
		),
	)
	collectEvents(stream)
	second, _ := adapter.lastToolResult[1].(map[string]any)
	content, _ := second["content"].(string)
	if !strings.Contains(content, "你已经连续 2 次工具调用没有向用户输出可见文本。") {
		t.Fatalf("expected localized silent progress sidecar, got %#v", second)
	}
	if strings.Contains(content, "You have completed") {
		t.Fatalf("zh-CN silent progress must not contain english text, got %#v", second)
	}
}
