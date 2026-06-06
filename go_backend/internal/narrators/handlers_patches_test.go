package narrators

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

func TestMergePermissionInputUpdatesStoresExitPlanDiff(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	originalPlan := "Inspect the service\nKeep old approval wording\nRun focused tests"
	inputJSON, _ := json.Marshal(map[string]any{"plan": originalPlan})
	if _, err := sqlDB.Exec(`INSERT INTO narrator_tool_calls (id, narrator_id, tool_use_id, tool_name, input_json, status, created_at) VALUES ('req-exit-plan', ?, 'exit-plan-diff-1', 'ExitPlanMode', ?, 'pending', ?)`, narratorID, string(inputJSON), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert pending ExitPlanMode: %v", err)
	}
	h := &Handler{Service: &Service{DB: sqlDB}}
	updatedPlan := "Inspect the service\nUse updated approval wording\nRun focused tests"
	updated, err := h.mergePermissionInputUpdates(context.Background(), "req-exit-plan", nil, updatedPlan, true)
	if err != nil {
		t.Fatalf("merge permission input updates: %v", err)
	}
	updatedMap, _ := updated.(map[string]any)
	if updatedMap["plan"] != updatedPlan || updatedMap["planSource"] != "updatedPlan" || updatedMap["compactAfter"] != true {
		t.Fatalf("unexpected updated input: %#v", updatedMap)
	}
	diff := stringFromAny(updatedMap["planDiff"])
	if !strings.Contains(diff, "- Keep old approval wording") || !strings.Contains(diff, "+ Use updated approval wording") {
		t.Fatalf("expected plan diff in updated input, got %q", diff)
	}
	var storedRaw string
	if err := sqlDB.QueryRow(`SELECT input_json FROM narrator_tool_calls WHERE id = 'req-exit-plan'`).Scan(&storedRaw); err != nil {
		t.Fatalf("load stored input: %v", err)
	}
	stored := map[string]any{}
	if err := json.Unmarshal([]byte(storedRaw), &stored); err != nil {
		t.Fatalf("decode stored input: %v", err)
	}
	if stringFromAny(stored["planDiff"]) != diff {
		t.Fatalf("expected stored plan diff %q, got %#v", diff, stored)
	}
}

func TestPermissionApproveRouteMergesExitPlanUpdatedPlanAndCompactAfter(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	originalPlan := "Review the route\nKeep old plan details\nRun tests"
	inputJSON, _ := json.Marshal(map[string]any{"plan": originalPlan})
	if _, err := sqlDB.Exec(`INSERT INTO narrator_tool_calls (id, narrator_id, tool_use_id, tool_name, input_json, status, created_at) VALUES ('req-exit-plan-route', ?, 'exit-plan-route-1', 'ExitPlanMode', ?, 'pending', ?)`, narratorID, string(inputJSON), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert pending ExitPlanMode: %v", err)
	}
	updatedPlan := "Review the route\nUse updated plan details\nRun tests"
	w := serveNarratorHandlerJSON(sqlDB, http.MethodPost, "/narrators/permissions/req-exit-plan-route/approve", map[string]any{"updatedPlan": updatedPlan, "compactAfter": true})
	if w.Code != http.StatusOK {
		t.Fatalf("expected approval 200, got %d body=%s", w.Code, w.Body.String())
	}
	var status, inputRaw string
	if err := sqlDB.QueryRow(`SELECT status, input_json FROM narrator_tool_calls WHERE id = 'req-exit-plan-route'`).Scan(&status, &inputRaw); err != nil {
		t.Fatalf("load approved permission: %v", err)
	}
	if status != "running" {
		t.Fatalf("expected approved permission to become running, got %q", status)
	}
	input := map[string]any{}
	if err := json.Unmarshal([]byte(inputRaw), &input); err != nil {
		t.Fatalf("decode stored input: %v", err)
	}
	if input["plan"] != updatedPlan || input["planSource"] != "updatedPlan" || input["compactAfter"] != true {
		t.Fatalf("unexpected route-updated input: %#v", input)
	}
	diff := stringFromAny(input["planDiff"])
	if !strings.Contains(diff, "- Keep old plan details") || !strings.Contains(diff, "+ Use updated plan details") {
		t.Fatalf("expected route to store plan diff, got %q", diff)
	}
}

func TestPermissionDenyRouteDoesNotMergeExitPlanUpdatedPlan(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	originalPlan := "Review the route\nKeep original denied plan\nRun tests"
	inputJSON, _ := json.Marshal(map[string]any{"plan": originalPlan})
	if _, err := sqlDB.Exec(`INSERT INTO narrator_tool_calls (id, narrator_id, tool_use_id, tool_name, input_json, status, created_at) VALUES ('req-exit-plan-deny', ?, 'exit-plan-deny-1', 'ExitPlanMode', ?, 'pending', ?)`, narratorID, string(inputJSON), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert pending ExitPlanMode: %v", err)
	}
	w := serveNarratorHandlerJSON(sqlDB, http.MethodPost, "/narrators/permissions/req-exit-plan-deny/deny", map[string]any{"updatedPlan": "Denied updated plan", "compactAfter": true})
	if w.Code != http.StatusOK {
		t.Fatalf("expected deny 200, got %d body=%s", w.Code, w.Body.String())
	}
	var status, inputRaw string
	if err := sqlDB.QueryRow(`SELECT status, input_json FROM narrator_tool_calls WHERE id = 'req-exit-plan-deny'`).Scan(&status, &inputRaw); err != nil {
		t.Fatalf("load denied permission: %v", err)
	}
	if status != "fail" {
		t.Fatalf("expected denied permission to fail, got %q", status)
	}
	input := map[string]any{}
	if err := json.Unmarshal([]byte(inputRaw), &input); err != nil {
		t.Fatalf("decode denied input: %v", err)
	}
	if input["plan"] != originalPlan || input["planDiff"] != nil || input["compactAfter"] != nil {
		t.Fatalf("deny route should not merge ExitPlanMode updated input, got %#v", input)
	}
}

