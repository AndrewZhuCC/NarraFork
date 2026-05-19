package containerproxy

import (
	"context"
	"database/sql"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gorilla/websocket"

	_ "modernc.org/sqlite"
)

func openProxyTestDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = database.Close() })
	for _, stmt := range []string{
		`CREATE TABLE projects (id TEXT PRIMARY KEY, proxy_domain TEXT)`,
		`CREATE TABLE chapters (id TEXT PRIMARY KEY, project_id TEXT)`,
		`CREATE TABLE container_instances (id TEXT PRIMARY KEY, chapter_id TEXT, container_id TEXT, service_name TEXT, status TEXT, container_ip TEXT, container_port INTEGER, proxy_label TEXT)`,
		`INSERT INTO projects (id, proxy_domain) VALUES ('p1', 'dev.example.test')`,
		`INSERT INTO chapters (id, project_id) VALUES ('c1', 'p1')`,
	} {
		if _, err := database.Exec(stmt); err != nil {
			t.Fatalf("exec fixture: %v\n%s", err, stmt)
		}
	}
	return database
}

func seedProxyTarget(t *testing.T, database *sql.DB, ip string, port int) {
	t.Helper()
	if _, err := database.Exec(`INSERT INTO container_instances (id, chapter_id, service_name, status, container_ip, container_port, proxy_label) VALUES ('ci1', 'c1', 'web', 'running', ?, ?, 'abc12345-web-8080')`, ip, port); err != nil {
		t.Fatalf("insert target: %v", err)
	}
}

func TestProxyHTTPForwardsByHostLabel(t *testing.T) {
	database := openProxyTestDB(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/hello" || r.URL.RawQuery != "name=go" {
			t.Fatalf("unexpected upstream URL: %s", r.URL.String())
		}
		if got := r.Header.Get("X-Forwarded-Host"); got != "abc12345-web-8080.dev.example.test" {
			t.Fatalf("X-Forwarded-Host = %q", got)
		}
		if got := r.Header.Get("X-NarraFork-Chapter"); got != "c1" {
			t.Fatalf("X-NarraFork-Chapter = %q", got)
		}
		w.Header().Set("X-Upstream", "ok")
		_, _ = w.Write([]byte("proxied"))
	}))
	defer upstream.Close()
	ip, port := hostPortFromURL(t, upstream.URL)
	seedProxyTarget(t, database, ip, port)
	proxy := httptest.NewServer(New(database, nil))
	defer proxy.Close()

	req, err := http.NewRequest(http.MethodGet, proxy.URL+"/hello?name=go", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Host = "abc12345-web-8080.dev.example.test"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("proxy request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("X-Upstream") != "ok" {
		t.Fatalf("proxy status/header = %d/%q", resp.StatusCode, resp.Header.Get("X-Upstream"))
	}
}

func TestProxyUnknownHostReturns502(t *testing.T) {
	database := openProxyTestDB(t)
	proxy := httptest.NewServer(New(database, nil))
	defer proxy.Close()
	req, err := http.NewRequest(http.MethodGet, proxy.URL+"/missing", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Host = "unknown.dev.example.test"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("proxy request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}
}

func TestProxyWebSocketEcho(t *testing.T) {
	database := openProxyTestDB(t)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("upstream upgrade: %v", err)
		}
		defer conn.Close()
		messageType, payload, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("upstream read: %v", err)
		}
		if err := conn.WriteMessage(messageType, append([]byte("echo:"), payload...)); err != nil {
			t.Fatalf("upstream write: %v", err)
		}
	}))
	defer upstream.Close()
	ip, port := hostPortFromURL(t, upstream.URL)
	seedProxyTarget(t, database, ip, port)
	proxy := httptest.NewServer(New(database, nil))
	defer proxy.Close()
	wsURL := "ws" + strings.TrimPrefix(proxy.URL, "http") + "/socket"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, http.Header{"Host": []string{"abc12345-web-8080.dev.example.test"}})
	if err != nil {
		t.Fatalf("dial proxy websocket: %v", err)
	}
	defer conn.Close()
	if err := conn.WriteMessage(websocket.TextMessage, []byte("ping")); err != nil {
		t.Fatalf("write websocket: %v", err)
	}
	_, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read websocket: %v", err)
	}
	if string(payload) != "echo:ping" {
		t.Fatalf("payload = %q", payload)
	}
}

func TestEnsureStartStopRestart(t *testing.T) {
	manager := New(openProxyTestDB(t), nil)
	first := freePort(t)
	second := freePort(t)
	if err := manager.Ensure(context.Background(), Desired{Enabled: true, Port: first}); err != nil {
		t.Fatalf("ensure start: %v", err)
	}
	if state := manager.State(); !state.Running || state.Port != first {
		t.Fatalf("state after start = %#v", state)
	}
	if err := manager.Ensure(context.Background(), Desired{Enabled: true, Port: second}); err != nil {
		t.Fatalf("ensure restart: %v", err)
	}
	if state := manager.State(); !state.Running || state.Port != second {
		t.Fatalf("state after restart = %#v", state)
	}
	if err := manager.Ensure(context.Background(), Desired{Enabled: false, Port: second}); err != nil {
		t.Fatalf("ensure stop: %v", err)
	}
	if state := manager.State(); state.Running || state.Port != 0 {
		t.Fatalf("state after stop = %#v", state)
	}
}

func TestDesiredFromSettings(t *testing.T) {
	raw := map[string]any{"containers": map[string]any{"proxy": map[string]any{"enabled": true, "port": float64(7781)}}}
	desired := DesiredFromSettings(raw)
	if !desired.Enabled || desired.Port != 7781 {
		t.Fatalf("desired = %#v", desired)
	}
}

func hostPortFromURL(t *testing.T, raw string) (string, int) {
	t.Helper()
	hostport := strings.TrimPrefix(raw, "http://")
	host, portRaw, err := net.SplitHostPort(hostport)
	if err != nil {
		t.Fatalf("split host port %q: %v", raw, err)
	}
	port, err := strconv.Atoi(portRaw)
	if err != nil {
		t.Fatalf("parse port %q: %v", portRaw, err)
	}
	return host, port
}

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen free port: %v", err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}
