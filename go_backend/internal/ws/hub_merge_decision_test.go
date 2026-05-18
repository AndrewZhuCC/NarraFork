package ws_test

import (
	"database/sql"
	"testing"

	"narrafork-go/internal/ws"

	_ "modernc.org/sqlite"
)

func TestHubBroadcastMergeProgressSendsToNarratorSockets(t *testing.T) {
	hub := ws.NewHub()
	conn, cleanup := dialNarratorHub(t, hub)
	defer cleanup()

	hub.BroadcastMergeProgress(map[string]any{"type": "merge:step_ok", "mergeSessionId": "merge_1", "mergedCount": 1})
	msg := readWSMessage(t, conn)
	if msg["type"] != "merge:step_ok" || msg["mergeSessionId"] != "merge_1" {
		t.Fatalf("unexpected merge progress payload: %#v", msg)
	}
}

func TestHubMergeDecisionUpdatesMergeSession(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sqlDB.Close()
	sqlDB.SetMaxOpenConns(1)
	_, err = sqlDB.Exec(`CREATE TABLE merge_sessions (
		id TEXT PRIMARY KEY, target_chapter_id TEXT, source_chapter_ids TEXT, strategy TEXT,
		status TEXT, current_index INTEGER, merged_count INTEGER, current_source_chapter_id TEXT,
		conflict_files TEXT, error TEXT, locale TEXT, created_at TEXT, updated_at TEXT
	);
	INSERT INTO merge_sessions (id, target_chapter_id, source_chapter_ids, strategy, status, current_index, merged_count, created_at, updated_at)
	VALUES ('merge_1', 'target', '["source"]', 'merge', 'waiting_decision', 0, 0, 'now', 'now');`)
	if err != nil {
		t.Fatalf("create merge session: %v", err)
	}

	hub := ws.NewHub()
	hub.SetMergeDB(sqlDB)
	conn, cleanup := dialNarratorHub(t, hub)
	defer cleanup()

	if err := conn.WriteJSON(map[string]any{"type": "merge_decision", "mergeSessionId": "merge_1", "decision": "continue"}); err != nil {
		t.Fatalf("write merge_decision: %v", err)
	}
	msg := readWSMessage(t, conn)
	if msg["type"] != "merge_decision_resolved" || msg["status"] != "running" {
		t.Fatalf("unexpected WS ack: %#v", msg)
	}

	var status string
	if err := sqlDB.QueryRow(`SELECT status FROM merge_sessions WHERE id = 'merge_1'`).Scan(&status); err != nil {
		t.Fatalf("query status: %v", err)
	}
	if status != "running" {
		t.Fatalf("status = %q, want running", status)
	}
}