func TestNarratorSettingsMutationsBroadcastTSCompatibleEvents(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	hub, events := captureNarratorBroadcasts()
	service := &Service{DB: sqlDB, Hub: hub}

	cases := []struct {
		name      string
		method    string
		path      string
		body      string
		eventType string
		assert    func(t *testing.T, payload map[string]any)
	}{
		{
			name:      "title",
			method:    http.MethodPatch,
			path:      "/narrators/" + narratorID + "/title",
			body:      `{"title":"New Title"}`,
			eventType: "title_updated",
			assert: func(t *testing.T, payload map[string]any) {
				t.Helper()
				if payload["title"] != "New Title" {
					t.Fatalf("unexpected title event: %#v", payload)
				}
			},
		},
		{
			name:      "permission mode",
			method:    http.MethodPatch,
			path:      "/narrators/" + narratorID + "/permission-mode",
			body:      `{"permissionMode":"readOnly"}`,
			eventType: "permission_mode_changed",
			assert: func(t *testing.T, payload map[string]any) {
				t.Helper()
				if payload["permissionMode"] != "readOnly" {
					t.Fatalf("unexpected permission mode event: %#v", payload)
				}
			},
		},
		{
			name:      "relaxed plan",
			method:    http.MethodPatch,
			path:      "/narrators/" + narratorID + "/relaxed-plan",
			body:      `{"relaxedPlan":true}`,
			eventType: "relaxed_plan_changed",
			assert: func(t *testing.T, payload map[string]any) {
				t.Helper()
				if payload["relaxedPlan"] != true {
					t.Fatalf("unexpected relaxed plan event: %#v", payload)
				}
			},
		},
		{
			name:      "reflection overrides",
			method:    http.MethodPatch,
			path:      "/narrators/" + narratorID + "/reflection-overrides",
			body:      `{"dangerReflectionOverride":"strict"}`,
			eventType: "reflection_overrides_changed",
			assert: func(t *testing.T, payload map[string]any) {
				t.Helper()
				if payload["dangerReflectionOverride"] != "strict" {
					t.Fatalf("unexpected reflection override event: %#v", payload)
				}
				var persisted string
				if err := sqlDB.QueryRow(`SELECT danger_reflection_override FROM narrators WHERE id = ?`, narratorID).Scan(&persisted); err != nil {
					t.Fatalf("query persisted danger reflection override: %v", err)
				}
				if persisted != "strict" {
					t.Fatalf("persisted danger reflection override = %q, want strict", persisted)
				}
				if _, ok := payload["planReflectionAutoApproveOverride"]; ok {
					t.Fatalf("missing reflection override keys must not be broadcast as null: %#v", payload)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			w := serveNarratorHandlerWithService(service, req)
			if w.Code < 200 || w.Code >= 300 {
				t.Fatalf("expected success, got %d body=%s", w.Code, w.Body.String())
			}
			payload := findBroadcast(events(), narratorID, tc.eventType)
			if payload == nil {
				t.Fatalf("expected %s broadcast, got %#v", tc.eventType, events())
			}
			if payload["narratorId"] != narratorID {
				t.Fatalf("event should include narratorId, got %#v", payload)
			}
			tc.assert(t, payload)
		})
	}
}

func TestPersistAgentErrorBroadcastsNarratorError(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	hub, events := captureNarratorBroadcasts()
	service := &Service{DB: sqlDB, Hub: hub}

	if err := service.persistAgentError(context.Background(), narratorID, errors.New("model exploded"), false); err != nil {
		t.Fatalf("persistAgentError returned error: %v", err)
	}
	payload := findBroadcast(events(), narratorID, "narrator_error")
	if payload == nil {
		t.Fatalf("expected narrator_error broadcast, got %#v", events())
	}
	if !strings.Contains(stringFromAny(payload["error"]), "model exploded") || payload["errorCode"] != "AGENT_ERROR" {
		t.Fatalf("unexpected narrator_error payload: %#v", payload)
	}
}

func TestUsageStatsReturnsTSShapeIncludesSubagentsAndHandlesMissingAPIRequests(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	childID := "nar_usage_child"
	if _, err := sqlDB.Exec(`INSERT INTO narrators (
		id, type, title, inherit_mode, parent_narrator_id, model, permission_mode, status, substatus, plan_mode,
		cwd, variant, traits, is_background, is_ask_in_passing, message_version, message_count,
		total_cost_usd, fast_mode, relaxed_plan, plan_reflection_auto_approve_override,
		danger_reflection_override, created_at, updated_at
	) VALUES (?, 'subagent', 'Usage child', 'fresh', ?, 'openai:test', 'default', 'idle', '[]', 0, '', 'subagent', '[]', 0, 0, 0, 0, 0, 0, 0, 'inherit', 'inherit', ?, ?)`, childID, narratorID, now, now); err != nil {
		t.Fatalf("insert child narrator: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO api_requests (id, narrator_id, input_tokens, output_tokens, cached_input_tokens, cache_creation_input_tokens, cache_creation_5m_tokens, cache_creation_1h_tokens, reasoning_tokens, duration_ms, ttft_ms, cost_usd, created_at) VALUES
		('api-parent', ?, 10, 4, 2, 3, 30, 60, 1, 100, 11, 0.5, ?),
		('api-child', ?, 7, 2, 1, 5, 50, 100, 2, 200, 22, 0.25, ?)`, narratorID, now, childID, now); err != nil {
		t.Fatalf("insert api requests: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO narrator_tool_calls (id, narrator_id, message_id, tool_use_id, tool_name, status, created_at) VALUES
		('tc-parent', ?, 'missing-parent-message', 'tool-parent', 'Read', 'success', ?),
		('tc-child', ?, 'missing-child-message', 'tool-child', 'Read', 'success', ?)`, narratorID, now, childID, now); err != nil {
		t.Fatalf("insert tool calls: %v", err)
	}

	w := serveNarratorHandlerRequest(sqlDB, http.MethodGet, "/narrators/"+narratorID+"/usage-stats")
	if w.Code != http.StatusOK {
		t.Fatalf("expected usage stats 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode usage stats: %v body=%s", err, w.Body.String())
	}
	if intNumber(body["totalRequests"]) != 2 || intNumber(body["totalInputTokens"]) != 17 || intNumber(body["totalOutputTokens"]) != 6 || intNumber(body["totalCacheCreationTokens"]) != 8 || intNumber(body["totalCacheReadTokens"]) != 3 || intNumber(body["totalTokens"]) != 34 || intNumber(body["totalReasoningTokens"]) != 3 || intNumber(body["toolCallCount"]) != 2 {
		t.Fatalf("unexpected TS-shaped usage stats with subagents: %#v", body)
	}
	if body["totalCost"] != 0.75 || body["totalCostUsd"] != 0.75 || intNumber(body["requestCount"]) != 2 || intNumber(body["inputTokens"]) != 17 {
		t.Fatalf("unexpected cost/legacy aliases: %#v", body)
	}

	w = serveNarratorHandlerRequest(sqlDB, http.MethodGet, "/narrators/"+narratorID+"/usage-stats?includeSubagents=false")
	if w.Code != http.StatusOK {
		t.Fatalf("expected usage stats without subagents 200, got %d body=%s", w.Code, w.Body.String())
	}
	body = map[string]any{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode usage stats without subagents: %v", err)
	}
	if intNumber(body["totalRequests"]) != 1 || intNumber(body["totalInputTokens"]) != 10 || intNumber(body["totalTokens"]) != 19 || intNumber(body["toolCallCount"]) != 1 {
		t.Fatalf("unexpected usage stats without subagents: %#v", body)
	}

	if _, err := sqlDB.Exec(`DROP TABLE api_requests`); err != nil {
		t.Fatalf("drop api_requests: %v", err)
	}
	w = serveNarratorHandlerRequest(sqlDB, http.MethodGet, "/narrators/"+narratorID+"/usage-stats")
	if w.Code != http.StatusOK {
		t.Fatalf("expected missing api_requests to fail open, got %d body=%s", w.Code, w.Body.String())
	}
	body = map[string]any{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode missing api_requests stats: %v", err)
	}
	if intNumber(body["totalRequests"]) != 0 || intNumber(body["totalInputTokens"]) != 0 || intNumber(body["toolCallCount"]) != 2 {
		t.Fatalf("missing api_requests should return zero usage with tool-call count, got %#v", body)
	}

	if _, err := sqlDB.Exec(`CREATE TABLE api_requests (id TEXT PRIMARY KEY, narrator_id TEXT, created_at TEXT)`); err != nil {
		t.Fatalf("create minimal api_requests: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO api_requests (id, narrator_id, created_at) VALUES ('api-minimal', ?, ?)`, narratorID, now); err != nil {
		t.Fatalf("insert minimal api request: %v", err)
	}
	w = serveNarratorHandlerRequest(sqlDB, http.MethodGet, "/narrators/"+narratorID+"/usage-stats")
	if w.Code != http.StatusOK {
		t.Fatalf("expected minimal api_requests to fail open, got %d body=%s", w.Code, w.Body.String())
	}
	body = map[string]any{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode minimal api_requests stats: %v", err)
	}
	if intNumber(body["totalRequests"]) != 1 || intNumber(body["totalInputTokens"]) != 0 || intNumber(body["totalTokens"]) != 0 || intNumber(body["toolCallCount"]) != 2 {
		t.Fatalf("minimal api_requests should count requests without usage columns, got %#v", body)
	}
}

func TestDeleteNarratorRejectsNonArchivedAndPreservesRow(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)

	w := serveNarratorHandlerRequest(sqlDB, http.MethodDelete, "/narrators/"+narratorID)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for non-archived narrator, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body["code"] != "BAD_REQUEST" || !strings.Contains(body["message"].(string), "only archived") {
		t.Fatalf("unexpected delete rejection body: %#v", body)
	}
	var count int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM narrators WHERE id = ?`, narratorID).Scan(&count); err != nil {
		t.Fatalf("count narrators: %v", err)
	}
	if count != 1 {
		t.Fatalf("non-archived narrator row was deleted")
	}
}

func TestPatchesListsFileSnapshotsWithoutLegacyPatchTable(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	createdAt := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := sqlDB.Exec(`INSERT INTO narrator_file_snapshots (id, narrator_id, file_path, original_content, created_at) VALUES (?, ?, ?, ?, ?)`, "snap_1", narratorID, "/tmp/example.txt", "before", createdAt); err != nil {
		t.Fatalf("insert snapshot: %v", err)
	}

	w := serveNarratorHandlerRequest(sqlDB, http.MethodGet, "/narrators/"+narratorID+"/patches")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode snapshot list: %v body=%s", err, w.Body.String())
	}
	if len(body) != 1 || body[0]["id"] != "snap_1" || body[0]["filePath"] != "/tmp/example.txt" {
		t.Fatalf("unexpected snapshot response: %#v", body)
	}
	if _, ok := body[0]["originalContent"]; !ok {
		t.Fatalf("snapshot response should include TS-compatible originalContent: %#v", body[0])
	}
}

func TestFileStateRebuildDiffRevertAndUnrevert(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	cwd := t.TempDir()
	if _, err := sqlDB.Exec(`UPDATE narrators SET cwd = ? WHERE id = ?`, cwd, narratorID); err != nil {
		t.Fatalf("set cwd: %v", err)
	}
	filePath := filepath.Join(cwd, "note.txt")
	if err := os.WriteFile(filePath, []byte("hello\nGo\n"), 0o644); err != nil {
		t.Fatalf("write fixture file: %v", err)
	}
	baseTime := time.Now().UTC()
	insertNarratorMessageForSnapshotTest(t, sqlDB, narratorID, "msg_user", 1, "user", baseTime)
	insertNarratorMessageForSnapshotTest(t, sqlDB, narratorID, "msg_write", 2, "assistant", baseTime.Add(time.Second))
	insertNarratorMessageForSnapshotTest(t, sqlDB, narratorID, "msg_edit", 3, "assistant", baseTime.Add(2*time.Second))
	if _, err := sqlDB.Exec(`INSERT INTO narrator_file_snapshots (id, narrator_id, file_path, original_content, created_at) VALUES ('snap_note', ?, ?, ?, ?)`, narratorID, filePath, "hello\n", baseTime.Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert snapshot: %v", err)
	}
	insertNarratorToolCallForSnapshotTest(t, sqlDB, narratorID, "msg_write", "write-1", "Write", map[string]any{"file_path": "note.txt", "content": "hello\nworld\n"}, baseTime.Add(time.Second))
	insertNarratorToolCallForSnapshotTest(t, sqlDB, narratorID, "msg_edit", "edit-1", "Edit", map[string]any{"file_path": "note.txt", "old_string": "world", "new_string": "Go"}, baseTime.Add(2*time.Second))
	writeBlocks, _ := json.Marshal([]map[string]any{
		{"type": "text", "text": "write"},
		{"type": "tool_use", "id": "write-1", "name": "Write", "input": map[string]any{"file_path": "note.txt", "content": "hello\nworld\n"}},
	})
	if _, err := sqlDB.Exec(`UPDATE narrator_messages SET content_json = ?, content_text = 'write' WHERE id = 'msg_write'`, string(writeBlocks)); err != nil {
		t.Fatalf("update write message blocks: %v", err)
	}

	w := serveNarratorHandlerRequest(sqlDB, http.MethodGet, "/narrators/"+narratorID+"/file-modifications")
	if w.Code != http.StatusOK {
		t.Fatalf("expected file modifications 200, got %d body=%s", w.Code, w.Body.String())
	}
	var mods map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &mods); err != nil {
		t.Fatalf("decode file modifications: %v body=%s", err, w.Body.String())
	}
	files, _ := mods["files"].([]any)
	if len(files) != 1 {
		t.Fatalf("expected one modified file, got %#v", mods)
	}
	file := files[0].(map[string]any)
	if file["filePath"] != filePath || int(file["editCount"].(float64)) != 2 {
		t.Fatalf("unexpected file summary: %#v", file)
	}
	ops, _ := file["operations"].([]any)
	if len(ops) != 2 || ops[0].(map[string]any)["toolUseId"] != "write-1" || ops[1].(map[string]any)["toolUseId"] != "edit-1" {
		t.Fatalf("operations should follow message/tool order: %#v", ops)
	}
	timeline, _ := mods["timeline"].([]any)
	if len(timeline) != 3 || timeline[1].(map[string]any)["hasEdits"] != true || timeline[2].(map[string]any)["hasEdits"] != true {
		t.Fatalf("unexpected timeline: %#v", timeline)
	}

	w = serveNarratorHandlerRequest(sqlDB, http.MethodGet, "/narrators/"+narratorID+"/patches/snap_note/diff")
	if w.Code != http.StatusOK {
		t.Fatalf("expected diff 200, got %d body=%s", w.Code, w.Body.String())
	}
	var diff map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &diff); err != nil {
		t.Fatalf("decode diff: %v body=%s", err, w.Body.String())
	}
	if diff["original"] != "hello\n" || diff["current"] != "hello\nGo\n" {
		t.Fatalf("diff should be rebuilt from tool calls, got %#v", diff)
	}

	w = serveNarratorHandlerRequest(sqlDB, http.MethodGet, "/narrators/"+narratorID+"/patches/snap_note/diff?fromMessageId=msg_write&upToMessageId=msg_edit")
	if w.Code != http.StatusOK {
		t.Fatalf("expected range diff 200, got %d body=%s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &diff); err != nil {
		t.Fatalf("decode range diff: %v body=%s", err, w.Body.String())
	}
	if diff["original"] != "hello\nworld\n" || diff["current"] != "hello\nGo\n" {
		t.Fatalf("range diff should rebuild boundaries, got %#v", diff)
	}

	w = serveNarratorHandlerRequest(sqlDB, http.MethodGet, "/narrators/"+narratorID+"/delete-preview?messageId=msg_edit")
	if w.Code != http.StatusOK {
		t.Fatalf("expected delete preview 200, got %d body=%s", w.Code, w.Body.String())
	}
	var preview map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &preview); err != nil {
		t.Fatalf("decode delete preview: %v body=%s", err, w.Body.String())
	}
	affected, _ := preview["affectedFiles"].([]any)
	if len(affected) != 1 {
		t.Fatalf("expected one affected file, got %#v", preview)
	}
	affectedFile := affected[0].(map[string]any)
	if affectedFile["currentContent"] != "hello\nGo\n" || affectedFile["revertedContent"] != "hello\nworld\n" || affectedFile["willBeDeleted"] != false {
		t.Fatalf("delete preview should use rebuilt current/reverted states, got %#v", affectedFile)
	}

	w = serveNarratorHandlerRequest(sqlDB, http.MethodGet, "/narrators/"+narratorID+"/rollback-preview?messageId=msg_write&blockIndex=0")
	if w.Code != http.StatusOK {
		t.Fatalf("expected rollback preview 200, got %d body=%s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &preview); err != nil {
		t.Fatalf("decode rollback preview: %v body=%s", err, w.Body.String())
	}
	if preview["toolCallCount"] != float64(2) || preview["deletedBlockCount"] != float64(1) || preview["deletedMessageCount"] != float64(1) {
		t.Fatalf("rollback preview should count removed tool calls/blocks/messages, got %#v", preview)
	}
	affected, _ = preview["affectedFiles"].([]any)
	if len(affected) != 1 {
		t.Fatalf("expected one rollback affected file, got %#v", preview)
	}
	affectedFile = affected[0].(map[string]any)
	if affectedFile["currentContent"] != "hello\nGo\n" || affectedFile["revertedContent"] != "hello\n" || affectedFile["willBeDeleted"] != false {
		t.Fatalf("rollback preview should expose current/reverted file-state diff, got %#v", affectedFile)
	}

	w = serveNarratorHandlerJSON(sqlDB, http.MethodPost, "/narrators/"+narratorID+"/revert", map[string]any{"messageId": "msg_edit"})
	if w.Code != http.StatusOK {
		t.Fatalf("expected targeted revert 200, got %d body=%s", w.Code, w.Body.String())
	}
	data, err := os.ReadFile(filePath)
	if err != nil || string(data) != "hello\nworld\n" {
		t.Fatalf("targeted revert should exclude later edit, data=%q err=%v", string(data), err)
	}
	w = serveNarratorHandlerJSON(sqlDB, http.MethodPost, "/narrators/"+narratorID+"/unrevert", map[string]any{})
	if w.Code != http.StatusOK {
		t.Fatalf("expected unrevert 200, got %d body=%s", w.Code, w.Body.String())
	}
	data, err = os.ReadFile(filePath)
	if err != nil || string(data) != "hello\nGo\n" {
		t.Fatalf("unrevert should replay full tool chain, data=%q err=%v", string(data), err)
	}
}

func TestPatchesFallbackWhenStorageTablesAreMissing(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	if _, err := sqlDB.Exec(`DROP TABLE narrator_file_snapshots`); err != nil {
		t.Fatalf("drop snapshots: %v", err)
	}

	w := serveNarratorHandlerRequest(sqlDB, http.MethodGet, "/narrators/"+narratorID+"/patches")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 fallback, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode fallback: %v body=%s", err, w.Body.String())
	}
	if body["fallback"] != true || body["supported"] != false {
		t.Fatalf("expected fallback metadata, got %#v", body)
	}
	if patches, ok := body["patches"].([]any); !ok || len(patches) != 0 {
		t.Fatalf("expected empty patches list, got %#v", body["patches"])
	}
	reason, _ := body["reason"].(string)
	if reason != "No narrator patch or snapshot storage table is available in this database" || body["error"] != reason || body["message"] != reason {
		t.Fatalf("patch fallback should expose reason/error/message, got %#v", body)
	}
}

func TestPaginatedListHasViewersIsNotHardcodedEmpty(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	if _, err := sqlDB.Exec(`CREATE TABLE terminals (id TEXT PRIMARY KEY, narrator_id TEXT, status TEXT)`); err != nil {
		t.Fatalf("create terminals: %v", err)
	}

	w := serveNarratorHandlerRequest(sqlDB, http.MethodGet, "/narrators?standalone=all&hasViewers=true")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode list body: %v body=%s", err, w.Body.String())
	}
	items, _ := body["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != narratorID {
		t.Fatalf("hasViewers=true should not be hardcoded empty, got %#v", body)
	}
}

func TestPostMessageBashSlashShortCircuits(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	cwd := t.TempDir()
	if _, err := sqlDB.Exec(`UPDATE narrators SET cwd = ? WHERE id = ?`, cwd, narratorID); err != nil {
		t.Fatalf("set cwd: %v", err)
	}

	w := serveNarratorHandlerJSON(sqlDB, http.MethodPost, "/narrators/"+narratorID+"/messages", map[string]any{"message": "/bash printf slash-bash"})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode bash body: %v", err)
	}
	if body["type"] != "bash" || !strings.Contains(stringValue(body["output"]), "slash-bash") {
		t.Fatalf("unexpected bash response: %#v", body)
	}
	var toolName string
	if err := sqlDB.QueryRow(`SELECT tool_name FROM narrator_tool_calls WHERE narrator_id = ?`, narratorID).Scan(&toolName); err != nil {
		t.Fatalf("load tool call: %v", err)
	}
	if toolName != "Bash" {
		t.Fatalf("tool name = %q", toolName)
	}
	var status string
	if err := sqlDB.QueryRow(`SELECT status FROM narrators WHERE id = ?`, narratorID).Scan(&status); err != nil {
		t.Fatalf("load narrator status: %v", err)
	}
	if status != "idle" {
		t.Fatalf("/bash should not start a model turn, status=%q", status)
	}
}

func TestPostMessageGoalSlashAddListResumeComplete(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)

	w := serveNarratorHandlerJSON(sqlDB, http.MethodPost, "/narrators/"+narratorID+"/messages", map[string]any{"message": "/goal ship route parity"})
	if w.Code != http.StatusOK {
		t.Fatalf("expected goal add 200, got %d body=%s", w.Code, w.Body.String())
	}
	var status string
	if err := sqlDB.QueryRow(`SELECT status FROM narrator_goals WHERE narrator_id = ? ORDER BY sort_order LIMIT 1`, narratorID).Scan(&status); err != nil {
		t.Fatalf("load added goal: %v", err)
	}
	if status != "active" {
		t.Fatalf("first goal should become active, got %q", status)
	}
	if _, err := sqlDB.Exec(`INSERT INTO narrator_goals (id, narrator_id, objective, status, sort_order, created_at, updated_at) VALUES ('goal_paused', ?, 'paused work', 'paused', 10, ?, ?)`, narratorID, time.Now().UTC().Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert paused goal: %v", err)
	}
	w = serveNarratorHandlerJSON(sqlDB, http.MethodPost, "/narrators/"+narratorID+"/messages", map[string]any{"message": "/goal complete"})
	if w.Code != http.StatusOK {
		t.Fatalf("expected goal complete 200, got %d body=%s", w.Code, w.Body.String())
	}
	if err := sqlDB.QueryRow(`SELECT status FROM narrator_goals WHERE id = 'goal_paused'`).Scan(&status); err != nil {
		t.Fatalf("load resumed goal: %v", err)
	}
	if status != "active" {
		t.Fatalf("complete should activate next paused/pending goal, got %q", status)
	}
	w = serveNarratorHandlerJSON(sqlDB, http.MethodPost, "/narrators/"+narratorID+"/messages", map[string]any{"message": "/goal"})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "goals") {
		t.Fatalf("expected goal list, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestGoalRESTMutationsBroadcastGoalsSetAndSoftCancel(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	hub, events := captureNarratorBroadcasts()
	service := &Service{DB: sqlDB, Hub: hub}

	postGoal := func(objective string) map[string]any {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"objective": objective})
		req := httptest.NewRequest(http.MethodPost, "/narrators/"+narratorID+"/goals", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := serveNarratorHandlerWithService(service, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("post goal %q got %d body=%s", objective, w.Code, w.Body.String())
		}
		var payload map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
			t.Fatalf("decode goal response: %v", err)
		}
		return payload
	}

	first := postGoal("first frontend-visible goal")
	firstGoal := first["goal"].(map[string]any)
	firstID := firstGoal["id"].(string)
	if firstGoal["status"] != "active" {
		t.Fatalf("first REST goal should become active, got %#v", firstGoal)
	}
	second := postGoal("second frontend-visible goal")
	secondGoal := second["goal"].(map[string]any)
	secondID := secondGoal["id"].(string)
	if secondGoal["status"] != "pending" {
		t.Fatalf("second REST goal should remain pending while first active, got %#v", secondGoal)
	}

	patchBody, _ := json.Marshal(map[string]any{"status": "active"})
	patchReq := httptest.NewRequest(http.MethodPatch, "/narrators/"+narratorID+"/goals/"+secondID, bytes.NewReader(patchBody))
	patchReq.Header.Set("Content-Type", "application/json")
	patch := serveNarratorHandlerWithService(service, patchReq)
	if patch.Code != http.StatusOK {
		t.Fatalf("patch active got %d body=%s", patch.Code, patch.Body.String())
	}
	statuses := map[string]string{}
	rows, err := sqlDB.Query(`SELECT id, status FROM narrator_goals WHERE narrator_id = ?`, narratorID)
	if err != nil {
		t.Fatalf("query goal statuses: %v", err)
	}
	for rows.Next() {
		var idValue, status string
		if err := rows.Scan(&idValue, &status); err != nil {
			t.Fatalf("scan goal status: %v", err)
		}
		statuses[idValue] = status
	}
	_ = rows.Close()
	if statuses[firstID] != "pending" || statuses[secondID] != "active" {
		t.Fatalf("activating second goal should demote previous active, statuses=%#v", statuses)
	}

	removeReq := httptest.NewRequest(http.MethodDelete, "/narrators/"+narratorID+"/goals/"+secondID, nil)
	remove := serveNarratorHandlerWithService(service, removeReq)
	if remove.Code != http.StatusOK {
		t.Fatalf("remove active got %d body=%s", remove.Code, remove.Body.String())
	}
	var secondStatus, firstStatus string
	if err := sqlDB.QueryRow(`SELECT status FROM narrator_goals WHERE id = ?`, secondID).Scan(&secondStatus); err != nil {
		t.Fatalf("load removed goal: %v", err)
	}
	if err := sqlDB.QueryRow(`SELECT status FROM narrator_goals WHERE id = ?`, firstID).Scan(&firstStatus); err != nil {
		t.Fatalf("load reactivated goal: %v", err)
	}
	statuses[secondID] = secondStatus
	statuses[firstID] = firstStatus
	if statuses[secondID] != "cancelled" || statuses[firstID] != "active" {
		t.Fatalf("remove should soft-cancel active goal and activate next pending, statuses=%#v", statuses)
	}

	clearReq := httptest.NewRequest(http.MethodDelete, "/narrators/"+narratorID+"/goals", nil)
	cleared := serveNarratorHandlerWithService(service, clearReq)
	if cleared.Code != http.StatusOK {
		t.Fatalf("clear goals got %d body=%s", cleared.Code, cleared.Body.String())
	}
	var openCount int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM narrator_goals WHERE narrator_id = ? AND status IN ('pending', 'active', 'paused')`, narratorID).Scan(&openCount); err != nil {
		t.Fatalf("count open goals: %v", err)
	}
	if openCount != 0 {
		t.Fatalf("clear goals should cancel all open goals, open=%d", openCount)
	}
	broadcasts := events()
	goalsSetCount := 0
	for _, event := range broadcasts {
		if event.NarratorID == narratorID && event.Payload["type"] == "goals_set" {
			goalsSetCount++
		}
	}
	if goalsSetCount < 5 {
		t.Fatalf("expected goals_set broadcast for every REST mutation, got %d events=%#v", goalsSetCount, broadcasts)
	}
}

func TestAddGoalHandlerReturnsQueryErrorWithoutInsertingFallbackState(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	stmts := []string{
		`ALTER TABLE narrator_goals RENAME TO narrator_goals_real`,
		`CREATE VIEW narrator_goals AS SELECT id, narrator_id, objective, status, broken_sort_order AS sort_order, tokens_used, time_used_seconds, created_by, completed_at, created_at, updated_at FROM narrator_goals_real`,
		`CREATE TRIGGER narrator_goals_insert INSTEAD OF INSERT ON narrator_goals BEGIN INSERT INTO narrator_goals_real (id, narrator_id, objective, status, sort_order, created_by, created_at, updated_at) VALUES (NEW.id, NEW.narrator_id, NEW.objective, NEW.status, NEW.sort_order, NEW.created_by, NEW.created_at, NEW.updated_at); END;`,
	}
	for _, stmt := range stmts {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("prepare broken goals view: %v", err)
		}
	}

	w := serveNarratorHandlerJSON(sqlDB, http.MethodPost, "/narrators/"+narratorID+"/goals", map[string]any{"objective": "do not insert on query failure"})
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected query failure 500, got %d body=%s", w.Code, w.Body.String())
	}
	var count int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM narrator_goals_real`).Scan(&count); err != nil {
		t.Fatalf("count backing goals: %v", err)
	}
	if count != 0 {
		t.Fatalf("query failure should not insert fallback goal state, count=%d", count)
	}
}

func TestPostMessageLoadSkillSlashInjectsSkillContent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	skillDir := filepath.Join(home, ".narrafork", "skills", "demo")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: demo\ndescription: Demo skill\n---\n\nUse demo instructions."), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	ensureSlashCommandTables(t, sqlDB)
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)

	w := serveNarratorHandlerJSON(sqlDB, http.MethodPost, "/narrators/"+narratorID+"/messages", map[string]any{"message": "/load-skill demo extra context"})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected skill send 201, got %d body=%s", w.Code, w.Body.String())
	}
	var text, commandText string
	if err := sqlDB.QueryRow(`SELECT content_text, command_text FROM narrator_messages WHERE narrator_id = ? AND role = 'user' ORDER BY created_at DESC LIMIT 1`, narratorID).Scan(&text, &commandText); err != nil {
		t.Fatalf("load skill user message: %v", err)
	}
	if !strings.Contains(text, "<command-name>demo</command-name>") || !strings.Contains(text, "Use demo instructions.") || !strings.Contains(text, "extra context") {
		t.Fatalf("skill content was not injected: %q", text)
	}
	if commandText != "/load-skill demo extra context" {
		t.Fatalf("commandText = %q", commandText)
	}
}

func TestPostMessageLoadToolSlashPersistsEnabledTool(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)

	w := serveNarratorHandlerJSON(sqlDB, http.MethodPost, "/narrators/"+narratorID+"/messages", map[string]any{"message": "/load terminal"})
	if w.Code != http.StatusOK {
		t.Fatalf("expected load tool 200, got %d body=%s", w.Code, w.Body.String())
	}
	body := decodeNarratorHandlerBody(t, w)
	if body["toolName"] != "Terminal" || body["loaded"] != true || body["alreadyLoaded"] != false {
		t.Fatalf("unexpected load tool response: %#v", body)
	}

	var enabledTools string
	if err := sqlDB.QueryRow(`SELECT enabled_tools FROM narrators WHERE id = ?`, narratorID).Scan(&enabledTools); err != nil {
		t.Fatalf("load enabled tools: %v", err)
	}
	if enabledTools != `["Terminal"]` {
		t.Fatalf("enabled_tools = %q, want Terminal", enabledTools)
	}
	var status string
	if err := sqlDB.QueryRow(`SELECT status FROM narrators WHERE id = ?`, narratorID).Scan(&status); err != nil {
		t.Fatalf("load narrator status: %v", err)
	}
	if status != "idle" {
		t.Fatalf("/load should not start a model run, status=%q", status)
	}

	var displayText, toolContent string
	if err := sqlDB.QueryRow(`SELECT content_text FROM narrator_messages WHERE narrator_id = ? AND role = 'disp' ORDER BY created_at DESC LIMIT 1`, narratorID).Scan(&displayText); err != nil {
		t.Fatalf("load display message: %v", err)
	}
	if !strings.Contains(displayText, "Tool loaded: Terminal") {
		t.Fatalf("unexpected display message: %q", displayText)
	}
	if err := sqlDB.QueryRow(`SELECT content_json FROM narrator_messages WHERE narrator_id = ? AND role = 'user' ORDER BY created_at DESC LIMIT 1`, narratorID).Scan(&toolContent); err != nil {
		t.Fatalf("load tool_loaded message: %v", err)
	}
	if !strings.Contains(toolContent, `"type":"tool_loaded"`) || !strings.Contains(toolContent, `"toolName":"Terminal"`) {
		t.Fatalf("tool_loaded message missing: %s", toolContent)
	}
}

func TestPostMessageLoadToolSlashAlreadyLoadedDoesNotDuplicateToolMessage(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)

	first := serveNarratorHandlerJSON(sqlDB, http.MethodPost, "/narrators/"+narratorID+"/messages", map[string]any{"message": "/load terminal"})
	if first.Code != http.StatusOK {
		t.Fatalf("expected first load 200, got %d body=%s", first.Code, first.Body.String())
	}
	second := serveNarratorHandlerJSON(sqlDB, http.MethodPost, "/narrators/"+narratorID+"/messages", map[string]any{"message": "/load terminal"})
	if second.Code != http.StatusOK {
		t.Fatalf("expected second load 200, got %d body=%s", second.Code, second.Body.String())
	}
	body := decodeNarratorHandlerBody(t, second)
	if body["toolName"] != "Terminal" || body["loaded"] != true || body["alreadyLoaded"] != true {
		t.Fatalf("unexpected already-loaded response: %#v", body)
	}
	var count int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM narrator_messages WHERE narrator_id = ? AND role = 'user' AND content_json LIKE '%tool_loaded%'`, narratorID).Scan(&count); err != nil {
		t.Fatalf("count tool_loaded messages: %v", err)
	}
	if count != 1 {
		t.Fatalf("already-loaded command should not duplicate model-visible message, count=%d", count)
	}
}

