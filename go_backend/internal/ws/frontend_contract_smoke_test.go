package ws

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type fakeNarratorSync struct {
	version int
	page    map[string]any
}

func (f fakeNarratorSync) NarratorMessageVersion(context.Context, string) (int, error) {
	return f.version, nil
}

func (f fakeNarratorSync) NarratorMessagesAfter(context.Context, string, any, int) (map[string]any, error) {
	if f.page != nil {
		return f.page, nil
	}
	return map[string]any{"topLevel": []any{}, "orphanChildren": []any{}, "hitLimit": false, "cursor": nil}, nil
}

type sequencedNarratorSync struct {
	versions     []int
	pages        []map[string]any
	versionCalls int
	pageCalls    int
}

func (f *sequencedNarratorSync) NarratorMessageVersion(context.Context, string) (int, error) {
	if len(f.versions) == 0 {
		return 0, nil
	}
	idx := f.versionCalls
	f.versionCalls++
	if idx >= len(f.versions) {
		idx = len(f.versions) - 1
	}
	return f.versions[idx], nil
}

func (f *sequencedNarratorSync) NarratorMessagesAfter(context.Context, string, any, int) (map[string]any, error) {
	idx := f.pageCalls
	f.pageCalls++
	if idx < len(f.pages) && f.pages[idx] != nil {
		return f.pages[idx], nil
	}
	return map[string]any{"topLevel": []any{}, "orphanChildren": []any{}, "hitLimit": false, "cursor": nil}, nil
}

