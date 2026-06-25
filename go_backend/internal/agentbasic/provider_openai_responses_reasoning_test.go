package agentbasic

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// startResponsesReasoningServer returns an httptest server that streams the
// supplied SSE event lines (each already a full `data: {...}` JSON object) and a
// trailing [DONE], for exercising the Responses reasoning capture path.
func startResponsesReasoningServer(t *testing.T, events []string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, ev := range events {
			fmt.Fprintf(w, "data: %s\n\n", ev)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
}

func collectResponsesEvents(t *testing.T, server *httptest.Server) []ParsedStreamEvent {
	t.Helper()
	adapter := &OpenAIChatCompletionsAdapter{Provider: Provider{Prefix: "openai", Kind: "openai", APIKey: "k", BaseURL: server.URL, Model: "gpt-5-codex", APIMode: "responses"}}
	stream, err := adapter.Chat(context.Background(), ChatParams{History: &BuiltHistory{History: []any{Message{Role: "user", Content: "hi"}}}, ToolScope: "normal"})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	var events []ParsedStreamEvent
	for event := range stream {
		if event.Err != nil {
			t.Fatalf("stream error: %v", event.Err)
		}
		events = append(events, event.Parsed)
	}
	return events
}

// encryptedReasoningContent extracts providerMetadata.openai.reasoningEncryptedContent
// from a parsed event, or "" when absent.
func encryptedReasoningContent(p ParsedStreamEvent) string {
	if p.ReasoningMetadata == nil {
		return ""
	}
	openai, _ := p.ReasoningMetadata["openai"].(map[string]any)
	enc, _ := openai["reasoningEncryptedContent"].(string)
	return enc
}

// TestOpenAIResponsesCapturesEncryptedReasoningContent verifies the full capture
// chain: output_item.added(reasoning) records itemId+encrypted_content,
// reasoning deltas carry that metadata, and output_item.done(reasoning) updates
// to the final encrypted_content and emits a metadata-only event. Previously Go
// requested reasoning.encrypted_content and knew how to replay it, but never
// captured it from the stream, so encrypted reasoning continuation was broken.
func TestOpenAIResponsesCapturesEncryptedReasoningContent(t *testing.T) {
	events := []string{
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"rs_1","encrypted_content":"enc_initial"}}`,
		`{"type":"response.reasoning_text.delta","delta":"thinking...","output_index":0}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"type":"reasoning","id":"rs_1","encrypted_content":"enc_final"}}`,
		`{"type":"response.completed","response":{"id":"resp_1","output":[],"usage":{"input_tokens":1,"output_tokens":1}}}`,
	}
	server := startResponsesReasoningServer(t, events)
	defer server.Close()
	parsed := collectResponsesEvents(t, server)

	var sawDeltaWithMetadata bool
	var finalEncrypted string
	for _, p := range parsed {
		if p.Reasoning == "thinking..." {
			// Delta carries the initial captured encrypted_content.
			if enc := encryptedReasoningContent(p); enc != "enc_initial" {
				t.Fatalf("reasoning delta should carry initial encrypted_content, got %q", enc)
			}
			sawDeltaWithMetadata = true
		}
		if p.Reasoning == "" && encryptedReasoningContent(p) != "" {
			finalEncrypted = encryptedReasoningContent(p)
		}
	}
	if !sawDeltaWithMetadata {
		t.Fatal("expected a reasoning delta carrying encrypted_content metadata")
	}
	if finalEncrypted != "enc_final" {
		t.Fatalf("expected metadata-only done event with final encrypted_content enc_final, got %q", finalEncrypted)
	}
}

// TestOpenAIResponsesReasoningDoneEmitsWhenNoDelta verifies a provider that
// streams reasoning text only via the .done event (no deltas) still surfaces the
// reasoning, mirroring TS emitOnlyIfAccumulatorEmpty.
func TestOpenAIResponsesReasoningDoneEmitsWhenNoDelta(t *testing.T) {
	events := []string{
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"rs_1","encrypted_content":"enc_x"}}`,
		`{"type":"response.reasoning_text.done","text":"final reasoning","output_index":0}`,
		`{"type":"response.completed","response":{"id":"resp_1","output":[],"usage":{"input_tokens":1,"output_tokens":1}}}`,
	}
	server := startResponsesReasoningServer(t, events)
	defer server.Close()
	parsed := collectResponsesEvents(t, server)

	var sawDoneText bool
	for _, p := range parsed {
		if p.Reasoning == "final reasoning" {
			sawDoneText = true
			if enc := encryptedReasoningContent(p); enc != "enc_x" {
				t.Fatalf("done-only reasoning should carry encrypted_content, got %q", enc)
			}
		}
	}
	if !sawDoneText {
		t.Fatal("expected reasoning text from .done event when no delta preceded it")
	}
}

