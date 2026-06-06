package agentbasic

import "testing"

func TestTSParityFixtureOpenAIResponsesRequestShape(t *testing.T) {
	input, instructions := openAIResponsesInputAndInstructions([]Message{
		{Role: "system", Content: "system instructions"},
		{Role: "user", Content: "hello"},
		{Role: "assistant", ToolCalls: []ToolCall{newFunctionToolCall("call_1", "Read", `{"file_path":"a.txt"}`)}},
		{Role: "tool", ToolCallID: "call_1", Content: "file contents"},
	})
	if instructions != "system instructions" {
		t.Fatalf("instructions mismatch: %q", instructions)
	}
	if len(input) != 3 {
		t.Fatalf("expected user/function_call/function_call_output fixture items, got %#v", input)
	}
	if item, _ := input[1].(map[string]any); item["type"] != "function_call" || item["call_id"] != "call_1" || item["name"] != "Read" {
		t.Fatalf("function_call fixture mismatch: %#v", input[1])
	}
	if item, _ := input[2].(map[string]any); item["type"] != "function_call_output" || item["call_id"] != "call_1" || item["output"] != "file contents" {
		t.Fatalf("function_call_output fixture mismatch: %#v", input[2])
	}
}

func TestTSParityFixtureCodexNativeTools(t *testing.T) {
	tools := appendCodexNativeTools([]map[string]any{{"type": "function", "name": "Read"}}, "gpt-5.3-codex", true, true)
	if !hasResponseTool(tools, "web_search") || !hasResponseTool(tools, "image_generation") {
		t.Fatalf("expected Codex native tools fixture, got %#v", tools)
	}
}

func TestTSParityFixtureAnthropicSystemCacheControl(t *testing.T) {
	system := anthropicSystemBlocks("system instructions")
	blocks, ok := system.([]map[string]any)
	if !ok || len(blocks) != 1 {
		t.Fatalf("expected Anthropic system content block fixture, got %#v", system)
	}
	if blocks[0]["type"] != "text" || blocks[0]["text"] != "system instructions" || blocks[0]["cache_control"] == nil {
		t.Fatalf("Anthropic cache_control fixture mismatch: %#v", blocks[0])
	}
}

func TestTSParityFixtureNkpPrepareRequest(t *testing.T) {
	request := buildKiroChatRequest("claude-sonnet-4.5", ChatParams{History: &BuiltHistory{TrailingUserText: "hi"}})
	prepareNkpRequest(request)
	state, _ := request["conversationState"].(map[string]any)
	workspace, _ := state["workspaceState"].(map[string]any)
	if workspace["sessionEpoch"] == nil || workspace["traceId"] == nil {
		t.Fatalf("expected NKP workspaceState fixture, got %#v", workspace)
	}
}
