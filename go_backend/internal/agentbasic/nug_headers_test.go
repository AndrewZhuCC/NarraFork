package agentbasic

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// captureNugRequestHeader drives a NUG provider chat against a scripted server
// and returns the captured request headers from the first request.
func captureNugRequestHeader(t *testing.T, apiMode string, params ChatParams) http.Header {
	t.Helper()
	captured := make(chan http.Header, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip the NUG usage-summary GET (emitNugUsageSummary) which precedes the
		// chat request and does not carry the chat headers.
		if r.URL.Path == "/v1/usage/summary" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		select {
		case captured <- r.Header.Clone():
		default:
		}
		w.Header().Set("Content-Type", "text/event-stream")
		// Minimal terminal event for each delegate's SSE parser.
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)

	provider := Provider{Prefix: "nug", Kind: "nug", ID: "nug-prov-1", APIKey: "k", BaseURL: server.URL, Model: "test-model", APIMode: apiMode}
	adapter := &NugProviderAdapter{Provider: provider}
	if params.History == nil {
		params.History = &BuiltHistory{History: []any{Message{Role: "user", Content: "hi"}}}
	}
	stream, err := adapter.Chat(context.Background(), params)
	if err != nil {
		t.Fatalf("Chat error: %v", err)
	}
	for event := range stream {
		_ = event
	}
	select {
	case h := <-captured:
		return h
	default:
		t.Fatal("no request captured")
		return nil
	}
}

// TestNugChatInjectsModelHashHeaderAllChannels verifies X-NUG-Model-Hash is sent
// on every NUG channel (mirrors TS createDelegate extraHeaders). With no cache
// file the value falls back to "none".
func TestNugChatInjectsModelHashHeaderAllChannels(t *testing.T) {
	// Isolate HOME so no real cache file is read; expect "none".
	t.Setenv("HOME", t.TempDir())
	for _, apiMode := range []string{"completions", "anthropic", "kiro"} {
		t.Run(apiMode, func(t *testing.T) {
			headers := captureNugRequestHeader(t, apiMode, ChatParams{})
			if got := headers.Get("X-NUG-Model-Hash"); got != "none" {
				t.Fatalf("channel %q must send X-NUG-Model-Hash=none (no cache), got %q", apiMode, got)
			}
		})
	}
}

// TestNugKiroChannelInjectsConversationIdHeader verifies the kiro native channel
// sends X-Conversation-ID (mirrors TS chatHeaders, used only on the kiro path).
func TestNugKiroChannelInjectsConversationIdHeader(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	headers := captureNugRequestHeader(t, "kiro", ChatParams{ConversationID: "conv-1", History: &BuiltHistory{History: []any{Message{Role: "user", Content: "hi"}}}})
	if got := headers.Get("X-Conversation-ID"); got != "conv-1" {
		t.Fatalf("kiro channel must send X-Conversation-ID=conv-1, got %q", got)
	}
}

// TestNugNonKiroChannelOmitsConversationId verifies non-kiro delegates do NOT
// send X-Conversation-ID (mirrors TS: non-kiro delegate extraHeaders only carry
// the model hash).
func TestNugNonKiroChannelOmitsConversationId(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	headers := captureNugRequestHeader(t, "completions", ChatParams{ConversationID: "conv-1", History: &BuiltHistory{History: []any{Message{Role: "user", Content: "hi"}}}})
	if got := headers.Get("X-Conversation-ID"); got != "" {
		t.Fatalf("non-kiro channel must NOT send X-Conversation-ID, got %q", got)
	}
}

