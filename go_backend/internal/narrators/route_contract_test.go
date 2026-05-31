package narrators

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"narrafork-go/internal/agentbasic"
	"narrafork-go/internal/ws"
)

func TestSubagentPendingPermissionBroadcastsToParentNarrator(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	parentID := insertNarratorHandlerTestRow(t, sqlDB)
	now := "2026-05-18T00:00:00Z"
	childID := "nar_sub_permission"
	if _, err := sqlDB.Exec(`INSERT INTO narrators (
		id, type, title, inherit_mode, parent_narrator_id, model, permission_mode, status, substatus, plan_mode,
		cwd, variant, traits, is_background, is_ask_in_passing, message_version, message_count,
		total_cost_usd, fast_mode, relaxed_plan, plan_reflection_auto_approve_override,
		danger_reflection_override, created_at, updated_at
	) VALUES (?, 'subagent', 'Permission child', 'fresh', ?, 'openai:test', 'default', 'idle', '[]', 0, '', 'subagent', '[]', 0, 0, 0, 0, 0, 0, 0, 'inherit', 'inherit', ?, ?)`, childID, parentID, now, now); err != nil {
		t.Fatalf("insert child narrator: %v", err)
	}
	service := &Service{DB: sqlDB}
	child, err := service.Get(context.Background(), childID)
	if err != nil {
		t.Fatalf("load child narrator: %v", err)
	}
	hub, events := captureNarratorBroadcasts()
	service.Hub = hub
	call := agentbasic.ToolCall{ID: "tool-sub-perm", Type: "function"}
	call.Function.Name = "Write"
	call.Function.Arguments = `{"file_path":"child.txt","content":"x"}`
	if err := service.recordPendingToolCall(context.Background(), childID, "msg-sub-perm", child, call, "needs approval"); err != nil {
		t.Fatalf("record pending subagent tool call: %v", err)
	}
	broadcasts := events()
	if len(broadcasts) != 1 || broadcasts[0].NarratorID != parentID || broadcasts[0].Payload["narratorId"] != parentID || broadcasts[0].Payload["type"] != "permission_request" {
		t.Fatalf("expected subagent permission request broadcast to parent, got %#v", broadcasts)
	}
	targetID, toolUseID, err := NewHandler(service).permissionTarget(context.Background(), "tool-sub-perm")
	if err != nil {
		t.Fatalf("resolve permission target: %v", err)
	}
	if targetID != parentID || toolUseID != "tool-sub-perm" {
		t.Fatalf("expected permission target parent/tool, got target=%q tool=%q", targetID, toolUseID)
	}
}

func TestParentPermissionsListIncludesSubagentPendingPermission(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	parentID := insertNarratorHandlerTestRow(t, sqlDB)
	now := "2026-05-18T00:00:00Z"
	childID := "nar_sub_permission_list"
	if _, err := sqlDB.Exec(`INSERT INTO narrators (
		id, type, title, inherit_mode, parent_narrator_id, model, permission_mode, status, substatus, plan_mode,
		cwd, variant, traits, is_background, is_ask_in_passing, message_version, message_count,
		total_cost_usd, fast_mode, relaxed_plan, plan_reflection_auto_approve_override,
		danger_reflection_override, created_at, updated_at
	) VALUES (?, 'subagent', 'Permission list child', 'fresh', ?, 'openai:test', 'default', 'waiting', '["awaiting_permission"]', 0, '', 'subagent', '[]', 0, 0, 0, 0, 0, 0, 0, 'inherit', 'inherit', ?, ?)`, childID, parentID, now, now); err != nil {
		t.Fatalf("insert child narrator: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO narrator_tool_calls (id, narrator_id, tool_use_id, tool_name, input_json, status, permission_decision_reason, created_at) VALUES ('req-sub-list', ?, 'tool-sub-list', 'Write', '{"file_path":"child.txt"}', 'pending', 'needs parent approval', ?)`, childID, now); err != nil {
		t.Fatalf("insert child pending permission: %v", err)
	}
	w := serveNarratorHandlerRequest(sqlDB, http.MethodGet, "/narrators/"+parentID+"/permissions")
	if w.Code != http.StatusOK {
		t.Fatalf("expected permissions 200, got %d body=%s", w.Code, w.Body.String())
	}
	var items []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode permissions response: %v", err)
	}
	if len(items) != 1 || items[0]["subagentNarratorId"] != childID || items[0]["toolUseId"] != "tool-sub-list" || items[0]["decisionReason"] != "needs parent approval" {
		t.Fatalf("expected parent list to include subagent pending permission, got %#v", items)
	}
}

