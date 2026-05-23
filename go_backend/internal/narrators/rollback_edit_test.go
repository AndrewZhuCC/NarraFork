package narrators

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"narrafork-go/internal/id"
)

func TestSQLTableExistsUsesDialectColumnHelper(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	exists, err := sqlTableExists(context.Background(), sqlDB, "narrators")
	if err != nil {
		t.Fatalf("sqlTableExists narrators: %v", err)
	}
	if !exists {
		t.Fatalf("expected narrators table to exist")
	}
	exists, err = sqlTableExists(context.Background(), sqlDB, "missing_table")
	if err != nil {
		t.Fatalf("sqlTableExists missing: %v", err)
	}
	if exists {
		t.Fatalf("missing table should not exist")
	}
}

func TestRollbackToBlockTruncatesBlocksDeletesFollowingRefsAndRebuildsFileState(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	service := &Service{DB: sqlDB}
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	cwd := t.TempDir()
	if _, err := sqlDB.Exec(`UPDATE narrators SET cwd = ? WHERE id = ?`, cwd, narratorID); err != nil {
		t.Fatalf("set cwd: %v", err)
	}
	filePath := filepath.Join(cwd, "note.txt")
	if err := os.WriteFile(filePath, []byte("changed"), 0o644); err != nil {
		t.Fatalf("write changed file: %v", err)
	}
	base := time.Now().UTC()
	if _, err := sqlDB.Exec(`INSERT INTO narrator_file_snapshots (id, narrator_id, file_path, original_content, created_at) VALUES ('snap_note', ?, ?, 'original', ?)`, narratorID, filePath, base.Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert snapshot: %v", err)
	}
	assistant, err := service.addMessageWithBlocks(context.Background(), narratorID, "assistant", "before\nafter", []map[string]any{
		{"type": "text", "text": "before"},
		{"type": "tool_use", "id": "write-rollback-1", "name": "Write", "input": map[string]any{"file_path": "note.txt", "content": "changed"}},
		{"type": "text", "text": "after"},
	}, nil)
	if err != nil {
		t.Fatalf("insert assistant: %v", err)
	}
	insertNarratorToolCallForSnapshotTest(t, sqlDB, narratorID, assistant.ID, "write-rollback-1", "Write", map[string]any{"file_path": "note.txt", "content": "changed"}, base.Add(time.Second))
	following, err := service.addMessage(context.Background(), narratorID, "assistant", "later", nil)
	if err != nil {
		t.Fatalf("insert following: %v", err)
	}

	payload, err := service.RollbackToBlock(context.Background(), narratorID, assistant.ID, 0, true)
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if payload["ok"] != true {
		t.Fatalf("unexpected rollback payload: %#v", payload)
	}
	var followingRefs int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM narrator_message_refs WHERE message_id = ?`, following.ID).Scan(&followingRefs); err != nil {
		t.Fatalf("count following refs: %v", err)
	}
	if followingRefs != 0 {
		t.Fatalf("following message ref should be deleted, got %d", followingRefs)
	}
	var raw string
	if err := sqlDB.QueryRow(`SELECT content_json FROM narrator_messages WHERE id = ?`, assistant.ID).Scan(&raw); err != nil {
		t.Fatalf("load rolled back message: %v", err)
	}
	var blocks []map[string]any
	if err := json.Unmarshal([]byte(raw), &blocks); err != nil {
		t.Fatalf("decode blocks: %v", err)
	}
	if len(blocks) != 1 || blocks[0]["type"] != "text" || blocks[0]["text"] != "before" {
		t.Fatalf("unexpected rolled back blocks: %#v", blocks)
	}
	var staleToolCalls int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM narrator_tool_calls WHERE tool_use_id = 'write-rollback-1'`).Scan(&staleToolCalls); err != nil {
		t.Fatalf("count stale tool calls: %v", err)
	}
	if staleToolCalls != 0 {
		t.Fatalf("tool call for removed block should be invalidated/deleted, got %d", staleToolCalls)
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read rebuilt file: %v", err)
	}
	if string(data) != "original" {
		t.Fatalf("file should be rebuilt to original content, got %q", string(data))
	}
}