func TestPostMessageUnloadToolSlashPersistsRemoval(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	if _, err := sqlDB.Exec(`UPDATE narrators SET enabled_tools = '["Terminal","Browser"]' WHERE id = ?`, narratorID); err != nil {
		t.Fatalf("seed enabled tools: %v", err)
	}

	w := serveNarratorHandlerJSON(sqlDB, http.MethodPost, "/narrators/"+narratorID+"/messages", map[string]any{"message": "/unload terminal"})
	if w.Code != http.StatusOK {
		t.Fatalf("expected unload tool 200, got %d body=%s", w.Code, w.Body.String())
	}
	body := decodeNarratorHandlerBody(t, w)
	if body["toolName"] != "Terminal" || body["unloaded"] != true || body["notLoaded"] != false {
		t.Fatalf("unexpected unload tool response: %#v", body)
	}
	var enabledTools string
	if err := sqlDB.QueryRow(`SELECT enabled_tools FROM narrators WHERE id = ?`, narratorID).Scan(&enabledTools); err != nil {
		t.Fatalf("load enabled tools: %v", err)
	}
	if enabledTools != `["Browser"]` {
		t.Fatalf("enabled_tools = %q, want Browser only", enabledTools)
	}
	var toolContent string
	if err := sqlDB.QueryRow(`SELECT content_json FROM narrator_messages WHERE narrator_id = ? AND role = 'user' ORDER BY created_at DESC LIMIT 1`, narratorID).Scan(&toolContent); err != nil {
		t.Fatalf("load tool_unloaded message: %v", err)
	}
	if !strings.Contains(toolContent, `"type":"tool_unloaded"`) || !strings.Contains(toolContent, `"toolName":"Terminal"`) {
		t.Fatalf("tool_unloaded message missing: %s", toolContent)
	}
}