func TestParentCanReadSubagentToolCallDetailAndPermissionPreview(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	parentID := insertNarratorHandlerTestRow(t, sqlDB)
	now := "2026-05-18T00:00:00Z"
	childID := "nar_sub_tool_detail"
	childCwd := t.TempDir()
	if err := os.WriteFile(childCwd+"/child.txt", []byte("old"), 0o644); err != nil {
		t.Fatalf("seed child file: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO narrators (
		id, type, title, inherit_mode, parent_narrator_id, model, permission_mode, status, substatus, plan_mode,
		cwd, variant, traits, is_background, is_ask_in_passing, message_version, message_count,
		total_cost_usd, fast_mode, relaxed_plan, plan_reflection_auto_approve_override,
		danger_reflection_override, created_at, updated_at
	) VALUES (?, 'subagent', 'Tool detail child', 'fresh', ?, 'openai:test', 'default', 'waiting', '["awaiting_permission"]', 0, ?, 'subagent', '[]', 0, 0, 0, 0, 0, 0, 0, 'inherit', 'inherit', ?, ?)`, childID, parentID, childCwd, now, now); err != nil {
		t.Fatalf("insert child narrator: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO narrator_tool_calls (id, narrator_id, tool_use_id, tool_name, input_json, status, created_at) VALUES ('req-sub-detail', ?, 'tool-sub-detail', 'Write', '{"file_path":"child.txt","content":"new"}', 'pending', ?)`, childID, now); err != nil {
		t.Fatalf("insert child tool call: %v", err)
	}
	detail := serveNarratorHandlerRequest(sqlDB, http.MethodGet, "/narrators/"+parentID+"/tool-calls/tool-sub-detail")
	if detail.Code != http.StatusOK {
		t.Fatalf("expected parent tool detail 200, got %d body=%s", detail.Code, detail.Body.String())
	}
	var detailBody map[string]any
	if err := json.Unmarshal(detail.Body.Bytes(), &detailBody); err != nil {
		t.Fatalf("decode detail response: %v", err)
	}
	if detailBody["subagentNarratorId"] != childID {
		t.Fatalf("expected detail to include subagent narrator id, got %#v", detailBody)
	}
	preview := serveNarratorHandlerRequest(sqlDB, http.MethodGet, "/narrators/"+parentID+"/permission-file-preview?toolUseId=tool-sub-detail")
	if preview.Code != http.StatusOK {
		t.Fatalf("expected parent permission preview 200, got %d body=%s", preview.Code, preview.Body.String())
	}
	var previewBody map[string]any
	if err := json.Unmarshal(preview.Body.Bytes(), &previewBody); err != nil {
		t.Fatalf("decode preview response: %v", err)
	}
	filePath, _ := previewBody["filePath"].(string)
	if !strings.HasSuffix(filePath, "/child.txt") || previewBody["previewContent"] != "new" {
		t.Fatalf("expected preview to use child cwd/file path, got %#v", previewBody)
	}
}

func TestParentBypassReprocessIncludesSubagentPendingPermission(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	parentID := insertNarratorHandlerTestRow(t, sqlDB)
	if _, err := sqlDB.Exec(`UPDATE narrators SET permission_mode = 'bypassPermissions' WHERE id = ?`, parentID); err != nil {
		t.Fatalf("set parent bypass: %v", err)
	}
	now := "2026-05-18T00:00:00Z"
	childID := "nar_sub_reprocess"
	if _, err := sqlDB.Exec(`INSERT INTO narrators (
		id, type, title, inherit_mode, parent_narrator_id, model, permission_mode, status, substatus, plan_mode,
		cwd, variant, traits, is_background, is_ask_in_passing, message_version, message_count,
		total_cost_usd, fast_mode, relaxed_plan, plan_reflection_auto_approve_override,
		danger_reflection_override, created_at, updated_at
	) VALUES (?, 'subagent', 'Reprocess child', 'fresh', ?, 'openai:test', 'bypassPermissions', 'waiting', '["awaiting_permission"]', 0, '', 'subagent', '[]', 0, 0, 0, 0, 0, 0, 0, 'inherit', 'inherit', ?, ?)`, childID, parentID, now, now); err != nil {
		t.Fatalf("insert child narrator: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO narrator_tool_calls (id, narrator_id, tool_use_id, tool_name, input_json, status, created_at) VALUES ('req-sub-reprocess', ?, 'tool-sub-reprocess', 'Write', '{"file_path":"child.txt","content":"x"}', 'pending', ?)`, childID, now); err != nil {
		t.Fatalf("insert child pending permission: %v", err)
	}
	hub, events := captureNarratorBroadcasts()
	service := &Service{DB: sqlDB, Hub: hub}
	count, err := service.reprocessPendingPermissionsForBypass(context.Background(), parentID)
	if err != nil {
		t.Fatalf("reprocess parent bypass permissions: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected one subagent pending permission reprocessed, got %d", count)
	}
	var status string
	if err := sqlDB.QueryRow(`SELECT status FROM narrator_tool_calls WHERE id = 'req-sub-reprocess'`).Scan(&status); err != nil {
		t.Fatalf("load child tool status: %v", err)
	}
	if status != "running" {
		t.Fatalf("expected child pending permission to become running, got %q", status)
	}
	broadcasts := events()
	if len(broadcasts) != 1 || broadcasts[0].NarratorID != parentID || broadcasts[0].Payload["decision"] != "allow" || broadcasts[0].Payload["subagentNarratorId"] != childID {
		t.Fatalf("expected parent permission_resolved allow broadcast with subagent id, got %#v", broadcasts)
	}
}

func TestPermissionTargetReturnsQueryErrors(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sqlDB.Close()
	for _, stmt := range []string{
		`CREATE TABLE narrator_tool_calls (id TEXT PRIMARY KEY, tool_use_id TEXT, created_at TEXT)`,
		`INSERT INTO narrator_tool_calls (id, tool_use_id, created_at) VALUES ('req-1', 'tool-1', 'now')`,
	} {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("prepare permission target fixture: %v", err)
		}
	}
	h := NewHandler(&Service{DB: sqlDB})

	_, _, err = h.permissionTarget(context.Background(), "req-1")
	if err == nil || !strings.Contains(err.Error(), "query permission target") {
		t.Fatalf("expected permission target query error, got %v", err)
	}
}