func TestRollbackFileApplyFailureLeavesMessageHistoryUnchanged(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	service := &Service{DB: sqlDB}
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	cwd := t.TempDir()
	if _, err := sqlDB.Exec(`UPDATE narrators SET cwd = ? WHERE id = ?`, cwd, narratorID); err != nil {
		t.Fatalf("set cwd: %v", err)
	}
	filePath := filepath.Join(cwd, "note.txt")
	if err := os.Mkdir(filePath, 0o755); err != nil {
		t.Fatalf("create directory at target path: %v", err)
	}
	base := time.Now().UTC()
	if _, err := sqlDB.Exec(`INSERT INTO narrator_file_snapshots (id, narrator_id, file_path, original_content, created_at) VALUES ('snap_note_dir_failure', ?, ?, 'original', ?)`, narratorID, filePath, base.Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert snapshot: %v", err)
	}
	assistant, err := service.addMessageWithBlocks(context.Background(), narratorID, "assistant", "before\nmiddle\nafter", []map[string]any{
		{"type": "text", "text": "before"},
		{"type": "text", "text": "middle"},
		{"type": "text", "text": "after"},
	}, nil)
	if err != nil {
		t.Fatalf("insert assistant: %v", err)
	}
	following, err := service.addMessageWithBlocks(context.Background(), narratorID, "assistant", "later", []map[string]any{
		{"type": "tool_use", "id": "write-rollback-dir-failure", "name": "Write", "input": map[string]any{"file_path": "note.txt", "content": "changed"}},
	}, nil)
	if err != nil {
		t.Fatalf("insert following: %v", err)
	}
	insertNarratorToolCallForSnapshotTest(t, sqlDB, narratorID, following.ID, "write-rollback-dir-failure", "Write", map[string]any{"file_path": "note.txt", "content": "changed"}, base.Add(time.Second))

	_, err = service.RollbackToBlock(context.Background(), narratorID, assistant.ID, 0, true)
	if err == nil {
		t.Fatalf("expected rollback to fail when target file path is a directory")
	}
	var followingRefs int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM narrator_message_refs WHERE message_id = ?`, following.ID).Scan(&followingRefs); err != nil {
		t.Fatalf("count following refs: %v", err)
	}
	if followingRefs != 1 {
		t.Fatalf("following message ref should remain after file failure, got %d", followingRefs)
	}
	var raw string
	if err := sqlDB.QueryRow(`SELECT content_json FROM narrator_messages WHERE id = ?`, assistant.ID).Scan(&raw); err != nil {
		t.Fatalf("load assistant content: %v", err)
	}
	var blocks []map[string]any
	if err := json.Unmarshal([]byte(raw), &blocks); err != nil {
		t.Fatalf("decode blocks: %v", err)
	}
	if len(blocks) != 3 {
		t.Fatalf("assistant blocks should remain unchanged after file failure: %#v", blocks)
	}
	if info, statErr := os.Stat(filePath); statErr != nil || !info.IsDir() {
		t.Fatalf("target directory should remain after failure, info=%v err=%v", info, statErr)
	}
}

func TestRollbackRevertFalseSkipsFileStateRebuild(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	service := &Service{DB: sqlDB}
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	cwd := t.TempDir()
	if _, err := sqlDB.Exec(`UPDATE narrators SET cwd = ? WHERE id = ?`, cwd, narratorID); err != nil {
		t.Fatalf("set cwd: %v", err)
	}
	filePath := filepath.Join(cwd, "note.txt")
	if err := os.WriteFile(filePath, []byte("changed"), 0o644); err != nil {
		t.Fatalf("write changed file: %v", err)
	}
	base := time.Now().UTC()
	if _, err := sqlDB.Exec(`INSERT INTO narrator_file_snapshots (id, narrator_id, file_path, original_content, created_at) VALUES ('snap_note_no_revert', ?, ?, 'original', ?)`, narratorID, filePath, base.Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert snapshot: %v", err)
	}
	assistant, err := service.addMessageWithBlocks(context.Background(), narratorID, "assistant", "before\nafter", []map[string]any{
		{"type": "text", "text": "before"},
		{"type": "tool_use", "id": "write-no-revert-1", "name": "Write", "input": map[string]any{"file_path": "note.txt", "content": "changed"}},
		{"type": "text", "text": "after"},
	}, nil)
	if err != nil {
		t.Fatalf("insert assistant: %v", err)
	}
	insertNarratorToolCallForSnapshotTest(t, sqlDB, narratorID, assistant.ID, "write-no-revert-1", "Write", map[string]any{"file_path": "note.txt", "content": "changed"}, base.Add(time.Second))
	following, err := service.addMessage(context.Background(), narratorID, "assistant", "later", nil)
	if err != nil {
		t.Fatalf("insert following: %v", err)
	}

	payload, err := service.RollbackToBlock(context.Background(), narratorID, assistant.ID, 0, false)
	if err != nil {
		t.Fatalf("rollback without revert: %v", err)
	}
	if payload["reverted"] != false {
		t.Fatalf("expected reverted=false payload, got %#v", payload)
	}
	var followingRefs int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM narrator_message_refs WHERE message_id = ?`, following.ID).Scan(&followingRefs); err != nil {
		t.Fatalf("count following refs: %v", err)
	}
	if followingRefs != 0 {
		t.Fatalf("following message ref should still be deleted, got %d", followingRefs)
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(data) != "changed" {
		t.Fatalf("file should not be rebuilt when revert=false, got %q", string(data))
	}
}

