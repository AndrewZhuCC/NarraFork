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

// TestDangerReflectionLoopReceivesParentHistory verifies the danger reflection
// loop is seeded with the parent conversation history as real provider messages
// (mirroring TS runReflectionLoop → agentLoop(prompt, [...history])), not just a
// text summary inlined into the prompt. Without the real history, the reflection
// model misjudged well-formed tool calls as having "no input/evidence" and
// cancelled them. This asserts the original user message appears as a message in
// the reflection request body.
func TestDangerReflectionLoopReceivesParentHistory(t *testing.T) {
	var mu sync.Mutex
	reflectionSawUserMessage := false

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")

		// The danger reflection request is the one that advertises DangerCancel.
		if requestHasTool(body, "DangerCancel") {
			// Inspect the reflection request's messages for the parent user turn.
			mu.Lock()
			if messagesContainUserText(body, "Please help.") {
				reflectionSawUserMessage = true
			}
			mu.Unlock()
			// Cancel so the reflection resolves to deny and the parent turn ends
			// without waiting on a user permission decision (default mode Bash would
			// otherwise pause for approval after an allow).
			if requestHasToolResult(body) {
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "reflection done"}}}})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": "dc-1", "type": "function", "function": map[string]any{"name": "DangerCancel", "arguments": `{"confirm":true,"feedback":"not justified"}`}}}}}}})
			return
		}
		if requestHasToolResult(body) {
			// Parent turn after the (denied) Bash: finish with plain text.
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "all done"}}}})
			return
		}
		// First parent turn: emit a dangerous Bash tool call.
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": "bash-1", "type": "function", "function": map[string]any{"name": "Bash", "arguments": `{"command":"rm -rf made-by-test"}`}}}}}}})
	}))
	defer server.Close()

	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", server.URL)
	// Danger reflection is the bypass-allow gate: it fires only when permissions are
	// already opened (bypassPermissions) AND the auto-running command is dangerous
	// (narrator-permission.ts:2497). Use bypassPermissions so the dangerous Bash is
	// auto-allowed and then routed into danger reflection (instead of pausing for an
	// interactive permission decision, which would never resolve in this test).
	if _, err := service.DB.Exec(`UPDATE narrators SET permission_mode = 'bypassPermissions', danger_reflection_override = 'on' WHERE id = ?`, narratorID); err != nil {
		t.Fatalf("enable danger reflection: %v", err)
	}
	service.runBasicAgent(context.Background(), narratorID)

	mu.Lock()
	defer mu.Unlock()
	if !reflectionSawUserMessage {
		t.Fatalf("danger reflection request did not include the parent user message as real history")
	}
}

// messagesContainUserText reports whether any user-role message in the request
// body contains the given text (handles both string and content-array shapes).
func messagesContainUserText(body map[string]any, text string) bool {
	messages, _ := body["messages"].([]any)
	for _, item := range messages {
		msg, _ := item.(map[string]any)
		if msg["role"] != "user" {
			continue
		}
		switch content := msg["content"].(type) {
		case string:
			if strings.Contains(content, text) {
				return true
			}
		case []any:
			for _, part := range content {
				p, _ := part.(map[string]any)
				if strings.Contains(stringFromAny(p["text"]), text) {
					return true
				}
			}
		}
	}
	return false
}
