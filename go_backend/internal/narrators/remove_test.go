package narrators

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDeleteNarratorRemovesArchivedRow(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	if _, err := sqlDB.Exec(`UPDATE narrators SET status = 'archived' WHERE id = ?`, narratorID); err != nil {
		t.Fatalf("archive narrator: %v", err)
	}
	uploadDir := filepath.Join(home, ".narrafork", "uploads", narratorID)
	if err := os.MkdirAll(uploadDir, 0o755); err != nil {
		t.Fatalf("create upload dir: %v", err)
	}

	w := serveNarratorHandlerRequest(sqlDB, http.MethodDelete, "/narrators/"+narratorID)
	if w.Code != http.StatusOK {
		t.Fatalf("expected delete 200, got %d body=%s", w.Code, w.Body.String())
	}
	assertRowCount(t, sqlDB, `SELECT COUNT(*) FROM narrators WHERE id = 'nar_test'`, 0)
	if _, err := os.Stat(uploadDir); !os.IsNotExist(err) {
		t.Fatalf("unshared upload dir should be removed after commit, stat err=%v", err)
	}
}

func TestDeleteNarratorRejectsNonArchivedBeforeMutation(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)

	w := serveNarratorHandlerRequest(sqlDB, http.MethodDelete, "/narrators/"+narratorID)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected delete 400 for non-archived narrator, got %d body=%s", w.Code, w.Body.String())
	}
	assertRowCount(t, sqlDB, `SELECT COUNT(*) FROM narrators WHERE id = 'nar_test'`, 1)
}

func TestRemoveNarratorDeletesArchivedChildTree(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	insertDeleteNarrator(t, sqlDB, "nar_parent", "", "archived")
	insertDeleteNarrator(t, sqlDB, "nar_child", "nar_parent", "archived")
	insertDeleteNarrator(t, sqlDB, "nar_grandchild", "nar_child", "archived")
	insertDeleteMessage(t, sqlDB, "msg-child", "nar_child", `[{"type":"text","text":"child"}]`)
	insertDeleteRef(t, sqlDB, "ref-child", "nar_child", "msg-child", 1)

	if err := (&Service{DB: sqlDB}).RemoveNarrator(context.Background(), "nar_parent"); err != nil {
		t.Fatalf("remove narrator: %v", err)
	}
	assertRowCount(t, sqlDB, `SELECT COUNT(*) FROM narrators WHERE id IN ('nar_parent', 'nar_child', 'nar_grandchild')`, 0)
	assertRowCount(t, sqlDB, `SELECT COUNT(*) FROM narrator_messages WHERE id = 'msg-child'`, 0)
	assertRowCount(t, sqlDB, `SELECT COUNT(*) FROM narrator_message_refs WHERE message_id = 'msg-child'`, 0)
}

func TestRemoveNarratorBlocksNonArchivedChildTree(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	insertDeleteNarrator(t, sqlDB, "nar_parent", "", "archived")
	insertDeleteNarrator(t, sqlDB, "nar_child", "nar_parent", "idle")

	err := (&Service{DB: sqlDB}).RemoveNarrator(context.Background(), "nar_parent")
	if err == nil {
		t.Fatalf("expected non-archived child to block deletion")
	}
	assertRowCount(t, sqlDB, `SELECT COUNT(*) FROM narrators WHERE id IN ('nar_parent', 'nar_child')`, 2)
}

