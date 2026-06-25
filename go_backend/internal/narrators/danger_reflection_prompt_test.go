package narrators

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"narrafork-go/internal/agentbasic"
)

// TestDangerReflectionPromptTextMatchesTS exercises the helper directly across
// locales, mirroring TS prompts.dangerReflection (prompt-i18n.ts:331-400) as
// assembled by buildDangerReflectionPrompt (loop.ts:696-714). It verifies the
// static template text, placeholder substitution (severity/level/summary), the
// consequences/alternatives bullet lists, the optional Details section, and the
// Go-inlined history-less compensation section.
func TestDangerReflectionPromptTextMatchesTS(t *testing.T) {
	info := danger(
		"Git push may rewrite or delete remote history.",
		[]string{"Remote commits or branches can be overwritten or removed for collaborators."},
		[]string{"Prefer a normal push."},
		[]string{"Command: git push --force origin main"},
		dangerSeverityCritical,
	)
	en := dangerReflectionPrompt("danger-1", "Bash", "{\n  \"command\": \"git push --force\"\n}", info, dangerReflectionStrict, "USER: do it", "en")
	for _, want := range []string{
		"Danger reflection pause: a risky tool call is pending and has NOT executed yet.",
		"Pending request ID: danger-1",
		"Original tool: Bash",
		"Detected risk:",
		"- Severity: critical",
		"- Reflection level: Strict",
		"- Git push may rewrite or delete remote history.",
		"Details:\n- Command: git push --force origin main",
		"Possible consequences:\n- Remote commits or branches can be overwritten or removed for collaborators.",
		"Safer alternatives to consider:\n- Prefer a normal push.",
		"Level-specific review policy:",
		"Current level: Strict. Even low-risk operations may pause, so be conservative but not mechanical.",
		"You have exactly one response, and you MUST call exactly one tool. Do not answer with plain text only.",
		`<DangerDecision>{"action":"confirm","reflection":"..."}</DangerDecision>`,
		"Do not call the original tool from this reflection loop.",
		// Go-inlined history-less compensation section.
		"Recent conversation and tool-result context:",
		"USER: do it",
	} {
		if !strings.Contains(en, want) {
			t.Fatalf("en danger reflection prompt missing %q:\n%s", want, en)
		}
	}

	zh := dangerReflectionPrompt("danger-2", "Bash", "{}", info, dangerReflectionLight, "", "zh-CN")
	for _, want := range []string{
		"危险反思暂停：一个风险工具调用正在等待确认，尚未执行。",
		"待确认请求 ID：danger-2",
		"原始工具：Bash",
		"检测到的风险：",
		"- 风险等级：critical",
		"- 反思档位：宽松",
		"详情：\n- Command: git push --force origin main",
		"可能后果：",
		"可考虑的更安全替代方案：",
		"当前档位审查策略：",
		"当前为宽松档：通常只会拦截高风险或关键风险。",
		"你只有一次回复机会，并且必须且只能调用一个工具；不要只输出普通文本。",
		"不要在这个反思 loop 中调用原始工具。",
	} {
		if !strings.Contains(zh, want) {
			t.Fatalf("zh danger reflection prompt missing %q:\n%s", want, zh)
		}
	}
	// Empty history falls back to the no-context placeholder, not a blank line.
	if !strings.Contains(zh, "(no prior conversation context available)") {
		t.Fatalf("zh danger reflection prompt should fall back to no-context placeholder:\n%s", zh)
	}
}