func TestFrontendUseNarratorWSContractSubscribeCatchupPresenceAndEvents(t *testing.T) {
	h, c, wsClient := newNarratorHubClient(t)
	defer wsClient.Close()
	h.SetNarratorSyncProvider(fakeNarratorSync{
		version: 3,
		page: map[string]any{
			"topLevel":       []any{map[string]any{"id": "msg-1", "role": "assistant", "contentText": "hello"}},
			"orphanChildren": []any{},
			"hitLimit":       false,
			"cursor":         map[string]any{"parentLastMessageId": "msg-1", "childAnchors": []any{}},
		},
	})
	// useNarratorWS passes fullSubscribe=true from panel views and expects an
	// immediate streaming_snapshot when the server has in-flight output cached.
	h.BroadcastNarrator("narrator-1", map[string]any{"type": "stream_text", "text": "cached", "outputIndex": 0})
	h.BroadcastNarrator("narrator-1", map[string]any{"type": "tool_call", "toolUseId": "tool-cached", "toolName": "Read", "input": map[string]any{"file_path": "README.md"}, "streamStartedAt": int64(1770000000000)})
	h.BroadcastNarrator("narrator-1", map[string]any{"type": "tool_use_chunk", "toolUseId": "tool-cached", "toolName": "Read", "inputCharsTotal": 42, "streamingField": map[string]any{"name": "file_path", "delta": "README.md"}})
	h.BroadcastNarrator("narrator-1", map[string]any{"type": "queue_status", "position": 2, "queueDepth": 5, "queueMessage": "queued behind gateway"})

	h.handleMessage(c, map[string]any{"type": "subscribe", "narratorIds": []any{"narrator-1"}, "lastMessageId": "msg-0"})

	snapshot := readWSMessage(t, wsClient)
	if snapshot["type"] != "streaming_snapshot" || snapshot["narratorId"] != "narrator-1" {
		t.Fatalf("unexpected initial streaming_snapshot: %#v", snapshot)
	}
	streamingBlocks, ok := snapshot["streamingBlocks"].([]any)
	if !ok || len(streamingBlocks) != 1 {
		t.Fatalf("streaming_snapshot must include one streamingBlocks entry: %#v", snapshot)
	}
	block := streamingBlocks[0].(map[string]any)
	if block["type"] != "text" || block["text"] != "cached" || intNumber(block["outputIndex"]) != 0 {
		t.Fatalf("streaming_snapshot should preserve in-flight stream_text: %#v", block)
	}
	toolChunks, ok := snapshot["toolChunks"].([]any)
	if !ok || len(toolChunks) != 1 {
		t.Fatalf("streaming_snapshot must include toolChunks array: %#v", snapshot)
	}
	chunk := toolChunks[0].(map[string]any)
	if chunk["toolUseId"] != "tool-cached" || intNumber(chunk["inputCharsTotal"]) != 42 || chunk["started"] != true || intNumber(chunk["streamStartedAt"]) != 1770000000000 {
		t.Fatalf("streaming_snapshot should preserve tool_use_chunk state: %#v", chunk)
	}

	queueStatus := readWSMessage(t, wsClient)
	if queueStatus["type"] != "queue_status" || queueStatus["narratorId"] != "narrator-1" || intNumber(queueStatus["position"]) != 2 || intNumber(queueStatus["queueDepth"]) != 5 || queueStatus["queueMessage"] != "queued behind gateway" {
		t.Fatalf("unexpected queue_status replay payload: %#v", queueStatus)
	}

	catchUp := readWSMessage(t, wsClient)
	if catchUp["type"] != "catch_up" || catchUp["narratorId"] != "narrator-1" || intNumber(catchUp["messageVersion"]) != 3 {
		t.Fatalf("unexpected catch_up payload: %#v", catchUp)
	}
	topLevel, ok := catchUp["topLevel"].([]any)
	if !ok || len(topLevel) != 1 || topLevel[0].(map[string]any)["id"] != "msg-1" {
		t.Fatalf("catch_up must include topLevel array: %#v", catchUp)
	}
	orphanChildren, ok := catchUp["orphanChildren"].([]any)
	if !ok || len(orphanChildren) != 0 {
		t.Fatalf("catch_up must include orphanChildren array: %#v", catchUp)
	}
	if !h.isSubscribed(c, "narrator-1") {
		t.Fatal("subscribe with catch-up should subscribe after sending catch_up")
	}
	h.BroadcastNarrator("narrator-1", map[string]any{"type": "stream_text", "text": "after-catch-up", "outputIndex": 1})
	live := readWSMessageOfType(t, wsClient, "stream_event")
	liveEvent, _ := live["event"].(map[string]any)
	liveDelta, _ := liveEvent["delta"].(map[string]any)
	if live["narratorId"] != "narrator-1" || liveEvent["type"] != "content_block_delta" || liveDelta["type"] != "text_delta" || liveDelta["text"] != "after-catch-up" || intNumber(liveEvent["outputIndex"]) != 1 {
		t.Fatalf("active reconnect should receive live stream_event after catch_up: %#v", live)
	}

	h.handleMessage(c, map[string]any{"type": "presence_join", "narratorId": "narrator-1"})
	presence := readWSMessage(t, wsClient)
	if presence["type"] != "presence_update" || presence["narratorId"] != "narrator-1" {
		t.Fatalf("unexpected presence_update payload: %#v", presence)
	}
	if viewers, ok := presence["viewers"].([]any); !ok || len(viewers) != 1 {
		t.Fatalf("presence_update must include one viewer: %#v", presence)
	}

	h.BroadcastNarrator("narrator-1", map[string]any{"type": "status", "status": "working", "substatus": []string{"calling_model"}})
	status := readWSMessage(t, wsClient)
	statusChange := readWSMessage(t, wsClient)
	substatusChange := readWSMessage(t, wsClient)
	if status["type"] != "status" || statusChange["type"] != "status_change" || substatusChange["type"] != "substatus_change" {
		t.Fatalf("unexpected status compatibility frames: %#v %#v %#v", status, statusChange, substatusChange)
	}
	if statusChange["narratorId"] != "narrator-1" || statusChange["status"] != "working" {
		t.Fatalf("unexpected status_change frame: %#v", statusChange)
	}
	if substatusChange["narratorId"] != "narrator-1" {
		t.Fatalf("unexpected substatus_change narrator id: %#v", substatusChange)
	}
	if substatus, ok := substatusChange["substatus"].([]any); !ok || len(substatus) != 1 || substatus[0] != "calling_model" {
		t.Fatalf("unexpected substatus_change frame: %#v", substatusChange)
	}

	h.BroadcastNarrator("narrator-1", map[string]any{"type": "stream_text", "text": "delta", "outputIndex": 0})
	legacyStream := readWSMessage(t, wsClient)
	streamEvent := readWSMessage(t, wsClient)
	if legacyStream["type"] != "stream_text" || streamEvent["type"] != "stream_event" {
		t.Fatalf("unexpected stream compatibility frames: %#v %#v", legacyStream, streamEvent)
	}
	event, _ := streamEvent["event"].(map[string]any)
	delta, _ := event["delta"].(map[string]any)
	if event["type"] != "content_block_delta" || delta["type"] != "text_delta" || delta["text"] != "delta" {
		t.Fatalf("unexpected stream_event shape: %#v", streamEvent)
	}

	h.BroadcastNarrator("narrator-1", map[string]any{"type": "tool_use_chunk", "toolUseId": "tool-1", "toolName": "Read", "inputCharsTotal": 7, "streamingField": map[string]any{"name": "file_path", "delta": "a.txt"}})
	toolChunk := readWSMessage(t, wsClient)
	if toolChunk["type"] != "tool_use_chunk" || toolChunk["toolUseId"] != "tool-1" || intNumber(toolChunk["inputCharsTotal"]) != 7 {
		t.Fatalf("unexpected tool_use_chunk frame: %#v", toolChunk)
	}

	h.BroadcastNarrator("narrator-1", map[string]any{"type": "permission_request", "request": map[string]any{"id": "req-1", "toolUseId": "tool-1", "toolName": "Write", "inputJson": map[string]any{"file_path": "a.txt"}}})
	permissionRequest := readWSMessage(t, wsClient)
	request, _ := permissionRequest["request"].(map[string]any)
	if permissionRequest["type"] != "permission_request" || permissionRequest["narratorId"] != "narrator-1" || request["id"] != "req-1" || request["toolUseId"] != "tool-1" {
		t.Fatalf("unexpected permission_request frame: %#v", permissionRequest)
	}
	h.BroadcastNarrator("narrator-1", map[string]any{"type": "permission_resolved", "requestId": "req-1", "toolUseId": "tool-1", "decision": "allow", "updatedInput": map[string]any{"file_path": "b.txt"}})
	permissionResolved := readWSMessage(t, wsClient)
	if permissionResolved["type"] != "permission_resolved" || permissionResolved["requestId"] != "req-1" || permissionResolved["toolUseId"] != "tool-1" || permissionResolved["decision"] != "allow" {
		t.Fatalf("unexpected permission_resolved frame: %#v", permissionResolved)
	}

	h.BroadcastNarrator("narrator-1", map[string]any{"type": "tool_result", "toolUseId": "tool-1", "toolName": "Read", "output": "ok"})
	legacyTool := readWSMessage(t, wsClient)
	toolCompleted := readWSMessage(t, wsClient)
	if legacyTool["type"] != "tool_result" || toolCompleted["type"] != "tool_completed" || toolCompleted["toolUseId"] != "tool-1" {
		t.Fatalf("unexpected tool compatibility frames: %#v %#v", legacyTool, toolCompleted)
	}
}