func TestPermissionDecisionMergesAnswersAndBroadcastsResolved(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	service := &Service{DB: sqlDB}
	assistant, err := service.addMessageWithBlocks(context.Background(), narratorID, "assistant", "needs permission", []map[string]any{{"type": "tool_use", "id": "tool-approve", "name": "Write", "input": map[string]any{"file_path": "old.txt"}}}, nil)
	if err != nil {
		t.Fatalf("add assistant: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO narrator_tool_calls (id, narrator_id, message_id, tool_use_id, tool_name, input_json, status, created_at) VALUES ('req-approve', ?, ?, 'tool-approve', 'Write', '{"file_path":"old.txt"}', 'pending', '2026-05-18T00:00:00Z')`, narratorID, assistant.ID); err != nil {
		t.Fatalf("insert pending tool call: %v", err)
	}
	hub, events := newPermissionBroadcastRecorder()
	h := NewHandler(&Service{DB: sqlDB, Hub: hub})
	router := chi.NewRouter()
	h.RegisterRoutes(router)
	req := httptest.NewRequest(http.MethodPost, "/permissions/req-approve/approve", strings.NewReader(`{"reason":"looks safe","answers":{"file_path":"new.txt"}}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected approve 200, got %d body=%s", w.Code, w.Body.String())
	}
	var status, reason, inputRaw string
	if err := sqlDB.QueryRow(`SELECT status, permission_decision_reason, input_json FROM narrator_tool_calls WHERE id = 'req-approve'`).Scan(&status, &reason, &inputRaw); err != nil {
		t.Fatalf("load approved tool call: %v", err)
	}
	if status != "running" || reason != "looks safe" || !strings.Contains(inputRaw, "new.txt") {
		t.Fatalf("approve should update running status/reason/input, status=%s reason=%q input=%s", status, reason, inputRaw)
	}
	if len(*events) != 1 || (*events)[0].NarratorID != narratorID || (*events)[0].Payload["type"] != "permission_resolved" || (*events)[0].Payload["decision"] != "allow" {
		t.Fatalf("expected permission_resolved allow broadcast, got %#v", *events)
	}
	updated, _ := (*events)[0].Payload["updatedInput"].(map[string]any)
	answers, _ := updated["answers"].(map[string]any)
	if answers["file_path"] != "new.txt" {
		t.Fatalf("permission_resolved should include updated input answers, got %#v", (*events)[0].Payload)
	}
}

func TestPermissionDenyBroadcastsResolvedAndPreservesPendingSideEffectState(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	if _, err := sqlDB.Exec(`INSERT INTO narrator_messages (id, narrator_id, role, content_json, content_text, created_at) VALUES ('deny-msg', ?, 'assistant', '[]', '', '2026-05-18T00:00:00Z')`, narratorID); err != nil {
		t.Fatalf("insert deny message: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO narrator_tool_calls (id, narrator_id, message_id, tool_use_id, tool_name, input_json, status, created_at) VALUES ('req-deny', ?, 'deny-msg', 'tool-deny', 'Write', '{"file_path":"blocked.txt"}', 'pending', '2026-05-18T00:00:00Z')`, narratorID); err != nil {
		t.Fatalf("insert pending deny tool call: %v", err)
	}
	hub, events := newPermissionBroadcastRecorder()
	h := NewHandler(&Service{DB: sqlDB, Hub: hub})
	router := chi.NewRouter()
	h.RegisterRoutes(router)
	req := httptest.NewRequest(http.MethodPost, "/permissions/req-deny/deny", strings.NewReader(`{"message":"do not write"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected deny 200, got %d body=%s", w.Code, w.Body.String())
	}
	var status, denyMessage sql.NullString
	if err := sqlDB.QueryRow(`SELECT status, permission_deny_message FROM narrator_tool_calls WHERE id = 'req-deny'`).Scan(&status, &denyMessage); err != nil {
		t.Fatalf("load denied tool call: %v", err)
	}
	if status.String != "fail" || !denyMessage.Valid || denyMessage.String != "do not write" {
		t.Fatalf("deny should mark tool call fail with message, status=%#v message=%#v", status, denyMessage)
	}
	if len(*events) != 1 || (*events)[0].Payload["decision"] != "deny" || (*events)[0].Payload["toolUseId"] != "tool-deny" {
		t.Fatalf("expected permission_resolved deny broadcast, got %#v", *events)
	}
	if _, ok := (*events)[0].Payload["updatedInput"]; ok {
		t.Fatalf("permission_resolved deny should omit updatedInput when unchanged, got %#v", (*events)[0].Payload)
	}
}

func TestStopDangerReflectionHandlesPersistedStateWithoutRuntime(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	suggestions := `[{"type":"danger_reflection","status":"running","requestId":"req-danger","danger":{"summary":"Risk"},"startedAt":"2026-05-18T00:00:00Z"}]`
	if _, err := sqlDB.Exec(`INSERT INTO narrator_tool_calls (id, narrator_id, tool_use_id, tool_name, input_json, status, permission_suggestions, created_at) VALUES ('req-danger', ?, 'tool-danger', 'Bash', '{"command":"rm -rf tmp"}', 'pending', ?, '2026-05-18T00:00:00Z')`, narratorID, suggestions); err != nil {
		t.Fatalf("insert danger reflection tool call: %v", err)
	}
	hub, events := newPermissionBroadcastRecorder()
	h := NewHandler(&Service{DB: sqlDB, Hub: hub})
	router := chi.NewRouter()
	h.RegisterRoutes(router)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/permissions/req-danger/stop-reflection", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("expected stop reflection 200, got %d body=%s", w.Code, w.Body.String())
	}
	var status, reason, updatedSuggestions string
	if err := sqlDB.QueryRow(`SELECT status, COALESCE(permission_decision_reason, ''), COALESCE(permission_suggestions, '') FROM narrator_tool_calls WHERE id = 'req-danger'`).Scan(&status, &reason, &updatedSuggestions); err != nil {
		t.Fatalf("load stopped danger reflection: %v", err)
	}
	if status != "fail" || reason == "" || !strings.Contains(updatedSuggestions, `"status":"aborted"`) {
		t.Fatalf("danger reflection should be aborted, status=%s reason=%q suggestions=%s", status, reason, updatedSuggestions)
	}
	if len(*events) != 1 || (*events)[0].Payload["type"] != "danger_reflection_resolved" || (*events)[0].Payload["decision"] != "aborted" {
		t.Fatalf("expected danger_reflection_resolved aborted broadcast, got %#v", *events)
	}

	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/permissions/req-danger/stop-reflection", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("expected idempotent stop reflection 200, got %d body=%s", w.Code, w.Body.String())
	}
	if len(*events) != 2 || (*events)[1].Payload["type"] != "danger_reflection_resolved" || (*events)[1].Payload["decision"] != "aborted" {
		t.Fatalf("expected idempotent resolved broadcast, got %#v", *events)
	}
}

func TestRecoverOnStartupAbortsStaleReflectionSuggestions(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	if _, err := sqlDB.Exec(`UPDATE narrators SET status = 'waiting', substatus = '["reflecting"]' WHERE id = ?`, narratorID); err != nil {
		t.Fatalf("mark narrator waiting: %v", err)
	}
	suggestions := `[{"type":"plan_reflection","status":"running","requestId":"req-plan","startedAt":"2026-05-18T00:00:00Z"}]`
	if _, err := sqlDB.Exec(`INSERT INTO narrator_tool_calls (id, narrator_id, tool_use_id, tool_name, input_json, status, permission_suggestions, created_at) VALUES ('req-plan', ?, 'tool-plan', 'ExitPlanMode', '{}', 'pending', ?, '2026-05-18T00:00:00Z')`, narratorID, suggestions); err != nil {
		t.Fatalf("insert stale reflection tool call: %v", err)
	}
	if err := (&Service{DB: sqlDB}).RecoverOnStartup(context.Background()); err != nil {
		t.Fatalf("recover on startup: %v", err)
	}
	var narratorStatus, substatus, toolStatus, updatedSuggestions string
	if err := sqlDB.QueryRow(`SELECT status, substatus FROM narrators WHERE id = ?`, narratorID).Scan(&narratorStatus, &substatus); err != nil {
		t.Fatalf("load recovered narrator: %v", err)
	}
	if err := sqlDB.QueryRow(`SELECT status, COALESCE(permission_suggestions, '') FROM narrator_tool_calls WHERE id = 'req-plan'`).Scan(&toolStatus, &updatedSuggestions); err != nil {
		t.Fatalf("load recovered tool call: %v", err)
	}
	if narratorStatus != "idle" || !strings.Contains(substatus, "interrupted") || toolStatus != "fail" || !strings.Contains(updatedSuggestions, `"status":"aborted"`) {
		t.Fatalf("unexpected recovery state narrator=%s substatus=%s tool=%s suggestions=%s", narratorStatus, substatus, toolStatus, updatedSuggestions)
	}
}

type permissionBroadcastEvent struct {
	NarratorID string
	Payload    map[string]any
}

func newPermissionBroadcastRecorder() (*ws.Hub, *[]permissionBroadcastEvent) {
	hub := ws.NewHub()
	events := []permissionBroadcastEvent{}
	hub.SetNarratorBroadcastHook(func(narratorID string, payload any, _ []byte) {
		payloadMap, _ := payload.(map[string]any)
		events = append(events, permissionBroadcastEvent{NarratorID: narratorID, Payload: payloadMap})
	})
	return hub, &events
}

func TestPlanRouteStoresAndReadsPlanTrait(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)

	w := serveNarratorHandlerJSON(sqlDB, http.MethodPost, "/narrators/"+narratorID+"/plan", map[string]any{"plan": "Ship the route contract tests."})
	if w.Code != http.StatusOK {
		t.Fatalf("expected plan write 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode plan write: %v", err)
	}
	if body["plan"] != "Ship the route contract tests." {
		t.Fatalf("unexpected plan response: %#v", body)
	}
	state, _ := body["state"].(map[string]any)
	if state["source"] != "api" || state["updatedAt"] == "" {
		t.Fatalf("plan state should include api source and updatedAt: %#v", state)
	}

	w = serveNarratorHandlerJSON(sqlDB, http.MethodPost, "/narrators/"+narratorID+"/plan", map[string]any{})
	if w.Code != http.StatusOK {
		t.Fatalf("expected plan read 200, got %d body=%s", w.Code, w.Body.String())
	}
	body = map[string]any{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode plan read: %v", err)
	}
	if body["plan"] != "Ship the route contract tests." {
		t.Fatalf("plan read should return stored content: %#v", body)
	}
}

func TestPlanModeRoutesReturnTSCompatiblePayloads(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	if _, err := sqlDB.Exec(`UPDATE narrators SET permission_mode = 'bypassPermissions' WHERE id = ?`, narratorID); err != nil {
		t.Fatalf("seed permission: %v", err)
	}

	w := serveNarratorHandlerJSON(sqlDB, http.MethodPost, "/narrators/"+narratorID+"/plan-mode/enter", map[string]any{})
	if w.Code != http.StatusOK {
		t.Fatalf("expected enter plan mode 200, got %d body=%s", w.Code, w.Body.String())
	}
	var entered map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &entered); err != nil {
		t.Fatalf("decode enter response: %v", err)
	}
	if entered["ok"] != true || entered["planMode"] != true || entered["previousPermissionMode"] != "bypassPermissions" || entered["planFileId"] == "" || entered["planFile"] == "" {
		t.Fatalf("unexpected enter plan-mode payload: %#v", entered)
	}

	w = serveNarratorHandlerJSON(sqlDB, http.MethodPost, "/narrators/"+narratorID+"/plan-mode/exit", map[string]any{})
	if w.Code != http.StatusOK {
		t.Fatalf("expected exit plan mode 200, got %d body=%s", w.Code, w.Body.String())
	}
	var exited map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &exited); err != nil {
		t.Fatalf("decode exit response: %v", err)
	}
	if exited["ok"] != true || exited["planMode"] != false {
		t.Fatalf("unexpected exit plan-mode payload: %#v", exited)
	}
	var permission string
	var planMode int
	var planFile sql.NullString
	if err := sqlDB.QueryRow(`SELECT permission_mode, plan_mode, plan_file_id FROM narrators WHERE id = ?`, narratorID).Scan(&permission, &planMode, &planFile); err != nil {
		t.Fatalf("load narrator plan fields: %v", err)
	}
	if permission != "bypassPermissions" || planMode != 0 || planFile.Valid {
		t.Fatalf("plan mode exit should restore permission and clear plan file, got permission=%q planMode=%d planFile=%#v", permission, planMode, planFile)
	}
}

func TestInterruptReturnsTSCompatiblePayload(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	if _, err := sqlDB.Exec(`UPDATE narrators SET status = 'working', substatus = '[]' WHERE id = ?`, narratorID); err != nil {
		t.Fatalf("seed working narrator: %v", err)
	}

	w := serveNarratorHandlerJSON(sqlDB, http.MethodPost, "/narrators/"+narratorID+"/interrupt", map[string]any{})
	if w.Code != http.StatusOK {
		t.Fatalf("expected interrupt 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode interrupt response: %v", err)
	}
	if body["interrupted"] != true || body["id"] != nil {
		t.Fatalf("interrupt should return TS-compatible {interrupted}, got %#v", body)
	}
	var status, substatus string
	if err := sqlDB.QueryRow(`SELECT status, substatus FROM narrators WHERE id = ?`, narratorID).Scan(&status, &substatus); err != nil {
		t.Fatalf("load interrupted narrator: %v", err)
	}
	if status != "idle" || substatus != `["interrupted"]` {
		t.Fatalf("unexpected interrupted narrator state: status=%s substatus=%s", status, substatus)
	}
}

func TestInterruptReturnsErrorWhenStatusUpdateFails(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	if _, err := sqlDB.Exec(`UPDATE narrators SET status = 'working' WHERE id = ?`, narratorID); err != nil {
		t.Fatalf("seed working narrator: %v", err)
	}
	if _, err := sqlDB.Exec(`CREATE TRIGGER fail_interrupt_update BEFORE UPDATE OF status ON narrators BEGIN SELECT RAISE(ABORT, 'interrupt update blocked'); END;`); err != nil {
		t.Fatalf("create interrupt trigger: %v", err)
	}

	w := serveNarratorHandlerJSON(sqlDB, http.MethodPost, "/narrators/"+narratorID+"/interrupt", map[string]any{})
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected interrupt update failure to return 500, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestCancelBackgroundTaskReturnsErrorWhenChildUpdateFails(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	parentID := insertNarratorHandlerTestRow(t, sqlDB)
	childID := insertChildNarratorTestRow(t, sqlDB, "child_bg")
	if _, err := sqlDB.Exec(`UPDATE narrators SET parent_narrator_id = ?, is_background = 1, status = 'working', background_status = 'running' WHERE id = ?`, parentID, childID); err != nil {
		t.Fatalf("seed background child narrator: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO background_tasks (id, parent_narrator_id, type, status, subagent_narrator_id, tool_use_id, output, started_at, created_at, updated_at) VALUES ('task_bg', ?, 'agent', 'running', ?, 'tool-1', 'partial output', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`, parentID, childID); err != nil {
		t.Fatalf("insert background task: %v", err)
	}
	if _, err := sqlDB.Exec(`CREATE TRIGGER fail_background_child_cancel BEFORE UPDATE OF background_status ON narrators BEGIN SELECT RAISE(ABORT, 'background child update blocked'); END;`); err != nil {
		t.Fatalf("create background cancel trigger: %v", err)
	}

	w := serveNarratorHandlerJSON(sqlDB, http.MethodPost, "/narrators/"+parentID+"/background-tasks/task_bg/cancel", map[string]any{})
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected background child update failure to return 500, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestAskInPassingPendingResolveContract(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)

	start := serveNarratorHandlerJSON(sqlDB, http.MethodPost, "/narrators/"+narratorID+"/ask-in-passing/start", map[string]any{"sourceMessageId": "source-msg", "sourceMessageUuid": "uuid-1"})
	if start.Code != http.StatusCreated {
		t.Fatalf("expected ask-in-passing start 201, got %d body=%s", start.Code, start.Body.String())
	}
	var startBody map[string]any
	if err := json.Unmarshal(start.Body.Bytes(), &startBody); err != nil {
		t.Fatalf("decode start response: %v", err)
	}
	pendingID := startBody["messageId"].(string)
	var pendingRole, pendingText, pendingJSON string
	if err := sqlDB.QueryRow(`SELECT role, content_text, content_json FROM narrator_messages WHERE narrator_id = ? AND id = ?`, narratorID, pendingID).Scan(&pendingRole, &pendingText, &pendingJSON); err != nil {
		t.Fatalf("load pending message: %v", err)
	}
	if pendingRole != "system" || pendingText != "[Ask in passing]" {
		t.Fatalf("unexpected pending role/text: %s %q", pendingRole, pendingText)
	}
	var pendingBlocks []map[string]any
	if err := json.Unmarshal([]byte(pendingJSON), &pendingBlocks); err != nil {
		t.Fatalf("decode pending content: %v", err)
	}
	if len(pendingBlocks) != 1 || pendingBlocks[0]["type"] != "ask_in_passing" || pendingBlocks[0]["status"] != "pending" || pendingBlocks[0]["sourceMessageId"] != "source-msg" || pendingBlocks[0]["sourceMessageUuid"] != "uuid-1" {
		t.Fatalf("unexpected pending block: %#v", pendingBlocks)
	}

	question := "Why does this fail?"
	resolved := serveNarratorHandlerJSON(sqlDB, http.MethodPost, "/narrators/"+narratorID+"/ask-in-passing", map[string]any{"question": question, "pendingMessageId": pendingID})
	if resolved.Code != http.StatusCreated {
		t.Fatalf("expected ask-in-passing resolve 201, got %d body=%s", resolved.Code, resolved.Body.String())
	}
	var resolvedBody map[string]any
	if err := json.Unmarshal(resolved.Body.Bytes(), &resolvedBody); err != nil {
		t.Fatalf("decode resolve response: %v", err)
	}
	childID := resolvedBody["id"].(string)
	var resolvedText, resolvedJSON string
	if err := sqlDB.QueryRow(`SELECT content_text, content_json FROM narrator_messages WHERE narrator_id = ? AND id = ?`, narratorID, pendingID).Scan(&resolvedText, &resolvedJSON); err != nil {
		t.Fatalf("load resolved pending message: %v", err)
	}
	var resolvedBlocks []map[string]any
	if err := json.Unmarshal([]byte(resolvedJSON), &resolvedBlocks); err != nil {
		t.Fatalf("decode resolved content: %v", err)
	}
	if resolvedText != "[Ask in passing] "+question || len(resolvedBlocks) != 1 || resolvedBlocks[0]["status"] != "resolved" || resolvedBlocks[0]["targetNarratorId"] != childID || resolvedBlocks[0]["question"] != question {
		t.Fatalf("unexpected resolved pending message: text=%q blocks=%#v", resolvedText, resolvedBlocks)
	}
	var permissionMode, traits string
	var isAsk, childMessageCount int
	if err := sqlDB.QueryRow(`SELECT permission_mode, is_ask_in_passing, traits, message_count FROM narrators WHERE id = ?`, childID).Scan(&permissionMode, &isAsk, &traits, &childMessageCount); err != nil {
		t.Fatalf("load child narrator: %v", err)
	}
	if permissionMode != "readOnly" || isAsk != 1 || childMessageCount < 1 {
		t.Fatalf("unexpected child ask-in-passing flags: perm=%s isAsk=%d count=%d", permissionMode, isAsk, childMessageCount)
	}
	var traitList []string
	if err := json.Unmarshal([]byte(traits), &traitList); err != nil {
		t.Fatalf("decode child traits: %v", err)
	}
	if !stringSliceContains(traitList, "ask-in-passing") || !stringSliceContains(traitList, "standalone") {
		t.Fatalf("child traits should include standalone and ask-in-passing, got %#v", traitList)
	}
	var childQuestion string
	if err := sqlDB.QueryRow(`SELECT content_text FROM narrator_messages WHERE narrator_id = ? AND role = 'user'`, childID).Scan(&childQuestion); err != nil {
		t.Fatalf("load child question message: %v", err)
	}
	if childQuestion != question {
		t.Fatalf("child user message should contain question, got %q", childQuestion)
	}
}

func TestPermissionModeRejectsAskInPassingNarratorUntilPromoted(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	if _, err := sqlDB.Exec(`UPDATE narrators SET permission_mode = 'readOnly', is_ask_in_passing = 1, traits = '["standalone","ask-in-passing"]' WHERE id = ?`, narratorID); err != nil {
		t.Fatalf("mark ask-in-passing narrator: %v", err)
	}

	w := serveNarratorHandlerJSON(sqlDB, http.MethodPatch, "/narrators/"+narratorID+"/permission-mode", map[string]any{"permissionMode": "bypassPermissions"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected ask-in-passing permission change to return 400, got %d body=%s", w.Code, w.Body.String())
	}
	var permissionMode string
	if err := sqlDB.QueryRow(`SELECT permission_mode FROM narrators WHERE id = ?`, narratorID).Scan(&permissionMode); err != nil {
		t.Fatalf("load permission mode: %v", err)
	}
	if permissionMode != "readOnly" {
		t.Fatalf("permission mode should remain readOnly, got %q", permissionMode)
	}
}

func TestCancelAskInPassingRejectsNonPendingMessage(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	service := &Service{DB: sqlDB}
	msg, err := service.addMessage(context.Background(), narratorID, "system", "ordinary system message", nil)
	if err != nil {
		t.Fatalf("insert ordinary system message: %v", err)
	}

	w := serveNarratorHandlerRequest(sqlDB, http.MethodDelete, "/narrators/"+narratorID+"/ask-in-passing/"+msg.ID)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected cancel of non-pending ask-in-passing to return 400, got %d body=%s", w.Code, w.Body.String())
	}
	var refCount int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM narrator_message_refs WHERE narrator_id = ? AND message_id = ?`, narratorID, msg.ID).Scan(&refCount); err != nil {
		t.Fatalf("count ordinary ref: %v", err)
	}
	if refCount != 1 {
		t.Fatalf("ordinary message ref should remain after rejected cancel, count=%d", refCount)
	}
}

func stringSliceContains(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func TestBrowserSessionListReturnsFrontendMillisecondShape(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	createBrowserSessionsTestTable(t, sqlDB)
	if _, err := sqlDB.Exec(`INSERT INTO narrator_browser_sessions (id, narrator_id, status, url, title, ttl_seconds, expires_at, tracing_status, tracing_json, metadata_json, created_at, updated_at) VALUES ('browser-1', ?, 'active', 'https://example.test', 'Example', 60, '2999-01-01T00:00:00Z', 'active', '{"startedAt": 1700000000000}', '{"networkRequestCount":3,"networkCaptureEnabled":true}', '2026-01-01T00:00:00Z', '2026-01-01T00:00:01Z')`, narratorID); err != nil {
		t.Fatalf("insert browser session: %v", err)
	}
	w := serveNarratorHandlerRequestWithBrowserStorage(sqlDB, http.MethodGet, "/narrators/"+narratorID+"/browser-sessions")
	if w.Code != http.StatusOK {
		t.Fatalf("list browser sessions status=%d body=%s", w.Code, w.Body.String())
	}
	var items []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode sessions: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items=%#v", items)
	}
	item := items[0]
	if item["ttlMs"] != float64(60000) || item["expiresAt"] == nil || item["lastActivity"] == nil || item["tracing"] == nil || item["networkRequestCount"] != float64(3) || item["networkCaptureEnabled"] != true {
		t.Fatalf("browser session item should expose frontend ms shape, got %#v", item)
	}
}

func TestBrowserSessionListReturnsErrorWhenExpireUpdateFails(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	createBrowserSessionsTestTable(t, sqlDB)
	if _, err := sqlDB.Exec(`INSERT INTO narrator_browser_sessions (id, narrator_id, status, url, title, ttl_seconds, expires_at, created_at, updated_at) VALUES ('browser-1', ?, 'active', 'https://example.test', 'Example', 60, '2000-01-01T00:00:00Z', '2000-01-01T00:00:00Z', '2000-01-01T00:00:00Z')`, narratorID); err != nil {
		t.Fatalf("insert expired browser session: %v", err)
	}
	if _, err := sqlDB.Exec(`CREATE TRIGGER fail_browser_expire BEFORE UPDATE OF status ON narrator_browser_sessions BEGIN SELECT RAISE(ABORT, 'browser expire update blocked'); END;`); err != nil {
		t.Fatalf("create browser expire trigger: %v", err)
	}

	w := serveNarratorHandlerRequestWithBrowserStorage(sqlDB, http.MethodGet, "/narrators/"+narratorID+"/browser-sessions")
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected browser session expire failure to return 500, got %d body=%s", w.Code, w.Body.String())
	}
}

func createBrowserSessionsTestTable(t *testing.T, sqlDB *sql.DB) {
	t.Helper()
	_, err := sqlDB.Exec(`CREATE TABLE narrator_browser_sessions (
		id TEXT PRIMARY KEY,
		narrator_id TEXT,
		status TEXT,
		url TEXT,
		title TEXT,
		ttl_seconds INTEGER,
		expires_at TEXT,
		screenshot_data_base64 TEXT,
		screenshot_mime TEXT,
		tracing_status TEXT,
		tracing_json TEXT,
		metadata_json TEXT,
		created_at TEXT,
		updated_at TEXT,
		closed_at TEXT
	)`)
	if err != nil {
		t.Fatalf("create narrator_browser_sessions table: %v", err)
	}
}

func TestBrowserSessionActionsManagePersistedFallbackRows(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	createBrowserSessionsTestTable(t, sqlDB)
	shot := base64.StdEncoding.EncodeToString([]byte("png"))
	if _, err := sqlDB.Exec(`INSERT INTO narrator_browser_sessions (id, narrator_id, status, url, title, ttl_seconds, expires_at, screenshot_data_base64, screenshot_mime, tracing_status, tracing_json, metadata_json, created_at, updated_at) VALUES ('browser-1', ?, 'active', 'https://example.test', 'Example', 60, '2999-01-01T00:00:00Z', ?, 'image/png', 'active', '{"status":"active"}', '{}', '2026-01-01T00:00:00Z', '2026-01-01T00:00:01Z')`, narratorID, shot); err != nil {
		t.Fatalf("insert browser row: %v", err)
	}
	w := serveNarratorHandlerJSONWithBrowserStorage(sqlDB, http.MethodPatch, "/narrators/"+narratorID+"/browser-sessions/browser-1/ttl", map[string]any{"ttlMs": 120000})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"ok":true`) || !strings.Contains(w.Body.String(), `"session"`) || !strings.Contains(w.Body.String(), `"ttlMs":120000`) {
		t.Fatalf("ttl update failed: status=%d body=%s", w.Code, w.Body.String())
	}
	w = serveNarratorHandlerJSONWithBrowserStorage(sqlDB, http.MethodPatch, "/narrators/"+narratorID+"/browser-sessions/browser-1/ttl", map[string]any{"ttlMs": 5})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid ttl should return 400, got %d body=%s", w.Code, w.Body.String())
	}
	w = serveNarratorHandlerRequestWithBrowserStorage(sqlDB, http.MethodGet, "/narrators/"+narratorID+"/browser-sessions/browser-1/screenshot")
	if w.Code != http.StatusOK || w.Body.String() != "png" || w.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("screenshot fallback failed: status=%d type=%s body=%q", w.Code, w.Header().Get("Content-Type"), w.Body.String())
	}
	w = serveNarratorHandlerRequestWithBrowserStorage(sqlDB, http.MethodPost, "/narrators/"+narratorID+"/browser-sessions/browser-1/stop-tracing")
	if w.Code != http.StatusOK || w.Body.String() != `{"ok":true}`+"\n" {
		t.Fatalf("stop tracing fallback failed: status=%d body=%s", w.Code, w.Body.String())
	}
	w = serveNarratorHandlerRequestWithBrowserStorage(sqlDB, http.MethodDelete, "/narrators/"+narratorID+"/browser-sessions/browser-1")
	if w.Code != http.StatusOK || w.Body.String() != `{"ok":true}`+"\n" {
		t.Fatalf("close fallback failed: status=%d body=%s", w.Code, w.Body.String())
	}
	var status string
	if err := sqlDB.QueryRow(`SELECT status FROM narrator_browser_sessions WHERE id = 'browser-1'`).Scan(&status); err != nil || status != "closed" {
		t.Fatalf("browser row status=%s err=%v", status, err)
	}
}