// TestOpenAIResponsesReasoningDoneSkippedWhenDeltaPresent verifies the .done
// event does NOT re-emit reasoning text when deltas already streamed it,
// mirroring TS emitOnlyIfAccumulatorEmpty (avoiding duplicate reasoning).
func TestOpenAIResponsesReasoningDoneSkippedWhenDeltaPresent(t *testing.T) {
	events := []string{
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"rs_1"}}`,
		`{"type":"response.reasoning_text.delta","delta":"partial","output_index":0}`,
		`{"type":"response.reasoning_text.done","text":"partial full text","output_index":0}`,
		`{"type":"response.completed","response":{"id":"resp_1","output":[],"usage":{"input_tokens":1,"output_tokens":1}}}`,
	}
	server := startResponsesReasoningServer(t, events)
	defer server.Close()
	parsed := collectResponsesEvents(t, server)

	reasoningTexts := []string{}
	for _, p := range parsed {
		if p.Reasoning != "" {
			reasoningTexts = append(reasoningTexts, p.Reasoning)
		}
	}
	if len(reasoningTexts) != 1 || reasoningTexts[0] != "partial" {
		t.Fatalf("expected only the delta reasoning 'partial' (no .done re-emit), got %#v", reasoningTexts)
	}
}

// TestCodexWebSocketCapturesEncryptedReasoningContent verifies the Codex
// WebSocket path (handleResponsesJSONEvent) captures encrypted reasoning
// identically to the SSE path: it shares handleResponsesReasoningEvent, so
// output_item.added/done(reasoning) record + finalize encrypted_content and
// deltas carry the metadata. This path is the primary one for codex models and
// relies on encrypted_content for previous_response_id cross-turn continuity.
func TestCodexWebSocketCapturesEncryptedReasoningContent(t *testing.T) {
	out := make(chan StreamEvent, 16)
	toolsByItem := map[string]*openAIStreamingTool{}
	reasoningByIndex := map[int]*responsesReasoningAccum{}
	inputs := []string{
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"rs_ws","encrypted_content":"ws_initial"}}`,
		`{"type":"response.reasoning_text.delta","delta":"ws thinking","output_index":0}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"type":"reasoning","id":"rs_ws","encrypted_content":"ws_final"}}`,
	}
	for _, input := range inputs {
		if err := handleResponsesJSONEvent(context.Background(), out, toolsByItem, reasoningByIndex, []byte(input)); err != nil {
			t.Fatalf("handle websocket responses event: %v", err)
		}
	}
	close(out)

	var sawDeltaMeta bool
	var finalEncrypted string
	for event := range out {
		p := event.Parsed
		if p.Reasoning == "ws thinking" && encryptedReasoningContent(p) == "ws_initial" {
			sawDeltaMeta = true
		}
		if p.Reasoning == "" && encryptedReasoningContent(p) != "" {
			finalEncrypted = encryptedReasoningContent(p)
		}
	}
	if !sawDeltaMeta {
		t.Fatal("expected WS reasoning delta carrying initial encrypted_content")
	}
	if finalEncrypted != "ws_final" {
		t.Fatalf("expected WS metadata-only done event with final encrypted_content ws_final, got %q", finalEncrypted)
	}
}

// TestCodexWebSocketReasoningDoneEmitsWhenNoDelta verifies the WS path also
// surfaces reasoning when the provider streams only the .done full text.
func TestCodexWebSocketReasoningDoneEmitsWhenNoDelta(t *testing.T) {
	out := make(chan StreamEvent, 16)
	toolsByItem := map[string]*openAIStreamingTool{}
	reasoningByIndex := map[int]*responsesReasoningAccum{}
	inputs := []string{
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"rs_ws","encrypted_content":"ws_enc"}}`,
		`{"type":"response.reasoning_text.done","text":"ws final reasoning","output_index":0}`,
	}
	for _, input := range inputs {
		if err := handleResponsesJSONEvent(context.Background(), out, toolsByItem, reasoningByIndex, []byte(input)); err != nil {
			t.Fatalf("handle websocket responses event: %v", err)
		}
	}
	close(out)

	var sawDoneText bool
	for event := range out {
		if event.Parsed.Reasoning == "ws final reasoning" && encryptedReasoningContent(event.Parsed) == "ws_enc" {
			sawDoneText = true
		}
	}
	if !sawDoneText {
		t.Fatal("expected WS reasoning text from .done event when no delta preceded it")
	}
}
