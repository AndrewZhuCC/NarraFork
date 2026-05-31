package ws_test

import (
	"database/sql"
	"testing"
	"time"

	"narrafork-go/internal/ws"

	_ "modernc.org/sqlite"
)

func TestHubBroadcastMergeProgressSendsToNarratorSockets(t *testing.T) {
	hub := ws.NewHub()
	conn, cleanup := dialNarratorHub(t, hub)
	defer cleanup()
	waitForNarratorSubscription(t, hub, conn, "")

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

func TestHubBroadcastMergeProgressFiltersBySubscribedProject(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sqlDB.Close()
	sqlDB.SetMaxOpenConns(1)
	_, err = sqlDB.Exec(`CREATE TABLE chapters (id TEXT PRIMARY KEY, project_id TEXT);
	CREATE TABLE narrators (id TEXT PRIMARY KEY, chapter_id TEXT);
	INSERT INTO chapters (id, project_id) VALUES ('chapter_a', 'project_a'), ('chapter_b', 'project_b');
	INSERT INTO narrators (id, chapter_id) VALUES ('nar_a', 'chapter_a'), ('nar_b', 'chapter_b');`)
	if err != nil {
		t.Fatalf("create project graph: %v", err)
	}

	hub := ws.NewHub()
	hub.SetMergeDB(sqlDB)
	connA, cleanupA := dialNarratorHub(t, hub)
	defer cleanupA()
	connB, cleanupB := dialNarratorHub(t, hub)
	defer cleanupB()
	subscribeNarrator(t, connA, "nar_a")
	subscribeNarrator(t, connB, "nar_b")
	waitForNarratorSubscription(t, hub, connA, "nar_a")
	waitForNarratorSubscription(t, hub, connB, "nar_b")

	hub.BroadcastMergeProgress(map[string]any{"type": "merge:conflict", "mergeSessionId": "merge_project_a", "projectId": "project_a"})
	msg := readWSMessage(t, connA)
	if msg["type"] != "merge:conflict" || msg["mergeSessionId"] != "merge_project_a" {
		t.Fatalf("unexpected project-scoped merge payload: %#v", msg)
	}

	_ = connB.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	var unexpected map[string]any
	if err := connB.ReadJSON(&unexpected); err == nil {
		t.Fatalf("subscriber for another project should not receive merge event: %#v", unexpected)
	}
}

func TestHubMergeDecisionRejectsUnsubscribedProject(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sqlDB.Close()
	sqlDB.SetMaxOpenConns(1)
	_, err = sqlDB.Exec(`CREATE TABLE chapters (id TEXT PRIMARY KEY, project_id TEXT);
	CREATE TABLE narrators (id TEXT PRIMARY KEY, chapter_id TEXT);
	CREATE TABLE merge_sessions (
		id TEXT PRIMARY KEY, target_chapter_id TEXT, source_chapter_ids TEXT, strategy TEXT,
		status TEXT, current_index INTEGER, merged_count INTEGER, current_source_chapter_id TEXT,
		conflict_files TEXT, error TEXT, locale TEXT, created_at TEXT, updated_at TEXT
	);
	INSERT INTO chapters (id, project_id) VALUES ('target', 'project_a'), ('other', 'project_b');
	INSERT INTO narrators (id, chapter_id) VALUES ('nar_other', 'other');
	INSERT INTO merge_sessions (id, target_chapter_id, source_chapter_ids, strategy, status, current_index, merged_count, created_at, updated_at)
	VALUES ('merge_project_a', 'target', '["source"]', 'merge', 'waiting_decision', 0, 0, 'now', 'now');`)
	if err != nil {
		t.Fatalf("create merge session: %v", err)
	}

	hub := ws.NewHub()
	hub.SetMergeDB(sqlDB)
	conn, cleanup := dialNarratorHub(t, hub)
	defer cleanup()
	subscribeNarrator(t, conn, "nar_other")
	waitForNarratorSubscription(t, hub, conn, "nar_other")

	if err := conn.WriteJSON(map[string]any{"type": "merge_decision", "mergeSessionId": "merge_project_a", "decision": "continue"}); err != nil {
		t.Fatalf("write merge_decision: %v", err)
	}
	msg := readWSMessage(t, conn)
	if msg["type"] != "error" {
		t.Fatalf("expected project-scoped merge decision rejection, got %#v", msg)
	}
	var status string
	if err := sqlDB.QueryRow(`SELECT status FROM merge_sessions WHERE id = 'merge_project_a'`).Scan(&status); err != nil {
		t.Fatalf("query status: %v", err)
	}
	if status != "waiting_decision" {
		t.Fatalf("status = %q, want waiting_decision", status)
	}
}

func TestHubMergeDecisionCancelMarksSessionCancelled(t *testing.T) {
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
	VALUES ('merge_cancel', 'target', '["source"]', 'merge', 'waiting_decision', 0, 0, 'now', 'now');`)
	if err != nil {
		t.Fatalf("create merge session: %v", err)
	}

	hub := ws.NewHub()
	hub.SetMergeDB(sqlDB)
	conn, cleanup := dialNarratorHub(t, hub)
	defer cleanup()

	if err := conn.WriteJSON(map[string]any{"type": "merge_decision", "mergeSessionId": "merge_cancel", "decision": "cancel"}); err != nil {
		t.Fatalf("write merge_decision: %v", err)
	}
	msg := readWSMessage(t, conn)
	if msg["type"] != "merge_decision_resolved" || msg["decision"] != "cancel" || msg["status"] != "cancelled" {
		t.Fatalf("unexpected WS ack: %#v", msg)
	}

	var status string
	if err := sqlDB.QueryRow(`SELECT status FROM merge_sessions WHERE id = 'merge_cancel'`).Scan(&status); err != nil {
		t.Fatalf("query status: %v", err)
	}
	if status != "cancelled" {
		t.Fatalf("status = %q, want cancelled", status)
	}
}