func TestBrowserSessionActionsReturnNotFoundForMissingPersistedRows(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	createBrowserSessionsTestTable(t, sqlDB)

	cases := []struct {
		name   string
		method string
		path   string
		body   map[string]any
	}{
		{name: "close", method: http.MethodDelete, path: "/narrators/" + narratorID + "/browser-sessions/missing"},
		{name: "ttl", method: http.MethodPatch, path: "/narrators/" + narratorID + "/browser-sessions/missing/ttl", body: map[string]any{"ttlMs": 60000}},
		{name: "stop tracing", method: http.MethodPost, path: "/narrators/" + narratorID + "/browser-sessions/missing/stop-tracing"},
		{name: "screenshot", method: http.MethodGet, path: "/narrators/" + narratorID + "/browser-sessions/missing/screenshot"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var w = serveNarratorHandlerRequestWithBrowserStorage(sqlDB, tc.method, tc.path)
			if tc.body != nil {
				w = serveNarratorHandlerJSONWithBrowserStorage(sqlDB, tc.method, tc.path, tc.body)
			}
			if w.Code != http.StatusNotFound {
				t.Fatalf("expected browser-session missing row 404, got %d body=%s", w.Code, w.Body.String())
			}
		})
	}
}

func TestBrowserSessionActionsExposeFeatureDisabledFallbackWithoutStorageTable(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)

	cases := []struct {
		name   string
		method string
		path   string
		body   map[string]any
	}{
		{name: "close", method: http.MethodDelete, path: "/narrators/" + narratorID + "/browser-sessions/session-1"},
		{name: "ttl", method: http.MethodPatch, path: "/narrators/" + narratorID + "/browser-sessions/session-1/ttl", body: map[string]any{"ttlMs": 60000}},
		{name: "stop tracing", method: http.MethodPost, path: "/narrators/" + narratorID + "/browser-sessions/session-1/stop-tracing"},
		{name: "screenshot", method: http.MethodGet, path: "/narrators/" + narratorID + "/browser-sessions/session-1/screenshot"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var w = serveNarratorHandlerRequest(sqlDB, tc.method, tc.path)
			if tc.body != nil {
				w = serveNarratorHandlerJSON(sqlDB, tc.method, tc.path, tc.body)
			}
			if w.Code != http.StatusNotImplemented {
				t.Fatalf("expected browser-session fallback 501, got %d body=%s", w.Code, w.Body.String())
			}
			var body map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode browser fallback: %v", err)
			}
			if body["ok"] != false || body["code"] != "FEATURE_DISABLED" || body["feature"] != "narrator.browserSessions" || body["supported"] != false || body["fallback"] != true {
				t.Fatalf("unexpected browser-session fallback payload: %#v", body)
			}
			reason, _ := body["reason"].(string)
			if reason == "" || body["error"] != reason || body["message"] != reason {
				t.Fatalf("browser-session fallback should expose reason/error/message, got %#v", body)
			}
		})
	}
}

