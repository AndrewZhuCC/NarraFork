package narrators

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"narrafork-go/internal/agentbasic"
)

// These tests lock the current Go narrator/agent event transcript contract used by
// the frontend. They are golden baselines for Go's present behavior, not claims of
// complete TypeScript parity.
func TestAgentEventTranscriptTextOnlyFrontendGolden(t *testing.T) {
	fixture := newAgentEventTranscriptFixture(t, "text-only")
	textIndex := 0

	fixture.feed(
		agentbasic.AgentEvent{Type: "stream_text", Text: "Hello ", OutputIndex: &textIndex},
		agentbasic.AgentEvent{Type: "stream_text", Text: "world", OutputIndex: &textIndex},
		agentbasic.AgentEvent{Type: "block_complete", Block: &agentbasic.ContentBlock{Type: "text", Text: "Hello world", OutputIndex: &textIndex}},
		agentbasic.AgentEvent{Type: "assistant_message", Text: "Hello world"},
		agentbasic.AgentEvent{Type: "turn_complete", TurnIndex: 0},
	)

	messages := fixture.assistantMessages()
	if len(messages) != 1 {
		t.Fatalf("expected one assistant draft/message, got %#v", messages)
	}
	if messages[0].ContentText != "Hello world" {
		t.Fatalf("assistant content_text drifted: %#v", messages[0])
	}
	wantBlocks := []map[string]any{{"type": "text", "text": "Hello world"}}
	assertJSONBlocks(t, messages[0].ContentJSON, wantBlocks)
	if fixture.state.draftMessageID == nil || *fixture.state.draftMessageID != "" {
		t.Fatalf("turn_complete should reset current draft pointer, got %#v", fixture.state.draftMessageID)
	}

	stream := fixture.firstBroadcast("stream_text")
	if stream["narratorId"] != fixture.narratorID || stream["text"] != "Hello " || intNumber(stream["outputIndex"]) != 0 {
		t.Fatalf("stream_text broadcast shape drifted: %#v", stream)
	}
	compat := fixture.firstBroadcast("stream_event")
	event, _ := compat["event"].(map[string]any)
	delta, _ := event["delta"].(map[string]any)
	if event["type"] != "content_block_delta" || delta["type"] != "text_delta" || delta["text"] != "Hello " || intNumber(event["outputIndex"]) != 0 {
		t.Fatalf("text stream_event compatibility shape drifted: %#v", compat)
	}
	block := fixture.firstBroadcast("block_complete")
	blockPayload, _ := block["block"].(map[string]any)
	if blockPayload["type"] != "text" || blockPayload["text"] != "Hello world" || intNumber(blockPayload["outputIndex"]) != 0 {
		t.Fatalf("block_complete text broadcast shape drifted: %#v", block)
	}
	message := fixture.firstBroadcast("message")
	messagePayload, _ := message["message"].(map[string]any)
	if messagePayload["role"] != "assistant" || messagePayload["contentText"] != "Hello world" {
		t.Fatalf("assistant message broadcast shape drifted: %#v", message)
	}
}

// This locks reasoning/text block ordering and field names for the current Go
// frontend transcript contract.
func TestAgentEventTranscriptReasoningAndTextFrontendGolden(t *testing.T) {
	fixture := newAgentEventTranscriptFixture(t, "reasoning-text")
	reasoningIndex := 0
	textIndex := 1
	metadata := map[string]any{"openai": map[string]any{"itemId": "rs_go_1"}}

	fixture.feed(
		agentbasic.AgentEvent{Type: "stream_reasoning", Text: "Think ", OutputIndex: &reasoningIndex, Metadata: metadata},
		agentbasic.AgentEvent{Type: "stream_reasoning", Text: "carefully", OutputIndex: &reasoningIndex, Metadata: metadata},
		agentbasic.AgentEvent{Type: "stream_text", Text: "Answer", OutputIndex: &textIndex},
		agentbasic.AgentEvent{Type: "block_complete", Block: &agentbasic.ContentBlock{Type: "reasoning", Text: "Think carefully", OutputIndex: &reasoningIndex, ProviderMetadata: metadata}},
		agentbasic.AgentEvent{Type: "block_complete", Block: &agentbasic.ContentBlock{Type: "text", Text: "Answer", OutputIndex: &textIndex}},
		agentbasic.AgentEvent{Type: "assistant_message", Text: "Answer"},
	)

	messages := fixture.assistantMessages()
	if len(messages) != 1 {
		t.Fatalf("expected one assistant message, got %#v", messages)
	}
	blocks := decodeBlocks(t, messages[0].ContentJSON)
	if len(blocks) != 2 || blocks[0]["type"] != "reasoning" || blocks[1]["type"] != "text" {
		t.Fatalf("reasoning/text block order drifted: %#v", blocks)
	}
	if blocks[0]["text"] != "Think carefully" || intNumber(blocks[0]["outputIndex"]) != 0 || blocks[0]["providerMetadata"] == nil {
		t.Fatalf("reasoning block fields drifted: %#v", blocks[0])
	}
	if blocks[1]["text"] != "Answer" || blocks[1]["outputIndex"] != nil {
		t.Fatalf("text block fields drifted: %#v", blocks[1])
	}

	reasoningStream := fixture.firstBroadcast("stream_reasoning")
	if reasoningStream["metadata"] == nil || reasoningStream["text"] != "Think " || intNumber(reasoningStream["outputIndex"]) != 0 {
		t.Fatalf("stream_reasoning broadcast shape drifted: %#v", reasoningStream)
	}
	compat := fixture.firstBroadcast("stream_event")
	event, _ := compat["event"].(map[string]any)
	delta, _ := event["delta"].(map[string]any)
	if delta["type"] != "reasoning_delta" || delta["text"] != "Think " || intNumber(delta["outputIndex"]) != 0 {
		t.Fatalf("reasoning compatibility stream_event shape drifted: %#v", compat)
	}
}

