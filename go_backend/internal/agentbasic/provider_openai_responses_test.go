package agentbasic

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAIResponsesStreamsTextReasoningToolAndUsage(t *testing.T) {
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if !strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
			t.Fatalf("expected SSE accept header, got %q", r.Header.Get("Accept"))
		}
		_ = json.NewDecoder(r.Body).Decode(&requestBody)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"type":"response.output_text.delta","delta":"hi","output_index":0}

`)
		fmt.Fprint(w, `data: {"type":"response.reasoning_summary_text.delta","delta":"thought","output_index":0}

`)
		fmt.Fprint(w, `data: {"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","id":"fc_1","call_id":"call_read","name":"Read"}}

`)
		fmt.Fprint(w, `data: {"type":"response.function_call_arguments.delta","item_id":"fc_1","output_index":1,"delta":"{\"file_path\":\"note.txt\"}"}

`)
		fmt.Fprint(w, `data: {"type":"response.function_call_arguments.done","item_id":"fc_1","output_index":1}

`)
		fmt.Fprint(w, `data: {"type":"response.output_item.added","output_index":2,"item":{"type":"web_search_call","id":"ws_1"}}

`)
		fmt.Fprint(w, `data: {"type":"response.web_search_call.searching","item_id":"ws_1","output_index":2}

`)
		fmt.Fprint(w, `data: {"type":"response.web_search_call.completed","item_id":"ws_1","output_index":2}

`)
		fmt.Fprint(w, `data: {"type":"response.output_item.done","output_index":2,"item":{"type":"web_search_call","id":"ws_1","action":{"type":"search","query":"golang","queries":["golang","go backend"]}}}

`)
		fmt.Fprint(w, `data: {"type":"response.output_item.added","output_index":3,"item":{"type":"image_generation_call","id":"img_1"}}

`)
		fmt.Fprint(w, `data: {"type":"response.image_generation_call.generating","item_id":"img_1","output_index":3}

`)
		fmt.Fprint(w, `data: {"type":"response.image_generation_call.completed","item_id":"img_1","output_index":3}

`)
		fmt.Fprint(w, `data: {"type":"response.output_item.done","output_index":3,"item":{"type":"image_generation_call","id":"img_1","status":"completed","revised_prompt":"draw","result":"base64"}}

`)
		fmt.Fprint(w, `data: {"type":"response.completed","response":{"id":"resp_1","output":[{"type":"message","content":[{"type":"output_text","text":" completed"}]},{"type":"function_call","call_id":"call_done","name":"Read","arguments":"{\"file_path\":\"done.txt\"}"}],"usage":{"input_tokens":5,"output_tokens":7,"output_tokens_details":{"reasoning_tokens":2},"input_tokens_details":{"cached_tokens":1}}}}

