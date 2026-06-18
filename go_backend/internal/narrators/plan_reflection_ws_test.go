package narrators

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// exitPlanReflectionMockServer returns an OpenAI-compatible mock that drives the
// ExitPlanMode plan-reflection loop: the reflection turn responds with the given
// reflection tool call (ExitPlanConfirm or ExitPlanRevise), and any other turn
// emits an ExitPlanMode tool call (entering plan approval) or a plain message.
func exitPlanReflectionMockServer(t *testing.T, reflectionToolName, reflectionArgs string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		// Reflection turn: the request offers the reflection decision tools.
		if requestHasTool(body, "ExitPlanConfirm") {
			if requestHasToolResult(body) {
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "plan reflection done"}}}})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": "reflect-1", "type": "function", "function": map[string]any{"name": reflectionToolName, "arguments": reflectionArgs}}}}}}})
			return
		}
		if requestHasToolResult(body) {
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "ok"}}}})
			return
		}
		plan := "Implement the plan-reflection WS events, broadcast started/resolved, add focused tests, and validate the target Go packages."
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": "exit-plan-1", "type": "function", "function": map[string]any{"name": "ExitPlanMode", "arguments": `{"plan":"` + plan + `"}`}}}}}}})
	}))
	t.Cleanup(server.Close)
	return server
}

// TestRunBasicAgentExitPlanReflectionBroadcastsStartedAndResolvedAllow verifies
// that the ExitPlanMode plan-reflection emits plan_reflection_started and a
// plan_reflection_resolved(allow) WS event, mirroring TS exit-plan-reflection.ts
// so the frontend can render reflection progress on the ExitPlanMode tool card.
func TestRunBasicAgentExitPlanReflectionBroadcastsStartedAndResolvedAllow(t *testing.T) {
	server := exitPlanReflectionMockServer(t, "ExitPlanConfirm", `{"confirm":true,"reflection":"plan is actionable"}`)
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", server.URL)
	hub, drain := captureNarratorBroadcasts()
	service.Hub = hub
	if _, err := service.DB.Exec(`UPDATE narrators SET plan_mode = 1, traits = '["plan"]' WHERE id = ?`, narratorID); err != nil {
		t.Fatalf("enter plan mode: %v", err)
	}
	go service.runBasicAgent(context.Background(), narratorID)
	waitForToolStatus(t, service, narratorID, "exit-plan-1", "pending")

	started, resolved := findPlanReflectionEvents(drain())
	if started == nil {
		t.Fatal("expected a plan_reflection_started broadcast")
	}
	if started.Payload["toolUseId"] != "exit-plan-1" {
		t.Fatalf("started.toolUseId = %v, want exit-plan-1", started.Payload["toolUseId"])
	}
	if started.Payload["toolName"] != "ExitPlanMode" {
		t.Fatalf("started.toolName = %v, want ExitPlanMode", started.Payload["toolName"])
	}
	if rid, _ := started.Payload["requestId"].(string); !strings.HasPrefix(rid, "plan_reflection_") {
		t.Fatalf("started.requestId = %v, want plan_reflection_* prefix", started.Payload["requestId"])
	}
	if resolved == nil {
		t.Fatal("expected a plan_reflection_resolved broadcast")
	}
	if resolved.Payload["decision"] != "allow" {
		t.Fatalf("resolved.decision = %v, want allow", resolved.Payload["decision"])
	}
}

// TestRunBasicAgentExitPlanReflectionBroadcastsResolvedDenyOnRevise verifies a
// revise decision surfaces as plan_reflection_resolved(deny) with feedback.
func TestRunBasicAgentExitPlanReflectionBroadcastsResolvedDenyOnRevise(t *testing.T) {
	server := exitPlanReflectionMockServer(t, "ExitPlanRevise", `{"confirm":true,"feedback":"add concrete test cases"}`)
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", server.URL)
	hub, drain := captureNarratorBroadcasts()
	service.Hub = hub
	if _, err := service.DB.Exec(`UPDATE narrators SET plan_mode = 1, traits = '["plan"]' WHERE id = ?`, narratorID); err != nil {
		t.Fatalf("enter plan mode: %v", err)
	}
	go service.runBasicAgent(context.Background(), narratorID)
	// Revise denies the ExitPlanMode tool call; the captured run (status "denied")
	// is persisted to the tool-call row as "fail".
	waitForToolStatus(t, service, narratorID, "exit-plan-1", "fail")

	started, resolved := findPlanReflectionEvents(drain())
	if started == nil {
		t.Fatal("expected a plan_reflection_started broadcast")
	}
	if resolved == nil {
		t.Fatal("expected a plan_reflection_resolved broadcast")
	}
	if resolved.Payload["decision"] != "deny" {
		t.Fatalf("resolved.decision = %v, want deny", resolved.Payload["decision"])
	}
	if reason, _ := resolved.Payload["reason"].(string); !strings.Contains(reason, "concrete test cases") {
		t.Fatalf("resolved.reason = %q, want revise feedback", resolved.Payload["reason"])
	}
}

func findPlanReflectionEvents(events []capturedBroadcast) (started, resolved *capturedBroadcast) {
	for i := range events {
		switch events[i].Payload["type"] {
		case "plan_reflection_started":
			started = &events[i]
		case "plan_reflection_resolved":
			resolved = &events[i]
		}
	}
	return started, resolved
}
