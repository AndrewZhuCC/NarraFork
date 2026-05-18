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
	for _, name := range []string{"Browser", "ShareFile", "ConcludeReview", "DangerConfirm", "ExitPlanConfirm", "GoalCompleteConfirm"} {
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

func TestToolInventoryMCPUnsupportedMetadata(t *testing.T) {
	metadata := ToolInventoryMetadataForOptions(ToolInventoryOptions{Scope: ToolScopeNormal})
	caps, ok := metadata.Capabilities["externalMCPToolInjection"].(map[string]any)
	if !ok || caps["supported"] != false || !strings.Contains(stringValue(caps["reason"]), "does not execute external MCP tools") {
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