`)
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()
	adapter := &OpenAIChatCompletionsAdapter{Provider: Provider{Prefix: "openai", Kind: "openai", APIKey: "test-key", BaseURL: server.URL, Model: "gpt-test", APIMode: "responses"}}
	stream, err := adapter.Chat(context.Background(), ChatParams{History: &BuiltHistory{History: []any{Message{Role: "user", Content: "hi"}}}, ToolScope: "normal"})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	seenText := false
	seenReasoning := false
	seenStop := false
	seenWeb := false
	seenImage := false
	seenCompletedText := false
	seenCompletedTool := false
	webStatuses := []string{}
	imageStatuses := []string{}
	var finalWeb *WebSearchEvent
	var finalImage *ImageGenerationEvent
	var usage *UsageSnapshot
	for event := range stream {
		if event.Err != nil {
			t.Fatalf("stream error: %v", event.Err)
		}
		if event.Parsed.Text == "hi" && event.Parsed.ResponsesAPI {
			seenText = true
		}
		if event.Parsed.Reasoning == "thought" {
			seenReasoning = true
		}
		if event.Parsed.ToolUseChunk != nil && event.Parsed.ToolUseChunk.Stop && event.Parsed.ToolUseChunk.ToolUseID == "call_read" {
			seenStop = true
		}
		if event.Parsed.WebSearch != nil && event.Parsed.WebSearch.ID == "ws_1" {
			seenWeb = true
			webStatuses = append(webStatuses, event.Parsed.WebSearch.Status)
			if event.Parsed.WebSearch.Final {
				finalWeb = event.Parsed.WebSearch
			}
		}
		if event.Parsed.ImageGeneration != nil && event.Parsed.ImageGeneration.ID == "img_1" {
			seenImage = true
			imageStatuses = append(imageStatuses, event.Parsed.ImageGeneration.Status)
			if event.Parsed.ImageGeneration.Final {
				finalImage = event.Parsed.ImageGeneration
			}
		}
		if event.Parsed.Text == " completed" && event.Parsed.ResponseID == "resp_1" {
			seenCompletedText = true
		}
		for _, call := range event.Parsed.ToolUses {
			if call.ID == "call_done" && call.Function.Name == "Read" {
				seenCompletedTool = true
			}
		}
		if event.Parsed.Usage != nil {
			usage = event.Parsed.Usage
		}
	}
	if !seenText || !seenReasoning || !seenStop || !seenWeb || !seenImage || !seenCompletedText || !seenCompletedTool {
		t.Fatalf("missing expected events text=%v reasoning=%v stop=%v web=%v image=%v completedText=%v completedTool=%v", seenText, seenReasoning, seenStop, seenWeb, seenImage, seenCompletedText, seenCompletedTool)
	}
	if strings.Join(webStatuses, ",") != "in_progress,searching,completed,completed" || finalWeb == nil || finalWeb.Query != "golang" || len(finalWeb.Queries) != 2 || finalWeb.Action["type"] != "search" {
		t.Fatalf("unexpected web search lifecycle statuses=%#v final=%#v", webStatuses, finalWeb)
	}
	if strings.Join(imageStatuses, ",") != "in_progress,generating,completed,completed" || finalImage == nil || finalImage.RevisedPrompt != "draw" || finalImage.Result != "base64" {
		t.Fatalf("unexpected image generation lifecycle statuses=%#v final=%#v", imageStatuses, finalImage)
	}
	if usage == nil || usage.PromptTokens != 5 || usage.CompletionTokens != 7 || usage.ReasoningTokens != 2 || usage.CachedInputTokens != 1 {
		t.Fatalf("unexpected usage: %#v", usage)
	}
	if requestBody["stream"] != true || requestBody["input"] == nil || requestBody["tools"] == nil {
		t.Fatalf("unexpected responses request body: %#v", requestBody)
	}
}

func TestCodexResponsesWebSocketParserNativeToolLifecycle(t *testing.T) {
	out := make(chan StreamEvent, 16)
	toolsByItem := map[string]*openAIStreamingTool{}
	inputs := []string{
		`{"type":"response.output_item.added","output_index":4,"item":{"type":"web_search_call","id":"ws_ws"}}`,
		`{"type":"response.web_search_call.searching","item_id":"ws_ws","output_index":4}`,
		`{"type":"response.output_item.done","output_index":4,"item":{"type":"web_search_call","id":"ws_ws","action":{"type":"search","query":"codex ws","queries":["codex ws"]}}}`,
		`{"type":"response.output_item.added","output_index":5,"item":{"type":"image_generation_call","id":"img_ws"}}`,
		`{"type":"response.image_generation_call.generating","item_id":"img_ws","output_index":5}`,
		`{"type":"response.output_item.done","output_index":5,"item":{"type":"image_generation_call","id":"img_ws","status":"completed","revised_prompt":"draw ws","result":"base64-ws"}}`,
	}
	for _, input := range inputs {
		if err := handleResponsesJSONEvent(context.Background(), out, toolsByItem, []byte(input)); err != nil {
			t.Fatalf("handle websocket responses event: %v", err)
		}
	}
	close(out)

	webStatuses := []string{}
	imageStatuses := []string{}
	var finalWeb *WebSearchEvent
	var finalImage *ImageGenerationEvent
	for event := range out {
		if event.Err != nil {
			t.Fatalf("unexpected stream error: %v", event.Err)
		}
		if ws := event.Parsed.WebSearch; ws != nil && ws.ID == "ws_ws" {
			webStatuses = append(webStatuses, ws.Status)
			if ws.Final {
				finalWeb = ws
			}
		}
		if image := event.Parsed.ImageGeneration; image != nil && image.ID == "img_ws" {
			imageStatuses = append(imageStatuses, image.Status)
			if image.Final {
				finalImage = image
			}
		}
	}
	if strings.Join(webStatuses, ",") != "in_progress,searching,completed" || finalWeb == nil || finalWeb.Query != "codex ws" || len(finalWeb.Queries) != 1 {
		t.Fatalf("unexpected websocket web-search lifecycle statuses=%#v final=%#v", webStatuses, finalWeb)
	}
	if strings.Join(imageStatuses, ",") != "in_progress,generating,completed" || finalImage == nil || finalImage.RevisedPrompt != "draw ws" || finalImage.Result != "base64-ws" {
		t.Fatalf("unexpected websocket image-generation lifecycle statuses=%#v final=%#v", imageStatuses, finalImage)
	}
}

func TestOpenAIResponsesEmitsSilentDisconnectWithoutCompleted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"type":"response.output_text.delta","delta":"partial"}

`)
	}))
	defer server.Close()
	adapter := &OpenAIChatCompletionsAdapter{Provider: Provider{Prefix: "openai", Kind: "openai", APIKey: "test-key", BaseURL: server.URL, Model: "gpt-test", APIMode: "responses"}}
	stream, err := adapter.Chat(context.Background(), ChatParams{History: &BuiltHistory{History: []any{Message{Role: "user", Content: "hi"}}}})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	seenPartial := false
	seenSilent := false
	for event := range stream {
		if event.Err != nil {
			t.Fatalf("stream error: %v", event.Err)
		}
		if event.Parsed.Text == "partial" {
			seenPartial = true
		}
		if event.Parsed.SilentDisconnect && event.Parsed.ResponsesAPI {
			seenSilent = true
		}
	}
	if !seenPartial || !seenSilent {
		t.Fatalf("expected partial responses text and silent disconnect, partial=%v silent=%v", seenPartial, seenSilent)
	}
}