func TestEditAndRegeneratePreservesImageBlocksDeletesOldResponseAndReruns(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "regenerated answer"}}}})
	}))
	defer server.Close()
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", server.URL)
	var userID string
	if err := service.DB.QueryRow(`SELECT id FROM narrator_messages WHERE narrator_id = ? AND role = 'user' ORDER BY created_at ASC LIMIT 1`, narratorID).Scan(&userID); err != nil {
		t.Fatalf("load user message: %v", err)
	}
	originalBlocks := []map[string]any{
		{"type": "image", "imageId": "img-1", "filename": "one.png", "mediaType": "image/png"},
		{"type": "text", "text": "original text"},
	}
	raw, _ := json.Marshal(originalBlocks)
	if _, err := service.DB.Exec(`UPDATE narrator_messages SET content_json = ?, content_text = 'original text' WHERE id = ?`, string(raw), userID); err != nil {
		t.Fatalf("update user content: %v", err)
	}
	oldAssistant, err := service.addMessage(context.Background(), narratorID, "assistant", "old response", nil)
	if err != nil {
		t.Fatalf("insert old assistant: %v", err)
	}

	result, err := service.EditAndRegenerate(context.Background(), narratorID, userID, "updated text", true, true)
	if err != nil {
		t.Fatalf("edit and regenerate: %v", err)
	}
	if result.PrivateMessageID != userID {
		t.Fatalf("non-shared edit should keep message id, got %s", result.PrivateMessageID)
	}
	waitForAssistantText(t, service, narratorID, "regenerated answer")
	var oldRefCount int
	if err := service.DB.QueryRow(`SELECT COUNT(*) FROM narrator_message_refs WHERE narrator_id = ? AND message_id = ?`, narratorID, oldAssistant.ID).Scan(&oldRefCount); err != nil {
		t.Fatalf("count old assistant refs: %v", err)
	}
	if oldRefCount != 0 {
		t.Fatalf("old assistant response should be deleted, got refs=%d", oldRefCount)
	}
	var updatedRaw string
	if err := service.DB.QueryRow(`SELECT content_json FROM narrator_messages WHERE id = ?`, userID).Scan(&updatedRaw); err != nil {
		t.Fatalf("load updated user content: %v", err)
	}
	var updatedBlocks []map[string]any
	if err := json.Unmarshal([]byte(updatedRaw), &updatedBlocks); err != nil {
		t.Fatalf("decode updated user content: %v", err)
	}
	if len(updatedBlocks) != 2 || updatedBlocks[0]["type"] != "image" || updatedBlocks[0]["uploadNarratorId"] != narratorID || updatedBlocks[1]["text"] != "updated text" {
		t.Fatalf("edit should preserve image order and upload owner while replacing text: %#v", updatedBlocks)
	}
}

