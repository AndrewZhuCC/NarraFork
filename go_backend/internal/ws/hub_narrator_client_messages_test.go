package ws_test

import (
	"database/sql"
	"encoding/json"
	"testing"

	_ "modernc.org/sqlite"

	"narrafork-go/internal/ws"
)

func TestNarratorBufferClientMessagesBroadcastBufferState(t *testing.T) {
	sqlDB := openNarratorWSDB(t)
	hub := ws.NewHub()
	hub.SetTerminalDB(sqlDB)

	conn, cleanup := dialNarratorHub(t, hub)
	defer cleanup()
	subscribeNarrator(t, conn, "nar_buf")
	waitForNarratorSubscription(t, hub, conn, "nar_buf")

	if err := conn.WriteJSON(map[string]any{"type": "buffer_message", "narratorId": "nar_buf", "text": "queued", "priority": true, "commandText": "run"}); err != nil {
		t.Fatalf("write buffer_message: %v", err)
	}
	msg := readWSMessage(t, conn)
	if msg["type"] == "error" && msg["message"] == "Unknown or unsupported WebSocket message type" {
		t.Fatalf("buffer_message returned unsupported: %#v", msg)
	}
	if msg["type"] != "buffer_set" || msg["narratorId"] != "nar_buf" {
		t.Fatalf("expected buffer_set, got %#v", msg)
	}
	messages := wsMessageItems(t, msg, "messages")
	if len(messages) != 1 || messages[0]["text"] != "queued" || messages[0]["priority"] != true || messages[0]["commandText"] != "run" {
		t.Fatalf("unexpected buffer messages: %#v", messages)
	}
	messageID, _ := messages[0]["id"].(string)
	if messageID == "" {
		t.Fatalf("expected message id in %#v", messages[0])
	}

	if err := conn.WriteJSON(map[string]any{"type": "update_buffer", "narratorId": "nar_buf", "messageId": messageID, "text": "edited", "priority": false}); err != nil {
		t.Fatalf("write update_buffer: %v", err)
	}
	msg = readWSMessage(t, conn)
	if msg["type"] != "buffer_set" {
		t.Fatalf("expected buffer_set after update, got %#v", msg)
	}
	messages = wsMessageItems(t, msg, "messages")
	if len(messages) != 1 || messages[0]["text"] != "edited" || messages[0]["priority"] != false {
		t.Fatalf("unexpected updated buffer: %#v", messages)
	}

	if err := conn.WriteJSON(map[string]any{"type": "remove_buffer", "narratorId": "nar_buf", "messageId": messageID}); err != nil {
		t.Fatalf("write remove_buffer: %v", err)
	}
	msg = readWSMessage(t, conn)
	if msg["type"] != "buffer_set" || len(wsMessageItems(t, msg, "messages")) != 0 {
		t.Fatalf("expected empty buffer_set after remove, got %#v", msg)
	}

	if err := conn.WriteJSON(map[string]any{"type": "buffer_message", "narratorId": "nar_buf", "text": "queued again"}); err != nil {
		t.Fatalf("write second buffer_message: %v", err)
	}
	if msg = readWSMessage(t, conn); msg["type"] != "buffer_set" {
		t.Fatalf("expected buffer_set for second insert, got %#v", msg)
	}
	if err := conn.WriteJSON(map[string]any{"type": "cancel_buffer", "narratorId": "nar_buf"}); err != nil {
		t.Fatalf("write cancel_buffer: %v", err)
	}
	msg = readWSMessage(t, conn)
	if msg["type"] != "buffer_cleared" || msg["narratorId"] != "nar_buf" || msg["reason"] != "cancelled" {
		t.Fatalf("expected buffer_cleared cancelled, got %#v", msg)
	}
}

