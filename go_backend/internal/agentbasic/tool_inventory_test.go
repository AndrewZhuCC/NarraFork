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

func TestToolInventoryGoldenCategoriesAndScopes(t *testing.T) {
	normal := toolDefinitionNames(toolDefinitions())
	for _, name := range []string{"Bash", "Read", "Write", "Edit", "WebSearch", "Agent", "TaskCreate"} {
		if !normal[name] {
			t.Fatalf("normal inventory missing core tool %s", name)
		}
	}
	for _, name := range []string{"Terminal", "Browser", "ShareFile", "ConcludeReview", "DangerConfirm", "ExitPlanConfirm", "GoalCompleteConfirm"} {
		if normal[name] {
			t.Fatalf("normal inventory should not include %s by default", name)
		}
	}

	review := toolDefinitionNames(toolDefinitions("review"))
	if !review["ConcludeReview"] || !review["Read"] {
		t.Fatalf("review inventory should include review and core tools: %#v", review)
	}
	if review["Browser"] {
		t.Fatalf("review inventory should still exclude optional tools unless enabled")
	}

	reflection := toolDefinitionNames(toolDefinitions("reflection"))
	if !reflection["DangerConfirm"] || !reflection["ExitPlanConfirm"] || !reflection["GoalCompleteConfirm"] {
		t.Fatalf("reflection inventory missing reflection tools: %#v", reflection)
	}
	if reflection["Read"] || reflection["ConcludeReview"] {
		t.Fatalf("reflection inventory leaked non-reflection tools: %#v", reflection)
	}
}

func TestToolInventoryEnabledAndDisabledFiltering(t *testing.T) {
	scope := encodedToolScope("normal", []string{"Browser", "ShareFile"}, []string{"Read", "Write"})
	names := toolDefinitionNames(toolDefinitions(scope))
	if !names["Browser"] || !names["ShareFile"] {
		t.Fatalf("enabled optional tools were not exposed: %#v", names)
	}
	if names["Read"] || names["Write"] {
		t.Fatalf("disabled tools leaked into inventory: %#v", names)
	}
	if !names["Bash"] {
		t.Fatalf("unrelated core tool should remain enabled: %#v", names)
	}
}

func TestToolInventoryMarksUnimplementedOptionalToolsUnsupported(t *testing.T) {
	metadata := ToolInventoryMetadataForOptions(ToolInventoryOptions{Scope: ToolScopeNormal})
	unsupported := map[string]bool{}
	for _, tool := range metadata.Tools {
		if tool.Category == ToolCategoryOptional && !tool.Supported {
			unsupported[tool.Name] = tool.Reason != "" && !tool.Enabled
		}
	}
	for _, name := range []string{"Recall", "ForkNarrator", "NarraForkAdmin"} {
		if !unsupported[name] {
			t.Fatalf("missing unsupported optional tool metadata for %s: %#v", name, metadata.Tools)
		}
	}
	menuNames := map[string]bool{}
	for _, item := range ToolMenuCapabilities() {
		menuNames[stringValue(item["name"])] = true
	}
	if !menuNames["Terminal"] {
		t.Fatalf("implemented Terminal optional tool missing from loadable menu: %#v", ToolMenuCapabilities())
	}
	for _, name := range []string{"Recall", "ForkNarrator", "NarraForkAdmin"} {
		if menuNames[name] {
			t.Fatalf("unsupported optional tool leaked into loadable menu: %#v", ToolMenuCapabilities())
		}
	}
}

func TestToolInventoryExternalMCPToolsAreInjectedAndGuarded(t *testing.T) {
	external := []ExternalToolSpec{{
		Name:        "mcp__filesystem__read_file",
		Description: "[MCP: filesystem] read_file",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}},
		Source:      "mcp",
		Metadata:    map[string]any{"serverId": "srv_fs", "originalToolName": "read_file"},
	}}
	opts := ToolInventoryOptions{Scope: ToolScopeNormal, ExternalTools: external, MCPExternalToolInjection: true}
	tools := formatToolsForAdapter(&OpenAIChatCompletionsAdapter{}, opts)
	anyTools := make([]any, len(tools))
	for i, tool := range tools {
		anyTools[i] = tool
	}
	if !providerToolListHas(anyTools, "mcp__filesystem__read_file") {
		t.Fatalf("external MCP tool not injected: %#v", tools)
	}
	metadata := ToolInventoryMetadataForOptions(opts)
	caps, _ := metadata.Capabilities["externalMCPToolInjection"].(map[string]any)
	if caps["supported"] != true || caps["toolCount"] != 1 {
		t.Fatalf("unexpected external MCP metadata: %#v", metadata.Capabilities)
	}
	foundMetadata := false
	for _, capability := range metadata.Tools {
		if capability.Name == "mcp__filesystem__read_file" {
			foundMetadata = capability.Metadata["serverId"] == "srv_fs" && capability.Metadata["originalToolName"] == "read_file"
		}
	}
	if !foundMetadata {
		t.Fatalf("external MCP tool metadata missing server/source details: %#v", metadata.Tools)
	}

	called := false
	executor := guardToolExecutor(func(ctx context.Context, cwd string, call ToolCall) (ToolRun, error) {
		called = true
		return ToolRun{ID: call.ID, Name: call.Function.Name, Arguments: call.Function.Arguments, Output: "ok", Status: "completed"}, nil
	}, opts)
	run, err := executor(context.Background(), t.TempDir(), newFunctionToolCall("tool_1", "mcp__filesystem__read_file", `{}`))
	if err != nil || !called || run.Status != "completed" {
		t.Fatalf("expected injected MCP tool to execute, run=%#v err=%v called=%v", run, err, called)
	}
	called = false
	run, err = executor(context.Background(), t.TempDir(), newFunctionToolCall("tool_2", "mcp__filesystem__write_file", `{}`))
	if err != nil || called || run.Status != "denied" {
		t.Fatalf("expected uninjected MCP tool to be denied, run=%#v err=%v called=%v", run, err, called)
	}
}

