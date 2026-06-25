package agentbasic

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestCodexWrappedFrameErrorClassifiesQuota verifies that a wrapped-error frame /
// close reason (quota/auth/limit envelope) is parsed into a classified error so
// the codex stream fails over instead of being treated as a clean finish.
func TestCodexWrappedFrameErrorClassifiesQuota(t *testing.T) {
	cases := []struct {
		name         string
		text         string
		wantNil      bool
		wantFailover bool
		wantContains string
	}{
		{
			name:         "quota envelope",
			text:         `{"type":"error","status":429,"error":{"code":"usage_limit_reached","message":"Usage limit reached"}}`,
			wantFailover: true,
			wantContains: "status=429",
		},
		{
			name:         "auth flat error",
			text:         `{"status_code":401,"message":"Unauthorized"}`,
			wantFailover: true,
			wantContains: "Unauthorized",
		},
		{
			name:         "close reason with text prefix then json",
			text:         `policy violation {"type":"error","error":{"code":"websocket_connection_limit_reached","message":"too many"}}`,
			wantFailover: false, // connection-limit is not a quota/auth/5xx failover by itself
			wantContains: "code=websocket_connection_limit_reached",
		},
		{
			name:    "plain text close reason (no envelope)",
			text:    "going away",
			wantNil: true,
		},
		{
			name:    "empty",
			text:    "",
			wantNil: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := codexWrappedFrameError(tc.text)
			if tc.wantNil {
				if err != nil {
					t.Fatalf("expected nil error, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error for %q", tc.text)
			}
			if tc.wantContains != "" && !strings.Contains(err.Error(), tc.wantContains) {
				t.Fatalf("error %q should contain %q", err.Error(), tc.wantContains)
			}
			if tc.wantFailover && !isCodexFailoverError(err) {
				t.Fatalf("error %q should be a codex failover error", err.Error())
			}
		})
	}
}

// TestCodexWebSocketCloseReasonExtractsCloseText verifies the close-frame reason
// text is surfaced from a gorilla *CloseError.
func TestCodexWebSocketCloseReasonExtractsCloseText(t *testing.T) {
	closeErr := &websocket.CloseError{Code: websocket.CloseInternalServerErr, Text: `{"type":"error","status":500}`}
	if got := codexWebSocketCloseReason(closeErr); got != `{"type":"error","status":500}` {
		t.Fatalf("unexpected close reason %q", got)
	}
	plain := errors.New("connection reset by peer")
	if got := codexWebSocketCloseReason(plain); got != "connection reset by peer" {
		t.Fatalf("non-close error should return raw message, got %q", got)
	}
}

// TestCodexWebSocketPrematureCloseEmitsSilentDisconnect is the CRITICAL parity
// regression test: a codex WebSocket that closes (normal closure) BEFORE
// response.completed must emit a silentDisconnect, NOT silently return success.
func TestCodexWebSocketPrematureCloseEmitsSilentDisconnect(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		// Read the request frame, stream a partial text delta, then close WITHOUT
		// sending response.completed.
		_, _, _ = conn.ReadMessage()
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.output_text.delta","delta":"partial","output_index":0}`))
		_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		time.Sleep(20 * time.Millisecond)
	}))
	defer server.Close()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	out := make(chan StreamEvent, 16)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	provider := Provider{Prefix: "codex", Kind: "codex", APIKey: "k", BaseURL: wsURL, Model: "gpt-5.3-codex", APIMode: "codex"}
	go func() {
		_ = streamCodexResponsesWebSocket(ctx, provider, nil, "", "normal", out)
		close(out)
	}()

	seenPartial := false
	seenSilent := false
	for event := range out {
		if event.Err != nil {
			t.Fatalf("unexpected stream error: %v", event.Err)
		}
		if event.Parsed.Text == "partial" {
			seenPartial = true
		}
		if event.Parsed.SilentDisconnect && event.Parsed.ResponsesAPI {
			seenSilent = true
		}
	}
	if !seenPartial || !seenSilent {
		t.Fatalf("premature codex WS close must emit partial+silentDisconnect, partial=%v silent=%v", seenPartial, seenSilent)
	}
}

// TestCodexWebSocketCompletedThenCloseIsSuccess verifies that after
// response.completed, a subsequent close is treated as a clean finish (no
// silentDisconnect, no error).
func TestCodexWebSocketCompletedThenCloseIsSuccess(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_, _, _ = conn.ReadMessage()
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.output_text.delta","delta":"done","output_index":0}`))
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.completed","response":{"id":"resp_done"}}`))
		_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		time.Sleep(20 * time.Millisecond)
	}))
	defer server.Close()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	out := make(chan StreamEvent, 16)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	provider := Provider{Prefix: "codex", Kind: "codex", APIKey: "k", BaseURL: wsURL, Model: "gpt-5.3-codex", APIMode: "codex"}
	var streamErr error
	go func() {
		streamErr = streamCodexResponsesWebSocket(ctx, provider, nil, "", "normal", out)
		close(out)
	}()

	seenSilent := false
	seenCompleted := false
	for event := range out {
		if event.Err != nil {
			t.Fatalf("unexpected stream error: %v", event.Err)
		}
		if event.Parsed.SilentDisconnect {
			seenSilent = true
		}
		if event.Parsed.ResponseID == "resp_done" {
			seenCompleted = true
		}
	}
	if streamErr != nil {
		t.Fatalf("completed-then-close should not error, got %v", streamErr)
	}
	if seenSilent {
		t.Fatalf("completed-then-close must NOT emit silentDisconnect")
	}
	if !seenCompleted {
		t.Fatalf("expected response.completed event with id resp_done")
	}
}

// TestCodexWebSocketErrorCloseTriggersFailover verifies that a close carrying a
// quota error envelope surfaces as a (failover) error rather than silentDisconnect.
func TestCodexWebSocketErrorCloseTriggersFailover(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_, _, _ = conn.ReadMessage()
		reason := `{"type":"error","status":429,"error":{"code":"usage_limit_reached","message":"quota"}}`
		_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, reason))
		time.Sleep(20 * time.Millisecond)
	}))
	defer server.Close()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	out := make(chan StreamEvent, 16)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	provider := Provider{Prefix: "codex", Kind: "codex", APIKey: "k", BaseURL: wsURL, Model: "gpt-5.3-codex", APIMode: "codex"}
	err := streamCodexResponsesWebSocket(ctx, provider, nil, "", "normal", out)
	close(out)
	for event := range out {
		if event.Parsed.SilentDisconnect {
			t.Fatalf("error close must NOT emit silentDisconnect")
		}
	}
	if err == nil {
		t.Fatalf("expected error from quota close reason")
	}
	if !isCodexFailoverError(err) {
		t.Fatalf("quota close error should be a failover error, got %v", err)
	}
}