// TestDangerReflectionPromptOmitsDetailsWhenEmpty verifies that, like TS
// formatDangerDetails, an empty Details slice renders no Details section and the
// consequences/alternatives still fall back to "- (none)" bullets.
func TestDangerReflectionPromptOmitsDetailsWhenEmpty(t *testing.T) {
	info := danger("Something risky.", nil, nil, nil, dangerSeverityMedium)
	en := dangerReflectionPrompt("d-3", "Bash", "{}", info, dangerReflectionStandard, "", "en")
	if strings.Contains(en, "Details:") {
		t.Fatalf("empty details must not render a Details section:\n%s", en)
	}
	if !strings.Contains(en, "Possible consequences:\n- (none)") {
		t.Fatalf("empty consequences should render - (none):\n%s", en)
	}
	if !strings.Contains(en, "Safer alternatives to consider:\n- (none)") {
		t.Fatalf("empty alternatives should render - (none):\n%s", en)
	}
	if !strings.Contains(en, "- Reflection level: Medium") {
		t.Fatalf("standard level should render Medium label:\n%s", en)
	}
	zh := dangerReflectionPrompt("d-3", "Bash", "{}", info, dangerReflectionStandard, "", "zh-CN")
	if strings.Contains(zh, "详情：") {
		t.Fatalf("empty details must not render a zh Details section:\n%s", zh)
	}
	if !strings.Contains(zh, "- 反思档位：中等") {
		t.Fatalf("standard level should render 中等 label:\n%s", zh)
	}
}

// TestClassifyDangerStructuredFieldsMatchTS pins the structured DangerInfo fields
// (consequences/saferAlternatives/details) that the new prompt port depends on,
// mirroring the TS classifyGitDanger / classifyShellDanger danger() calls.
func TestClassifyDangerStructuredFieldsMatchTS(t *testing.T) {
	cwd := t.TempDir()

	// git push --force -> critical, with remote-history consequences.
	push, ok := classifyDangerReflectionToolCall(cwd, bashCall("git push --force origin main"), false)
	if !ok || push.Severity != dangerSeverityCritical {
		t.Fatalf("git push --force should be critical, got ok=%v sev=%s", ok, push.Severity)
	}
	if len(push.Consequences) == 0 || !strings.Contains(push.Consequences[0], "Remote commits or branches can be overwritten") {
		t.Fatalf("git push danger missing TS consequences: %#v", push.Consequences)
	}
	if len(push.SaferAlternatives) == 0 || push.SaferAlternatives[0] != "Prefer a normal push." {
		t.Fatalf("git push danger missing TS alternatives: %#v", push.SaferAlternatives)
	}
	if len(push.Details) == 0 || !strings.HasPrefix(push.Details[0], "Command: ") {
		t.Fatalf("git push danger missing Command detail: %#v", push.Details)
	}

	// rm -rf -> high, recursive deletion summary + consequences.
	rm, ok := classifyDangerReflectionToolCall(cwd, bashCall("rm -rf build"), false)
	if !ok || rm.Severity != dangerSeverityHigh {
		t.Fatalf("rm -rf should be high, got ok=%v sev=%s", ok, rm.Severity)
	}
	if !strings.Contains(rm.Summary, "deletes files recursively") {
		t.Fatalf("rm -rf summary should note recursive deletion, got %q", rm.Summary)
	}
	if len(rm.Consequences) == 0 || !strings.Contains(rm.Consequences[0], "Deleted files may not be recoverable") {
		t.Fatalf("rm danger missing TS consequences: %#v", rm.Consequences)
	}
}

// TestParseDangerReflectionTextFallbackMatchesTS mirrors TS
// parseDangerReflectionTextFallback (loop.ts:1085-1112): a valid
// <DangerDecision> tag yields the matching confirm/cancel decision; missing or
// invalid tags yield no decision.
func TestParseDangerReflectionTextFallbackMatchesTS(t *testing.T) {
	confirm, ok := parseDangerReflectionTextFallback(`prefix <DangerDecision>{"action":"confirm","reflection":"looks fine"}</DangerDecision> suffix`)
	if !ok || confirm.Action != "confirm" || confirm.Reflection != "looks fine" {
		t.Fatalf("confirm fallback parse failed: ok=%v %#v", ok, confirm)
	}
	cancel, ok := parseDangerReflectionTextFallback(`<dangerdecision>{"action":"cancel","reason":"too risky"}</dangerdecision>`)
	if !ok || cancel.Action != "cancel" || cancel.Feedback != "too risky" {
		t.Fatalf("cancel fallback parse (case-insensitive) failed: ok=%v %#v", ok, cancel)
	}
	// Cancel with no reason falls back to the default feedback.
	cancelNoReason, ok := parseDangerReflectionTextFallback(`<DangerDecision>{"action":"cancel"}</DangerDecision>`)
	if !ok || cancelNoReason.Action != "cancel" || cancelNoReason.Feedback != "Danger reflection rejected this tool call." {
		t.Fatalf("cancel-no-reason fallback should use default feedback: ok=%v %#v", ok, cancelNoReason)
	}
	if _, ok := parseDangerReflectionTextFallback("no tag here"); ok {
		t.Fatal("text without a DangerDecision tag must not parse")
	}
	if _, ok := parseDangerReflectionTextFallback(`<DangerDecision>not json</DangerDecision>`); ok {
		t.Fatal("invalid JSON inside the tag must not parse")
	}
	if _, ok := parseDangerReflectionTextFallback(`<DangerDecision>{"action":"maybe"}</DangerDecision>`); ok {
		t.Fatal("unknown action must not parse")
	}
}

