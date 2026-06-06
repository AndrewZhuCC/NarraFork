package narrators

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"narrafork-go/internal/agentbasic"
	"narrafork-go/internal/ws"
)

func TestBackgroundTaskToolUseIDReturnsLookupErrors(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	if _, err := sqlDB.Exec(`INSERT INTO background_tasks (id, parent_narrator_id, type, status, tool_use_id, started_at, created_at, updated_at) VALUES ('task-1', 'parent-1', 'general', 'running', 'tool-bg', 'now', 'now', 'now')`); err != nil {
		t.Fatalf("insert background task: %v", err)
	}
	for _, stmt := range []string{
		`ALTER TABLE background_tasks RENAME TO background_tasks_real`,
		`CREATE VIEW background_tasks AS SELECT id, parent_narrator_id, type, status, command, exit_code, subagent_narrator_id, subagent_type, json_extract('not-json', '$') AS tool_use_id, alias, title, output, output_bytes, output_truncated, notified, started_at, completed_at, created_at, updated_at FROM background_tasks_real`,
	} {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("prepare broken background_tasks view: %v", err)
		}
	}
	service := &Service{DB: sqlDB}

	_, err := service.backgroundTaskToolUseID(context.Background(), "parent-1", "task-1")
	if err == nil {
		t.Fatalf("expected background task tool_use_id lookup error")
	}
}

func TestSubagentFallbackResultsExposeReason(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	service := &Service{DB: sqlDB}
	ctx := context.Background()

	cases := []struct {
		name string
		call func() (map[string]any, error)
	}{
		{name: "detach", call: func() (map[string]any, error) { return service.DetachSubagent(ctx, narratorID) }},
		{name: "attach", call: func() (map[string]any, error) { return service.AttachSubagent(ctx, narratorID) }},
		{name: "conclusion", call: func() (map[string]any, error) { return service.UpdateSubagentConclusion(ctx, narratorID) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload, err := tc.call()
			if err != nil {
				t.Fatalf("call %s: %v", tc.name, err)
			}
			reason, _ := payload["reason"].(string)
			if payload["fallback"] != true || reason != "Not a subagent" || payload["error"] != reason || payload["message"] != reason {
				t.Fatalf("subagent fallback should expose reason/error/message, got %#v", payload)
			}
		})
	}
}

func TestRecordedToolCallIDReturnsLookupErrors(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	if _, err := sqlDB.Exec(`INSERT INTO narrator_tool_calls (id, narrator_id, message_id, tool_use_id, tool_name, status, created_at) VALUES ('tc-1', 'nar-1', 'msg-1', 'tool-1', 'Bash', 'pending', 'now')`); err != nil {
		t.Fatalf("insert tool call: %v", err)
	}
	for _, stmt := range []string{
		`ALTER TABLE narrator_tool_calls RENAME TO narrator_tool_calls_real`,
		`CREATE VIEW narrator_tool_calls AS SELECT id, narrator_id, message_id, tool_use_id, tool_name, input_json, status, output_json, execution_started_at, completed_at, error_message, permission_decided_by, permission_decided_at, permission_deny_message, permission_decision_reason, result_message_id, json_extract('not-json', '$') AS created_at FROM narrator_tool_calls_real`,
	} {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("prepare broken tool call view: %v", err)
		}
	}
	service := &Service{DB: sqlDB}

	_, err := service.recordedToolCallID(context.Background(), "nar-1", "tool-1")
	if err == nil {
		t.Fatalf("expected recorded tool call lookup error")
	}
}

func TestRecordRunningToolCallReturnsDuplicateLookupErrors(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	for _, stmt := range []string{
		`ALTER TABLE narrator_tool_calls RENAME TO narrator_tool_calls_real`,
		`CREATE VIEW narrator_tool_calls AS SELECT id, narrator_id, message_id, json_extract('not-json', '$') AS tool_use_id, tool_name, input_json, status, execution_started_at, is_background, created_at FROM narrator_tool_calls_real`,
		`CREATE TRIGGER narrator_tool_calls_insert INSTEAD OF INSERT ON narrator_tool_calls BEGIN INSERT INTO narrator_tool_calls_real (id, narrator_id, message_id, tool_use_id, tool_name, input_json, status, execution_started_at, is_background, created_at) VALUES (NEW.id, NEW.narrator_id, NEW.message_id, NEW.tool_use_id, NEW.tool_name, NEW.input_json, NEW.status, NEW.execution_started_at, NEW.is_background, NEW.created_at); END;`,
	} {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("prepare broken tool call view: %v", err)
		}
	}
	service := &Service{DB: sqlDB}

	err := service.recordRunningToolCall(context.Background(), "nar-1", "msg-1", Narrator{}, agentbasic.AgentEvent{ToolUseID: "tool-1", ToolName: "Bash", Input: map[string]any{"command": "true"}})
	if err == nil || !strings.Contains(err.Error(), "query running tool call") {
		t.Fatalf("expected running tool call lookup error, got %v", err)
	}
	var count int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM narrator_tool_calls_real`).Scan(&count); err != nil {
		t.Fatalf("count backing tool calls: %v", err)
	}
	if count != 0 {
		t.Fatalf("lookup failure should not insert tool call, count=%d", count)
	}
}

func TestSendToolRejectsInterruptFromSubagentCaller(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	parentID := insertNarratorHandlerTestRow(t, sqlDB)
	service := &Service{DB: sqlDB}
	parent, err := service.Get(context.Background(), parentID)
	if err != nil {
		t.Fatalf("load parent: %v", err)
	}
	child, err := service.createSubagentNarrator(context.Background(), parent, "general", "Worker", "openai:test", "", "", "worker", false)
	if err != nil {
		t.Fatalf("create child: %v", err)
	}

	payload, err := service.sendTool(context.Background(), child.ID, map[string]any{"id": child.ID, "message": "hello", "doInterrupt": true})
	if err != nil {
		t.Fatalf("send tool returned error: %v", err)
	}
	reason := assertToolDiagnosticFields(t, payload, "SEND_INTERRUPT_UNSUPPORTED_FROM_SUBAGENT")
	if payload["ok"] != false || !strings.Contains(reason, "doInterrupt") {
		t.Fatalf("expected doInterrupt guard diagnostics, got %#v", payload)
	}
	var messageCount int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM narrator_messages WHERE narrator_id = ? AND content_text = 'hello'`, child.ID).Scan(&messageCount); err != nil {
		t.Fatalf("count child messages: %v", err)
	}
	if messageCount != 0 {
		t.Fatalf("send should not enqueue message after interrupt guard, count=%d", messageCount)
	}
}