func TestOpenAIResponsesSendsPreviousResponseID(t *testing.T) {
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&requestBody)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"type":"response.output_text.delta","delta":"stateful"}

`)
		fmt.Fprint(w, `data: {"type":"response.completed","response":{"id":"resp_next"}}

`)
	}))
	defer server.Close()
	adapter := &OpenAIChatCompletionsAdapter{Provider: Provider{Prefix: "openai", Kind: "openai", APIKey: "test-key", BaseURL: server.URL, Model: "gpt-test", APIMode: "responses", PreviousResponseID: "resp_prev"}}
	stream, err := adapter.Chat(context.Background(), ChatParams{History: &BuiltHistory{History: []any{Message{Role: "user", Content: "continue"}}}})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	for event := range stream {
		if event.Err != nil {
			t.Fatalf("stream error: %v", event.Err)
		}
	}
	if requestBody["previous_response_id"] != "resp_prev" {
		t.Fatalf("expected previous_response_id in body, got %#v", requestBody)
	}
}

func TestOpenAIResponsesInputReplaysAssistantFunctionCallsAndToolOutputs(t *testing.T) {
	call := newFunctionToolCall("call_1", "Read", `{"file_path":"note.txt"}`)
	input := openAIResponsesInput([]Message{
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{call}},
		{Role: "tool", ToolCallID: "call_1", Content: "tool output"},
	})
	if len(input) != 2 {
		t.Fatalf("expected function call and output items, got %#v", input)
	}
	fn, _ := input[0].(map[string]any)
	if fn["type"] != "function_call" || fn["call_id"] != "call_1" || fn["name"] != "Read" {
		t.Fatalf("unexpected function call replay item: %#v", fn)
	}
	out, _ := input[1].(map[string]any)
	if out["type"] != "function_call_output" || out["call_id"] != "call_1" || out["output"] != "tool output" {
		t.Fatalf("unexpected tool output replay item: %#v", out)
	}
}

func TestResolveCustomResponsesProviderUsesResponsesMode(t *testing.T) {
	store := testConfigStore(t, map[string]any{
		"customApiProviders": []any{map[string]any{
			"prefix":       "custom",
			"protocol":     "responses-compatible",
			"apiKey":       "test-key",
			"baseUrl":      "https://example.invalid/v1",
			"defaultModel": "gpt-test",
		}},
	})
	resolution, err := ResolveProviderAndModel(store, "custom:gpt-test")
	if err != nil {
		t.Fatalf("ResolveProviderAndModel returned error: %v", err)
	}
	if resolution.Config.APIMode != "responses" {
		t.Fatalf("expected responses api mode, got %#v", resolution.Config)
	}
}

// Wave 312: response.image_generation_call.partial_image must carry the
// partialImageIndex/partialImageB64 streaming preview fields (TS parity), while
// generating/completed events must not, on both the OpenAI Responses SSE parser
// and the Codex WebSocket parser (they share handleResponsesJSONEvent).
func TestResponsesImageGenerationPartialStreamingFieldsParity(t *testing.T) {
	out := make(chan StreamEvent, 16)
	toolsByItem := map[string]*openAIStreamingTool{}
	inputs := []string{
		`{"type":"response.image_generation_call.generating","item_id":"img_partial","output_index":2}`,
		`{"type":"response.image_generation_call.partial_image","item_id":"img_partial","output_index":2,"partial_image_index":0,"partial_image_b64":"AAAA"}`,
		`{"type":"response.image_generation_call.partial_image","item_id":"img_partial","output_index":2,"partial_image_index":1,"partial_image_b64":"BBBB"}`,
		`{"type":"response.image_generation_call.completed","item_id":"img_partial","output_index":2}`,
	}
	for _, input := range inputs {
		if err := handleResponsesJSONEvent(context.Background(), out, toolsByItem, []byte(input)); err != nil {
			t.Fatalf("handle responses event: %v", err)
		}
	}
	close(out)

	type sample struct {
		status     string
		hasPartial bool
		index      int
		b64        string
	}
	var samples []sample
	for event := range out {
		if event.Err != nil {
			t.Fatalf("unexpected stream error: %v", event.Err)
		}
		img := event.Parsed.ImageGeneration
		if img == nil || img.ID != "img_partial" {
			continue
		}
		s := sample{status: img.Status}
		if img.PartialImageIndex != nil {
			s.hasPartial = true
			s.index = *img.PartialImageIndex
			s.b64 = img.PartialImageB64
		}
		samples = append(samples, s)
	}
	if len(samples) != 4 {
		t.Fatalf("expected 4 image-generation events, got %d: %#v", len(samples), samples)
	}
	if samples[0].status != "generating" || samples[0].hasPartial {
		t.Fatalf("generating event must not carry partial fields: %#v", samples[0])
	}
	if samples[1].status != "generating" || !samples[1].hasPartial || samples[1].index != 0 || samples[1].b64 != "AAAA" {
		t.Fatalf("first partial_image event wrong: %#v", samples[1])
	}
	if samples[2].status != "generating" || !samples[2].hasPartial || samples[2].index != 1 || samples[2].b64 != "BBBB" {
		t.Fatalf("second partial_image event wrong: %#v", samples[2])
	}
	if samples[3].status != "completed" || samples[3].hasPartial {
		t.Fatalf("completed event must not carry partial fields: %#v", samples[3])
	}
}