// This fixture pins current Go tool_call/tool_result persistence plus the frontend
// broadcast frames. It intentionally covers both success and error outputs.
func TestAgentEventToolLifecycleTranscriptFrontendGolden(t *testing.T) {
	fixture := newAgentEventTranscriptFixture(t, "tool-lifecycle")

	fixture.feed(
		agentbasic.AgentEvent{Type: "tool_call", ToolUseID: "read-success", ToolName: "Read", Input: map[string]any{"file_path": "ok.txt"}, StreamStartedAt: 1770000000000},
		agentbasic.AgentEvent{Type: "tool_result", ToolUseID: "read-success", ToolName: "Read", Output: "ok output", DurationMs: 12, ToolRun: &agentbasic.ToolRun{ID: "read-success", Name: "Read", Arguments: `{"file_path":"ok.txt"}`, Output: "ok output", Status: "completed"}},
		agentbasic.AgentEvent{Type: "tool_call", ToolUseID: "bash-error", ToolName: "Bash", Input: map[string]any{"command": "exit 7"}},
		agentbasic.AgentEvent{Type: "tool_result", ToolUseID: "bash-error", ToolName: "Bash", Output: "Tool error: exit status 7", IsError: true, DurationMs: 34, ToolRun: &agentbasic.ToolRun{ID: "bash-error", Name: "Bash", Arguments: `{"command":"exit 7"}`, Output: "Tool error: exit status 7", Status: "error", Error: "exit status 7"}},
	)

	messages := fixture.assistantMessages()
	if len(messages) != 1 {
		t.Fatalf("expected one assistant tool draft, got %#v", messages)
	}
	blocks := decodeBlocks(t, messages[0].ContentJSON)
	if len(blocks) != 2 || blocks[0]["type"] != "tool_use" || blocks[1]["type"] != "tool_use" {
		t.Fatalf("tool_use blocks drifted: %#v", blocks)
	}
	if blocks[0]["id"] != "read-success" || blocks[0]["name"] != "Read" || intNumber(blocks[0]["streamStartedAt"]) != 1770000000000 {
		t.Fatalf("read tool_use block shape drifted: %#v", blocks[0])
	}
	if blocks[1]["id"] != "bash-error" || blocks[1]["name"] != "Bash" {
		t.Fatalf("error tool_use block shape drifted: %#v", blocks[1])
	}

	success := fixture.toolCall("read-success")
	if success.Status != "success" || !strings.Contains(success.OutputJSON, "ok output") || success.ResultMessageID == "" {
		t.Fatalf("success tool call DB shape drifted: %#v", success)
	}
	errorCall := fixture.toolCall("bash-error")
	if errorCall.Status != "fail" || !strings.Contains(errorCall.OutputJSON, "Tool error") || errorCall.ErrorMessage != "" {
		t.Fatalf("error tool call DB shape drifted: %#v", errorCall)
	}

	started := fixture.firstBroadcast("tool_started")
	if started["toolUseId"] != "read-success" || started["toolName"] != "Read" || started["input"] == nil || intNumber(started["streamStartedAt"]) != 1770000000000 {
		t.Fatalf("tool_started broadcast shape drifted: %#v", started)
	}
	completed := fixture.broadcastByTool("tool_completed", "read-success")
	if completed["status"] != "success" || completed["output"] != "ok output" || intNumber(completed["durationMs"]) != 12 {
		t.Fatalf("success tool_completed shape drifted: %#v", completed)
	}
	failed := fixture.broadcastByTool("tool_completed", "bash-error")
	if failed["status"] != "fail" || failed["output"] != "Tool error: exit status 7" || intNumber(failed["durationMs"]) != 34 {
		t.Fatalf("error tool_completed shape drifted: %#v", failed)
	}
}