func TestToolInventoryMCPUnsupportedMetadata(t *testing.T) {
	metadata := ToolInventoryMetadataForOptions(ToolInventoryOptions{Scope: ToolScopeNormal})
	caps, ok := metadata.Capabilities["externalMCPToolInjection"].(map[string]any)
	if !ok || caps["supported"] != false || !strings.Contains(stringValue(caps["reason"]), "not configured") {
		t.Fatalf("unexpected MCP capability metadata: %#v", metadata.Capabilities)
	}
	foundMCP := false
	for _, tool := range metadata.Tools {
		if tool.Name == "mcp__external_tools" {
			foundMCP = true
			if tool.Supported || tool.Enabled || tool.Category != ToolCategoryMCP {
				t.Fatalf("unexpected MCP tool capability: %#v", tool)
			}
		}
	}
	if !foundMCP {
		t.Fatalf("expected unsupported MCP capability entry: %#v", metadata.Tools)
	}
}

func TestToolMenuCapabilitiesOnlyExposeConfiguredMCPTools(t *testing.T) {
	for _, item := range ToolMenuCapabilities() {
		if stringValue(item["name"]) == "mcp__external_tools" {
			t.Fatalf("unsupported MCP placeholder should not be exposed as a configurable tool: %#v", item)
		}
	}
	external := []ExternalToolSpec{{
		Name:        "mcp__filesystem__read_file",
		Description: "[MCP: filesystem] read_file",
		Source:      "mcp",
		Metadata:    map[string]any{"serverId": "srv_fs", "originalToolName": "read_file"},
	}}
	items := ToolMenuCapabilitiesForOptions(ToolInventoryOptions{ExternalTools: external, MCPExternalToolInjection: true})
	found := false
	for _, item := range items {
		if stringValue(item["name"]) == "mcp__filesystem__read_file" {
			found = item["supported"] == true && item["category"] == ToolCategoryMCP
		}
	}
	if !found {
		t.Fatalf("injected MCP tool should be exposed as configurable: %#v", items)
	}
}

func TestRunAgentLoopEmitsInventoryAndFiltersProviderTools(t *testing.T) {
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected provider path: %s", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&requestBody)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()
	store := testConfigStore(t, map[string]any{
		"openaiProviders": []any{map[string]any{
			"prefix":       "openai",
			"apiKey":       "test-key",
			"baseUrl":      server.URL,
			"defaultModel": "gpt-test",
		}},
	})

	stream, err := RunAgentLoop(context.Background(), store, "openai:gpt-test", []Message{{Role: "user", Content: "hi"}}, t.TempDir(), WithEnabledTools([]string{"Browser"}), WithDisabledTools([]string{"Read"}), WithToolInventoryEvent(true), WithMaxTurns(1))
	if err != nil {
		t.Fatalf("RunAgentLoop returned error: %v", err)
	}
	seenInventory := false
	for event := range stream {
		if event.Type == "tool_inventory" {
			seenInventory = true
			inventory, ok := event.Metadata["inventory"].(ToolInventoryMetadata)
			if !ok || inventory.Scope != ToolScopeNormal {
				t.Fatalf("unexpected inventory event metadata: %#v", event.Metadata)
			}
		}
	}
	if !seenInventory {
		t.Fatalf("expected tool_inventory event")
	}
	tools, _ := requestBody["tools"].([]any)
	if !providerToolListHas(tools, "Browser") || providerToolListHas(tools, "Read") || providerToolListHas(tools, "ConcludeReview") {
		t.Fatalf("provider tools were not filtered by enabled/disabled inventory: %#v", tools)
	}
}

func toolDefinitionNames(tools []map[string]any) map[string]bool {
	out := map[string]bool{}
	for _, tool := range tools {
		fn, _ := tool["function"].(map[string]any)
		if name := stringValue(fn["name"]); name != "" {
			out[name] = true
		}
	}
	return out
}

func providerToolListHas(tools []any, name string) bool {
	for _, item := range tools {
		tool, _ := item.(map[string]any)
		fn, _ := tool["function"].(map[string]any)
		if stringValue(fn["name"]) == name {
			return true
		}
	}
	return false
}
