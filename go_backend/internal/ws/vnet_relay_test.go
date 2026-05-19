package ws

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

func TestVNetHandlerAnonymousRelayPeerCleanup(t *testing.T) {
	authService := newWSAuthService(t)
	settings := authService.Settings
	if _, err := settings.Patch(map[string]any{"vnet": map[string]any{"enabled": true, "allowAnonymousRelay": true, "maxMessageBytes": float64(4096), "udp": map[string]any{"enabled": false}}}); err != nil {
		t.Fatalf("patch settings: %v", err)
	}
	hub := NewHub()
	server := httptest.NewServer(hub.VNetHandler(authService, settings))
	defer server.Close()

	connA, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial vnet a: %v", err)
	}
	defer connA.Close()
	connB, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial vnet b: %v", err)
	}
	defer connB.Close()

	if err := connA.WriteJSON(map[string]any{"type": "hello", "networkId": "net", "peerId": "a", "virtualIp": "10.0.0.1"}); err != nil {
		t.Fatalf("hello a: %v", err)
	}
	if msg := readWSMessageOfType(t, connA, "welcome"); msg["peerId"] != "a" || msg["networkId"] != "net" {
		t.Fatalf("unexpected welcome a: %#v", msg)
	}
	if err := connB.WriteJSON(map[string]any{"type": "hello", "networkId": "net", "peerId": "b", "virtualIp": "10.0.0.2"}); err != nil {
		t.Fatalf("hello b: %v", err)
	}
	if msg := readWSMessageOfType(t, connB, "welcome"); msg["peerId"] != "b" {
		t.Fatalf("unexpected welcome b: %#v", msg)
	}
	if msg := readWSMessageOfType(t, connA, "peer_joined"); msg["type"] != "peer_joined" {
		t.Fatalf("expected peer_joined for a, got %#v", msg)
	}

	_ = connB.Close()
	if msg := readWSMessageOfType(t, connA, "peer_left"); msg["peerId"] != "b" {
		t.Fatalf("expected peer_left for b, got %#v", msg)
	}
	stats := hub.VNetStats(settings)
	if stats["peers"] != 1 || stats["connections"] != 1 {
		t.Fatalf("unexpected stats after b close: %#v", stats)
	}
}

func TestVNetHandlerRelayTokenJWTDisabledAndMessageLimit(t *testing.T) {
	authService := newWSAuthService(t)
	settings := authService.Settings
	hub := NewHub()
	server := httptest.NewServer(hub.VNetHandler(authService, settings))
	defer server.Close()
	url := "ws" + strings.TrimPrefix(server.URL, "http")

	if _, err := settings.Patch(map[string]any{"vnet": map[string]any{"enabled": true, "allowAnonymousRelay": false, "relayToken": "relay-secret", "maxMessageBytes": float64(4096)}}); err != nil {
		t.Fatalf("patch relay settings: %v", err)
	}
	relayConn, _, err := websocket.DefaultDialer.Dial(url+"?token=relay-secret", nil)
	if err != nil {
		t.Fatalf("relay token should connect: %v", err)
	}
	_ = relayConn.Close()

	token, err := authService.CreateToken("user-1", "admin")
	if err != nil {
		t.Fatalf("create jwt: %v", err)
	}
	jwtConn, _, err := websocket.DefaultDialer.Dial(url+"?token="+token, nil)
	if err != nil {
		t.Fatalf("jwt should connect: %v", err)
	}
	_ = jwtConn.Close()

	_, resp, err := websocket.DefaultDialer.Dial(url, nil)
	if err == nil {
		t.Fatal("anonymous disabled connection without token should fail")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unauthenticated vnet, resp=%v err=%v", resp, err)
	}

	if _, err := settings.Patch(map[string]any{"vnet": map[string]any{"enabled": true, "allowAnonymousRelay": true, "maxMessageBytes": float64(96)}}); err != nil {
		t.Fatalf("patch limit settings: %v", err)
	}
	limitedConn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("limited anonymous connect: %v", err)
	}
	defer limitedConn.Close()
	if err := limitedConn.WriteJSON(map[string]any{"type": "hello", "networkId": strings.Repeat("n", 200), "peerId": "p", "virtualIp": "10.0.0.1"}); err != nil {
		t.Fatalf("write oversized hello: %v", err)
	}
	if msg := readWSMessageOfType(t, limitedConn, "error"); msg["code"] != "MESSAGE_TOO_LARGE" {
		t.Fatalf("expected MESSAGE_TOO_LARGE, got %#v", msg)
	}

	if _, err := settings.Patch(map[string]any{"vnet": map[string]any{"enabled": false}}); err != nil {
		t.Fatalf("patch disabled settings: %v", err)
	}
	disabledConn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("disabled vnet should still upgrade to report error: %v", err)
	}
	defer disabledConn.Close()
	if msg := readWSMessageOfType(t, disabledConn, "error"); msg["code"] != "VNET_DISABLED" || msg["supported"] != true {
		t.Fatalf("expected VNET_DISABLED websocket payload, got %#v", msg)
	}
}
