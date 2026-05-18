package ws_test

import (
	"database/sql"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	_ "modernc.org/sqlite"

	"narrafork-go/internal/auth"
	"narrafork-go/internal/config"
	"narrafork-go/internal/ws"
)

func TestNarratorPresenceJoinAndLeaveBroadcastsViewerFields(t *testing.T) {
	hub, authService, server, cleanup := setupPresenceHub(t)
	defer cleanup()

	viewer := dialPresenceConn(t, server, authService, "user_1")
	defer viewer.Close()
	observer := dialPresenceConn(t, server, authService, "user_2")
	defer observer.Close()

	subscribeNarrator(t, viewer, "nar_presence")
	subscribeNarrator(t, observer, "nar_presence")
	waitForPresenceSubscription(t, hub, observer, "nar_presence")

	if err := viewer.WriteJSON(map[string]any{"type": "presence_join", "narratorId": "nar_presence"}); err != nil {
		t.Fatalf("write presence_join: %v", err)
	}
	msg := readWSMessage(t, observer)
	if msg["type"] != "presence_update" || msg["narratorId"] != "nar_presence" {
		t.Fatalf("expected presence_update, got %#v", msg)
	}
	viewers := presenceViewers(t, msg)
	if len(viewers) != 1 {
		t.Fatalf("expected one viewer, got %#v", viewers)
	}
	if viewers[0]["userId"] != "user_1" || viewers[0]["username"] != "Alice" || viewers[0]["avatarColor"] != "#123456" || viewers[0]["avatarImageId"] != "avatar_1" {
		t.Fatalf("viewer payload is not frontend-compatible: %#v", viewers[0])
	}

	if err := viewer.WriteJSON(map[string]any{"type": "presence_leave", "narratorId": "nar_presence"}); err != nil {
		t.Fatalf("write presence_leave: %v", err)
	}
	msg = readWSMessage(t, observer)
	if msg["type"] != "presence_update" || len(presenceViewers(t, msg)) != 0 {
		t.Fatalf("expected empty presence_update after leave, got %#v", msg)
	}

}

func TestNarratorPresenceDeduplicatesUserAndCleansUpOnClose(t *testing.T) {
	hub, authService, server, cleanup := setupPresenceHub(t)
	defer cleanup()

	observer := dialPresenceConn(t, server, authService, "user_2")
	defer observer.Close()
	first := dialPresenceConn(t, server, authService, "user_1")
	second := dialPresenceConn(t, server, authService, "user_1")
	defer second.Close()

	subscribeNarrator(t, observer, "nar_presence")
	subscribeNarrator(t, first, "nar_presence")
	subscribeNarrator(t, second, "nar_presence")
	waitForPresenceSubscription(t, hub, observer, "nar_presence")

	if err := first.WriteJSON(map[string]any{"type": "presence_join", "narratorId": "nar_presence"}); err != nil {
		t.Fatalf("write first presence_join: %v", err)
	}
	if viewers := presenceViewers(t, readWSMessage(t, observer)); len(viewers) != 1 {
		t.Fatalf("expected first connection to add one unique viewer, got %#v", viewers)
	}

	if err := second.WriteJSON(map[string]any{"type": "presence_join", "narratorId": "nar_presence"}); err != nil {
		t.Fatalf("write second presence_join: %v", err)
	}
	if viewers := presenceViewers(t, readWSMessage(t, observer)); len(viewers) != 1 {
		t.Fatalf("expected same user to stay deduplicated, got %#v", viewers)
	}

	_ = first.Close()
	if viewers := presenceViewers(t, readWSMessage(t, observer)); len(viewers) != 1 {
		t.Fatalf("expected second connection to keep user present, got %#v", viewers)
	}

	_ = second.Close()
	if viewers := presenceViewers(t, readWSMessage(t, observer)); len(viewers) != 0 {
		t.Fatalf("expected final close to clear presence, got %#v", viewers)
	}
}

func setupPresenceHub(t *testing.T) (*ws.Hub, *auth.Service, *httptest.Server, func()) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	settings, err := config.Load()
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	_, err = sqlDB.Exec(`CREATE TABLE users (
		id TEXT PRIMARY KEY,
		username TEXT,
		password_hash TEXT,
		role TEXT,
		avatar_color TEXT,
		avatar_image_id TEXT,
		git_username TEXT,
		git_email TEXT,
		created_at TEXT
	)`)
	if err != nil {
		t.Fatalf("create users: %v", err)
	}
	_, err = sqlDB.Exec(`INSERT INTO users (id, username, password_hash, role, avatar_color, avatar_image_id, created_at) VALUES
		('user_1', 'Alice', 'hash', 'user', '#123456', 'avatar_1', '2026-05-14T00:00:00Z'),
		('user_2', 'Bob', 'hash', 'user', '#abcdef', NULL, '2026-05-14T00:00:00Z')`)
	if err != nil {
		t.Fatalf("insert users: %v", err)
	}
	authService := auth.NewService(sqlDB, settings)
	hub := ws.NewHub()
	server := httptest.NewServer(hub.NarratorHandler(authService))
	return hub, authService, server, func() {
		server.Close()
		_ = sqlDB.Close()
	}
}

func dialPresenceConn(t *testing.T, server *httptest.Server, authService *auth.Service, userID string) *websocket.Conn {
	t.Helper()
	token, err := authService.CreateToken(userID, "user")
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"?token="+token, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	return conn
}

func subscribeNarrator(t *testing.T, conn *websocket.Conn, narratorID string) {
	t.Helper()
	if err := conn.WriteJSON(map[string]any{"type": "subscribe", "narratorIds": []string{narratorID}}); err != nil {
		t.Fatalf("subscribe narrator: %v", err)
	}
}

func waitForPresenceSubscription(t *testing.T, _ *ws.Hub, _ *websocket.Conn, _ string) {
	t.Helper()
	time.Sleep(50 * time.Millisecond)
}

func presenceViewers(t *testing.T, msg map[string]any) []map[string]any {
	t.Helper()
	raw, _ := msg["viewers"].([]any)
	viewers := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		viewer, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("unexpected viewer item: %#v", item)
		}
		viewers = append(viewers, viewer)
	}
	return viewers
}