func TestRemoveNarratorHandoffsSharedMessageAndAnnotatesImageOwner(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	insertDeleteNarrator(t, sqlDB, "nar_old", "", "archived")
	insertDeleteNarrator(t, sqlDB, "nar_survivor", "", "idle")
	insertDeleteMessage(t, sqlDB, "msg-shared", "nar_old", `[{"type":"image","imageId":"img1","filename":"a.png","mediaType":"image/png"},{"type":"text","text":"shared"}]`)
	insertDeleteRef(t, sqlDB, "ref-old", "nar_old", "msg-shared", 1)
	insertDeleteRef(t, sqlDB, "ref-survivor", "nar_survivor", "msg-shared", 2)
	uploadDir := filepath.Join(home, ".narrafork", "uploads", "nar_old")
	if err := os.MkdirAll(uploadDir, 0o755); err != nil {
		t.Fatalf("create upload dir: %v", err)
	}

	if err := (&Service{DB: sqlDB}).RemoveNarrator(context.Background(), "nar_old"); err != nil {
		t.Fatalf("remove narrator: %v", err)
	}
	var ownerID, raw string
	if err := sqlDB.QueryRow(`SELECT narrator_id, content_json FROM narrator_messages WHERE id = 'msg-shared'`).Scan(&ownerID, &raw); err != nil {
		t.Fatalf("load shared message: %v", err)
	}
	if ownerID != "nar_survivor" {
		t.Fatalf("shared message owner = %q, want survivor", ownerID)
	}
	var blocks []map[string]any
	if err := json.Unmarshal([]byte(raw), &blocks); err != nil {
		t.Fatalf("decode content json: %v", err)
	}
	if blocks[0]["uploadNarratorId"] != "nar_old" {
		t.Fatalf("shared image should be annotated with old upload owner: %#v", blocks)
	}
	assertRowCount(t, sqlDB, `SELECT COUNT(*) FROM narrator_message_refs WHERE message_id = 'msg-shared' AND narrator_id = 'nar_old'`, 0)
	assertRowCount(t, sqlDB, `SELECT COUNT(*) FROM narrator_message_refs WHERE message_id = 'msg-shared' AND narrator_id = 'nar_survivor'`, 1)
	if _, err := os.Stat(uploadDir); err != nil {
		t.Fatalf("shared image upload dir should be retained, stat err=%v", err)
	}
}

