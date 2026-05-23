package ws

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"narrafork-go/internal/auth"
	"narrafork-go/internal/config"
)

func TestJSONHeartbeatServerPingAndClientPingPong(t *testing.T) {
	previousInterval := websocketHeartbeatInterval
	websocketHeartbeatInterval = 10 * time.Millisecond
	t.Cleanup(func() { websocketHeartbeatInterval = previousInterval })

	authService := newWSAuthService(t)
	token, err := authService.CreateToken("user-1", "admin")
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	hub := NewHub()
	server := httptest.NewServer(hub.NarratorHandler(authService))
	defer server.Close()

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"?token="+token, nil)
	if err != nil {
		t.Fatalf("dial narrator websocket: %v", err)
	}
	defer conn.Close()

	if msg := readWSMessageOfType(t, conn, "ping"); msg["type"] != "ping" {
		t.Fatalf("expected server JSON ping, got %#v", msg)
	}
	if err := conn.WriteJSON(map[string]any{"type": "pong"}); err != nil {
		t.Fatalf("write pong: %v", err)
	}
	if err := conn.WriteJSON(map[string]any{"type": "ping"}); err != nil {
		t.Fatalf("write ping: %v", err)
	}
	if msg := readWSMessageOfType(t, conn, "pong"); msg["type"] != "pong" {
		t.Fatalf("expected JSON pong response, got %#v", msg)
	}
}

func TestVNetWebSocketDisabledContract(t *testing.T) {
	hub := NewHub()
	server := httptest.NewServer(hub.VNetDisabledHandler())
	defer server.Close()

	plainReq := httptest.NewRequest(http.MethodGet, "/ws/vnet", nil)
	plainW := httptest.NewRecorder()
	hub.VNetDisabledHandler().ServeHTTP(plainW, plainReq)
	if plainW.Code != http.StatusNotImplemented {
		t.Fatalf("expected plain /ws/vnet to return 501, got %d: %s", plainW.Code, plainW.Body.String())
	}
	var plainBody map[string]any
	if err := json.Unmarshal(plainW.Body.Bytes(), &plainBody); err != nil {
		t.Fatalf("decode plain vnet disabled body: %v", err)
	}
	if plainBody["reason"] != "VNet relay/UDP WebSocket runtime is not bundled in go_backend" || plainBody["error"] != plainBody["reason"] || plainBody["message"] != plainBody["reason"] {
		t.Fatalf("plain disabled vnet payload should expose reason/error/message, got %#v", plainBody)
	}

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial vnet disabled websocket: %v", err)
	}
	defer conn.Close()

	msg := readWSMessage(t, conn)
	if msg["type"] != "error" || msg["code"] != "VNET_DISABLED" || msg["supported"] != false || msg["fallback"] != true || msg["reason"] != plainBody["reason"] || msg["error"] != plainBody["reason"] || msg["message"] != plainBody["reason"] {
		t.Fatalf("unexpected disabled vnet payload: %#v", msg)
	}
}

func readWSMessageOfType(t *testing.T, conn *websocket.Conn, want string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		msg := readWSMessage(t, conn)
		if msg["type"] == want {
			return msg
		}
	}
	t.Fatalf("timed out waiting for websocket message type %q", want)
	return nil
}

func newWSAuthService(t *testing.T) *auth.Service {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	settings, err := config.Load()
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	return &auth.Service{Settings: settings}
}