func TestFinishRecordedToolCallLookupFallbackEventExposesReason(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	if _, err := sqlDB.Exec(`INSERT INTO narrator_tool_calls (id, narrator_id, message_id, tool_use_id, tool_name, status, created_at) VALUES ('tc-1', 'nar-1', 'msg-1', 'tool-1', 'Bash', 'pending', 'now')`); err != nil {
		t.Fatalf("insert tool call: %v", err)
	}
	for _, stmt := range []string{
		`ALTER TABLE narrator_tool_calls RENAME TO narrator_tool_calls_real`,
		`CREATE VIEW narrator_tool_calls AS SELECT id, narrator_id, message_id, tool_use_id, tool_name, input_json, status, output_json, execution_started_at, completed_at, error_message, permission_decided_by, permission_decided_at, permission_deny_message, permission_decision_reason, result_message_id, CASE WHEN status IN ('success', 'fail', 'error') THEN json_extract('not-json', '$') ELSE created_at END AS created_at FROM narrator_tool_calls_real`,
		`CREATE TRIGGER narrator_tool_calls_update INSTEAD OF UPDATE ON narrator_tool_calls BEGIN UPDATE narrator_tool_calls_real SET status = NEW.status, output_json = NEW.output_json, execution_started_at = NEW.execution_started_at, completed_at = NEW.completed_at, error_message = NEW.error_message, permission_decided_by = NEW.permission_decided_by, permission_decided_at = NEW.permission_decided_at, permission_deny_message = NEW.permission_deny_message, permission_decision_reason = NEW.permission_decision_reason, result_message_id = NEW.result_message_id WHERE narrator_id = OLD.narrator_id AND tool_use_id = OLD.tool_use_id; END;`,
	} {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("prepare broken tool call view: %v", err)
		}
	}
	hub, events := captureNarratorBroadcasts()
	service := &Service{DB: sqlDB, Hub: hub}

	err := service.finishRecordedToolCall(context.Background(), "nar-1", "tool-1", "msg-result", agentbasic.ToolRun{ID: "tool-1", Name: "Bash", Output: "done", Status: "completed"}, permissionDecision{}, "success")
	if err == nil || !strings.Contains(err.Error(), "query recorded tool call") {
		t.Fatalf("expected recorded tool call query error, got %v", err)
	}
	payload := findBroadcast(events(), "nar-1", "permission_resolved")
	if payload == nil {
		t.Fatalf("expected degraded permission_resolved broadcast, got %#v", events())
	}
	reason, _ := payload["reason"].(string)
	if payload["fallback"] != true || payload["degraded"] != true || reason == "" || payload["error"] != reason || payload["message"] != reason {
		t.Fatalf("permission_resolved fallback should expose reason/error/message, got %#v", payload)
	}
	completed := findBroadcast(events(), "nar-1", "tool_completed")
	if completed == nil || completed["status"] != "success" || completed["output"] != "done" || completed["fallback"] != true {
		t.Fatalf("fallback should still broadcast final tool_completed status, got %#v", events())
	}
}

func TestFinishRecordedToolCallBroadcastsToolCompletedStatus(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	if _, err := sqlDB.Exec(`INSERT INTO narrator_tool_calls (id, narrator_id, message_id, tool_use_id, tool_name, status, created_at) VALUES ('tc-1', 'nar-1', 'msg-1', 'tool-1', 'Bash', 'pending', 'now')`); err != nil {
		t.Fatalf("insert tool call: %v", err)
	}
	hub, events := captureNarratorBroadcasts()
	service := &Service{DB: sqlDB, Hub: hub}

	err := service.finishRecordedToolCall(context.Background(), "nar-1", "tool-1", "msg-result", agentbasic.ToolRun{ID: "tool-1", Name: "Bash", Output: "done", Status: "completed"}, permissionDecision{Status: "approved"}, "success")
	if err != nil {
		t.Fatalf("finish recorded tool call: %v", err)
	}
	completed := findBroadcast(events(), "nar-1", "tool_completed")
	if completed == nil || completed["toolUseId"] != "tool-1" || completed["toolName"] != "Bash" || completed["status"] != "success" || completed["output"] != "done" {
		t.Fatalf("expected final tool_completed broadcast, got %#v", events())
	}
}

