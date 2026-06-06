package ws_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"narrafork-go/internal/auth"
	"narrafork-go/internal/config"
	"narrafork-go/internal/ws"
)

type fakeNarratorSyncProvider struct {
	mu      sync.Mutex
	version int
	page    map[string]any
	anchors []any
}

func (p *fakeNarratorSyncProvider) NarratorMessageVersion(context.Context, string) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.version, nil
}

func (p *fakeNarratorSyncProvider) NarratorMessagesAfter(_ context.Context, _ string, anchor any, _ int) (map[string]any, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.anchors = append(p.anchors, anchor)
	return p.page, nil
}

func TestNarratorSubscribeSendsStreamingSnapshotAndCatchUp(t *testing.T) {
	hub := ws.NewHub()
	provider := &fakeNarratorSyncProvider{
		version: 7,
		page: map[string]any{
			"topLevel":       []any{map[string]any{"id": "msg_2", "role": "assistant"}},
			"orphanChildren": []any{},
			"hitLimit":       false,
			"cursor":         map[string]any{"parentLastMessageId": "msg_2", "childAnchors": []any{}},
		},
	}
	hub.SetNarratorSyncProvider(provider)
	hub.BroadcastNarrator("nar_1", map[string]any{"type": "stream_text", "narratorId": "nar_1", "text": "hel", "outputIndex": 0})
	hub.BroadcastNarrator("nar_1", map[string]any{"type": "stream_text", "narratorId": "nar_1", "text": "lo", "outputIndex": 0})
	hub.BroadcastNarrator("nar_1", map[string]any{"type": "tool_call", "narratorId": "nar_1", "toolUseId": "tool_1", "toolName": "Read", "input": map[string]any{"file_path": "a.txt"}, "streamStartedAt": int64(1770000000000)})
	hub.BroadcastNarrator("nar_1", map[string]any{"type": "tool_use_chunk", "narratorId": "nar_1", "toolUseId": "tool_1", "toolName": "Read", "inputCharsTotal": 42, "streamingField": map[string]any{"name": "file_path", "delta": "a.txt"}})

	conn, cleanup := dialNarratorHub(t, hub)
	defer cleanup()
	if err := conn.WriteJSON(map[string]any{"type": "subscribe", "narratorIds": []string{"nar_1"}, "lastMessageId": "msg_1"}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	snapshot := readWSMessage(t, conn)
	if snapshot["type"] != "streaming_snapshot" || snapshot["narratorId"] != "nar_1" {
		t.Fatalf("expected streaming_snapshot, got %#v", snapshot)
	}
	blocks, _ := snapshot["streamingBlocks"].([]any)
	if len(blocks) != 1 || blocks[0].(map[string]any)["text"] != "hello" || wsTestIntNumber(blocks[0].(map[string]any)["outputIndex"]) != 0 {
		t.Fatalf("expected aggregated streaming text, got %#v", snapshot["streamingBlocks"])
	}
	chunks, _ := snapshot["toolChunks"].([]any)
	if len(chunks) != 1 {
		t.Fatalf("expected active tool chunk, got %#v", snapshot["toolChunks"])
	}
	chunk := chunks[0].(map[string]any)
	if chunk["toolUseId"] != "tool_1" || chunk["started"] != true || wsTestIntNumber(chunk["inputCharsTotal"]) != 42 || wsTestIntNumber(chunk["streamStartedAt"]) != 1770000000000 {
		t.Fatalf("expected active tool chunk with started/inputCharsTotal, got %#v", chunk)
	}

	catchUp := readWSMessage(t, conn)
	if catchUp["type"] != "catch_up" || catchUp["messageVersion"] != float64(7) {
		t.Fatalf("expected catch_up with messageVersion, got %#v", catchUp)
	}
	topLevel, _ := catchUp["topLevel"].([]any)
	if len(topLevel) != 1 || topLevel[0].(map[string]any)["id"] != "msg_2" {
		t.Fatalf("expected catch-up top-level message, got %#v", catchUp["topLevel"])
	}
	orphanChildren, _ := catchUp["orphanChildren"].([]any)
	if len(orphanChildren) != 0 {
		t.Fatalf("expected empty catch-up orphanChildren, got %#v", catchUp["orphanChildren"])
	}

	hub.BroadcastNarrator("nar_1", map[string]any{"type": "message", "narratorId": "nar_1", "message": map[string]any{"id": "msg_3"}})
	realtime := readWSMessage(t, conn)
	if realtime["type"] != "message" {
		t.Fatalf("expected realtime message after catch-up subscription, got %#v", realtime)
	}

	provider.mu.Lock()
	anchors := append([]any{}, provider.anchors...)
	provider.mu.Unlock()
	if len(anchors) != 1 || anchors[0] != "msg_1" {
		t.Fatalf("expected legacy lastMessageId anchor, got %#v", anchors)
	}
}

func TestNarratorSyncCheckUsesCatchUpOrFullReload(t *testing.T) {
	hub := ws.NewHub()
	provider := &fakeNarratorSyncProvider{
		version: 3,
		page: map[string]any{
			"topLevel":       []any{map[string]any{"id": "msg_4"}},
			"orphanChildren": []any{},
			"hitLimit":       false,
			"cursor":         map[string]any{"parentLastMessageId": "msg_4"},
		},
	}
	hub.SetNarratorSyncProvider(provider)
	conn, cleanup := dialNarratorHub(t, hub)
	defer cleanup()

	if err := conn.WriteJSON(map[string]any{"type": "sync_check", "narratorId": "nar_1", "version": 3}); err != nil {
		t.Fatalf("write sync_check: %v", err)
	}
	if msg := readWSMessage(t, conn); msg["type"] != "sync_ok" || msg["version"] != float64(3) {
		t.Fatalf("expected sync_ok, got %#v", msg)
	}

	if err := conn.WriteJSON(map[string]any{"type": "sync_check", "narratorId": "nar_1", "version": 1, "catchUpCursor": map[string]any{"parentLastMessageId": "msg_3", "childAnchors": []any{}}}); err != nil {
		t.Fatalf("write catch-up sync_check: %v", err)
	}
	if msg := readWSMessage(t, conn); msg["type"] != "catch_up" || msg["messageVersion"] != float64(3) {
		t.Fatalf("expected catch_up, got %#v", msg)
	}

	if err := conn.WriteJSON(map[string]any{"type": "sync_check", "narratorId": "nar_1", "version": 1}); err != nil {
		t.Fatalf("write reload sync_check: %v", err)
	}
	if msg := readWSMessage(t, conn); msg["type"] != "full_reload" {
		t.Fatalf("expected full_reload without anchor, got %#v", msg)
	}
}

func dialNarratorHub(t *testing.T, hub *ws.Hub) (*websocket.Conn, func()) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	settings, err := config.Load()
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	authService := &auth.Service{Settings: settings}
	token, err := authService.CreateToken("user_1", "admin")
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	server := httptest.NewServer(hub.NarratorHandler(authService))
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"?token="+token, nil)
	if err != nil {
		server.Close()
		t.Fatalf("dial websocket: %v", err)
	}
	return conn, func() {
		_ = conn.Close()
		server.Close()
	}
}

func readWSMessage(t *testing.T, conn *websocket.Conn) map[string]any {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var msg map[string]any
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatalf("read websocket message: %v", err)
	}
	return msg
}

func wsTestIntNumber(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	default:
		return 0
	}
}

func waitForNarratorSubscription(t *testing.T, _ *ws.Hub, _ *websocket.Conn, _ string) {
	t.Helper()
	time.Sleep(20 * time.Millisecond)
}
