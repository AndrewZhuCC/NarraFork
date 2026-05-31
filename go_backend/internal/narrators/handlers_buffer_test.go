package narrators

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"narrafork-go/internal/config"
	"narrafork-go/internal/id"
)

func TestPostMessageBuffersWorkingNarratorWithPriority(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	if _, err := sqlDB.Exec(`UPDATE narrators SET status = 'working' WHERE id = ?`, narratorID); err != nil {
		t.Fatalf("mark working: %v", err)
	}

	body := bytes.NewBufferString(`{"message":"cut in line","priority":true}`)
	req := httptest.NewRequest(http.MethodPost, "/narrators/"+narratorID+"/messages", body)
	req.Header.Set("Content-Type", "application/json")
	w := serveNarratorHandlerHTTPRequest(sqlDB, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload["buffered"] != true || payload["id"] == "" || payload["bufferedAt"] == "" {
		t.Fatalf("unexpected buffered response: %#v", payload)
	}

	var text, createdBy string
	var priority, seq int
	if err := sqlDB.QueryRow(`SELECT text, created_by, priority, seq FROM narrator_buffered_messages WHERE narrator_id = ?`, narratorID).Scan(&text, &createdBy, &priority, &seq); err != nil {
		t.Fatalf("load buffered row: %v", err)
	}
	if text != "cut in line" || createdBy != "system" || priority != 1 || seq != 0 {
		t.Fatalf("unexpected buffered row text=%q createdBy=%q priority=%d seq=%d", text, createdBy, priority, seq)
	}
	var messageCount int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM narrator_messages WHERE narrator_id = ?`, narratorID).Scan(&messageCount); err != nil {
		t.Fatalf("count messages: %v", err)
	}
	if messageCount != 0 {
		t.Fatalf("working POST should not persist a user message immediately, got %d", messageCount)
	}
}

func TestPostMessageBuffersExpandedSlashCommandWithRawCommandText(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	ensureSlashCommandTables(t, sqlDB)
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	if _, err := sqlDB.Exec(`UPDATE narrators SET status = 'working' WHERE id = ?`, narratorID); err != nil {
		t.Fatalf("mark working: %v", err)
	}
	commands := `[{"name":"review","prompt":"Review {{input}}"}]`
	if _, err := sqlDB.Exec(`INSERT INTO user_preferences (user_id, commands) VALUES ('system', ?)`, commands); err != nil {
		t.Fatalf("insert user commands: %v", err)
	}

	body := bytes.NewBufferString(`{"message":"/review queued file"}`)
	req := httptest.NewRequest(http.MethodPost, "/narrators/"+narratorID+"/messages", body)
	req.Header.Set("Content-Type", "application/json")
	w := serveNarratorHandlerHTTPRequest(sqlDB, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
	var text, commandText string
	if err := sqlDB.QueryRow(`SELECT text, COALESCE(command_text, '') FROM narrator_buffered_messages WHERE narrator_id = ?`, narratorID).Scan(&text, &commandText); err != nil {
		t.Fatalf("load buffered command row: %v", err)
	}
	if text != "Review queued file" || commandText != "/review queued file" {
		t.Fatalf("unexpected buffered slash command text=%q commandText=%q", text, commandText)
	}
}

func TestReorderBufferRequiresExactIDsAndUsesZeroBasedSeqs(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := sqlDB.Exec(`INSERT INTO narrator_buffered_messages (id, narrator_id, text, images_json, command_text, created_by, creator_json, text_file_paths_json, priority, seq, buffered_at) VALUES (?, ?, ?, NULL, NULL, ?, NULL, NULL, 0, 0, ?), (?, ?, ?, NULL, NULL, ?, NULL, NULL, 0, 1, ?)`, "buf-a", narratorID, "first", "user-1", now, "buf-b", narratorID, "second", "user-1", now); err != nil {
		t.Fatalf("insert buffered rows: %v", err)
	}
	hub, events := captureNarratorBroadcasts()
	service := &Service{DB: sqlDB, Hub: hub}

	badReq := httptest.NewRequest(http.MethodPut, "/narrators/"+narratorID+"/buffer/reorder", bytes.NewBufferString(`{"orderedIds":["buf-b"]}`))
	badReq.Header.Set("Content-Type", "application/json")
	bad := serveNarratorHandlerWithService(service, badReq)
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("expected partial reorder to fail with 400, got %d body=%s", bad.Code, bad.Body.String())
	}

	goodReq := httptest.NewRequest(http.MethodPut, "/narrators/"+narratorID+"/buffer/reorder", bytes.NewBufferString(`{"orderedIds":["buf-b","buf-a"]}`))
	goodReq.Header.Set("Content-Type", "application/json")
	good := serveNarratorHandlerWithService(service, goodReq)
	if good.Code != http.StatusOK {
		t.Fatalf("expected reorder 200, got %d body=%s", good.Code, good.Body.String())
	}
	seqs := map[string]int{}
	rows, err := sqlDB.Query(`SELECT id, seq FROM narrator_buffered_messages WHERE narrator_id = ?`, narratorID)
	if err != nil {
		t.Fatalf("query reordered seqs: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var idValue string
		var seq int
		if err := rows.Scan(&idValue, &seq); err != nil {
			t.Fatalf("scan reordered seqs: %v", err)
		}
		seqs[idValue] = seq
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate reordered seqs: %v", err)
	}
	if seqs["buf-b"] != 0 || seqs["buf-a"] != 1 {
		t.Fatalf("expected zero-based reordered seqs, got %#v", seqs)
	}
	event := waitForBroadcast(t, events, narratorID, "buffer_set")
	messages, ok := event["messages"].([]any)
	if !ok || len(messages) != 2 {
		t.Fatalf("expected reordered buffer_set messages, got %#v", event)
	}
	first, firstOK := messages[0].(map[string]any)
	second, secondOK := messages[1].(map[string]any)
	if !firstOK || !secondOK || first["id"] != "buf-b" || second["id"] != "buf-a" {
		t.Fatalf("expected reordered buffer_set broadcast, got %#v", event)
	}
}

func TestPostMessageBuffersMultipartTextFileWithoutDuplicatingAttachmentHint(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	ensureSlashCommandTables(t, sqlDB)
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, ".git"), []byte("gitdir: test\n"), 0o644); err != nil {
		t.Fatalf("write git marker: %v", err)
	}
	if _, err := sqlDB.Exec(`UPDATE narrators SET status = 'working', cwd = ? WHERE id = ?`, cwd, narratorID); err != nil {
		t.Fatalf("mark working: %v", err)
	}
	commands := `[{"name":"review","prompt":"Review {{input}}"}]`
	if _, err := sqlDB.Exec(`INSERT INTO user_preferences (user_id, commands) VALUES ('system', ?)`, commands); err != nil {
		t.Fatalf("insert user commands: %v", err)
	}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	if err := writer.WriteField("message", "/review queued file"); err != nil {
		t.Fatalf("write message field: %v", err)
	}
	part, err := writer.CreateFormFile("textFiles", "notes.md")
	if err != nil {
		t.Fatalf("create text file part: %v", err)
	}
	if _, err := part.Write([]byte("queued attachment body")); err != nil {
		t.Fatalf("write text file part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/narrators/"+narratorID+"/messages", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := serveNarratorHandlerHTTPRequest(sqlDB, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}

	var bufferID, text, commandText, pathsRaw string
	if err := sqlDB.QueryRow(`SELECT id, text, COALESCE(command_text, ''), COALESCE(text_file_paths_json, '') FROM narrator_buffered_messages WHERE narrator_id = ?`, narratorID).Scan(&bufferID, &text, &commandText, &pathsRaw); err != nil {
		t.Fatalf("load buffered row: %v", err)
	}
	if text != "Review queued file" || commandText != "/review queued file" {
		t.Fatalf("unexpected buffered row text=%q command=%q", text, commandText)
	}
	if strings.Contains(text, "<attached_files>") {
		t.Fatalf("buffer text should store raw expanded prompt without attachment hint: %q", text)
	}
	var refs []map[string]any
	if err := json.Unmarshal([]byte(pathsRaw), &refs); err != nil {
		t.Fatalf("decode buffered text file refs: %v raw=%s", err, pathsRaw)
	}
	if len(refs) != 1 {
		t.Fatalf("expected one text file ref, got %#v", refs)
	}
	persistedPath, _ := refs[0]["path"].(string)
	if persistedPath == "" {
		persistedPath, _ = refs[0]["persistedPath"].(string)
	}
	if persistedPath == "" || !strings.Contains(persistedPath, filepath.Join(".narrafork", "buffered-files", bufferID)) {
		t.Fatalf("expected buffered sidecar path under HOME, got %#v", refs[0])
	}
	if data, err := os.ReadFile(persistedPath); err != nil || string(data) != "queued attachment body" {
		t.Fatalf("unexpected persisted buffer file data=%q err=%v", string(data), err)
	}
	originalWorktreePath := stringFromAny(refs[0]["filePath"])
	if originalWorktreePath == "" || !strings.Contains(originalWorktreePath, filepath.Join(cwd, ".narrafork", "attached", "notes.md")) {
		t.Fatalf("expected original worktree path metadata, got %#v", refs[0])
	}
	if _, err := os.Stat(originalWorktreePath); !os.IsNotExist(err) {
		t.Fatalf("busy buffer should not leave uploaded text file in worktree before consumption, stat err=%v", err)
	}

	service := &Service{DB: sqlDB}
	buffered, ok, err := service.nextBufferedMessage(context.Background(), narratorID)
	if err != nil || !ok {
		t.Fatalf("load next buffered message ok=%v err=%v", ok, err)
	}
	effective := buffered.EffectiveText()
	if strings.Count(effective, "<attached_files>") != 1 || !strings.Contains(effective, filepath.Join(cwd, ".narrafork", "attached", "notes")) {
		t.Fatalf("effective text should contain one worktree attachment hint, got %q", effective)
	}
	blocks := buffered.ContentBlocks()
	textFileBlocks := 0
	for _, block := range blocks {
		if block["type"] == "text_file" {
			textFileBlocks++
			if got := stringFromAny(block["filePath"]); got == "" || !strings.Contains(got, filepath.Join(cwd, ".narrafork", "attached", "notes")) {
				t.Fatalf("unexpected materialized text_file block: %#v", block)
			}
		}
	}
	if textFileBlocks != 1 {
		t.Fatalf("expected one materialized text_file block, got %d blocks=%#v", textFileBlocks, blocks)
	}
	materializedPath := filepath.Join(cwd, ".narrafork", "attached", "notes.md")
	if data, err := os.ReadFile(materializedPath); err != nil || string(data) != "queued attachment body" {
		t.Fatalf("unexpected materialized worktree file data=%q err=%v", string(data), err)
	}
}

func TestPostMessageBufferedStorageMissingReturnsReasonedFallback(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	if _, err := sqlDB.Exec(`UPDATE narrators SET status = 'working' WHERE id = ?`, narratorID); err != nil {
		t.Fatalf("mark working: %v", err)
	}
	if _, err := sqlDB.Exec(`DROP TABLE narrator_buffered_messages`); err != nil {
		t.Fatalf("drop buffered messages: %v", err)
	}

	body := bytes.NewBufferString(`{"message":"cut in line"}`)
	req := httptest.NewRequest(http.MethodPost, "/narrators/"+narratorID+"/messages", body)
	req.Header.Set("Content-Type", "application/json")
	w := serveNarratorHandlerHTTPRequest(sqlDB, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202 fallback, got %d body=%s", w.Code, w.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	reason, _ := payload["reason"].(string)
	if payload["ok"] != false || payload["queued"] != false || payload["fallback"] != true || reason == "" || payload["error"] != reason || payload["message"] != reason {
		t.Fatalf("buffered storage fallback should expose reason/error/message, got %#v", payload)
	}
}

func TestDeletedMessageCountAfterSeqReturnsQueryErrors(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	for _, stmt := range []string{
		`ALTER TABLE narrator_message_refs RENAME TO narrator_message_refs_real`,
		`CREATE VIEW narrator_message_refs AS SELECT id, narrator_id, message_id, is_compact FROM narrator_message_refs_real`,
	} {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("prepare broken refs view: %v", err)
		}
	}
	h := &Handler{Service: &Service{DB: sqlDB}}

	_, err := h.deletedMessageCountAfterSeq(context.Background(), "narrator-1", 1)
	if err == nil || !strings.Contains(err.Error(), "seq") {
		t.Fatalf("expected deleted message count query error, got %v", err)
	}
}

func TestBufferUserMessageReturnsSequenceQueryErrorWithoutInsert(t *testing.T) {
	service, narratorID, _ := newBufferedTestNarratorService(t, "openai:test-model", "http://127.0.0.1")
	defer service.DB.Close()
	if _, err := service.DB.Exec(`INSERT INTO narrator_buffered_messages (id, narrator_id, text, images_json, command_text, created_by, creator_json, text_file_paths_json, priority, seq, buffered_at) VALUES (?, ?, ?, NULL, NULL, ?, NULL, NULL, 0, 0, ?)`, "existing", narratorID, "already queued", "user-1", time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert existing buffer: %v", err)
	}
	stmts := []string{
		`ALTER TABLE narrator_buffered_messages RENAME TO narrator_buffered_messages_real`,
		`CREATE VIEW narrator_buffered_messages AS SELECT id, narrator_id, text, images_json, command_text, created_by, creator_json, text_file_paths_json, priority, json_extract('not-json', '$') AS seq, buffered_at FROM narrator_buffered_messages_real`,
		`CREATE TRIGGER narrator_buffered_messages_insert INSTEAD OF INSERT ON narrator_buffered_messages BEGIN INSERT INTO narrator_buffered_messages_real (id, narrator_id, text, images_json, command_text, created_by, creator_json, text_file_paths_json, priority, seq, buffered_at) VALUES (NEW.id, NEW.narrator_id, NEW.text, NEW.images_json, NEW.command_text, NEW.created_by, NEW.creator_json, NEW.text_file_paths_json, NEW.priority, NEW.seq, NEW.buffered_at); END;`,
	}
	for _, stmt := range stmts {
		if _, err := service.DB.Exec(stmt); err != nil {
			t.Fatalf("prepare broken buffer view: %v", err)
		}
	}

	_, err := service.BufferUserMessage(context.Background(), narratorID, "user-1", "queued after failure", nil, false)
	if err == nil || !strings.Contains(err.Error(), "query next buffered message sequence") {
		t.Fatalf("expected buffered sequence query error, got %v", err)
	}
	var count int
	if err := service.DB.QueryRow(`SELECT COUNT(*) FROM narrator_buffered_messages_real`).Scan(&count); err != nil {
		t.Fatalf("count backing buffer rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("sequence query failure should not insert fallback buffer row, count=%d", count)
	}
}

func TestFinishAgentRunConsumesNextBufferedMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "processed queued"}}},
		})
	}))
	defer server.Close()

	service, narratorID, _ := newBufferedTestNarratorService(t, "openai:test-model", server.URL)
	defer service.DB.Close()
	if _, err := service.DB.Exec(`INSERT INTO narrator_buffered_messages (id, narrator_id, text, images_json, command_text, created_by, creator_json, text_file_paths_json, priority, seq, buffered_at) VALUES (?, ?, ?, NULL, NULL, ?, NULL, NULL, 0, 0, ?)`, "buf_1", narratorID, "queued follow-up", "user_1", time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert buffer: %v", err)
	}

	state := &agentRunState{narratorID: narratorID, narrator: Narrator{ID: narratorID, Status: "working"}}
	if err := service.finishAgentRunSuccess(context.Background(), state); err != nil {
		t.Fatalf("finish run: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var remaining int
		_ = service.DB.QueryRow(`SELECT COUNT(*) FROM narrator_buffered_messages WHERE narrator_id = ?`, narratorID).Scan(&remaining)
		var assistantText sql.NullString
		_ = service.DB.QueryRow(`SELECT content_text FROM narrator_messages WHERE narrator_id = ? AND role = 'assistant' ORDER BY created_at DESC LIMIT 1`, narratorID).Scan(&assistantText)
		if remaining == 0 && assistantText.Valid && strings.Contains(assistantText.String, "processed queued") {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("buffer was not consumed and processed before timeout")
}

func newBufferedTestNarratorService(t *testing.T, model, baseURL string) (*Service, string, string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	settings, err := config.Load()
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	_, err = settings.Patch(map[string]any{
		"openaiProviders": []any{map[string]any{
			"prefix":       "openai",
			"apiKey":       "test-key",
			"baseUrl":      baseURL,
			"defaultModel": strings.TrimPrefix(model, "openai:"),
		}},
	})
	if err != nil {
		t.Fatalf("patch settings: %v", err)
	}
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := createNarratorTestSchema(sqlDB); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	service := &Service{DB: sqlDB, Settings: settings}
	narratorID := id.New()
	cwd := t.TempDir()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = sqlDB.Exec(`INSERT INTO narrators (
		id, type, title, inherit_mode, model, permission_mode, status, substatus, plan_mode,
		cwd, variant, traits, is_background, is_ask_in_passing, message_version, message_count,
		total_cost_usd, fast_mode, relaxed_plan, plan_reflection_auto_approve_override,
		danger_reflection_override, created_at, updated_at
	) VALUES (?, 'primary', 'Test narrator', 'fresh', ?, 'default', 'working', '[]', 0, ?, 'primary', '[]', 0, 0, 0, 0, 0, 0, 0, 'inherit', 'inherit', ?, ?)`, narratorID, model, cwd, now, now)
	if err != nil {
		t.Fatalf("insert narrator: %v", err)
	}
	return service, narratorID, cwd
}

func serveNarratorHandlerHTTPRequest(sqlDB *sql.DB, req *http.Request) *httptest.ResponseRecorder {
	h := NewHandler(&Service{DB: sqlDB})
	r := chi.NewRouter()
	r.Route("/narrators", h.RegisterRoutes)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}