func bashCall(command string) agentbasic.ToolCall {
	args, _ := json.Marshal(map[string]any{"command": command})
	var call agentbasic.ToolCall
	call.ID = "bash-test"
	call.Type = "function"
	call.Function.Name = "Bash"
	call.Function.Arguments = string(args)
	return call
}

// dangerReflectionPromptCaptureServer drives the bypass-mode danger reflection
// loop, recording the user-prompt text of the DangerCancel reflection turn so a
// test can assert the TS-aligned prompt is used.
func dangerReflectionPromptCaptureServer(t *testing.T, capture *string, mu *sync.Mutex) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		if requestHasTool(body, "DangerCancel") {
			mu.Lock()
			*capture = firstUserMessageText(body)
			mu.Unlock()
			if requestHasToolResult(body) {
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "danger reflection done"}}}})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": "danger-cancel-1", "type": "function", "function": map[string]any{"name": "DangerCancel", "arguments": `{"confirm":true,"feedback":"Do not run this command."}`}}}}}}})
			return
		}
		if requestHasToolResult(body) {
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "danger cancelled"}}}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": "danger-bash-1", "type": "function", "function": map[string]any{"name": "Bash", "arguments": `{"command":"git push --force origin main"}`}}}}}}})
	}))
	t.Cleanup(server.Close)
	return server
}

// TestRunBasicAgentDangerReflectionUsesTSPromptText verifies the danger
// reflection loop is driven by the new TS-aligned prompt (severity/level/
// consequences/alternatives) using the owner's zh-CN locale, rather than the old
// minimal "Review this potentially dangerous tool call" prompt.
func TestRunBasicAgentDangerReflectionUsesTSPromptText(t *testing.T) {
	var captured string
	var mu sync.Mutex
	server := dangerReflectionPromptCaptureServer(t, &captured, &mu)
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", server.URL)
	if _, err := service.DB.Exec(`CREATE TABLE user_preferences (user_id TEXT PRIMARY KEY, language TEXT)`); err != nil {
		t.Fatalf("create user_preferences: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := service.DB.Exec(`INSERT INTO narrator_messages (id, narrator_id, role, parent_tool_use_id, created_by, created_at) VALUES ('owner-msg', ?, 'user', NULL, 'user-zh', ?)`, narratorID, now); err != nil {
		t.Fatalf("insert owner message: %v", err)
	}
	if _, err := service.DB.Exec(`INSERT INTO user_preferences (user_id, language) VALUES ('user-zh', 'zh-CN')`); err != nil {
		t.Fatalf("insert pref: %v", err)
	}
	if _, err := service.DB.Exec(`UPDATE narrators SET permission_mode = 'bypassPermissions', danger_reflection_override = 'on' WHERE id = ?`, narratorID); err != nil {
		t.Fatalf("enable bypass danger reflection: %v", err)
	}
	service.runBasicAgent(context.Background(), narratorID)

	mu.Lock()
	prompt := captured
	mu.Unlock()
	if prompt == "" {
		t.Fatal("danger reflection turn prompt was not captured")
	}
	for _, want := range []string{
		"危险反思暂停：一个风险工具调用正在等待确认，尚未执行。",
		"检测到的风险：",
		"- 风险等级：critical",
		"当前档位审查策略：",
		"Git push may rewrite or delete remote history.",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("danger reflection prompt missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "Review this potentially dangerous tool call") {
		t.Fatalf("danger reflection should no longer use the old minimal prompt:\n%s", prompt)
	}
}