func TestRemoveNarratorCleansOrphanMessagesAndOptionalReferences(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	ensureDeleteOptionalTables(t, sqlDB)
	insertDeleteNarrator(t, sqlDB, "nar_delete", "", "archived")
	insertDeleteNarrator(t, sqlDB, "nar_other", "", "idle")
	insertDeleteMessage(t, sqlDB, "msg-orphan", "nar_delete", `[{"type":"text","text":"bye"}]`)
	insertDeleteRef(t, sqlDB, "ref-orphan", "nar_delete", "msg-orphan", 1)
	if _, err := sqlDB.Exec(`INSERT INTO narrator_tool_calls (id, narrator_id, message_id, tool_use_id, tool_name, status, created_at) VALUES ('tc-orphan', 'nar_delete', 'msg-orphan', 'tool-1', 'Read', 'success', 'now')`); err != nil {
		t.Fatalf("insert tool call: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO narrator_patches (id, narrator_id, message_id, tool_use_id) VALUES ('patch-1', 'nar_delete', 'msg-orphan', 'tool-1')`); err != nil {
		t.Fatalf("insert patch: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO narrator_sidecars (id, narrator_id, message_id, tool_use_id) VALUES ('side-1', 'nar_delete', 'msg-orphan', 'tool-1')`); err != nil {
		t.Fatalf("insert sidecar: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO chapter_commits (id, narrator_message_id) VALUES ('commit-1', 'msg-orphan')`); err != nil {
		t.Fatalf("insert chapter commit: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO api_requests (id, narrator_id, message_id, created_at) VALUES ('api-1', 'nar_delete', 'msg-orphan', 'now')`); err != nil {
		t.Fatalf("insert api request: %v", err)
	}
	if _, err := sqlDB.Exec(`UPDATE narrators SET fork_message_id = 'msg-orphan', prune_boundary_message_id = 'msg-orphan' WHERE id = 'nar_other'`); err != nil {
		t.Fatalf("point other narrator at orphan: %v", err)
	}

	if err := (&Service{DB: sqlDB}).RemoveNarrator(context.Background(), "nar_delete"); err != nil {
		t.Fatalf("remove narrator: %v", err)
	}
	for _, query := range []string{
		`SELECT COUNT(*) FROM narrator_messages WHERE id = 'msg-orphan'`,
		`SELECT COUNT(*) FROM narrator_tool_calls WHERE id = 'tc-orphan'`,
		`SELECT COUNT(*) FROM narrator_patches WHERE id = 'patch-1'`,
		`SELECT COUNT(*) FROM narrator_sidecars WHERE id = 'side-1'`,
		`SELECT COUNT(*) FROM narrator_message_refs WHERE message_id = 'msg-orphan'`,
	} {
		assertRowCount(t, sqlDB, query, 0)
	}
	assertRowCount(t, sqlDB, `SELECT COUNT(*) FROM api_requests WHERE id = 'api-1' AND message_id IS NULL`, 1)
	assertRowCount(t, sqlDB, `SELECT COUNT(*) FROM chapter_commits WHERE id = 'commit-1' AND narrator_message_id IS NULL`, 1)
	assertRowCount(t, sqlDB, `SELECT COUNT(*) FROM narrators WHERE id = 'nar_other' AND fork_message_id IS NULL AND prune_boundary_message_id IS NULL`, 1)
}

func TestRemoveNarratorCleansTerminalAndBackgroundRows(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	ensureDeleteTerminalTables(t, sqlDB)
	insertDeleteNarrator(t, sqlDB, "nar_delete", "", "archived")
	insertDeleteNarrator(t, sqlDB, "nar_subagent", "nar_delete", "archived")
	if _, err := sqlDB.Exec(`INSERT INTO background_tasks (id, parent_narrator_id, type, status, subagent_narrator_id, started_at, created_at, updated_at) VALUES ('bg-parent', 'nar_delete', 'bash', 'completed', NULL, 'now', 'now', 'now'), ('bg-child', 'nar_other', 'agent', 'completed', 'nar_subagent', 'now', 'now', 'now')`); err != nil {
		t.Fatalf("insert background tasks: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO terminals (id, narrator_id, status) VALUES ('term-1', 'nar_delete', 'exited'), ('term-child', 'nar_subagent', 'exited')`); err != nil {
		t.Fatalf("insert terminals: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO terminal_tabs (id, narrator_id) VALUES ('tab-1', 'nar_delete'), ('tab-child', 'nar_subagent')`); err != nil {
		t.Fatalf("insert terminal tabs: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO terminal_view_state (id, narrator_id) VALUES ('view-1', 'nar_delete'), ('view-child', 'nar_subagent')`); err != nil {
		t.Fatalf("insert terminal views: %v", err)
	}

	if err := (&Service{DB: sqlDB}).RemoveNarrator(context.Background(), "nar_delete"); err != nil {
		t.Fatalf("remove narrator: %v", err)
	}
	assertRowCount(t, sqlDB, `SELECT COUNT(*) FROM background_tasks`, 0)
	assertRowCount(t, sqlDB, `SELECT COUNT(*) FROM terminals`, 0)
	assertRowCount(t, sqlDB, `SELECT COUNT(*) FROM terminal_tabs`, 0)
	assertRowCount(t, sqlDB, `SELECT COUNT(*) FROM terminal_view_state`, 0)
}

func TestRemoveNarratorBlocksRunningOwnedRows(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	ensureDeleteTerminalTables(t, sqlDB)
	insertDeleteNarrator(t, sqlDB, "nar_delete", "", "archived")
	if _, err := sqlDB.Exec(`INSERT INTO background_tasks (id, parent_narrator_id, type, status, started_at, created_at, updated_at) VALUES ('bg-running', 'nar_delete', 'bash', 'running', 'now', 'now', 'now')`); err != nil {
		t.Fatalf("insert background task: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO terminals (id, narrator_id, status) VALUES ('term-running', 'nar_delete', 'running')`); err != nil {
		t.Fatalf("insert terminal: %v", err)
	}

	err := (&Service{DB: sqlDB}).RemoveNarrator(context.Background(), "nar_delete")
	if err == nil {
		t.Fatalf("expected running owned rows to block deletion")
	}
	assertRowCount(t, sqlDB, `SELECT COUNT(*) FROM narrators WHERE id = 'nar_delete'`, 1)
	assertRowCount(t, sqlDB, `SELECT COUNT(*) FROM background_tasks WHERE id = 'bg-running'`, 1)
	assertRowCount(t, sqlDB, `SELECT COUNT(*) FROM terminals WHERE id = 'term-running'`, 1)
}

func insertDeleteNarrator(t *testing.T, sqlDB *sql.DB, narratorID, parentID, status string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var parent any
	if parentID != "" {
		parent = parentID
	}
	_, err := sqlDB.Exec(`INSERT INTO narrators (
		id, parent_narrator_id, type, title, inherit_mode, model, permission_mode, status, substatus,
		plan_mode, cwd, variant, traits, is_background, is_ask_in_passing, message_version,
		message_count, total_cost_usd, fast_mode, relaxed_plan, plan_reflection_auto_approve_override,
		danger_reflection_override, created_at, updated_at
	) VALUES (?, ?, 'primary', ?, 'fresh', 'openai:test', 'default', ?, '[]', 0, '', 'primary', '[]', 0, 0, 0, 0, 0, 0, 0, 'inherit', 'inherit', ?, ?)`, narratorID, parent, narratorID, status, now, now)
	if err != nil {
		t.Fatalf("insert narrator %s: %v", narratorID, err)
	}
}

func insertDeleteMessage(t *testing.T, sqlDB *sql.DB, messageID, narratorID, contentJSON string) {
	t.Helper()
	if _, err := sqlDB.Exec(`INSERT INTO narrator_messages (id, narrator_id, role, content_json, content_text, created_at) VALUES (?, ?, 'user', ?, '', ?)`, messageID, narratorID, contentJSON, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert message %s: %v", messageID, err)
	}
}

func insertDeleteRef(t *testing.T, sqlDB *sql.DB, refID, narratorID, messageID string, seq int) {
	t.Helper()
	if _, err := sqlDB.Exec(`INSERT INTO narrator_message_refs (id, narrator_id, message_id, seq) VALUES (?, ?, ?, ?)`, refID, narratorID, messageID, seq); err != nil {
		t.Fatalf("insert ref %s: %v", refID, err)
	}
}

func ensureDeleteOptionalTables(t *testing.T, sqlDB *sql.DB) {
	t.Helper()
	stmts := []string{
		`CREATE TABLE narrator_patches (id TEXT PRIMARY KEY, narrator_id TEXT, message_id TEXT, tool_use_id TEXT)`,
		`CREATE TABLE narrator_sidecars (id TEXT PRIMARY KEY, narrator_id TEXT, message_id TEXT, tool_use_id TEXT)`,
		`CREATE TABLE chapter_commits (id TEXT PRIMARY KEY, narrator_message_id TEXT)`,
	}
	for _, stmt := range stmts {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("create delete optional table: %v", err)
		}
	}
}

func ensureDeleteTerminalTables(t *testing.T, sqlDB *sql.DB) {
	t.Helper()
	stmts := []string{
		`CREATE TABLE terminals (id TEXT PRIMARY KEY, narrator_id TEXT, status TEXT)`,
		`CREATE TABLE terminal_tabs (id TEXT PRIMARY KEY, narrator_id TEXT)`,
		`CREATE TABLE terminal_view_state (id TEXT PRIMARY KEY, narrator_id TEXT)`,
	}
	for _, stmt := range stmts {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("create delete terminal table: %v", err)
		}
	}
}

func assertRowCount(t *testing.T, sqlDB *sql.DB, query string, want int) {
	t.Helper()
	var got int
	if err := sqlDB.QueryRow(query).Scan(&got); err != nil {
		t.Fatalf("count query %q: %v", query, err)
	}
	if got != want {
		t.Fatalf("count query %q = %d, want %d", query, got, want)
	}
}