func TestNarratorWSMessageBoundsRejectOutOfRangeValues(t *testing.T) {
	sqlDB := openNarratorWSDB(t)
	hub := ws.NewHub()
	hub.SetTerminalDB(sqlDB)

	conn, cleanup := dialNarratorHub(t, hub)
	defer cleanup()
	subscribeNarrator(t, conn, "nar_bounds")
	waitForNarratorSubscription(t, hub, conn, "nar_bounds")

	overlong := make([]byte, 100_001)
	for i := range overlong {
		overlong[i] = 'x'
	}

	// buffer_message text max 100000.
	if err := conn.WriteJSON(map[string]any{"type": "buffer_message", "narratorId": "nar_bounds", "text": string(overlong)}); err != nil {
		t.Fatalf("write overlong buffer_message: %v", err)
	}
	msg := readWSMessage(t, conn)
	if msg["type"] != "error" || msg["message"] != "text must be at most 100000 characters" {
		t.Fatalf("expected buffer_message length error, got %#v", msg)
	}

	// update_timeout timeoutMs below the 1000 lower bound.
	if err := conn.WriteJSON(map[string]any{"type": "update_timeout", "toolUseId": "tool_1", "timeoutMs": 500}); err != nil {
		t.Fatalf("write small update_timeout: %v", err)
	}
	msg = readWSMessage(t, conn)
	if msg["type"] != "error" || msg["message"] != "timeoutMs must be between 1000 and 86400000" {
		t.Fatalf("expected update_timeout lower-bound error, got %#v", msg)
	}

	// update_timeout timeoutMs above the 86400000 upper bound.
	if err := conn.WriteJSON(map[string]any{"type": "update_timeout", "toolUseId": "tool_1", "timeoutMs": 90_000_000}); err != nil {
		t.Fatalf("write large update_timeout: %v", err)
	}
	msg = readWSMessage(t, conn)
	if msg["type"] != "error" || msg["message"] != "timeoutMs must be between 1000 and 86400000" {
		t.Fatalf("expected update_timeout upper-bound error, got %#v", msg)
	}
}

func TestNarratorBufferLoadToolSlashUpdatesEnabledTools(t *testing.T) {
	sqlDB := openNarratorWSDB(t)
	ensureWSOptionalToolSchema(t, sqlDB)
	insertWSOptionalToolNarrator(t, sqlDB, "nar_load", "[]")
	hub := ws.NewHub()
	hub.SetTerminalDB(sqlDB)

	conn, cleanup := dialNarratorHub(t, hub)
	defer cleanup()
	subscribeNarrator(t, conn, "nar_load")
	waitForNarratorSubscription(t, hub, conn, "nar_load")

	if err := conn.WriteJSON(map[string]any{"type": "buffer_message", "narratorId": "nar_load", "text": "/load terminal"}); err != nil {
		t.Fatalf("write load buffer_message: %v", err)
	}
	msg := readWSMessage(t, conn)
	if msg["type"] != "message" {
		t.Fatalf("expected display message, got %#v", msg)
	}
	msg = readWSMessage(t, conn)
	if msg["type"] != "user_message" {
		t.Fatalf("expected tool_loaded user_message, got %#v", msg)
	}
	assertWSNarratorEnabledTools(t, sqlDB, "nar_load", `["Terminal"]`)
	assertWSBufferCount(t, sqlDB, "nar_load", 0)
}

func TestNarratorBufferUnloadToolSlashUpdatesEnabledTools(t *testing.T) {
	sqlDB := openNarratorWSDB(t)
	ensureWSOptionalToolSchema(t, sqlDB)
	insertWSOptionalToolNarrator(t, sqlDB, "nar_unload", `["Terminal","Browser"]`)
	hub := ws.NewHub()
	hub.SetTerminalDB(sqlDB)

	conn, cleanup := dialNarratorHub(t, hub)
	defer cleanup()
	subscribeNarrator(t, conn, "nar_unload")
	waitForNarratorSubscription(t, hub, conn, "nar_unload")

	if err := conn.WriteJSON(map[string]any{"type": "buffer_message", "narratorId": "nar_unload", "text": "/unload terminal"}); err != nil {
		t.Fatalf("write unload buffer_message: %v", err)
	}
	msg := readWSMessage(t, conn)
	if msg["type"] != "message" {
		t.Fatalf("expected display message, got %#v", msg)
	}
	msg = readWSMessage(t, conn)
	if msg["type"] != "user_message" {
		t.Fatalf("expected tool_unloaded user_message, got %#v", msg)
	}
	assertWSNarratorEnabledTools(t, sqlDB, "nar_unload", `["Browser"]`)
	assertWSBufferCount(t, sqlDB, "nar_unload", 0)
}

