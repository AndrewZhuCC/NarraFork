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

// TestReorderGoalsRequiresOrderedIdsArrayLikeTS locks PUT /narrators/{id}/goals/reorder
// against TS (server/routes/narrators.ts): `if (!Array.isArray(body.orderedIds))
// throw "orderedIds is required"`. A missing OR non-array orderedIds must 400; an
// empty array is valid (no-op). Go previously decoded into a typed []string, so a
// missing orderedIds became a nil slice that silently returned 200, and a
// non-array value failed with a generic "invalid JSON" message.
func TestReorderGoalsRequiresOrderedIdsArrayLikeTS(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	service := &Service{DB: sqlDB}

	for _, tc := range []struct{ name, body string }{
		{"missing orderedIds", `{}`},
		{"orderedIds string", `{"orderedIds":"foo"}`},
		{"orderedIds number", `{"orderedIds":123}`},
		{"orderedIds object", `{"orderedIds":{"a":1}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPut, "/narrators/"+narratorID+"/goals/reorder", bytes.NewBufferString(tc.body))
			req.Header.Set("Content-Type", "application/json")
			w := serveNarratorHandlerWithService(service, req)
			if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "orderedIds is required") {
				t.Fatalf("expected 400 \"orderedIds is required\" for %s, got %d body=%s", tc.name, w.Code, w.Body.String())
			}
		})
	}

	// An empty array is valid (Array.isArray([]) is true) -> 200 no-op reorder.
	emptyReq := httptest.NewRequest(http.MethodPut, "/narrators/"+narratorID+"/goals/reorder", bytes.NewBufferString(`{"orderedIds":[]}`))
	emptyReq.Header.Set("Content-Type", "application/json")
	empty := serveNarratorHandlerWithService(service, emptyReq)
	if empty.Code != http.StatusOK {
		t.Fatalf("expected empty orderedIds array to 200, got %d body=%s", empty.Code, empty.Body.String())
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

func TestPostMessageImmediateMultipartPersistsImageAndTextFileBlocks(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, ".git"), []byte("gitdir: test\n"), 0o644); err != nil {
		t.Fatalf("write git marker: %v", err)
	}
	// Idle narrator with a cwd: the POST must send immediately (201) instead of buffering.
	if _, err := sqlDB.Exec(`UPDATE narrators SET status = 'idle', cwd = ? WHERE id = ?`, cwd, narratorID); err != nil {
		t.Fatalf("set narrator cwd: %v", err)
	}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	if err := writer.WriteField("message", "look at these"); err != nil {
		t.Fatalf("write message field: %v", err)
	}
	imagePart, err := writer.CreateFormFile("images", "diagram.png")
	if err != nil {
		t.Fatalf("create image part: %v", err)
	}
	// Minimal valid PNG header so content-type detection accepts it.
	pngHeader := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}
	if _, err := imagePart.Write(append(pngHeader, []byte("fake-image-bytes")...)); err != nil {
		t.Fatalf("write image part: %v", err)
	}
	textPart, err := writer.CreateFormFile("textFiles", "notes.md")
	if err != nil {
		t.Fatalf("create text file part: %v", err)
	}
	if _, err := textPart.Write([]byte("attached note body")); err != nil {
		t.Fatalf("write text file part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/narrators/"+narratorID+"/messages", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := serveNarratorHandlerHTTPRequest(sqlDB, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected immediate multipart send 201, got %d body=%s", w.Code, w.Body.String())
	}

	var contentJSON, contentText string
	if err := sqlDB.QueryRow(`SELECT content_json, content_text FROM narrator_messages WHERE narrator_id = ? AND role = 'user' ORDER BY created_at DESC LIMIT 1`, narratorID).Scan(&contentJSON, &contentText); err != nil {
		t.Fatalf("load persisted user message: %v", err)
	}
	var blocks []map[string]any
	if err := json.Unmarshal([]byte(contentJSON), &blocks); err != nil {
		t.Fatalf("decode content blocks: %v raw=%s", err, contentJSON)
	}
	imageBlocks, textFileBlocks, textBlocks := 0, 0, 0
	for _, block := range blocks {
		switch block["type"] {
		case "image":
			imageBlocks++
			if stringFromAny(block["imageId"]) == "" || stringFromAny(block["filename"]) != "diagram.png" {
				t.Fatalf("unexpected image block: %#v", block)
			}
		case "text_file":
			textFileBlocks++
			if got := stringFromAny(block["filePath"]); got == "" || !strings.Contains(got, filepath.Join(cwd, ".narrafork", "attached", "notes")) {
				t.Fatalf("unexpected text_file block: %#v", block)
			}
		case "text":
			textBlocks++
		}
	}
	if imageBlocks != 1 || textFileBlocks != 1 || textBlocks != 1 {
		t.Fatalf("expected one image + one text_file + one text block, got image=%d textFile=%d text=%d blocks=%#v", imageBlocks, textFileBlocks, textBlocks, blocks)
	}
	if !strings.Contains(contentText, "look at these") || !strings.Contains(contentText, "<attached_files>") {
		t.Fatalf("content text should include message plus attachment hint, got %q", contentText)
	}
	// The text file must be materialized into the worktree for the immediate send.
	materialized := filepath.Join(cwd, ".narrafork", "attached", "notes.md")
	if data, err := os.ReadFile(materialized); err != nil || string(data) != "attached note body" {
		t.Fatalf("unexpected materialized worktree file data=%q err=%v", string(data), err)
	}
	// Image upload must be persisted under the narrator uploads directory.
	imageID := ""
	for _, block := range blocks {
		if block["type"] == "image" {
			imageID = stringFromAny(block["imageId"])
		}
	}
	uploadDir := filepath.Join(narratorUploadsRoot(), narratorID)
	entries, err := os.ReadDir(uploadDir)
	if err != nil || len(entries) == 0 {
		t.Fatalf("expected persisted image upload under %s, entries=%#v err=%v", uploadDir, entries, err)
	}
	foundImage := false
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), imageID) {
			foundImage = true
		}
	}
	if !foundImage {
		t.Fatalf("expected uploaded image file prefixed with %q in %#v", imageID, entries)
	}
}

func TestBufferUpdateRemoveClearBroadcastBufferSet(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := sqlDB.Exec(`INSERT INTO narrator_buffered_messages (id, narrator_id, text, images_json, command_text, created_by, creator_json, text_file_paths_json, priority, seq, buffered_at) VALUES (?, ?, ?, NULL, NULL, ?, NULL, NULL, 0, 0, ?), (?, ?, ?, NULL, NULL, ?, NULL, NULL, 0, 1, ?)`, "buf-a", narratorID, "first", "user-1", now, "buf-b", narratorID, "second", "user-1", now); err != nil {
		t.Fatalf("insert buffered rows: %v", err)
	}
	hub, events := captureNarratorBroadcasts()
	service := &Service{DB: sqlDB, Hub: hub}

	updateReq := httptest.NewRequest(http.MethodPatch, "/narrators/"+narratorID+"/buffer/buf-a", bytes.NewBufferString(`{"text":"updated first","priority":true,"commandText":"/review updated first"}`))
	updateReq.Header.Set("Content-Type", "application/json")
	updated := serveNarratorHandlerWithService(service, updateReq)
	if updated.Code != http.StatusOK {
		t.Fatalf("update buffer got %d body=%s", updated.Code, updated.Body.String())
	}
	var updatePayload map[string]any
	if err := json.Unmarshal(updated.Body.Bytes(), &updatePayload); err != nil {
		t.Fatalf("decode update buffer: %v", err)
	}
	message, ok := updatePayload["message"].(map[string]any)
	if !ok || message["text"] != "updated first" || message["priority"] != true || message["commandText"] != "/review updated first" {
		t.Fatalf("unexpected updated buffer payload: %#v", updatePayload)
	}
	updateEvent := waitForBroadcast(t, events, narratorID, "buffer_set")
	messages, ok := updateEvent["messages"].([]any)
	if !ok || len(messages) != 2 {
		t.Fatalf("expected update buffer_set with two messages, got %#v", updateEvent)
	}

	removeReq := httptest.NewRequest(http.MethodDelete, "/narrators/"+narratorID+"/buffer/buf-a", nil)
	removed := serveNarratorHandlerWithService(service, removeReq)
	if removed.Code != http.StatusOK {
		t.Fatalf("remove buffer got %d body=%s", removed.Code, removed.Body.String())
	}
	var removePayload map[string]any
	if err := json.Unmarshal(removed.Body.Bytes(), &removePayload); err != nil {
		t.Fatalf("decode remove buffer: %v", err)
	}
	bufferAfterRemove, ok := removePayload["buffer"].([]any)
	if removePayload["removed"] != true || !ok || len(bufferAfterRemove) != 1 {
		t.Fatalf("unexpected remove buffer payload: %#v", removePayload)
	}

	clearReq := httptest.NewRequest(http.MethodDelete, "/narrators/"+narratorID+"/buffer", nil)
	cleared := serveNarratorHandlerWithService(service, clearReq)
	if cleared.Code != http.StatusOK {
		t.Fatalf("clear buffer got %d body=%s", cleared.Code, cleared.Body.String())
	}
	var clearPayload map[string]any
	if err := json.Unmarshal(cleared.Body.Bytes(), &clearPayload); err != nil {
		t.Fatalf("decode clear buffer: %v", err)
	}
	if clearPayload["ok"] != true || clearPayload["cleared"] != float64(1) {
		t.Fatalf("unexpected clear buffer payload: %#v", clearPayload)
	}
	bufferEvents := 0
	for _, event := range events() {
		if event.NarratorID == narratorID && event.Payload["type"] == "buffer_set" {
			bufferEvents++
		}
	}
	if bufferEvents != 3 {
		t.Fatalf("expected update/remove/clear buffer_set broadcasts, got %d events=%#v", bufferEvents, events())
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

func TestRetryLastUserMessageDeletesTrailingMessagesAndReturnsUserMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "retried answer"}}},
		})
	}))
	defer server.Close()

	service, narratorID, _ := newBufferedTestNarratorService(t, "openai:test-model", server.URL)
	defer service.DB.Close()
	// Idle narrator so retry restarts a fresh run instead of buffering.
	if _, err := service.DB.Exec(`UPDATE narrators SET status = 'idle' WHERE id = ?`, narratorID); err != nil {
		t.Fatalf("mark idle: %v", err)
	}
	hub, events := captureNarratorBroadcasts()
	service.Hub = hub

	ctx := context.Background()
	userMsg, err := service.SeedUserMessage(ctx, narratorID, "user-1", "first question")
	if err != nil {
		t.Fatalf("seed user message: %v", err)
	}
	assistantMsg, err := service.addMessage(ctx, narratorID, "assistant", "stale answer", nil)
	if err != nil {
		t.Fatalf("seed assistant message: %v", err)
	}

	retried, deletedIDs, err := service.RetryLastUserMessage(ctx, narratorID)
	if err != nil {
		t.Fatalf("retry last user message: %v", err)
	}
	if retried.ID != userMsg.ID {
		t.Fatalf("retry should return the last user message, got %q want %q", retried.ID, userMsg.ID)
	}
	foundAssistant := false
	for _, id := range deletedIDs {
		if id == assistantMsg.ID {
			foundAssistant = true
		}
	}
	if !foundAssistant {
		t.Fatalf("retry should delete the trailing assistant message, deletedIDs=%#v", deletedIDs)
	}
	// The trailing assistant ref must be removed while the user message ref survives.
	var assistantRefs, userRefs int
	if err := service.DB.QueryRow(`SELECT COUNT(*) FROM narrator_message_refs WHERE narrator_id = ? AND message_id = ?`, narratorID, assistantMsg.ID).Scan(&assistantRefs); err != nil {
		t.Fatalf("count assistant refs: %v", err)
	}
	if err := service.DB.QueryRow(`SELECT COUNT(*) FROM narrator_message_refs WHERE narrator_id = ? AND message_id = ?`, narratorID, userMsg.ID).Scan(&userRefs); err != nil {
		t.Fatalf("count user refs: %v", err)
	}
	if assistantRefs != 0 || userRefs != 1 {
		t.Fatalf("retry should drop trailing assistant ref and keep the user ref, assistantRefs=%d userRefs=%d", assistantRefs, userRefs)
	}
	if event := waitForBroadcast(t, events, narratorID, "messages_deleted"); event["deletedMessageIds"] == nil {
		t.Fatalf("retry should broadcast messages_deleted, got %#v", event)
	}
	// Allow the restarted run to settle so the deferred goroutine does not race the DB close.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var status string
		_ = service.DB.QueryRow(`SELECT status FROM narrators WHERE id = ?`, narratorID).Scan(&status)
		if status == "idle" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestContinueRunInjectsContinueUserMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "continued answer"}}},
		})
	}))
	defer server.Close()

	service, narratorID, _ := newBufferedTestNarratorService(t, "openai:test-model", server.URL)
	defer service.DB.Close()
	if _, err := service.DB.Exec(`UPDATE narrators SET status = 'idle' WHERE id = ?`, narratorID); err != nil {
		t.Fatalf("mark idle: %v", err)
	}

	msg, err := service.AddUserMessage(context.Background(), narratorID, "user-1", "Continue.")
	if err != nil {
		t.Fatalf("continue run: %v", err)
	}
	if msg.ContentText == nil || *msg.ContentText != "Continue." || msg.Role != "user" {
		t.Fatalf("continue should inject a Continue. user message, got %#v", msg)
	}
	var count int
	if err := service.DB.QueryRow(`SELECT COUNT(*) FROM narrator_messages WHERE narrator_id = ? AND role = 'user' AND content_text = 'Continue.'`, narratorID).Scan(&count); err != nil {
		t.Fatalf("count continue messages: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected one persisted Continue. user message, got %d", count)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var status string
		_ = service.DB.QueryRow(`SELECT status FROM narrators WHERE id = ?`, narratorID).Scan(&status)
		if status == "idle" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
}
