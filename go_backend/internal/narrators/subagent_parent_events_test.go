package narrators

import (
	"context"
	"testing"
	"time"

	"narrafork-go/internal/id"
)

// insertTestSubagent creates a subagent narrator row whose parent is parentID.
func insertTestSubagent(t *testing.T, service *Service, parentID string) string {
	t.Helper()
	subID := id.New()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := service.DB.Exec(`INSERT INTO narrators (
		id, type, title, inherit_mode, model, permission_mode, status, substatus, plan_mode,
		cwd, variant, traits, is_background, is_ask_in_passing, message_version, message_count,
		total_cost_usd, fast_mode, relaxed_plan, plan_reflection_auto_approve_override,
		danger_reflection_override, parent_narrator_id, created_at, updated_at
	) VALUES (?, 'subagent', 'Sub', 'fresh', 'openai:gpt-test', 'default', 'working', '[]', 0, '/tmp', 'subagent', '[]', 0, 0, 0, 0, 0, 0, 0, 'inherit', 'inherit', ?, ?, ?)`,
		subID, parentID, now, now)
	if err != nil {
		t.Fatalf("insert subagent: %v", err)
	}
	return subID
}

func TestUpdateTodosBroadcastsSubagentTodosUpdatedToParent(t *testing.T) {
	service, parentID, _ := newTestNarratorService(t, "openai:gpt-test", "")
	hub, drain := captureNarratorBroadcasts()
	service.Hub = hub
	subID := insertTestSubagent(t, service, parentID)

	rawTodos := []any{map[string]any{"id": "t1", "content": "do work", "status": "in_progress", "priority": "high"}}
	if _, err := service.updateTodos(context.Background(), subID, "todo-tool-1", rawTodos); err != nil {
		t.Fatalf("updateTodos: %v", err)
	}

	events := drain()
	var ownTodos, parentTodos *capturedBroadcast
	for i := range events {
		switch events[i].Payload["type"] {
		case "todos_updated":
			if events[i].NarratorID == subID {
				ownTodos = &events[i]
			}
		case "subagent_todos_updated":
			parentTodos = &events[i]
		}
	}
	if ownTodos == nil {
		t.Fatal("expected todos_updated to the subagent's own subscribers")
	}
	if parentTodos == nil {
		t.Fatal("expected subagent_todos_updated to the parent narrator")
	}
	if parentTodos.NarratorID != parentID {
		t.Fatalf("subagent_todos_updated broadcast target = %q, want parent %q", parentTodos.NarratorID, parentID)
	}
	if parentTodos.Payload["subagentNarratorId"] != subID {
		t.Fatalf("subagentNarratorId = %v, want %q", parentTodos.Payload["subagentNarratorId"], subID)
	}
	if parentTodos.Payload["toolUseId"] != "todo-tool-1" {
		t.Fatalf("toolUseId = %v, want todo-tool-1", parentTodos.Payload["toolUseId"])
	}
	if parentTodos.Payload["narratorId"] != parentID {
		t.Fatalf("payload.narratorId = %v, want parent %q", parentTodos.Payload["narratorId"], parentID)
	}
}

func TestUpdateTodosPrimaryNarratorDoesNotBroadcastSubagentEvent(t *testing.T) {
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", "")
	hub, drain := captureNarratorBroadcasts()
	service.Hub = hub

	rawTodos := []any{map[string]any{"id": "t1", "content": "do work", "status": "pending", "priority": "low"}}
	if _, err := service.updateTodos(context.Background(), narratorID, "todo-tool-2", rawTodos); err != nil {
		t.Fatalf("updateTodos: %v", err)
	}

	for _, ev := range drain() {
		if ev.Payload["type"] == "subagent_todos_updated" {
			t.Fatal("primary narrator must not emit subagent_todos_updated")
		}
	}
}

func TestUpdateSubagentConclusionBroadcastsToParent(t *testing.T) {
	service, parentID, _ := newTestNarratorService(t, "openai:gpt-test", "")
	hub, drain := captureNarratorBroadcasts()
	service.Hub = hub
	subID := insertTestSubagent(t, service, parentID)

	// A parent tool_use row that spawned the subagent, plus the subagent's first
	// user message linking back to that tool_use (used to resolve the parent
	// tool_use id), plus the subagent's final assistant message (conclusion output).
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := service.DB.Exec(`INSERT INTO narrator_tool_calls (id, narrator_id, tool_use_id, tool_name, status, created_at) VALUES (?, ?, 'task-tool-1', 'Task', 'running', ?)`, id.New(), parentID, now); err != nil {
		t.Fatalf("insert parent tool call: %v", err)
	}
	if _, err := service.DB.Exec(`INSERT INTO narrator_messages (id, narrator_id, parent_tool_use_id, role, content_json, created_at) VALUES (?, ?, 'task-tool-1', 'user', '[]', ?)`, id.New(), subID, now); err != nil {
		t.Fatalf("insert subagent linking user message: %v", err)
	}
	if _, err := service.addMessageWithBlocks(context.Background(), subID, "assistant", "final conclusion text", []map[string]any{{"type": "text", "text": "final conclusion text"}}, nil); err != nil {
		t.Fatalf("insert subagent assistant message: %v", err)
	}

	payload, err := service.UpdateSubagentConclusion(context.Background(), subID)
	if err != nil {
		t.Fatalf("UpdateSubagentConclusion: %v", err)
	}
	if ok, _ := payload["ok"].(bool); !ok {
		t.Fatalf("expected ok=true conclusion update, got %#v", payload)
	}

	var concl *capturedBroadcast
	events := drain()
	for i := range events {
		if events[i].Payload["type"] == "subagent_conclusion_updated" {
			concl = &events[i]
		}
	}
	if concl == nil {
		t.Fatal("expected subagent_conclusion_updated broadcast to parent")
	}
	if concl.NarratorID != parentID {
		t.Fatalf("broadcast target = %q, want parent %q", concl.NarratorID, parentID)
	}
	if concl.Payload["subagentNarratorId"] != subID {
		t.Fatalf("subagentNarratorId = %v, want %q", concl.Payload["subagentNarratorId"], subID)
	}
	if concl.Payload["output"] != "final conclusion text" {
		t.Fatalf("output = %v, want final conclusion text", concl.Payload["output"])
	}
	if concl.Payload["toolUseId"] != "task-tool-1" {
		t.Fatalf("toolUseId = %v, want task-tool-1", concl.Payload["toolUseId"])
	}
}