func TestChapterTerminalExitBroadcastsCountToChapterNarrators(t *testing.T) {
	sqlDB := openNarratorWSDB(t)
	_, err := sqlDB.Exec(`
CREATE TABLE narrators (id TEXT PRIMARY KEY, chapter_id TEXT);
CREATE TABLE terminals (id TEXT PRIMARY KEY, chapter_id TEXT, narrator_id TEXT, status TEXT);
INSERT INTO narrators (id, chapter_id) VALUES ('nar_chapter', 'chapter-1'), ('nar_other', 'other');
INSERT INTO terminals (id, chapter_id, narrator_id, status) VALUES
	('term-chapter', 'chapter-1', NULL, 'running'),
	('term-other', 'other', NULL, 'running');`)
	if err != nil {
		t.Fatalf("seed chapter terminal data: %v", err)
	}
	hub := ws.NewHub()
	hub.SetTerminalDB(sqlDB)

	conn, cleanup := dialNarratorHub(t, hub)
	defer cleanup()
	subscribeNarrator(t, conn, "nar_chapter")
	waitForNarratorSubscription(t, hub, conn, "nar_chapter")

	hub.BroadcastTerminal("term-chapter", map[string]any{"type": "exit", "terminalId": "term-chapter", "code": 0})
	msg := readWSMessage(t, conn)
	if msg["type"] != "terminal_count_changed" || msg["narratorId"] != "nar_chapter" || msg["activeTerminalCount"] != float64(0) {
		t.Fatalf("expected chapter terminal count broadcast, got %#v", msg)
	}
}

func TestNarratorSubscribeSendsTerminalCount(t *testing.T) {
	sqlDB := openNarratorWSDB(t)
	_, err := sqlDB.Exec(`
CREATE TABLE terminals (id TEXT PRIMARY KEY, narrator_id TEXT, status TEXT);
INSERT INTO terminals (id, narrator_id, status) VALUES
	('term-running', 'nar_terms', 'running'),
	('term-exited', 'nar_terms', 'exited'),
	('term-other', 'other', 'running');`)
	if err != nil {
		t.Fatalf("seed terminals: %v", err)
	}
	hub := ws.NewHub()
	hub.SetTerminalDB(sqlDB)

	conn, cleanup := dialNarratorHub(t, hub)
	defer cleanup()
	subscribeNarrator(t, conn, "nar_terms")

	msg := readWSMessage(t, conn)
	if msg["type"] != "terminal_count_changed" || msg["narratorId"] != "nar_terms" || msg["activeTerminalCount"] != float64(1) || msg["count"] != float64(1) {
		t.Fatalf("expected terminal_count_changed=1, got %#v", msg)
	}
}

func TestNarratorSubscribeMarksTerminalCountQueryFailureDegraded(t *testing.T) {
	sqlDB := openNarratorWSDB(t)
	_, err := sqlDB.Exec(`CREATE TABLE terminals (id TEXT PRIMARY KEY, narrator_id TEXT);`)
	if err != nil {
		t.Fatalf("create broken terminals view: %v", err)
	}
	hub := ws.NewHub()
	hub.SetTerminalDB(sqlDB)

	conn, cleanup := dialNarratorHub(t, hub)
	defer cleanup()
	subscribeNarrator(t, conn, "nar_terms")

	msg := readWSMessage(t, conn)
	fallbacks, _ := msg["fallbacks"].([]any)
	if msg["type"] != "terminal_count_changed" || msg["narratorId"] != "nar_terms" || msg["degraded"] != true || msg["activeTerminalCount"] != float64(0) || len(fallbacks) != 1 || fallbacks[0] != "terminal_count_query_failed" {
		t.Fatalf("expected degraded terminal_count_changed, got %#v", msg)
	}
}