func TestPostMessageLoadToolSlashUnknownToolIsHandledLocally(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)

	w := serveNarratorHandlerJSON(sqlDB, http.MethodPost, "/narrators/"+narratorID+"/messages", map[string]any{"message": "/load nope"})
	if w.Code != http.StatusOK {
		t.Fatalf("expected unknown load 200, got %d body=%s", w.Code, w.Body.String())
	}
	body := decodeNarratorHandlerBody(t, w)
	if body["toolName"] != "nope" || body["loaded"] != false || body["alreadyLoaded"] != false {
		t.Fatalf("unexpected unknown-tool response: %#v", body)
	}
	var count int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM narrator_messages WHERE narrator_id = ? AND role = 'user'`, narratorID).Scan(&count); err != nil {
		t.Fatalf("count user messages: %v", err)
	}
	if count != 0 {
		t.Fatalf("unknown /load should not send a model-visible user message, count=%d", count)
	}
	var displayText string
	if err := sqlDB.QueryRow(`SELECT content_text FROM narrator_messages WHERE narrator_id = ? AND role = 'disp' ORDER BY created_at DESC LIMIT 1`, narratorID).Scan(&displayText); err != nil {
		t.Fatalf("load display message: %v", err)
	}
	if !strings.Contains(displayText, "Unknown tool: nope") || !strings.Contains(displayText, "terminal") {
		t.Fatalf("unexpected unknown-tool display message: %q", displayText)
	}
}

func TestPostMessageLoadToolSlashRespectsDisabledToolTrait(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	traits, _ := json.Marshal([]string{disabledToolsTrait([]string{"Terminal"})})
	if _, err := sqlDB.Exec(`UPDATE narrators SET traits = ? WHERE id = ?`, string(traits), narratorID); err != nil {
		t.Fatalf("seed traits: %v", err)
	}

	w := serveNarratorHandlerJSON(sqlDB, http.MethodPost, "/narrators/"+narratorID+"/messages", map[string]any{"message": "/load terminal"})
	if w.Code != http.StatusOK {
		t.Fatalf("expected disabled load 200, got %d body=%s", w.Code, w.Body.String())
	}
	body := decodeNarratorHandlerBody(t, w)
	if body["toolName"] != "Terminal" || body["loaded"] != false || body["alreadyLoaded"] != false {
		t.Fatalf("unexpected disabled-tool response: %#v", body)
	}
	var enabledTools sql.NullString
	if err := sqlDB.QueryRow(`SELECT enabled_tools FROM narrators WHERE id = ?`, narratorID).Scan(&enabledTools); err != nil {
		t.Fatalf("load enabled tools: %v", err)
	}
	if enabledTools.Valid && strings.TrimSpace(enabledTools.String) != "" {
		t.Fatalf("disabled tool should not be persisted, enabled_tools=%q", enabledTools.String)
	}
}

func TestPostMessageCustomCommandPreBashAndModelOverride(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	ensureSlashCommandTables(t, sqlDB)
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	cwd := t.TempDir()
	if _, err := sqlDB.Exec(`UPDATE narrators SET cwd = ? WHERE id = ?`, cwd, narratorID); err != nil {
		t.Fatalf("set cwd: %v", err)
	}
	commands := `[{"name":"review","prompt":"Review {{input}}","runBashFirst":true,"bashCommand":"printf pre > pre.txt","modelOverride":{"model":"openai:override","mode":"permanent"}}]`
	if _, err := sqlDB.Exec(`INSERT INTO user_preferences (user_id, commands) VALUES ('system', ?)`, commands); err != nil {
		t.Fatalf("insert user commands: %v", err)
	}

	w := serveNarratorHandlerJSON(sqlDB, http.MethodPost, "/narrators/"+narratorID+"/messages", map[string]any{"message": "/review this file"})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected custom command 201, got %d body=%s", w.Code, w.Body.String())
	}
	if data, err := os.ReadFile(filepath.Join(cwd, "pre.txt")); err != nil || string(data) != "pre" {
		t.Fatalf("pre prompt bash did not run: data=%q err=%v", string(data), err)
	}
	var text, commandText, model string
	if err := sqlDB.QueryRow(`SELECT content_text, command_text FROM narrator_messages WHERE narrator_id = ? AND role = 'user' AND command_text = '/review this file' LIMIT 1`, narratorID).Scan(&text, &commandText); err != nil {
		t.Fatalf("load custom command user message: %v", err)
	}
	if text != "Review this file" || commandText != "/review this file" {
		t.Fatalf("unexpected expanded message text=%q commandText=%q", text, commandText)
	}
	if err := sqlDB.QueryRow(`SELECT model FROM narrators WHERE id = ?`, narratorID).Scan(&model); err != nil {
		t.Fatalf("load model: %v", err)
	}
	if model != "openai:override" {
		t.Fatalf("model override = %q", model)
	}
}

func insertNarratorMessageForSnapshotTest(t *testing.T, sqlDB *sql.DB, narratorID, messageID string, seq int, role string, createdAt time.Time) {
	t.Helper()
	created := createdAt.UTC().Format(time.RFC3339Nano)
	if _, err := sqlDB.Exec(`INSERT INTO narrator_messages (id, narrator_id, role, content_json, content_text, created_at) VALUES (?, ?, ?, '[]', '', ?)`, messageID, narratorID, role, created); err != nil {
		t.Fatalf("insert message %s: %v", messageID, err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO narrator_message_refs (id, narrator_id, message_id, seq, is_compact) VALUES (?, ?, ?, ?, 0)`, "ref_"+messageID, narratorID, messageID, seq); err != nil {
		t.Fatalf("insert ref %s: %v", messageID, err)
	}
}

