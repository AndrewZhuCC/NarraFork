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
		fmt.Fprint(w, `data: {"type":"response.output_item.added","output_index":2,"item":{"type":"web_search_call","id":"ws_1","status":"completed","query":"golang"}}

`)
		fmt.Fprint(w, `data: {"type":"response.output_item.added","output_index":3,"item":{"type":"image_generation_call","id":"img_1","status":"completed","revised_prompt":"draw","result":"base64"}}

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
		}
		if event.Parsed.ImageGeneration != nil && event.Parsed.ImageGeneration.ID == "img_1" {
			seenImage = true
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
	if usage == nil || usage.PromptTokens != 5 || usage.CompletionTokens != 7 || usage.ReasoningTokens != 2 || usage.CachedInputTokens != 1 {
		t.Fatalf("unexpected usage: %#v", usage)
	}
	if requestBody["stream"] != true || requestBody["input"] == nil || requestBody["tools"] == nil {
		t.Fatalf("unexpected responses request body: %#v", requestBody)
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