func TestNarratorSubscribeSendsBrowserSessionCount(t *testing.T) {
	sqlDB := openNarratorWSDB(t)
	_, err := sqlDB.Exec(`
CREATE TABLE narrator_browser_sessions (id TEXT PRIMARY KEY, narrator_id TEXT, status TEXT);
INSERT INTO narrator_browser_sessions (id, narrator_id, status) VALUES
	('browser-active', 'nar_browser', 'active'),
	('browser-running', 'nar_browser', 'running'),
	('browser-closed', 'nar_browser', 'closed'),
	('browser-other', 'other', 'active');`)
	if err != nil {
		t.Fatalf("seed browser sessions: %v", err)
	}
	hub := ws.NewHub()
	hub.SetTerminalDB(sqlDB)

	conn, cleanup := dialNarratorHub(t, hub)
	defer cleanup()
	subscribeNarrator(t, conn, "nar_browser")

	msg := readWSMessage(t, conn)
	if msg["type"] != "browser_session_count" || msg["narratorId"] != "nar_browser" || msg["activeBrowserSessions"] != float64(2) {
		t.Fatalf("expected browser_session_count=2, got %#v", msg)
	}
}

func TestNarratorStatsAndTimeoutClientMessages(t *testing.T) {
	sqlDB := openNarratorWSDB(t)
	_, err := sqlDB.Exec(`INSERT INTO narrator_tool_calls (id, narrator_id, tool_use_id, input_json, created_at) VALUES ('tc_1', 'nar_timeout', 'tool_1', '{"existing":true}', '2026-05-14T00:00:00Z')`)
	if err != nil {
		t.Fatalf("insert tool call: %v", err)
	}
	hub := ws.NewHub()
	hub.SetTerminalDB(sqlDB)

	conn, cleanup := dialNarratorHub(t, hub)
	defer cleanup()
	subscribeNarrator(t, conn, "nar_timeout")
	waitForNarratorSubscription(t, hub, conn, "nar_timeout")

	if err := conn.WriteJSON(map[string]any{"type": "subscribe_stats", "narratorId": "nar_timeout"}); err != nil {
		t.Fatalf("write subscribe_stats: %v", err)
	}
	msg := readWSMessage(t, conn)
	if msg["type"] == "error" && msg["message"] == "Unknown or unsupported WebSocket message type" {
		t.Fatalf("subscribe_stats returned unsupported: %#v", msg)
	}
	if msg["type"] != "output_stats" || msg["charsPerSecond"] != float64(0) || msg["tokensPerSecond"] != float64(0) {
		t.Fatalf("expected output_stats, got %#v", msg)
	}

	if err := conn.WriteJSON(map[string]any{"type": "unsubscribe_stats", "narratorId": "nar_timeout"}); err != nil {
		t.Fatalf("write unsubscribe_stats: %v", err)
	}
	if msg = readWSMessage(t, conn); msg["type"] != "output_stats" {
		t.Fatalf("expected output_stats unsubscribe ack, got %#v", msg)
	}

	if err := conn.WriteJSON(map[string]any{"type": "update_timeout", "toolUseId": "tool_1", "timeout": 60000}); err != nil {
		t.Fatalf("write update_timeout: %v", err)
	}
	msg = readWSMessage(t, conn)
	if msg["type"] == "error" && msg["message"] == "Unknown or unsupported WebSocket message type" {
		t.Fatalf("update_timeout returned unsupported: %#v", msg)
	}
	if msg["type"] != "timeout_updated" || msg["toolUseId"] != "tool_1" || msg["timeout"] != float64(60000) {
		t.Fatalf("expected timeout_updated, got %#v", msg)
	}

	var inputRaw string
	if err := sqlDB.QueryRow(`SELECT input_json FROM narrator_tool_calls WHERE id = 'tc_1'`).Scan(&inputRaw); err != nil {
		t.Fatalf("select input_json: %v", err)
	}
	var input map[string]any
	if err := json.Unmarshal([]byte(inputRaw), &input); err != nil {
		t.Fatalf("decode input_json: %v", err)
	}
	if input["existing"] != true || input["timeout"] != float64(60000) {
		t.Fatalf("timeout update did not preserve input object: %#v", input)
	}
}

func openNarratorWSDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	sqlDB.SetMaxOpenConns(1)
	_, err = sqlDB.Exec(`
CREATE TABLE narrator_buffered_messages (
	id TEXT PRIMARY KEY,
	narrator_id TEXT,
	text TEXT,
	images_json TEXT,
	command_text TEXT,
	created_by TEXT,
	creator_json TEXT,
	text_file_paths_json TEXT,
	priority INTEGER DEFAULT 0,
	seq INTEGER DEFAULT 0,
	buffered_at TEXT
);
CREATE TABLE narrator_tool_calls (
	id TEXT PRIMARY KEY,
	narrator_id TEXT,
	tool_use_id TEXT,
	input_json TEXT,
	created_at TEXT
);`)
	if err != nil {
		t.Fatalf("create tables: %v", err)
	}
	return sqlDB
}

func ensureWSOptionalToolSchema(t *testing.T, sqlDB *sql.DB) {
	t.Helper()
	_, err := sqlDB.Exec(`
CREATE TABLE narrators (
	id TEXT PRIMARY KEY,
	enabled_tools TEXT,
	traits TEXT DEFAULT '[]',
	message_count INTEGER DEFAULT 0,
	message_version INTEGER DEFAULT 0,
	last_message_at TEXT,
	updated_at TEXT
);
CREATE TABLE narrator_messages (
	id TEXT PRIMARY KEY,
	narrator_id TEXT,
	role TEXT,
	content_json TEXT,
	content_text TEXT,
	created_at TEXT
);
CREATE TABLE narrator_message_refs (
	id TEXT PRIMARY KEY,
	narrator_id TEXT,
	message_id TEXT,
	seq INTEGER,
	is_compact INTEGER DEFAULT 0
);`)
	if err != nil {
		t.Fatalf("create optional tool ws schema: %v", err)
	}
}

func insertWSOptionalToolNarrator(t *testing.T, sqlDB *sql.DB, narratorID, enabledTools string) {
	t.Helper()
	if _, err := sqlDB.Exec(`INSERT INTO narrators (id, enabled_tools, traits, message_count, message_version, updated_at) VALUES (?, ?, '[]', 0, 0, 'now')`, narratorID, enabledTools); err != nil {
		t.Fatalf("insert optional tool narrator: %v", err)
	}
}

func assertWSNarratorEnabledTools(t *testing.T, sqlDB *sql.DB, narratorID, want string) {
	t.Helper()
	var got string
	if err := sqlDB.QueryRow(`SELECT enabled_tools FROM narrators WHERE id = ?`, narratorID).Scan(&got); err != nil {
		t.Fatalf("query enabled tools: %v", err)
	}
	if got != want {
		t.Fatalf("enabled_tools = %q, want %q", got, want)
	}
}

func assertWSBufferCount(t *testing.T, sqlDB *sql.DB, narratorID string, want int) {
	t.Helper()
	var got int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM narrator_buffered_messages WHERE narrator_id = ?`, narratorID).Scan(&got); err != nil {
		t.Fatalf("query buffer count: %v", err)
	}
	if got != want {
		t.Fatalf("buffer count = %d, want %d", got, want)
	}
}

func wsMessageItems(t *testing.T, msg map[string]any, key string) []map[string]any {
	t.Helper()
	raw, _ := msg[key].([]any)
	items := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("unexpected item in %s: %#v", key, item)
		}
		items = append(items, m)
	}
	return items
}
