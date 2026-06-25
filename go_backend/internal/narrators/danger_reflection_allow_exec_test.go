package narrators

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDangerReflectionAllowExecutesToolAndPersistsResult reproduces the reported
// issue where, after danger reflection CONFIRMS (allow) in bypass mode, the Bash
// tool appeared to not return a result. It drives the full bypass-mode flow with
// an OpenAI-compatible mock and asserts:
//   - danger_reflection_started is broadcast (with requestId + danger),
//   - danger_reflection_resolved allow is broadcast,
//   - the Bash tool actually executes and its real output is persisted with
//     success status (not stuck running / not empty / not failed).
func TestDangerReflectionAllowExecutesToolAndPersistsResult(t *testing.T) {
	hub, events := captureNarratorBroadcasts()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		// Reflection turn (advertises DangerConfirm): approve.
		if requestHasTool(body, "DangerConfirm") {
			if requestHasToolResult(body) {
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "reflection ok"}}}})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": "dc-1", "type": "function", "function": map[string]any{"name": "DangerConfirm", "arguments": `{"confirm":true,"reflection":"matches the task"}`}}}}}}})
			return
		}
		// Parent turn after the tool result: finish with text.
		if requestHasToolResult(body) {
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "done reading"}}}})
			return
		}
		// First parent turn: emit a Bash tool call. `cat` produces real output;
		// the trailing unknown command forces danger reflection to trigger (it is
		// outside the safety allowlist), while the shell still runs the cat.
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": "bash-1", "type": "function", "function": map[string]any{"name": "Bash", "arguments": `{"command":"cat marker.txt; some_unknown_cmd_xyz 2>/dev/null || true"}`}}}}}}})
	}))
	defer server.Close()

	service, narratorID, cwd := newTestNarratorService(t, "openai:gpt-test", server.URL)
	service.Hub = hub
	// A real file so the Bash command yields concrete, non-empty output.
	if err := os.WriteFile(filepath.Join(cwd, "marker.txt"), []byte("HELLO_FROM_BASH"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	if _, err := service.DB.Exec(`UPDATE narrators SET permission_mode = 'bypassPermissions', danger_reflection_override = 'on' WHERE id = ?`, narratorID); err != nil {
		t.Fatalf("set mode: %v", err)
	}

	service.runBasicAgent(context.Background(), narratorID)

	// Broadcast ordering: started (with requestId+danger) then resolved allow.
	started := findBroadcast(events(), narratorID, "danger_reflection_started")
	if started == nil {
		t.Fatalf("expected danger_reflection_started broadcast, got: %#v", events())
	}
	if started["requestId"] == nil || started["danger"] == nil {
		t.Fatalf("danger_reflection_started missing requestId/danger: %#v", started)
	}
	resolved := findBroadcast(events(), narratorID, "danger_reflection_resolved")
	if resolved == nil || resolved["decision"] != "allow" {
		t.Fatalf("expected danger_reflection_resolved allow, got: %#v", resolved)
	}

	// The Bash tool must have executed and persisted its real output as success.
	var status, outputRaw string
	if err := service.DB.QueryRow(`SELECT status, COALESCE(output_json,'') FROM narrator_tool_calls WHERE narrator_id = ? AND tool_use_id = 'bash-1'`, narratorID).Scan(&status, &outputRaw); err != nil {
		t.Fatalf("load bash tool call: %v", err)
	}
	if status == "running" || status == "pending" || status == "initializing" {
		t.Fatalf("bash tool stuck in non-terminal status %q after reflection allow", status)
	}
	if status != "success" {
		t.Fatalf("expected bash success after reflection allow, got %q (output=%s)", status, outputRaw)
	}
	if !strings.Contains(outputRaw, "HELLO_FROM_BASH") {
		t.Fatalf("bash output not persisted after reflection allow: %s", outputRaw)
	}

	// The danger reflection state must be durably persisted on the tool-call row
	// (permission_suggestions), so a refetch/catch-up rebuilds the "confirmed"
	// reflection card even if the live started frame was missed/raced.
	var suggestions string
	if err := service.DB.QueryRow(`SELECT COALESCE(permission_suggestions,'') FROM narrator_tool_calls WHERE narrator_id = ? AND tool_use_id = 'bash-1'`, narratorID).Scan(&suggestions); err != nil {
		t.Fatalf("load permission_suggestions: %v", err)
	}
	if !strings.Contains(suggestions, "danger_reflection") || !strings.Contains(suggestions, "confirmed") {
		t.Fatalf("expected persisted confirmed danger_reflection suggestion, got: %s", suggestions)
	}
}
