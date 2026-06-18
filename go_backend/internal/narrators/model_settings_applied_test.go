package narrators

import (
	"context"
	"testing"

	"narrafork-go/internal/agentbasic"
)

// TestModelSwitchedAlsoBroadcastsModelSettingsApplied verifies that a
// model_switched agent event produces BOTH the raw model_switched frame and the
// model_settings_applied companion frame, mirroring the TS dual broadcast.
func TestModelSwitchedAlsoBroadcastsModelSettingsApplied(t *testing.T) {
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", "http://127.0.0.1")
	hub, events := captureNarratorBroadcasts()
	service.Hub = hub
	narrator, err := service.Get(context.Background(), narratorID)
	if err != nil {
		t.Fatalf("load narrator: %v", err)
	}
	state := &agentRunState{narratorID: narratorID, narrator: narrator}

	event := agentbasic.AgentEvent{
		Type:     "model_switched",
		Provider: "anthropic",
		Model:    "claude-x",
		Metadata: map[string]any{"reasoningEffort": "high"},
		ModelSwitched: &agentbasic.ModelSwitchedEvent{
			Provider:        "anthropic",
			Model:           "claude-x",
			ReasoningEffort: "high",
			Cause:           "fallback",
		},
	}
	if err := service.handleAgentEvent(context.Background(), state, event); err != nil {
		t.Fatalf("handle model_switched: %v", err)
	}

	if findBroadcast(events(), narratorID, "model_switched") == nil {
		t.Fatalf("expected raw model_switched broadcast, got %#v", events())
	}
	applied := findBroadcast(events(), narratorID, "model_settings_applied")
	if applied == nil {
		t.Fatalf("expected model_settings_applied companion broadcast, got %#v", events())
	}
	if applied["model"] != "claude-x" || applied["provider"] != "anthropic" {
		t.Fatalf("unexpected model_settings_applied model/provider: %#v", applied)
	}
	if applied["reasoningEffort"] != "high" {
		t.Fatalf("expected reasoningEffort=high, got %#v", applied["reasoningEffort"])
	}
}

// TestModelSettingsAppliedOmitsEmptyReasoningEffort verifies the optional
// reasoningEffort field is omitted when not present (matching the TS optional
// type).
func TestModelSettingsAppliedOmitsEmptyReasoningEffort(t *testing.T) {
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", "http://127.0.0.1")
	hub, events := captureNarratorBroadcasts()
	service.Hub = hub
	narrator, err := service.Get(context.Background(), narratorID)
	if err != nil {
		t.Fatalf("load narrator: %v", err)
	}
	state := &agentRunState{narratorID: narratorID, narrator: narrator}

	event := agentbasic.AgentEvent{
		Type:          "model_switched",
		Provider:      "openai",
		Model:         "gpt-y",
		ModelSwitched: &agentbasic.ModelSwitchedEvent{Provider: "openai", Model: "gpt-y"},
	}
	if err := service.handleAgentEvent(context.Background(), state, event); err != nil {
		t.Fatalf("handle model_switched: %v", err)
	}

	applied := findBroadcast(events(), narratorID, "model_settings_applied")
	if applied == nil {
		t.Fatalf("expected model_settings_applied broadcast, got %#v", events())
	}
	if _, ok := applied["reasoningEffort"]; ok {
		t.Fatalf("reasoningEffort must be omitted when empty, got %#v", applied)
	}
	if applied["model"] != "gpt-y" || applied["provider"] != "openai" {
		t.Fatalf("unexpected model/provider: %#v", applied)
	}
}