func insertNarratorToolCallForSnapshotTest(t *testing.T, sqlDB *sql.DB, narratorID, messageID, toolUseID, toolName string, input map[string]any, createdAt time.Time) {
	t.Helper()
	inputJSON, _ := json.Marshal(input)
	created := createdAt.UTC().Format(time.RFC3339Nano)
	if _, err := sqlDB.Exec(`INSERT INTO narrator_tool_calls (id, narrator_id, message_id, tool_use_id, tool_name, input_json, output_json, status, created_at) VALUES (?, ?, ?, ?, ?, ?, '{}', 'success', ?)`, "tc_"+toolUseID, narratorID, messageID, toolUseID, toolName, string(inputJSON), created); err != nil {
		t.Fatalf("insert tool call %s: %v", toolUseID, err)
	}
}

func openNarratorHandlerTestDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := createNarratorTestSchema(sqlDB); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	return sqlDB
}

func insertNarratorHandlerTestRow(t *testing.T, sqlDB *sql.DB) string {
	t.Helper()
	narratorID := "nar_test"
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := sqlDB.Exec(`INSERT INTO narrators (
		id, type, title, inherit_mode, model, permission_mode, status, substatus, plan_mode,
		cwd, variant, traits, is_background, is_ask_in_passing, message_version, message_count,
		total_cost_usd, fast_mode, relaxed_plan, plan_reflection_auto_approve_override,
		danger_reflection_override, created_at, updated_at
	) VALUES (?, 'primary', 'Test narrator', 'fresh', 'openai:test', 'default', 'idle', '[]', 0, '', 'primary', '[]', 0, 0, 0, 0, 0, 0, 0, 'inherit', 'inherit', ?, ?)`, narratorID, now, now)
	if err != nil {
		t.Fatalf("insert narrator: %v", err)
	}
	return narratorID
}

