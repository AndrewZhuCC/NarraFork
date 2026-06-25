package narrators

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"narrafork-go/internal/ws"
)

// TestRunBasicAgentSilentDisconnectPreservesBlocksAndFailsRunningTools is the
// end-to-end regression for the Kiro/streaming silent-disconnect bug reported by
// the user: after a tool call streamed and the provider stream dropped without a
// terminal marker, the frontend replies vanished, the narrator went idle, and a
// reload showed tool calls stuck "executing".
//
// It drives the real runBasicAgent loop with an OpenAI-compatible streaming mock
// that emits reasoning + text + a tool_call delta and then closes the SSE stream
// WITHOUT a `[DONE]` marker, which makes the chat-completions parser emit
// silent_disconnect. The test then asserts the persisted DB state:
//   - the assistant message keeps its reasoning + text blocks (flushed via
//     flushTurnBlocks on the terminal path) alongside the tool_use block, instead
//     of dropping the streamed content;
//   - the in-flight tool call is finalized as `fail` instead of staying `running`
//     forever (markInterruptedToolCalls on the silent-disconnect finalizer).
func TestRunBasicAgentSilentDisconnectPreservesBlocksAndFailsRunningTools(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		write := func(chunk string) {
			_, _ = w.Write([]byte(chunk))
			if flusher != nil {
				flusher.Flush()
			}
		}
		// Reasoning delta, then visible text, then a tool call — then close the
		// stream WITHOUT `data: [DONE]` to simulate a provider silent disconnect
		// after content already streamed. The finish_reason: "tool_calls" chunk
		// finalizes the tool call (so it is recorded as running, exactly like the
		// real Kiro scenario), but the missing [DONE] still triggers silent_disconnect.
		write("data: {\"choices\":[{\"index\":0,\"delta\":{\"reasoning_content\":\"thinking hard\"}}]}\n\n")
		write("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Working on it\"}}]}\n\n")
		write("data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_bash\",\"type\":\"function\",\"function\":{\"name\":\"Bash\",\"arguments\":\"{\\\"command\\\":\\\"ls\\\"}\"}}]}}]}\n\n")
		write("data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n")
		// no [DONE] — stream just ends after the tool call is finalized.
	}))
	defer server.Close()

	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", server.URL)
	service.Hub = ws.NewHub()
	// Bypass permissions so the streamed Bash tool call is recorded as "running"
	// (the real failing run used "全部允许"/bypass), exercising the orphaned-tool
	// cleanup on the silent-disconnect finalizer.
	if _, err := service.DB.Exec(`UPDATE narrators SET permission_mode = 'bypassPermissions' WHERE id = ?`, narratorID); err != nil {
		t.Fatalf("set permission mode: %v", err)
	}
	service.runBasicAgent(context.Background(), narratorID)

	// Assistant blocks must include the streamed reasoning + text (not just the
	// tool_use block), proving the terminal-path flush persisted partial content.
	blocks := latestAssistantBlocks(t, service, narratorID)
	var hasText, hasReasoning, hasToolUse bool
	for _, b := range blocks {
		switch b["type"] {
		case "text":
			if stringFromAny(b["text"]) == "Working on it" {
				hasText = true
			}
		case "reasoning":
			if stringFromAny(b["text"]) == "thinking hard" {
				hasReasoning = true
			}
		case "tool_use":
			hasToolUse = true
		}
	}
	if !hasText {
		t.Fatalf("assistant message lost streamed text after silent disconnect: %#v", blocks)
	}
	if !hasReasoning {
		t.Fatalf("assistant message lost streamed reasoning after silent disconnect: %#v", blocks)
	}
	if !hasToolUse {
		t.Fatalf("assistant message lost the tool_use block after silent disconnect: %#v", blocks)
	}

	// The in-flight tool call must NOT be left running — it should be failed.
	var status string
	if err := service.DB.QueryRow(`SELECT status FROM narrator_tool_calls WHERE narrator_id = ? AND tool_use_id = 'call_bash'`, narratorID).Scan(&status); err != nil {
		t.Fatalf("load tool call status: %v", err)
	}
	if status == "running" || status == "pending" || status == "initializing" {
		t.Fatalf("tool call left in non-terminal status %q after silent disconnect (should be failed)", status)
	}
	if status != "fail" {
		t.Fatalf("expected tool call status 'fail' after silent disconnect, got %q", status)
	}

	// The narrator must finish idle (not stuck working) with the silent_disconnect substatus.
	var narratorStatus, substatus string
	if err := service.DB.QueryRow(`SELECT status, substatus FROM narrators WHERE id = ?`, narratorID).Scan(&narratorStatus, &substatus); err != nil {
		t.Fatalf("load narrator status: %v", err)
	}
	if narratorStatus != "idle" {
		t.Fatalf("narrator should be idle after silent disconnect, got %q", narratorStatus)
	}
	if substatus != `["silent_disconnect"]` {
		t.Fatalf("expected silent_disconnect substatus, got %q", substatus)
	}
}