func TestRollbackAndEditRoutesAreActive(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)

	cases := []struct {
		name string
		path string
		body map[string]any
	}{
		{
			name: "rollback missing message",
			path: "/narrators/" + narratorID + "/rollback/msg-1",
			body: map[string]any{"blockIndex": 0},
		},
		{
			name: "edit missing message",
			path: "/narrators/" + narratorID + "/edit-and-regenerate/msg-1",
			body: map[string]any{"content": "updated", "rollback": true},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := serveNarratorHandlerJSON(sqlDB, http.MethodPost, tc.path, tc.body)
			if w.Code != http.StatusNotFound {
				t.Fatalf("expected active route to validate message refs with 404, got %d body=%s", w.Code, w.Body.String())
			}
			var body map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode error body: %v", err)
			}
			if body["code"] == "FEATURE_DISABLED" || body["supported"] == false {
				t.Fatalf("rollback/edit routes should no longer return feature-disabled fallback: %#v", body)
			}
		})
	}
}

func TestStandaloneNarratorListUsesTSPaginatedShape(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	if _, err := sqlDB.Exec(`CREATE TABLE terminals (id TEXT PRIMARY KEY, narrator_id TEXT, status TEXT)`); err != nil {
		t.Fatalf("create terminals table: %v", err)
	}
	insertNarratorHandlerTestRow(t, sqlDB)

	w := serveNarratorHandlerRequest(sqlDB, http.MethodGet, "/narrators/?standalone=true")
	if w.Code != http.StatusOK {
		t.Fatalf("expected standalone list 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode standalone list: %v", err)
	}
	items, ok := body["items"].([]any)
	if !ok {
		t.Fatalf("standalone=true should return paginated object with items array, got %#v", body)
	}
	if body["hasMore"] != false || body["nextCursor"] != nil || body["totalCount"].(float64) != 1 || len(items) != 1 {
		t.Fatalf("unexpected standalone paginated payload: %#v", body)
	}
}

