package agentbasic

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestCodexDefaultInstructionsMatchesTSConstant pins the codex Responses default
// instructions fallback to the TS openai-provider CODEX_DEFAULT_INSTRUCTIONS
// constant verbatim. The codex gateway requires non-empty instructions even on
// pure tool-result turns; when the conversation carries no developer/system
// message, TS sends "You are NarraFork Narrator. Follow the user's request and
// answer concisely." Go previously sent a different "You are an AI coding agent
// working inside NarraFork." string. This covers both the HTTP Responses (SSE)
// path and the codex WebSocket path, plus the system-message override (which
// must NOT fall back to the default).
func TestCodexDefaultInstructionsMatchesTSConstant(t *testing.T) {
	const wantDefault = "You are NarraFork Narrator. Follow the user's request and answer concisely."
	if codexDefaultInstructions != wantDefault {
		t.Fatalf("codexDefaultInstructions = %q, want TS CODEX_DEFAULT_INSTRUCTIONS %q", codexDefaultInstructions, wantDefault)
	}

	t.Run("http responses path no system message uses TS default", func(t *testing.T) {
		var requestBody map[string]any
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&requestBody)
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\"}}\n\n")
		}))
		defer server.Close()
		// UseWebSocket=false forces the codex Kind path onto the HTTP /responses fallback.
		useWS := false
		adapter := &OpenAIChatCompletionsAdapter{Provider: Provider{Prefix: "codex", Kind: "codex", APIKey: "k", BaseURL: server.URL, Model: "gpt-5-codex", APIMode: "codex", UseWebSocket: &useWS}}
		drainChatStream(t, adapter)
		if got := requestBody["instructions"]; got != wantDefault {
			t.Fatalf("http responses default instructions = %#v, want %q", got, wantDefault)
		}
	})

	t.Run("http responses path with system message overrides default", func(t *testing.T) {
		var requestBody map[string]any
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&requestBody)
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\"}}\n\n")
		}))
		defer server.Close()
		useWS := false
		adapter := &OpenAIChatCompletionsAdapter{Provider: Provider{Prefix: "codex", Kind: "codex", APIKey: "k", BaseURL: server.URL, Model: "gpt-5-codex", APIMode: "codex", UseWebSocket: &useWS}}
		stream, err := adapter.Chat(context.Background(), ChatParams{History: &BuiltHistory{History: []any{
			Message{Role: "system", Content: "custom system instructions"},
			Message{Role: "user", Content: "hi"},
		}}})
		if err != nil {
			t.Fatalf("Chat error: %v", err)
		}
		for event := range stream {
			if event.Err != nil {
				t.Fatalf("stream error: %v", event.Err)
			}
		}
		if got := requestBody["instructions"]; got != "custom system instructions" {
			t.Fatalf("system message should override default, got %#v", got)
		}
	})

	t.Run("websocket path no system message uses TS default", func(t *testing.T) {
		bodyCh := make(chan map[string]any, 1)
		upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()
			var req map[string]any
			if err := conn.ReadJSON(&req); err != nil {
				return
			}
			bodyCh <- req
			_ = conn.WriteJSON(map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp_ws"}})
			_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
		}))
		defer server.Close()
		provider := Provider{Kind: "codex", APIKey: "tok", BaseURL: server.URL, Model: "gpt-5-codex"}
		out := make(chan StreamEvent, 8)
		go func() {
			_ = streamCodexResponsesWebSocket(context.Background(), provider, []Message{{Role: "user", Content: "hi"}}, "", "normal", out)
			close(out)
		}()
		for range out {
		}
		select {
		case body := <-bodyCh:
			if got := body["instructions"]; got != wantDefault {
				t.Fatalf("ws default instructions = %#v, want %q", got, wantDefault)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("did not capture ws response.create body")
		}
	})
}

func drainChatStream(t *testing.T, adapter *OpenAIChatCompletionsAdapter) {
	t.Helper()
	stream, err := adapter.Chat(context.Background(), ChatParams{History: &BuiltHistory{History: []any{Message{Role: "user", Content: "hi"}}}})
	if err != nil {
		t.Fatalf("Chat error: %v", err)
	}
	for event := range stream {
		if event.Err != nil && !strings.Contains(event.Err.Error(), "close") {
			t.Fatalf("stream error: %v", event.Err)
		}
	}
}
