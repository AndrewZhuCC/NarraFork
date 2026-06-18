package containerproxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

// TestProxyWebSocketUnknownHostReturns502 verifies a WebSocket upgrade request
// for a host that resolves to no running container is rejected with 502 BEFORE
// any upgrade (target resolution happens in ServeHTTP prior to the WS branch),
// rather than upgrading and then dangling. gorilla's Dial surfaces this as
// websocket.ErrBadHandshake with the 502 response attached.
func TestProxyWebSocketUnknownHostReturns502(t *testing.T) {
	database := openProxyTestDB(t)
	proxy := httptest.NewServer(New(database, nil))
	defer proxy.Close()

	wsURL := "ws" + strings.TrimPrefix(proxy.URL, "http") + "/socket"
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, http.Header{"Host": []string{"nope.dev.example.test"}})
	if conn != nil {
		conn.Close()
		t.Fatal("expected no websocket connection for unknown host")
	}
	if err == nil {
		t.Fatal("expected dial error for unknown host")
	}
	if resp == nil {
		t.Fatalf("expected an HTTP response with the dial error, got nil (err=%v)", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("unknown-host WS upgrade status = %d, want 502", resp.StatusCode)
	}
}
