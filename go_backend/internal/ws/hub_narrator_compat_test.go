package ws_test

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	_ "modernc.org/sqlite"

	"narrafork-go/internal/ws"
)

func TestNarratorCompatibilityMessagesDoNotReturnUnsupported(t *testing.T) {
	hub := ws.NewHub()
	conn, cleanup := dialNarratorHub(t, hub)
	defer cleanup()

	messages := []map[string]any{
		{"type": "subscribe_stats"},
		{"type": "unsubscribe_stats"},
		{"type": "update_timeout", "narratorId": "nar_compat", "toolUseId": "tool_1", "timeoutMs": 120000},
		{"type": "buffer_message", "narratorId": "nar_compat", "text": "queued"},
		{"type": "update_buffer", "narratorId": "nar_compat", "messageId": "missing", "text": "updated"},
		{"type": "remove_buffer", "narratorId": "nar_compat", "messageId": "missing"},
		{"type": "cancel_buffer", "narratorId": "nar_compat"},
	}
	for _, msg := range messages {
		t.Run(msg["type"].(string), func(t *testing.T) {
			if err := conn.WriteJSON(msg); err != nil {
				t.Fatalf("write message: %v", err)
			}
			assertNoUnsupportedMessage(t, conn)
		})
	}
}

func TestNarratorBufferAndTimeoutMessagesBroadcastCompatibilityEvents(t *testing.T) {
	hub := ws.NewHub()
	sqlDB := openNarratorCompatDB(t)
	defer sqlDB.Close()
	hub.SetTerminalDB(sqlDB)
	conn, cleanup := dialNarratorHub(t, hub)

	defer cleanup()

	if err := conn.WriteJSON(map[string]any{"type": "subscribe", "narratorIds": []string{"nar_compat"}}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	waitForNarratorSubscription(t, hub, conn, "nar_compat")

	if err := conn.WriteJSON(map[string]any{"type": "buffer_message", "narratorId": "nar_compat", "text": "first"}); err != nil {
		t.Fatalf("buffer message: %v", err)
	}
	bufferSet := readWSMessage(t, conn)
	if bufferSet["type"] != "buffer_set" || bufferSet["narratorId"] != "nar_compat" {
		t.Fatalf("expected buffer_set, got %#v", bufferSet)
	}
	messages, _ := bufferSet["messages"].([]any)
	if len(messages) != 1 || messages[0].(map[string]any)["text"] != "first" {
		t.Fatalf("unexpected buffer messages: %#v", bufferSet["messages"])
	}
	messageID, _ := messages[0].(map[string]any)["id"].(string)
	if strings.TrimSpace(messageID) == "" {
		t.Fatalf("buffered message should include id: %#v", messages[0])
	}

	if err := conn.WriteJSON(map[string]any{"type": "update_buffer", "narratorId": "nar_compat", "messageId": messageID, "text": "updated"}); err != nil {
		t.Fatalf("update buffer: %v", err)
	}
	updated := readWSMessage(t, conn)
	updatedMessages, _ := updated["messages"].([]any)
	if updated["type"] != "buffer_set" || len(updatedMessages) != 1 || updatedMessages[0].(map[string]any)["text"] != "updated" {
		t.Fatalf("expected updated buffer_set, got %#v", updated)
	}

	if err := conn.WriteJSON(map[string]any{"type": "remove_buffer", "narratorId": "nar_compat", "messageId": messageID}); err != nil {
		t.Fatalf("remove buffer: %v", err)
	}
	removed := readWSMessage(t, conn)
	removedMessages, _ := removed["messages"].([]any)
	if removed["type"] != "buffer_set" || len(removedMessages) != 0 {
		t.Fatalf("expected empty buffer_set after remove, got %#v", removed)
	}

	if err := conn.WriteJSON(map[string]any{"type": "cancel_buffer", "narratorId": "nar_compat"}); err != nil {
		t.Fatalf("cancel buffer: %v", err)
	}
	cleared := readWSMessage(t, conn)
	if cleared["type"] != "buffer_cleared" || cleared["reason"] != "cancelled" {
		t.Fatalf("expected buffer_cleared cancelled, got %#v", cleared)
	}

	if err := conn.WriteJSON(map[string]any{"type": "update_timeout", "narratorId": "nar_compat", "toolUseId": "tool_1", "timeoutMs": 45000}); err != nil {
		t.Fatalf("update timeout: %v", err)
	}
	timeoutUpdated := readWSMessage(t, conn)
	if timeoutUpdated["type"] != "timeout_updated" || timeoutUpdated["toolUseId"] != "tool_1" || timeoutUpdated["timeoutMs"] != float64(45000) {
		t.Fatalf("expected timeout_updated, got %#v", timeoutUpdated)
	}
}

func TestNarratorBufferMessageReturnsSequenceQueryErrorWithoutInsert(t *testing.T) {
	hub := ws.NewHub()
	sqlDB := openNarratorCompatDB(t)
	defer sqlDB.Close()
	if _, err := sqlDB.Exec(`INSERT INTO narrator_buffered_messages (id, narrator_id, text, created_by, priority, seq, buffered_at) VALUES ('existing', 'nar_compat', 'already queued', 'user-1', 0, 0, '2026-05-15T00:00:00Z')`); err != nil {
		t.Fatalf("insert existing buffer: %v", err)
	}
	stmts := []string{
		`ALTER TABLE narrator_buffered_messages RENAME TO narrator_buffered_messages_real`,
		`CREATE VIEW narrator_buffered_messages AS SELECT id, narrator_id, text, images_json, command_text, created_by, creator_json, text_file_paths_json, priority, json_extract('not-json', '$') AS seq, buffered_at FROM narrator_buffered_messages_real`,
		`CREATE TRIGGER narrator_buffered_messages_insert INSTEAD OF INSERT ON narrator_buffered_messages BEGIN INSERT INTO narrator_buffered_messages_real (id, narrator_id, text, images_json, command_text, created_by, creator_json, text_file_paths_json, priority, seq, buffered_at) VALUES (NEW.id, NEW.narrator_id, NEW.text, NEW.images_json, NEW.command_text, NEW.created_by, NEW.creator_json, NEW.text_file_paths_json, NEW.priority, NEW.seq, NEW.buffered_at); END;`,
	}
	for _, stmt := range stmts {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("prepare broken buffer view: %v", err)
		}
	}
	hub.SetTerminalDB(sqlDB)
	conn, cleanup := dialNarratorHub(t, hub)
	defer cleanup()

	if err := conn.WriteJSON(map[string]any{"type": "buffer_message", "narratorId": "nar_compat", "text": "new queued"}); err != nil {
		t.Fatalf("buffer message: %v", err)
	}
	msg := readWSMessage(t, conn)
	message, _ := msg["message"].(string)
	if msg["type"] != "error" || !strings.Contains(message, "query next buffered message sequence") {
		t.Fatalf("expected sequence query error, got %#v", msg)
	}
	var count int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM narrator_buffered_messages_real`).Scan(&count); err != nil {
		t.Fatalf("count backing buffer rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("sequence query failure should not insert websocket buffer row, count=%d", count)
	}
}

func TestPermissionDecisionReturnsTargetQueryError(t *testing.T) {
	hub := ws.NewHub()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sqlDB.Close()
	_, err = sqlDB.Exec(`CREATE TABLE narrator_tool_calls (
		id TEXT PRIMARY KEY,
		tool_use_id TEXT,
		input_json TEXT,
		status TEXT,
		permission_decided_by TEXT,
		permission_decided_at TEXT,
		permission_decision_reason TEXT,
		permission_deny_message TEXT,
		created_at TEXT
	);
	INSERT INTO narrator_tool_calls (id, tool_use_id, input_json, status, created_at) VALUES ('req-1', 'tool-1', '{}', 'permission', 'now');`)
	if err != nil {
		t.Fatalf("prepare permission decision fixture: %v", err)
	}
	hub.SetTerminalDB(sqlDB)
	conn, cleanup := dialNarratorHub(t, hub)
	defer cleanup()

	if err := conn.WriteJSON(map[string]any{"type": "permission_decision", "requestId": "req-1", "decision": "allow"}); err != nil {
		t.Fatalf("write permission decision: %v", err)
	}
	msg := readWSMessage(t, conn)
	message, _ := msg["message"].(string)
	if msg["type"] != "error" || !strings.Contains(message, "query permission target") {
		t.Fatalf("expected permission target query error, got %#v", msg)
	}
}

func openNarratorCompatDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	stmts := []string{
		`CREATE TABLE narrator_buffered_messages (
			id TEXT PRIMARY KEY,
			narrator_id TEXT NOT NULL,
			text TEXT NOT NULL,
			images_json TEXT,
			command_text TEXT,
			created_by TEXT,
			creator_json TEXT,
			text_file_paths_json TEXT,
			priority INTEGER NOT NULL DEFAULT 0,
			seq INTEGER NOT NULL,
			buffered_at TEXT NOT NULL
		)`,
		`CREATE TABLE narrator_tool_calls (
			id TEXT PRIMARY KEY,
			narrator_id TEXT,
			tool_use_id TEXT,
			input_json TEXT,
			created_at TEXT
		)`,
		`INSERT INTO narrator_tool_calls (id, narrator_id, tool_use_id, input_json, created_at) VALUES ('call_1', 'nar_compat', 'tool_1', '{}', '2026-05-15T00:00:00Z')`,
	}
	for _, stmt := range stmts {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("exec fixture stmt: %v", err)
		}
	}
	return sqlDB
}

func assertNoUnsupportedMessage(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(75 * time.Millisecond))
	var msg map[string]any
	if err := conn.ReadJSON(&msg); err != nil {
		return
	}
	if msg["type"] == "error" && strings.Contains(strings.ToLower(stringValue(msg["message"])), "unsupported") {
		t.Fatalf("received unsupported error: %#v", msg)
	}
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}
