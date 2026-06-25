package agentbasic

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestKiroNormalStreamCloseIsNotSilentDisconnect locks the core Kiro completion
// contract: a normal Kiro SSE response (assistantResponseEvent chunks) that ends
// by simply closing the stream WITHOUT a [DONE]/message_stop/stopReason terminal
// marker must be treated as a successful completion, NOT a silent disconnect.
// TS kiro-protocol.ts reads until the stream ends and never emits silentDisconnect;
// the real CodeWhisperer/Kiro protocol signals completion by closing the stream.
// Regressing this made every Kiro turn abort via the loop's errTerminalEvent path.
func TestKiroNormalStreamCloseIsNotSilentDisconnect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: assistantResponseEvent\ndata: {\"content\":\"Here is my answer\",\"messageId\":\"msg_1\"}\n\n")
		fmt.Fprint(w, "event: contextUsageEvent\ndata: {\"contextUsagePercentage\":5}\n\n")
		// Stream closes here with NO terminal marker — this is how real Kiro ends.
	}))
	defer server.Close()
	adapter := &KiroProviderAdapter{Provider: Provider{Prefix: "kiro", Kind: "kiro", APIKey: "kiro-token", BaseURL: server.URL, Model: "claude-sonnet-4.5"}}
	stream, err := adapter.Chat(context.Background(), ChatParams{History: &BuiltHistory{TrailingUserText: "hi"}, Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	silentDisconnect := false
	sawText := false
	for event := range stream {
		if event.Err != nil {
			t.Fatalf("stream error: %v", event.Err)
		}
		if event.Parsed.SilentDisconnect {
			silentDisconnect = true
		}
		if event.Parsed.Text == "Here is my answer" {
			sawText = true
		}
	}
	if !sawText {
		t.Fatalf("expected to see assistant text")
	}
	if silentDisconnect {
		t.Fatalf("normal Kiro stream close must not emit silent_disconnect (TS treats stream close as completion)")
	}
}
