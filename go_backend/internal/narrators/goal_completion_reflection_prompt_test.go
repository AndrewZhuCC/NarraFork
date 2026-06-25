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

// TestGoalCompletionReflectionPromptTextMatchesTS exercises the helper directly
// across locales, mirroring TS prompts.goalCompletionReflection
// (prompt-i18n.ts:283-329). The appended Go-inlined sections (history / evidence
// / reflection) are the history-less reflection-loop compensation, so the prompt
// must include both the verbatim TS body and the inlined context.
func TestGoalCompletionReflectionPromptTextMatchesTS(t *testing.T) {
	en := goalCompletionReflectionPrompt("goal_complete_x", "{\n  \"status\": \"complete\"\n}", "{\n  \"objective\": \"ship it\"\n}", "[\n  {\"objective\": \"ship it\"}\n]", "USER: did the thing", "tests passed", "looks done", "en")
	for _, want := range []string{
		"Goal-completion reflection: the narrator is attempting to mark a user-set goal complete.",
		"Pending reflection ID: goal_complete_x",
		"Original UpdateGoal input:",
		"Active goal:",
		"Open goal list:",
		"Audit the active goal against the conversation and tool-result evidence available in this reflection loop.",
		"You have exactly one response, and you MUST call exactly one tool:",
		"GoalCompleteConfirm if the active goal is actually achieved. Include objective-specific evidence.",
		"Do not call UpdateGoal from this reflection loop.",
		// Go-inlined compensation sections.
		"Recent conversation and tool-result context:",
		"USER: did the thing",
		"Agent-provided completion evidence, if any:",
		"tests passed",
		"Agent reflection, if any:",
		"looks done",
	} {
		if !strings.Contains(en, want) {
			t.Fatalf("en goal reflection prompt missing %q:\n%s", want, en)
		}
	}

	zh := goalCompletionReflectionPrompt("goal_complete_x", "{}", "{}", "[]", "", "", "", "zh-CN")
	for _, want := range []string{
		"目标完成反思：叙述者正在尝试把一个用户设定的目标标记为完成。",
		"待反思 ID：goal_complete_x",
		"开放目标列表：",
		"你只有一次回复机会，并且必须且只能调用一个工具：",
		"不要在这个 reflection loop 中调用 UpdateGoal。",
	} {
		if !strings.Contains(zh, want) {
			t.Fatalf("zh goal reflection prompt missing %q:\n%s", want, zh)
		}
	}
	// Empty inputs fall back to placeholders, not blank sections.
	if !strings.Contains(zh, "(no prior conversation context available)") {
		t.Fatalf("zh goal reflection prompt should fall back to no-context placeholder:\n%s", zh)
	}
	if !strings.Contains(zh, "(none)") {
		t.Fatalf("zh goal reflection prompt should fall back to (none) for empty evidence/reflection:\n%s", zh)
	}
}

// TestGoalCompletionReflectionPromptIncludesActiveGoalAndGoalsJSON verifies the
// {activeGoalJson} and {goalsJson} placeholders are both substituted (the old Go
// prompt only inlined the objective string and had no open goal list).
func TestGoalCompletionReflectionPromptIncludesActiveGoalAndGoalsJSON(t *testing.T) {
	activeGoal := `{"id":"g-active","objective":"ACTIVE_GOAL_MARKER"}`
	goalsList := `[{"id":"g-active","objective":"ACTIVE_GOAL_MARKER"},{"id":"g-open","objective":"OPEN_GOAL_MARKER"}]`
	prompt := goalCompletionReflectionPrompt("goal_complete_y", "{}", activeGoal, goalsList, "ctx", "ev", "ref", "en")
	if !strings.Contains(prompt, "ACTIVE_GOAL_MARKER") {
		t.Fatalf("prompt missing active goal JSON marker:\n%s", prompt)
	}
	if !strings.Contains(prompt, "OPEN_GOAL_MARKER") {
		t.Fatalf("prompt missing open goal list marker:\n%s", prompt)
	}
}

// goalReflectionPromptCaptureServer drives the UpdateGoal -> goal-completion
// reflection loop like TestRunBasicAgentPlanAndGoalReflectionToolsWorkThroughEventLoop,
// recording the user prompt text of the reflection turn for assertion.
func goalReflectionPromptCaptureServer(t *testing.T, capture *string, mu *sync.Mutex) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		if requestHasTool(body, "GoalCompleteConfirm") {
			mu.Lock()
			*capture = firstUserMessageText(body)
			mu.Unlock()
			if requestHasToolResult(body) {
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "reflection done"}}}})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": "goal-confirm-1", "type": "function", "function": map[string]any{"name": "GoalCompleteConfirm", "arguments": `{"confirm":true,"evidence":"Verified the goal reflection prompt alignment is complete.","reflection":"safe"}`}}}}}}})
			return
		}
		if requestHasToolResult(body) {
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "goal updated"}}}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": "goal-update-1", "type": "function", "function": map[string]any{"name": "UpdateGoal", "arguments": `{"status":"complete"}`}}}}}}})
	}))
	t.Cleanup(server.Close)
	return server
}

// TestRunBasicAgentGoalReflectionUsesTSPromptText verifies the goal-completion
// reflection loop is driven by the new TS-aligned prompt (with the requestId line
// and open goal list) rather than the old Go prompt.
func TestRunBasicAgentGoalReflectionUsesTSPromptText(t *testing.T) {
	var captured string
	var mu sync.Mutex
	server := goalReflectionPromptCaptureServer(t, &captured, &mu)
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", server.URL)
	if _, err := service.addGoal(context.Background(), narratorID, "Complete the goal reflection prompt alignment", "tester"); err != nil {
		t.Fatalf("add goal: %v", err)
	}
	service.runBasicAgent(context.Background(), narratorID)

	mu.Lock()
	prompt := captured
	mu.Unlock()
	if prompt == "" {
		t.Fatal("goal reflection turn prompt was not captured")
	}
	for _, want := range []string{
		"Goal-completion reflection: the narrator is attempting to mark a user-set goal complete.",
		"Pending reflection ID: goal_complete_",
		"Open goal list:",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("goal reflection prompt missing %q:\n%s", want, prompt)
		}
	}
}
