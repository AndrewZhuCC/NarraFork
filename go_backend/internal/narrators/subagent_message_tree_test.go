package narrators

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"narrafork-go/internal/agentbasic"
)

func TestH04SubagentEventMessagesBindParentToolUseAndParentTree(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	parentID := insertNarratorHandlerTestRow(t, sqlDB)
	hub, events := captureNarratorBroadcasts()
	service := &Service{DB: sqlDB, Hub: hub}
	ctx := context.Background()

	assistant, err := service.addMessageWithBlocks(ctx, parentID, "assistant", "launch child", []map[string]any{{"type": "tool_use", "id": "h04_tool", "name": "Agent", "input": map[string]any{"prompt": "go"}}}, nil)
	if err != nil {
		t.Fatalf("insert parent assistant: %v", err)
	}
	insertToolCallTestRow(t, sqlDB, parentID, assistant.ID, "h04_tool")
	parent, err := service.Get(ctx, parentID)
	if err != nil {
		t.Fatalf("load parent: %v", err)
	}
	child, err := service.createSubagentNarrator(ctx, parent, "general", "H04 child", "openai:test", "", "", "h04", false)
	if err != nil {
		t.Fatalf("create child: %v", err)
	}
	if _, err := service.addMessageWithBlocksWithCommandAndParentToolUse(ctx, child.ID, "user", "do child work", []map[string]any{{"type": "text", "text": "do child work"}}, nil, "", "h04_tool"); err != nil {
		t.Fatalf("insert child user: %v", err)
	}
	state := &agentRunState{narratorID: child.ID, narrator: child}
	if err := service.handleAgentEvent(ctx, state, agentbasic.AgentEvent{Type: "tool_call", ToolUseID: "child_read", ToolName: "Read", Input: map[string]any{"file_path": "README.md"}}); err != nil {
		t.Fatalf("handle child tool call: %v", err)
	}
	if err := service.handleAgentEvent(ctx, state, agentbasic.AgentEvent{Type: "tool_result", ToolUseID: "child_read", ToolName: "Read", Output: "read output", ToolRun: &agentbasic.ToolRun{ID: "child_read", Name: "Read", Arguments: `{"file_path":"README.md"}`, Output: "read output", Status: "completed"}}); err != nil {
		t.Fatalf("handle child tool result: %v", err)
	}
	if err := service.handleAgentEvent(ctx, state, agentbasic.AgentEvent{Type: "assistant_message", Text: "child complete"}); err != nil {
		t.Fatalf("handle child assistant message: %v", err)
	}

	assertAllSubagentMessagesHaveParentToolUse(t, sqlDB, child.ID, "h04_tool")
	page, err := service.MessagesCursor(ctx, parentID, 20, "", "older")
	if err != nil {
		t.Fatalf("load parent messages: %v", err)
	}
	if len(page.Messages) != 1 || len(page.Messages[0].Children) != 2 {
		t.Fatalf("expected parent tree with child user+assistant messages, got %#v", page.Messages)
	}
	catchUp, err := service.MessagesAfter(ctx, parentID, CatchUpCursor{ParentLastMessageID: &assistant.ID}, 40)
	if err != nil {
		t.Fatalf("load catch-up: %v", err)
	}
	if len(catchUp.OrphanChildren) != 2 {
		t.Fatalf("expected catch-up to include complete child output, got %#v", catchUp.OrphanChildren)
	}
	if !hasParentMessageBroadcast(events(), parentID, child.ID, "h04_tool") {
		t.Fatalf("expected child message broadcast on parent narrator, got %#v", events())
	}
}

