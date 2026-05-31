package narrators

import (
	"context"
	"strings"
	"testing"
	"time"

	"narrafork-go/internal/id"
)

func TestRecallSearchIsSelfScopedAndBounded(t *testing.T) {
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", "http://127.0.0.1")
	ctx := context.Background()
	selfMsg, err := service.addMessage(ctx, narratorID, "assistant", "needle self result", nil)
	if err != nil {
		t.Fatalf("insert self message: %v", err)
	}
	foreignID := id.New()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := service.DB.Exec(`INSERT INTO narrators (id, type, title, inherit_mode, model, permission_mode, status, substatus, plan_mode, cwd, variant, traits, is_background, is_ask_in_passing, message_version, message_count, total_cost_usd, fast_mode, relaxed_plan, plan_reflection_auto_approve_override, danger_reflection_override, created_at, updated_at) VALUES (?, 'primary', 'Foreign narrator', 'fresh', 'openai:gpt-test', 'default', 'idle', '[]', 0, '', 'primary', '[]', 0, 0, 0, 0, 0, 0, 0, 'inherit', 'inherit', ?, ?)`, foreignID, now, now); err != nil {
		t.Fatalf("insert foreign narrator: %v", err)
	}
	foreignMsg, err := service.addMessage(ctx, foreignID, "assistant", "needle foreign secret", nil)
	if err != nil {
		t.Fatalf("insert foreign message: %v", err)
	}
	if _, err := service.addMessage(ctx, narratorID, "assistant", "needle second result", nil); err != nil {
		t.Fatalf("insert second self message: %v", err)
	}

	out, status, err := service.executeRecallTool(ctx, narratorID, map[string]any{"action": "search", "query": "needle", "limit": 1})
	if err != nil {
		t.Fatalf("recall search: %v", err)
	}
	if status != "" {
		t.Fatalf("unexpected status %q: %s", status, out)
	}
	if !strings.Contains(out, "Found 1 result") || !strings.Contains(out, narratorID) {
		t.Fatalf("expected one self-scoped search result, got %q", out)
	}
	if strings.Contains(out, foreignID) || strings.Contains(out, foreignMsg.ID) || strings.Contains(out, "foreign secret") {
		t.Fatalf("search crossed narrator scope: %q", out)
	}
	if strings.Contains(out, selfMsg.ID) && strings.Count(out, "message ") > 1 {
		t.Fatalf("search limit was not bounded: %q", out)
	}
}

func TestRecallReadConversationIncludesToolSummaries(t *testing.T) {
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", "http://127.0.0.1")
	ctx := context.Background()
	assistant, err := service.addMessage(ctx, narratorID, "assistant", "I read the file.", nil)
	if err != nil {
		t.Fatalf("insert assistant message: %v", err)
	}
	if _, err := service.DB.Exec(`INSERT INTO narrator_tool_calls (id, narrator_id, message_id, tool_use_id, tool_name, input_json, output_json, status, created_at) VALUES (?, ?, ?, 'read-self-1', 'Read', '{"file_path":"docs/README.md"}', '{"output":"file body"}', 'success', ?)`, id.New(), narratorID, assistant.ID, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert tool call: %v", err)
	}

	out, status, err := service.executeRecallTool(ctx, narratorID, map[string]any{"action": "read_conversation", "message_id": assistant.ID, "limit": 5})
	if err != nil {
		t.Fatalf("recall read_conversation: %v", err)
	}
	if status != "" {
		t.Fatalf("unexpected status %q: %s", status, out)
	}
	if !strings.Contains(out, "I read the file") || !strings.Contains(out, "toolUseId=read-self-1") || !strings.Contains(out, "README.md") {
		t.Fatalf("conversation output missing message/tool summary: %q", out)
	}

	out, status, err = service.executeRecallTool(ctx, narratorID, map[string]any{"action": "read_conversation", "narrator_id": "other-narrator"})
	if err != nil {
		t.Fatalf("cross-scope recall: %v", err)
	}
	if status != "error" || !strings.Contains(out, "scoped to the current narrator") {
		t.Fatalf("cross-scope read_conversation should be denied, status=%q out=%q", status, out)
	}
}

func TestRecallReadToolCallIsSelfScopedAndTruncated(t *testing.T) {
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", "http://127.0.0.1")
	ctx := context.Background()
	assistant, err := service.addMessage(ctx, narratorID, "assistant", "Tool output available.", nil)
	if err != nil {
		t.Fatalf("insert assistant message: %v", err)
	}
	bigOutput := strings.Repeat("x", 4500)
	if _, err := service.DB.Exec(`INSERT INTO narrator_tool_calls (id, narrator_id, message_id, tool_use_id, tool_name, input_json, output_json, status, created_at) VALUES (?, ?, ?, 'bash-self-1', 'Bash', '{"command":"printf x"}', ?, 'success', ?)`, id.New(), narratorID, assistant.ID, `{"output":"`+bigOutput+`"}`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert self tool call: %v", err)
	}
	foreignID := id.New()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := service.DB.Exec(`INSERT INTO narrators (id, type, title, inherit_mode, model, permission_mode, status, substatus, plan_mode, cwd, variant, traits, is_background, is_ask_in_passing, message_version, message_count, total_cost_usd, fast_mode, relaxed_plan, plan_reflection_auto_approve_override, danger_reflection_override, created_at, updated_at) VALUES (?, 'primary', 'Foreign narrator', 'fresh', 'openai:gpt-test', 'default', 'idle', '[]', 0, '', 'primary', '[]', 0, 0, 0, 0, 0, 0, 0, 'inherit', 'inherit', ?, ?)`, foreignID, now, now); err != nil {
		t.Fatalf("insert foreign narrator: %v", err)
	}
	foreignMsg, err := service.addMessage(ctx, foreignID, "assistant", "foreign tool", nil)
	if err != nil {
		t.Fatalf("insert foreign message: %v", err)
	}
	if _, err := service.DB.Exec(`INSERT INTO narrator_tool_calls (id, narrator_id, message_id, tool_use_id, tool_name, input_json, output_json, status, created_at) VALUES (?, ?, ?, 'bash-foreign-1', 'Bash', '{"command":"secret"}', '{"output":"secret"}', 'success', ?)`, id.New(), foreignID, foreignMsg.ID, now); err != nil {
		t.Fatalf("insert foreign tool call: %v", err)
	}

	out, status, err := service.executeRecallTool(ctx, narratorID, map[string]any{"action": "read_tool_call", "tool_call_id": "bash-self-1"})
	if err != nil {
		t.Fatalf("read own tool call: %v", err)
	}
	if status != "" || !strings.Contains(out, "printf x") || !strings.Contains(out, "truncated") {
		t.Fatalf("own tool call should include input and truncated output, status=%q out=%q", status, out)
	}
	out, status, err = service.executeRecallTool(ctx, narratorID, map[string]any{"action": "read_tool_call", "tool_call_id": "bash-foreign-1"})
	if err != nil {
		t.Fatalf("read foreign tool call: %v", err)
	}
	if status != "error" || !strings.Contains(out, "not found in the current narrator") || strings.Contains(out, "secret") {
		t.Fatalf("foreign tool call should be hidden, status=%q out=%q", status, out)
	}
}