func serveNarratorHandlerRequest(sqlDB *sql.DB, method string, path string) *httptest.ResponseRecorder {
	h := NewHandler(&Service{DB: sqlDB})
	r := chi.NewRouter()
	r.Route("/narrators", h.RegisterRoutes)
	req := httptest.NewRequest(method, path, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func serveNarratorHandlerJSON(sqlDB *sql.DB, method string, path string, body any) *httptest.ResponseRecorder {
	h := NewHandler(&Service{DB: sqlDB})
	r := chi.NewRouter()
	r.Route("/narrators", h.RegisterRoutes)
	data, _ := json.Marshal(body)
	req := httptest.NewRequest(method, path, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func serveNarratorHandlerRequestWithBrowserStorage(sqlDB *sql.DB, method string, path string) *httptest.ResponseRecorder {
	h := NewHandler(&Service{DB: sqlDB, BrowserSessionPersistenceEnabled: true})
	r := chi.NewRouter()
	r.Route("/narrators", h.RegisterRoutes)
	req := httptest.NewRequest(method, path, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func serveNarratorHandlerJSONWithBrowserStorage(sqlDB *sql.DB, method string, path string, body any) *httptest.ResponseRecorder {
	h := NewHandler(&Service{DB: sqlDB, BrowserSessionPersistenceEnabled: true})
	r := chi.NewRouter()
	r.Route("/narrators", h.RegisterRoutes)
	data, _ := json.Marshal(body)
	req := httptest.NewRequest(method, path, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func ensureSlashCommandTables(t *testing.T, sqlDB *sql.DB) {
	t.Helper()
	stmts := []string{
		`CREATE TABLE user_preferences (user_id TEXT PRIMARY KEY, commands TEXT)`,
		`CREATE TABLE projects (id TEXT PRIMARY KEY, chapter_settings TEXT, git_path TEXT)`,
		`CREATE TABLE chapters (id TEXT PRIMARY KEY, project_id TEXT)`,
	}
	for _, stmt := range stmts {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("create slash command table: %v", err)
		}
	}
}