func TestFrontendUseNarratorWSContractSubscribeCatchesUpMessagesArrivingDuringReconnect(t *testing.T) {
	h, c, wsClient := newNarratorHubClient(t)
	defer wsClient.Close()
	provider := &sequencedNarratorSync{
		versions: []int{3, 4},
		pages: []map[string]any{
			{
				"topLevel":       []any{map[string]any{"id": "msg-1", "role": "assistant", "contentText": "first"}},
				"orphanChildren": []any{},
				"hitLimit":       false,
				"cursor":         map[string]any{"parentLastMessageId": "msg-1", "childAnchors": []any{}},
			},
			{
				"topLevel":       []any{map[string]any{"id": "msg-2", "role": "assistant", "contentText": "arrived-during-catchup"}},
				"orphanChildren": []any{},
				"hitLimit":       false,
				"cursor":         map[string]any{"parentLastMessageId": "msg-2", "childAnchors": []any{}},
			},
		},
	}
	h.SetNarratorSyncProvider(provider)

	h.handleMessage(c, map[string]any{"type": "subscribe", "narratorIds": []any{"narrator-race"}, "lastMessageId": "msg-0"})

	initial := readWSMessage(t, wsClient)
	if initial["type"] != "catch_up" || initial["narratorId"] != "narrator-race" || intNumber(initial["messageVersion"]) != 3 {
		t.Fatalf("expected initial reconnect catch_up version 3, got %#v", initial)
	}
	initialTop, _ := initial["topLevel"].([]any)
	if len(initialTop) != 1 || initialTop[0].(map[string]any)["id"] != "msg-1" {
		t.Fatalf("initial catch_up should include msg-1, got %#v", initial)
	}

	delta := readWSMessage(t, wsClient)
	if delta["type"] != "catch_up" || delta["narratorId"] != "narrator-race" || intNumber(delta["messageVersion"]) != 4 {
		t.Fatalf("expected race delta catch_up version 4, got %#v", delta)
	}
	deltaTop, _ := delta["topLevel"].([]any)
	if len(deltaTop) != 1 || deltaTop[0].(map[string]any)["id"] != "msg-2" {
		t.Fatalf("race delta catch_up should include msg-2, got %#v", delta)
	}
	if !h.isSubscribed(c, "narrator-race") {
		t.Fatal("client should stay subscribed after reconnect catch-up race handling")
	}
}