func TestPaginatedNarratorListWithHasViewersDoesNotForceEmpty(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	if _, err := sqlDB.Exec(`CREATE TABLE terminals (id TEXT PRIMARY KEY, narrator_id TEXT, status TEXT)`); err != nil {
		t.Fatalf("create terminals table: %v", err)
	}
	insertNarratorHandlerTestRow(t, sqlDB)

	w := serveNarratorHandlerRequest(sqlDB, http.MethodGet, "/narrators/?standalone=true&hasViewers=true")
	if w.Code != http.StatusOK {
		t.Fatalf("expected hasViewers list 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode hasViewers list: %v", err)
	}
	items, ok := body["items"].([]any)
	if !ok || len(items) != 1 || body["totalCount"].(float64) != 1 {
		t.Fatalf("hasViewers=true should not force an empty result: %#v", body)
	}
}

func TestBareNarratorListRequiresChapterOrStandalone(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	insertNarratorHandlerTestRow(t, sqlDB)

	w := serveNarratorHandlerRequest(sqlDB, http.MethodGet, "/narrators/")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected validation 400, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode bare list error: %v", err)
	}
	if body["code"] != "VALIDATION_ERROR" || body["error"] != "chapterId or standalone=true is required" {
		t.Fatalf("unexpected bare list validation payload: %#v", body)
	}
}

