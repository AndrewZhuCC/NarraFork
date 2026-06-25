package narrators

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"narrafork-go/internal/ws"
)

// TestRunBasicAgentStopHookFiresOnCompletion verifies the Stop agent hook fires
// once when the narrator's response loop ends normally, carrying the TS-aligned
// Stop payload (hook_event_name=Stop, stop_reason=done, stop_error=false,
// last_assistant_text). Mirrors TS narrator-session.ts:2505-2548.
func TestRunBasicAgentStopHookFiresOnCompletion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "final answer text"}}}})
	}))
	defer server.Close()

	service, narratorID, cwd := newTestNarratorService(t, "openai:gpt-test", server.URL)
	service.Hub = ws.NewHub()
	if _, err := service.DB.Exec(`CREATE TABLE hooks (id TEXT PRIMARY KEY, project_id TEXT, event TEXT, matcher TEXT, type TEXT, command TEXT, url TEXT, headers TEXT, timeout INTEGER, enabled INTEGER, sort_order INTEGER, created_at TEXT)`); err != nil {
		t.Fatalf("create hooks table: %v", err)
	}
	hookOutput := filepath.Join(cwd, "stop-hook-payload.json")
	// Empty matcher → fires for any Stop (no tool scope). Capture stdin payload.
	command := fmt.Sprintf("cat > %q", hookOutput)
	if _, err := service.DB.Exec(`INSERT INTO hooks (id, event, matcher, type, command, timeout, enabled, sort_order, created_at) VALUES ('hook-stop', 'Stop', '', 'command', ?, 5, 1, 0, '2026-01-01T00:00:00Z')`, command); err != nil {
		t.Fatalf("insert stop hook: %v", err)
	}

	service.runBasicAgent(context.Background(), narratorID)
	waitForAssistantText(t, service, narratorID, "final answer text")
	waitForFileContains(t, hookOutput, `"hook_event_name":"Stop"`)

	payloadRaw, err := os.ReadFile(hookOutput)
	if err != nil {
		t.Fatalf("read stop hook payload: %v", err)
	}
	payload := map[string]any{}
	if err := json.Unmarshal(payloadRaw, &payload); err != nil {
		t.Fatalf("decode stop hook payload: %v raw=%s", err, string(payloadRaw))
	}
	if payload["hook_event_name"] != "Stop" {
		t.Fatalf("expected hook_event_name=Stop, got %#v", payload)
	}
	if payload["stop_reason"] != "done" {
		t.Fatalf("expected stop_reason=done for a clean completion, got %#v", payload["stop_reason"])
	}
	if payload["stop_error"] != false {
		t.Fatalf("expected stop_error=false for a clean completion, got %#v", payload["stop_error"])
	}
	if payload["narrator_id"] != narratorID {
		t.Fatalf("expected narrator_id=%s, got %#v", narratorID, payload["narrator_id"])
	}
	if got := stringFromAny(payload["last_assistant_text"]); got != "final answer text" {
		t.Fatalf("expected last_assistant_text to carry the final answer, got %q", got)
	}
}

// TestRunBasicAgentStopHookSkippedForSubagent verifies the Stop hook does NOT
// fire for subagent narrators, mirroring the TS isSubagentVariant guard
// (narrator-session.ts:2517) that restricts Stop hooks to the main narrator.
func TestRunBasicAgentStopHookSkippedForSubagent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "subagent answer"}}}})
	}))
	defer server.Close()

	service, narratorID, cwd := newTestNarratorService(t, "openai:gpt-test", server.URL)
	service.Hub = ws.NewHub()
	// Mark this narrator a subagent: Stop hooks must be skipped.
	if _, err := service.DB.Exec(`UPDATE narrators SET type = 'subagent', variant = 'subagent' WHERE id = ?`, narratorID); err != nil {
		t.Fatalf("mark subagent: %v", err)
	}
	if _, err := service.DB.Exec(`CREATE TABLE hooks (id TEXT PRIMARY KEY, project_id TEXT, event TEXT, matcher TEXT, type TEXT, command TEXT, url TEXT, headers TEXT, timeout INTEGER, enabled INTEGER, sort_order INTEGER, created_at TEXT)`); err != nil {
		t.Fatalf("create hooks table: %v", err)
	}
	hookOutput := filepath.Join(cwd, "stop-hook-subagent.json")
	command := fmt.Sprintf("cat > %q", hookOutput)
	if _, err := service.DB.Exec(`INSERT INTO hooks (id, event, matcher, type, command, timeout, enabled, sort_order, created_at) VALUES ('hook-stop-sa', 'Stop', '', 'command', ?, 5, 1, 0, '2026-01-01T00:00:00Z')`, command); err != nil {
		t.Fatalf("insert stop hook: %v", err)
	}

	service.runBasicAgent(context.Background(), narratorID)
	waitForAssistantText(t, service, narratorID, "subagent answer")

	// The Stop hook must not have produced its payload file for a subagent.
	if _, err := os.Stat(hookOutput); !os.IsNotExist(err) {
		t.Fatalf("Stop hook must not fire for a subagent narrator (payload file exists, stat err=%v)", err)
	}
}
