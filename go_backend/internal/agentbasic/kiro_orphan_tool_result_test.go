package agentbasic

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestKiroRequestPreservesOrphanToolResults guards the history-building bug where
// an interrupted/continued turn (user, assistant+toolUse, tool result — with the
// tool result trailing as the current message's context) produced a Kiro request
// whose currentMessage had empty content AND no toolResults, while the assistant
// kept a dangling toolUse. The model then saw a degenerate conversation and
// replied as if the user "sent just a period".
//
// Root cause was readArray() not handling the concrete []map[string]any that
// kiroToolUses/FormatTools produce, so sanitizeKiroToolPairs could not match the
// assistant toolUses against the current toolResults and dropped both. This test
// drives the real BuildHistory → buildKiroChatRequest → normalize path and asserts
// the tool result survives, paired with its toolUse.
func TestKiroRequestPreservesOrphanToolResults(t *testing.T) {
	hist := []Message{
		{Role: "user", Content: "从go_backend删除所有nkp相关代码"},
		func() Message {
			m := Message{Role: "assistant", Content: "exploring the project"}
			m.ToolCalls = []ToolCall{newFunctionToolCall("tooluse_ABC", "Bash", `{"command":"ls -la"}`)}
			return m
		}(),
		{Role: "tool", ToolCallID: "tooluse_ABC", Content: "drwxr-xr-x go_backend"},
	}
	adapter := &KiroProviderAdapter{Provider: Provider{Model: "claude-sonnet-4.5"}}
	built, err := adapter.BuildHistory(hist, "claude-sonnet-4.5", "")
	if err != nil {
		t.Fatalf("BuildHistory: %v", err)
	}
	adapter.InjectSystemPrompt(&built, "SYSTEM PROMPT", "claude-sonnet-4.5", "")
	req := buildKiroChatRequest("claude-sonnet-4.5", ChatParams{
		Model:   "claude-sonnet-4.5",
		History: &built,
		Tools:   []map[string]any{{"toolSpecification": map[string]any{"name": "Bash"}}},
		Cwd:     "/tmp",
	})

	raw, _ := json.Marshal(req)
	body := string(raw)

	// The interrupted tool result must reach the model (in the current message's
	// userInputMessageContext.toolResults), not be silently dropped.
	if !strings.Contains(body, "tooluse_ABC") {
		t.Fatalf("tool use/result id missing from kiro request: %s", body)
	}
	if !strings.Contains(body, "toolResults") {
		t.Fatalf("kiro request lost toolResults entirely: %s", body)
	}

	// Structurally verify: current message carries the toolResults for tooluse_ABC.
	state, _ := req["conversationState"].(map[string]any)
	current, _ := state["currentMessage"].(map[string]any)
	user, _ := current["userInputMessage"].(map[string]any)
	ctx, _ := user["userInputMessageContext"].(map[string]any)
	results := readArray(ctx["toolResults"])
	if len(results) == 0 {
		t.Fatalf("current message has no toolResults: %#v", ctx)
	}
	first, _ := results[0].(map[string]any)
	if first["toolUseId"] != "tooluse_ABC" {
		t.Fatalf("toolResults not paired with the assistant toolUse: %#v", results)
	}
}

// TestReadArrayHandlesTypedMapSlice locks the readArray fix directly.
func TestReadArrayHandlesTypedMapSlice(t *testing.T) {
	typed := []map[string]any{{"toolUseId": "x"}, {"toolUseId": "y"}}
	got := readArray(typed)
	if len(got) != 2 {
		t.Fatalf("readArray([]map[string]any) = %d items, want 2", len(got))
	}
	if m, ok := got[0].(map[string]any); !ok || m["toolUseId"] != "x" {
		t.Fatalf("readArray did not preserve typed map slice element: %#v", got[0])
	}
}