func TestSubagentDetachAttachRoutesExposeParentBlockingStatus(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	parentID := insertNarratorHandlerTestRow(t, sqlDB)
	childID := insertChildNarratorTestRow(t, sqlDB, "route_child")
	if _, err := sqlDB.Exec(`UPDATE narrators SET parent_narrator_id = ?, status = 'working', is_background = 0, traits = '[]' WHERE id = ?`, parentID, childID); err != nil {
		t.Fatalf("seed child status: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO narrator_messages (id, narrator_id, parent_tool_use_id, role, content_json, content_text, created_at) VALUES ('route_child_msg', ?, 'agent-route', 'user', '[{"type":"text","text":"launch"}]', 'launch', '2026-01-01T00:00:00Z')`, childID); err != nil {
		t.Fatalf("seed child parent tool use: %v", err)
	}

	w := serveNarratorHandlerJSON(sqlDB, http.MethodPost, "/narrators/"+childID+"/detach", map[string]any{})
	if w.Code != http.StatusOK {
		t.Fatalf("expected detach 200, got %d body=%s", w.Code, w.Body.String())
	}
	var detached map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &detached); err != nil {
		t.Fatalf("decode detach: %v", err)
	}
	if detached["detached"] != true || detached["fallback"] != false || detached["background"] != true || detached["backgroundStatus"] != "running" {
		t.Fatalf("unexpected detach payload: %#v", detached)
	}

	w = serveNarratorHandlerJSON(sqlDB, http.MethodPost, "/narrators/"+childID+"/attach", map[string]any{})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected attach stale recovery 400, got %d body=%s", w.Code, w.Body.String())
	}
	var attached map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &attached); err != nil {
		t.Fatalf("decode attach: %v", err)
	}
	if attached["attached"] != false || attached["staleRecovered"] != true || attached["backgroundStatus"] != "failed" {
		t.Fatalf("unexpected attach stale recovery payload: %#v", attached)
	}
}
