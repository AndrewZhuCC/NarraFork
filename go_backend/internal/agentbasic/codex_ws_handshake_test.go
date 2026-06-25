package agentbasic

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestIsOfficialChatGPTDomain(t *testing.T) {
	cases := []struct {
		url  string
		want bool
	}{
		{"https://chatgpt.com/backend-api/codex", true},
		{"https://api.chatgpt.com", true},
		{"https://x.openai.com/v1", true},
		{"https://example.com", false},
		{"http://127.0.0.1:8080", false},
		{"://bad", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := isOfficialChatGPTDomain(tc.url); got != tc.want {
			t.Errorf("isOfficialChatGPTDomain(%q)=%v want %v", tc.url, got, tc.want)
		}
	}
}

// startCodexWSHandshakeServer returns an httptest server that upgrades the codex
// WS handshake, records the request headers, then immediately closes the
// connection so streamCodexResponsesWebSocket returns. The captured headers are
// delivered on the returned channel.
func startCodexWSHandshakeServer(t *testing.T) (*httptest.Server, <-chan http.Header) {
	t.Helper()
	headerCh := make(chan http.Header, 1)
	var once sync.Once
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured := r.Header.Clone()
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		once.Do(func() { headerCh <- captured })
		// Close immediately so the client's ReadMessage returns and the stream
		// function exits.
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
		_ = conn.Close()
	}))
	return server, headerCh
}

func drainCodexWSStream(t *testing.T, provider Provider) {
	t.Helper()
	out := make(chan StreamEvent, 8)
	go func() {
		_ = streamCodexResponsesWebSocket(context.Background(), provider, []Message{{Role: "user", Content: "hi"}}, "", "normal", out)
		close(out)
	}()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-out:
			if !ok {
				return
			}
		case <-timeout:
			t.Fatal("codex ws stream did not finish in time")
		}
	}
}

// TestCodexWSHandshakeSetsExpectedHeaders verifies the codex WS handshake sends
// User-Agent, Origin, session_id, x-client-request-id, OpenAI-Beta and originator,
// and that ChatGPT-Account-Id is NOT sent to a non-official (test) domain.
func TestCodexWSHandshakeSetsExpectedHeaders(t *testing.T) {
	server, headerCh := startCodexWSHandshakeServer(t)
	defer server.Close()

	provider := Provider{Kind: "codex", APIKey: "tok", BaseURL: server.URL, Model: "gpt-5-codex", AccountID: "acc-1", PromptCacheKey: "sess-1"}
	drainCodexWSStream(t, provider)

	var headers http.Header
	select {
	case headers = <-headerCh:
	case <-time.After(2 * time.Second):
		t.Fatal("did not capture handshake headers")
	}

	if ua := headers.Get("User-Agent"); ua == "" || !strings.Contains(ua, "narrafork/") {
		t.Fatalf("User-Agent should be set and branded, got %q", ua)
	}
	if origin := headers.Get("Origin"); origin != server.URL {
		t.Fatalf("Origin should equal baseURL for non-official domain, got %q (want %q)", origin, server.URL)
	}
	if got := headers.Get("session_id"); got != "sess-1" {
		t.Fatalf("session_id should equal PromptCacheKey, got %q", got)
	}
	if got := headers.Get("x-client-request-id"); got != "sess-1" {
		t.Fatalf("x-client-request-id should equal PromptCacheKey, got %q", got)
	}
	if got := headers.Get("OpenAI-Beta"); got != codexResponsesWSBeta {
		t.Fatalf("OpenAI-Beta mismatch, got %q", got)
	}
	if got := headers.Get("originator"); got != "narrafork" {
		t.Fatalf("originator mismatch, got %q", got)
	}
	if got := headers.Get("ChatGPT-Account-Id"); got != "" {
		t.Fatalf("ChatGPT-Account-Id must NOT be sent to a non-official domain, got %q", got)
	}
}

// TestCodexWSHandshakeOmitsSessionHeadersWhenEmpty verifies session headers are
// omitted when no PromptCacheKey is available.
func TestCodexWSHandshakeOmitsSessionHeadersWhenEmpty(t *testing.T) {
	server, headerCh := startCodexWSHandshakeServer(t)
	defer server.Close()

	provider := Provider{Kind: "codex", APIKey: "tok", BaseURL: server.URL, Model: "gpt-5-codex"}
	drainCodexWSStream(t, provider)

	var headers http.Header
	select {
	case headers = <-headerCh:
	case <-time.After(2 * time.Second):
		t.Fatal("did not capture handshake headers")
	}
	if got := headers.Get("session_id"); got != "" {
		t.Fatalf("session_id must be omitted when empty, got %q", got)
	}
	if got := headers.Get("x-client-request-id"); got != "" {
		t.Fatalf("x-client-request-id must be omitted when empty, got %q", got)
	}
}