func TestH04AgentToolForegroundBackgroundSendAwaitAndTeamStatusKeepChildTree(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "child response " + string(rune('0'+calls))}}}})
	}))
	defer server.Close()
	service, parentID, _ := newTestNarratorService(t, "openai:gpt-test", server.URL)
	ctx := context.Background()

	fgAssistant, err := service.addMessageWithBlocks(ctx, parentID, "assistant", "launch fg", []map[string]any{{"type": "tool_use", "id": "fg_tool", "name": "Agent", "input": map[string]any{"prompt": "fg"}}}, nil)
	if err != nil {
		t.Fatalf("insert foreground parent assistant: %v", err)
	}
	insertToolCallTestRow(t, service.DB, parentID, fgAssistant.ID, "fg_tool")
	fgPayload, err := service.executeAgentTool(ctx, parentID, map[string]any{"prompt": "foreground task", "subagent_type": "general", "description": "Foreground H04", "alias": "fg", "timeout": 3000, "_tool_use_id": "fg_tool"})
	if err != nil {
		t.Fatalf("execute foreground Agent tool: %v", err)
	}
	fgChildID := strings.TrimSpace(stringFromAny(fgPayload["subagentId"]))
	if fgChildID == "" || fgPayload["foreground"] != true {
		t.Fatalf("unexpected foreground payload: %#v", fgPayload)
	}
	assertAllSubagentMessagesHaveParentToolUse(t, service.DB, fgChildID, "fg_tool")

	sendPayload, err := service.sendTool(ctx, parentID, map[string]any{"id": fgChildID, "message": "follow-up", "await": true, "timeout": 3000})
	if err != nil {
		t.Fatalf("send to child: %v", err)
	}
	if sendPayload["ok"] != true {
		t.Fatalf("unexpected send payload: %#v", sendPayload)
	}
	assertAllSubagentMessagesHaveParentToolUse(t, service.DB, fgChildID, "fg_tool")
	teamPayload, err := service.teamStatusTool(ctx, parentID, map[string]any{"action": "list"})
	if err != nil {
		t.Fatalf("team status: %v", err)
	}
	if len(teamPayload["subagents"].([]map[string]any)) != 1 {
		t.Fatalf("expected team status to list child, got %#v", teamPayload)
	}

	bgAssistant, err := service.addMessageWithBlocks(ctx, parentID, "assistant", "launch bg", []map[string]any{{"type": "tool_use", "id": "bg_tool", "name": "Agent", "input": map[string]any{"prompt": "bg"}}}, nil)
	if err != nil {
		t.Fatalf("insert background parent assistant: %v", err)
	}
	insertToolCallTestRow(t, service.DB, parentID, bgAssistant.ID, "bg_tool")
	bgPayload, err := service.executeAgentTool(ctx, parentID, map[string]any{"prompt": "background task", "subagent_type": "explore", "description": "Background H04", "alias": "bg", "run_in_background": true, "_tool_use_id": "bg_tool"})
	if err != nil {
		t.Fatalf("execute background Agent tool: %v", err)
	}
	bgChildID := strings.TrimSpace(stringFromAny(bgPayload["subagentId"]))
	if bgChildID == "" || bgPayload["background"] != true {
		t.Fatalf("unexpected background payload: %#v", bgPayload)
	}
	waitForBackgroundStatus(t, service.DB, bgChildID, "completed")
	assertAllSubagentMessagesHaveParentToolUse(t, service.DB, bgChildID, "bg_tool")
	awaited, err := service.awaitTool(ctx, parentID, map[string]any{"type": "agent", "id": "bg", "timeout": 3000})
	if err != nil {
		t.Fatalf("await background child: %v", err)
	}
	if awaited["backgroundStatus"] != "completed" || awaited["alias"] != "bg" {
		t.Fatalf("unexpected await payload: %#v", awaited)
	}
	unsupportedAwait, err := service.awaitTool(ctx, parentID, map[string]any{"type": "bash", "id": "cmd-1"})
	if err != nil {
		t.Fatalf("await unsupported task type: %v", err)
	}
	if unsupportedAwait["status"] != "unsupported" || !strings.Contains(stringFromAny(unsupportedAwait["message"]), "type=agent only") {
		t.Fatalf("unsupported await should return stable fallback metadata, got %#v", unsupportedAwait)
	}

	page, err := service.MessagesCursor(ctx, parentID, 20, "", "older")
	if err != nil {
		t.Fatalf("load parent tree: %v", err)
	}
	childrenByTool := map[string]int{}
	for _, msg := range page.Messages {
		for _, child := range msg.Children {
			if child.ParentToolUseID != nil {
				childrenByTool[*child.ParentToolUseID]++
			}
		}
	}
	if childrenByTool["fg_tool"] < 4 || childrenByTool["bg_tool"] < 2 {
		t.Fatalf("expected complete foreground/background child trees, got %#v", childrenByTool)
	}
}

func assertAllSubagentMessagesHaveParentToolUse(t *testing.T, sqlDB *sql.DB, narratorID, want string) {
	t.Helper()
	rows, err := sqlDB.Query(`SELECT id, COALESCE(parent_tool_use_id, '') FROM narrator_messages WHERE narrator_id = ? ORDER BY created_at ASC`, narratorID)
	if err != nil {
		t.Fatalf("query subagent messages: %v", err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var messageID, parentToolUseID string
		if err := rows.Scan(&messageID, &parentToolUseID); err != nil {
			t.Fatalf("scan subagent message: %v", err)
		}
		count++
		if parentToolUseID != want {
			t.Fatalf("message %s parent_tool_use_id=%q, want %q", messageID, parentToolUseID, want)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate subagent messages: %v", err)
	}
	if count == 0 {
		t.Fatalf("expected subagent messages for %s", narratorID)
	}
}

func waitForBackgroundStatus(t *testing.T, sqlDB *sql.DB, narratorID, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var status string
		err := sqlDB.QueryRow(`SELECT COALESCE(background_status, '') FROM narrators WHERE id = ?`, narratorID).Scan(&status)
		if err == nil && status == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("narrator %s background_status did not reach %s", narratorID, want)
}

func hasParentMessageBroadcast(events []capturedBroadcast, parentID, childID, parentToolUseID string) bool {
	for _, event := range events {
		if event.NarratorID != parentID || event.Payload["type"] != "message" || event.Payload["childNarratorId"] != childID || event.Payload["parentToolUseId"] != parentToolUseID {
			continue
		}
		message, _ := event.Payload["message"].(map[string]any)
		if message["narratorId"] == childID && message["parentToolUseId"] == parentToolUseID {
			return true
		}
	}
	return false
}
