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
			body:      `{"dangerReflectionOverride":"on"}`,
			eventType: "reflection_overrides_changed",
			assert: func(t *testing.T, payload map[string]any) {
				t.Helper()
				if payload["dangerReflectionOverride"] != "on" {
					t.Fatalf("unexpected reflection override event: %#v", payload)
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

func TestDeleteNarratorReturnsExplicitFeatureDisabledFallbackAndPreservesRow(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)

	w := serveNarratorHandlerRequest(sqlDB, http.MethodDelete, "/narrators/"+narratorID)
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501 feature disabled, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode fallback body: %v", err)
	}
	if body["ok"] != false || body["code"] != "FEATURE_DISABLED" || body["feature"] != "narrators.delete" || body["supported"] != false || body["fallback"] != true {
		t.Fatalf("unexpected delete fallback body: %#v", body)
	}
	reason, _ := body["reason"].(string)
	if !strings.Contains(reason, "Safe narrator deletion semantics are not implemented in go_backend yet") || body["error"] != reason || body["message"] != reason {
		t.Fatalf("delete fallback should expose reason/error/message, got %#v", body)
	}
	var count int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM narrators WHERE id = ?`, narratorID).Scan(&count); err != nil {
		t.Fatalf("count narrators: %v", err)
	}
	if count != 1 {
		t.Fatalf("narrator row was deleted despite feature-disabled response")
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