// TestNugCachedModelHashReadsCacheFile verifies the cache reader returns the
// stored hash and falls back to "none" when absent.
func TestNugCachedModelHashReadsCacheFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if got := nugCachedModelHash("nug-prov-1"); got != "none" {
		t.Fatalf("missing cache file should yield none, got %q", got)
	}
	if got := nugCachedModelHash(""); got != "none" {
		t.Fatalf("empty id should yield none, got %q", got)
	}

	dir := filepath.Join(home, ".narrafork")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	cache := map[string]any{"providers": map[string]any{"nug-prov-1": map[string]any{"modelHash": "abc123"}}}
	data, _ := json.Marshal(cache)
	if err := os.WriteFile(filepath.Join(dir, "nug-models-providers.json"), data, 0o644); err != nil {
		t.Fatalf("write cache: %v", err)
	}
	if got := nugCachedModelHash("nug-prov-1"); got != "abc123" {
		t.Fatalf("cached hash should be abc123, got %q", got)
	}
	if got := nugCachedModelHash("other"); got != "none" {
		t.Fatalf("uncached id should yield none, got %q", got)
	}
}

// captureNugRequestBody drives a NUG provider chat against a scripted server and
// returns the decoded JSON body of the first non-usage-summary request.
func captureNugRequestBody(t *testing.T, provider Provider, params ChatParams) map[string]any {
	t.Helper()
	captured := make(chan []byte, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/usage/summary" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		body, _ := io.ReadAll(r.Body)
		select {
		case captured <- body:
		default:
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)

	provider.BaseURL = server.URL
	adapter := &NugProviderAdapter{Provider: provider}
	if params.History == nil {
		params.History = &BuiltHistory{History: []any{Message{Role: "user", Content: "hi"}}}
	}
	stream, err := adapter.Chat(context.Background(), params)
	if err != nil {
		t.Fatalf("Chat error: %v", err)
	}
	for event := range stream {
		_ = event
	}
	select {
	case raw := <-captured:
		decoded := map[string]any{}
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatalf("decode request body: %v (raw=%s)", err, raw)
		}
		return decoded
	default:
		t.Fatal("no request captured")
		return nil
	}
}

// TestNugKiroChannelWrapsBodyWithTopLevelModel pins the fix for the NUG gateway
// "missing model field" error: the kiro channel must wrap the kiro
// conversationState body with a top-level "model" field set to the
// channel-prefixed routedModel, while the internal userInputMessage.modelId uses
// the bare model. Mirrors TS nug-provider.ts chatKiro.
func TestNugKiroChannelWrapsBodyWithTopLevelModel(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	provider := Provider{
		Prefix: "nug", Kind: "nug", ID: "nug-prov-1", APIKey: "k",
		Model: "kiro:claude-opus-4.8", APIMode: "kiro",
	}
	body := captureNugRequestBody(t, provider, ChatParams{ConversationID: "conv-1"})

	if got, _ := body["model"].(string); got != "kiro:claude-opus-4.8" {
		t.Fatalf("kiro body must carry top-level model=kiro:claude-opus-4.8, got %q", got)
	}
	state, _ := body["conversationState"].(map[string]any)
	current, _ := state["currentMessage"].(map[string]any)
	userInput, _ := current["userInputMessage"].(map[string]any)
	if got, _ := userInput["modelId"].(string); got != "claude-opus-4.8" {
		t.Fatalf("kiro userInputMessage.modelId must be bare model claude-opus-4.8, got %q", got)
	}
}

// TestNugNonKiroChannelOmitsTopLevelModelWrap verifies non-kiro channels do not
// get the kiro top-level-model wrap (their delegates set the model themselves in
// their own request body shape).
func TestNugNonKiroChannelOmitsTopLevelModelWrap(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	provider := Provider{
		Prefix: "nug", Kind: "nug", ID: "nug-prov-1", APIKey: "k",
		Model: "completions-model", APIMode: "completions",
	}
	body := captureNugRequestBody(t, provider, ChatParams{})
	// The completions delegate body has no conversationState; its model field is
	// the delegate's own (provider.Model), not a kiro routedModel wrap.
	if _, ok := body["conversationState"]; ok {
		t.Fatal("completions channel must not produce a kiro conversationState body")
	}
}