func TestFrontendUseNarratorWSContractSubscribeReplaysNKPQueueStatus(t *testing.T) {
	h, c, wsClient := newNarratorHubClient(t)
	defer wsClient.Close()

	h.BroadcastNarrator("narrator-queue", map[string]any{"type": "nkp_queue_status", "position": 3, "queueDepth": 9})
	h.handleMessage(c, map[string]any{"type": "subscribe", "narratorIds": []any{"narrator-queue"}})

	replay := readWSMessage(t, wsClient)
	if replay["type"] != "nkp_queue_status" || replay["narratorId"] != "narrator-queue" || intNumber(replay["position"]) != 3 || intNumber(replay["queueDepth"]) != 9 {
		t.Fatalf("unexpected nkp_queue_status replay payload: %#v", replay)
	}
}

func TestBroadcastUserSendsOnlyToMatchingNarratorConnections(t *testing.T) {
	h := NewHub()
	serverConnCh := make(chan *websocket.Conn, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := h.upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		serverConnCh <- conn
	}))
	defer server.Close()
	url := "ws" + strings.TrimPrefix(server.URL, "http")
	attach := func(userID string) *websocket.Conn {
		wsClient, _, err := websocket.DefaultDialer.Dial(url, nil)
		if err != nil {
			t.Fatalf("dial websocket: %v", err)
		}
		var serverConn *websocket.Conn
		select {
		case serverConn = <-serverConnCh:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for server websocket")
		}
		c := &client{
			conn:        serverConn,
			kind:        "narrator",
			userID:      userID,
			viewer:      viewerInfo{UserID: userID, Username: userID},
			subscribers: map[string]struct{}{},
		}
		h.add(c)
		t.Cleanup(func() {
			h.remove(c)
			_ = wsClient.Close()
		})
		return wsClient
	}
	userOne := attach("user-1")
	userTwo := attach("user-2")

	h.BroadcastUser("user-1", map[string]any{
		"type":     "user:recent_tabs_snapshot",
		"tabs":     []any{},
		"revision": int64(42),
	})

	msg := readWSMessage(t, userOne)
	if msg["type"] != "user:recent_tabs_snapshot" || msg["revision"] != float64(42) {
		t.Fatalf("unexpected user broadcast payload: %#v", msg)
	}
	_ = userTwo.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	if _, _, err := userTwo.ReadMessage(); err == nil {
		t.Fatal("broadcast for user-1 should not be delivered to user-2")
	}
}

