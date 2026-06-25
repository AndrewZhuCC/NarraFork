package narrators

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// TestExitPlanReflectionPromptTextMatchesTS exercises the helper directly across
// locales, mirroring TS prompts.exitPlanReflection (prompt-i18n.ts:235-273). The
// exitPlanReflectionAutoCompact suffix is intentionally not ported (Go has no
// ExitPlanConfirmAndCompact), so neither locale may mention it.
func TestExitPlanReflectionPromptTextMatchesTS(t *testing.T) {
	en := exitPlanReflectionPrompt("plan_reflection_x", "{\n  \"plan\": \"do work\"\n}", "do work", "- Bash: run the tests", "en")
	for _, want := range []string{
		"ExitPlanMode reflection: a plan is about to be submitted to the user for approval.",
		"Pending reflection ID: plan_reflection_x",
		"Original ExitPlanMode input:",
		"Resolved plan:\ndo work",
		"Prompt-based permissions requested:\n- Bash: run the tests",
		"You have exactly one response, and you MUST call exactly one allowed tool:",
		"ExitPlanConfirm if the plan is ready to present to the user for approval.",
		"Do not call ExitPlanMode from this reflection loop.",
	} {
		if !strings.Contains(en, want) {
			t.Fatalf("en reflection prompt missing %q:\n%s", want, en)
		}
	}
	if strings.Contains(en, "ExitPlanConfirmAndCompact") {
		t.Fatalf("en reflection prompt must not port the autoCompact suffix:\n%s", en)
	}

	zh := exitPlanReflectionPrompt("plan_reflection_x", "{}", "做这件事", "- (none)", "zh-CN")
	for _, want := range []string{
		"ExitPlanMode 反思：一个计划即将提交给用户审批。",
		"待反思 ID：plan_reflection_x",
		"已解析计划：\n做这件事",
		"你只有一次回复机会，并且必须且只能调用一个允许的工具：",
		"不要在这个反思 loop 中调用 ExitPlanMode。",
	} {
		if !strings.Contains(zh, want) {
			t.Fatalf("zh reflection prompt missing %q:\n%s", want, zh)
		}
	}
	if strings.Contains(zh, "ExitPlanConfirmAndCompact") {
		t.Fatalf("zh reflection prompt must not port the autoCompact suffix:\n%s", zh)
	}
}

// TestFormatExitPlanAllowedPromptsList mirrors TS formatAllowedPrompts: empty /
// non-array -> "- (none)", entries with a non-empty prompt render "- [tool: ]prompt",
// empty-prompt entries are skipped.
func TestFormatExitPlanAllowedPromptsList(t *testing.T) {
	if got := formatExitPlanAllowedPromptsList(nil); got != "- (none)" {
		t.Fatalf("nil -> %q, want - (none)", got)
	}
	if got := formatExitPlanAllowedPromptsList([]any{}); got != "- (none)" {
		t.Fatalf("empty []any -> %q, want - (none)", got)
	}
	if got := formatExitPlanAllowedPromptsList([]map[string]any{{"tool": "Bash", "prompt": "run tests"}}); got != "- Bash: run tests" {
		t.Fatalf("single entry -> %q, want - Bash: run tests", got)
	}
	if got := formatExitPlanAllowedPromptsList([]map[string]any{{"prompt": "no tool"}}); got != "- no tool" {
		t.Fatalf("no-tool entry -> %q, want - no tool", got)
	}
	// Entry with empty prompt is skipped; if all are skipped -> "- (none)".
	if got := formatExitPlanAllowedPromptsList([]map[string]any{{"tool": "Bash", "prompt": ""}}); got != "- (none)" {
		t.Fatalf("empty-prompt-only -> %q, want - (none)", got)
	}
	// Mixed: skip empty, keep non-empty.
	mixed := formatExitPlanAllowedPromptsList([]any{
		map[string]any{"tool": "Bash", "prompt": ""},
		map[string]any{"tool": "Bash", "prompt": "build"},
	})
	if mixed != "- Bash: build" {
		t.Fatalf("mixed -> %q, want - Bash: build", mixed)
	}
}

// exitPlanReflectionPromptCaptureServer drives the ExitPlanMode plan-reflection
// loop like exitPlanReflectionMockServer, but additionally records the user
// prompt text of the reflection turn so a test can assert the TS prompt is used.
func exitPlanReflectionPromptCaptureServer(t *testing.T, capture *string, mu *sync.Mutex) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		if requestHasTool(body, "ExitPlanConfirm") {
			// Reflection turn: record the user-message prompt text.
			mu.Lock()
			*capture = firstUserMessageText(body)
			mu.Unlock()
			if requestHasToolResult(body) {
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "plan reflection done"}}}})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": "reflect-1", "type": "function", "function": map[string]any{"name": "ExitPlanConfirm", "arguments": `{"confirm":true,"reflection":"ok"}`}}}}}}})
			return
		}
		if requestHasToolResult(body) {
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "ok"}}}})
			return
		}
		plan := "Implement the exit-plan reflection prompt alignment with the TS template, add focused helper and end-to-end tests, and validate the target Go packages."
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": "exit-plan-1", "type": "function", "function": map[string]any{"name": "ExitPlanMode", "arguments": `{"plan":"` + plan + `"}`}}}}}}})
	}))
	t.Cleanup(server.Close)
	return server
}

// firstUserMessageText returns the concatenated text content of the first user
// message in an OpenAI-style request body.
func firstUserMessageText(body map[string]any) string {
	messages, _ := body["messages"].([]any)
	for _, raw := range messages {
		msg, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if stringFromAny(msg["role"]) != "user" {
			continue
		}
		switch content := msg["content"].(type) {
		case string:
			return content
		case []any:
			parts := []string{}
			for _, c := range content {
				if cm, ok := c.(map[string]any); ok {
					parts = append(parts, stringFromAny(cm["text"]))
				}
			}
			return strings.Join(parts, "")
		}
	}
	return ""
}

// TestRunBasicAgentExitPlanReflectionUsesTSPromptText verifies the reflection
// loop is driven by the new TS-aligned prompt text rather than the old Go
// single-paragraph English prompt.
func TestRunBasicAgentExitPlanReflectionUsesTSPromptText(t *testing.T) {
	var captured string
	var mu sync.Mutex
	server := exitPlanReflectionPromptCaptureServer(t, &captured, &mu)
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", server.URL)
	if _, err := service.DB.Exec(`UPDATE narrators SET plan_mode = 1, traits = '["plan"]' WHERE id = ?`, narratorID); err != nil {
		t.Fatalf("enter plan mode: %v", err)
	}
	go service.runBasicAgent(context.Background(), narratorID)
	waitForToolStatus(t, service, narratorID, "exit-plan-1", "pending")

	mu.Lock()
	prompt := captured
	mu.Unlock()
	if prompt == "" {
		t.Fatal("reflection turn prompt was not captured")
	}
	for _, want := range []string{
		"ExitPlanMode reflection: a plan is about to be submitted to the user for approval.",
		"Pending reflection ID: plan_reflection_",
		"Do not call ExitPlanMode from this reflection loop.",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("reflection prompt missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "Review this ExitPlanMode plan before it is submitted") {
		t.Fatalf("reflection prompt still uses the old Go single-paragraph text:\n%s", prompt)
	}
}