func TestMessageByIDIncludesToolCallStatusForLiveBroadcast(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	service := &Service{DB: sqlDB}
	msg, err := service.addMessageWithBlocks(context.Background(), narratorID, "assistant", "", []map[string]any{{"type": "tool_use", "id": "tool-1", "name": "Bash", "input": map[string]any{"command": "true"}}}, nil)
	if err != nil {
		t.Fatalf("insert assistant message: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO narrator_tool_calls (id, narrator_id, message_id, tool_use_id, tool_name, input_json, output_json, status, created_at) VALUES ('tc-1', ?, ?, 'tool-1', 'Bash', '{"command":"true"}', '{"output":"done"}', 'success', 'now')`, narratorID, msg.ID); err != nil {
		t.Fatalf("insert tool call: %v", err)
	}

	loaded, err := service.messageByID(context.Background(), narratorID, msg.ID)
	if err != nil {
		t.Fatalf("load message: %v", err)
	}
	if len(loaded.ToolCalls) != 1 || loaded.ToolCalls[0].ToolUseID != "tool-1" || loaded.ToolCalls[0].Status != "success" {
		t.Fatalf("messageByID should include current tool call status, got %#v", loaded.ToolCalls)
	}
}

func TestPersistAgentErrorReturnsMessageInsertErrorsAndBroadcastsFallback(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	prepareBrokenNarratorMessageRefsSeq(t, sqlDB, narratorID)
	hub, events := captureNarratorBroadcasts()
	service := &Service{DB: sqlDB, Hub: hub}

	err := service.persistAgentError(context.Background(), narratorID, errors.New("model failed"), false)
	if err == nil || !strings.Contains(err.Error(), "persist agent error message") {
		t.Fatalf("expected message persist error, got %v", err)
	}
	payload := findBroadcast(events(), narratorID, "error")
	if payload == nil || payload["code"] != "AGENT_ERROR_PERSIST_FAILED" || payload["stage"] != "message" || payload["fallback"] != true || payload["fatal"] != false {
		t.Fatalf("expected message-stage persistence fallback broadcast, got %#v", events())
	}
	reason, _ := payload["reason"].(string)
	if reason == "" || payload["error"] != reason || payload["message"] != reason {
		t.Fatalf("agent error message persistence fallback should expose reason/error/message, got %#v", payload)
	}
	assertNoMessageRefSeqFallbackInsert(t, sqlDB, narratorID, "AI provider call failed: model failed")
	var status, substatus, errorMessage string
	if err := sqlDB.QueryRow(`SELECT status, substatus, COALESCE(error_message, '') FROM narrators WHERE id = ?`, narratorID).Scan(&status, &substatus, &errorMessage); err != nil {
		t.Fatalf("load narrator after message persist failure: %v", err)
	}
	if status != "idle" || substatus != "[]" || errorMessage != "" {
		t.Fatalf("message persist failure should not continue to status update, status=%s substatus=%s error=%s", status, substatus, errorMessage)
	}
}

func TestPersistAgentErrorReturnsStatusUpdateErrorsAndBroadcastsFallback(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	if _, err := sqlDB.Exec(`CREATE TRIGGER fail_agent_error_status_update BEFORE UPDATE OF status ON narrators BEGIN SELECT RAISE(ABORT, 'agent error status update blocked'); END;`); err != nil {
		t.Fatalf("create status trigger: %v", err)
	}
	hub, events := captureNarratorBroadcasts()
	service := &Service{DB: sqlDB, Hub: hub}

	err := service.persistAgentError(context.Background(), narratorID, errors.New("model failed"), false)
	if err == nil || !strings.Contains(err.Error(), "persist agent error status") {
		t.Fatalf("expected status persist error, got %v", err)
	}
	found := false
	for _, event := range events() {
		payload := event.Payload
		if payload["type"] == "error" && payload["code"] == "AGENT_ERROR_PERSIST_FAILED" && payload["stage"] == "status" {
			reason, _ := payload["reason"].(string)
			if reason == "" || payload["error"] != reason || payload["message"] != reason {
				t.Fatalf("agent error persistence fallback should expose reason/error/message, got %#v", payload)
			}
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected agent error persistence fallback broadcast, got %#v", events())
	}
}

func TestDetachSubagentUnblocksParentBlockingAwait(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	parentID := insertNarratorHandlerTestRow(t, sqlDB)
	hub, events := captureNarratorBroadcasts()
	service := &Service{DB: sqlDB, Hub: hub}
	parent, err := service.Get(context.Background(), parentID)
	if err != nil {
		t.Fatalf("load parent: %v", err)
	}
	child, err := service.createSubagentNarrator(context.Background(), parent, "general", "Detach worker", "openai:test", "", "", "worker", false)
	if err != nil {
		t.Fatalf("create child: %v", err)
	}
	if _, err := service.addMessageWithBlocksWithCommandAndParentToolUse(context.Background(), child.ID, "user", "launch", []map[string]any{{"type": "text", "text": "launch"}}, nil, "", "toolu_detach"); err != nil {
		t.Fatalf("seed child parent tool use: %v", err)
	}
	if _, err := sqlDB.Exec(`UPDATE narrators SET status = 'working' WHERE id = ?`, child.ID); err != nil {
		t.Fatalf("mark child working: %v", err)
	}

	resultCh := make(chan map[string]any, 1)
	errCh := make(chan error, 1)
	go func() {
		payload, err := service.awaitTool(context.Background(), parentID, map[string]any{"type": "agent", "id": child.ID, "timeout": 5000, "_parent_blocking": true, "_tool_use_id": "toolu_detach"})
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- payload
	}()

	detached, err := service.DetachSubagent(context.Background(), child.ID)
	if err != nil {
		t.Fatalf("detach child: %v", err)
	}
	if detached["detached"] != true || detached["fallback"] != false {
		t.Fatalf("unexpected detach payload: %#v", detached)
	}
	select {
	case err := <-errCh:
		t.Fatalf("await returned error: %v", err)
	case payload := <-resultCh:
		if payload["status"] != "detached" || payload["detached"] != true || !strings.Contains(stringFromAny(payload["formatted"]), "<background_task_id>") {
			t.Fatalf("parent-blocking await should return detached background payload, got %#v", payload)
		}
	case <-time.After(1500 * time.Millisecond):
		t.Fatal("parent-blocking await did not unblock after detach")
	}
	var taskStatus, toolUseID string
	if err := sqlDB.QueryRow(`SELECT status, tool_use_id FROM background_tasks WHERE id = ?`, child.ID).Scan(&taskStatus, &toolUseID); err != nil {
		t.Fatalf("load background task: %v", err)
	}
	if taskStatus != "running" || toolUseID != "toolu_detach" {
		t.Fatalf("background task status/tool=%s/%s", taskStatus, toolUseID)
	}
	if !hasBroadcast(events(), parentID, "subagent_detached") || !hasBroadcast(events(), parentID, "background_task_started") {
		t.Fatalf("expected detach/start broadcasts, got %#v", events())
	}
}

func TestAttachRunningBackgroundReportsParentBlockingWhenRuntimeActive(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	parentID := insertNarratorHandlerTestRow(t, sqlDB)
	hub, events := captureNarratorBroadcasts()
	service := &Service{DB: sqlDB, Hub: hub}
	parent, err := service.Get(context.Background(), parentID)
	if err != nil {
		t.Fatalf("load parent: %v", err)
	}
	child, err := service.createSubagentNarrator(context.Background(), parent, "general", "Attach worker", "openai:test", "", "", "attach", true)
	if err != nil {
		t.Fatalf("create background child: %v", err)
	}
	if err := service.createBackgroundAgentTask(context.Background(), parentID, child.ID, "general", "Attach worker", "toolu_attach", "attach"); err != nil {
		t.Fatalf("create background task: %v", err)
	}
	service.runMu.Lock()
	service.activeRuns = map[string]context.CancelFunc{child.ID: func() {}}
	service.runMu.Unlock()

	payload, err := service.AttachSubagent(context.Background(), child.ID)
	if err != nil {
		t.Fatalf("attach child: %v", err)
	}
	if payload["attached"] != true || payload["parentBlocking"] != true || payload["fallback"] != false {
		t.Fatalf("unexpected attach payload: %#v", payload)
	}
	var isBackground int
	if err := sqlDB.QueryRow(`SELECT is_background FROM narrators WHERE id = ?`, child.ID).Scan(&isBackground); err != nil {
		t.Fatalf("load child background flag: %v", err)
	}
	if isBackground != 0 {
		t.Fatalf("attach should clear is_background, got %d", isBackground)
	}
	if !hasBroadcast(events(), parentID, "subagent_attached") {
		t.Fatalf("expected subagent_attached broadcast, got %#v", events())
	}
}

func TestBackgroundAgentCompletionSyncsTaskRecordAndBroadcastsParentEvents(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	parentID := insertNarratorHandlerTestRow(t, sqlDB)
	hub, events := captureNarratorBroadcasts()
	service := &Service{DB: sqlDB, Hub: hub}
	parent, err := service.Get(context.Background(), parentID)
	if err != nil {
		t.Fatalf("load parent: %v", err)
	}
	child, err := service.createSubagentNarrator(context.Background(), parent, "general", "Background worker", "openai:test", "", "", "worker", true)
	if err != nil {
		t.Fatalf("create child: %v", err)
	}
	insertParentAgentToolCallForTest(t, service, parentID, "toolu_bg")
	if err := service.createBackgroundAgentTask(context.Background(), parentID, child.ID, "general", "Background worker", "toolu_bg", "worker"); err != nil {
		t.Fatalf("create background task: %v", err)
	}
	if !hasBroadcast(events(), parentID, "background_task_started") {
		t.Fatalf("expected background_task_started broadcast, got %#v", events())
	}

	state := &agentRunState{narratorID: child.ID, narrator: child, assistantText: "finished output"}
	if err := service.finishAgentRunSuccess(context.Background(), state); err != nil {
		t.Fatalf("finish background run: %v", err)
	}

	var taskStatus, taskOutput string
	var notified int
	if err := sqlDB.QueryRow(`SELECT status, output, notified FROM background_tasks WHERE id = ?`, child.ID).Scan(&taskStatus, &taskOutput, &notified); err != nil {
		t.Fatalf("load background task: %v", err)
	}
	if taskStatus != "completed" || taskOutput != "finished output" || notified != 0 {
		t.Fatalf("task status/output/notified = %q/%q/%d", taskStatus, taskOutput, notified)
	}
	var backgroundStatus, backgroundResult string
	if err := sqlDB.QueryRow(`SELECT background_status, background_result FROM narrators WHERE id = ?`, child.ID).Scan(&backgroundStatus, &backgroundResult); err != nil {
		t.Fatalf("load child background fields: %v", err)
	}
	if backgroundStatus != "completed" || backgroundResult != "finished output" {
		t.Fatalf("child background status/result = %q/%q", backgroundStatus, backgroundResult)
	}
	var toolStatus, toolOutput string
	if err := sqlDB.QueryRow(`SELECT status, output_json FROM narrator_tool_calls WHERE narrator_id = ? AND tool_use_id = 'toolu_bg'`, parentID).Scan(&toolStatus, &toolOutput); err != nil {
		t.Fatalf("load injected parent tool call: %v", err)
	}
	if toolStatus != "success" || !strings.Contains(toolOutput, "finished output") || !strings.Contains(toolOutput, "backgroundCompleted") {
		t.Fatalf("parent tool call not injected: status=%s output=%s", toolStatus, toolOutput)
	}
	if !hasBroadcast(events(), parentID, "tool_completed") {
		t.Fatalf("expected injected tool_completed broadcast, got %#v", events())
	}
	if !hasBroadcast(events(), parentID, "background_task_completed") {
		t.Fatalf("expected background_task_completed broadcast, got %#v", events())
	}
	if !hasBroadcastStatus(events(), parentID, child.ID, "completed") {
		t.Fatalf("expected background_task_status_changed completed broadcast, got %#v", events())
	}
}

func TestBackgroundAgentCompletionBroadcastsFallbackWhenTaskRecordUpdateFails(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	parentID := insertNarratorHandlerTestRow(t, sqlDB)
	hub, events := captureNarratorBroadcasts()
	service := &Service{DB: sqlDB, Hub: hub}
	parent, err := service.Get(context.Background(), parentID)
	if err != nil {
		t.Fatalf("load parent: %v", err)
	}
	child, err := service.createSubagentNarrator(context.Background(), parent, "general", "Background worker", "openai:test", "", "", "worker", true)
	if err != nil {
		t.Fatalf("create child: %v", err)
	}
	insertParentAgentToolCallForTest(t, service, parentID, "toolu_bg_fail")
	if err := service.createBackgroundAgentTask(context.Background(), parentID, child.ID, "general", "Background worker", "toolu_bg_fail", "worker"); err != nil {
		t.Fatalf("create background task: %v", err)
	}
	if _, err := sqlDB.Exec(`CREATE TRIGGER fail_background_task_finish BEFORE UPDATE OF status ON background_tasks BEGIN SELECT RAISE(ABORT, 'background task finish blocked'); END;`); err != nil {
		t.Fatalf("create background task failure trigger: %v", err)
	}

	state := &agentRunState{narratorID: child.ID, narrator: child, assistantText: "finished output"}
	err = service.finishAgentRunSuccess(context.Background(), state)
	if err == nil || !strings.Contains(err.Error(), "background task finish blocked") {
		t.Fatalf("expected background task finish error, got %v", err)
	}
	payload := findBroadcast(events(), child.ID, "error")
	if payload == nil || payload["code"] != "AGENT_ERROR_PERSIST_FAILED" || payload["stage"] != "background_task" || payload["fallback"] != true || payload["fatal"] != false {
		t.Fatalf("expected background-task persistence fallback broadcast, got %#v", events())
	}
	reason, _ := payload["reason"].(string)
	if reason == "" || payload["error"] != reason || payload["message"] != reason {
		t.Fatalf("background-task persistence fallback should expose reason/error/message, got %#v", payload)
	}
	var taskStatus, backgroundStatus, backgroundResult string
	if err := sqlDB.QueryRow(`SELECT status FROM background_tasks WHERE id = ?`, child.ID).Scan(&taskStatus); err != nil {
		t.Fatalf("load task status: %v", err)
	}
	if err := sqlDB.QueryRow(`SELECT background_status, background_result FROM narrators WHERE id = ?`, child.ID).Scan(&backgroundStatus, &backgroundResult); err != nil {
		t.Fatalf("load child background fields: %v", err)
	}
	if taskStatus != "running" || backgroundStatus != "completed" || backgroundResult != "finished output" {
		t.Fatalf("unexpected partial background state task=%s child=%s result=%q", taskStatus, backgroundStatus, backgroundResult)
	}
	if hasBroadcast(events(), parentID, "background_task_completed") || hasBroadcastStatus(events(), parentID, child.ID, "completed") {
		t.Fatalf("failed task-record update should not broadcast completed parent events, got %#v", events())
	}
}

func TestAgentToolEventsBroadcastTSCompatibleToolStartedAndCompleted(t *testing.T) {
	hub, events := captureNarratorBroadcasts()
	service := &Service{Hub: hub}
	service.broadcastAgentEvent("nar_tool", agentbasic.AgentEvent{Type: "tool_call", ToolUseID: "toolu_1", ToolName: "Task", Input: map[string]any{"prompt": "go"}})
	service.broadcastAgentEvent("nar_tool", agentbasic.AgentEvent{Type: "tool_result", ToolUseID: "toolu_1", ToolName: "Task", Output: "started", DurationMs: 12})

	if !hasBroadcast(events(), "nar_tool", "tool_started") {
		t.Fatalf("expected tool_started broadcast, got %#v", events())
	}
	completed := findBroadcast(events(), "nar_tool", "tool_completed")
	if completed == nil || completed["status"] != "success" || completed["output"] != "started" {
		t.Fatalf("unexpected tool_completed broadcast: %#v all=%#v", completed, events())
	}
}

func TestAgentQueueEventsBroadcastTSCompatibleFields(t *testing.T) {
	hub, events := captureNarratorBroadcasts()
	service := &Service{Hub: hub}
	service.broadcastAgentEvent("nar_queue", agentbasic.AgentEvent{Type: "nkp_queue_status", Message: "waiting", Metadata: map[string]any{"position": 2, "queueDepth": 5}})
	service.broadcastAgentEvent("nar_queue", agentbasic.AgentEvent{Type: "queue_status", Message: "Waiting for capacity", Metadata: map[string]any{"position": 3, "queueDepth": 8}})

	nkp := findBroadcast(events(), "nar_queue", "nkp_queue_status")
	if nkp == nil || nkp["position"] != float64(2) || nkp["queueDepth"] != float64(5) {
		t.Fatalf("unexpected nkp_queue_status broadcast: %#v all=%#v", nkp, events())
	}
	generic := findBroadcast(events(), "nar_queue", "queue_status")
	if generic == nil || generic["position"] != float64(3) || generic["queueDepth"] != float64(8) || generic["queueMessage"] != "Waiting for capacity" {
		t.Fatalf("unexpected queue_status broadcast: %#v all=%#v", generic, events())
	}
}

func TestCancelBackgroundTaskBroadcastsCancellationAndUpdatesChild(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	parentID := insertNarratorHandlerTestRow(t, sqlDB)
	hub, events := captureNarratorBroadcasts()
	service := &Service{DB: sqlDB, Hub: hub}
	parent, err := service.Get(context.Background(), parentID)
	if err != nil {
		t.Fatalf("load parent: %v", err)
	}
	child, err := service.createSubagentNarrator(context.Background(), parent, "general", "Cancelable", "openai:test", "", "", "", true)
	if err != nil {
		t.Fatalf("create child: %v", err)
	}
	if _, err := sqlDB.Exec(`UPDATE narrators SET status = 'working', background_status = 'running' WHERE id = ?`, child.ID); err != nil {
		t.Fatalf("mark child running: %v", err)
	}
	insertParentAgentToolCallForTest(t, service, parentID, "toolu_cancel")
	if err := service.createBackgroundAgentTask(context.Background(), parentID, child.ID, "general", "Cancelable", "toolu_cancel", ""); err != nil {
		t.Fatalf("create background task: %v", err)
	}

	req := httptestRequest(http.MethodPost, "/narrators/"+parentID+"/background-tasks/"+child.ID+"/cancel")
	w := serveNarratorHandlerWithService(service, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var taskStatus, childStatus string
	if err := sqlDB.QueryRow(`SELECT status FROM background_tasks WHERE id = ?`, child.ID).Scan(&taskStatus); err != nil {
		t.Fatalf("load task status: %v", err)
	}
	if err := sqlDB.QueryRow(`SELECT background_status FROM narrators WHERE id = ?`, child.ID).Scan(&childStatus); err != nil {
		t.Fatalf("load child status: %v", err)
	}
	if taskStatus != "cancelled" || childStatus != "cancelled" {
		t.Fatalf("task/child status = %q/%q", taskStatus, childStatus)
	}
	var toolStatus, toolOutput string
	if err := sqlDB.QueryRow(`SELECT status, output_json FROM narrator_tool_calls WHERE narrator_id = ? AND tool_use_id = 'toolu_cancel'`, parentID).Scan(&toolStatus, &toolOutput); err != nil {
		t.Fatalf("load cancelled parent tool call: %v", err)
	}
	if toolStatus != "error" || !strings.Contains(toolOutput, "cancelled") {
		t.Fatalf("cancelled parent tool call not injected: status=%s output=%s", toolStatus, toolOutput)
	}
	if !hasBroadcast(events(), parentID, "background_task_cancelled") {
		t.Fatalf("expected background_task_cancelled broadcast, got %#v", events())
	}
	if !hasBroadcastStatus(events(), parentID, child.ID, "cancelled") {
		t.Fatalf("expected status_changed cancelled broadcast, got %#v", events())
	}
}

// This locks the current Go subagent foreground mirror broadcast baseline used by
// the frontend. It is not a claim of complete TypeScript parity.
func TestJ08SubagentForegroundDualBroadcastTranscriptGolden(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	parentID := insertNarratorHandlerTestRow(t, sqlDB)
	hub, events := captureNarratorBroadcasts()
	service := &Service{DB: sqlDB, Hub: hub}
	ctx := context.Background()

	parent, err := service.Get(ctx, parentID)
	if err != nil {
		t.Fatalf("load parent: %v", err)
	}
	child, err := service.createSubagentNarrator(ctx, parent, "general", "J08 foreground", "openai:test", "", "", "fg", false)
	if err != nil {
		t.Fatalf("create foreground child: %v", err)
	}
	if _, err := service.addMessageWithBlocksWithCommandAndParentToolUse(ctx, child.ID, "user", "do foreground work", []map[string]any{{"type": "text", "text": "do foreground work"}}, nil, "", "toolu_j08_fg"); err != nil {
		t.Fatalf("seed child parent tool use: %v", err)
	}

	textIndex := 0
	state := &agentRunState{narratorID: child.ID, narrator: child}
	for _, event := range []agentbasic.AgentEvent{
		{Type: "stream_text", Text: "foreground ", OutputIndex: &textIndex},
		{Type: "block_complete", Block: &agentbasic.ContentBlock{Type: "text", Text: "foreground done", OutputIndex: &textIndex}},
		{Type: "assistant_message", Text: "foreground done"},
	} {
		if err := service.handleAgentEvent(ctx, state, event); err != nil {
			t.Fatalf("handle child event %s: %v", event.Type, err)
		}
	}

	childStream := requireJ08Broadcast(t, events(), child.ID, func(payload map[string]any) bool {
		return payload["type"] == "stream_text"
	})
	if childStream["narratorId"] != child.ID || childStream["text"] != "foreground " || intNumber(childStream["outputIndex"]) != 0 {
		t.Fatalf("child stream_text should stay scoped to child narrator, got %#v", childStream)
	}
	if childStream["childNarratorId"] != nil || childStream["parentToolUseId"] != nil {
		t.Fatalf("child stream_text should not be top-level parent mirror, got %#v", childStream)
	}

	childMessage := requireJ08Broadcast(t, events(), child.ID, func(payload map[string]any) bool {
		return payload["type"] == "message"
	})
	childMessageBody, _ := childMessage["message"].(map[string]any)
	if childMessage["narratorId"] != child.ID || childMessage["childNarratorId"] != nil || childMessageBody["narratorId"] != child.ID || childMessageBody["parentToolUseId"] != "toolu_j08_fg" {
		t.Fatalf("child message broadcast shape drifted: payload=%#v message=%#v", childMessage, childMessageBody)
	}

	parentMirror := requireJ08Broadcast(t, events(), parentID, func(payload map[string]any) bool {
		return payload["type"] == "message" && payload["childNarratorId"] == child.ID
	})
	parentMessageBody, _ := parentMirror["message"].(map[string]any)
	if parentMirror["narratorId"] != parentID || parentMirror["parentToolUseId"] != "toolu_j08_fg" || parentMessageBody["narratorId"] != child.ID || parentMessageBody["parentToolUseId"] != "toolu_j08_fg" {
		t.Fatalf("parent child-message mirror shape drifted: payload=%#v message=%#v", parentMirror, parentMessageBody)
	}
}

// This locks the current Go background subagent broadcast frame baseline used by
// the frontend. It is not a claim of complete TypeScript parity.
func TestJ08BackgroundSubagentBroadcastFramesCurrentGoContractGolden(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	parentID := insertNarratorHandlerTestRow(t, sqlDB)
	hub, events := captureNarratorBroadcasts()
	service := &Service{DB: sqlDB, Hub: hub}
	ctx := context.Background()

	parent, err := service.Get(ctx, parentID)
	if err != nil {
		t.Fatalf("load parent: %v", err)
	}
	child, err := service.createSubagentNarrator(ctx, parent, "general", "J08 background", "openai:test", "", "", "bg", true)
	if err != nil {
		t.Fatalf("create background child: %v", err)
	}
	insertParentAgentToolCallForTest(t, service, parentID, "toolu_j08_bg")
	if err := service.createBackgroundAgentTask(ctx, parentID, child.ID, "general", "J08 background", "toolu_j08_bg", "bg"); err != nil {
		t.Fatalf("create background task: %v", err)
	}

	started := requireJ08Broadcast(t, events(), parentID, func(payload map[string]any) bool {
		return payload["type"] == "background_task_started" && payload["taskId"] == child.ID
	})
	if started["narratorId"] != parentID || started["taskNarratorId"] != child.ID || started["toolUseId"] != "toolu_j08_bg" || started["subagentType"] != "general" {
		t.Fatalf("background_task_started frame shape drifted: %#v", started)
	}
	running := requireJ08BackgroundStatusFrame(t, events(), parentID, child.ID, "running")
	if running["narratorId"] != parentID || running["output"] != nil {
		t.Fatalf("background_task_status_changed running shape drifted: %#v", running)
	}

	state := &agentRunState{narratorID: child.ID, narrator: child}
	if err := service.handleAgentEvent(ctx, state, agentbasic.AgentEvent{Type: "stream_text", Text: "delta"}); err != nil {
		t.Fatalf("handle background stream: %v", err)
	}
	childStream := requireJ08Broadcast(t, events(), child.ID, func(payload map[string]any) bool {
		return payload["type"] == "stream_text" && payload["text"] == "delta"
	})
	if childStream["narratorId"] != child.ID {
		t.Fatalf("background child stream_text should keep child narratorId, got %#v", childStream)
	}
	output := requireJ08Broadcast(t, events(), parentID, func(payload map[string]any) bool {
		return payload["type"] == "background_task_output" && payload["taskId"] == child.ID
	})
	if output["narratorId"] != parentID || intNumber(output["outputBytes"]) != len([]byte("delta")) {
		t.Fatalf("background_task_output frame shape drifted: %#v", output)
	}

	if err := service.finishBackgroundTaskRecord(ctx, parentID, child.ID, "completed", "final answer"); err != nil {
		t.Fatalf("finish completed background task: %v", err)
	}
	completed := requireJ08Broadcast(t, events(), parentID, func(payload map[string]any) bool {
		return payload["type"] == "background_task_completed" && payload["taskNarratorId"] == child.ID
	})
	if completed["narratorId"] != parentID || completed["toolUseId"] != "toolu_j08_bg" || completed["resultPreview"] != "final answer" {
		t.Fatalf("background_task_completed frame shape drifted: %#v", completed)
	}
	completedStatus := requireJ08BackgroundStatusFrame(t, events(), parentID, child.ID, "completed")
	if completedStatus["output"] != "final answer" {
		t.Fatalf("background_task_status_changed completed output drifted: %#v", completedStatus)
	}
	completedTool := requireJ08Broadcast(t, events(), parentID, func(payload map[string]any) bool {
		return payload["type"] == "tool_completed" && payload["toolUseId"] == "toolu_j08_bg"
	})
	if completedTool["subagentNarratorId"] != child.ID || completedTool["backgroundStatus"] != "completed" || completedTool["status"] != "success" || completedTool["output"] != "final answer" {
		t.Fatalf("background subagent tool_completed shape drifted: %#v", completedTool)
	}

	failedChild, err := service.createSubagentNarrator(ctx, parent, "general", "J08 failed", "openai:test", "", "", "fail", true)
	if err != nil {
		t.Fatalf("create failed child: %v", err)
	}
	insertParentAgentToolCallForTest(t, service, parentID, "toolu_j08_fail")
	if err := service.createBackgroundAgentTask(ctx, parentID, failedChild.ID, "general", "J08 failed", "toolu_j08_fail", "fail"); err != nil {
		t.Fatalf("create failed task: %v", err)
	}
	if err := service.finishBackgroundTaskRecord(ctx, parentID, failedChild.ID, "failed", "boom"); err != nil {
		t.Fatalf("finish failed background task: %v", err)
	}
	failed := requireJ08Broadcast(t, events(), parentID, func(payload map[string]any) bool {
		return payload["type"] == "background_task_failed" && payload["taskNarratorId"] == failedChild.ID
	})
	if failed["narratorId"] != parentID || failed["toolUseId"] != "toolu_j08_fail" || failed["error"] != "boom" {
		t.Fatalf("background_task_failed frame shape drifted: %#v", failed)
	}
	failedStatus := requireJ08BackgroundStatusFrame(t, events(), parentID, failedChild.ID, "failed")
	if failedStatus["output"] != "boom" {
		t.Fatalf("background_task_status_changed failed output drifted: %#v", failedStatus)
	}

	cancelChild, err := service.createSubagentNarrator(ctx, parent, "general", "J08 cancelled", "openai:test", "", "", "cancel", true)
	if err != nil {
		t.Fatalf("create cancelled child: %v", err)
	}
	insertParentAgentToolCallForTest(t, service, parentID, "toolu_j08_cancel")
	if err := service.createBackgroundAgentTask(ctx, parentID, cancelChild.ID, "general", "J08 cancelled", "toolu_j08_cancel", "cancel"); err != nil {
		t.Fatalf("create cancelled task: %v", err)
	}
	if err := service.finishBackgroundTaskRecord(ctx, parentID, cancelChild.ID, "cancelled", "Cancelled"); err != nil {
		t.Fatalf("finish cancelled background task: %v", err)
	}
	cancelled := requireJ08Broadcast(t, events(), parentID, func(payload map[string]any) bool {
		return payload["type"] == "background_task_cancelled" && payload["taskNarratorId"] == cancelChild.ID
	})
	if cancelled["narratorId"] != parentID || cancelled["toolUseId"] != "toolu_j08_cancel" {
		t.Fatalf("background_task_cancelled frame shape drifted: %#v", cancelled)
	}
	cancelledStatus := requireJ08BackgroundStatusFrame(t, events(), parentID, cancelChild.ID, "cancelled")
	if cancelledStatus["output"] != "Cancelled" {
		t.Fatalf("background_task_status_changed cancelled output drifted: %#v", cancelledStatus)
	}
}

func requireJ08BackgroundStatusFrame(t *testing.T, events []capturedBroadcast, narratorID, taskID, status string) map[string]any {
	t.Helper()
	return requireJ08Broadcast(t, events, narratorID, func(payload map[string]any) bool {
		return payload["type"] == "background_task_status_changed" && payload["taskId"] == taskID && payload["status"] == status
	})
}

func requireJ08Broadcast(t *testing.T, events []capturedBroadcast, narratorID string, match func(map[string]any) bool) map[string]any {
	t.Helper()
	for _, event := range events {
		if event.NarratorID == narratorID && match(event.Payload) {
			return event.Payload
		}
	}
	t.Fatalf("missing J08 broadcast for narrator %s in %#v", narratorID, events)
	return nil
}

type capturedBroadcast struct {
	NarratorID string
	Payload    map[string]any
}

func insertParentAgentToolCallForTest(t *testing.T, service *Service, parentID, toolUseID string) {
	t.Helper()
	assistant, err := service.addMessageWithBlocks(context.Background(), parentID, "assistant", "launch", []map[string]any{{"type": "tool_use", "id": toolUseID, "name": "Agent", "input": map[string]any{"prompt": "go"}}}, nil)
	if err != nil {
		t.Fatalf("insert parent assistant message: %v", err)
	}
	if _, err := service.DB.ExecContext(context.Background(), `INSERT INTO narrator_tool_calls (id, narrator_id, message_id, tool_use_id, tool_name, input_json, output_json, status, created_at) VALUES (?, ?, ?, ?, 'Agent', '{"prompt":"go"}', '{}', 'running', ?)`, "tc_"+toolUseID, parentID, assistant.ID, toolUseID, "2026-01-01T00:00:00Z"); err != nil {
		t.Fatalf("insert parent tool call: %v", err)
	}
}

func captureNarratorBroadcasts() (*ws.Hub, func() []capturedBroadcast) {
	hub := ws.NewHub()
	events := []capturedBroadcast{}
	hub.SetNarratorBroadcastHook(func(narratorID string, payload any, data []byte) {
		decoded := map[string]any{}
		if err := json.Unmarshal(data, &decoded); err != nil {
			return
		}
		events = append(events, capturedBroadcast{NarratorID: narratorID, Payload: decoded})
	})
	return hub, func() []capturedBroadcast { return append([]capturedBroadcast(nil), events...) }
}

func hasBroadcast(events []capturedBroadcast, narratorID string, eventType string) bool {
	return findBroadcast(events, narratorID, eventType) != nil
}

func hasBroadcastStatus(events []capturedBroadcast, narratorID string, taskID string, status string) bool {
	for _, event := range events {
		if event.NarratorID == narratorID && event.Payload["type"] == "background_task_status_changed" && event.Payload["taskId"] == taskID && event.Payload["status"] == status {
			return true
		}
	}
	return false
}

func findBroadcast(events []capturedBroadcast, narratorID string, eventType string) map[string]any {
	for _, event := range events {
		if event.NarratorID == narratorID && event.Payload["type"] == eventType {
			return event.Payload
		}
	}
	return nil
}

func httptestRequest(method string, path string) *http.Request {
	return httptest.NewRequest(method, path, nil)
}

func serveNarratorHandlerWithService(service *Service, req *http.Request) *httptest.ResponseRecorder {
	h := NewHandler(service)
	r := chi.NewRouter()
	r.Route("/narrators", h.RegisterRoutes)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}
