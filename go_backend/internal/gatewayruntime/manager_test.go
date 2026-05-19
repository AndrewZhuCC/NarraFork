package gatewayruntime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTelegramAdapterPollsAllowedUserAndSends(t *testing.T) {
	received := make(chan InboundMessage, 1)
	sent := make(chan map[string]any, 1)
	updatesServed := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bottoken/getUpdates":
			if !updatesServed {
				updatesServed = true
				_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": []any{map[string]any{"update_id": 1, "message": map[string]any{"text": "hello", "chat": map[string]any{"id": 42}, "from": map[string]any{"id": 7, "username": "alice"}}}}})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": []any{}})
		case "/bottoken/sendMessage":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			sent <- body
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"message_id": 1}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	adapter := newTelegramAdapter(platformConfig{Platform: "telegram", Enabled: true, Token: "token", APIBase: server.URL, AllowedUsers: []string{"7"}}, func(ctx context.Context, msg InboundMessage) error {
		received <- msg
		return nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := adapter.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer adapter.Stop()
	select {
	case msg := <-received:
		if msg.Platform != "telegram" || msg.ChatID != "42" || msg.UserID != "7" || msg.Text != "hello" {
			t.Fatalf("unexpected inbound message: %#v", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("telegram update was not delivered")
	}
	if err := adapter.Send(context.Background(), "42", "reply"); err != nil {
		t.Fatalf("send: %v", err)
	}
	select {
	case body := <-sent:
		if body["chat_id"] != "42" || body["text"] != "reply" {
			t.Fatalf("unexpected send body: %#v", body)
		}
	case <-time.After(time.Second):
		t.Fatal("telegram send was not observed")
	}
}

func TestManagerStartsTelegramRuntimeFromEnv(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": []any{}})
	}))
	defer server.Close()
	t.Setenv("GATEWAY_ENABLED", "true")
	t.Setenv("TELEGRAM_BOT_TOKEN", "token")
	t.Setenv("TELEGRAM_API_BASE", server.URL)
	m := New(nil, nil, nil, nil)
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer m.Stop()
	status := m.Status(context.Background())
	if status["persistentRuntimes"] != true || status["supported"] != true {
		t.Fatalf("unexpected gateway status: %#v", status)
	}
	platforms := status["platforms"].([]string)
	if len(platforms) != 1 || platforms[0] != "telegram" {
		t.Fatalf("expected telegram active platform, got %#v", status["platforms"])
	}
}