// AgentEvent currently has no first-class permission_request/decision event handled
// by Service.handleAgentEvent. This locks the closest current path: pending permission
// request creation and resolved decision/tool completion broadcasts.
func TestPermissionLifecycleClosestTranscriptFrontendGolden(t *testing.T) {
	fixture := newAgentEventTranscriptFixture(t, "permission-lifecycle")
	call := agentbasic.ToolCall{ID: "write-permission", Type: "function"}
	call.Function.Name = "Write"
	call.Function.Arguments = `{"file_path":"approved.txt","content":"ok"}`

	fixture.feed(agentbasic.AgentEvent{Type: "tool_call", ToolUseID: call.ID, ToolName: call.Function.Name, Input: map[string]any{"file_path": "approved.txt", "content": "ok"}})
	if err := fixture.service.recordPendingToolCall(context.Background(), fixture.narratorID, *fixture.state.draftMessageID, fixture.state.narrator, call, "Write requires approval"); err != nil {
		t.Fatalf("record pending permission: %v", err)
	}

	pending := fixture.toolCall(call.ID)
	if pending.Status != "pending" || pending.PermissionStartedAt == "" || !strings.Contains(pending.PermissionSuggestions, "awaiting_user") || !strings.Contains(pending.PermissionDecisionReason, "Write requires approval") {
		t.Fatalf("pending permission DB shape drifted: %#v", pending)
	}
	request := fixture.firstBroadcast("permission_request")
	requestPayload, _ := request["request"].(map[string]any)
	if request["narratorId"] != fixture.narratorID || requestPayload["toolUseId"] != call.ID || requestPayload["toolName"] != "Write" || requestPayload["inputJson"] == nil || requestPayload["decisionReason"] != "Write requires approval" || requestPayload["suggestions"] == nil {
		t.Fatalf("permission_request broadcast shape drifted: %#v", request)
	}
	requestID, _ := requestPayload["id"].(string)
	if requestID == "" || requestID != pending.ID {
		t.Fatalf("permission request id should match tool_call row id, request=%#v row=%#v", requestPayload, pending)
	}

	decision := permissionDecision{Status: "approved", Reason: "looks safe", Message: "approved by tester", DecidedBy: "tester", DecidedAt: "2026-06-01T00:00:00Z"}
	run := agentbasic.ToolRun{ID: call.ID, Name: "Write", Arguments: call.Function.Arguments, Output: "Wrote approved.txt", Status: "completed"}
	if err := fixture.service.finishRecordedToolCall(context.Background(), fixture.narratorID, call.ID, *fixture.state.draftMessageID, run, decision, "success"); err != nil {
		t.Fatalf("finish recorded tool call: %v", err)
	}

	finished := fixture.toolCall(call.ID)
	if finished.Status != "success" || finished.PermissionDecidedBy != "tester" || finished.PermissionDecidedAt != "2026-06-01T00:00:00Z" || !strings.Contains(finished.OutputJSON, "Wrote approved.txt") {
		t.Fatalf("resolved permission DB shape drifted: %#v", finished)
	}
	resolved := fixture.firstBroadcast("permission_resolved")
	if resolved["requestId"] != requestID || resolved["toolUseId"] != call.ID || resolved["decision"] != "allow" {
		t.Fatalf("permission_resolved broadcast shape drifted: %#v", resolved)
	}
	completed := fixture.broadcastByTool("tool_completed", call.ID)
	if completed["status"] != "success" || completed["output"] != "Wrote approved.txt" || completed["toolName"] != "Write" {
		t.Fatalf("permission tool_completed broadcast shape drifted: %#v", completed)
	}
}

type agentEventTranscriptFixture struct {
	t          *testing.T
	service    *Service
	narratorID string
	state      *agentRunState
	events     func() []capturedBroadcast
}

func newAgentEventTranscriptFixture(t *testing.T, name string) *agentEventTranscriptFixture {
	t.Helper()
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", "http://127.0.0.1")
	hub, events := captureNarratorBroadcasts()
	service.Hub = hub
	narrator, err := service.Get(context.Background(), narratorID)
	if err != nil {
		t.Fatalf("load narrator for %s: %v", name, err)
	}
	return &agentEventTranscriptFixture{
		t:          t,
		service:    service,
		narratorID: narratorID,
		state:      &agentRunState{narratorID: narratorID, narrator: narrator},
		events:     events,
	}
}