func TestFrontendUseTerminalWSContractSubscribeInputResizeAndFanout(t *testing.T) {
	sqlDB := newTerminalTestDB(t)
	insertTerminal(t, sqlDB, "term-frontend", "running", nil)
	runtime := &fakeTerminalRuntime{snapshots: map[string]map[string]any{
		"term-frontend": {"data": "scrollback", "cols": 90, "rows": 30, "mouseTracking": false, "cursorVisible": true},
	}}
	h, c, wsClient := newTerminalHubClient(t, sqlDB, runtime)
	defer wsClient.Close()

	h.handleMessage(c, map[string]any{"type": "subscribe", "terminalIds": []any{"term-frontend"}})
	ready := readWSMessage(t, wsClient)
	if ready["type"] != "ready" || ready["terminalId"] != "term-frontend" || ready["status"] != "running" {
		t.Fatalf("unexpected ready payload: %#v", ready)
	}
	_ = readWSMessage(t, wsClient) // snapshot
	scrollback := readWSMessage(t, wsClient)
	if scrollback["type"] != "scrollback" || scrollback["data"] != "scrollback" || intNumber(scrollback["cols"]) != 90 || intNumber(scrollback["rows"]) != 30 {
		t.Fatalf("unexpected scrollback payload: %#v", scrollback)
	}
	bufferState := readWSMessage(t, wsClient)
	if bufferState["type"] != "bufferState" || bufferState["mouseTracking"] != false || bufferState["cursorVisible"] != true {
		t.Fatalf("unexpected bufferState payload: %#v", bufferState)
	}

	h.handleMessage(c, map[string]any{"type": "input", "terminalId": "term-frontend", "data": "pwd\n"})
	requestResize := readWSMessage(t, wsClient)
	if requestResize["type"] != "requestResize" || requestResize["terminalId"] != "term-frontend" {
		t.Fatalf("unexpected requestResize payload: %#v", requestResize)
	}
	h.handleMessage(c, map[string]any{"type": "resize", "terminalId": "term-frontend", "cols": float64(100), "rows": float64(40)})
	runtime.mu.Lock()
	writes := append([]fakeWriteCall{}, runtime.writes...)
	resizes := append([]fakeSizeCall{}, runtime.resizes...)
	runtime.mu.Unlock()
	if len(writes) != 1 || writes[0] != (fakeWriteCall{terminalID: "term-frontend", data: "pwd\n"}) {
		t.Fatalf("expected frontend input payload to call Write, got %#v", writes)
	}
	if len(resizes) != 1 || resizes[0] != (fakeSizeCall{terminalID: "term-frontend", cols: 100, rows: 40}) {
		t.Fatalf("expected frontend resize payload to call Resize, got %#v", resizes)
	}

	for _, payload := range []map[string]any{
		{"type": "output", "terminalId": "term-frontend", "data": "out"},
		{"type": "requestResize", "terminalId": "term-frontend"},
		{"type": "error", "terminalId": "term-frontend", "message": "boom"},
		{"type": "exit", "terminalId": "term-frontend", "code": 0},
	} {
		h.BroadcastTerminal("term-frontend", payload)
		msg := readWSMessage(t, wsClient)
		if msg["type"] != payload["type"] || msg["terminalId"] != "term-frontend" {
			t.Fatalf("unexpected terminal fanout for %#v: %#v", payload, msg)
		}
	}
}

func newNarratorHubClient(t *testing.T) (*Hub, *client, *websocket.Conn) {
	t.Helper()
	h := NewHub()
	serverConnCh := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := h.upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		serverConnCh <- conn
	}))
	t.Cleanup(server.Close)
	url := "ws" + strings.TrimPrefix(server.URL, "http")
	wsClient, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	var serverConn *websocket.Conn
	select {
	case serverConn = <-serverConnCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for server websocket")
	}
	c := &client{
		conn:        serverConn,
		kind:        "narrator",
		userID:      "user-1",
		viewer:      viewerInfo{UserID: "user-1", Username: "frontend"},
		subscribers: map[string]struct{}{},
	}
	h.add(c)
	t.Cleanup(func() { h.remove(c) })
	return h, c, wsClient
}