func TestEditAndRegenerateCanSkipRerunAndFileRevert(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	service := &Service{DB: sqlDB}
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	cwd := t.TempDir()
	if _, err := sqlDB.Exec(`UPDATE narrators SET cwd = ? WHERE id = ?`, cwd, narratorID); err != nil {
		t.Fatalf("set cwd: %v", err)
	}
	filePath := filepath.Join(cwd, "note.txt")
	if err := os.WriteFile(filePath, []byte("changed"), 0o644); err != nil {
		t.Fatalf("write changed file: %v", err)
	}
	base := time.Now().UTC()
	if _, err := sqlDB.Exec(`INSERT INTO narrator_file_snapshots (id, narrator_id, file_path, original_content, created_at) VALUES ('snap_edit_no_revert', ?, ?, 'original', ?)`, narratorID, filePath, base.Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert snapshot: %v", err)
	}
	userMsg, err := service.addMessageWithBlocks(context.Background(), narratorID, "user", "old", []map[string]any{{"type": "text", "text": "old"}}, nil)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	assistant, err := service.addMessageWithBlocks(context.Background(), narratorID, "assistant", "tool", []map[string]any{{"type": "tool_use", "id": "write-edit-no-revert-1", "name": "Write", "input": map[string]any{"file_path": "note.txt", "content": "changed"}}}, nil)
	if err != nil {
		t.Fatalf("insert assistant: %v", err)
	}
	insertNarratorToolCallForSnapshotTest(t, sqlDB, narratorID, assistant.ID, "write-edit-no-revert-1", "Write", map[string]any{"file_path": "note.txt", "content": "changed"}, base.Add(time.Second))

	result, err := service.EditAndRegenerate(context.Background(), narratorID, userMsg.ID, "new", false, false)
	if err != nil {
		t.Fatalf("edit without rerun/revert: %v", err)
	}
	if len(result.DeletedMessageIDs) != 1 || result.DeletedMessageIDs[0] != assistant.ID {
		t.Fatalf("expected old assistant deletion, got %#v", result.DeletedMessageIDs)
	}
	var status string
	if err := sqlDB.QueryRow(`SELECT status FROM narrators WHERE id = ?`, narratorID).Scan(&status); err != nil {
		t.Fatalf("load narrator status: %v", err)
	}
	if status != "idle" {
		t.Fatalf("regenerate=false should not start a new run, status=%s", status)
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(data) != "changed" {
		t.Fatalf("rollback=false should skip file rebuild, got %q", string(data))
	}
}

func TestEditAndRegeneratePreservesCompactBoundaryAndDeletesLongToolChain(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	service := &Service{DB: sqlDB}
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	ctx := context.Background()

	compactMessage, err := service.addMessageWithBlocks(ctx, narratorID, "system", "[Compact] Prior context", []map[string]any{
		{"type": "compact", "status": "compacted", "summary": "Prior compacted context"},
	}, nil)
	if err != nil {
		t.Fatalf("insert compact marker: %v", err)
	}
	if _, err := sqlDB.Exec(`UPDATE narrator_message_refs SET is_compact = 1 WHERE narrator_id = ? AND message_id = ?`, narratorID, compactMessage.ID); err != nil {
		t.Fatalf("mark compact ref: %v", err)
	}
	userMsg, err := service.addMessageWithBlocks(ctx, narratorID, "user", "old question", []map[string]any{{"type": "text", "text": "old question"}}, nil)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	firstTool, err := service.addMessageWithBlocks(ctx, narratorID, "assistant", "call first tool", []map[string]any{
		{"type": "text", "text": "I will inspect"},
		{"type": "tool_use", "id": "long-chain-tool-1", "name": "Read", "input": map[string]any{"file_path": "README.md"}},
	}, nil)
	if err != nil {
		t.Fatalf("insert first tool message: %v", err)
	}
	insertNarratorToolCallForSnapshotTest(t, sqlDB, narratorID, firstTool.ID, "long-chain-tool-1", "Read", map[string]any{"file_path": "README.md"}, time.Now().UTC())
	firstResult, err := service.addMessageWithBlocks(ctx, narratorID, "user", "read result", []map[string]any{{"type": "tool_result", "tool_use_id": "long-chain-tool-1", "content": "read output"}}, nil)
	if err != nil {
		t.Fatalf("insert first tool result: %v", err)
	}
	secondTool, err := service.addMessageWithBlocks(ctx, narratorID, "assistant", "call second tool", []map[string]any{
		{"type": "tool_use", "id": "long-chain-tool-2", "name": "Grep", "input": map[string]any{"pattern": "TODO", "path": "."}},
	}, nil)
	if err != nil {
		t.Fatalf("insert second tool message: %v", err)
	}
	insertNarratorToolCallForSnapshotTest(t, sqlDB, narratorID, secondTool.ID, "long-chain-tool-2", "Grep", map[string]any{"pattern": "TODO", "path": "."}, time.Now().UTC())
	secondResult, err := service.addMessageWithBlocks(ctx, narratorID, "user", "grep result", []map[string]any{{"type": "tool_result", "tool_use_id": "long-chain-tool-2", "content": "grep output"}}, nil)
	if err != nil {
		t.Fatalf("insert second tool result: %v", err)
	}
	finalAssistant, err := service.addMessageWithBlocks(ctx, narratorID, "assistant", "final answer", []map[string]any{{"type": "text", "text": "final answer"}}, nil)
	if err != nil {
		t.Fatalf("insert final assistant: %v", err)
	}

	result, err := service.EditAndRegenerate(ctx, narratorID, userMsg.ID, "new question", false, false)
	if err != nil {
		t.Fatalf("edit without rerun: %v", err)
	}
	wantDeleted := []string{firstTool.ID, firstResult.ID, secondTool.ID, secondResult.ID, finalAssistant.ID}
	if len(result.DeletedMessageIDs) != len(wantDeleted) {
		t.Fatalf("deleted message count mismatch: got %#v want %#v", result.DeletedMessageIDs, wantDeleted)
	}
	for i, want := range wantDeleted {
		if result.DeletedMessageIDs[i] != want {
			t.Fatalf("deleted message order mismatch: got %#v want %#v", result.DeletedMessageIDs, wantDeleted)
		}
	}
	var refCount, compactRef int
	if err := sqlDB.QueryRow(`SELECT COUNT(*), COALESCE(MAX(is_compact), 0) FROM narrator_message_refs WHERE narrator_id = ? AND message_id = ?`, narratorID, compactMessage.ID).Scan(&refCount, &compactRef); err != nil {
		t.Fatalf("load compact ref: %v", err)
	}
	if refCount != 1 || compactRef != 1 {
		t.Fatalf("compact boundary should be preserved, refs=%d compact=%d", refCount, compactRef)
	}
	for _, deletedID := range wantDeleted {
		var refs int
		if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM narrator_message_refs WHERE narrator_id = ? AND message_id = ?`, narratorID, deletedID).Scan(&refs); err != nil {
			t.Fatalf("count deleted ref %s: %v", deletedID, err)
		}
		if refs != 0 {
			t.Fatalf("deleted message %s should not keep refs, got %d", deletedID, refs)
		}
	}
	var staleToolCalls int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM narrator_tool_calls WHERE tool_use_id IN ('long-chain-tool-1', 'long-chain-tool-2')`).Scan(&staleToolCalls); err != nil {
		t.Fatalf("count stale tool calls: %v", err)
	}
	if staleToolCalls != 0 {
		t.Fatalf("deleted long tool chain should remove tool calls, got %d", staleToolCalls)
	}
	history, err := service.agentHistory(ctx, narratorID, 20)
	if err != nil {
		t.Fatalf("agent history: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("expected compact summary plus edited user only, got %#v", history)
	}
	if history[0].Role != "user" || !strings.Contains(history[0].Content, "Prior compacted conversation summary:\nPrior compacted context") {
		t.Fatalf("compact boundary summary mismatch: %#v", history[0])
	}
	if history[1].Role != "user" || history[1].Content != "new question" {
		t.Fatalf("edited user history mismatch: %#v", history[1])
	}
}

func TestCopyOnWriteMessageDoesNotMutateSharedMessage(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	service := &Service{DB: sqlDB}
	n1 := insertNarratorForCopyOnWriteTest(t, sqlDB, "nar_cow_1")
	n2 := insertNarratorForCopyOnWriteTest(t, sqlDB, "nar_cow_2")
	messageID := "msg_shared_cow"
	content := []map[string]any{{"type": "text", "text": "shared"}}
	contentRaw, _ := json.Marshal(content)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := sqlDB.Exec(`INSERT INTO narrator_messages (id, narrator_id, role, content_json, content_text, created_at) VALUES (?, ?, 'user', ?, 'shared', ?)`, messageID, n1, string(contentRaw), now); err != nil {
		t.Fatalf("insert shared message: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO narrator_message_refs (id, narrator_id, message_id, seq, is_compact) VALUES (?, ?, ?, 1, 0), (?, ?, ?, 1, 0)`, id.New(), n1, messageID, id.New(), n2, messageID); err != nil {
		t.Fatalf("insert shared refs: %v", err)
	}
	updatedText := "private"
	privateID, err := service.copyOnWriteMessage(context.Background(), n2, messageID, copyOnWriteOverrides{ContentText: &updatedText, ContentJSON: []map[string]any{{"type": "text", "text": updatedText}}})
	if err != nil {
		t.Fatalf("copy on write: %v", err)
	}
	if privateID == messageID {
		t.Fatalf("shared edit should create private message id")
	}
	var n1Ref, n2Ref string
	if err := sqlDB.QueryRow(`SELECT message_id FROM narrator_message_refs WHERE narrator_id = ?`, n1).Scan(&n1Ref); err != nil {
		t.Fatalf("load n1 ref: %v", err)
	}
	if err := sqlDB.QueryRow(`SELECT message_id FROM narrator_message_refs WHERE narrator_id = ?`, n2).Scan(&n2Ref); err != nil {
		t.Fatalf("load n2 ref: %v", err)
	}
	if n1Ref != messageID || n2Ref != privateID {
		t.Fatalf("unexpected refs after copy-on-write: n1=%s n2=%s private=%s", n1Ref, n2Ref, privateID)
	}
	var originalText, privateText string
	if err := sqlDB.QueryRow(`SELECT content_text FROM narrator_messages WHERE id = ?`, messageID).Scan(&originalText); err != nil {
		t.Fatalf("load original text: %v", err)
	}
	if err := sqlDB.QueryRow(`SELECT content_text FROM narrator_messages WHERE id = ?`, privateID).Scan(&privateText); err != nil {
		t.Fatalf("load private text: %v", err)
	}
	if originalText != "shared" || privateText != "private" {
		t.Fatalf("copy-on-write mutated wrong rows: original=%q private=%q", originalText, privateText)
	}
}

func TestDeleteToolUseBlockDetachesSubagentChildFromParentTree(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	service := &Service{DB: sqlDB}
	parentID := insertNarratorForCopyOnWriteTest(t, sqlDB, "nar_parent_tree")
	childID := insertNarratorForCopyOnWriteTest(t, sqlDB, "nar_child_tree")
	if _, err := sqlDB.Exec(`UPDATE narrators SET type = 'subagent', variant = 'subagent:general', parent_narrator_id = ? WHERE id = ?`, parentID, childID); err != nil {
		t.Fatalf("mark child subagent: %v", err)
	}
	assistant, err := service.addMessageWithBlocks(context.Background(), parentID, "assistant", "launch", []map[string]any{{"type": "tool_use", "id": "agent-tree-1", "name": "Agent", "input": map[string]any{"prompt": "go"}}}, nil)
	if err != nil {
		t.Fatalf("insert parent tool message: %v", err)
	}
	if _, err := service.DB.Exec(`INSERT INTO narrator_tool_calls (id, narrator_id, message_id, tool_use_id, tool_name, input_json, output_json, status, created_at) VALUES (?, ?, ?, 'agent-tree-1', 'Agent', '{}', '{}', 'success', ?)`, id.New(), parentID, assistant.ID, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert parent agent tool call: %v", err)
	}
	childMsg, err := service.addMessageWithBlocksWithCommandAndParentToolUse(context.Background(), childID, "assistant", "child", []map[string]any{{"type": "text", "text": "child"}}, nil, "", "agent-tree-1")
	if err != nil {
		t.Fatalf("insert child message: %v", err)
	}
	if _, err := service.deleteMessageBlock(context.Background(), parentID, assistant.ID, 0, false, true); err != nil {
		t.Fatalf("delete tool block: %v", err)
	}
	messages, err := service.Messages(context.Background(), parentID, 20)
	if err != nil {
		t.Fatalf("load parent messages: %v", err)
	}
	for _, msg := range messages {
		if msg.ID == assistant.ID && len(msg.Children) > 0 {
			t.Fatalf("parent message should not keep child messages after tool block delete: %#v", msg.Children)
		}
	}
	var childRows int
	if err := service.DB.QueryRow(`SELECT COUNT(*) FROM narrator_messages WHERE id = ?`, childMsg.ID).Scan(&childRows); err != nil {
		t.Fatalf("count child rows: %v", err)
	}
	if childRows != 1 {
		t.Fatalf("child subagent history should remain ownable by child narrator, got rows=%d", childRows)
	}
}

func insertNarratorForCopyOnWriteTest(t *testing.T, sqlDB *sql.DB, narratorID string) string {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := sqlDB.Exec(`INSERT INTO narrators (
		id, type, title, inherit_mode, model, permission_mode, status, substatus, plan_mode,
		cwd, variant, traits, is_background, is_ask_in_passing, message_version, message_count,
		total_cost_usd, fast_mode, relaxed_plan, plan_reflection_auto_approve_override,
		danger_reflection_override, created_at, updated_at
	) VALUES (?, 'primary', ?, 'fresh', 'openai:test', 'default', 'idle', '[]', 0, '', 'primary', '[]', 0, 0, 0, 0, 0, 0, 0, 'inherit', 'inherit', ?, ?)`, narratorID, strings.TrimPrefix(narratorID, "nar_"), now, now)
	if err != nil {
		t.Fatalf("insert narrator %s: %v", narratorID, err)
	}
	return narratorID
}