func (f *agentEventTranscriptFixture) feed(events ...agentbasic.AgentEvent) {
	f.t.Helper()
	for _, event := range events {
		if err := f.service.handleAgentEvent(context.Background(), f.state, event); err != nil {
			f.t.Fatalf("handle agent event %s: %v", event.Type, err)
		}
	}
}

func (f *agentEventTranscriptFixture) assistantMessages() []agentEventAssistantMessageRow {
	f.t.Helper()
	rows, err := f.service.DB.Query(`SELECT m.id, COALESCE(m.content_text, ''), COALESCE(m.content_json, '[]') FROM narrator_messages m JOIN narrator_message_refs r ON r.message_id = m.id AND r.narrator_id = m.narrator_id WHERE m.narrator_id = ? AND m.role = 'assistant' ORDER BY r.seq ASC`, f.narratorID)
	if err != nil {
		f.t.Fatalf("query assistant messages: %v", err)
	}
	defer rows.Close()
	items := []agentEventAssistantMessageRow{}
	for rows.Next() {
		var item agentEventAssistantMessageRow
		if err := rows.Scan(&item.ID, &item.ContentText, &item.ContentJSON); err != nil {
			f.t.Fatalf("scan assistant message: %v", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		f.t.Fatalf("iterate assistant messages: %v", err)
	}
	return items
}

func (f *agentEventTranscriptFixture) toolCall(toolUseID string) agentEventToolCallRow {
	f.t.Helper()
	var row agentEventToolCallRow
	var resultMessageID sql.NullString
	err := f.service.DB.QueryRow(`SELECT id, tool_use_id, tool_name, COALESCE(input_json, ''), COALESCE(output_json, ''), status, COALESCE(permission_started_at, ''), COALESCE(permission_suggestions, ''), COALESCE(permission_decision_reason, ''), COALESCE(permission_decided_by, ''), COALESCE(permission_decided_at, ''), COALESCE(error_message, ''), result_message_id FROM narrator_tool_calls WHERE narrator_id = ? AND tool_use_id = ? ORDER BY created_at DESC LIMIT 1`, f.narratorID, toolUseID).Scan(&row.ID, &row.ToolUseID, &row.ToolName, &row.InputJSON, &row.OutputJSON, &row.Status, &row.PermissionStartedAt, &row.PermissionSuggestions, &row.PermissionDecisionReason, &row.PermissionDecidedBy, &row.PermissionDecidedAt, &row.ErrorMessage, &resultMessageID)
	if err != nil {
		f.t.Fatalf("query tool call %s: %v", toolUseID, err)
	}
	row.ResultMessageID = sqlNullString(resultMessageID)
	return row
}

func (f *agentEventTranscriptFixture) firstBroadcast(eventType string) map[string]any {
	f.t.Helper()
	for _, event := range f.events() {
		if event.NarratorID == f.narratorID && event.Payload["type"] == eventType {
			return event.Payload
		}
	}
	f.t.Fatalf("missing broadcast %s in %#v", eventType, f.events())
	return nil
}

func (f *agentEventTranscriptFixture) broadcastByTool(eventType, toolUseID string) map[string]any {
	f.t.Helper()
	for _, event := range f.events() {
		if event.NarratorID == f.narratorID && event.Payload["type"] == eventType && event.Payload["toolUseId"] == toolUseID {
			return event.Payload
		}
	}
	f.t.Fatalf("missing broadcast %s/%s in %#v", eventType, toolUseID, f.events())
	return nil
}

type agentEventAssistantMessageRow struct {
	ID          string
	ContentText string
	ContentJSON string
}

type agentEventToolCallRow struct {
	ID                       string
	ToolUseID                string
	ToolName                 string
	InputJSON                string
	OutputJSON               string
	Status                   string
	PermissionStartedAt      string
	PermissionSuggestions    string
	PermissionDecisionReason string
	PermissionDecidedBy      string
	PermissionDecidedAt      string
	ErrorMessage             string
	ResultMessageID          string
}

func decodeBlocks(t *testing.T, raw string) []map[string]any {
	t.Helper()
	var blocks []map[string]any
	if err := json.Unmarshal([]byte(raw), &blocks); err != nil {
		t.Fatalf("decode blocks: %v raw=%s", err, raw)
	}
	return blocks
}

func assertJSONBlocks(t *testing.T, raw string, want []map[string]any) {
	t.Helper()
	got := decodeBlocks(t, raw)
	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(want)
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("content_json blocks drifted:\n got: %s\nwant: %s", gotJSON, wantJSON)
	}
}
